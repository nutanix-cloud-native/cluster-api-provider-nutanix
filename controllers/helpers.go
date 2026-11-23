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
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	multidomainModels "github.com/nutanix/ntnx-api-golang-clients/multidomain-go-client/v4/models/multidomain/v4/config"
	prismModels "github.com/nutanix/ntnx-api-golang-clients/prism-go-client/v4/models/prism/v4/config"
	vmmconfig "github.com/nutanix/ntnx-api-golang-clients/vmm-go-client/v4/models/vmm/v4/ahv/config"
	volumesconfig "github.com/nutanix/ntnx-api-golang-clients/volumes-go-client/v4/models/volumes/v4/config"
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
	"github.com/nutanix-cloud-native/cluster-api-provider-nutanix/pkg/services"
	"github.com/nutanix-cloud-native/prism-go-client/converged"
	v4Converged "github.com/nutanix-cloud-native/prism-go-client/converged/v4"
	prismclientv3 "github.com/nutanix-cloud-native/prism-go-client/v3"
)

const (
	providerIdPrefix = "nutanix://"

	detachVGRequeueAfter = 30 * time.Second

	// failedVMCreateRetryAfter is how long CAPX waits before minting a new
	// vm-creation request id after a Prism CreateVm task fails with no adopted VM.
	// Reusing the failed request id forever replays the same FAILED task.
	failedVMCreateRetryAfter = 30 * time.Minute

	// maxFailedVMCreateAttempts is how many confirmed Prism CreateVm task failures
	// CAPX tolerates before setting failureReason. Attempts 1..max-1 wait
	// failedVMCreateRetryAfter and retry. The next confirmed failure is terminal.
	// Prism task text is not classified: "not enough cores", quota, and invalid
	// config all look the same, and capacity errors can become retryable.
	maxFailedVMCreateAttempts = 4

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

	// VMCreateRetryAfterAnnotation is an RFC3339 timestamp. While it is in the future, CAPX
	// must not mint a new create request-id or call Prism CreateVm. The annotation is durable
	// across reconcile re-entry and controller restarts; RequeueAfter alone is not.
	VMCreateRetryAfterAnnotation = "capx.nutanix.com/vm-create-retry-after"

	// VMCreateFailureCountAnnotation counts confirmed Prism CreateVm task failures
	// that did not adopt a VM. It survives the retry-after window so a later failure
	// can become terminal. Cleared once a VM is adopted or created.
	VMCreateFailureCountAnnotation = "capx.nutanix.com/vm-create-failure-count"

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
	vmCustomAttributePrefix4FailureDomain           = "failure-domain:"

	drConfigEntityType    = "entity_dr_config"
	drRoleDecoupled       = "kDecoupled"
	drConfigRoleAttribute = "role"

	// skippedDecoupledVMUUIDAnnotation records the Machine's original VM UUID when
	// delete skipped it because DR marked the VM decoupled (Metro UPFO).
	skippedDecoupledVMUUIDAnnotation = "capx.nutanix.com/skipped-decoupled-vm-uuid"
	// recoveredVMUUIDAnnotation is the non-decoupled recovered VM CAPX will delete
	// instead of the decoupled original.
	recoveredVMUUIDAnnotation = "capx.nutanix.com/recovered-vm-uuid"
)

// nutanixResourceIdentifierSpecified reports whether nri identifies a Prism resource.
// A zero-value identifier (typical when NutanixMachine.spec.cluster is omitted on a
// topology/metro template) is treated as unset so callers can inherit from a FailureDomain.
func nutanixResourceIdentifierSpecified(nri infrav1.NutanixResourceIdentifier) bool {
	return nri.IsName() || nri.IsUUID()
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

// delayedReconcileError tells reconcileNormal to requeue after a fixed delay
// without treating the create failure as an immediate controller-runtime error
// (which would race the delay with exponential backoff).
type delayedReconcileError struct {
	after time.Duration
	err   error
}

func (e *delayedReconcileError) Error() string {
	if e == nil || e.err == nil {
		return "delayed reconcile"
	}
	return e.err.Error()
}

func (e *delayedReconcileError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func requeueAfterFromError(err error) (time.Duration, bool) {
	var delayed *delayedReconcileError
	if errors.As(err, &delayed) && delayed.after > 0 {
		return delayed.after, true
	}
	return 0, false
}

// isPrismTaskFailed reports whether err is a confirmed Prism async task failure.
// Operation.Wait returns "task <uuid> failed: ..."; Results returns ErrTaskFailed.
func isPrismTaskFailed(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, converged.ErrTaskFailed) {
		return true
	}
	msg := err.Error()
	// Match prism-go-client Operation.Wait failure / cancel strings. Avoid matching
	// transport errors like "failed to get task <uuid>: ...".
	if strings.Contains(msg, "task ") && strings.Contains(msg, " failed:") {
		return true
	}
	return strings.Contains(msg, "task ") && strings.Contains(msg, " was canceled")
}

// nowFunc is overridable in unit tests for backoff timing.
var nowFunc = time.Now

// remainingVMCreateBackoff returns how long CAPX must wait before another CreateVm
// attempt. ok is false when there is no backoff, the annotation is expired, or it is
// unparseable (fail open so a bad value cannot block forever).
func remainingVMCreateBackoff(nm *infrav1.NutanixMachine, now time.Time) (time.Duration, bool) {
	if nm == nil || nm.Annotations == nil {
		return 0, false
	}
	raw := nm.Annotations[VMCreateRetryAfterAnnotation]
	if raw == "" {
		return 0, false
	}
	retryAt, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return 0, false
	}
	remaining := retryAt.Sub(now)
	if remaining <= 0 {
		return 0, false
	}
	return remaining, true
}

// vmCreateFailureCount returns the persisted count of confirmed CreateVm task
// failures. Missing or unparseable values count as zero so a bad annotation
// cannot force an immediate terminal failure.
func vmCreateFailureCount(nm *infrav1.NutanixMachine) int {
	if nm == nil || nm.Annotations == nil {
		return 0
	}
	raw := nm.Annotations[VMCreateFailureCountAnnotation]
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0
	}
	return n
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

	if services.IsPCVersionHigherThan75(pcVersion) && projectExtID != nil && !isVMUsableForProject(response, *projectExtID) {
		return nil, services.NewTerminalError(fmt.Sprintf(
			"VM with UUID %s is not in project %s", uuid, *projectExtID))
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

// FindVM retrieves the VM with the given uuid or name within the specified project.
// On Metro/MetroSite, a UUID miss (or a DR-decoupled leftover) is resolved the same
// way as reconcileDelete: list by Machine/NutanixMachine name and skip decoupled VMs.
func FindVM(ctx context.Context, client *v4Converged.Client, machine *capiv1beta2.Machine, nutanixMachine *infrav1.NutanixMachine, vmName string, projectExtID *string, pcVersion string, v3Client prismclientv3.Service) (*vmmconfig.Vm, error) {
	log := ctrl.LoggerFrom(ctx)
	vmUUID, err := GetVMUUID(machine, nutanixMachine)
	if err != nil {
		return nil, err
	}
	metro := useMetroDRDeletePath(&nctx.MachineContext{Machine: machine, NutanixMachine: nutanixMachine})
	nmName := ""
	if nutanixMachine != nil {
		nmName = nutanixMachine.Name
	}

	if vmUUID == "" {
		log.Info(fmt.Sprintf("Searching for VM %s using name", vmName))
		if metro {
			return findNonDecoupledVMByName(ctx, client, v3Client, vmNamesForDelete(vmName, nmName), projectExtID, pcVersion)
		}
		vm, err := FindVMByName(ctx, client, vmName, projectExtID, pcVersion)
		if err != nil {
			log.Error(err, fmt.Sprintf("error occurred finding VM %s by name", vmName))
			return nil, err
		}
		return vm, nil
	}

	log.V(1).Info(fmt.Sprintf("Searching for VM %s using UUID %s", vmName, vmUUID))
	vm, err := FindVMByUUID(ctx, client, vmUUID, projectExtID, pcVersion)
	if err != nil {
		return nil, err
	}
	if vm != nil {
		if vm.Name == nil {
			return nil, fmt.Errorf("found VM with UUID %s but name was empty", vmUUID)
		}
		if *vm.Name != vmName && *vm.Name != nmName {
			return nil, fmt.Errorf("found VM with UUID %s but name %s did not match %s", vmUUID, *vm.Name, vmName)
		}
		if !metro {
			return vm, nil
		}
		decoupled, err := isVMDecoupled(ctx, v3Client, vmUUID)
		if err != nil {
			return nil, err
		}
		if !decoupled {
			return vm, nil
		}
		log.Info(fmt.Sprintf("VM %s with UUID %s is decoupled; looking for a recovered VM by name", vmName, vmUUID))
	} else if metro {
		log.Info(fmt.Sprintf("VM UUID %s returned 404; confirming non-existence by name %s", vmUUID, vmName))
	}

	if metro {
		live, err := findNonDecoupledVMByName(ctx, client, v3Client, vmNamesForDelete(vmName, nmName), projectExtID, pcVersion)
		if err != nil {
			return nil, err
		}
		if live != nil {
			return live, nil
		}
	}
	return nil, fmt.Errorf("no vm %s found with UUID %s but was expected to be present", vmName, vmUUID)
}

// FindVMByName retrieves the VM with the given vm name. Project filtering is only
// applied on PC >= 7.6; for older PC versions project scoping is unsupported.
func FindVMByName(ctx context.Context, client *v4Converged.Client, vmName string, projectExtID *string, pcVersion string) (*vmmconfig.Vm, error) {
	log := ctrl.LoggerFrom(ctx)

	var filter string
	if services.IsPCVersionHigherThan75(pcVersion) && projectExtID != nil {
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

	if vms[0].ExtId == nil {
		return nil, fmt.Errorf("vm with name %s has a nil ExtId", vmName)
	}

	return FindVMByUUID(ctx, client, *vms[0].ExtId, projectExtID, pcVersion)
}

func groupsFieldValue(entity *prismclientv3.GroupsEntity, name string) string {
	if entity == nil {
		return ""
	}
	for _, data := range entity.Data {
		if data == nil || data.Name != name {
			continue
		}
		for _, pair := range data.Values {
			if pair == nil || len(pair.Values) == 0 {
				continue
			}
			return pair.Values[0]
		}
	}
	return ""
}

// useMetroDRDeletePath reports whether reconcileDelete may call v3 Groups
// (entity_dr_config) to skip DR-decoupled VMs. kDecoupled is a Metro UPFO signal.
// Groups is not project-scoped and is denied for least-privilege / Projects 2.0
// users, so non-metro delete must stay UUID-only even when Groups is down.
func useMetroDRDeletePath(rctx *nctx.MachineContext) bool {
	if rctx == nil {
		return false
	}
	if rctx.Machine != nil && (isNutanixMetroFailureDomain(rctx.Machine.Spec.FailureDomain) ||
		isNutanixMetroSiteFailureDomain(rctx.Machine.Spec.FailureDomain)) {
		return true
	}
	if rctx.NutanixMachine == nil {
		return false
	}
	if fd := ptr.Deref(rctx.NutanixMachine.Status.FailureDomain, ""); isNutanixMetroFailureDomain(fd) ||
		isNutanixMetroSiteFailureDomain(fd) {
		return true
	}
	if nutanixMachineAnnotation(rctx.NutanixMachine, metroActivePlacementPEAnnotation) != "" {
		return true
	}
	return nutanixMachineAnnotation(rctx.NutanixMachine, skippedDecoupledVMUUIDAnnotation) != "" ||
		nutanixMachineAnnotation(rctx.NutanixMachine, recoveredVMUUIDAnnotation) != ""
}

func nutanixV3Service(rctx *nctx.MachineContext) prismclientv3.Service {
	if rctx == nil || rctx.NutanixClient == nil {
		return nil
	}
	return rctx.NutanixClient.V3
}

// isVMDecoupled reports whether Prism Central DR config marks the VM as decoupled.
// A decoupled VM must not be deleted by CAPX; Disaster Recovery owns it.
// Callers must only use this on the Metro delete path (see useMetroDRDeletePath).
func isVMDecoupled(ctx context.Context, v3Client prismclientv3.Service, vmUUID string) (bool, error) {
	if v3Client == nil || vmUUID == "" {
		return false, nil
	}

	resp, err := v3Client.GroupsGetEntities(ctx, &prismclientv3.GroupsGetEntitiesRequest{
		EntityType:     ptr.To(drConfigEntityType),
		FilterCriteria: fmt.Sprintf("entity_uuid=in=%s", vmUUID),
		GroupMemberAttributes: []*prismclientv3.GroupsRequestedAttribute{
			{Attribute: ptr.To(drConfigRoleAttribute)},
		},
	})
	if err != nil {
		return false, fmt.Errorf("failed to get DR config for VM %s: %w", vmUUID, err)
	}
	if resp == nil {
		return false, nil
	}

	for _, group := range resp.GroupResults {
		if group == nil {
			continue
		}
		for _, entity := range group.EntityResults {
			if groupsFieldValue(entity, drConfigRoleAttribute) == drRoleDecoupled {
				return true, nil
			}
		}
	}
	return false, nil
}

// vmsUsableForProject drops VMs outside the given project. It is a no-op when projectExtID is
// nil or the PC version predates project scoping (< 7.6).
func vmsUsableForProject(ctx context.Context, vms []*vmmconfig.Vm, projectExtID *string, pcVersion string) []*vmmconfig.Vm {
	if projectExtID == nil || !services.IsPCVersionHigherThan75(pcVersion) {
		return vms
	}
	log := ctrl.LoggerFrom(ctx)
	usable := make([]*vmmconfig.Vm, 0, len(vms))
	for _, vm := range vms {
		if !isVMUsableForProject(vm, *projectExtID) {
			log.Info(fmt.Sprintf("skipping VM %s with UUID %s: not in project %s", ptr.Deref(vm.Name, ""), ptr.Deref(vm.ExtId, ""), *projectExtID))
			continue
		}
		usable = append(usable, vm)
	}
	return usable
}

// findNonDecoupledVMByName lists VMs with the given names and returns the single
// VM that is not DR-decoupled. Returns nil if only decoupled VMs exist (or none).
// When projectExtID is non-nil and the PC version supports projects (>= 7.6), VMs outside
// that project are skipped, mirroring the project filter FindVMByName applies.
func findNonDecoupledVMByName(ctx context.Context, client *v4Converged.Client, v3Client prismclientv3.Service, names []string, projectExtID *string, pcVersion string) (*vmmconfig.Vm, error) {
	log := ctrl.LoggerFrom(ctx)
	seen := map[string]struct{}{}
	live := make([]*vmmconfig.Vm, 0)

	for _, name := range names {
		if name == "" {
			continue
		}
		vms, err := client.VMs.List(ctx, converged.WithFilter(fmt.Sprintf("name eq '%s'", name)))
		if err != nil {
			return nil, fmt.Errorf("failed to list VMs named %s: %w", name, err)
		}
		for i := range vms {
			vm := vms[i]
			if vm.ExtId == nil || *vm.ExtId == "" {
				continue
			}
			uuid := *vm.ExtId
			if _, ok := seen[uuid]; ok {
				continue
			}
			seen[uuid] = struct{}{}

			decoupled, err := isVMDecoupled(ctx, v3Client, uuid)
			if err != nil {
				return nil, err
			}
			if decoupled {
				log.Info(fmt.Sprintf("skipping decoupled VM %s with UUID %s", name, uuid))
				continue
			}

			full, err := FindVMByUUID(ctx, client, uuid, nil, pcVersion)
			if err != nil {
				return nil, err
			}
			if full != nil {
				live = append(live, full)
			}
		}
	}

	live = vmsUsableForProject(ctx, live, projectExtID, pcVersion)
	if len(live) == 0 {
		return nil, nil
	}
	if len(live) > 1 {
		return nil, fmt.Errorf("found more than one (%d) non-decoupled VMs with names %v", len(live), names)
	}
	return live[0], nil
}

func nutanixMachineAnnotation(nm *infrav1.NutanixMachine, key string) string {
	if nm == nil || nm.Annotations == nil {
		return ""
	}
	return nm.Annotations[key]
}

func setNutanixMachineAnnotation(nm *infrav1.NutanixMachine, key, value string) {
	if nm.Annotations == nil {
		nm.Annotations = map[string]string{}
	}
	nm.Annotations[key] = value
}

func deleteNutanixMachineAnnotation(nm *infrav1.NutanixMachine, key string) {
	if nm == nil || nm.Annotations == nil {
		return
	}
	delete(nm.Annotations, key)
}

func vmNamesForDelete(machineName, nutanixMachineName string) []string {
	names := []string{machineName}
	if nutanixMachineName != "" && nutanixMachineName != machineName {
		names = append(names, nutanixMachineName)
	}
	return names
}

func VmHasTaskInProgress(ctx context.Context, client *v4Converged.Client, vmExtId string, projectExtID *string, pcVersion string) (bool, error) {
	log := ctrl.LoggerFrom(ctx)
	if vmExtId == "" {
		return false, fmt.Errorf("cannot extract task uuid for empty vm extId")
	}

	log.V(1).Info(fmt.Sprintf("Getting task uuid for vm %s", vmExtId))

	var filterString string
	if services.IsPCVersionHigherThan75(pcVersion) && projectExtID != nil {
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
		if volumeGroup.VolumeGroupExtId == nil {
			log.Info(fmt.Sprintf("skipping volume group with nil ExtId on virtual machine %s", vmName))
			continue
		}
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
			// Prism returns this when the key-value was created between List and Create.
			// The response has no category body; the existing extId is only in the message:
			// "400 InvalidArgument: Operation failed as a category with the provided key and value already exists with extId: <extId>"
			extID, alreadyExists, parseErr := existingCategoryExtID(err)
			if parseErr != nil {
				return nil, parseErr
			}
			if alreadyExists {
				log.V(1).Info(fmt.Sprintf("Category with key %s and value %s already exists.", categoryIdentifier.Key, categoryIdentifier.Value))
				if prismCategory != nil && prismCategory.ExtId != nil {
					return prismCategory, nil
				}
				if extID == "" {
					return nil, fmt.Errorf("category with key %s and value %s already exists, but the error did not include an extId: %w", categoryIdentifier.Key, categoryIdentifier.Value, err)
				}
				category.ExtId = ptr.To(extID)
				return category, nil
			}
			errorMsg := fmt.Errorf("failed to create category with key %s and value %s. error: %w", categoryIdentifier.Key, categoryIdentifier.Value, err)
			log.Error(errorMsg, "failed to create category")
			return nil, errorMsg
		}
	}
	return prismCategory, nil
}

const categoryAlreadyExistsMessage = "category with the provided key and value already exists"

const categoryAlreadyExistsExtIDPattern = `already exists with extId:\s*(\S+)`

// existingCategoryExtID reports whether err is Prism's "category already exists"
// response and, when present, the extId embedded in that message.
func existingCategoryExtID(err error) (string, bool, error) {
	if err == nil || !strings.Contains(err.Error(), categoryAlreadyExistsMessage) {
		return "", false, nil
	}
	re, compileErr := regexp.Compile(categoryAlreadyExistsExtIDPattern)
	if compileErr != nil {
		return "", false, fmt.Errorf("failed to compile category already-exists regex: %w", compileErr)
	}
	matches := re.FindStringSubmatch(err.Error())
	if len(matches) < 2 {
		return "", true, nil
	}
	return strings.TrimRight(matches[1], ".,;"), true, nil
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
			errorMsg := services.NewTerminalError(fmt.Sprintf("category value %s not found in category %s", ci.Value, ci.Key))
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

// resolveProjectInfoForPolicy resolves the project (ExtID + Name) implied by a cluster's
// project policy. It returns nil when no project scoping applies: an unrestricted (or
// unset) policy, or a PC version older than 7.6. The returned ProjectInfo always carries
// both ExtID and Name when available. For single-project policy, the projectUUID parameter
// must be provided.
func resolveProjectInfoForPolicy(ctx context.Context, client *v4Converged.Client, projectPolicy, projectUUID, pcVersion string) (*nctx.ProjectInfo, error) {
	if !services.IsPCVersionHigherThan75(pcVersion) {
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

// GetDefaultProjectUUID returns the UUID of the default system project
func GetDefaultProjectUUID(rctx *nctx.MachineContext) (string, error) {
	if !services.IsPCVersionHigherThan75(rctx.PCVersion) {
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
		return nil, services.NewTerminalError(
			fmt.Sprintf("no resource group found for project %s", *effectiveProject.Name),
		)
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
		return nil, services.NewTerminalError(
			fmt.Sprintf("no resource group found for project %s", projectExtID),
		)
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
				return nil, services.NewTerminalError(fmt.Sprintf("failed to find project with UUID %s: %v", *projectUUID, err))
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
		return nil, services.NewTerminalError(fmt.Sprintf("failed to retrieve project by name %s", *projectName))
	} else if len(foundProjects) > 1 {
		return nil, fmt.Errorf("more than one project found with name %s", *projectName)
	}
	return &nctx.ProjectInfo{
		ExtID: foundProjects[0].Metadata.UUID,
		Name:  &foundProjects[0].Spec.Name,
	}, nil
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
