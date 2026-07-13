/*
Copyright 2022 Nutanix

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/google/uuid"
	clusterModels "github.com/nutanix/ntnx-api-golang-clients/clustermgmt-go-client/v4/models/clustermgmt/v4/config"
	subnetModels "github.com/nutanix/ntnx-api-golang-clients/networking-go-client/v4/models/networking/v4/config"
	prismModels "github.com/nutanix/ntnx-api-golang-clients/prism-go-client/v4/models/prism/v4/config"
	vmmconfig "github.com/nutanix/ntnx-api-golang-clients/vmm-go-client/v4/models/vmm/v4/ahv/config"
	imageModels "github.com/nutanix/ntnx-api-golang-clients/vmm-go-client/v4/models/vmm/v4/content"
	volumesconfig "github.com/nutanix/ntnx-api-golang-clients/volumes-go-client/v4/models/volumes/v4/config"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	v1 "k8s.io/client-go/informers/core/v1"
	"k8s.io/utils/ptr"
	capiv1beta1 "sigs.k8s.io/cluster-api/api/core/v1beta1" //nolint:staticcheck // suppress complaining on Deprecated package
	v1beta1conditions "sigs.k8s.io/cluster-api/util/deprecated/v1beta1/conditions"         //nolint:staticcheck // suppress complaining on Deprecated package
	v1beta2conditions "sigs.k8s.io/cluster-api/util/deprecated/v1beta1/conditions/v1beta2" //nolint:staticcheck // suppress complaining on Deprecated package
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	nutanixclient "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/pkg/client"
	nctx "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/pkg/context"
	"github.com/nutanix-cloud-native/prism-go-client/converged"
	v4Converged "github.com/nutanix-cloud-native/prism-go-client/converged/v4"
	prismclientv3 "github.com/nutanix-cloud-native/prism-go-client/v3"
)

const (
	providerIdPrefix = "nutanix://"

	subnetTypeOverlay = "OVERLAY"

	detachVGRequeueAfter = 30 * time.Second

	ImageStateDeletePending    = "DELETE_PENDING"
	ImageStateDeleteInProgress = "DELETE_IN_PROGRESS"

	createErrorFailureReason  = "CreateError"
	powerOnErrorFailureReason = "PowerOnError"

	CAPXProjectPolicyAnnotation      = "capx.nutanix.com/project-policy"
	CAPXProjectPolicyDefaultOnly     = "default-only"
	CAPXProjectPolicyUnrestricted    = "unrestricted"
	metroFailureDomainPrefix         = "NutanixMetro/"
	metroSiteFailureDomainPrefix     = "NutanixMetroSite/"
	metroNativeFailureDomainLabelKey = "metro.nutanix.com/native-failuredomain"
	metroNativePELabelKey            = "metro.nutanix.com/native-pe"

	// clusterScopeMovementGroupName is the well-known key of the cluster-scope movement group within
	// a NutanixVirtualHADomain's MovementGroups. Only this scope is supported for now; nodepool-scope
	// movement groups are not yet handled.
	clusterScopeMovementGroupName = "default"

	vmCustomAttributePrefix4MetroPreferredPE        = "metro-preferred-pe:"
	vmCustomAttributePrefix4MetroNodeGroupNameLabel = "metro-node-group-name:"
)

type StorageContainerIntentResponse struct {
	Name        *string
	UUID        *string
	ClusterName *string
	ClusterUUID *string
}

// terminalError represents a deterministic, non-retryable error caused by
// invalid user configuration (e.g. referenced resource does not exist).
// It is distinct from converged.APIError which represents HTTP-level failures.
type terminalError struct {
	message string
}

func (e *terminalError) Error() string { return e.message }

func isTerminalError(err error) bool {
	var te *terminalError
	return errors.As(err, &te)
}

func isRetryableAPIError(err error) bool {
	switch {
	case converged.IsNotFound(err), isTerminalError(err):
		return false
	case converged.IsRateLimit(err), converged.IsInternal(err):
		return true
	default:
		// Converged API errors with Kind == nil are parsed HTTP responses that
		// are not expected to succeed on retry (for example, 4xx validation errors).
		// Non-API errors (for example, transport/network timeouts) remain
		// retryable.
		var apiErr *converged.APIError
		if errors.As(err, &apiErr) {
			return false
		}
		return true
	}
}

// DeleteVM deletes a VM and is invoked by the NutanixMachineReconciler
func DeleteVM(ctx context.Context, client *v4Converged.Client, vmName, vmUUID string) (string, error) {
	log := ctrl.LoggerFrom(ctx)
	var err error

	if vmUUID == "" {
		log.V(1).Info("VmUUID was empty. Skipping delete")
		return "", nil
	}

	log.Info(fmt.Sprintf("Deleting VM %s with UUID: %s", vmName, vmUUID))
	task, err := client.VMs.DeleteAsync(ctx, vmUUID)
	if err != nil {
		log.Error(err, fmt.Sprintf("error deleting vm %s", vmName))
		return "", err
	}

	if task == nil {
		log.Error(fmt.Errorf("no task received for vm %s", vmName), "no task received")
		return "", fmt.Errorf("no task received for vm %s", vmName)
	}

	return task.UUID(), nil
}

// FindVMByUUID retrieves the VM with the given vm UUID. Returns nil if not found
func FindVMByUUID(ctx context.Context, client *v4Converged.Client, uuid string) (*vmmconfig.Vm, error) {
	log := ctrl.LoggerFrom(ctx)
	log.V(1).Info(fmt.Sprintf("Checking if VM with UUID %s exists.", uuid))

	response, err := client.VMs.Get(ctx, uuid)
	if err != nil {
		if converged.IsNotFound(err) {
			log.V(1).Info(fmt.Sprintf("vm with uuid %s does not exist.", uuid))
			return nil, nil
		}
		log.Error(err, fmt.Sprintf("Failed to find VM by vmUUID %s", uuid))
		return nil, err
	}

	return response, nil
}

// GenerateProviderID generates a provider ID for the given resource UUID
func GenerateProviderID(uuid string) string {
	return fmt.Sprintf("%s%s", providerIdPrefix, uuid)
}

// GetVMUUID returns the VM's ExtId - the identifier Prism Central assigns to the
// VM and the only value accepted by VMs.Get. It deliberately consults only
// ExtId-namespace sources, in order of reliability:
//
//   - NutanixMachine.Status.VmUUID: the VM's current ExtId as last observed by
//     CAPX.
//   - NutanixMachine.Spec.ProviderID: set once at creation and persisted with
//     the object, so it survives a clusterctl move where status is recreated
//     empty on the target cluster. Its value is the VM's original ExtId, which
//     at creation is identical to the VM's biosUUID; because the CAPI contract
//     requires providerID to be immutable, it stays pinned to that original UUID
//     even after the live ExtId later changes.
//
// It intentionally does NOT consult Machine.Status.NodeInfo.SystemUUID. That
// field is the guest-reported biosUUID, and its relationship to the live ExtId is
// version-dependent, which makes it an unreliable VMs.Get key: after an unplanned
// Prism Element failover PC 7.6 keeps the biosUUID pinned to the original value
// (so it diverges from the new ExtId), while PC 7.5 re-syncs the biosUUID to the
// new ExtId (so it happens to equal the current ExtId). On top of that it is only
// populated once the node has registered, so it is absent during initial
// provisioning. providerID is the better anchor: it is always present once set,
// stable per the CAPI contract, and its value (the original biosUUID) doubles as
// the biosUUID filter during rediscovery (see findVMByStableIdentifier) - so
// SystemUUID is never needed as a direct lookup key here.
func GetVMUUID(nutanixMachine *infrav1.NutanixMachine) (string, error) {
	vmUUID := nutanixMachine.Status.VmUUID
	if vmUUID != "" {
		if _, err := uuid.Parse(vmUUID); err != nil {
			return "", fmt.Errorf("VMUUID was set but was not a valid UUID: %s err: %w", vmUUID, err)
		}
		return vmUUID, nil
	}
	if nutanixMachine.Spec.ProviderID != "" {
		providerUUID := strings.TrimPrefix(nutanixMachine.Spec.ProviderID, providerIdPrefix)
		if _, err := uuid.Parse(providerUUID); err != nil {
			return "", fmt.Errorf("NutanixMachine.Spec.ProviderID was set but did not contain a valid UUID: %s err: %w", nutanixMachine.Spec.ProviderID, err)
		}
		return providerUUID, nil
	}
	return "", nil
}

// FindVM retrieves the VM with the given uuid or name
func FindVM(ctx context.Context, client *v4Converged.Client, nutanixMachine *infrav1.NutanixMachine, vmName string) (*vmmconfig.Vm, error) {
	log := ctrl.LoggerFrom(ctx)
	vmUUID, err := GetVMUUID(nutanixMachine)
	if err != nil {
		return nil, err
	}
	// Search via uuid if it is present
	if vmUUID != "" {
		log.V(1).Info(fmt.Sprintf("Searching for VM %s using UUID %s", vmName, vmUUID))
		vm, err := FindVMByUUID(ctx, client, vmUUID)
		if err != nil {
			return nil, err
		}
		if vm == nil {
			// The last-known ExtId no longer resolves. This is expected when the
			// VM's backing Prism Element cluster failed over and the VM was
			// assigned a new ExtId. Attempt to rediscover the VM by identifiers
			// that survive failover before concluding it is gone.
			log.Info(fmt.Sprintf("VM %s not found by UUID %s; attempting rediscovery by stable identifiers", vmName, vmUUID))
			recovered, rerr := findVMByStableIdentifier(ctx, client, nutanixMachine, vmName)
			if rerr != nil {
				return nil, rerr
			}
			if recovered == nil {
				return nil, fmt.Errorf("no vm %s found with UUID %s but was expected to be present", vmName, vmUUID)
			}
			return recovered, nil
		}
		// Check if the VM name matches the Machine name or the NutanixMachine name.
		// Earlier, we were creating VMs with the same name as the NutanixMachine name.
		// Now, we create VMs with the same name as the Machine name in line with other CAPI providers.
		// This check is to ensure that we are deleting the correct VM for both cases as older CAPX VMs
		// will have the NutanixMachine name as the VM name.
		if *vm.Name != vmName && *vm.Name != nutanixMachine.Name {
			return nil, fmt.Errorf("found VM with UUID %s but name %s did not match %s", vmUUID, *vm.Name, vmName)
		}
		return vm, nil
		// otherwise search via name
	} else {
		log.Info(fmt.Sprintf("Searching for VM %s using name", vmName))
		vm, err := FindVMByName(ctx, client, nutanixMachine, vmName)
		if err != nil {
			log.Error(err, fmt.Sprintf("error occurred finding VM %s by name", vmName))
			return nil, err
		}
		return vm, nil
	}
}

// findVMByStableIdentifier rediscovers a VM whose ExtId has changed - for
// example after an unplanned Prism Element failover - using identifiers that
// survive the failover. The recovery key is Spec.ProviderID's value: the VM's
// original ExtId, which at creation equals the VM's biosUUID and, being an
// immutable providerID, stays pinned to that original UUID. That dual identity
// is what makes the two strategies below work - the same key is both the biosUUID
// (stable on PC 7.6) and the value CAPX stamps into the provider-id custom
// attribute (providerid:<original-ExtId>).
//
// Two strategies are combined to cover both supported PC versions:
//   - PC 7.6+: biosUUID is stable across unplanned failover and is server-side
//     filterable, so it is used as a cheap, precise lookup. The PC version is
//     fetched here because it is not otherwise available on this path.
//   - PC 7.5 (and as a universal fallback): on unplanned failover the biosUUID
//     is re-synced to the new ExtId (both change to the same new value), so the
//     original biosUUID held in recoveryKey no longer matches the VM - a biosUuid
//     filter would miss even if 7.5 offered one. What does survive failover is the
//     VM name and the provider-id custom attribute (providerid:<original ExtId>),
//     so list by name and disambiguate by that custom attribute.
//
// Returns (nil, nil) when no VM could be rediscovered so the caller can decide
// how to proceed (the create-guard in getOrCreateVM ensures this never results
// in a duplicate VM once an identity has been established).
func findVMByStableIdentifier(ctx context.Context, client *v4Converged.Client, nutanixMachine *infrav1.NutanixMachine, vmName string) (*vmmconfig.Vm, error) {
	log := ctrl.LoggerFrom(ctx)

	if nutanixMachine.Spec.ProviderID == "" {
		return nil, nil
	}
	recoveryKey := strings.TrimPrefix(nutanixMachine.Spec.ProviderID, providerIdPrefix)
	if recoveryKey == "" {
		return nil, nil
	}

	// PC 7.6+ exposes a server-side filter on biosUuid, which is stable across
	// unplanned failovers. Since recoveryKey is the original biosUUID and the
	// biosUUID does not change on 7.6, "biosUuid eq recoveryKey" still matches the
	// failed-over VM under its new ExtId. Prefer it when available. A failure to
	// determine the PC version is non-fatal: fall through to the
	// version-independent path.
	if pcVersion, verr := client.DomainManager.GetPrismCentralVersion(ctx); verr != nil {
		log.Error(verr, "failed to get PC version while rediscovering VM; skipping biosUuid lookup")
	} else if isPCVersionHigherThan75(pcVersion) {
		vms, err := client.VMs.List(ctx, converged.WithFilter(fmt.Sprintf("biosUuid eq '%s'", recoveryKey)))
		if err != nil {
			return nil, fmt.Errorf("failed to list VMs by biosUuid %s while rediscovering VM %s: %w", recoveryKey, vmName, err)
		}
		if vm := matchVMByProviderIDCustomAttribute(vms, recoveryKey); vm != nil {
			log.Info(fmt.Sprintf("Rediscovered VM %s by biosUuid %s with new ExtId %s", vmName, recoveryKey, *vm.ExtId))
			return vm, nil
		}
		if len(vms) == 1 {
			log.Info(fmt.Sprintf("Rediscovered VM %s by biosUuid %s with new ExtId %s", vmName, recoveryKey, *vms[0].ExtId))
			return &vms[0], nil
		}
	}

	// Universal fallback (and the only option on PC 7.5): the VM name does not
	// change across failover, so list by name and disambiguate by the
	// provider-id custom attribute which carries the original ExtId.
	vms, err := client.VMs.List(ctx, converged.WithFilter(fmt.Sprintf("name eq '%s'", vmName)))
	if err != nil {
		return nil, fmt.Errorf("failed to list VMs by name %s while rediscovering VM: %w", vmName, err)
	}
	if vm := matchVMByProviderIDCustomAttribute(vms, recoveryKey); vm != nil {
		log.Info(fmt.Sprintf("Rediscovered VM %s by provider-id custom attribute %s with ExtId %s", vmName, recoveryKey, *vm.ExtId))
		return vm, nil
	}

	return nil, nil
}

// matchVMByProviderIDCustomAttribute returns the VM carrying the
// providerid:<recoveryKey> custom attribute, or nil if none match.
func matchVMByProviderIDCustomAttribute(vms []vmmconfig.Vm, recoveryKey string) *vmmconfig.Vm {
	want := vmCustomAttributePrefix4ProviderID + recoveryKey
	for i := range vms {
		if slices.Contains(vms[i].CustomAttributes, want) {
			return &vms[i]
		}
	}
	return nil
}

// CleanPCVersion normalizes a Prism Central version string by trimming whitespace,
// lower-casing it, and removing the optional "pc." prefix.
func CleanPCVersion(version string) string {
	lowerVersion := strings.ToLower(strings.TrimSpace(version))
	return strings.TrimPrefix(lowerVersion, "pc.")
}

func convertStringToIntList(str string) []int {
	strList := strings.Split(str, ".")
	var intList []int
	for _, x := range strList {
		if val, err := strconv.Atoi(x); err != nil {
			return []int{9999}
		} else {
			intList = append(intList, val)
		}
	}
	return intList
}

// CompareVersions compares version numbers of the format '3.5.2.1'.
// Returns 0 : if v1 == v2
// Returns 1 : if v1 > v2
// Returns -1: if v1 < v2
//
// If either version is not in the correct format, they will be the greater,
// unless neither can be parsed in which case they are equal. The case where a
// branch can't be parsed is if the cluster is running master, or some other
// non-release branch, or empty string. This is only expected in a test/debug
// situation and is the motivation for making an unparseable format greater.
func CompareVersions(v1, v2 string) int {
	if strings.EqualFold(v1, "master") {
		v1 = "9999"
	}
	if strings.EqualFold(v2, "master") {
		v2 = "9999"
	}

	v1IntList := convertStringToIntList(v1)
	v2IntList := convertStringToIntList(v2)

	maxLen := max(len(v1IntList), len(v2IntList))

	v1NormIntList := make([]int, maxLen)
	v2NormIntList := make([]int, maxLen)
	copy(v1NormIntList, v1IntList)
	copy(v2NormIntList, v2IntList)

	for i, e := range v1NormIntList {
		if e > v2NormIntList[i] {
			return 1
		} else if e < v2NormIntList[i] {
			return -1
		}
	}
	return 0
}

// isPCVersionHigherThan75 reports whether the given PC version is 7.6 or newer.
// PC 7.6 is the first release where the biosUUID is stable across an unplanned
// failover and is server-side filterable. The version may include a "pc." prefix
// (e.g. "pc.7.6.0.5").
func isPCVersionHigherThan75(version string) bool {
	v := CleanPCVersion(version)
	return CompareVersions(v, "7.6") >= 0
}

// FindVMByName retrieves the VM with the given vm name. When more than one VM
// shares the name - for example because a duplicate was created by an earlier
// idempotency gap - it disambiguates instead of dead-locking: it prefers the VM
// carrying this machine's provider-id custom attribute, and otherwise adopts the
// oldest matching VM (the VM name is unique per CAPI Machine, so all matches
// belong to this machine and the duplicate can then be reaped).
func FindVMByName(ctx context.Context, client *v4Converged.Client, nutanixMachine *infrav1.NutanixMachine, vmName string) (*vmmconfig.Vm, error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info(fmt.Sprintf("Checking if VM with name %s exists.", vmName))

	vms, err := client.VMs.List(ctx, converged.WithFilter(fmt.Sprintf("name eq '%s'", vmName)))
	if err != nil {
		return nil, err
	}

	if len(vms) == 0 {
		return nil, nil
	}

	if len(vms) == 1 {
		return FindVMByUUID(ctx, client, *vms[0].ExtId)
	}

	// More than one VM matched the name. Historically this returned an error,
	// which permanently wedged the machine whenever a duplicate VM existed - the
	// exact situation we want to be able to recover from.
	log.Info(fmt.Sprintf("found %d VMs with name %s; disambiguating instead of failing", len(vms), vmName))

	// Prefer the VM that carries this machine's provider-id custom attribute.
	if nutanixMachine != nil && nutanixMachine.Spec.ProviderID != "" {
		recoveryKey := strings.TrimPrefix(nutanixMachine.Spec.ProviderID, providerIdPrefix)
		if vm := matchVMByProviderIDCustomAttribute(vms, recoveryKey); vm != nil {
			return FindVMByUUID(ctx, client, *vm.ExtId)
		}
	}

	// No identity to match against: adopt the oldest VM deterministically so the
	// machine can make progress; the extra VM(s) can be cleaned up afterwards.
	oldest := oldestVM(vms)
	log.Info(fmt.Sprintf("adopting oldest VM (ExtId %s) among %d name matches for %s", *oldest.ExtId, len(vms), vmName))
	return FindVMByUUID(ctx, client, *oldest.ExtId)
}

// oldestVM returns the VM with the earliest CreateTime. VMs without a CreateTime
// are considered newest so a VM with a known creation time is always preferred.
func oldestVM(vms []vmmconfig.Vm) *vmmconfig.Vm {
	oldest := &vms[0]
	for i := 1; i < len(vms); i++ {
		switch {
		case vms[i].CreateTime == nil:
			continue
		case oldest.CreateTime == nil:
			oldest = &vms[i]
		case vms[i].CreateTime.Before(*oldest.CreateTime):
			oldest = &vms[i]
		}
	}
	return oldest
}

// GetPEUUID returns the UUID of the Prism Element cluster with the given name or UUID.
func GetPEUUID(ctx context.Context, client *v4Converged.Client, peName, peUUID *string) (string, error) {
	peCluster, err := GetPEClusterByIdentifier(ctx, client, peName, peUUID)
	if err != nil {
		return "", err
	}
	return ptr.Deref(peCluster.ExtId, ""), nil
}

// GetPECluster returns the Prism Element cluster with the given UUID.
func GetPECluster(ctx context.Context, client *v4Converged.Client, peUUID string) (*clusterModels.Cluster, error) {
	peCluster, err := client.Clusters.Get(ctx, peUUID)
	if err != nil {
		if converged.IsNotFound(err) {
			return nil, fmt.Errorf("failed to find Prism Element cluster with UUID %s: %w", peUUID, err)
		}
		return nil, fmt.Errorf("failed to get Prism Element cluster with UUID %s: %w", peUUID, err)
	}

	return peCluster, nil
}

// GetPEClusterByIdentifier resolves a Prism Element cluster by UUID or name and returns the full
// cluster object in a single API call. Callers that only need the UUID can use GetPEUUID; callers
// that need the cluster object (for example to read Config.IsAvailable) should prefer this over
// GetPEUUID followed by GetPECluster, which performs a redundant List+Get round trip.
func GetPEClusterByIdentifier(ctx context.Context, client *v4Converged.Client, peName, peUUID *string) (*clusterModels.Cluster, error) {
	if client == nil {
		return nil, fmt.Errorf("cannot retrieve Prism Element cluster if nutanix client is nil")
	}
	if peUUID != nil && *peUUID != "" {
		return GetPECluster(ctx, client, *peUUID)
	}
	if peName != nil && *peName != "" {
		responsePEs, err := client.Clusters.List(ctx, converged.WithFilter(fmt.Sprintf("name eq '%s'", *peName)))
		if err != nil {
			return nil, err
		}
		foundPEs := make([]clusterModels.Cluster, 0)
		for _, s := range responsePEs {
			if strings.EqualFold(*s.Name, *peName) && hasPEClusterServiceEnabled(&s) {
				foundPEs = append(foundPEs, s)
			}
		}
		switch len(foundPEs) {
		case 1:
			return &foundPEs[0], nil
		case 0:
			return nil, &terminalError{message: fmt.Sprintf("failed to retrieve Prism Element cluster by name %s", *peName)}
		default:
			return nil, fmt.Errorf("more than one Prism Element cluster found with name %s", *peName)
		}
	}
	return nil, fmt.Errorf("failed to retrieve Prism Element cluster by name or uuid. Verify input parameters")
}

// IsPEAvailable returns whether the Prism Element cluster with the given UUID is currently available.
func IsPEAvailable(ctx context.Context, client *v4Converged.Client, peUUID string) (bool, error) {
	pe, err := GetPECluster(ctx, client, peUUID)
	if err != nil {
		return false, err
	}
	if pe.Config == nil || pe.Config.IsAvailable == nil {
		return false, nil
	}
	return *pe.Config.IsAvailable, nil
}

// GetMibValueOfQuantity returns the given quantity value in Mib
func GetMibValueOfQuantity(quantity resource.Quantity) int64 {
	return quantity.Value() / (1024 * 1024)
}

func CreateSystemDiskSpec(imageUUID string, systemDiskSizeInBytes int64) (*vmmconfig.Disk, error) {
	if imageUUID == "" {
		return nil, fmt.Errorf("image UUID must be set when creating system disk")
	}
	if systemDiskSizeInBytes <= 0 {
		return nil, fmt.Errorf("invalid system disk size in bytes: %d. Provide in XXGi (for example 70Gi) format instead", systemDiskSizeInBytes)
	}

	disk := vmmconfig.NewDisk()
	err := disk.SetBackingInfo(*newVmDiskWithImageRef(&imageUUID, systemDiskSizeInBytes))
	if err != nil {
		return nil, err
	}

	return disk, nil
}

// CreateDataDiskList creates a list of data disks and cdRoms with the given data disk specs
func CreateDataDiskList(ctx context.Context, convergedClient *v4Converged.Client, dataDiskSpecs []infrav1.NutanixMachineVMDisk, peUUID string) ([]vmmconfig.Disk, []vmmconfig.CdRom, error) {
	dataDisks := []vmmconfig.Disk{}
	dataCdRoms := []vmmconfig.CdRom{}

	latestDeviceIndexByAdapterType := make(map[string]int)
	getDeviceIndex := func(adapterType string) int {
		if latestDeviceIndex, ok := latestDeviceIndexByAdapterType[adapterType]; ok {
			latestDeviceIndexByAdapterType[adapterType] = latestDeviceIndex + 1
			return latestDeviceIndex
		}

		if adapterType == string(infrav1.NutanixMachineDiskAdapterTypeSCSI) || adapterType == string(infrav1.NutanixMachineDiskAdapterTypeIDE) {
			latestDeviceIndexByAdapterType[adapterType] = 1
			return 1
		} else {
			latestDeviceIndexByAdapterType[adapterType] = 0
			return 0
		}
	}

	for _, dataDiskSpec := range dataDiskSpecs {
		vmDisk := vmmconfig.NewVmDisk()
		vmDisk.DiskSizeBytes = ptr.To(int64(dataDiskSpec.DiskSize.Value()))

		err := addDataSourceImageRefToVmDisk(ctx, convergedClient, vmDisk, dataDiskSpec.DataSource)
		if err != nil {
			return nil, nil, err
		}

		err = addStorageConfigAndContainerToVmDisk(ctx, convergedClient, vmDisk, dataDiskSpec.StorageConfig, peUUID)
		if err != nil {
			return nil, nil, err
		}

		// Set default values for device type and adapter type
		deviceType := infrav1.NutanixMachineDiskDeviceTypeDisk
		adapterType := infrav1.NutanixMachineDiskAdapterTypeSCSI

		// If device properties are provided, use them
		if dataDiskSpec.DeviceProperties != nil {
			deviceType = dataDiskSpec.DeviceProperties.DeviceType
			adapterType = dataDiskSpec.DeviceProperties.AdapterType
		}

		deviceIndex := getDeviceIndex(string(adapterType))
		if dataDiskSpec.DeviceProperties != nil && dataDiskSpec.DeviceProperties.DeviceIndex != 0 {
			deviceIndex = int(dataDiskSpec.DeviceProperties.DeviceIndex)
		}

		// Set device properties
		switch deviceType {
		case infrav1.NutanixMachineDiskDeviceTypeDisk:
			disk := vmmconfig.NewDisk()
			disk.DiskAddress = vmmconfig.NewDiskAddress()
			disk.DiskAddress.Index = ptr.To(deviceIndex)
			disk.DiskAddress.BusType = adapterTypeToDiskBusType(adapterType)
			err = disk.SetBackingInfo(*vmDisk)
			if err != nil {
				return nil, nil, err
			}

			dataDisks = append(dataDisks, *disk)
		case infrav1.NutanixMachineDiskDeviceTypeCDRom:
			cdRom := vmmconfig.NewCdRom()
			cdRom.DiskAddress = vmmconfig.NewCdRomAddress()
			cdRom.DiskAddress.Index = ptr.To(deviceIndex)
			cdRom.DiskAddress.BusType = adapterTypeToCdRomBusType(adapterType)
			cdRom.BackingInfo = vmDisk

			dataCdRoms = append(dataCdRoms, *cdRom)
		default:
			return nil, nil, fmt.Errorf("invalid NutanixMachineDiskDeviceType to create data disks")
		}
	}

	return dataDisks, dataCdRoms, nil
}

func addDataSourceImageRefToVmDisk(ctx context.Context, convergedClient *v4Converged.Client, vmDisk *vmmconfig.VmDisk, dataSource *infrav1.NutanixResourceIdentifier) error {
	if dataSource == nil {
		return nil
	}

	image, err := GetImage(ctx, convergedClient, infrav1.NutanixResourceIdentifier{
		UUID: dataSource.UUID,
		Type: infrav1.NutanixIdentifierUUID,
	})
	if err != nil {
		return err
	}

	vmDisk.DataSource = vmmconfig.NewDataSource()
	imageRef := vmmconfig.NewImageReference()
	imageRef.ImageExtId = image.ExtId
	err = vmDisk.DataSource.SetReference(*imageRef)
	if err != nil {
		return err
	}
	vmDisk.DataSource.ReferenceItemDiscriminator_ = nil

	return nil
}

func addStorageConfigAndContainerToVmDisk(ctx context.Context, convergedClient *v4Converged.Client, vmDisk *vmmconfig.VmDisk, storageConfig *infrav1.NutanixMachineVMStorageConfig, peUUID string) error {
	if storageConfig == nil {
		return nil
	}

	vmDisk.StorageConfig = vmmconfig.NewVmDiskStorageConfig()

	flashModeEnabled := storageConfig.DiskMode == infrav1.NutanixMachineDiskModeFlash
	vmDisk.StorageConfig.IsFlashModeEnabled = ptr.To(flashModeEnabled)

	if storageConfig.StorageContainer != nil {
		peID := infrav1.NutanixResourceIdentifier{
			UUID: &peUUID,
			Type: infrav1.NutanixIdentifierUUID,
		}
		sc, err := GetStorageContainerInCluster(ctx, convergedClient, *storageConfig.StorageContainer, peID)
		if err != nil {
			return err
		}

		vmDisk.StorageContainer = vmmconfig.NewVmDiskContainerReference()
		vmDisk.StorageContainer.ExtId = sc.ContainerExtId
	}

	return nil
}

func newVmDiskWithImageRef(dataSourceImageExtId *string, diskSizeInBytes int64) *vmmconfig.VmDisk {
	vmDisk := vmmconfig.NewVmDisk()

	if diskSizeInBytes > 0 {
		vmDisk.DiskSizeBytes = ptr.To(diskSizeInBytes)
	}

	if dataSourceImageExtId != nil {
		vmDisk.DataSource = vmmconfig.NewDataSource()
		imageRef := vmmconfig.NewImageReference()
		imageRef.ImageExtId = dataSourceImageExtId
		_ = vmDisk.DataSource.SetReference(*imageRef)
		vmDisk.DataSource.ReferenceItemDiscriminator_ = nil
	}

	return vmDisk
}

func adapterTypeToDiskBusType(adapterType infrav1.NutanixMachineDiskAdapterType) *vmmconfig.DiskBusType {
	switch adapterType {
	case infrav1.NutanixMachineDiskAdapterTypeSCSI:
		return vmmconfig.DISKBUSTYPE_SCSI.Ref()
	case infrav1.NutanixMachineDiskAdapterTypeIDE:
		return vmmconfig.DISKBUSTYPE_IDE.Ref()
	case infrav1.NutanixMachineDiskAdapterTypePCI:
		return vmmconfig.DISKBUSTYPE_PCI.Ref()
	case infrav1.NutanixMachineDiskAdapterTypeSATA:
		return vmmconfig.DISKBUSTYPE_SATA.Ref()
	default:
		return vmmconfig.DISKBUSTYPE_UNKNOWN.Ref()
	}
}

func adapterTypeToCdRomBusType(adapterType infrav1.NutanixMachineDiskAdapterType) *vmmconfig.CdRomBusType {
	switch adapterType {
	case infrav1.NutanixMachineDiskAdapterTypeIDE:
		return vmmconfig.CDROMBUSTYPE_IDE.Ref()
	case infrav1.NutanixMachineDiskAdapterTypeSATA:
		return vmmconfig.CDROMBUSTYPE_SATA.Ref()
	default:
		return vmmconfig.CDROMBUSTYPE_UNKNOWN.Ref()
	}
}

// subnetBelongsToCluster checks if a subnet belongs to the specified PE cluster.
// It checks both ClusterReference (single UUID) and ClusterReferenceList (list of UUIDs).
// According to the networking team, non-overlay subnets may have:
// - Both ClusterReference and ClusterReferenceList present (most cases): clusterReference will be deprecated in the future
// - Only ClusterReference present: Old AOS clusters (i.e. <=7.0) that don't support ClusterReferenceList on basic vlan subnets
// - Only ClusterReferenceList present: subnets backed by PC based vSwitches (i.e. >=7.3)
func subnetBelongsToCluster(subnet *subnetModels.Subnet, peUUID string) bool {
	// Check ClusterReference field
	if subnet.ClusterReference != nil && *subnet.ClusterReference == peUUID {
		return true
	}

	// Check ClusterReferenceList field
	if subnet.ClusterReferenceList != nil && slices.Contains(subnet.ClusterReferenceList, peUUID) {
		return true
	}

	return false
}

// GetSubnetUUID returns the UUID of the subnet with the given name
func GetSubnetUUID(ctx context.Context, client *v4Converged.Client, peUUID string, subnetName, subnetUUID *string) (string, error) {
	var foundSubnetUUID string
	if subnetUUID == nil && subnetName == nil {
		return "", fmt.Errorf("subnet name or subnet uuid must be passed in order to retrieve the subnet")
	}
	if subnetUUID != nil {
		subnetIntentResponse, err := client.Subnets.Get(ctx, *subnetUUID)
		if err != nil {
			if converged.IsNotFound(err) {
				return "", fmt.Errorf("failed to find subnet with UUID %s: %w", *subnetUUID, err)
			}
			return "", fmt.Errorf("failed to get subnet with UUID %s: %w", *subnetUUID, err)
		}
		foundSubnetUUID = *subnetIntentResponse.ExtId
	} else { // else search by name
		// Not using additional filtering since we want to list overlay and vlan subnets
		responseSubnets, err := client.Subnets.List(ctx, converged.WithFilter(fmt.Sprintf("name eq '%s'", *subnetName)))
		if err != nil {
			return "", err
		}
		// Validate filtered Subnets
		foundSubnets := make([]subnetModels.Subnet, 0)
		for _, subnet := range responseSubnets {
			if subnet.Name == nil || subnet.SubnetType == nil {
				continue
			}
			if *subnet.Name == *subnetName {
				if subnet.SubnetType.GetName() == subnetTypeOverlay {
					foundSubnets = append(foundSubnets, subnet)
					continue
				}

				// Check if subnet belongs to the PE cluster via ClusterReference or ClusterReferenceList
				if subnetBelongsToCluster(&subnet, peUUID) {
					foundSubnets = append(foundSubnets, subnet)
				}
			}
		}

		if len(foundSubnets) == 0 {
			return "", &terminalError{message: fmt.Sprintf("failed to retrieve subnet by name %s", *subnetName)}
		} else if len(foundSubnets) > 1 {
			return "", fmt.Errorf("more than one subnet found with name %s", *subnetName)
		} else {
			foundSubnetUUID = *foundSubnets[0].ExtId
		}
		if foundSubnetUUID == "" {
			return "", fmt.Errorf("failed to retrieve subnet by name or uuid. Verify input parameters")
		}
	}
	return foundSubnetUUID, nil
}

// GetImage returns an image. If no UUID is provided, returns the unique image with the name.
// Returns an error if no image has the UUID, if no image has the name, or more than one image has the name.
func GetImage(ctx context.Context, client *v4Converged.Client, id infrav1.NutanixResourceIdentifier) (*imageModels.Image, error) {
	switch {
	case id.IsUUID():
		resp, err := client.Images.Get(ctx, *id.UUID)
		if err != nil {
			if converged.IsNotFound(err) {
				return nil, fmt.Errorf("failed to find image with UUID %s: %w", *id.UUID, err)
			}
			return nil, fmt.Errorf("failed to get image with UUID %s: %w", *id.UUID, err)
		}
		return resp, nil
	case id.IsName():
		responseImages, err := client.Images.List(ctx, converged.WithFilter(fmt.Sprintf("name eq '%s'", *id.Name)))
		if err != nil {
			return nil, err
		}
		// Validate filtered Images
		foundImages := make([]*imageModels.Image, 0)
		for _, image := range responseImages {
			if strings.EqualFold(*image.Name, *id.Name) {
				foundImages = append(foundImages, &image)
			}
		}
		if len(foundImages) == 0 {
			return nil, &terminalError{message: fmt.Sprintf("found no image with name %s", *id.Name)}
		} else if len(foundImages) > 1 {
			return nil, fmt.Errorf("more than one image found with name %s", *id.Name)
		} else {
			return foundImages[0], nil
		}
	default:
		return nil, fmt.Errorf("image identifier is missing both name and uuid")
	}
}

type ImageLookup struct {
	BaseOS     string
	K8sVersion string
}

func GetImageByLookup(
	ctx context.Context,
	client *v4Converged.Client,
	imageTemplate,
	imageLookupBaseOS,
	k8sVersion *string,
) (*imageModels.Image, error) {
	if strings.Contains(*k8sVersion, "v") {
		k8sVersion = ptr.To(strings.Replace(*k8sVersion, "v", "", 1))
	}
	params := ImageLookup{*imageLookupBaseOS, *k8sVersion}
	t, err := template.New("k8sTemplate").Parse(*imageTemplate)
	if err != nil {
		return nil, fmt.Errorf("failed to parse template given %s: %w", *imageTemplate, err)
	}
	var templateBytes bytes.Buffer
	err = t.Execute(&templateBytes, params)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to substitute string %s with params %v error: %w",
			*imageTemplate,
			params,
			err,
		)
	}
	responseImages, err := client.Images.List(ctx)
	if err != nil {
		return nil, err
	}
	re := regexp.MustCompile(templateBytes.String())
	foundImages := make([]*imageModels.Image, 0)
	for _, image := range responseImages {
		if re.Match([]byte(*image.Name)) {
			foundImages = append(foundImages, &image)
		}
	}
	sorted := sortImagesByLatestCreationTime(foundImages)
	if len(sorted) == 0 {
		return nil, &terminalError{message: fmt.Sprintf("failed to find image with filter %s", templateBytes.String())}
	}
	return sorted[0], nil
}

// returns the images with the latest creation time first.
func sortImagesByLatestCreationTime(
	images []*imageModels.Image,
) []*imageModels.Image {
	sort.Slice(images, func(i, j int) bool {
		if images[i].CreateTime == nil || images[j].CreateTime == nil {
			return images[i].CreateTime != nil
		}
		timeI := *images[i].CreateTime
		timeJ := *images[j].CreateTime
		return timeI.After(timeJ)
	})
	return images
}

func ImageMarkedForDeletion(ctx context.Context, client *v4Converged.Client, image *imageModels.Image) (bool, error) {
	// Get tasks for the image
	fmtString := "entitiesAffected/any(a:a/extId eq '%s') " +
		"and (status eq Prism.Config.TaskStatus'RUNNING' or status eq Prism.Config.TaskStatus'QUEUED') " +
		"and (operation eq 'kImageDelete')"
	tasks, err := client.Tasks.List(ctx, converged.WithFilter(fmt.Sprintf(fmtString, *image.ExtId)))
	if err != nil {
		return false, err
	}
	return len(tasks) > 0, nil
}

func VmHasTaskInProgress(ctx context.Context, client *v4Converged.Client, vmExtId string) (bool, error) {
	log := ctrl.LoggerFrom(ctx)
	if vmExtId == "" {
		return false, fmt.Errorf("cannot extract task uuid for empty vm extId")
	}

	log.V(1).Info(fmt.Sprintf("Getting task uuid for vm %s", vmExtId))
	fmtString := "entitiesAffected/any(a:a/extId eq '%s') " +
		"and (status eq Prism.Config.TaskStatus'RUNNING' or status eq Prism.Config.TaskStatus'QUEUED')"
	tasks, err := client.Tasks.List(ctx, converged.WithFilter(fmt.Sprintf(fmtString, vmExtId)))
	if err != nil {
		return false, err
	}

	runningTasks := make([]*prismModels.Task, 0)
	runningTasksUUIDs := ""
	queuedTasks := make([]*prismModels.Task, 0)
	queuedTasksUUIDs := ""
	for _, task := range tasks {
		if task.Status != nil && task.ExtId != nil {
			switch *task.Status {
			case prismModels.TASKSTATUS_RUNNING:
				runningTasks = append(runningTasks, &task)
				runningTasksUUIDs = fmt.Sprintf("%s,%s", runningTasksUUIDs, *task.ExtId)
			case prismModels.TASKSTATUS_QUEUED:
				queuedTasks = append(queuedTasks, &task)
				queuedTasksUUIDs = fmt.Sprintf("%s,%s", queuedTasksUUIDs, *task.ExtId)
			default:
				continue
			}
		}
	}
	log.V(1).Info(fmt.Sprintf("Found %d running tasks for vm: %s, UUIDs: [%s]", len(runningTasks), vmExtId, runningTasksUUIDs))
	log.V(1).Info(fmt.Sprintf("Found %d queued tasks for vm: %s, UUIDs: [%s]", len(queuedTasks), vmExtId, queuedTasksUUIDs))
	return len(runningTasks) > 0 || len(queuedTasks) > 0, nil
}

// GetSubnetUUIDList returns a list of subnet UUIDs for the given list of subnet names
func GetSubnetUUIDList(ctx context.Context, client *v4Converged.Client, machineSubnets []infrav1.NutanixResourceIdentifier, peUUID string) ([]string, error) {
	subnetUUIDs := make([]string, 0)
	for _, machineSubnet := range machineSubnets {
		subnetUUID, err := GetSubnetUUID(
			ctx,
			client,
			peUUID,
			machineSubnet.Name,
			machineSubnet.UUID,
		)
		if err != nil {
			return subnetUUIDs, err
		}
		subnetUUIDs = append(subnetUUIDs, subnetUUID)
	}
	return subnetUUIDs, nil
}

// GetDefaultCAPICategoryIdentifiers returns the default CAPI category identifiers
func GetDefaultCAPICategoryIdentifiers(clusterName string) []*infrav1.NutanixCategoryIdentifier {
	return []*infrav1.NutanixCategoryIdentifier{
		{
			Key:   infrav1.DefaultCAPICategoryKeyForName,
			Value: clusterName,
		},
	}
}

// GetObsoleteDefaultCAPICategoryIdentifiers returns the default CAPI category identifiers
func GetObsoleteDefaultCAPICategoryIdentifiers(clusterName string) []*infrav1.NutanixCategoryIdentifier {
	return []*infrav1.NutanixCategoryIdentifier{
		{
			Key:   fmt.Sprintf("%s%s", infrav1.ObsoleteDefaultCAPICategoryPrefix, clusterName),
			Value: infrav1.ObsoleteDefaultCAPICategoryOwnedValue,
		},
	}
}

// GetOrCreateCategories returns the list of category UUIDs for the given list of category names
func GetOrCreateCategories(ctx context.Context, client *v4Converged.Client, categoryIdentifiers []*infrav1.NutanixCategoryIdentifier) ([]*prismModels.Category, error) {
	categories := make([]*prismModels.Category, 0)
	for _, ci := range categoryIdentifiers {
		if ci == nil {
			return categories, fmt.Errorf("cannot get or create nil category")
		}
		category, err := getOrCreateCategory(ctx, client, ci)
		if err != nil {
			return categories, err
		}
		categories = append(categories, category)
	}
	return categories, nil
}

func getCategory(ctx context.Context, client *v4Converged.Client, key, value string) (*prismModels.Category, error) {
	categories, err := client.Categories.List(ctx, converged.WithFilter(fmt.Sprintf("key eq '%s' and value eq '%s'", key, value)))
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve category value %s in category %s. error: %w", value, key, err)
	}
	if len(categories) == 0 {
		return nil, nil
	}
	return &categories[0], nil
}

func deleteCategoryKeyValues(ctx context.Context, client *v4Converged.Client, categoryIdentifiers []*infrav1.NutanixCategoryIdentifier) error {
	log := ctrl.LoggerFrom(ctx)
	groupCategoriesByKey := make(map[string][]string, 0)
	for _, ci := range categoryIdentifiers {
		ciKey := ci.Key
		ciValue := ci.Value
		if gck, ok := groupCategoriesByKey[ciKey]; ok {
			groupCategoriesByKey[ciKey] = append(gck, ciValue)
			continue
		}

		groupCategoriesByKey[ciKey] = []string{ciValue}
	}

	for key, values := range groupCategoriesByKey {
		for _, value := range values {
			prismCategory, err := getCategory(ctx, client, key, value)
			if err != nil {
				errorMsg := fmt.Errorf("failed to retrieve category value %s in category %s. error: %w", value, key, err)
				log.Error(errorMsg, "failed to retrieve category value")
				return errorMsg
			}
			if prismCategory == nil {
				log.V(1).Info(fmt.Sprintf("Category with value %s in category %s not found. Already deleted?", value, key))
				continue
			}

			err = client.Categories.Delete(ctx, *prismCategory.ExtId)
			if err != nil {
				errorMsg := fmt.Errorf("failed to delete category value with key:value %s:%s. error: %w", key, value, err)
				log.Error(errorMsg, "failed to delete category value")
				// NCN-101935: If the category value still has VMs assigned, do not delete the category key:value
				// TODO:deepakmntnx Add a check for specific error mentioned in NCN-101935
				return nil
			}
		}
	}
	return nil
}

// DeleteCategories deletes the given list of categories
func DeleteCategories(ctx context.Context, clientV4 *v4Converged.Client, categoryIdentifiers, obsoleteCategoryIdentifiers []*infrav1.NutanixCategoryIdentifier) error {
	// Dont delete keys with newer format as key is constant string
	err := deleteCategoryKeyValues(ctx, clientV4, categoryIdentifiers)
	if err != nil {
		return err
	}
	// Delete obsolete keys with older format to cleanup brownfield setups
	err = deleteCategoryKeyValues(ctx, clientV4, obsoleteCategoryIdentifiers)
	if err != nil {
		return err
	}

	return nil
}

func getOrCreateCategory(ctx context.Context, client *v4Converged.Client, categoryIdentifier *infrav1.NutanixCategoryIdentifier) (*prismModels.Category, error) {
	log := ctrl.LoggerFrom(ctx)
	if categoryIdentifier == nil {
		return nil, fmt.Errorf("category identifier cannot be nil when getting or creating categories")
	}
	if categoryIdentifier.Key == "" {
		return nil, fmt.Errorf("category identifier key must be set when when getting or creating categories")
	}
	if categoryIdentifier.Value == "" {
		return nil, fmt.Errorf("category identifier key must be set when when getting or creating categories")
	}
	log.V(1).Info(fmt.Sprintf("Checking existence of category with key %s and value %s", categoryIdentifier.Key, categoryIdentifier.Value))
	prismCategory, err := getCategory(ctx, client, categoryIdentifier.Key, categoryIdentifier.Value)
	if err != nil {
		errorMsg := fmt.Errorf("failed to retrieve category with key %s. error: %w", categoryIdentifier.Key, err)
		log.Error(errorMsg, "failed to retrieve category")
		return nil, errorMsg
	}
	if prismCategory == nil {
		log.V(1).Info(fmt.Sprintf("Category with key %s and value %s did not exist.", categoryIdentifier.Key, categoryIdentifier.Value))
		prismCategory, err = client.Categories.Create(ctx, &prismModels.Category{
			Key:         ptr.To(categoryIdentifier.Key),
			Description: ptr.To(infrav1.DefaultCAPICategoryDescription),
			Value:       ptr.To(categoryIdentifier.Value),
		})
		if err != nil {
			errorMsg := fmt.Errorf("failed to create category with key %s and value %s. error: %w", categoryIdentifier.Key, categoryIdentifier.Value, err)
			log.Error(errorMsg, "failed to create category")
			return nil, errorMsg
		}
	}
	return prismCategory, nil
}

func GetPrismReferencesOfCategoryIdentifiers(
	ctx context.Context,
	client *v4Converged.Client,
	categoryIdentifiers []*infrav1.NutanixCategoryIdentifier,
) ([]vmmconfig.CategoryReference, error) {
	log := ctrl.LoggerFrom(ctx)
	categoryExtIds := []string{}

	for _, ci := range categoryIdentifiers {
		if ci == nil {
			return nil, fmt.Errorf("category identifier cannot be nil")
		}
		prismCategory, err := getCategory(ctx, client, ci.Key, ci.Value)
		if err != nil {
			errorMsg := fmt.Errorf("error occurred while to retrieving category value %s in category %s. error: %w", ci.Value, ci.Key, err)
			log.Error(errorMsg, "failed to retrieve category")
			return nil, errorMsg
		}
		if prismCategory == nil || prismCategory.ExtId == nil {
			errorMsg := &terminalError{message: fmt.Sprintf("category value %s not found in category %s", ci.Value, ci.Key)}
			log.Error(errorMsg, "category value not found")
			return nil, errorMsg
		}

		if !slices.Contains(categoryExtIds, *prismCategory.ExtId) {
			categoryExtIds = append(categoryExtIds, *prismCategory.ExtId)
		}
	}

	categoryReferences := []vmmconfig.CategoryReference{}
	for _, extId := range categoryExtIds {
		ref := vmmconfig.NewCategoryReference()
		ref.ExtId = ptr.To(extId)
		categoryReferences = append(categoryReferences, *ref)
	}

	return categoryReferences, nil
}

// GetProjectUUID returns the UUID of the project with the given name
func GetProjectUUID(ctx context.Context, client *prismclientv3.Client, projectName, projectUUID *string) (string, error) {
	var foundProjectUUID string
	if projectUUID == nil && projectName == nil {
		return "", fmt.Errorf("name or uuid must be passed in order to retrieve the project")
	}
	if projectUUID != nil {
		projectIntentResponse, err := client.V3.GetProject(ctx, *projectUUID)
		if err != nil {
			if strings.Contains(fmt.Sprint(err), "ENTITY_NOT_FOUND") {
				return "", &terminalError{message: fmt.Sprintf("failed to find project with UUID %s: %v", *projectUUID, err)}
			}
			return "", fmt.Errorf("failed to get project with UUID %s: %w", *projectUUID, err)
		}
		foundProjectUUID = *projectIntentResponse.Metadata.UUID
	} else { // else search by name
		responseProjects, err := client.V3.ListAllProject(ctx, "")
		if err != nil {
			return "", err
		}
		foundProjects := make([]*prismclientv3.Project, 0)
		for _, s := range responseProjects.Entities {
			projectSpec := s.Spec
			if strings.EqualFold(projectSpec.Name, *projectName) {
				foundProjects = append(foundProjects, s)
			}
		}
		if len(foundProjects) == 0 {
			return "", &terminalError{message: fmt.Sprintf("failed to retrieve project by name %s", *projectName)}
		} else if len(foundProjects) > 1 {
			return "", fmt.Errorf("more than one project found with name %s", *projectName)
		} else {
			foundProjectUUID = *foundProjects[0].Metadata.UUID
		}
		if foundProjectUUID == "" {
			return "", fmt.Errorf("failed to retrieve project by name or uuid. Verify input parameters")
		}
	}
	return foundProjectUUID, nil
}

func hasPEClusterServiceEnabled(peCluster *clusterModels.Cluster) bool {
	if peCluster.Config == nil ||
		peCluster.Config.ClusterFunction == nil {
		return false
	}
	serviceList := peCluster.Config.ClusterFunction
	for _, s := range serviceList {
		if strings.ToUpper(string(s.GetName())) == clusterModels.CLUSTERFUNCTIONREF_AOS.GetName() {
			return true
		}
	}
	return false
}

// GetGPUList returns a list of GPU device IDs for the given list of GPUs
func GetGPUList(ctx context.Context, client *v4Converged.Client, gpus []infrav1.NutanixGPU, peUUID string) ([]vmmconfig.Gpu, error) {
	resultGPUs := make([]vmmconfig.Gpu, 0)
	for _, gpu := range gpus {
		foundGPU, err := GetGPU(ctx, client, peUUID, gpu)
		if err != nil {
			return nil, err
		}
		resultGPUs = append(resultGPUs, *foundGPU)
	}
	return resultGPUs, nil
}

// GetGPUDeviceID returns the device ID of a GPU with the given name
func GetGPU(ctx context.Context, client *v4Converged.Client, peUUID string, gpu infrav1.NutanixGPU) (*vmmconfig.Gpu, error) {
	gpuDeviceID := gpu.DeviceID
	gpuDeviceName := gpu.Name
	if gpuDeviceID == nil && gpuDeviceName == nil {
		return nil, fmt.Errorf("gpu name or gpu device ID must be passed in order to retrieve the GPU")
	}

	allUnusedGPUs, err := GetGPUsForPE(ctx, client, peUUID, gpu)
	if err != nil {
		return nil, err
	}
	if len(allUnusedGPUs) == 0 {
		return nil, &terminalError{message: fmt.Sprintf("no available GPUs found in Prism Element cluster with UUID %s", peUUID)}
	}

	randomIndex := rand.Intn(len(allUnusedGPUs))
	return allUnusedGPUs[randomIndex], nil
}

func GetGPUsForPE(ctx context.Context, client *v4Converged.Client, peUUID string, gpu infrav1.NutanixGPU) ([]*vmmconfig.Gpu, error) {
	var filter string
	var gpus []*vmmconfig.Gpu

	if gpu.DeviceID != nil {
		filter = fmt.Sprintf("physicalGpuConfig/deviceId eq %d", *gpu.DeviceID)
	} else if gpu.Name != nil {
		filter = fmt.Sprintf("physicalGpuConfig/deviceName eq '%s'", *gpu.Name)
	}

	physicalGPUs, err := client.Clusters.ListClusterPhysicalGPUs(ctx, peUUID, converged.WithFilter(filter))
	if err != nil {
		return nil, err
	}
	for _, physicalGPU := range physicalGPUs {
		if physicalGPU.PhysicalGpuConfig.IsInUse != nil && *physicalGPU.PhysicalGpuConfig.IsInUse {
			continue
		}

		vmGpu := vmmconfig.NewGpu()
		vmGpu.Name = physicalGPU.PhysicalGpuConfig.DeviceName
		vmGpu.DeviceId = ptr.To(int(*physicalGPU.PhysicalGpuConfig.DeviceId))
		vmGpu.Mode = vmmconfig.GPUMODE_PASSTHROUGH_COMPUTE.Ref()
		if physicalGPU.PhysicalGpuConfig.Type != nil && *physicalGPU.PhysicalGpuConfig.Type == clusterModels.GPUTYPE_PASSTHROUGH_GRAPHICS {
			vmGpu.Mode = vmmconfig.GPUMODE_PASSTHROUGH_GRAPHICS.Ref()
		}
		vmGpu.Vendor = gpuVendorStringToGpuVendor(*physicalGPU.PhysicalGpuConfig.VendorName)
		gpus = append(gpus, vmGpu)
	}

	if gpu.Name != nil {
		filter = fmt.Sprintf("virtualGpuConfig/deviceName eq '%s'", *gpu.Name)
	} else if gpu.DeviceID != nil {
		filter = fmt.Sprintf("virtualGpuConfig/deviceId eq %d", *gpu.DeviceID)
	}

	virtualGPUs, err := client.Clusters.ListClusterVirtualGPUs(ctx, peUUID, converged.WithFilter(filter))
	if err != nil {
		return nil, err
	}
	for _, virtualGPU := range virtualGPUs {
		if virtualGPU.VirtualGpuConfig.IsInUse != nil && *virtualGPU.VirtualGpuConfig.IsInUse {
			continue
		}

		vmGpu := vmmconfig.NewGpu()
		vmGpu.Name = virtualGPU.VirtualGpuConfig.DeviceName
		vmGpu.DeviceId = ptr.To(int(*virtualGPU.VirtualGpuConfig.DeviceId))
		vmGpu.Mode = vmmconfig.GPUMODE_VIRTUAL.Ref()
		vmGpu.Vendor = gpuVendorStringToGpuVendor(*virtualGPU.VirtualGpuConfig.VendorName)
		gpus = append(gpus, vmGpu)
	}
	return gpus, nil
}

func gpuVendorStringToGpuVendor(vendor string) *vmmconfig.GpuVendor {
	switch vendor {
	case "kNvidia":
		return vmmconfig.GPUVENDOR_NVIDIA.Ref()
	case "kIntel":
		return vmmconfig.GPUVENDOR_INTEL.Ref()
	case "kAmd":
		return vmmconfig.GPUVENDOR_AMD.Ref()
	default:
		return vmmconfig.GPUVENDOR_UNKNOWN.Ref()
	}
}

// GetLegacyFailureDomainFromNutanixCluster gets the failure domain with a given name from a NutanixCluster object.
func GetLegacyFailureDomainFromNutanixCluster(failureDomainName string, nutanixCluster *infrav1.NutanixCluster) *infrav1.NutanixFailureDomainConfig { //nolint:staticcheck // suppress complaining on Deprecated type
	for _, fd := range nutanixCluster.Spec.FailureDomains { //nolint:staticcheck // suppress complaining on Deprecated field
		if fd.Name == failureDomainName {
			return &fd
		}
	}
	return nil
}

func GetStorageContainerInCluster(ctx context.Context, client *v4Converged.Client, storageContainerIdentifier, clusterIdentifier infrav1.NutanixResourceIdentifier) (*clusterModels.StorageContainer, error) {
	var filter, identifier string
	switch {
	case storageContainerIdentifier.IsUUID():
		identifier = *storageContainerIdentifier.UUID
		filter = fmt.Sprintf("containerExtId eq '%s'", identifier)
	case storageContainerIdentifier.IsName():
		identifier = *storageContainerIdentifier.Name
		filter = fmt.Sprintf("name eq '%s'", identifier)
	default:
		return nil, fmt.Errorf("storage container identifier is missing both name and uuid")
	}

	switch {
	case clusterIdentifier.IsUUID():
		filter = fmt.Sprintf("%s and clusterExtId eq '%s'", filter, *clusterIdentifier.UUID)
	case clusterIdentifier.IsName():
		filter = fmt.Sprintf("%s and clusterName eq '%s'", filter, *clusterIdentifier.Name)
	default:
		return nil, fmt.Errorf("cluster identifier is missing both name and uuid")
	}

	storageContainers, err := client.StorageContainers.List(ctx, converged.WithFilter(filter))
	if err != nil {
		return nil, err
	}

	if len(storageContainers) == 0 {
		return nil, &terminalError{message: fmt.Sprintf("found no storage container using filter: %s", filter)}
	}

	return &storageContainers[0], nil
}

func getPrismCentralClientForCluster(ctx context.Context, cluster *infrav1.NutanixCluster, secretInformer v1.SecretInformer, mapInformer v1.ConfigMapInformer) (*prismclientv3.Client, error) {
	log := ctrl.LoggerFrom(ctx)

	log.V(1).Info("Get client helper")
	clientHelper := nutanixclient.NewHelper(secretInformer, mapInformer)

	log.V(1).Info("Build management endpoint")
	managementEndpoint, err := clientHelper.BuildManagementEndpoint(ctx, cluster)
	if err != nil {
		log.Error(err, fmt.Sprintf("error occurred while getting management endpoint for cluster %q", cluster.GetNamespacedName()))
		v1beta1conditions.MarkFalse(cluster, infrav1.PrismCentralClientCondition, infrav1.PrismCentralClientInitializationFailed, capiv1beta1.ConditionSeverityError, "%s", err.Error())
		v1beta2conditions.Set(cluster, metav1.Condition{
			Type:    string(infrav1.PrismCentralClientCondition),
			Status:  metav1.ConditionFalse,
			Reason:  infrav1.PrismCentralClientInitializationFailed,
			Message: err.Error(),
		})
		return nil, err
	}

	log.V(1).Info("Get or create prism central client v3")
	v3Client, err := nutanixclient.NutanixClientCache.GetOrCreate(&nutanixclient.CacheParams{
		NutanixCluster:          cluster,
		PrismManagementEndpoint: managementEndpoint,
	})
	if err != nil {
		log.Error(err, "error occurred while getting nutanix prism v3 Client from cache")
		v1beta1conditions.MarkFalse(cluster, infrav1.PrismCentralClientCondition, infrav1.PrismCentralClientInitializationFailed, capiv1beta1.ConditionSeverityError, "%s", err.Error())
		v1beta2conditions.Set(cluster, metav1.Condition{
			Type:    string(infrav1.PrismCentralClientCondition),
			Status:  metav1.ConditionFalse,
			Reason:  infrav1.PrismCentralClientInitializationFailed,
			Message: err.Error(),
		})
		return nil, fmt.Errorf("nutanix prism v3 Client error: %w", err)
	}

	v1beta1conditions.MarkTrue(cluster, infrav1.PrismCentralClientCondition)
	v1beta2conditions.Set(cluster, metav1.Condition{
		Type:   string(infrav1.PrismCentralClientCondition),
		Status: metav1.ConditionTrue,
		Reason: infrav1.Succeeded,
	})
	return v3Client, nil
}

func getPrismCentralConvergedV4ClientForCluster(ctx context.Context, cluster *infrav1.NutanixCluster, secretInformer v1.SecretInformer, mapInformer v1.ConfigMapInformer) (*v4Converged.Client, error) {
	log := ctrl.LoggerFrom(ctx)

	clientHelper := nutanixclient.NewHelper(secretInformer, mapInformer)
	managementEndpoint, err := clientHelper.BuildManagementEndpoint(ctx, cluster)
	if err != nil {
		log.Error(err, fmt.Sprintf("error occurred while getting management endpoint for cluster %q", cluster.GetNamespacedName()))
		v1beta1conditions.MarkFalse(cluster, infrav1.PrismCentralConvergedV4ClientCondition, infrav1.PrismCentralConvergedV4ClientInitializationFailed, capiv1beta1.ConditionSeverityError, "%s", err.Error())
		v1beta2conditions.Set(cluster, metav1.Condition{
			Type:    string(infrav1.PrismCentralConvergedV4ClientCondition),
			Status:  metav1.ConditionFalse,
			Reason:  infrav1.PrismCentralConvergedV4ClientInitializationFailed,
			Message: err.Error(),
		})
		return nil, err
	}

	client, err := nutanixclient.NutanixConvergedClientV4Cache.GetOrCreate(&nutanixclient.CacheParams{
		NutanixCluster:          cluster,
		PrismManagementEndpoint: managementEndpoint,
	})
	if err != nil {
		log.Error(err, "error occurred while getting nutanix prism converged v4 client from cache")
		v1beta1conditions.MarkFalse(cluster, infrav1.PrismCentralConvergedV4ClientCondition, infrav1.PrismCentralConvergedV4ClientInitializationFailed, capiv1beta1.ConditionSeverityError, "%s", err.Error())
		v1beta2conditions.Set(cluster, metav1.Condition{
			Type:    string(infrav1.PrismCentralConvergedV4ClientCondition),
			Status:  metav1.ConditionFalse,
			Reason:  infrav1.PrismCentralConvergedV4ClientInitializationFailed,
			Message: err.Error(),
		})
		return nil, fmt.Errorf("nutanix prism converged v4 client error: %w", err)
	}

	v1beta1conditions.MarkTrue(cluster, infrav1.PrismCentralConvergedV4ClientCondition)
	v1beta2conditions.Set(cluster, metav1.Condition{
		Type:   string(infrav1.PrismCentralConvergedV4ClientCondition),
		Status: metav1.ConditionTrue,
		Reason: infrav1.Succeeded,
	})
	return client, nil
}

func isBackedByVolumeGroupReference(disk *vmmconfig.Disk) bool {
	if disk == nil {
		return false
	}
	backingInfo := disk.GetBackingInfo()
	if backingInfo == nil {
		return false
	}

	_, ok := backingInfo.(vmmconfig.ADSFVolumeGroupReference)
	return ok
}

func detachVolumeGroupsFromVM(ctx context.Context, client *v4Converged.Client, vmName string, vmUUID string, vmDiskList []vmmconfig.Disk) error {
	log := ctrl.LoggerFrom(ctx)
	// Detach the volume groups from the virtual machine
	for _, disk := range vmDiskList {
		if !isBackedByVolumeGroupReference(&disk) {
			continue
		}

		volumeGroup := disk.GetBackingInfo().(vmmconfig.ADSFVolumeGroupReference)
		volumeGroupExtId := *volumeGroup.VolumeGroupExtId

		log.Info(fmt.Sprintf("detaching volume group %s from virtual machine %s", volumeGroupExtId, vmName))
		body := &volumesconfig.VmAttachment{
			ExtId: ptr.To(vmUUID),
		}

		_, err := client.VolumeGroups.DetachFromVM(ctx, volumeGroupExtId, *body)
		if err != nil {
			return fmt.Errorf("failed to detach volume group %s from virtual machine %s: %w", volumeGroupExtId, vmUUID, err)
		}
		return nil
	}

	return nil
}

func resourceIdsEquals(nris1, nris2 []infrav1.NutanixResourceIdentifier) bool {
	if nris1 == nil && nris2 == nil {
		return true
	}
	if (nris1 == nil && nris2 != nil) ||
		(nris1 != nil && nris2 == nil) ||
		len(nris1) != len(nris2) {
		return false
	}

	for i := range nris1 {
		found := false
		for j := range nris2 {
			if nris1[i].EqualTo(&nris2[j]) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	return true
}

func isNutanixMetroFailureDomain(fdName string) bool {
	return strings.HasPrefix(fdName, metroFailureDomainPrefix)
}

func isNutanixMetroSiteFailureDomain(fdName string) bool {
	return strings.HasPrefix(fdName, metroSiteFailureDomainPrefix)
}

func getNutanixFailureDomainObject(ctx context.Context, ctlclient client.Client, objectName, namespace string) (*infrav1.NutanixFailureDomain, error) {
	fdObj := &infrav1.NutanixFailureDomain{}
	objKey := client.ObjectKey{Name: objectName, Namespace: namespace}
	if err := ctlclient.Get(ctx, objKey, fdObj); err != nil {
		return nil, fmt.Errorf("failed to fetch NutanixFailureDomain object by name %q: %w", objectName, err)
	}
	return fdObj, nil
}

func getNutanixMetroObject(ctx context.Context, ctlclient client.Client, objectName, namespace string) (*infrav1.NutanixMetro, error) {
	metroObj := &infrav1.NutanixMetro{}
	objKey := client.ObjectKey{Name: objectName, Namespace: namespace}
	if err := ctlclient.Get(ctx, objKey, metroObj); err != nil {
		return nil, fmt.Errorf("failed to fetch NutanixMetro object by name %q: %w", objectName, err)
	}
	return metroObj, nil
}

func getNutanixMetroSiteObject(ctx context.Context, ctlclient client.Client, objectName, namespace string) (*infrav1.NutanixMetroSite, error) {
	metroSiteObj := &infrav1.NutanixMetroSite{}
	objKey := client.ObjectKey{Name: objectName, Namespace: namespace}
	if err := ctlclient.Get(ctx, objKey, metroSiteObj); err != nil {
		return nil, fmt.Errorf("failed to fetch NutanixMetroSite object by name %q: %w", objectName, err)
	}
	return metroSiteObj, nil
}

// vHADomainName builds the NutanixVirtualHADomain object name for a (cluster, metro) pair. The name
// is scoped to the cluster so that distinct clusters referencing the same NutanixMetro do not collide
// on a single object.
func vHADomainName(clusterName, metroName string) string {
	return fmt.Sprintf("%s-%s", clusterName, metroName)
}

func getNutanixVHADomainObject(ctx context.Context, ctlclient client.Client, objectName, namespace string) (*infrav1.NutanixVirtualHADomain, error) {
	vhaDomain := &infrav1.NutanixVirtualHADomain{}
	objKey := client.ObjectKey{Name: objectName, Namespace: namespace}
	if err := ctlclient.Get(ctx, objKey, vhaDomain); err != nil {
		return nil, fmt.Errorf("failed to fetch NutanixVirtualHADomain object by name %q: %w", objectName, err)
	}
	return vhaDomain, nil
}

// getOwnedVHADomains returns the NutanixVirtualHADomain objects owned by the given NutanixCluster
// object in the local namespace.
func getOwnedVHADomains(ctx context.Context, ctlclient client.Client, ncl *infrav1.NutanixCluster) ([]*infrav1.NutanixVirtualHADomain, error) {
	// Get all the NutanixVirtualHADomain CRs in the local namespace
	vHADomainsList := &infrav1.NutanixVirtualHADomainList{}
	if err := ctlclient.List(ctx, vHADomainsList, client.InNamespace(ncl.Namespace)); err != nil {
		return nil, err
	}

	vHADomains := []*infrav1.NutanixVirtualHADomain{}
	for i := range vHADomainsList.Items {
		vhaDomain := &vHADomainsList.Items[i]
		for _, ownerRef := range vhaDomain.GetOwnerReferences() {
			if ownerRef.Kind != infrav1.NutanixClusterKind || ownerRef.Name != ncl.Name {
				continue
			}
			gv, err := schema.ParseGroupVersion(ownerRef.APIVersion)
			if err != nil {
				continue
			}
			if gv.Group == infrav1.GroupVersion.Group {
				vHADomains = append(vHADomains, vhaDomain)
				break
			}
		}
	}

	return vHADomains, nil
}

// getVHADomainCategory returns the NutanixVirtualHADomain category that should be applied to a
// Metro/MetroSite machine's VM so that it is placed on the preferred failure domain's Prism Element.
func getVHADomainCategory(mctx *nctx.MachineContext, ctlclient client.Client) (*infrav1.NutanixCategoryIdentifier, error) {
	fdName := mctx.Machine.Spec.FailureDomain
	if !isNutanixMetroFailureDomain(fdName) && !isNutanixMetroSiteFailureDomain(fdName) {
		return nil, fmt.Errorf("the Machine's spec.failureDomain is not configured with NutanixMetro/ or NutanixMetroSite/ prefix: %s", fdName)
	}

	metroName := ""
	namespace := mctx.Machine.Namespace
	if isNutanixMetroSiteFailureDomain(fdName) {
		metrositeObj, err := getNutanixMetroSiteObject(mctx.Context, ctlclient, fdName[len(metroSiteFailureDomainPrefix):], namespace)
		if err != nil {
			return nil, err
		}
		metroName = metrositeObj.Spec.MetroRef.Name
	} else if isNutanixMetroFailureDomain(fdName) {
		metroName = fdName[len(metroFailureDomainPrefix):]
	}

	if mctx.Datastore == nil {
		return nil, fmt.Errorf("failed to get %s from reconciling context", nctx.MetroPreferredFailureDomainName)
	}
	preferredFailureDomain := mctx.Datastore[nctx.MetroPreferredFailureDomainName]
	if preferredFailureDomain == nil {
		return nil, fmt.Errorf("failed to get %s from reconciling context", nctx.MetroPreferredFailureDomainName)
	}

	// Fetch the NutanixCluster owned vHADomain CRs
	vHADomains, err := getOwnedVHADomains(mctx.Context, ctlclient, mctx.NutanixCluster)
	if err != nil {
		return nil, err
	}

	for _, vhaDomain := range vHADomains {
		if vhaDomain.Spec.MetroRef.Name != metroName {
			continue
		}
		if !vhaDomain.Status.Ready {
			return nil, fmt.Errorf("the vHADomain %s is not ready", vhaDomain.Name)
		}

		// Find the category-recovery-plan mapping for the preferred failure domain. Only the
		// cluster-scope movement group is supported for now; nodepool-scope movement groups are not
		// yet handled, so we restrict the lookup to the well-known cluster-scope group.
		mgIdx := -1
		for i, mg := range vhaDomain.Spec.MovementGroups {
			if mg.Name == clusterScopeMovementGroupName {
				mgIdx = i
				break
			}
		}
		if mgIdx < 0 {
			return nil, fmt.Errorf("vHADomain %s has no %q (cluster-scope) movement group", vhaDomain.Name, clusterScopeMovementGroupName)
		}
		movementGroup := vhaDomain.Spec.MovementGroups[mgIdx]

		for i := range movementGroup.CategoryRecoveryPlans {
			crp := movementGroup.CategoryRecoveryPlans[i]
			if crp.FailureDomainRef.Name != *preferredFailureDomain {
				continue
			}

			preferredCategory := crp.Category
			// validate the preferredCategory exists in PC
			if _, err := getCategory(mctx.Context, mctx.ConvergedClient, preferredCategory.Key, preferredCategory.Value); err != nil {
				return nil, fmt.Errorf("HADomain: %s, NutanixMetro: %s, failed to fetch Category (key:%s, value:%s) from PC: %w", vhaDomain.Name, metroName, preferredCategory.Key, preferredCategory.Value, err)
			}

			return &preferredCategory, nil
		}

		return nil, fmt.Errorf("vHADomain %s has no category mapping for the preferred failureDomain %s", vhaDomain.Name, *preferredFailureDomain)
	}

	return nil, fmt.Errorf("not found vHADomain category for NutanixMachine")
}
