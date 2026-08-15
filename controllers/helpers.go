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
	multidomainModels "github.com/nutanix/ntnx-api-golang-clients/multidomain-go-client/v4/models/multidomain/v4/config"
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
	capiv1beta2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
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

	CAPXProjectPolicyAnnotation    = "capx.nutanix.com/project-policy"
	CAPXProjectPolicyDefaultOnly   = "default-only"
	CAPXProjectPolicyUnrestricted  = "unrestricted"
	CAPXProjectPolicySingleProject = "single-project"

	CAPXProjectUUIDAnnotation = "capx.nutanix.com/project-uuid"

	// VMCreationRequestIDAnnotation holds the idempotency key (Ntnx-Request-Id) used for
	// the VM Create call. It is minted once and persisted before the first Create attempt,
	// then reused by every later reconcile so a retried Create returns the original task's
	// result instead of creating a second VM. It lives in an annotation, not status, because
	// clusterctl move drops status on Create for objects with the status subresource enabled,
	// while metadata (including annotations) passes through unchanged.
	VMCreationRequestIDAnnotation = "capx.nutanix.com/vm-creation-request-id"

	metroFailureDomainPrefix         = "NutanixMetro/"
	metroSiteFailureDomainPrefix     = "NutanixMetroSite/"
	metroNativeFailureDomainLabelKey = "metro.nutanix.com/native-failuredomain"
	metroNativePELabelKey            = "metro.nutanix.com/native-pe"
	metroActivePlacementPEAnnotation = "metro.nutanix.com/active-placement-pe"

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

// isVMUsableForProject returns true if the VM belongs to the given project.
func isVMUsableForProject(vm *vmmconfig.Vm, projectExtID string) bool {
	if vm == nil {
		return false
	}
	if vm.Project == nil || vm.Project.ExtId == nil {
		return false
	}
	return *vm.Project.ExtId == projectExtID
}

// FindVMByUUID retrieves the VM with the given vm UUID. Returns nil if not found.
// Project validation is only performed on PC >= 7.6; for older PC versions
// project scoping is unsupported and is skipped entirely.
func FindVMByUUID(ctx context.Context, client *v4Converged.Client, uuid string, projectExtID *string, pcVersion string) (*vmmconfig.Vm, error) {
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

	if isPCVersionHigherThan75(pcVersion) && projectExtID != nil && !isVMUsableForProject(response, *projectExtID) {
		return nil, &terminalError{message: fmt.Sprintf(
			"VM with UUID %s is not in project %s", uuid, *projectExtID)}
	}

	return response, nil
}

// GenerateProviderID generates a provider ID for the given resource UUID
func GenerateProviderID(uuid string) string {
	return fmt.Sprintf("%s%s", providerIdPrefix, uuid)
}

// GetVMUUID returns the UUID of the VM.
func GetVMUUID(machine *capiv1beta2.Machine, nutanixMachine *infrav1.NutanixMachine) (string, error) {
	// First, try to get the systemUUID from Machine.Status.NodeInfo
	if machine != nil && machine.Status.NodeInfo != nil && machine.Status.NodeInfo.SystemUUID != "" {
		systemUUID := machine.Status.NodeInfo.SystemUUID
		if _, err := uuid.Parse(systemUUID); err != nil {
			return "", fmt.Errorf("Machine.Status.NodeInfo.SystemUUID was set but was not a valid UUID: %s err: %w", systemUUID, err)
		}
		return systemUUID, nil
	}
	vmUUID := nutanixMachine.Status.VmUUID
	if vmUUID != "" {
		if _, err := uuid.Parse(vmUUID); err != nil {
			return "", fmt.Errorf("VMUUID was set but was not a valid UUID: %s err: %w", vmUUID, err)
		}
		return vmUUID, nil
	}
	// Spec.ProviderID is patched in the same call as Status.VmUUID, but lands on the
	// API server first (metadata+spec patch before the status patch), so a reconcile
	// triggered immediately behind VM creation can observe it while Status.VmUUID is
	// still not durable. It is also the only one of the two fields that survives a
	// clusterctl move, since status is dropped on Create for objects with the status
	// subresource. Falling back to it here lets FindVM locate the VM by UUID instead
	// of racing Prism's name-search index.
	//
	// Unlike Status.VmUUID, this field isn't exclusively controller-written: templates
	// are free to pre-populate NutanixMachine.Spec.ProviderID with a non-UUID placeholder
	// (the e2e NutanixMachineTemplate fixture ships "nutanix://${CLUSTER_NAME}-m1"), so a
	// value that doesn't parse must be treated as "no usable identifier yet" rather than
	// a hard error - an error here would permanently block reconciliation for any machine
	// created from such a template.
	if providerID := nutanixMachine.Spec.ProviderID; providerID != "" {
		vmUUID, ok := strings.CutPrefix(providerID, providerIdPrefix)
		if !ok {
			return "", nil
		}
		if _, err := uuid.Parse(vmUUID); err != nil {
			return "", nil
		}
		return vmUUID, nil
	}
	return "", nil
}

// FindVM retrieves the VM with the given uuid or name within the specified project
func FindVM(ctx context.Context, client *v4Converged.Client, machine *capiv1beta2.Machine, nutanixMachine *infrav1.NutanixMachine, vmName string, projectExtID *string, pcVersion string) (*vmmconfig.Vm, error) {
	log := ctrl.LoggerFrom(ctx)
	vmUUID, err := GetVMUUID(machine, nutanixMachine)
	if err != nil {
		return nil, err
	}
	// Search via uuid if it is present
	if vmUUID != "" {
		log.V(1).Info(fmt.Sprintf("Searching for VM %s using UUID %s", vmName, vmUUID))
		vm, err := FindVMByUUID(ctx, client, vmUUID, projectExtID, pcVersion)
		if err != nil {
			return nil, err
		}
		if vm == nil {
			return nil, fmt.Errorf("no vm %s found with UUID %s but was expected to be present", vmName, vmUUID)
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
		vm, err := FindVMByName(ctx, client, vmName, projectExtID, pcVersion)
		if err != nil {
			log.Error(err, fmt.Sprintf("error occurred finding VM %s by name", vmName))
			return nil, err
		}
		return vm, nil
	}
}

// FindVMByName retrieves the VM with the given vm name. Project filtering is only
// applied on PC >= 7.6; for older PC versions project scoping is unsupported.
func FindVMByName(ctx context.Context, client *v4Converged.Client, vmName string, projectExtID *string, pcVersion string) (*vmmconfig.Vm, error) {
	log := ctrl.LoggerFrom(ctx)

	var filter string
	if isPCVersionHigherThan75(pcVersion) && projectExtID != nil {
		filter = fmt.Sprintf("name eq '%s' and projectExtId eq '%s'", vmName, *projectExtID)
		log.Info(fmt.Sprintf("Checking if VM with name %s exists in project %s.", vmName, *projectExtID))
	} else {
		filter = fmt.Sprintf("name eq '%s'", vmName)
		log.Info(fmt.Sprintf("Checking if VM with name %s exists.", vmName))
	}

	vms, err := client.VMs.List(ctx, converged.WithFilter(filter))
	if err != nil {
		return nil, err
	}

	if len(vms) > 1 {
		return nil, fmt.Errorf("error: found more than one (%v) vms with name %s", len(vms), vmName)
	}

	if len(vms) == 0 {
		return nil, nil
	}

	return FindVMByUUID(ctx, client, *vms[0].ExtId, projectExtID, pcVersion)
}

// GetPEUUID returns the UUID of the Prism Element cluster with the given name or UUID.
//
// When rg is non-nil, the lookup is constrained to the project's resource group
// (its placement targets); a PE that is not part of placement targets of the resource group is
// treated as not authorized for the project. When rg is nil (default-project
// path), it falls back to cluster-wide client.Clusters.* lookups.
func GetPEUUID(ctx context.Context, client *v4Converged.Client, rg *multidomainModels.ResourceGroup, peName, peUUID *string) (string, error) {
	if client == nil {
		return "", fmt.Errorf("cannot retrieve Prism Element UUID if nutanix client is nil")
	}
	if peUUID == nil && peName == nil {
		return "", fmt.Errorf("cluster name or uuid must be passed in order to retrieve the Prism Element UUID")
	}

	// Project-scoped path: resolve against the resource group's placement targets.
	if rg != nil {
		return resolvePEFromResourceGroup(ctx, client, rg, peName, peUUID)
	}

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

// resolvePEFromResourceGroup resolves a PE identifier (name or UUID) against the
// Prism Elements reachable from the project's resource group. It relies on the
// prism-go-client ResourceGroups.ListPrismElements helper to extract the PEs
// from the resource group's placement targets.
func resolvePEFromResourceGroup(ctx context.Context, client *v4Converged.Client, rg *multidomainModels.ResourceGroup, peName, peUUID *string) (string, error) {
	if rg.ExtId == nil {
		return "", fmt.Errorf("resource group has no ExtId; cannot resolve Prism Element")
	}

	prismElements, err := client.ResourceGroups.ListPrismElements(ctx, *rg.ExtId)
	if err != nil {
		return "", fmt.Errorf("failed to list Prism Elements for resource group %s: %w", *rg.ExtId, err)
	}

	if peUUID != nil && *peUUID != "" {
		for _, pe := range prismElements {
			if pe.ExtId == *peUUID {
				return *peUUID, nil
			}
		}
		return "", &terminalError{message: fmt.Sprintf(
			"Prism Element cluster with UUID %s is not authorized for this project (not in resource group)", *peUUID)}
	}

	matches := make([]string, 0)
	for _, pe := range prismElements {
		if strings.EqualFold(pe.Name, *peName) {
			matches = append(matches, pe.ExtId)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return "", &terminalError{message: fmt.Sprintf(
			"failed to retrieve Prism Element cluster by name %s in this project's resource group", *peName)}
	default:
		return "", fmt.Errorf("more than one Prism Element cluster found with name %s in this project's resource group", *peName)
	}
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
func CreateDataDiskList(ctx context.Context, convergedClient *v4Converged.Client, dataDiskSpecs []infrav1.NutanixMachineVMDisk, peUUID string, project *nctx.ProjectInfo, pcVersion string, resourceGroup *multidomainModels.ResourceGroup) ([]vmmconfig.Disk, []vmmconfig.CdRom, error) {
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

		err := addDataSourceImageRefToVmDisk(ctx, convergedClient, vmDisk, dataDiskSpec.DataSource, project, pcVersion)
		if err != nil {
			return nil, nil, err
		}

		err = addStorageConfigAndContainerToVmDisk(ctx, convergedClient, vmDisk, dataDiskSpec.StorageConfig, peUUID, resourceGroup)
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

func addDataSourceImageRefToVmDisk(ctx context.Context, convergedClient *v4Converged.Client, vmDisk *vmmconfig.VmDisk, dataSource *infrav1.NutanixResourceIdentifier, project *nctx.ProjectInfo, pcVersion string) error {
	if dataSource == nil {
		return nil
	}

	image, err := GetImage(ctx, convergedClient, infrav1.NutanixResourceIdentifier{
		UUID: dataSource.UUID,
		Type: infrav1.NutanixIdentifierUUID,
	}, project, pcVersion)
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

func addStorageConfigAndContainerToVmDisk(ctx context.Context, convergedClient *v4Converged.Client, vmDisk *vmmconfig.VmDisk, storageConfig *infrav1.NutanixMachineVMStorageConfig, peUUID string, resourceGroup *multidomainModels.ResourceGroup) error {
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
		sc, err := GetStorageContainerInCluster(ctx, convergedClient, resourceGroup, *storageConfig.StorageContainer, peID)
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

// isSubnetUsableForProject returns true if the subnet is accessible from the
// given project, i.e. it is either owned by the project or explicitly shared
// with it. PC < 7.6 has no concept of project ownership / sharing for subnets,
// so callers must gate this check on the PC version before invoking it.
func isSubnetUsableForProject(subnet *subnetModels.Subnet, projectExtID string) bool {
	if subnet == nil {
		return false
	}
	// Directly owned by the project.
	if subnet.ProjectExtId != nil && *subnet.ProjectExtId == projectExtID {
		return true
	}
	// Explicitly shared with the project.
	return slices.Contains(subnet.SharedWithProjects, projectExtID)
}

// subnetReachableFromPE returns true if the subnet is usable from the given
// Prism Element: overlay subnets are reachable from any PE, while VLAN subnets
// must belong to the PE cluster.
func subnetReachableFromPE(subnet *subnetModels.Subnet, peUUID string) bool {
	if subnet.SubnetType != nil && subnet.SubnetType.GetName() == subnetTypeOverlay {
		return true
	}
	return subnetBelongsToCluster(subnet, peUUID)
}

// GetSubnetUUID returns the UUID of the subnet with the given name or UUID, scoped
// to the PE identified by peUUID.
//
// When project is non-nil and the PC version supports projects (>= 7.6), the result
// is additionally scoped to the given project: a UUID lookup must resolve to a subnet
// owned by or shared with the project, and a name lookup retains only subnets accessible
// from the project, preferring a project-owned subnet over a shared one of the same name
// (so the presence of both is not treated as ambiguous). On PC < 7.6 (or when project is
// nil) the legacy, unscoped behavior is preserved.
//
//nolint:gocognit // project-aware lookup requires branching on UUID/name and ownership/shared state
func GetSubnetUUID(ctx context.Context, client *v4Converged.Client, peUUID string, subnetName, subnetUUID *string, project *nctx.ProjectInfo, pcVersion string) (string, error) {
	if subnetUUID == nil && subnetName == nil {
		return "", fmt.Errorf("subnet name or subnet uuid must be passed in order to retrieve the subnet")
	}

	var projectExtID *string
	if project != nil {
		projectExtID = project.ExtID
	}
	// projectAware indicates project scoping should be enforced. On PC < 7.6 the
	// concept of project ownership / shared-with-projects does not exist, so we
	// fall back to the legacy (unscoped) lookup behavior.
	projectAware := isPCVersionHigherThan75(pcVersion) && projectExtID != nil

	switch {
	case subnetUUID != nil:
		subnetResp, err := client.Subnets.Get(ctx, *subnetUUID)
		if err != nil {
			if converged.IsNotFound(err) {
				return "", fmt.Errorf("failed to find subnet with UUID %s: %w", *subnetUUID, err)
			}
			return "", fmt.Errorf("failed to get subnet with UUID %s: %w", *subnetUUID, err)
		}
		if projectAware && !isSubnetUsableForProject(subnetResp, *projectExtID) {
			return "", &terminalError{message: fmt.Sprintf(
				"subnet with UUID %s is not accessible in project %s", *subnetUUID, *project.Name)}
		}
		return *subnetResp.ExtId, nil

	default: // search by name
		// Not using additional filtering since we want to list overlay and vlan subnets
		responseSubnets, err := client.Subnets.List(ctx, converged.WithFilter(fmt.Sprintf("name eq '%s'", *subnetName)))
		if err != nil {
			return "", err
		}

		if !projectAware {
			// Legacy behavior: exact name match + PE/overlay scoping, must resolve to a single subnet.
			foundSubnets := make([]subnetModels.Subnet, 0)
			for i := range responseSubnets {
				subnet := &responseSubnets[i]
				if subnet.Name == nil || subnet.SubnetType == nil || !strings.EqualFold(*subnet.Name, *subnetName) {
					continue
				}
				if subnetReachableFromPE(subnet, peUUID) {
					foundSubnets = append(foundSubnets, *subnet)
				}
			}
			switch len(foundSubnets) {
			case 0:
				return "", &terminalError{message: fmt.Sprintf("failed to retrieve subnet by name %s", *subnetName)}
			case 1:
				return *foundSubnets[0].ExtId, nil
			default:
				return "", fmt.Errorf("more than one subnet found with name %s", *subnetName)
			}
		}

		// Project-aware: a subnet must be PE-scoped (or overlay) and usable by the
		// project; partition the usable name matches into subnets owned by this
		// project versus subnets merely shared with it.
		var owned, shared []subnetModels.Subnet
		for i := range responseSubnets {
			subnet := &responseSubnets[i]
			if subnet.Name == nil || subnet.SubnetType == nil || !strings.EqualFold(*subnet.Name, *subnetName) {
				continue
			}
			if !subnetReachableFromPE(subnet, peUUID) {
				continue
			}
			if !isSubnetUsableForProject(subnet, *projectExtID) {
				continue
			}
			if subnet.ProjectExtId != nil && *subnet.ProjectExtId == *projectExtID {
				owned = append(owned, *subnet)
			} else {
				shared = append(shared, *subnet)
			}
		}

		// Prefer a project-owned subnet over a shared one of the same name.
		switch {
		case len(owned) == 1:
			return *owned[0].ExtId, nil
		case len(owned) > 1:
			return "", fmt.Errorf(
				"more than one subnet found with name %s owned by project %s", *subnetName, *project.Name)
		case len(shared) == 1:
			return *shared[0].ExtId, nil
		case len(shared) > 1:
			return "", fmt.Errorf(
				"more than one subnet found with name %s shared with project %s and none owned by it",
				*subnetName, *project.Name)
		default:
			return "", &terminalError{message: fmt.Sprintf(
				"failed to retrieve subnet by name %s in project %s", *subnetName, *project.Name)}
		}
	}
}

// isImageUsableForProject returns true if the image is accessible from the
// given project, i.e. it is either owned by the project or shared with all
// projects. PC < 7.6 has no concept of project ownership / sharing for images,
// so callers must gate this check on the PC version before invoking it.
func isImageUsableForProject(image *imageModels.Image, projectExtID string) bool {
	if image == nil {
		return false
	}
	// Directly owned by the project.
	if image.ProjectExtId != nil && *image.ProjectExtId == projectExtID {
		return true
	}
	// Shared with all projects.
	if image.IsSharedWithAllProjects != nil && *image.IsSharedWithAllProjects {
		return true
	}
	return false
}

// GetImage returns an image. If no UUID is provided, returns the unique image with the name.
// Returns an error if no image has the UUID, if no image has the name, or more than one image has the name.
//
// When project is non-nil and the PC version supports projects (>= 7.6),
// the result is scoped to the given project: a UUID lookup must resolve to an
// image that is owned by or shared with the project, and a name lookup retains
// only images accessible from the project. During name-based lookups a
// project-owned image is preferred over a shared image of the same name, so the
// presence of both is not treated as ambiguous. On PC < 7.6 (or when
// project is nil) the legacy, unscoped behavior is preserved.
//
//nolint:gocognit // project-aware lookup requires branching on UUID/name and ownership/shared state
func GetImage(ctx context.Context, client *v4Converged.Client, id infrav1.NutanixResourceIdentifier, project *nctx.ProjectInfo, pcVersion string) (*imageModels.Image, error) {
	var projectExtID *string
	if project != nil {
		projectExtID = project.ExtID
	}

	// projectAware indicates project scoping should be enforced. On PC < 7.6 the
	// concept of project ownership / shared-with-all-projects does not exist, so
	// we fall back to the legacy (unscoped) lookup behavior.
	projectAware := isPCVersionHigherThan75(pcVersion) && projectExtID != nil

	switch {
	case id.IsUUID():
		resp, err := client.Images.Get(ctx, *id.UUID)
		if err != nil {
			if converged.IsNotFound(err) {
				return nil, fmt.Errorf("failed to find image with UUID %s: %w", *id.UUID, err)
			}
			return nil, fmt.Errorf("failed to get image with UUID %s: %w", *id.UUID, err)
		}
		if projectAware && !isImageUsableForProject(resp, *projectExtID) {
			return nil, &terminalError{message: fmt.Sprintf(
				"image with UUID %s is not accessible in project %s", *id.UUID, *project.Name)}
		}
		return resp, nil
	case id.IsName():
		responseImages, err := client.Images.List(ctx, converged.WithFilter(fmt.Sprintf("name eq '%s'", *id.Name)))
		if err != nil {
			return nil, err
		}

		if !projectAware {
			// Legacy behavior: exact name match, must resolve to a single image.
			foundImages := make([]*imageModels.Image, 0)
			for i := range responseImages {
				if responseImages[i].Name != nil && strings.EqualFold(*responseImages[i].Name, *id.Name) {
					foundImages = append(foundImages, &responseImages[i])
				}
			}
			switch len(foundImages) {
			case 0:
				return nil, &terminalError{message: fmt.Sprintf("found no image with name %s", *id.Name)}
			case 1:
				return foundImages[0], nil
			default:
				return nil, fmt.Errorf("more than one image found with name %s", *id.Name)
			}
		}

		// Project-aware: partition the usable name matches into images owned by
		// this project versus images merely shared with it.
		var owned, shared []*imageModels.Image
		for i := range responseImages {
			img := &responseImages[i]
			if img.Name == nil || !strings.EqualFold(*img.Name, *id.Name) {
				continue
			}
			if !isImageUsableForProject(img, *projectExtID) {
				continue
			}
			if img.ProjectExtId != nil && *img.ProjectExtId == *projectExtID {
				owned = append(owned, img)
			} else {
				shared = append(shared, img)
			}
		}

		// Prefer a project-owned image over a shared one of the same name.
		switch {
		case len(owned) == 1:
			return owned[0], nil
		case len(owned) > 1:
			return nil, fmt.Errorf(
				"more than one image found with name %s owned by project %s", *id.Name, *project.Name)
		case len(shared) == 1:
			return shared[0], nil
		case len(shared) > 1:
			return nil, fmt.Errorf(
				"more than one image found with name %s shared with project %s and none owned by it",
				*id.Name, *project.Name)
		default:
			return nil, &terminalError{message: fmt.Sprintf(
				"found no image with name %s accessible in project %s", *id.Name, *project.Name)}
		}
	default:
		return nil, fmt.Errorf("image identifier is missing both name and uuid")
	}
}

// isVMProfileUsableForProject returns true if the VM profile is accessible from
// the given project, i.e. it is either owned by the project or explicitly shared
// with it. PC < 7.6 has no concept of project ownership / sharing for VM
// profiles, so callers must gate this check on the PC version before invoking it.
func isVMProfileUsableForProject(profile *vmmconfig.VmProfile, projectExtID string) bool {
	if profile == nil {
		return false
	}
	// Directly owned by the project.
	if profile.ProjectExtId != nil && *profile.ProjectExtId == projectExtID {
		return true
	}
	// Explicitly shared with the project.
	return slices.Contains(profile.SharedWithProjects, projectExtID)
}

// GetVMProfile returns a VM profile scoped to the given project. VM profiles are only
// supported on PC 7.6 or later, so the lookup is always project-aware: a UUID lookup must
// resolve to a profile owned by or shared with the project, and a name lookup retains only
// profiles accessible from the project, preferring a project-owned profile over a shared
// one of the same name (so the presence of both is not treated as ambiguous).
func GetVMProfile(ctx context.Context, client *v4Converged.Client, id infrav1.NutanixResourceIdentifier, project *nctx.ProjectInfo) (*vmmconfig.VmProfile, error) {
	if project == nil || project.ExtID == nil {
		return nil, fmt.Errorf("project must be set to look up VM profile %s", id.String())
	}
	projectExtID := *project.ExtID

	switch {
	case id.IsUUID():
		profile, err := client.VMProfiles.Get(ctx, *id.UUID)
		if err != nil {
			return nil, fmt.Errorf("failed to find VM profile with UUID %s: %w", *id.UUID, err)
		}
		if !isVMProfileUsableForProject(profile, projectExtID) {
			return nil, &terminalError{message: fmt.Sprintf(
				"VM profile with UUID %s is not accessible in project %s", *id.UUID, *project.Name)}
		}
		return profile, nil
	case id.IsName():
		responseProfiles, err := client.VMProfiles.List(ctx, converged.WithFilter(fmt.Sprintf("name eq '%s'", *id.Name)))
		if err != nil {
			return nil, err
		}

		// Partition the usable name matches into profiles owned by this project
		// versus profiles merely shared with it.
		var owned, shared []*vmmconfig.VmProfile
		for i := range responseProfiles {
			p := &responseProfiles[i]
			if p.Name == nil || !strings.EqualFold(*p.Name, *id.Name) {
				continue
			}
			if !isVMProfileUsableForProject(p, projectExtID) {
				continue
			}
			if p.ProjectExtId != nil && *p.ProjectExtId == projectExtID {
				owned = append(owned, p)
			} else {
				shared = append(shared, p)
			}
		}

		// Prefer a project-owned profile over a shared one of the same name.
		switch {
		case len(owned) == 1:
			return owned[0], nil
		case len(owned) > 1:
			return nil, fmt.Errorf(
				"more than one VM profile found with name %s owned by project %s", *id.Name, *project.Name)
		case len(shared) == 1:
			return shared[0], nil
		case len(shared) > 1:
			return nil, fmt.Errorf(
				"more than one VM profile found with name %s shared with project %s and none owned by it",
				*id.Name, *project.Name)
		default:
			return nil, &terminalError{message: fmt.Sprintf(
				"found no VM profile with name %s accessible in project %s", *id.Name, *project.Name)}
		}
	default:
		return nil, fmt.Errorf("VM profile identifier is missing both name and uuid")
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
	project *nctx.ProjectInfo,
	pcVersion string,
) (*imageModels.Image, error) {
	var projectExtID *string
	if project != nil {
		projectExtID = project.ExtID
	}

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
	// projectAware indicates project scoping should be enforced. On PC < 7.6 the
	// concept of project ownership / shared-with-all-projects does not exist, so
	// we fall back to the legacy (unscoped) lookup behavior.
	projectAware := isPCVersionHigherThan75(pcVersion) && projectExtID != nil

	re := regexp.MustCompile(templateBytes.String())
	// Partition the regex survivors into images owned by this project versus
	// images merely shared with it. When not project-aware everything lands in
	// owned, preserving the legacy behavior.
	owned := make([]*imageModels.Image, 0)
	shared := make([]*imageModels.Image, 0)
	for i := range responseImages {
		img := &responseImages[i]
		if img.Name == nil || !re.Match([]byte(*img.Name)) {
			continue
		}
		if !projectAware {
			owned = append(owned, img)
			continue
		}
		if !isImageUsableForProject(img, *projectExtID) {
			continue
		}
		if img.ProjectExtId != nil && *img.ProjectExtId == *projectExtID {
			owned = append(owned, img)
		} else {
			shared = append(shared, img)
		}
	}

	// Prefer project-owned images over shared ones; only fall back to shared
	// images when the project owns none matching the lookup.
	candidates := owned
	if len(candidates) == 0 {
		candidates = shared
	}
	sorted := sortImagesByLatestCreationTime(candidates)
	if len(sorted) == 0 {
		if projectAware {
			return nil, &terminalError{message: fmt.Sprintf(
				"failed to find image with filter %s accessible in project %s",
				templateBytes.String(), *project.Name)}
		}
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
	filterString := fmt.Sprintf(
		"entitiesAffected/any(a:a/extId eq '%s') "+
			"and (status eq Prism.Config.TaskStatus'RUNNING' or status eq Prism.Config.TaskStatus'QUEUED') "+
			"and (operation eq 'kImageDelete')",
		*image.ExtId)
	tasks, err := client.Tasks.List(ctx, converged.WithFilter(filterString))
	if err != nil {
		return false, err
	}
	return len(tasks) > 0, nil
}

func VmHasTaskInProgress(ctx context.Context, client *v4Converged.Client, vmExtId string, projectExtID *string, pcVersion string) (bool, error) {
	log := ctrl.LoggerFrom(ctx)
	if vmExtId == "" {
		return false, fmt.Errorf("cannot extract task uuid for empty vm extId")
	}

	log.V(1).Info(fmt.Sprintf("Getting task uuid for vm %s", vmExtId))

	var filterString string
	if isPCVersionHigherThan75(pcVersion) && projectExtID != nil {
		filterString = fmt.Sprintf(
			"entitiesAffected/any(a:a/extId eq '%s') "+
				"and (status eq Prism.Config.TaskStatus'RUNNING' or status eq Prism.Config.TaskStatus'QUEUED') "+
				"and projectExtId eq '%s'",
			vmExtId, *projectExtID)
	} else {
		// Project scoping is unsupported on PC < 7.6, query without it.
		filterString = fmt.Sprintf(
			"entitiesAffected/any(a:a/extId eq '%s') "+
				"and (status eq Prism.Config.TaskStatus'RUNNING' or status eq Prism.Config.TaskStatus'QUEUED')",
			vmExtId)
	}
	tasks, err := client.Tasks.List(ctx, converged.WithFilter(filterString))
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

// GetSubnetUUIDList returns a list of subnet UUIDs for the given list of subnet names,
// scoped to the PE identified by peUUID and (on PC 7.6+ with a non-nil project) to the
// given project.
func GetSubnetUUIDList(ctx context.Context, client *v4Converged.Client, machineSubnets []infrav1.NutanixResourceIdentifier, peUUID string, project *nctx.ProjectInfo, pcVersion string) ([]string, error) {
	subnetUUIDs := make([]string, 0)
	for _, machineSubnet := range machineSubnets {
		subnetUUID, err := GetSubnetUUID(
			ctx,
			client,
			peUUID,
			machineSubnet.Name,
			machineSubnet.UUID,
			project,
			pcVersion,
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

// GetOrCreateCategories returns the list of categories for the given list of category
// identifiers, without project scoping.
func GetOrCreateCategories(
	ctx context.Context,
	client *v4Converged.Client,
	categoryIdentifiers []*infrav1.NutanixCategoryIdentifier,
) ([]*prismModels.Category, error) {
	return GetOrCreateCategoriesForProject(ctx, client, categoryIdentifiers, nil)
}

// GetOrCreateCategoriesForProject returns the list of categories for the given list of
// category identifiers, scoped to the given project. A nil projectExtID means no project
// scoping; callers are responsible for passing nil on PC versions older than 7.6, where
// project-scoped categories do not exist.
func GetOrCreateCategoriesForProject(
	ctx context.Context,
	client *v4Converged.Client,
	categoryIdentifiers []*infrav1.NutanixCategoryIdentifier,
	projectExtID *string,
) ([]*prismModels.Category, error) {
	categories := make([]*prismModels.Category, 0)
	for _, ci := range categoryIdentifiers {
		if ci == nil {
			return categories, fmt.Errorf("cannot get or create nil category")
		}
		category, err := getOrCreateCategoryForProject(ctx, client, ci, projectExtID)
		if err != nil {
			return categories, err
		}
		categories = append(categories, category)
	}
	return categories, nil
}

func getCategories(ctx context.Context, client *v4Converged.Client, key, value string) ([]prismModels.Category, error) {
	categories, err := client.Categories.List(ctx, converged.WithFilter(fmt.Sprintf("key eq '%s' and value eq '%s'", key, value)))
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve category value %s in category %s. error: %w", value, key, err)
	}
	return categories, nil
}

// getCategory retrieves the category matching the given key/value without project
// scoping, returning the first match (or nil when none exists).
func getCategory(ctx context.Context, client *v4Converged.Client, key, value string) (*prismModels.Category, error) {
	categories, err := getCategories(ctx, client, key, value)
	if err != nil {
		return nil, err
	}
	if len(categories) == 0 {
		return nil, nil
	}
	return &categories[0], nil
}

// getCategoryForProject filters a list of categories to find one usable for the given project.
// If projectExtID is nil, returns the first category (non-project-aware behavior).
// If projectExtID is set, prefers a category owned by the project over one that is merely shared.
// Returns nil when no usable category exists.
func getCategoryForProject(categories []prismModels.Category, projectExtID *string) *prismModels.Category {
	if len(categories) == 0 {
		return nil
	}
	if projectExtID == nil {
		return &categories[0]
	}
	// Prefer a category owned by the project over one that is merely shared with it.
	for i := range categories {
		if categories[i].ProjectExtId != nil && *categories[i].ProjectExtId == *projectExtID {
			return &categories[i]
		}
	}
	for i := range categories {
		if isCategoryUsableForProject(&categories[i], *projectExtID) {
			return &categories[i]
		}
	}
	return nil
}

// deleteCategoryKeyValues deletes categories without project scoping.
func deleteCategoryKeyValues(ctx context.Context, client *v4Converged.Client, categoryIdentifiers []*infrav1.NutanixCategoryIdentifier) error {
	return deleteCategoryKeyValuesForProject(ctx, client, categoryIdentifiers, nil)
}

func deleteCategoryKeyValuesForProject(ctx context.Context, client *v4Converged.Client, categoryIdentifiers []*infrav1.NutanixCategoryIdentifier, projectExtID *string) error {
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
			categories, err := getCategories(ctx, client, key, value)
			if err != nil {
				errorMsg := fmt.Errorf("failed to retrieve category value %s in category %s. error: %w", value, key, err)
				log.Error(errorMsg, "failed to retrieve category value")
				return errorMsg
			}
			prismCategory := getCategoryForProject(categories, projectExtID)
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

// DeleteCategories deletes the given list of categories without project scoping.
func DeleteCategories(ctx context.Context, clientV4 *v4Converged.Client, categoryIdentifiers, obsoleteCategoryIdentifiers []*infrav1.NutanixCategoryIdentifier) error {
	return DeleteCategoriesForProject(ctx, clientV4, categoryIdentifiers, obsoleteCategoryIdentifiers, nil)
}

// DeleteCategoriesForProject deletes the given list of categories, scoped to the given
// project. A nil projectExtID means no project scoping; callers pass nil on PC versions
// older than 7.6, where project-scoped categories do not exist.
func DeleteCategoriesForProject(ctx context.Context, clientV4 *v4Converged.Client, categoryIdentifiers, obsoleteCategoryIdentifiers []*infrav1.NutanixCategoryIdentifier, projectExtID *string) error {
	// Dont delete keys with newer format as key is constant string
	err := deleteCategoryKeyValuesForProject(ctx, clientV4, categoryIdentifiers, projectExtID)
	if err != nil {
		return err
	}
	// Delete obsolete keys with older format to cleanup brownfield setups
	err = deleteCategoryKeyValuesForProject(ctx, clientV4, obsoleteCategoryIdentifiers, projectExtID)
	if err != nil {
		return err
	}

	return nil
}

// resolveProjectInfoForPolicy resolves the project (ExtID + Name) implied by a cluster's
// project policy. It returns nil when no project scoping applies: an unrestricted (or
// unset) policy, or a PC version older than 7.6. The returned ProjectInfo always carries
// both ExtID and Name when available. For single-project policy, the projectUUID parameter
// must be provided.
func resolveProjectInfoForPolicy(ctx context.Context, client *v4Converged.Client, projectPolicy, projectUUID, pcVersion string) (*nctx.ProjectInfo, error) {
	if !isPCVersionHigherThan75(pcVersion) {
		return nil, nil
	}
	switch projectPolicy {
	case "", CAPXProjectPolicyUnrestricted:
		return nil, nil
	case CAPXProjectPolicyDefaultOnly:
		project, err := client.Projects.GetDefaultProject(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get default project for policy %q: %w", projectPolicy, err)
		}
		return &nctx.ProjectInfo{ExtID: project.ExtId, Name: project.Name}, nil
	case CAPXProjectPolicySingleProject:
		if projectUUID == "" {
			return nil, fmt.Errorf("single-project policy requires %s annotation", CAPXProjectUUIDAnnotation)
		}
		return &nctx.ProjectInfo{ExtID: &projectUUID, Name: &projectUUID}, nil
	default:
		return nil, fmt.Errorf("invalid project policy %q", projectPolicy)
	}
}

// getOrCreateCategory gets or creates a category without project scoping.
func getOrCreateCategory(
	ctx context.Context,
	client *v4Converged.Client,
	categoryIdentifier *infrav1.NutanixCategoryIdentifier,
) (*prismModels.Category, error) {
	return getOrCreateCategoryForProject(ctx, client, categoryIdentifier, nil)
}

func getOrCreateCategoryForProject(
	ctx context.Context,
	client *v4Converged.Client,
	categoryIdentifier *infrav1.NutanixCategoryIdentifier,
	projectExtID *string,
) (*prismModels.Category, error) {
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
	categories, err := getCategories(ctx, client, categoryIdentifier.Key, categoryIdentifier.Value)
	if err != nil {
		errorMsg := fmt.Errorf("failed to retrieve category with key %s. error: %w", categoryIdentifier.Key, err)
		log.Error(errorMsg, "failed to retrieve category")
		return nil, errorMsg
	}
	prismCategory := getCategoryForProject(categories, projectExtID)
	if prismCategory == nil {
		log.V(1).Info(fmt.Sprintf("Category with key %s and value %s did not exist.", categoryIdentifier.Key, categoryIdentifier.Value))
		category := &prismModels.Category{
			Key:         ptr.To(categoryIdentifier.Key),
			Description: ptr.To(infrav1.DefaultCAPICategoryDescription),
			Value:       ptr.To(categoryIdentifier.Value),
		}
		// Stamp the project on the category only when a project is in scope. Callers
		// pass a nil projectExtID on PC < 7.6, where project-scoped categories are not
		// supported and the API rejects the field.
		if projectExtID != nil {
			category.ProjectExtId = projectExtID
		}
		prismCategory, err = client.Categories.Create(ctx, &prismModels.Category{
			Key:          category.Key,
			Description:  category.Description,
			Value:        category.Value,
			ProjectExtId: category.ProjectExtId,
		})
		if err != nil {
			errorMsg := fmt.Errorf("failed to create category with key %s and value %s. error: %w", categoryIdentifier.Key, categoryIdentifier.Value, err)
			log.Error(errorMsg, "failed to create category")
			return nil, errorMsg
		}
	}
	return prismCategory, nil
}

func isCategoryUsableForProject(category *prismModels.Category, projectExtID string) bool {
	if category == nil {
		return false
	}
	if category.ProjectExtId != nil && *category.ProjectExtId == projectExtID {
		return true
	}
	if category.IsSharedWithAllProjects != nil && *category.IsSharedWithAllProjects {
		return true
	}
	return slices.Contains(category.SharedWithProjects, projectExtID)
}

// GetPrismReferencesOfCategoryIdentifiers resolves the given category identifiers into
// Prism category references without project scoping.
func GetPrismReferencesOfCategoryIdentifiers(
	ctx context.Context,
	client *v4Converged.Client,
	categoryIdentifiers []*infrav1.NutanixCategoryIdentifier,
) ([]vmmconfig.CategoryReference, error) {
	return GetPrismReferencesOfCategoryIdentifiersForProject(ctx, client, categoryIdentifiers, nil)
}

// GetPrismReferencesOfCategoryIdentifiersForProject resolves the given category identifiers
// into Prism category references, scoped to the given project. A nil projectExtID means no
// project scoping; callers pass nil on PC versions older than 7.6.
func GetPrismReferencesOfCategoryIdentifiersForProject(
	ctx context.Context,
	client *v4Converged.Client,
	categoryIdentifiers []*infrav1.NutanixCategoryIdentifier,
	projectExtID *string,
) ([]vmmconfig.CategoryReference, error) {
	log := ctrl.LoggerFrom(ctx)
	categoryExtIds := []string{}

	for _, ci := range categoryIdentifiers {
		if ci == nil {
			return nil, fmt.Errorf("category identifier cannot be nil")
		}
		categories, err := getCategories(ctx, client, ci.Key, ci.Value)
		if err != nil {
			errorMsg := fmt.Errorf("error occurred while to retrieving category value %s in category %s. error: %w", ci.Value, ci.Key, err)
			log.Error(errorMsg, "failed to retrieve category")
			return nil, errorMsg
		}
		prismCategory := getCategoryForProject(categories, projectExtID)
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

// Regex for PC version format 202x.xx.xx.xx.
var pcVersion202xRe = regexp.MustCompile(`^202(\d(\.\d+)+)$`)

// Regex for PC version format 7.xx.xx.
var pcVersion7xRe = regexp.MustCompile(`^7((\.\d+)+)$`)

// is202XPcVersion checks if the version is of the format 202x.xx.xx..
func is202XPcVersion(version string) bool {
	return pcVersion202xRe.MatchString(version)
}

// is7XPcVersion checks if the version is of the format 7.xx.xx..
func is7XPcVersion(version string) bool {
	return pcVersion7xRe.MatchString(version)
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

// ComparePCVersions compares PC version numbers of the format '2024.2.0.1', '7.3', etc.
// Returns 0 : if ver1 == ver2
// Returns 1 : if ver1 > ver2
// Returns -1: if ver1 < ver2
//
// If either version is not in the correct format, they will be the greater,
// unless neither can be parsed in which case they are equal. The case where a
// branch can't be parsed is if the cluster is running master, or some other
// non-release branch. This is only expected in a test/debug situation and is
// the motivation for making an unparseable format greater.
func ComparePCVersions(v1, v2 string) int {
	cleanV1 := CleanPCVersion(v1)
	cleanV2 := CleanPCVersion(v2)

	// Special case for comparing PC versions of format 7.xx and 202x.xx.xx
	if is7XPcVersion(cleanV1) && is202XPcVersion(cleanV2) {
		return 1
	}
	if is7XPcVersion(cleanV2) && is202XPcVersion(cleanV1) {
		return -1
	}

	return CompareVersions(cleanV1, cleanV2)
}

// isPCVersionHigherThan75 returns true if the PC version is >= 7.6. Version may include a "pc." prefix (e.g. "pc.7.6.0.5").
func isPCVersionHigherThan75(version string) bool {
	v := CleanPCVersion(version)
	if v == "" {
		return false
	}
	return CompareVersions(v, "7.6") >= 0
}

// GetDefaultProjectUUID returns the UUID of the default system project
func GetDefaultProjectUUID(rctx *nctx.MachineContext) (string, error) {
	if !isPCVersionHigherThan75(rctx.PCVersion) {
		// PC < 7.6 doesn't support the default project concept
		// Return empty string for legacy behavior (skip project validation)
		return "", nil
	}

	project, err := rctx.ConvergedClient.Projects.GetDefaultProject(rctx.Context)
	if err != nil {
		return "", err
	}
	return *project.ExtId, nil
}

// zeroProjectUUID is a stand-in for the default project used when the real default
// project UUID cannot be resolved (e.g. a project-scoped user on PC 7.6+ without
// permission to read the default project). It never matches a real project, so
// resource-group resolution still proceeds for the user's own project.
const zeroProjectUUID = "00000000-0000-0000-0000-000000000000"

// GetResourceGroupForProject returns the ResourceGroup owned by the given project,
// or nil if none exists. Resource groups are a PC 7.6+ concept.
func GetResourceGroupForProject(ctx context.Context, client *v4Converged.Client, projectExtID string) (*multidomainModels.ResourceGroup, error) {
	resourceGroups, err := client.ResourceGroups.List(ctx, converged.WithFilter(fmt.Sprintf("projectExtId eq '%s'", projectExtID)))
	if err != nil {
		return nil, err
	}
	for i := range resourceGroups {
		rg := &resourceGroups[i]
		if rg.ProjectExtId != nil && *rg.ProjectExtId == projectExtID {
			return rg, nil
		}
	}
	return nil, nil
}

// resolveResourceGroup returns the ResourceGroup for the given project, or nil
// when no project-scoped lookup is required (e.g. the default project). When
// non-nil, downstream helpers will use it instead of cluster-wide APIs.
func resolveResourceGroup(rctx *nctx.MachineContext, effectiveProject *nctx.ProjectInfo) (*multidomainModels.ResourceGroup, error) {
	effectiveProjectExtID := *effectiveProject.ExtID

	defaultUUID, err := GetDefaultProjectUUID(rctx)
	if err != nil {
		// A project-scoped user on PC 7.6+ may not have permission to read the
		// default project. Fall back to the zero UUID for the comparison below so
		// the user's own project-scoped resource group still gets resolved.
		defaultUUID = zeroProjectUUID
	}
	if effectiveProjectExtID == defaultUUID {
		return nil, nil
	}
	rg, err := GetResourceGroupForProject(rctx.Context, rctx.ConvergedClient, effectiveProjectExtID)
	if err != nil {
		return nil, err
	}
	if rg == nil {
		return nil, &terminalError{
			message: fmt.Sprintf("no resource group found for project %s", *effectiveProject.Name),
		}
	}
	return rg, nil
}

// resolveResourceGroupForProjectPolicy resolves the project-scoped resource group implied
// by the cluster's project policy. It returns nil when no project-scoped lookup
// applies: an unrestricted (or unset) policy, a PC version older than 7.6, or the
// default project (which has no project-scoped resource group). It mirrors
// resolveResourceGroup for callers (e.g. the cluster reconciler) that only have the
// project policy rather than a resolved effective project.
// For single-project policy, the projectUUID parameter must be provided.
func resolveResourceGroupForProjectPolicy(ctx context.Context, client *v4Converged.Client, projectPolicy, projectUUID, pcVersion string) (*multidomainModels.ResourceGroup, error) {
	project, err := resolveProjectInfoForPolicy(ctx, client, projectPolicy, projectUUID, pcVersion)
	if err != nil {
		return nil, err
	}
	if project == nil || project.ExtID == nil {
		// Unrestricted policy or PC < 7.6: no project-scoped resource group.
		return nil, nil
	}
	projectExtID := *project.ExtID

	// The default project has no project-scoped resource group. Fall back to the
	// zero UUID if the default project cannot be read (e.g. a project-scoped user
	// on PC 7.6+) so a named project's resource group still gets resolved.
	defaultUUID := zeroProjectUUID
	if defaultProject, derr := client.Projects.GetDefaultProject(ctx); derr == nil && defaultProject.ExtId != nil {
		defaultUUID = *defaultProject.ExtId
	}
	if projectExtID == defaultUUID {
		return nil, nil
	}

	rg, err := GetResourceGroupForProject(ctx, client, projectExtID)
	if err != nil {
		return nil, err
	}
	if rg == nil {
		return nil, &terminalError{
			message: fmt.Sprintf("no resource group found for project %s", projectExtID),
		}
	}
	return rg, nil
}

// GetProjectV4 returns the project info (UUID and name) using the v4 client
func GetProjectV4(rctx *nctx.MachineContext, projectRef *infrav1.NutanixResourceIdentifier) (*nctx.ProjectInfo, error) {
	ctx := rctx.Context
	client := rctx.ConvergedClient
	projectUUID := projectRef.UUID
	projectName := projectRef.Name

	if projectUUID == nil && projectName == nil {
		return nil, fmt.Errorf("name or uuid must be passed in order to retrieve the project")
	}
	if projectUUID != nil {
		project, err := client.Projects.Get(ctx, *projectUUID)
		if err != nil {
			return nil, err
		}
		return &nctx.ProjectInfo{
			ExtID: project.ExtId,
			Name:  project.Name,
		}, nil
	}
	project, err := client.Projects.GetByName(ctx, *projectName)
	if err != nil {
		return nil, err
	}
	return &nctx.ProjectInfo{
		ExtID: project.ExtId,
		Name:  project.Name,
	}, nil
}

// GetProjectV3 returns the project info (UUID and name) using the v3 client
func GetProjectV3(rctx *nctx.MachineContext, projectRef *infrav1.NutanixResourceIdentifier) (*nctx.ProjectInfo, error) {
	ctx := rctx.Context
	client := rctx.NutanixClient
	projectUUID := projectRef.UUID
	projectName := projectRef.Name

	if projectUUID == nil && projectName == nil {
		return nil, fmt.Errorf("name or uuid must be passed in order to retrieve the project")
	}
	if projectUUID != nil {
		projectIntentResponse, err := client.V3.GetProject(ctx, *projectUUID)
		if err != nil {
			if strings.Contains(fmt.Sprint(err), "ENTITY_NOT_FOUND") {
				return nil, &terminalError{message: fmt.Sprintf("failed to find project with UUID %s: %v", *projectUUID, err)}
			}
			return nil, fmt.Errorf("failed to get project with UUID %s: %w", *projectUUID, err)
		}
		return &nctx.ProjectInfo{
			ExtID: projectIntentResponse.Metadata.UUID,
			Name:  &projectIntentResponse.Spec.Name,
		}, nil
	}
	// else search by name
	responseProjects, err := client.V3.ListAllProject(ctx, "")
	if err != nil {
		return nil, err
	}
	foundProjects := make([]*prismclientv3.Project, 0)
	for _, s := range responseProjects.Entities {
		projectSpec := s.Spec
		if strings.EqualFold(projectSpec.Name, *projectName) {
			foundProjects = append(foundProjects, s)
		}
	}
	if len(foundProjects) == 0 {
		return nil, &terminalError{message: fmt.Sprintf("failed to retrieve project by name %s", *projectName)}
	} else if len(foundProjects) > 1 {
		return nil, fmt.Errorf("more than one project found with name %s", *projectName)
	}
	return &nctx.ProjectInfo{
		ExtID: foundProjects[0].Metadata.UUID,
		Name:  &foundProjects[0].Spec.Name,
	}, nil
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
func GetGPUList(ctx context.Context, client *v4Converged.Client, gpus []infrav1.NutanixGPU, peUUID, pcVersion string) ([]vmmconfig.Gpu, error) {
	resultGPUs := make([]vmmconfig.Gpu, 0)
	for _, gpu := range gpus {
		foundGPU, err := GetGPU(ctx, client, peUUID, gpu, pcVersion)
		if err != nil {
			return nil, err
		}
		resultGPUs = append(resultGPUs, *foundGPU)
	}
	return resultGPUs, nil
}

// GetGPU resolves a single GPU for the given Prism Element.
//
// When the GPU is identified by profile (PC 7.6+ only), CAPX only attaches a
// reference to the matching AHV GPU profile (physical or virtual); AHV then
// picks the concrete GPU at power-on according to its own scheduling logic.
//
// Otherwise the legacy device-name / device-ID lookup is used: CAPX lists the matching
// GPUs on the Prism Element, skips any already in use, and selects one at random.
func GetGPU(ctx context.Context, client *v4Converged.Client, peUUID string, gpu infrav1.NutanixGPU, pcVersion string) (*vmmconfig.Gpu, error) {
	if gpu.Type == infrav1.NutanixGPUIdentifierProfile {
		return getGPUFromProfile(ctx, client, peUUID, gpu, pcVersion)
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

// getGPUFromProfile resolves a GPU identified by an AHV GPU profile name or UUID. Profile-based
// GPU assignment uses the named GPU profile APIs, which are only available on PC 7.6 or later.
// It searches both the physical and virtual GPU profile catalogs of the Prism Element and
// returns a GPU whose backing info references the matching profile. The profile identifier must
// resolve to exactly one profile across both catalogs.
func getGPUFromProfile(ctx context.Context, client *v4Converged.Client, peUUID string, gpu infrav1.NutanixGPU, pcVersion string) (*vmmconfig.Gpu, error) {
	if !isPCVersionHigherThan75(pcVersion) {
		return nil, &terminalError{message: fmt.Sprintf(
			"GPU profile %s requires Prism Central 7.6 or later", gpu.Profile.DisplayString())}
	}
	profile := *gpu.Profile

	var profileFilter converged.ODataOption
	switch profile.Type {
	case infrav1.NutanixIdentifierName:
		// Match by profile name server-side via an OData filter. We compare on the exact
		// name (not tolower()) because tolower() silently returns zero results for names
		// containing special characters such as "NVIDIA Corporation GA107GL [A2 / A16]";
		// single quotes are doubled per OData escaping.
		profileFilter = converged.WithFilter(fmt.Sprintf(
			"configuration/name eq '%s'", strings.ReplaceAll(*profile.Name, "'", "''")))
	case infrav1.NutanixIdentifierUUID:
		profileFilter = converged.WithFilter(fmt.Sprintf("extId eq '%s'", *profile.UUID))
	default:
		return nil, &terminalError{message: fmt.Sprintf("unsupported GPU profile identifier %s", profile.DisplayString())}
	}

	physicalProfiles, err := client.Clusters.ListAHVPhysicalGPUProfiles(ctx, peUUID, profileFilter)
	if err != nil {
		return nil, fmt.Errorf("failed to list physical GPU profiles for Prism Element cluster with UUID %s: %w", peUUID, err)
	}

	virtualProfiles, err := client.Clusters.ListAHVVirtualGPUProfiles(ctx, peUUID, profileFilter)
	if err != nil {
		return nil, fmt.Errorf("failed to list virtual GPU profiles for Prism Element cluster with UUID %s: %w", peUUID, err)
	}

	switch total := len(physicalProfiles) + len(virtualProfiles); {
	case total == 0:
		return nil, &terminalError{message: fmt.Sprintf(
			"no GPU profile found with %s in Prism Element cluster with UUID %s", profile.DisplayString(), peUUID)}
	case total > 1:
		return nil, fmt.Errorf(
			"more than one GPU profile found with %s in Prism Element cluster with UUID %s", profile.DisplayString(), peUUID)
	}

	vmGpu := vmmconfig.NewGpu()
	if len(physicalProfiles) == 1 {
		ref := vmmconfig.NewPhysicalGpuProfileReference()
		ref.ExtId = physicalProfiles[0].ExtId
		backing := vmmconfig.NewPhysicalGpu()
		backing.PhysicalGpuProfileReference = ref
		if err := vmGpu.SetBackingInfo(*backing); err != nil {
			return nil, fmt.Errorf("failed to set physical GPU backing info for profile %s: %w", profile.DisplayString(), err)
		}
		return vmGpu, nil
	}

	ref := vmmconfig.NewVirtualGpuProfileReference()
	ref.ExtId = virtualProfiles[0].ExtId
	backing := vmmconfig.NewVirtualGpu()
	backing.VirtualGpuProfileReference = ref
	if err := vmGpu.SetBackingInfo(*backing); err != nil {
		return nil, fmt.Errorf("failed to set virtual GPU backing info for profile %s: %w", profile.DisplayString(), err)
	}
	return vmGpu, nil
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

// GetStorageContainerInCluster resolves a storage container in the given Prism Element.
//
// When rg is non-nil, the lookup is constrained to the project's resource group
// (its placement targets); a storage container that is not reachable from the
// resource group is treated as not authorized for the project. When rg is nil
// (default-project path), it falls back to a cluster-wide client.StorageContainers.List.
func GetStorageContainerInCluster(ctx context.Context, client *v4Converged.Client, rg *multidomainModels.ResourceGroup, storageContainerIdentifier, clusterIdentifier infrav1.NutanixResourceIdentifier) (*clusterModels.StorageContainer, error) {
	// Project-scoped path: resolve against the resource group's placement targets.
	if rg != nil {
		return resolveStorageContainerFromResourceGroup(ctx, client, rg, storageContainerIdentifier, clusterIdentifier)
	}

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

// resolveStorageContainerFromResourceGroup resolves a storage container part of the project's resource group
// (its placement targets) constrained to the given Prism Element. It uses
// the prism-go-client ResourceGroups.ListStorageContainers helper to enumerate the
// storage containers (and their owning Prism Element) from the resource group's
// placement targets.
func resolveStorageContainerFromResourceGroup(ctx context.Context, client *v4Converged.Client, rg *multidomainModels.ResourceGroup, sc, peID infrav1.NutanixResourceIdentifier) (*clusterModels.StorageContainer, error) {
	if rg.ExtId == nil {
		return nil, fmt.Errorf("resource group has no ExtId; cannot resolve storage container")
	}

	storageContainers, err := client.ResourceGroups.ListStorageContainers(ctx, *rg.ExtId)
	if err != nil {
		return nil, fmt.Errorf("failed to list storage containers for resource group %s: %w", *rg.ExtId, err)
	}

	for i := range storageContainers {
		scInfo := &storageContainers[i]
		// Constrain to the requested Prism Element.
		if !prismElementInfoMatches(scInfo.PrismElement, peID) {
			continue
		}
		if storageContainerInfoMatches(scInfo, sc) {
			return &clusterModels.StorageContainer{
				ContainerExtId: ptr.To(scInfo.ExtId),
				Name:           ptr.To(scInfo.Name),
				ClusterExtId:   ptr.To(scInfo.PrismElement.ExtId),
				ClusterName:    ptr.To(scInfo.PrismElement.Name),
			}, nil
		}
	}

	// Resolve the Prism Element name from the resource group's storage container
	// listing for a clearer error; fall back to the identifier only defensively.
	peName := peID.String()
	for i := range storageContainers {
		pe := storageContainers[i].PrismElement
		if prismElementInfoMatches(pe, peID) && pe.Name != "" {
			peName = pe.Name
			break
		}
	}

	return nil, &terminalError{message: fmt.Sprintf(
		"no storage container %s found in resource group which is associated with prism element %s", sc.String(), peName)}
}

// prismElementInfoMatches reports whether the resource-group Prism Element matches
// the given PE identifier (by UUID or name).
func prismElementInfoMatches(pe converged.PrismElementInfo, peID infrav1.NutanixResourceIdentifier) bool {
	switch {
	case peID.IsUUID():
		return pe.ExtId == *peID.UUID
	case peID.IsName():
		return strings.EqualFold(pe.Name, *peID.Name)
	default:
		return false
	}
}

// storageContainerInfoMatches reports whether the resource-group storage container
// matches the given storage container identifier (by UUID or name).
func storageContainerInfoMatches(scInfo *converged.StorageContainerInfo, sc infrav1.NutanixResourceIdentifier) bool {
	switch {
	case sc.IsUUID():
		return scInfo.ExtId == *sc.UUID
	case sc.IsName():
		return strings.EqualFold(scInfo.Name, *sc.Name)
	default:
		return false
	}
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

// clusterVHADomainCategoryExtIds resolves the vHADomain categories (key VHADomainDefaultCategoryKey,
// i.e. "k8s-vha-native-site") declared in the movement groups of the NutanixCluster's owned
// NutanixVirtualHADomain CRs to their Prism Central extIds. Only the categories referenced by this
// cluster's vHADomain CRs are resolved (a handful of targeted lookups), so this stays cheap even when
// Prism Central hosts hundreds of metro clusters sharing the "k8s-vha-native-site" key.
func clusterVHADomainCategoryExtIds(mctx *nctx.MachineContext, ctlclient client.Client) (map[string]struct{}, error) {
	vHADomains, err := getOwnedVHADomains(mctx.Context, ctlclient, mctx.NutanixCluster)
	if err != nil {
		return nil, err
	}

	extIds := map[string]struct{}{}
	for _, vhaDomain := range vHADomains {
		for _, mg := range vhaDomain.Spec.MovementGroups {
			for i := range mg.CategoryRecoveryPlans {
				category := mg.CategoryRecoveryPlans[i].Category
				// Defensive: the vHADomain categories are keyed by VHADomainDefaultCategoryKey; skip
				// anything else so an unexpected mapping cannot inflate the count.
				if category.Key != VHADomainDefaultCategoryKey {
					continue
				}
				prismCategory, err := getCategory(mctx.Context, mctx.ConvergedClient, category.Key, category.Value)
				if err != nil {
					return nil, fmt.Errorf("vHADomain %s: failed to resolve category (key:%s, value:%s) from Prism Central: %w", vhaDomain.Name, category.Key, category.Value, err)
				}
				if prismCategory == nil || prismCategory.ExtId == nil {
					continue
				}
				extIds[*prismCategory.ExtId] = struct{}{}
			}
		}
	}

	return extIds, nil
}

// countVMVHADomainCategories returns the number of categories assigned to the VM that are vHADomain
// categories (key VHADomainDefaultCategoryKey, i.e. "k8s-vha-native-site") belonging to the
// NutanixCluster's own vHADomain CRs. It traverses the VM's assigned category extIds and matches them
// against the cluster's vHADomain category extIds, instead of listing every "k8s-vha-native-site"
// category in Prism Central (which is costly when many metro clusters share the key).
func countVMVHADomainCategories(mctx *nctx.MachineContext, ctlclient client.Client, vm *vmmconfig.Vm) (int, error) {
	vhaExtIds, err := clusterVHADomainCategoryExtIds(mctx, ctlclient)
	if err != nil {
		return 0, err
	}

	count := 0
	for i := range vm.Categories {
		extId := vm.Categories[i].ExtId
		if extId == nil {
			continue
		}
		if _, ok := vhaExtIds[*extId]; ok {
			count++
		}
	}

	return count, nil
}
