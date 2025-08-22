/*
Copyright 2025 Nutanix, Inc.

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
package nutanixmachine

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	apitypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	"github.com/nutanix-cloud-native/cluster-api-provider-nutanix/controllers"
)

const (
	createErrorFailureReason = "CreateError"
)

func (r *NutanixMachineReconciler) FatalPrismCondtion(nctx *controllers.NutanixExtendedContext, scope *NutanixMachineScope) (controllers.ExtendedResult, error) {
	log := ctrl.LoggerFrom(nctx.Context)

	err := fmt.Errorf("no supported API found for NutanixMachine reconciliation")
	log.Error(err, "no supported API found for NutanixMachine reconciliation")

	return controllers.ExtendedResult{
		Result:        reconcile.Result{RequeueAfter: 5 * time.Minute},
		StopReconcile: true,
		ActionError:   err,
	}, err
}

func (r *NutanixMachineReconciler) AddFinalizer(nctx *controllers.NutanixExtendedContext, scope *NutanixMachineScope) (controllers.ExtendedResult, error) {
	// Add finalizer first if not exist to avoid the race condition
	if !ctrlutil.ContainsFinalizer(scope.NutanixMachine, infrav1.NutanixMachineFinalizer) {
		updated := ctrlutil.AddFinalizer(scope.NutanixMachine, infrav1.NutanixMachineFinalizer)
		if !updated {
			err := fmt.Errorf("failed to add finalizer")
			return controllers.ExtendedResult{
				Result:      reconcile.Result{},
				ActionError: err,
			}, err
		}
	}

	// Remove deprecated finalizer
	ctrlutil.RemoveFinalizer(scope.NutanixMachine, infrav1.DeprecatedNutanixMachineFinalizer)
	return controllers.ExtendedResult{
		Result: reconcile.Result{},
	}, nil
}

func (r *NutanixMachineReconciler) validateMachineConfig(nctx *controllers.NutanixExtendedContext, scope *NutanixMachineScope) error {
	log := ctrl.LoggerFrom(nctx.Context)

	fdName := scope.Machine.Spec.FailureDomain
	if fdName != nil && *fdName != "" {
		log.WithValues("failureDomain", *fdName)
		fdObj, err := r.validateFailureDomainRef(nctx, scope, *fdName)
		if err != nil {
			log.Error(err, "Failed to validate the failure domain")
			return err
		}

		// Update the NutanixMachine machine config based on the failure domain spec
		scope.NutanixMachine.Spec.Cluster = fdObj.Spec.PrismElementCluster
		scope.NutanixMachine.Spec.Subnets = fdObj.Spec.Subnets
		scope.NutanixMachine.Status.FailureDomain = &fdObj.Name
		log.Info(fmt.Sprintf("Updated the NutanixMachine %s machine config from the failure domain %s configuration.", scope.NutanixMachine.Name, fdObj.Name))
	}

	if len(scope.NutanixMachine.Spec.Subnets) == 0 {
		return fmt.Errorf("at least one subnet is needed to create the VM %s", scope.NutanixMachine.Name)
	}
	if (scope.NutanixMachine.Spec.Cluster.Name == nil || *scope.NutanixMachine.Spec.Cluster.Name == "") &&
		(scope.NutanixMachine.Spec.Cluster.UUID == nil || *scope.NutanixMachine.Spec.Cluster.UUID == "") {
		return fmt.Errorf("cluster name or uuid are required to create the VM %s", scope.NutanixMachine.Name)
	}

	diskSize := scope.NutanixMachine.Spec.SystemDiskSize
	// Validate disk size
	if diskSize.Cmp(minMachineSystemDiskSize) < 0 {
		diskSizeMib := controllers.GetMibValueOfQuantity(diskSize)
		minMachineSystemDiskSizeMib := controllers.GetMibValueOfQuantity(minMachineSystemDiskSize)
		return fmt.Errorf("minimum systemDiskSize is %vMib but given %vMib", minMachineSystemDiskSizeMib, diskSizeMib)
	}

	memorySize := scope.NutanixMachine.Spec.MemorySize
	// Validate memory size
	if memorySize.Cmp(minMachineMemorySize) < 0 {
		memorySizeMib := controllers.GetMibValueOfQuantity(memorySize)
		minMachineMemorySizeMib := controllers.GetMibValueOfQuantity(minMachineMemorySize)
		return fmt.Errorf("minimum memorySize is %vMib but given %vMib", minMachineMemorySizeMib, memorySizeMib)
	}

	vcpusPerSocket := scope.NutanixMachine.Spec.VCPUsPerSocket
	if vcpusPerSocket < int32(minVCPUsPerSocket) {
		return fmt.Errorf("minimum vcpus per socket is %v but given %v", minVCPUsPerSocket, vcpusPerSocket)
	}

	vcpuSockets := scope.NutanixMachine.Spec.VCPUSockets
	if vcpuSockets < int32(minVCPUSockets) {
		return fmt.Errorf("minimum vcpu sockets is %v but given %v", minVCPUSockets, vcpuSockets)
	}

	dataDisks := scope.NutanixMachine.Spec.DataDisks
	if dataDisks != nil {
		if err := r.validateDataDisks(dataDisks); err != nil {
			return err
		}
	}

	return nil
}

func (r *NutanixMachineReconciler) validateFailureDomainRef(nctx *controllers.NutanixExtendedContext, scope *NutanixMachineScope, fdName string) (*infrav1.NutanixFailureDomain, error) {
	// Fetch the referent failure domain object
	fdObj, err := r.getFailureDomainObj(nctx, scope, fdName)
	if err != nil {
		return nil, err
	}

	ctx := nctx.Context
	v3Client := nctx.GetV3Client()

	// Validate the failure domain configuration
	pe := fdObj.Spec.PrismElementCluster
	peUUID, err := controllers.GetPEUUID(ctx, v3Client, pe.Name, pe.UUID)
	if err != nil {
		return nil, err
	}

	subnets := fdObj.Spec.Subnets
	_, err = controllers.GetSubnetUUIDList(ctx, v3Client, subnets, peUUID)
	if err != nil {
		return nil, err
	}

	return fdObj, nil
}

func (r *NutanixMachineReconciler) getFailureDomainObj(nctx *controllers.NutanixExtendedContext, scope *NutanixMachineScope, fdName string) (*infrav1.NutanixFailureDomain, error) {
	fdObj := &infrav1.NutanixFailureDomain{}
	fdKey := client.ObjectKey{Name: fdName, Namespace: scope.NutanixMachine.Namespace}
	if err := nctx.Client.Get(nctx.Context, fdKey, fdObj); err != nil {
		return nil, fmt.Errorf("failed to fetch the referent failure domain object %q: %w", fdName, err)
	}
	return fdObj, nil
}

func (r *NutanixMachineReconciler) validateDataDisks(dataDisks []infrav1.NutanixMachineVMDisk) error {
	errors := []error{}
	for _, disk := range dataDisks {

		if disk.DiskSize.Cmp(minMachineDataDiskSize) < 0 {
			diskSizeMib := controllers.GetMibValueOfQuantity(disk.DiskSize)
			minMachineDataDiskSizeMib := controllers.GetMibValueOfQuantity(minMachineDataDiskSize)
			errors = append(errors, fmt.Errorf("minimum data disk size is %vMib but given %vMib", minMachineDataDiskSizeMib, diskSizeMib))
		}

		if disk.DeviceProperties != nil {
			errors = validateDataDiskDeviceProperties(disk, errors)
		}

		if disk.DataSource != nil {
			errors = validateDataDiskDataSource(disk, errors)
		}

		if disk.StorageConfig != nil {
			errors = validateDataDiskStorageConfig(disk, errors)
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("data disks validation errors: %v", errors)
	}

	return nil
}

func validateDataDiskStorageConfig(disk infrav1.NutanixMachineVMDisk, errors []error) []error {
	if disk.StorageConfig.StorageContainer != nil && disk.StorageConfig.StorageContainer.IsUUID() {
		if disk.StorageConfig.StorageContainer.UUID == nil {
			errors = append(errors, fmt.Errorf("name or uuid is required for storage container in data disk"))
		} else {
			if _, err := uuid.Parse(*disk.StorageConfig.StorageContainer.UUID); err != nil {
				errors = append(errors, fmt.Errorf("invalid UUID for storage container in data disk: %v", err))
			}
		}
	}

	if disk.StorageConfig.StorageContainer != nil &&
		disk.StorageConfig.StorageContainer.IsName() &&
		disk.StorageConfig.StorageContainer.Name == nil {
		errors = append(errors, fmt.Errorf("name or uuid is required for storage container in data disk"))
	}

	if disk.StorageConfig.DiskMode != infrav1.NutanixMachineDiskModeFlash && disk.StorageConfig.DiskMode != infrav1.NutanixMachineDiskModeStandard {
		errors = append(errors, fmt.Errorf("invalid disk mode %s for data disk", disk.StorageConfig.DiskMode))
	}
	return errors
}

func validateDataDiskDataSource(disk infrav1.NutanixMachineVMDisk, errors []error) []error {
	if disk.DataSource.Type == infrav1.NutanixIdentifierUUID && disk.DataSource.UUID == nil {
		errors = append(errors, fmt.Errorf("UUID is required for data disk with UUID source"))
	}

	if disk.DataSource.Type == infrav1.NutanixIdentifierName && disk.DataSource.Name == nil {
		errors = append(errors, fmt.Errorf("name is required for data disk with name source"))
	}
	return errors
}

func validateDataDiskDeviceProperties(disk infrav1.NutanixMachineVMDisk, errors []error) []error {
	validAdapterTypes := map[infrav1.NutanixMachineDiskAdapterType]bool{
		infrav1.NutanixMachineDiskAdapterTypeIDE:   false,
		infrav1.NutanixMachineDiskAdapterTypeSCSI:  false,
		infrav1.NutanixMachineDiskAdapterTypeSATA:  false,
		infrav1.NutanixMachineDiskAdapterTypePCI:   false,
		infrav1.NutanixMachineDiskAdapterTypeSPAPR: false,
	}

	switch disk.DeviceProperties.DeviceType {
	case infrav1.NutanixMachineDiskDeviceTypeDisk:
		validAdapterTypes[infrav1.NutanixMachineDiskAdapterTypeSCSI] = true
		validAdapterTypes[infrav1.NutanixMachineDiskAdapterTypePCI] = true
		validAdapterTypes[infrav1.NutanixMachineDiskAdapterTypeSPAPR] = true
		validAdapterTypes[infrav1.NutanixMachineDiskAdapterTypeSATA] = true
		validAdapterTypes[infrav1.NutanixMachineDiskAdapterTypeIDE] = true
	case infrav1.NutanixMachineDiskDeviceTypeCDRom:
		validAdapterTypes[infrav1.NutanixMachineDiskAdapterTypeIDE] = true
		validAdapterTypes[infrav1.NutanixMachineDiskAdapterTypePCI] = true
	default:
		errors = append(errors, fmt.Errorf("invalid device type %s for data disk", disk.DeviceProperties.DeviceType))
	}

	if !validAdapterTypes[disk.DeviceProperties.AdapterType] {
		errors = append(errors, fmt.Errorf("invalid adapter type %s for data disk", disk.DeviceProperties.AdapterType))
	}

	if disk.DeviceProperties.DeviceIndex < 0 {
		errors = append(errors, fmt.Errorf("invalid device index %d for data disk", disk.DeviceProperties.DeviceIndex))
	}
	return errors
}

func (r *NutanixMachineReconciler) setFailureStatus(scope *NutanixMachineScope, reason string, err error) {
	scope.NutanixMachine.Status.FailureReason = &reason
	scope.NutanixMachine.Status.FailureMessage = ptr.To(err.Error())
}

// getBootstrapData returns the Bootstrap data from the ref secret
func (r *NutanixMachineReconciler) getBootstrapData(nctx *controllers.NutanixExtendedContext, scope *NutanixMachineScope) ([]byte, error) {
	if scope.NutanixMachine.Spec.BootstrapRef == nil {
		return nil, fmt.Errorf("NutanixMachine spec.BootstrapRef is nil")
	}

	secretName := scope.NutanixMachine.Spec.BootstrapRef.Name
	secret := &corev1.Secret{}
	secretKey := apitypes.NamespacedName{
		Namespace: scope.NutanixMachine.Spec.BootstrapRef.Namespace,
		Name:      secretName,
	}
	if err := nctx.Client.Get(nctx.Context, secretKey, secret); err != nil {
		return nil, fmt.Errorf("failed to retrieve bootstrap data secret %s: %w", secretName, err)
	}

	value, ok := secret.Data["value"]
	if !ok {
		return nil, fmt.Errorf("error retrieving bootstrap data: secret value key is missing")
	}

	return value, nil
}
