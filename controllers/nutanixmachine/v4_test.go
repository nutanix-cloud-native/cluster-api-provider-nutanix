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
	"context"
	"testing"

	"go.uber.org/mock/gomock"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/patch"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	clusterModels "github.com/nutanix/ntnx-api-golang-clients/clustermgmt-go-client/v4/models/clustermgmt/v4/config"
	v4prismModels "github.com/nutanix/ntnx-api-golang-clients/prism-go-client/v4/models/prism/v4/config"
	vmmModels "github.com/nutanix/ntnx-api-golang-clients/vmm-go-client/v4/models/vmm/v4/ahv/config"
	imageModels "github.com/nutanix/ntnx-api-golang-clients/vmm-go-client/v4/models/vmm/v4/content"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	"github.com/nutanix-cloud-native/cluster-api-provider-nutanix/controllers"
	mocknutanixv3 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/mocks/nutanix"
	mocknutanixv4 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/mocks/nutanixv4"
	"github.com/nutanix-cloud-native/prism-go-client/utils"
	prismclientv3 "github.com/nutanix-cloud-native/prism-go-client/v3"
)

// Helper functions for V4 testing
func createMockV4Client(t *testing.T) (*gomock.Controller, *mocknutanixv4.MockFacadeClientV4) {
	ctrl := gomock.NewController(t)
	mockV4Client := mocknutanixv4.NewMockFacadeClientV4(ctrl)
	return ctrl, mockV4Client
}

func createV4TestContext(mockV4Client *mocknutanixv4.MockFacadeClientV4, nutanixMachine *infrav1.NutanixMachine) *controllers.NutanixExtendedContext {
	scheme := runtime.NewScheme()
	_ = infrav1.AddToScheme(scheme)
	_ = capiv1.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	// Create patch helper if nutanixMachine is provided
	var patchHelper *patch.Helper
	if nutanixMachine != nil {
		var err error
		patchHelper, err = patch.NewHelper(nutanixMachine, fakeClient)
		if err != nil {
			// For tests, we'll create a mock patch helper that does nothing
			patchHelper = nil
		}
	}

	// Create a mock V3 client for tests that need it
	ctrl := gomock.NewController(nil) // Create a controller for the V3 mock
	mockV3Service := mocknutanixv3.NewMockService(ctrl)
	mockV3Client := &prismclientv3.Client{V3: mockV3Service}

	return &controllers.NutanixExtendedContext{
		ExtendedContext: controllers.ExtendedContext{
			Context:     context.Background(),
			Client:      fakeClient,
			PatchHelper: patchHelper,
		},
		NutanixClients: &controllers.NutanixClients{
			V3Client: mockV3Client,
			V4Facade: mockV4Client,
		},
	}
}

func createV4TestScope() (*infrav1.NutanixCluster, *capiv1.Cluster, *capiv1.Machine, *infrav1.NutanixMachine) {
	nutanixCluster := &infrav1.NutanixCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-nutanix-cluster",
			Namespace: "default",
		},
	}

	cluster := &capiv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "default",
		},
	}

	machine := &capiv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-machine",
			Namespace: "default",
		},
		Spec: capiv1.MachineSpec{
			Version: ptr.To("v1.21.0"),
		},
	}

	nutanixMachine := &infrav1.NutanixMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-nutanix-machine",
			Namespace: "default",
		},
		Spec: infrav1.NutanixMachineSpec{
			VCPUSockets:    2,
			VCPUsPerSocket: 1,
			MemorySize:     resource.MustParse("4Gi"),
			SystemDiskSize: resource.MustParse("20Gi"),
			Image: &infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierName,
				Name: ptr.To("test-image"),
			},
			Cluster: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierName,
				Name: ptr.To("test-pe-cluster"),
			},
			Subnets: []infrav1.NutanixResourceIdentifier{
				{
					Type: infrav1.NutanixIdentifierName,
					Name: ptr.To("test-subnet"),
				},
			},
		},
	}

	return nutanixCluster, cluster, machine, nutanixMachine
}

// MockV4TaskWaiter implements facade.TaskWaiter for testing
type MockV4TaskWaiter struct {
	VMs   []*vmmModels.Vm
	Error error
}

func (w *MockV4TaskWaiter) WaitForTaskCompletion() ([]*vmmModels.Vm, error) {
	if w.Error != nil {
		return nil, w.Error
	}
	return w.VMs, nil
}

// TestV4NutanixMachineVMReadyV4 tests the main V4 reconciliation function
func TestV4NutanixMachineVMReadyV4(t *testing.T) {
	ctrl, mockV4Client := createMockV4Client(t)
	defer ctrl.Finish()

	nutanixCluster, cluster, machine, nutanixMachine := createV4TestScope()
	nctx := createV4TestContext(mockV4Client, nutanixMachine)
	scope := NewNutanixMachineScope(nutanixCluster, nutanixMachine, cluster, machine)

	t.Run("TestV4VMReadyReconciliationVMExists", func(t *testing.T) {
		// Mock VM already exists - simplest test case
		mockV4Client.EXPECT().
			ListVMs(gomock.Any()).
			Return([]vmmModels.Vm{
				{
					ExtId: ptr.To("existing-vm-uuid"),
					Name:  ptr.To("test-machine"),
				},
			}, nil)

		reconciler := &NutanixMachineReconciler{}
		result, err := reconciler.NutanixMachineVMReadyV4(nctx, scope)

		if err != nil {
			t.Errorf("Expected no error, got: %v", err)
		}
		if scope.NutanixMachine.Status.VmUUID != "existing-vm-uuid" {
			t.Errorf("Expected VM UUID 'existing-vm-uuid', got: %s", scope.NutanixMachine.Status.VmUUID)
		}
		if result.Result.Requeue {
			t.Errorf("Expected no requeue, got requeue")
		}
	})
}

// TestV4FindVmV4 tests VM lookup functionality
func TestV4FindVmV4(t *testing.T) {
	ctrl, mockV4Client := createMockV4Client(t)
	defer ctrl.Finish()

	nutanixCluster, cluster, machine, nutanixMachine := createV4TestScope()
	nctx := createV4TestContext(mockV4Client, nutanixMachine)
	scope := NewNutanixMachineScope(nutanixCluster, nutanixMachine, cluster, machine)

	t.Run("TestV4FindVMByNameSuccess", func(t *testing.T) {
		mockV4Client.EXPECT().
			ListVMs(gomock.Any()).
			Return([]vmmModels.Vm{
				{
					ExtId: ptr.To("found-vm-uuid"),
					Name:  ptr.To("test-vm"),
				},
			}, nil)

		reconciler := &NutanixMachineReconciler{}
		vm, err := reconciler.FindVmV4(nctx, scope, "test-vm")

		if err != nil {
			t.Errorf("Expected no error, got: %v", err)
		}
		if vm == nil {
			t.Error("Expected VM to be found, got nil")
		}
		if vm != nil && *vm.ExtId != "found-vm-uuid" {
			t.Errorf("Expected VM UUID 'found-vm-uuid', got: %s", *vm.ExtId)
		}
	})

	t.Run("TestV4FindVMByUUIDSuccess", func(t *testing.T) {
		scope.NutanixMachine.Status.VmUUID = "existing-vm-uuid"

		mockV4Client.EXPECT().
			GetVM("existing-vm-uuid").
			Return(&vmmModels.Vm{
				ExtId: ptr.To("existing-vm-uuid"),
				Name:  ptr.To("test-vm"),
			}, nil)

		reconciler := &NutanixMachineReconciler{}
		vm, err := reconciler.FindVmV4(nctx, scope, "test-vm")

		if err != nil {
			t.Errorf("Expected no error, got: %v", err)
		}
		if vm == nil {
			t.Error("Expected VM to be found, got nil")
		}
	})

	t.Run("TestV4FindVMNotFound", func(t *testing.T) {
		scope.NutanixMachine.Status.VmUUID = ""

		mockV4Client.EXPECT().
			ListVMs(gomock.Any()).
			Return([]vmmModels.Vm{}, nil)

		reconciler := &NutanixMachineReconciler{}
		vm, err := reconciler.FindVmV4(nctx, scope, "non-existing-vm")

		if err == nil {
			t.Error("Expected error when VM not found, got nil")
		}
		if vm != nil {
			t.Error("Expected VM to be nil, got VM object")
		}
	})
}

// TestV4GetSubnetAndPEUUIDsV4 tests network configuration lookup
func TestV4GetSubnetAndPEUUIDsV4(t *testing.T) {
	ctrl, mockV4Client := createMockV4Client(t)
	defer ctrl.Finish()

	nutanixCluster, cluster, machine, nutanixMachine := createV4TestScope()
	nctx := createV4TestContext(mockV4Client, nutanixMachine)
	scope := NewNutanixMachineScope(nutanixCluster, nutanixMachine, cluster, machine)

	t.Run("TestV4GetSubnetAndPEUUIDsWithUUID", func(t *testing.T) {
		// Test with UUID-based identifiers - simplest case
		scope.NutanixMachine.Spec.Cluster.Type = infrav1.NutanixIdentifierUUID
		scope.NutanixMachine.Spec.Cluster.UUID = ptr.To("pe-cluster-uuid")
		scope.NutanixMachine.Spec.Subnets[0].Type = infrav1.NutanixIdentifierUUID
		scope.NutanixMachine.Spec.Subnets[0].UUID = ptr.To("subnet-uuid")

		reconciler := &NutanixMachineReconciler{}
		peUUID, subnetUUIDs, err := reconciler.GetSubnetAndPEUUIDsV4(nctx, scope)

		if err != nil {
			t.Errorf("Expected no error, got: %v", err)
		}
		if peUUID != "pe-cluster-uuid" {
			t.Errorf("Expected PE UUID 'pe-cluster-uuid', got: %s", peUUID)
		}
		if len(subnetUUIDs) != 1 || subnetUUIDs[0] != "subnet-uuid" {
			t.Errorf("Expected subnet UUIDs ['subnet-uuid'], got: %v", subnetUUIDs)
		}
	})
}

// TestV4GetSystemDiskV4 tests system disk creation for V4
func TestV4GetSystemDiskV4(t *testing.T) {
	ctrl, mockV4Client := createMockV4Client(t)
	defer ctrl.Finish()

	nutanixCluster, cluster, machine, nutanixMachine := createV4TestScope()
	nctx := createV4TestContext(mockV4Client, nutanixMachine)
	scope := NewNutanixMachineScope(nutanixCluster, nutanixMachine, cluster, machine)

	t.Run("TestV4GetSystemDiskSuccess", func(t *testing.T) {
		mockV4Client.EXPECT().
			ListImages(gomock.Any()).
			Return([]imageModels.Image{
				{
					ExtId: ptr.To("image-uuid"),
					Name:  ptr.To("test-image"),
				},
			}, nil)

		reconciler := &NutanixMachineReconciler{}
		disk, err := reconciler.getSystemDiskV4(nctx, scope)

		if err != nil {
			t.Errorf("Expected no error, got: %v", err)
		}
		if disk == nil {
			t.Error("Expected disk to be created, got nil")
		}
	})

	t.Run("TestV4GetSystemDiskImageNotFound", func(t *testing.T) {
		mockV4Client.EXPECT().
			ListImages(gomock.Any()).
			Return([]imageModels.Image{}, nil)

		reconciler := &NutanixMachineReconciler{}
		_, err := reconciler.getSystemDiskV4(nctx, scope)

		if err == nil {
			t.Error("Expected error when image not found, got nil")
		}
	})
}

// TestV4AddBootTypeToVMV4 tests boot type configuration for V4
func TestV4AddBootTypeToVMV4(t *testing.T) {
	ctrl, _ := createMockV4Client(t)
	defer ctrl.Finish()

	nutanixCluster, cluster, machine, nutanixMachine := createV4TestScope()
	nctx := createV4TestContext(nil, nutanixMachine)
	scope := NewNutanixMachineScope(nutanixCluster, nutanixMachine, cluster, machine)

	t.Run("TestV4AddBootTypeLegacy", func(t *testing.T) {
		scope.NutanixMachine.Spec.BootType = infrav1.NutanixBootTypeLegacy

		reconciler := &NutanixMachineReconciler{}
		bootConfig, err := reconciler.addBootTypeToVMV4(nctx, scope)

		if err != nil {
			t.Errorf("Expected no error, got: %v", err)
		}
		if bootConfig == nil {
			t.Error("Expected boot config to be set even for legacy boot type")
		}
	})

	t.Run("TestV4AddBootTypeUEFI", func(t *testing.T) {
		scope.NutanixMachine.Spec.BootType = infrav1.NutanixBootTypeUEFI

		reconciler := &NutanixMachineReconciler{}
		bootConfig, err := reconciler.addBootTypeToVMV4(nctx, scope)

		if err != nil {
			t.Errorf("Expected no error, got: %v", err)
		}
		if bootConfig == nil {
			t.Error("Expected boot config to be set for UEFI boot type")
		}
	})

	t.Run("TestV4AddBootTypeInvalid", func(t *testing.T) {
		scope.NutanixMachine.Spec.BootType = "invalid"

		reconciler := &NutanixMachineReconciler{}
		_, err := reconciler.addBootTypeToVMV4(nctx, scope)

		if err == nil {
			t.Error("Expected error for invalid boot type, got nil")
		}
	})
}

// TestV4GPUConfiguration tests GPU configuration for V4
func TestV4GPUConfiguration(t *testing.T) {
	ctrl, mockV4Client := createMockV4Client(t)
	defer ctrl.Finish()

	nutanixCluster, cluster, machine, nutanixMachine := createV4TestScope()
	nctx := createV4TestContext(mockV4Client, nutanixMachine)
	scope := NewNutanixMachineScope(nutanixCluster, nutanixMachine, cluster, machine)

	t.Run("TestV4GetGPUListEmpty", func(t *testing.T) {
		// Test with no GPUs configured
		scope.NutanixMachine.Spec.GPUs = []infrav1.NutanixGPU{}

		reconciler := &NutanixMachineReconciler{}
		gpuList, err := reconciler.getGPUListV4(nctx, scope, "pe-uuid")

		if err != nil {
			t.Errorf("Expected no error, got: %v", err)
		}
		if len(gpuList) != 0 {
			t.Errorf("Expected empty GPU list, got: %d GPUs", len(gpuList))
		}
	})

	t.Run("TestV4GetGPUListWithGPUs", func(t *testing.T) {
		// Test with GPUs configured
		scope.NutanixMachine.Spec.GPUs = []infrav1.NutanixGPU{
			{Name: ptr.To("Tesla-K80")},
		}

		// Mock empty physical and virtual GPU lists for simplicity
		mockV4Client.EXPECT().
			ListClusterPhysicalGPUs("pe-uuid", gomock.Any()).
			Return([]clusterModels.PhysicalGpuProfile{}, nil)

		mockV4Client.EXPECT().
			ListClusterVirtualGPUs("pe-uuid", gomock.Any()).
			Return([]clusterModels.VirtualGpuProfile{}, nil)

		reconciler := &NutanixMachineReconciler{}
		_, err := reconciler.getGPUListV4(nctx, scope, "pe-uuid")

		// Should return error since no matching GPU found
		if err == nil {
			t.Error("Expected error when no matching GPU found, got nil")
		}
	})
}

// TestV4CategoryManagement tests category creation and management for V4
func TestV4CategoryManagement(t *testing.T) {
	ctrl, mockV4Client := createMockV4Client(t)
	defer ctrl.Finish()

	nutanixCluster, cluster, machine, nutanixMachine := createV4TestScope()
	nctx := createV4TestContext(mockV4Client, nutanixMachine)
	scope := NewNutanixMachineScope(nutanixCluster, nutanixMachine, cluster, machine)

	t.Run("TestV4GetOrCreateCategoriesSuccess", func(t *testing.T) {
		categoryIdentifiers := []*infrav1.NutanixCategoryIdentifier{
			{Key: "test-key", Value: "test-value"},
		}

		// Mock category not found, then created
		mockV4Client.EXPECT().
			ListCategories(gomock.Any()).
			Return([]v4prismModels.Category{}, nil)

		mockV4Client.EXPECT().
			CreateCategory(gomock.Any()).
			Return(&v4prismModels.Category{
				ExtId: ptr.To("category-uuid"),
				Key:   ptr.To("test-key"),
				Value: ptr.To("test-value"),
			}, nil)

		reconciler := &NutanixMachineReconciler{}
		categories, err := reconciler.getOrCreateCategoriesV4(nctx, categoryIdentifiers)

		if err != nil {
			t.Errorf("Expected no error, got: %v", err)
		}
		if len(categories) != 1 {
			t.Errorf("Expected 1 category, got: %d", len(categories))
		}
	})

	// Suppress unused variable warning
	_ = scope
}

// TestV4UpdateVMWithProject tests updating a V4 VM with project using V3 API
func TestV4UpdateVMWithProject(t *testing.T) {
	ctrl, mockV4Client := createMockV4Client(t)
	defer ctrl.Finish()

	nutanixCluster, cluster, machine, nutanixMachine := createV4TestScope()

	// Add project reference to the NutanixMachine
	nutanixMachine.Spec.Project = &infrav1.NutanixResourceIdentifier{
		Type: infrav1.NutanixIdentifierName,
		Name: ptr.To("test-project"),
	}

	nctx := createV4TestContext(mockV4Client, nutanixMachine)
	scope := NewNutanixMachineScope(nutanixCluster, nutanixMachine, cluster, machine)

	t.Run("TestV4UpdateVMWithProjectSuccess", func(t *testing.T) {
		// Create a VM object with ExtId
		vm := &vmmModels.Vm{
			ExtId: ptr.To("vm-uuid-123"),
			Name:  ptr.To("test-vm"),
		}

		// Mock V3 client expectations for project lookup
		mockV3Service := nctx.NutanixClients.V3Client.V3.(*mocknutanixv3.MockService)

		// Mock project lookup
		mockV3Service.EXPECT().
			ListAllProject(gomock.Any(), gomock.Any()).
			Return(&prismclientv3.ProjectListResponse{
				Entities: []*prismclientv3.Project{
					{
						Metadata: &prismclientv3.Metadata{UUID: utils.StringPtr("project-uuid")},
						Spec:     &prismclientv3.ProjectSpec{Name: "test-project"},
					},
				},
			}, nil)

		// Mock GetVM call
		mockV3Service.EXPECT().
			GetVM(gomock.Any(), "vm-uuid-123").
			Return(&prismclientv3.VMIntentResponse{
				Metadata: &prismclientv3.Metadata{
					Kind:        utils.StringPtr("vm"),
					SpecVersion: utils.Int64Ptr(1),
				},
				Spec: &prismclientv3.VM{Name: utils.StringPtr("test-vm")},
			}, nil)

		// Mock UpdateVM call
		mockV3Service.EXPECT().
			UpdateVM(gomock.Any(), "vm-uuid-123", gomock.Any()).
			Return(&prismclientv3.VMIntentResponse{}, nil)

		reconciler := &NutanixMachineReconciler{}
		err := reconciler.updateVMWithProject(nctx, scope, vm)

		if err != nil {
			t.Errorf("Expected no error, got: %v", err)
		}
	})

	t.Run("TestV4UpdateVMWithProjectNoProject", func(t *testing.T) {
		// Remove project from scope
		scopeNoProject := &NutanixMachineScope{
			NutanixClusterScope: scope.NutanixClusterScope,
			NutanixMachine: &infrav1.NutanixMachine{
				Spec: infrav1.NutanixMachineSpec{}, // No project specified
			},
			Machine: scope.Machine,
		}

		vm := &vmmModels.Vm{
			ExtId: ptr.To("vm-uuid-123"),
			Name:  ptr.To("test-vm"),
		}

		reconciler := &NutanixMachineReconciler{}
		err := reconciler.updateVMWithProject(nctx, scopeNoProject, vm)

		// Should succeed with no project
		if err != nil {
			t.Errorf("Expected no error when no project specified, got: %v", err)
		}
	})

	t.Run("TestV4UpdateVMWithProjectNoExtId", func(t *testing.T) {
		// VM without ExtId should fail
		vm := &vmmModels.Vm{
			Name: ptr.To("test-vm"),
		}

		reconciler := &NutanixMachineReconciler{}
		err := reconciler.updateVMWithProject(nctx, scope, vm)

		// Should fail with error about missing ExtId
		if err == nil {
			t.Error("Expected error when VM has no ExtId, got nil")
		}
	})
}
