/*
Copyright 2025 Nutanix Inc.

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
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/nutanix-cloud-native/prism-go-client/utils"
	prismclientv3 "github.com/nutanix-cloud-native/prism-go-client/v3"

	"k8s.io/utils/ptr"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/conditions"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	"github.com/nutanix-cloud-native/cluster-api-provider-nutanix/controllers"
	nutanixclient "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/pkg/client"
)

func (r *NutanixMachineReconciler) NutanixMachineVMReadyV3(nctx *controllers.NutanixExtendedContext, scope *NutanixMachineScope) (controllers.ExtendedResult, error) {
	log := ctrl.LoggerFrom(nctx.Context)
	vm, err := r.getOrCreateVmV3(nctx, scope)
	if err != nil {
		log.Error(err, fmt.Sprintf("Failed to create VM %s.", scope.Machine.Name))
		return controllers.ExtendedResult{
			Result:      reconcile.Result{},
			ActionError: err,
		}, err
	}
	log.V(1).Info(fmt.Sprintf("Found VM with name: %s, vmUUID: %s", scope.Machine.Name, *vm.Metadata.UUID))
	scope.NutanixMachine.Status.VmUUID = *vm.Metadata.UUID
	nctx.PatchHelper.Patch(nctx.Context, scope.NutanixMachine)

	return controllers.ExtendedResult{
		Result: reconcile.Result{},
	}, nil
}

func (r *NutanixMachineReconciler) getOrCreateVmV3(nctx *controllers.NutanixExtendedContext, scope *NutanixMachineScope) (*prismclientv3.VMIntentResponse, error) {
	var err error
	var vm *prismclientv3.VMIntentResponse
	ctx := nctx.Context
	log := ctrl.LoggerFrom(ctx)
	vmName := scope.Machine.Name
	v3Client := nctx.GetV3Client()

	// Check if the VM already exists
	vm, err = r.FindVmV3(nctx, scope, vmName)
	if err != nil {
		log.Error(err, fmt.Sprintf("error occurred finding VM %s by name or uuid", vmName))
		return nil, err
	}

	// if VM exists
	if vm != nil {
		log.Info(fmt.Sprintf("vm %s found with UUID %s", *vm.Spec.Name, scope.NutanixMachine.Status.VmUUID))
		conditions.MarkTrue(scope.NutanixMachine, infrav1.VMProvisionedCondition)
		return vm, nil
	}

	log.Info(fmt.Sprintf("No existing VM found. Starting creation process of VM %s.", vmName))
	err = r.validateMachineConfig(nctx, scope)
	if err != nil {
		r.setFailureStatus(scope, createErrorFailureReason, err)
		return nil, err
	}

	peUUID, subnetUUIDs, err := r.GetSubnetAndPEUUIDsV3(nctx, scope)
	if err != nil {
		log.Error(err, fmt.Sprintf("failed to get the config for VM %s.", vmName))
		r.setFailureStatus(scope, createErrorFailureReason, err)
		return nil, err
	}

	vmInput := &prismclientv3.VMIntentInput{}
	vmSpec := &prismclientv3.VM{Name: utils.StringPtr(vmName)}

	nicList := make([]*prismclientv3.VMNic, len(subnetUUIDs))
	for idx, subnetUUID := range subnetUUIDs {
		nicList[idx] = &prismclientv3.VMNic{
			SubnetReference: &prismclientv3.Reference{
				UUID: utils.StringPtr(subnetUUID),
				Kind: utils.StringPtr("subnet"),
			},
		}
	}

	// Set Categories to VM Sepc before creating VM
	categories, err := controllers.GetCategoryVMSpec(ctx, v3Client, r.getMachineCategoryIdentifiersV3(nctx, scope))
	if err != nil {
		errorMsg := fmt.Errorf("error occurred while creating category spec for vm %s: %v", vmName, err)
		r.setFailureStatus(scope, createErrorFailureReason, errorMsg)
		return nil, errorMsg
	}

	vmMetadata := &prismclientv3.Metadata{
		Kind:        utils.StringPtr("vm"),
		SpecVersion: utils.Int64Ptr(1),
		Categories:  categories,
	}
	// Set Project in VM Spec before creating VM
	err = r.addVMToProjectV3(nctx, scope, vmMetadata)
	if err != nil {
		errorMsg := fmt.Errorf("error occurred while trying to add VM %s to project: %v", vmName, err)
		r.setFailureStatus(scope, createErrorFailureReason, errorMsg)
		return nil, err
	}

	// Get GPU list
	gpuList, err := controllers.GetGPUList(ctx, v3Client, scope.NutanixMachine.Spec.GPUs, peUUID)
	if err != nil {
		errorMsg := fmt.Errorf("failed to get the GPU list to create the VM %s. %v", vmName, err)
		r.setFailureStatus(scope, createErrorFailureReason, errorMsg)
		return nil, err
	}

	diskList, err := r.getDiskListV3(nctx, scope, peUUID)
	if err != nil {
		errorMsg := fmt.Errorf("failed to get the disk list to create the VM %s. %v", vmName, err)
		r.setFailureStatus(scope, createErrorFailureReason, errorMsg)
		return nil, err
	}

	memorySizeMib := controllers.GetMibValueOfQuantity(scope.NutanixMachine.Spec.MemorySize)
	vmSpec.Resources = &prismclientv3.VMResources{
		PowerState:            utils.StringPtr("ON"),
		HardwareClockTimezone: utils.StringPtr("UTC"),
		NumVcpusPerSocket:     utils.Int64Ptr(int64(scope.NutanixMachine.Spec.VCPUsPerSocket)),
		NumSockets:            utils.Int64Ptr(int64(scope.NutanixMachine.Spec.VCPUSockets)),
		MemorySizeMib:         utils.Int64Ptr(memorySizeMib),
		NicList:               nicList,
		DiskList:              diskList,
		GpuList:               gpuList,
	}
	vmSpec.ClusterReference = &prismclientv3.Reference{
		Kind: utils.StringPtr("cluster"),
		UUID: utils.StringPtr(peUUID),
	}

	if err := r.addGuestCustomizationToVMV3(nctx, scope, vmSpec); err != nil {
		errorMsg := fmt.Errorf("error occurred while adding guest customization to vm spec: %v", err)
		r.setFailureStatus(scope, createErrorFailureReason, errorMsg)
		return nil, err
	}

	// Set BootType in VM Spec before creating VM
	err = r.addBootTypeToVMV3(nctx, scope, vmSpec)
	if err != nil {
		errorMsg := fmt.Errorf("error occurred while adding boot type to vm spec: %v", err)
		r.setFailureStatus(scope, createErrorFailureReason, errorMsg)
		return nil, err
	}

	vmInput.Spec = vmSpec
	vmInput.Metadata = vmMetadata
	// Create the actual VM/Machine
	log.Info(fmt.Sprintf("Creating VM with name %s for cluster %s", vmName, scope.NutanixCluster.Name))
	vmResponse, err := v3Client.V3.CreateVM(ctx, vmInput)
	if err != nil {
		errorMsg := fmt.Errorf("failed to create VM %s. error: %v", vmName, err)
		r.setFailureStatus(scope, createErrorFailureReason, errorMsg)
		return nil, err
	}

	if vmResponse == nil || vmResponse.Metadata == nil || vmResponse.Metadata.UUID == nil || *vmResponse.Metadata.UUID == "" {
		errorMsg := fmt.Errorf("no valid VM UUID found in response after creating vm %s", scope.Machine.Name)
		r.setFailureStatus(scope, createErrorFailureReason, errorMsg)
		return nil, errorMsg
	}
	vmUuid := *vmResponse.Metadata.UUID
	// set the VM UUID on the nutanix machine as soon as it is available. VM UUID can be used for cleanup in case of failure
	scope.NutanixMachine.Spec.ProviderID = controllers.GenerateProviderID(vmUuid)
	scope.NutanixMachine.Status.VmUUID = vmUuid

	log.V(1).Info(fmt.Sprintf("Sent the post request to create VM %s. Got the vm UUID: %s, status.state: %s", vmName, vmUuid, *vmResponse.Status.State))
	log.V(1).Info(fmt.Sprintf("Getting task vmUUID for VM %s", vmName))
	lastTaskUUID, err := controllers.GetTaskUUIDFromVM(vmResponse)
	if err != nil {
		errorMsg := fmt.Errorf("error occurred fetching task UUID from vm %s after creation: %v", scope.Machine.Name, err)
		r.setFailureStatus(scope, createErrorFailureReason, errorMsg)
		return nil, errorMsg
	}

	if lastTaskUUID == "" {
		errorMsg := fmt.Errorf("failed to retrieve task UUID for VM %s after creation", vmName)
		r.setFailureStatus(scope, createErrorFailureReason, errorMsg)
		return nil, errorMsg
	}

	log.Info(fmt.Sprintf("Waiting for task %s to get completed for VM %s", lastTaskUUID, scope.NutanixMachine.Name))
	if err := nutanixclient.WaitForTaskToSucceed(ctx, v3Client, lastTaskUUID); err != nil {
		errorMsg := fmt.Errorf("error occurred while waiting for task %s to start: %v", lastTaskUUID, err)
		r.setFailureStatus(scope, createErrorFailureReason, errorMsg)
		return nil, errorMsg
	}

	log.Info("Fetching VM after creation")
	vm, err = controllers.FindVMByUUID(ctx, v3Client, vmUuid)
	if err != nil {
		errorMsg := fmt.Errorf("error occurred while getting VM %s after creation: %v", vmName, err)
		r.setFailureStatus(scope, createErrorFailureReason, errorMsg)
		return nil, errorMsg
	}

	conditions.MarkTrue(scope.NutanixMachine, infrav1.VMProvisionedCondition)
	return vm, nil
}

func (r *NutanixMachineReconciler) FindVmV3(nctx *controllers.NutanixExtendedContext, scope *NutanixMachineScope, vmName string) (*prismclientv3.VMIntentResponse, error) {
	ctx := nctx.Context
	v3Client := nctx.GetV3Client()
	return controllers.FindVM(ctx, v3Client, scope.NutanixMachine, vmName)
}

func (r *NutanixMachineReconciler) addGuestCustomizationToVMV3(nctx *controllers.NutanixExtendedContext, scope *NutanixMachineScope, vmSpec *prismclientv3.VM) error {
	// Get the bootstrapData
	bootstrapRef := scope.NutanixMachine.Spec.BootstrapRef
	if bootstrapRef != nil && bootstrapRef.Kind == infrav1.NutanixMachineBootstrapRefKindSecret {
		bootstrapData, err := r.getBootstrapData(nctx, scope)
		if err != nil {
			return err
		}

		// Encode the bootstrapData by base64
		bsdataEncoded := base64.StdEncoding.EncodeToString(bootstrapData)
		metadata := fmt.Sprintf("{\"hostname\": \"%s\", \"uuid\": \"%s\"}", scope.Machine.Name, uuid.New())
		metadataEncoded := base64.StdEncoding.EncodeToString([]byte(metadata))

		vmSpec.Resources.GuestCustomization = &prismclientv3.GuestCustomization{
			IsOverridable: utils.BoolPtr(true),
			CloudInit: &prismclientv3.GuestCustomizationCloudInit{
				UserData: utils.StringPtr(bsdataEncoded),
				MetaData: utils.StringPtr(metadataEncoded),
			},
		}
	}

	return nil
}

func (r *NutanixMachineReconciler) getDiskListV3(nctx *controllers.NutanixExtendedContext, scope *NutanixMachineScope, peUUID string) ([]*prismclientv3.VMDisk, error) {
	diskList := make([]*prismclientv3.VMDisk, 0)

	systemDisk, err := r.getSystemDiskV3(nctx, scope)
	if err != nil {
		return nil, err
	}
	diskList = append(diskList, systemDisk)

	bootstrapRef := scope.NutanixMachine.Spec.BootstrapRef
	if bootstrapRef != nil && bootstrapRef.Kind == infrav1.NutanixMachineBootstrapRefKindImage {
		bootstrapDisk, err := r.getBootstrapDiskV3(nctx, scope)
		if err != nil {
			return nil, err
		}

		diskList = append(diskList, bootstrapDisk)
	}

	dataDisks, err := r.getDataDisksV3(nctx, scope, peUUID)
	if err != nil {
		return nil, err
	}
	diskList = append(diskList, dataDisks...)

	return diskList, nil
}

func (r *NutanixMachineReconciler) getSystemDiskV3(nctx *controllers.NutanixExtendedContext, scope *NutanixMachineScope) (*prismclientv3.VMDisk, error) {
	var nodeOSImage *prismclientv3.ImageIntentResponse
	var err error
	ctx := nctx.Context
	v3Client := nctx.GetV3Client()

	if scope.NutanixMachine.Spec.Image != nil {
		nodeOSImage, err = controllers.GetImage(
			ctx,
			v3Client,
			*scope.NutanixMachine.Spec.Image,
		)
	} else if scope.NutanixMachine.Spec.ImageLookup != nil {
		nodeOSImage, err = controllers.GetImageByLookup(
			ctx,
			v3Client,
			scope.NutanixMachine.Spec.ImageLookup.Format,
			&scope.NutanixMachine.Spec.ImageLookup.BaseOS,
			scope.Machine.Spec.Version,
		)
	}
	if err != nil {
		errorMsg := fmt.Errorf("failed to get system disk image %q: %w", scope.NutanixMachine.Spec.Image, err)
		r.setFailureStatus(scope, createErrorFailureReason, errorMsg)
		return nil, err
	}

	// Consider this a precaution. If the image is marked for deletion after we
	// create the "VM create" task, then that task will fail. We will handle that
	// failure separately.
	if controllers.ImageMarkedForDeletion(nodeOSImage) {
		err := fmt.Errorf("system disk image %s is being deleted", *nodeOSImage.Metadata.UUID)
		r.setFailureStatus(scope, createErrorFailureReason, err)
		return nil, err
	}

	systemDiskSizeMib := controllers.GetMibValueOfQuantity(scope.NutanixMachine.Spec.SystemDiskSize)
	systemDisk, err := controllers.CreateSystemDiskSpec(*nodeOSImage.Metadata.UUID, systemDiskSizeMib)
	if err != nil {
		errorMsg := fmt.Errorf("error occurred while creating system disk spec: %w", err)
		r.setFailureStatus(scope, createErrorFailureReason, errorMsg)
		return nil, err
	}

	return systemDisk, nil
}

func (r *NutanixMachineReconciler) getBootstrapDiskV3(nctx *controllers.NutanixExtendedContext, scope *NutanixMachineScope) (*prismclientv3.VMDisk, error) {
	ctx := nctx.Context
	v3Client := nctx.GetV3Client()

	if scope.NutanixMachine.Spec.BootstrapRef == nil {
		return nil, fmt.Errorf("bootstrapRef is nil, cannot create bootstrap disk")
	}

	bootstrapImageRef := infrav1.NutanixResourceIdentifier{
		Type: infrav1.NutanixIdentifierName,
		Name: ptr.To(scope.NutanixMachine.Spec.BootstrapRef.Name),
	}
	bootstrapImage, err := controllers.GetImage(ctx, v3Client, bootstrapImageRef)
	if err != nil {
		errorMsg := fmt.Errorf("failed to get bootstrap disk image %q: %w", bootstrapImageRef, err)
		r.setFailureStatus(scope, createErrorFailureReason, errorMsg)
		return nil, err
	}

	// Consider this a precaution. If the image is marked for deletion after we
	// create the "VM create" task, then that task will fail. We will handle that
	// failure separately.
	if controllers.ImageMarkedForDeletion(bootstrapImage) {
		err := fmt.Errorf("bootstrap disk image %s is being deleted", *bootstrapImage.Metadata.UUID)
		r.setFailureStatus(scope, createErrorFailureReason, err)
		return nil, err
	}

	bootstrapDisk := &prismclientv3.VMDisk{
		DeviceProperties: &prismclientv3.VMDiskDeviceProperties{
			DeviceType: ptr.To(deviceTypeCDROM),
			DiskAddress: &prismclientv3.DiskAddress{
				AdapterType: ptr.To(adapterTypeIDE),
				DeviceIndex: ptr.To(int64(0)),
			},
		},
		DataSourceReference: &prismclientv3.Reference{
			Kind: ptr.To(strings.ToLower(infrav1.NutanixMachineBootstrapRefKindImage)),
			UUID: bootstrapImage.Metadata.UUID,
		},
	}

	return bootstrapDisk, nil
}

func (r *NutanixMachineReconciler) getDataDisksV3(nctx *controllers.NutanixExtendedContext, scope *NutanixMachineScope, peUUID string) ([]*prismclientv3.VMDisk, error) {
	ctx := nctx.Context
	v3Client := nctx.GetV3Client()

	dataDisks, err := controllers.CreateDataDiskList(ctx, v3Client, scope.NutanixMachine.Spec.DataDisks, peUUID)
	if err != nil {
		errorMsg := fmt.Errorf("error occurred while creating data disk spec: %w", err)
		r.setFailureStatus(scope, createErrorFailureReason, errorMsg)
		return nil, err
	}

	return dataDisks, nil
}

func (r *NutanixMachineReconciler) addBootTypeToVMV3(_ *controllers.NutanixExtendedContext, scope *NutanixMachineScope, vmSpec *prismclientv3.VM) error {
	bootType := scope.NutanixMachine.Spec.BootType
	// Defaults to legacy if boot type is not set.
	if bootType != "" {
		if bootType != infrav1.NutanixBootTypeLegacy && bootType != infrav1.NutanixBootTypeUEFI {
			errorMsg := fmt.Errorf("boot type must be %s or %s but was %s", string(infrav1.NutanixBootTypeLegacy), string(infrav1.NutanixBootTypeUEFI), bootType)
			conditions.MarkFalse(scope.NutanixMachine, infrav1.VMProvisionedCondition, infrav1.VMBootTypeInvalid, capiv1.ConditionSeverityError, "%s", errorMsg.Error())
			return errorMsg
		}

		// Only modify VM spec if boot type is UEFI. Otherwise, assume default Legacy mode
		if bootType == infrav1.NutanixBootTypeUEFI {
			vmSpec.Resources.BootConfig = &prismclientv3.VMBootConfig{
				BootType: utils.StringPtr(strings.ToUpper(string(bootType))),
			}
		}
	}

	return nil
}

func (r *NutanixMachineReconciler) addVMToProjectV3(nctx *controllers.NutanixExtendedContext, scope *NutanixMachineScope, vmMetadata *prismclientv3.Metadata) error {
	log := ctrl.LoggerFrom(nctx.Context)
	ctx := nctx.Context
	v3Client := nctx.GetV3Client()
	vmName := scope.Machine.Name
	projectRef := scope.NutanixMachine.Spec.Project
	if projectRef == nil {
		log.V(1).Info("Not linking VM to a project")
		return nil
	}

	if vmMetadata == nil {
		errorMsg := fmt.Errorf("metadata cannot be nil when adding VM %s to project", vmName)
		log.Error(errorMsg, "failed to add vm to project")
		conditions.MarkFalse(scope.NutanixMachine, infrav1.ProjectAssignedCondition, infrav1.ProjectAssignationFailed, capiv1.ConditionSeverityError, "%s", errorMsg.Error())
		return errorMsg
	}

	projectUUID, err := controllers.GetProjectUUID(ctx, v3Client, projectRef.Name, projectRef.UUID)
	if err != nil {
		errorMsg := fmt.Errorf("error occurred while searching for project for VM %s: %v", vmName, err)
		log.Error(errorMsg, "error occurred while searching for project")
		conditions.MarkFalse(scope.NutanixMachine, infrav1.ProjectAssignedCondition, infrav1.ProjectAssignationFailed, capiv1.ConditionSeverityError, "%s", errorMsg.Error())
		return errorMsg
	}

	vmMetadata.ProjectReference = &prismclientv3.Reference{
		Kind: utils.StringPtr(projectKind),
		UUID: utils.StringPtr(projectUUID),
	}
	conditions.MarkTrue(scope.NutanixMachine, infrav1.ProjectAssignedCondition)
	return nil
}

func (r *NutanixMachineReconciler) getMachineCategoryIdentifiersV3(nctx *controllers.NutanixExtendedContext, scope *NutanixMachineScope) []*infrav1.NutanixCategoryIdentifier {
	log := ctrl.LoggerFrom(nctx.Context)
	ctx := nctx.Context
	v3Client := nctx.GetV3Client()
	categoryIdentifiers := controllers.GetDefaultCAPICategoryIdentifiers(scope.Cluster.Name)
	// Only try to create default categories. ignoring error so that we can return all including
	// additionalCategories as well
	_, err := controllers.GetOrCreateCategories(ctx, v3Client, categoryIdentifiers)
	if err != nil {
		log.Error(err, "Failed to getOrCreateCategories")
	}

	additionalCategories := scope.NutanixMachine.Spec.AdditionalCategories
	if len(additionalCategories) > 0 {
		for _, at := range additionalCategories {
			additionalCat := at
			categoryIdentifiers = append(categoryIdentifiers, &additionalCat)
		}
	}

	return categoryIdentifiers
}

func (r *NutanixMachineReconciler) GetSubnetAndPEUUIDsV3(nctx *controllers.NutanixExtendedContext, scope *NutanixMachineScope) (string, []string, error) {
	if scope == nil {
		return "", nil, fmt.Errorf("cannot create machine config if machine scope is nil")
	}

	ctx := nctx.Context
	v3Client := nctx.GetV3Client()

	peUUID, err := controllers.GetPEUUID(ctx, v3Client, scope.NutanixMachine.Spec.Cluster.Name, scope.NutanixMachine.Spec.Cluster.UUID)
	if err != nil {
		return "", nil, err
	}

	subnetUUIDs, err := controllers.GetSubnetUUIDList(ctx, v3Client, scope.NutanixMachine.Spec.Subnets, peUUID)
	if err != nil {
		return "", nil, err
	}

	return peUUID, subnetUUIDs, nil
}
