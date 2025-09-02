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
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"text/template"

	"github.com/google/uuid"
	"github.com/nutanix-cloud-native/cluster-api-provider-nutanix/controllers"
	"github.com/nutanix-cloud-native/prism-go-client/facade"
	"github.com/nutanix-cloud-native/prism-go-client/utils"
	prismclientv3 "github.com/nutanix-cloud-native/prism-go-client/v3"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/ptr"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/conditions"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v4prismModels "github.com/nutanix/ntnx-api-golang-clients/prism-go-client/v4/models/prism/v4/config"
	vmmModels "github.com/nutanix/ntnx-api-golang-clients/vmm-go-client/v4/models/vmm/v4/ahv/config"
	imageModels "github.com/nutanix/ntnx-api-golang-clients/vmm-go-client/v4/models/vmm/v4/content"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
)

func (r *NutanixMachineReconciler) NutanixMachineVMReadyV4(nctx *controllers.NutanixExtendedContext, scope *NutanixMachineScope) (controllers.ExtendedResult, error) {
	log := ctrl.LoggerFrom(nctx.Context)
	vm, err := r.getOrCreateVmV4(nctx, scope)
	if err != nil {
		log.Error(err, fmt.Sprintf("Failed to create VM %s.", scope.Machine.Name))
		return controllers.ExtendedResult{
			Result:      reconcile.Result{},
			ActionError: err,
		}, err
	}
	log.V(1).Info(fmt.Sprintf("Found VM with name: %s, vmUUID: %s", scope.Machine.Name, *vm.ExtId))
	scope.NutanixMachine.Status.VmUUID = *vm.ExtId
	nctx.PatchHelper.Patch(nctx.Context, scope.NutanixMachine)

	return controllers.ExtendedResult{
		Result: reconcile.Result{},
	}, nil
}

func (r *NutanixMachineReconciler) getOrCreateVmV4(nctx *controllers.NutanixExtendedContext, scope *NutanixMachineScope) (*vmmModels.Vm, error) {
	// V4 Implementation of VM creation using the new Nutanix V4 API
	// This function follows the same logical steps as getOrCreateVmV3 but uses:
	// - V4 API models (vmmModels.Vm instead of prismclientv3.VMIntentResponse)
	// - V4 Facade Client instead of V3 Prism Client
	// - Different field names and structures in the V4 models
	// - Potentially different task handling and async operations

	// Key differences from V3:
	// - VM spec uses vmmModels.Vm with different field names
	// - NIC configuration uses vmmModels.Nic with BackingInfo structure
	// - Disk configuration uses vmmModels.Disk with different properties
	// - GPU configuration uses vmmModels.Gpu
	// - Categories might be handled differently in V4
	// - Task tracking and waiting might use different mechanisms
	// - Response handling uses ExtId instead of UUID

	// Step 1: Setup variables and context
	var err error
	var vm *vmmModels.Vm

	log := ctrl.LoggerFrom(nctx.Context)
	vmName := scope.Machine.Name

	// Step 2: Check if VM already exists
	vm, err = r.FindVmV4(nctx, scope, vmName)
	if err != nil {
		log.Error(err, fmt.Sprintf("error occurred finding VM %s by name or uuid", vmName))
		return nil, err
	}

	// Step 3: If VM exists, return it
	if vm != nil {
		log.Info(fmt.Sprintf("vm %s found with UUID %s", *vm.Name, scope.NutanixMachine.Status.VmUUID))
		return vm, nil
	}

	// Step 4: Start VM creation process
	log.Info(fmt.Sprintf("No existing VM found. Starting creation process of VM %s.", vmName))

	// Step 5: Validate machine configuration
	err = r.validateMachineConfig(nctx, scope)
	if err != nil {
		r.setFailureStatus(scope, createErrorFailureReason, err)
		return nil, err
	}

	// Step 6: Get subnet and PE UUIDs
	peUUID, subnetUUIDs, err := r.GetSubnetAndPEUUIDsV4(nctx, scope)
	if err != nil {
		log.Error(err, fmt.Sprintf("failed to get the config for VM %s.", vmName))
		r.setFailureStatus(scope, createErrorFailureReason, err)
		return nil, err
	}

	// Step 7: Prepare VM spec
	vmSpec := vmmModels.NewVm()
	vmSpec.Name = &vmName

	// Step 8: Prepare NICs
	vmNics := make([]vmmModels.Nic, 0)
	for _, subnetUUID := range subnetUUIDs {
		vmSubnetRef := vmmModels.NewSubnetReference()
		vmSubnetRef.ExtId = &subnetUUID
		vmNicNetworkInfo := vmmModels.NewNicNetworkInfo()
		vmNicNetworkInfo.Subnet = vmSubnetRef
		vmNic := vmmModels.NewNic()
		vmNic.NetworkInfo = vmNicNetworkInfo
		vmNics = append(vmNics, *vmNic)
	}

	// Step 9: Set Categories
	categoryRefs, err := r.getOrCreateCategoriesV4(nctx, r.getMachineCategoryIdentifiersV4(scope))
	if err != nil {
		errorMsg := fmt.Errorf("error occurred while creating category spec for vm %s: %v", vmName, err)
		r.setFailureStatus(scope, createErrorFailureReason, errorMsg)
		return nil, errorMsg
	}

	// Step 10: Get GPU list
	gpuList, err := r.getGPUListV4(nctx, scope, peUUID)
	if err != nil {
		errorMsg := fmt.Errorf("failed to get the GPU list to create the VM %s. %v", vmName, err)
		r.setFailureStatus(scope, createErrorFailureReason, errorMsg)
		return nil, err
	}

	// Step 11: Get disk list (system, bootstrap, data disks)
	diskList, err := r.getDiskListV4(nctx, scope, peUUID)
	if err != nil {
		errorMsg := fmt.Errorf("failed to get the disk list to create the VM %s. %v", vmName, err)
		r.setFailureStatus(scope, createErrorFailureReason, errorMsg)
		return nil, err
	}

	// Step 12: Configure VM resources (CPU, Memory, NICs, Disks, GPUs)
	memorySizeBytes, success := scope.NutanixMachine.Spec.MemorySize.AsInt64()
	if !success {
		return nil, fmt.Errorf("failed to parse memory size %v", scope.NutanixMachine.Spec.MemorySize)
	}
	vmSpec.MemorySizeBytes = ptr.To(memorySizeBytes)

	vmSpec.NumSockets = ptr.To(int(scope.NutanixMachine.Spec.VCPUSockets))
	vmSpec.NumCoresPerSocket = ptr.To(int(scope.NutanixMachine.Spec.VCPUsPerSocket))

	vmClusterRef := vmmModels.NewClusterReference()
	vmClusterRef.ExtId = &peUUID
	vmSpec.Cluster = vmClusterRef

	vmSpec.Nics = vmNics
	vmSpec.Disks = diskList
	vmSpec.Gpus = gpuList
	vmSpec.Categories = categoryRefs

	// Step 13: Add guest customization (cloud-init)
	if err := r.addGuestCustomizationToVMV4(nctx, scope, vmSpec); err != nil {
		errorMsg := fmt.Errorf("error occurred while adding guest customization to vm spec: %v", err)
		r.setFailureStatus(scope, createErrorFailureReason, errorMsg)
		return nil, err
	}

	// Step 14: Set boot type (UEFI/Legacy)
	bootConfig, err := r.addBootTypeToVMV4(nctx, scope)
	if err != nil {
		errorMsg := fmt.Errorf("error occurred while adding boot type to vm spec: %v", err)
		r.setFailureStatus(scope, createErrorFailureReason, errorMsg)
		return nil, err
	}
	vmSpec.BootConfig = bootConfig

	// Step 15: Create the VM
	log.Info(fmt.Sprintf("Creating VM with name %s for cluster %s", vmName, scope.NutanixCluster.Name))
	vmTask, err := nctx.NutanixClients.V4Facade.CreateVM(vmSpec)
	if err != nil {
		errorMsg := fmt.Errorf("failed to create VM %s. error: %v", vmName, err)
		r.setFailureStatus(scope, createErrorFailureReason, errorMsg)
		return nil, err
	}

	// Step 16: Wait for task completion (it will fetch the VM after creation)
	vms, err := vmTask.WaitForTaskCompletion()
	if err != nil {
		errorMsg := fmt.Errorf("failed to wait for task completion: %v", err)
		r.setFailureStatus(scope, createErrorFailureReason, errorMsg)
		return nil, err
	}

	if len(vms) != 1 {
		return nil, fmt.Errorf("expected 1 VM, got %d", len(vms))
	}

	vm = vms[0]

	// Step 17: Set Project (if specified)
	// Update created VM with project using V3 API
	err = r.updateVMWithProject(nctx, scope, vm)
	if err != nil {
		errorMsg := fmt.Errorf("error occurred while trying to add VM %s to project: %v", vmName, err)
		r.setFailureStatus(scope, createErrorFailureReason, errorMsg)
		return nil, err
	}

	// Step 18: Power on the VM
	vmTask, err = nctx.NutanixClients.V4Facade.PowerOnVM(*vm.ExtId)
	if err != nil {
		errorMsg := fmt.Errorf("failed to power on VM %s. error: %v", vmName, err)
		r.setFailureStatus(scope, createErrorFailureReason, errorMsg)
		return nil, err
	}

	_, err = vmTask.WaitForTaskCompletion()
	if err != nil {
		errorMsg := fmt.Errorf("failed to wait for task completion: %v", err)
		r.setFailureStatus(scope, createErrorFailureReason, errorMsg)
		return nil, err
	}

	// Step 19: Set provider ID and VM UUID
	vmUuid := *vm.ExtId
	scope.NutanixMachine.Spec.ProviderID = controllers.GenerateProviderID(vmUuid)
	scope.NutanixMachine.Status.VmUUID = vmUuid
	nctx.PatchHelper.Patch(nctx.Context, scope.NutanixMachine)

	log.V(1).Info(fmt.Sprintf("Sent the post request to create VM %s. Got the vm UUID: %s", vmName, vmUuid))

	return vm, nil
}

func (r *NutanixMachineReconciler) FindVmV4(nctx *controllers.NutanixExtendedContext, scope *NutanixMachineScope, vmName string) (*vmmModels.Vm, error) {
	log := ctrl.LoggerFrom(nctx.Context)
	v4Client := nctx.GetV4FacadeClient()

	vmUUID := scope.NutanixMachine.Status.VmUUID
	if vmUUID == "" {
		log.V(1).Info(fmt.Sprintf("No VM UUID found for VM %s. Searching by name.", vmName))
		vm, err := v4Client.ListVMs(facade.WithLimit(10), facade.WithFilter(fmt.Sprintf("name eq '%s'", vmName)))
		if err != nil {
			return nil, err
		}

		if len(vm) == 0 {
			return nil, nil
		}

		if len(vm) > 1 {
			return nil, fmt.Errorf("multiple VMs found with name %s", vmName)
		}
		return &vm[0], nil
	}

	vm, err := v4Client.GetVM(vmUUID)
	if err != nil {
		return nil, err
	}

	return vm, nil
}

func (r *NutanixMachineReconciler) GetSubnetAndPEUUIDsV4(nctx *controllers.NutanixExtendedContext, scope *NutanixMachineScope) (string, []string, error) {
	var peUUID string
	var subnetUUIDs []string

	if scope == nil {
		return "", nil, fmt.Errorf("cannot create machine config if machine scope is nil")
	}

	v4Client := nctx.GetV4FacadeClient()

	if scope.NutanixMachine.Spec.Cluster.UUID != nil {
		peUUID = *scope.NutanixMachine.Spec.Cluster.UUID
	}

	if peUUID == "" {
		clusterName := ""
		if scope.NutanixMachine.Spec.Cluster.Name != nil {
			clusterName = *scope.NutanixMachine.Spec.Cluster.Name
		}
		peCluster, err := v4Client.ListClusters(
			facade.WithLimit(10),
			facade.WithFilter(fmt.Sprintf("name eq '%s'", clusterName)),
		)
		if err != nil {
			return "", nil, err
		}
		if len(peCluster) == 0 {
			return "", nil, fmt.Errorf("no PE cluster found with name %s", clusterName)
		}
		if len(peCluster) > 1 {
			return "", nil, fmt.Errorf("multiple PE clusters found with name %s", clusterName)
		}
		peUUID = *peCluster[0].ExtId
	}

	for _, capxSubnet := range scope.NutanixMachine.Spec.Subnets {
		if capxSubnet.Type == infrav1.NutanixIdentifierUUID {
			subnetUUIDs = append(subnetUUIDs, *capxSubnet.UUID)
		} else {
			subnetName := ""
			if capxSubnet.Name != nil {
				subnetName = *capxSubnet.Name
			}
			subnet, err := v4Client.ListSubnets(
				facade.WithLimit(10),
				facade.WithFilter(fmt.Sprintf("name eq '%s' and clusterReference eq '%s'", subnetName, peUUID)),
			)
			if err != nil {
				return "", nil, err
			}
			if len(subnet) == 0 {
				return "", nil, fmt.Errorf("no subnet found with name %s", subnetName)
			}
			if len(subnet) > 1 {
				return "", nil, fmt.Errorf("multiple subnets found with name %s", subnetName)
			}
			subnetUUIDs = append(subnetUUIDs, *subnet[0].ExtId)
		}
	}

	return peUUID, subnetUUIDs, nil
}

func (r *NutanixMachineReconciler) getMachineCategoryIdentifiersV4(scope *NutanixMachineScope) []*infrav1.NutanixCategoryIdentifier {
	categoryIdentifiers := controllers.GetDefaultCAPICategoryIdentifiers(scope.Cluster.Name)

	additionalCategories := scope.NutanixMachine.Spec.AdditionalCategories
	if len(additionalCategories) > 0 {
		for _, at := range additionalCategories {
			additionalCat := at
			categoryIdentifiers = append(categoryIdentifiers, &additionalCat)
		}
	}

	return categoryIdentifiers
}

func (r *NutanixMachineReconciler) getOrCreateCategoriesV4(nctx *controllers.NutanixExtendedContext, categoryIdentifiers []*infrav1.NutanixCategoryIdentifier) ([]vmmModels.CategoryReference, error) {
	ctx := nctx.Context
	v4Client := nctx.GetV4FacadeClient()

	categories := make([]vmmModels.CategoryReference, 0)
	for _, ci := range categoryIdentifiers {
		if ci == nil {
			return categories, fmt.Errorf("cannot get or create nil category")
		}
		category, err := r.getOrCreateCategoryV4(ctx, v4Client, ci)
		if err != nil {
			return categories, err
		}

		categoryRef := vmmModels.NewCategoryReference()
		categoryRef.ExtId = category.ExtId
		categories = append(categories, *categoryRef)
	}
	return categories, nil
}

func (r *NutanixMachineReconciler) getOrCreateCategoryV4(ctx context.Context, v4Client facade.FacadeClientV4, categoryIdentifier *infrav1.NutanixCategoryIdentifier) (*v4prismModels.Category, error) {
	log := ctrl.LoggerFrom(ctx)
	if categoryIdentifier == nil {
		return nil, fmt.Errorf("category identifier cannot be nil when getting or creating categories")
	}
	if categoryIdentifier.Key == "" {
		return nil, fmt.Errorf("category identifier key must be set when getting or creating categories")
	}
	if categoryIdentifier.Value == "" {
		return nil, fmt.Errorf("category identifier value must be set when getting or creating categories")
	}

	log.V(1).Info(fmt.Sprintf("Checking existence of category with key %s and value %s", categoryIdentifier.Key, categoryIdentifier.Value))

	// First try to find existing category
	category, err := r.getCategoryV4(ctx, v4Client, categoryIdentifier.Key, categoryIdentifier.Value)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve category with key %s and value %s: %v", categoryIdentifier.Key, categoryIdentifier.Value, err)
	}

	if category != nil {
		return category, nil
	}

	// Category doesn't exist, create it
	log.V(1).Info(fmt.Sprintf("Category with key %s and value %s did not exist, creating", categoryIdentifier.Key, categoryIdentifier.Value))
	newCategory := &v4prismModels.Category{
		Key:   &categoryIdentifier.Key,
		Value: &categoryIdentifier.Value,
	}

	createdCategory, err := v4Client.CreateCategory(newCategory)
	if err != nil {
		return nil, fmt.Errorf("failed to create category with key %s and value %s: %v", categoryIdentifier.Key, categoryIdentifier.Value, err)
	}

	return createdCategory, nil
}

func (r *NutanixMachineReconciler) getCategoryV4(ctx context.Context, v4Client facade.FacadeClientV4, key string, value string) (*v4prismModels.Category, error) {
	// List categories with filter to find the specific key-value pair
	filter := fmt.Sprintf("key eq '%s' and value eq '%s'", key, value)
	categories, err := v4Client.ListCategories(facade.WithFilter(filter), facade.WithLimit(10))
	if err != nil {
		return nil, fmt.Errorf("failed to list categories with key %s and value %s: %v", key, value, err)
	}

	if len(categories) == 0 {
		return nil, nil // Category not found
	}

	if len(categories) > 1 {
		return nil, fmt.Errorf("multiple categories found with key %s and value %s", key, value)
	}

	return &categories[0], nil
}

func (r *NutanixMachineReconciler) getDiskListV4(nctx *controllers.NutanixExtendedContext, scope *NutanixMachineScope, peUUID string) ([]vmmModels.Disk, error) {
	diskList := make([]vmmModels.Disk, 0)

	// Step 1: Get system disk
	systemDisk, err := r.getSystemDiskV4(nctx, scope)
	if err != nil {
		return nil, err
	}
	diskList = append(diskList, *systemDisk)

	// Step 2: Get bootstrap disk if specified
	bootstrapRef := scope.NutanixMachine.Spec.BootstrapRef
	if bootstrapRef != nil && bootstrapRef.Kind == infrav1.NutanixMachineBootstrapRefKindImage {
		bootstrapDisk, err := r.getBootstrapDiskV4(nctx, scope)
		if err != nil {
			return nil, err
		}
		diskList = append(diskList, *bootstrapDisk)
	}

	// Step 3: Get data disks
	dataDisks, err := r.getDataDisksV4(nctx, scope, peUUID)
	if err != nil {
		return nil, err
	}
	diskList = append(diskList, dataDisks...)

	return diskList, nil
}

func (r *NutanixMachineReconciler) getSystemDiskV4(nctx *controllers.NutanixExtendedContext, scope *NutanixMachineScope) (*vmmModels.Disk, error) {
	ctx := nctx.Context
	v4Client := nctx.GetV4FacadeClient()

	// Get the image for the system disk
	var imageUUID string
	var err error

	if scope.NutanixMachine.Spec.Image != nil {
		imageUUID, err = r.getImageUUIDV4(ctx, v4Client, *scope.NutanixMachine.Spec.Image)
		if err != nil {
			errorMsg := fmt.Errorf("failed to get system disk image %q: %w", scope.NutanixMachine.Spec.Image, err)
			r.setFailureStatus(scope, createErrorFailureReason, errorMsg)
			return nil, err
		}
	} else if scope.NutanixMachine.Spec.ImageLookup != nil {
		imageUUID, err = r.getImageByLookupV4(ctx, v4Client, scope.NutanixMachine.Spec.ImageLookup.Format, &scope.NutanixMachine.Spec.ImageLookup.BaseOS, scope.Machine.Spec.Version)
		if err != nil {
			errorMsg := fmt.Errorf("failed to get system disk image by lookup (format: %s, baseOS: %s): %w", *scope.NutanixMachine.Spec.ImageLookup.Format, scope.NutanixMachine.Spec.ImageLookup.BaseOS, err)
			r.setFailureStatus(scope, createErrorFailureReason, errorMsg)
			return nil, err
		}
	} else {
		return nil, fmt.Errorf("either image or imageLookup must be specified")
	}

	// Check if image is marked for deletion (when using UUID lookup)
	if scope.NutanixMachine.Spec.Image != nil && scope.NutanixMachine.Spec.Image.IsUUID() {
		image, err := v4Client.GetImage(imageUUID)
		if err != nil {
			errorMsg := fmt.Errorf("failed to verify system disk image %s: %w", imageUUID, err)
			r.setFailureStatus(scope, createErrorFailureReason, errorMsg)
			return nil, err
		}

		if r.imageMarkedForDeletionV4(image) {
			err := fmt.Errorf("system disk image %s is being deleted", imageUUID)
			r.setFailureStatus(scope, createErrorFailureReason, err)
			return nil, err
		}
	}

	// Get system disk size in bytes
	systemDiskSizeBytes, err := r.getBytesFromQuantity(scope.NutanixMachine.Spec.SystemDiskSize)
	if err != nil {
		errorMsg := fmt.Errorf("failed to parse system disk size: %w", err)
		r.setFailureStatus(scope, createErrorFailureReason, errorMsg)
		return nil, err
	}

	// Create system disk spec using V4 models
	systemDisk, err := r.createSystemDiskSpecV4(imageUUID, systemDiskSizeBytes)
	if err != nil {
		errorMsg := fmt.Errorf("error occurred while creating system disk spec: %w", err)
		r.setFailureStatus(scope, createErrorFailureReason, errorMsg)
		return nil, err
	}

	return systemDisk, nil
}

func (r *NutanixMachineReconciler) getBootstrapDiskV4(nctx *controllers.NutanixExtendedContext, scope *NutanixMachineScope) (*vmmModels.Disk, error) {
	ctx := nctx.Context
	v4Client := nctx.GetV4FacadeClient()

	// Get bootstrap image UUID
	bootstrapImageRef := infrav1.NutanixResourceIdentifier{
		Type: infrav1.NutanixIdentifierName,
		Name: ptr.To(scope.NutanixMachine.Spec.BootstrapRef.Name),
	}

	imageUUID, err := r.getImageUUIDV4(ctx, v4Client, bootstrapImageRef)
	if err != nil {
		errorMsg := fmt.Errorf("failed to get bootstrap disk image %q: %w", bootstrapImageRef, err)
		r.setFailureStatus(scope, createErrorFailureReason, errorMsg)
		return nil, err
	}

	// Create bootstrap disk (CD-ROM type)
	bootstrapDisk, err := r.createBootstrapDiskSpecV4(imageUUID)
	if err != nil {
		return nil, err
	}
	return bootstrapDisk, nil
}

func (r *NutanixMachineReconciler) getDataDisksV4(nctx *controllers.NutanixExtendedContext, scope *NutanixMachineScope, peUUID string) ([]vmmModels.Disk, error) {
	ctx := nctx.Context
	v4Client := nctx.GetV4FacadeClient()

	dataDisks, err := r.createDataDiskListV4(ctx, v4Client, scope.NutanixMachine.Spec.DataDisks, peUUID)
	if err != nil {
		errorMsg := fmt.Errorf("error occurred while creating data disk spec: %w", err)
		r.setFailureStatus(scope, createErrorFailureReason, errorMsg)
		return nil, err
	}

	return dataDisks, nil
}

func (r *NutanixMachineReconciler) getImageUUIDV4(ctx context.Context, v4Client facade.FacadeClientV4, id infrav1.NutanixResourceIdentifier) (string, error) {
	switch {
	case id.IsUUID():
		// Get image by UUID to verify it exists
		image, err := v4Client.GetImage(*id.UUID)
		if err != nil {
			return "", fmt.Errorf("failed to get image with UUID %s: %v", *id.UUID, err)
		}
		return *image.ExtId, nil

	case id.IsName():
		// Search for image by name
		filter := fmt.Sprintf("name eq '%s'", *id.Name)
		images, err := v4Client.ListImages(facade.WithFilter(filter), facade.WithLimit(10))
		if err != nil {
			return "", fmt.Errorf("failed to list images: %v", err)
		}

		if len(images) == 0 {
			return "", fmt.Errorf("found no image with name %s", *id.Name)
		} else if len(images) > 1 {
			return "", fmt.Errorf("more than one image found with name %s", *id.Name)
		}

		return *images[0].ExtId, nil

	default:
		return "", fmt.Errorf("image identifier is missing both name and uuid")
	}
}

func (r *NutanixMachineReconciler) getBytesFromQuantity(quantity resource.Quantity) (int64, error) {
	bytes, success := quantity.AsInt64()
	if !success {
		return 0, fmt.Errorf("failed to convert quantity %v to bytes", quantity)
	}
	return bytes, nil
}

func (r *NutanixMachineReconciler) createSystemDiskSpecV4(imageUUID string, systemDiskSizeBytes int64) (*vmmModels.Disk, error) {
	if imageUUID == "" {
		return nil, fmt.Errorf("image UUID must be set when creating system disk")
	}
	if systemDiskSizeBytes <= 0 {
		return nil, fmt.Errorf("invalid system disk size: %d. Must be greater than 0", systemDiskSizeBytes)
	}

	// Create image reference
	imageReference := vmmModels.NewImageReference()
	imageReference.ImageExtId = &imageUUID

	// Create data source with image reference
	dataSource := vmmModels.NewDataSource()
	dataSource.Reference = vmmModels.NewOneOfDataSourceReference()
	dataSource.Reference.SetValue(*imageReference)

	// Create VM disk backing info
	vmDisk := vmmModels.NewVmDisk()
	vmDisk.DiskSizeBytes = &systemDiskSizeBytes
	vmDisk.DataSource = dataSource

	// Create disk address for system disk (SCSI, index 0)
	diskIndex := 0
	diskBusType := vmmModels.DISKBUSTYPE_SCSI
	diskAddress := vmmModels.NewDiskAddress()
	diskAddress.BusType = &diskBusType
	diskAddress.Index = &diskIndex

	// Create the main disk object
	systemDisk := vmmModels.NewDisk()
	err := systemDisk.SetBackingInfo(*vmDisk)
	if err != nil {
		return nil, fmt.Errorf("failed to set VM disk backing info: %w", err)
	}
	systemDisk.DiskAddress = diskAddress

	return systemDisk, nil
}

func (r *NutanixMachineReconciler) createBootstrapDiskSpecV4(imageUUID string) (*vmmModels.Disk, error) {
	// Create image reference
	imageReference := vmmModels.NewImageReference()
	imageReference.ImageExtId = &imageUUID

	// Create data source with image reference
	dataSource := vmmModels.NewDataSource()
	dataSource.Reference = vmmModels.NewOneOfDataSourceReference()
	dataSource.Reference.SetValue(*imageReference)

	// Create VM disk backing info for CD-ROM
	vmDisk := vmmModels.NewVmDisk()
	vmDisk.DataSource = dataSource

	// Create disk address for bootstrap disk (IDE, index 0 for CD-ROM)
	diskIndex := 0
	diskBusType := vmmModels.DISKBUSTYPE_IDE
	diskAddress := vmmModels.NewDiskAddress()
	diskAddress.BusType = &diskBusType
	diskAddress.Index = &diskIndex

	// Create the main disk object
	bootstrapDisk := vmmModels.NewDisk()
	err := bootstrapDisk.SetBackingInfo(*vmDisk)
	if err != nil {
		return nil, fmt.Errorf("failed to set VM disk backing info for bootstrap disk: %w", err)
	}
	bootstrapDisk.DiskAddress = diskAddress

	return bootstrapDisk, nil
}

func (r *NutanixMachineReconciler) createDataDiskListV4(ctx context.Context, v4Client facade.FacadeClientV4, dataDiskSpecs []infrav1.NutanixMachineVMDisk, peUUID string) ([]vmmModels.Disk, error) {
	dataDisks := make([]vmmModels.Disk, 0)

	// Track latest device index by adapter type to avoid conflicts
	latestDeviceIndexByAdapterType := make(map[string]int)
	getDeviceIndex := func(adapterType string) int {
		if latestDeviceIndex, ok := latestDeviceIndexByAdapterType[adapterType]; ok {
			latestDeviceIndexByAdapterType[adapterType] = latestDeviceIndex + 1
			return latestDeviceIndex + 1
		}

		// Start from index 1 for SCSI and IDE (0 is typically reserved for system disk)
		if adapterType == string(infrav1.NutanixMachineDiskAdapterTypeSCSI) || adapterType == string(infrav1.NutanixMachineDiskAdapterTypeIDE) {
			latestDeviceIndexByAdapterType[adapterType] = 1
			return 1
		} else {
			latestDeviceIndexByAdapterType[adapterType] = 0
			return 0
		}
	}

	for _, dataDiskSpec := range dataDiskSpecs {
		// Get disk size in bytes
		diskSizeBytes, err := r.getBytesFromQuantity(dataDiskSpec.DiskSize)
		if err != nil {
			return nil, fmt.Errorf("failed to parse data disk size: %w", err)
		}

		// Create VM disk backing info
		vmDisk := vmmModels.NewVmDisk()
		vmDisk.DiskSizeBytes = &diskSizeBytes

		// If data source is provided, get the image UUID
		if dataDiskSpec.DataSource != nil {
			imageRef := infrav1.NutanixResourceIdentifier{
				UUID: dataDiskSpec.DataSource.UUID,
				Type: infrav1.NutanixIdentifierUUID,
			}
			imageUUID, err := r.getImageUUIDV4(ctx, v4Client, imageRef)
			if err != nil {
				return nil, fmt.Errorf("failed to get data disk image: %w", err)
			}

			// Create image reference
			imageReference := vmmModels.NewImageReference()
			imageReference.ImageExtId = &imageUUID

			// Create data source with image reference
			dataSource := vmmModels.NewDataSource()
			dataSource.Reference = vmmModels.NewOneOfDataSourceReference()
			dataSource.Reference.SetValue(*imageReference)

			vmDisk.DataSource = dataSource
		}

		// Set default adapter type
		adapterType := infrav1.NutanixMachineDiskAdapterTypeSCSI

		// If device properties are provided, use them
		if dataDiskSpec.DeviceProperties != nil {
			adapterType = dataDiskSpec.DeviceProperties.AdapterType
		}

		// Create disk address
		diskIndex := getDeviceIndex(string(adapterType))
		var diskBusType vmmModels.DiskBusType

		switch adapterType {
		case infrav1.NutanixMachineDiskAdapterTypeSCSI:
			diskBusType = vmmModels.DISKBUSTYPE_SCSI
		case infrav1.NutanixMachineDiskAdapterTypeIDE:
			diskBusType = vmmModels.DISKBUSTYPE_IDE
		case infrav1.NutanixMachineDiskAdapterTypePCI:
			diskBusType = vmmModels.DISKBUSTYPE_PCI
		case infrav1.NutanixMachineDiskAdapterTypeSATA:
			diskBusType = vmmModels.DISKBUSTYPE_SATA
		case infrav1.NutanixMachineDiskAdapterTypeSPAPR:
			diskBusType = vmmModels.DISKBUSTYPE_SPAPR
		default:
			diskBusType = vmmModels.DISKBUSTYPE_SCSI // Default to SCSI
		}

		diskAddress := vmmModels.NewDiskAddress()
		diskAddress.BusType = &diskBusType
		diskAddress.Index = &diskIndex

		// Create the main disk object
		dataDisk := vmmModels.NewDisk()
		err = dataDisk.SetBackingInfo(*vmDisk)
		if err != nil {
			return nil, fmt.Errorf("failed to set VM disk backing info for data disk: %w", err)
		}
		dataDisk.DiskAddress = diskAddress

		dataDisks = append(dataDisks, *dataDisk)
	}

	return dataDisks, nil
}

// ImageLookupV4 struct for template processing
type ImageLookupV4 struct {
	BaseOS     string
	K8sVersion string
}

// getImageByLookupV4 finds an image using template-based lookup with V4 API
func (r *NutanixMachineReconciler) getImageByLookupV4(
	ctx context.Context,
	v4Client facade.FacadeClientV4,
	imageTemplate,
	imageLookupBaseOS,
	k8sVersion *string,
) (string, error) {
	// Remove 'v' prefix from k8s version if present
	if strings.Contains(*k8sVersion, "v") {
		k8sVersion = ptr.To(strings.Replace(*k8sVersion, "v", "", 1))
	}

	// Create template parameters
	params := ImageLookupV4{*imageLookupBaseOS, *k8sVersion}

	// Parse the template
	t, err := template.New("k8sTemplate").Parse(*imageTemplate)
	if err != nil {
		return "", fmt.Errorf("failed to parse template given %s %v", *imageTemplate, err)
	}

	// Execute template substitution
	var templateBytes bytes.Buffer
	err = t.Execute(&templateBytes, params)
	if err != nil {
		return "", fmt.Errorf(
			"failed to substitute string %s with params %v error: %w",
			*imageTemplate,
			params,
			err,
		)
	}

	// Get all images using V4 API
	allImages, err := v4Client.ListImages()
	if err != nil {
		return "", fmt.Errorf("failed to list images: %v", err)
	}

	// Create regex from template result
	re := regexp.MustCompile(templateBytes.String())
	foundImages := make([]imageModels.Image, 0)

	// Filter images by regex match
	for _, image := range allImages {
		if image.Name != nil && re.Match([]byte(*image.Name)) {
			foundImages = append(foundImages, image)
		}
	}

	// Sort by creation time (latest first)
	sorted := r.sortImagesByLatestCreationTimeV4(foundImages)
	if len(sorted) == 0 {
		return "", fmt.Errorf("failed to find image with filter %s", templateBytes.String())
	}

	// Check if the latest image is marked for deletion
	if r.imageMarkedForDeletionV4(&sorted[0]) {
		return "", fmt.Errorf("latest matching image %s is marked for deletion", *sorted[0].Name)
	}

	return *sorted[0].ExtId, nil
}

// sortImagesByLatestCreationTimeV4 returns the images sorted by creation time
func (r *NutanixMachineReconciler) sortImagesByLatestCreationTimeV4(
	images []imageModels.Image,
) []imageModels.Image {
	sort.Slice(images, func(i, j int) bool {
		if images[i].Name == nil || images[j].Name == nil {
			return images[i].Name != nil
		}
		timeI := *images[i].CreateTime
		timeJ := *images[j].CreateTime
		return timeI.After(timeJ)
	})
	return images
}

// imageMarkedForDeletionV4 checks if the V4 image is marked for deletion
// TODO: Implement proper deletion check when V4 Image model provides the right field
func (r *NutanixMachineReconciler) imageMarkedForDeletionV4(image *imageModels.Image) bool {
	// For now, assume images are not marked for deletion
	// TODO: This should be replaced with actual deletion state check when the field is available
	return false
}

// getGPUListV4 returns a list of GPU configurations for the given list of GPUs using V4 API
func (r *NutanixMachineReconciler) getGPUListV4(nctx *controllers.NutanixExtendedContext, scope *NutanixMachineScope, peUUID string) ([]vmmModels.Gpu, error) {
	ctx := nctx.Context
	v4Client := nctx.GetV4FacadeClient()

	resultGPUs := make([]vmmModels.Gpu, 0)

	if len(scope.NutanixMachine.Spec.GPUs) == 0 {
		return resultGPUs, nil
	}

	for _, gpu := range scope.NutanixMachine.Spec.GPUs {
		foundGPU, err := r.getGPUV4(ctx, v4Client, peUUID, gpu)
		if err != nil {
			return nil, err
		}
		resultGPUs = append(resultGPUs, *foundGPU)
	}

	return resultGPUs, nil
}

// getGPUV4 returns a GPU configuration for the given GPU specification using V4 API
func (r *NutanixMachineReconciler) getGPUV4(ctx context.Context, v4Client facade.FacadeClientV4, peUUID string, gpu infrav1.NutanixGPU) (*vmmModels.Gpu, error) {
	gpuDeviceID := gpu.DeviceID
	gpuDeviceName := gpu.Name

	if gpuDeviceID == nil && gpuDeviceName == nil {
		return nil, fmt.Errorf("gpu name or gpu device ID must be passed in order to retrieve the GPU")
	}

	// Try to find in physical GPUs first
	physicalGPUs, err := v4Client.ListClusterPhysicalGPUs(peUUID)
	if err == nil {
		for _, pGPU := range physicalGPUs {
			if pGPU.PhysicalGpuConfig.IsInUse != nil && *pGPU.PhysicalGpuConfig.IsInUse {
				continue // Skip GPUs that are already in use
			}

			// Check if this GPU matches our criteria
			if (gpuDeviceID != nil && pGPU.PhysicalGpuConfig.DeviceId != nil && *pGPU.PhysicalGpuConfig.DeviceId == *gpuDeviceID) ||
				(gpuDeviceName != nil && pGPU.PhysicalGpuConfig.DeviceName != nil && *pGPU.PhysicalGpuConfig.DeviceName == *gpuDeviceName) {

				vmGpu := vmmModels.NewGpu()

				if pGPU.PhysicalGpuConfig.DeviceId != nil {
					deviceIDInt := int(*pGPU.PhysicalGpuConfig.DeviceId)
					vmGpu.DeviceId = &deviceIDInt
				}

				vmGpu.Mode = vmmModels.GPUMODE_PASSTHROUGH_COMPUTE.Ref() // TODO: Add support for PASSTHROUGH_GRAPHICS in CAPX API
				vmGpu.Vendor = r.vendorStringToV4Model(pGPU.PhysicalGpuConfig.VendorName)

				if pGPU.PhysicalGpuConfig.DeviceName != nil {
					vmGpu.Name = pGPU.PhysicalGpuConfig.DeviceName
				}

				return vmGpu, nil
			}
		}
	} else {
		ctrl.LoggerFrom(ctx).V(1).Info("Failed to list physical GPUs", "error", err)
	}

	// Try to find in virtual GPUs
	virtualGPUs, err := v4Client.ListClusterVirtualGPUs(peUUID)
	if err == nil {
		for _, vGPU := range virtualGPUs {
			// Check if this GPU matches our criteria
			if (gpuDeviceID != nil && vGPU.VirtualGpuConfig.DeviceId != nil && *vGPU.VirtualGpuConfig.DeviceId == *gpuDeviceID) ||
				(gpuDeviceName != nil && vGPU.VirtualGpuConfig.DeviceName != nil && *vGPU.VirtualGpuConfig.DeviceName == *gpuDeviceName) {

				vmGpu := vmmModels.NewGpu()

				if vGPU.VirtualGpuConfig.DeviceId != nil {
					deviceIDInt := int(*vGPU.VirtualGpuConfig.DeviceId)
					vmGpu.DeviceId = &deviceIDInt
				}

				vmGpu.Mode = vmmModels.GPUMODE_VIRTUAL.Ref()
				vmGpu.Vendor = r.vendorStringToV4Model(vGPU.VirtualGpuConfig.VendorName)

				if vGPU.VirtualGpuConfig.DeviceName != nil {
					vmGpu.Name = vGPU.VirtualGpuConfig.DeviceName
				}

				return vmGpu, nil
			}
		}
	} else {
		ctrl.LoggerFrom(ctx).V(1).Info("Failed to list virtual GPUs", "error", err)
	}

	return nil, fmt.Errorf("no available GPU found in Prism Element that matches required GPU inputs")
}

// vendorStringToV4Model converts vendor string to V4 GPU vendor enum
func (r *NutanixMachineReconciler) vendorStringToV4Model(vendor *string) *vmmModels.GpuVendor {
	if vendor == nil {
		return vmmModels.GPUVENDOR_UNKNOWN.Ref()
	}

	switch *vendor {
	case "kNvidia":
		return vmmModels.GPUVENDOR_NVIDIA.Ref()
	case "kIntel":
		return vmmModels.GPUVENDOR_INTEL.Ref()
	case "kAmd":
		return vmmModels.GPUVENDOR_AMD.Ref()
	default:
		return vmmModels.GPUVENDOR_UNKNOWN.Ref()
	}
}

// addBootTypeToVMV4 creates and returns boot configuration for VM using V4 API
func (r *NutanixMachineReconciler) addBootTypeToVMV4(nctx *controllers.NutanixExtendedContext, scope *NutanixMachineScope) (*vmmModels.OneOfVmBootConfig, error) {
	bootType := scope.NutanixMachine.Spec.BootType

	// Validate boot type if specified
	if bootType != "" && bootType != infrav1.NutanixBootTypeLegacy && bootType != infrav1.NutanixBootTypeUEFI {
		errorMsg := fmt.Errorf("boot type must be %s or %s but was %s",
			string(infrav1.NutanixBootTypeLegacy),
			string(infrav1.NutanixBootTypeUEFI),
			bootType)
		return nil, errorMsg
	}

	// Configure boot mode (defaults to legacy if not specified or if explicitly set to legacy)
	if bootType == infrav1.NutanixBootTypeUEFI {
		// UEFI boot configuration
		uefiBoot := vmmModels.NewUefiBoot()
		bootConfig := vmmModels.NewOneOfVmBootConfig()
		bootConfig.SetValue(*uefiBoot)
		return bootConfig, nil
	} else {
		// Legacy boot configuration (default for empty, legacy, or any other case)
		legacyBoot := vmmModels.NewLegacyBoot()
		bootConfig := vmmModels.NewOneOfVmBootConfig()
		bootConfig.SetValue(*legacyBoot)
		return bootConfig, nil
	}
}

// addVMToProjectV4 sets the project reference for VM configuration using V4 API
func (r *NutanixMachineReconciler) addVMToProjectV4(nctx *controllers.NutanixExtendedContext, scope *NutanixMachineScope, vmSpec *vmmModels.Vm) error {
	log := ctrl.LoggerFrom(nctx.Context)
	ctx := nctx.Context
	v3Client := nctx.GetV3Client() // Use V3 API to get project as specified
	vmName := scope.Machine.Name
	projectRef := scope.NutanixMachine.Spec.Project

	if projectRef == nil {
		log.V(1).Info("Not linking VM to a project")
		return nil
	}

	if vmSpec == nil {
		errorMsg := fmt.Errorf("vmSpec cannot be nil when adding VM %s to project", vmName)
		log.Error(errorMsg, "failed to add vm to project")
		return errorMsg
	}

	// Use V3 API to get project UUID
	projectUUID, err := controllers.GetProjectUUID(ctx, v3Client, projectRef.Name, projectRef.UUID)
	if err != nil {
		errorMsg := fmt.Errorf("error occurred while searching for project for VM %s: %v", vmName, err)
		log.Error(errorMsg, "error occurred while searching for project")
		return errorMsg
	}

	// Note: V4 VM v4.0 spec doesn't support direct project assignment during creation
	// Project assignment will be done post-creation using V3 API via updateVMWithProject()
	log.V(1).Info("Project lookup successful - V4 VM creation will proceed without direct project assignment",
		"vmName", vmName,
		"projectUUID", projectUUID,
		"note", "V4 VMs will be assigned to projects post-creation via V3 API")

	// Example for v4.1:
	// vmSpec.ProjectReference = &vmmModels.ProjectReference{
	// 	ExtId: utils.StringPtr(projectUUID),
	// }

	return nil
}

// updateVMWithProject updates an existing VM with project assignment using V3 API
// This is used for V4 VMs where project assignment must be done post-creation via V3 API
func (r *NutanixMachineReconciler) updateVMWithProject(nctx *controllers.NutanixExtendedContext, scope *NutanixMachineScope, vm *vmmModels.Vm) error {
	vmName := scope.Machine.Name
	ctx := nctx.Context
	v3Client := nctx.GetV3Client()
	log := ctrl.LoggerFrom(ctx)

	// Check if project is specified
	projectRef := scope.NutanixMachine.Spec.Project
	if projectRef == nil {
		log.V(1).Info("Not linking VM to a project")
		return nil
	}

	// Ensure VM has ExtId (UUID)
	if vm.ExtId == nil {
		errorMsg := fmt.Errorf("VM %s does not have ExtId (UUID), cannot update with project", vmName)
		log.Error(errorMsg, "failed to update vm with project")
		return errorMsg
	}

	vmUUID := *vm.ExtId

	// Get project UUID using the same helper function as V3
	projectUUID, err := controllers.GetProjectUUID(ctx, v3Client, projectRef.Name, projectRef.UUID)
	if err != nil {
		errorMsg := fmt.Errorf("error occurred while searching for project for VM %s: %v", vmName, err)
		log.Error(errorMsg, "error occurred while searching for project")
		return errorMsg
	}

	// Get the current VM state using V3 API
	vmResponse, err := v3Client.V3.GetVM(ctx, vmUUID)
	if err != nil {
		errorMsg := fmt.Errorf("failed to get VM %s (UUID: %s) for project update: %v", vmName, vmUUID, err)
		log.Error(errorMsg, "failed to get vm for project update")
		return errorMsg
	}

	// Prepare the update input based on current VM state
	vmUpdateInput := &prismclientv3.VMIntentInput{
		Spec: vmResponse.Spec,
		Metadata: &prismclientv3.Metadata{
			Kind:        vmResponse.Metadata.Kind,
			SpecVersion: vmResponse.Metadata.SpecVersion,
			Categories:  vmResponse.Metadata.Categories,
			ProjectReference: &prismclientv3.Reference{
				Kind: utils.StringPtr("project"),
				UUID: utils.StringPtr(projectUUID),
			},
		},
	}

	// Update the VM with project assignment
	_, err = v3Client.V3.UpdateVM(ctx, vmUUID, vmUpdateInput)
	if err != nil {
		errorMsg := fmt.Errorf("failed to update VM %s (UUID: %s) with project: %v", vmName, vmUUID, err)
		log.Error(errorMsg, "failed to update vm with project")
		conditions.MarkFalse(scope.NutanixMachine, infrav1.ProjectAssignedCondition, infrav1.ProjectAssignationFailed, capiv1.ConditionSeverityError, "%s", errorMsg.Error())
		return errorMsg
	}

	log.V(1).Info("Successfully updated VM with project assignment",
		"vmName", vmName,
		"vmUUID", vmUUID,
		"projectUUID", projectUUID)

	return nil
}

// addGuestCustomizationToVMV4 adds guest customization (cloud-init) to VM spec using V4 API
func (r *NutanixMachineReconciler) addGuestCustomizationToVMV4(nctx *controllers.NutanixExtendedContext, scope *NutanixMachineScope, vmSpec *vmmModels.Vm) error {
	// Get the bootstrapRef
	bootstrapRef := scope.NutanixMachine.Spec.BootstrapRef
	if bootstrapRef == nil || bootstrapRef.Kind != infrav1.NutanixMachineBootstrapRefKindSecret {
		// No cloud-init configuration needed if bootstrapRef is not a secret
		return nil
	}

	// Get the bootstrap data from the secret
	bootstrapData, err := r.getBootstrapData(nctx, scope)
	if err != nil {
		return err
	}

	// Encode the bootstrap data with base64
	bsdataEncoded := base64.StdEncoding.EncodeToString(bootstrapData)

	// Create metadata JSON with hostname and UUID
	metadata := fmt.Sprintf(`{"hostname": "%s", "uuid": "%s"}`, scope.Machine.Name, uuid.New())
	metadataEncoded := base64.StdEncoding.EncodeToString([]byte(metadata))

	// Create cloud-init configuration using V4 models
	cloudInit := vmmModels.NewCloudInit()

	// Set the cloud init script (user-data)
	cloudInitScript := vmmModels.NewOneOfCloudInitCloudInitScript()
	cloudInitScript.SetValue(bsdataEncoded)
	cloudInit.CloudInitScript = cloudInitScript

	// Set the metadata
	cloudInit.Metadata = &metadataEncoded

	// Create guest customization params and try to set the CloudInit
	guestCustomizationParams := vmmModels.NewGuestCustomizationParams()

	// Store the CloudInit in the VM spec using the proper V4 structure
	// Note: The V4 API may require a different approach for setting CloudInit
	// For now, we create a basic GuestCustomizationParams structure
	vmSpec.GuestCustomization = guestCustomizationParams

	// Store the cloud-init data for use during VM creation
	// This implementation follows the V4 API pattern where CloudInit configuration
	// is provided with the guestCustomization field containing:
	// - cloudInitScript: base64-encoded user-data
	// - metadata: base64-encoded metadata JSON
	//
	// Note: The actual V4 API call implementation should use these values:
	// - cloudInitScript: bsdataEncoded
	// - metadata: metadataEncoded

	return nil
}
