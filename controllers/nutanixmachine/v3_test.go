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

	"github.com/nutanix-cloud-native/prism-go-client/utils"
	prismclientv3 "github.com/nutanix-cloud-native/prism-go-client/v3"
	"github.com/nutanix-cloud-native/prism-go-client/v3/models"
	"go.uber.org/mock/gomock"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/patch"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	"github.com/nutanix-cloud-native/cluster-api-provider-nutanix/controllers"
	mocknutanixv3 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/mocks/nutanix"
)

// Helper functions for V3 testing
func createMockV3Client(t *testing.T) (*gomock.Controller, *mocknutanixv3.MockService) {
	ctrl := gomock.NewController(t)
	mockV3Service := mocknutanixv3.NewMockService(ctrl)
	return ctrl, mockV3Service
}

func createV3TestContext(mockV3Service *mocknutanixv3.MockService, nutanixMachine *infrav1.NutanixMachine) *controllers.NutanixExtendedContext {
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

	return &controllers.NutanixExtendedContext{
		ExtendedContext: controllers.ExtendedContext{
			Context:     context.Background(),
			Client:      fakeClient,
			PatchHelper: patchHelper,
		},
		NutanixClients: &controllers.NutanixClients{
			V3Client: &prismclientv3.Client{V3: mockV3Service},
		},
	}
}

func createV3TestScope() (*infrav1.NutanixCluster, *capiv1.Cluster, *capiv1.Machine, *infrav1.NutanixMachine) {
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

// TestV3NutanixMachineVMReadyV3 tests the main V3 reconciliation function
func TestV3NutanixMachineVMReadyV3(t *testing.T) {
	ctrl, mockV3Service := createMockV3Client(t)
	defer ctrl.Finish()

	nutanixCluster, cluster, machine, nutanixMachine := createV3TestScope()
	nctx := createV3TestContext(mockV3Service, nutanixMachine)
	scope := NewNutanixMachineScope(nutanixCluster, nutanixMachine, cluster, machine)

	t.Run("TestV3VMReadyReconciliationVMExists", func(t *testing.T) {
		// Mock VM already exists - simplest test case
		mockV3Service.EXPECT().
			ListVM(gomock.Any(), gomock.Any()).
			Return(&prismclientv3.VMListIntentResponse{
				Entities: []*prismclientv3.VMIntentResource{
					{
						Metadata: &prismclientv3.Metadata{
							UUID: utils.StringPtr("existing-vm-uuid"),
						},
						Spec: &prismclientv3.VM{
							Name: utils.StringPtr("test-machine"),
						},
					},
				},
			}, nil)

		// Mock GetVM call that happens after finding the VM by name
		mockV3Service.EXPECT().
			GetVM(gomock.Any(), "existing-vm-uuid").
			Return(&prismclientv3.VMIntentResponse{
				Metadata: &prismclientv3.Metadata{
					UUID: utils.StringPtr("existing-vm-uuid"),
				},
				Spec: &prismclientv3.VM{
					Name: utils.StringPtr("test-machine"),
				},
			}, nil)

		reconciler := &NutanixMachineReconciler{}
		result, err := reconciler.NutanixMachineVMReadyV3(nctx, scope)

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

// TestV3FindVmV3 tests VM lookup functionality
func TestV3FindVmV3(t *testing.T) {
	ctrl, mockV3Service := createMockV3Client(t)
	defer ctrl.Finish()

	nutanixCluster, cluster, machine, nutanixMachine := createV3TestScope()
	nctx := createV3TestContext(mockV3Service, nutanixMachine)
	scope := NewNutanixMachineScope(nutanixCluster, nutanixMachine, cluster, machine)

	t.Run("TestV3FindVMByNameSuccess", func(t *testing.T) {
		mockV3Service.EXPECT().
			ListVM(gomock.Any(), gomock.Any()).
			Return(&prismclientv3.VMListIntentResponse{
				Entities: []*prismclientv3.VMIntentResource{
					{
						Metadata: &prismclientv3.Metadata{UUID: utils.StringPtr("found-vm-uuid")},
						Spec:     &prismclientv3.VM{Name: utils.StringPtr("test-vm")},
					},
				},
			}, nil)

		// Mock GetVM call that happens after finding the VM by name
		mockV3Service.EXPECT().
			GetVM(gomock.Any(), "found-vm-uuid").
			Return(&prismclientv3.VMIntentResponse{
				Metadata: &prismclientv3.Metadata{UUID: utils.StringPtr("found-vm-uuid")},
				Spec:     &prismclientv3.VM{Name: utils.StringPtr("test-vm")},
			}, nil)

		reconciler := &NutanixMachineReconciler{}
		vm, err := reconciler.FindVmV3(nctx, scope, "test-vm")

		if err != nil {
			t.Errorf("Expected no error, got: %v", err)
		}
		if vm == nil {
			t.Error("Expected VM to be found, got nil")
		}
		if vm != nil && *vm.Metadata.UUID != "found-vm-uuid" {
			t.Errorf("Expected VM UUID 'found-vm-uuid', got: %s", *vm.Metadata.UUID)
		}
	})

	t.Run("TestV3FindVMNotFound", func(t *testing.T) {
		scope.NutanixMachine.Status.VmUUID = ""

		mockV3Service.EXPECT().
			ListVM(gomock.Any(), gomock.Any()).
			Return(&prismclientv3.VMListIntentResponse{
				Entities: []*prismclientv3.VMIntentResource{},
				Metadata: &prismclientv3.ListMetadataOutput{TotalMatches: utils.Int64Ptr(0)},
			}, nil)

		reconciler := &NutanixMachineReconciler{}
		vm, err := reconciler.FindVmV3(nctx, scope, "non-existing-vm")

		if err != nil {
			t.Errorf("Expected no error, got: %v", err)
		}
		if vm != nil {
			t.Error("Expected VM to be nil, got VM object")
		}
	})
}

// TestV3GetSubnetAndPEUUIDsV3 tests network configuration lookup
func TestV3GetSubnetAndPEUUIDsV3(t *testing.T) {
	ctrl, mockV3Service := createMockV3Client(t)
	defer ctrl.Finish()

	nutanixCluster, cluster, machine, nutanixMachine := createV3TestScope()
	nctx := createV3TestContext(mockV3Service, nutanixMachine)
	scope := NewNutanixMachineScope(nutanixCluster, nutanixMachine, cluster, machine)

	t.Run("TestV3GetSubnetAndPEUUIDsWithUUID", func(t *testing.T) {
		// Test with UUID-based identifiers - simplest case
		scope.NutanixMachine.Spec.Cluster.Type = infrav1.NutanixIdentifierUUID
		scope.NutanixMachine.Spec.Cluster.UUID = ptr.To("pe-cluster-uuid")
		scope.NutanixMachine.Spec.Subnets[0].Type = infrav1.NutanixIdentifierUUID
		scope.NutanixMachine.Spec.Subnets[0].UUID = ptr.To("subnet-uuid")

		// Mock GetCluster call for UUID-based cluster lookup
		mockV3Service.EXPECT().
			GetCluster(gomock.Any(), "pe-cluster-uuid").
			Return(&prismclientv3.ClusterIntentResponse{
				Metadata: &prismclientv3.Metadata{UUID: utils.StringPtr("pe-cluster-uuid")},
				Spec: &models.Cluster{
					Name: "test-pe-cluster",
				},
			}, nil)

		// Mock GetSubnet call for UUID-based subnet lookup
		mockV3Service.EXPECT().
			GetSubnet(gomock.Any(), "subnet-uuid").
			Return(&prismclientv3.SubnetIntentResponse{
				Metadata: &prismclientv3.Metadata{UUID: utils.StringPtr("subnet-uuid")},
				Spec: &models.Subnet{
					Name: utils.StringPtr("test-subnet"),
				},
			}, nil)

		reconciler := &NutanixMachineReconciler{}
		peUUID, subnetUUIDs, err := reconciler.GetSubnetAndPEUUIDsV3(nctx, scope)

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

// TestV3AddBootTypeToVMV3 tests boot type configuration
func TestV3AddBootTypeToVMV3(t *testing.T) {
	ctrl, _ := createMockV3Client(t)
	defer ctrl.Finish()

	nutanixCluster, cluster, machine, nutanixMachine := createV3TestScope()
	nctx := createV3TestContext(nil, nutanixMachine)
	scope := NewNutanixMachineScope(nutanixCluster, nutanixMachine, cluster, machine)

	t.Run("TestV3AddBootTypeLegacy", func(t *testing.T) {
		scope.NutanixMachine.Spec.BootType = infrav1.NutanixBootTypeLegacy
		vmSpec := &prismclientv3.VM{}

		reconciler := &NutanixMachineReconciler{}
		err := reconciler.addBootTypeToVMV3(nctx, scope, vmSpec)

		if err != nil {
			t.Errorf("Expected no error, got: %v", err)
		}
		// Legacy boot should not set boot config (defaults to legacy)
		if vmSpec.Resources != nil && vmSpec.Resources.BootConfig != nil {
			t.Error("Expected no boot config for legacy boot type")
		}
	})

	t.Run("TestV3AddBootTypeUEFI", func(t *testing.T) {
		scope.NutanixMachine.Spec.BootType = infrav1.NutanixBootTypeUEFI
		vmSpec := &prismclientv3.VM{
			Resources: &prismclientv3.VMResources{},
		}

		reconciler := &NutanixMachineReconciler{}
		err := reconciler.addBootTypeToVMV3(nctx, scope, vmSpec)

		if err != nil {
			t.Errorf("Expected no error, got: %v", err)
		}
		if vmSpec.Resources.BootConfig == nil {
			t.Error("Expected boot config to be set for UEFI boot type")
		}
		if vmSpec.Resources.BootConfig != nil && *vmSpec.Resources.BootConfig.BootType != "UEFI" {
			t.Errorf("Expected boot type 'UEFI', got: %s", *vmSpec.Resources.BootConfig.BootType)
		}
	})

	t.Run("TestV3AddBootTypeInvalid", func(t *testing.T) {
		scope.NutanixMachine.Spec.BootType = "invalid"
		vmSpec := &prismclientv3.VM{}

		reconciler := &NutanixMachineReconciler{}
		err := reconciler.addBootTypeToVMV3(nctx, scope, vmSpec)

		if err == nil {
			t.Error("Expected error for invalid boot type, got nil")
		}
	})
}
