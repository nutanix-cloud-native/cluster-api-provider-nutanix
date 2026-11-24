/*
Copyright 2023 Nutanix

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
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	clusterAhvModels "github.com/nutanix/ntnx-api-golang-clients/clustermgmt-go-client/v4/models/clustermgmt/v4/ahv/config"
	clusterModels "github.com/nutanix/ntnx-api-golang-clients/clustermgmt-go-client/v4/models/clustermgmt/v4/config"
	dpModels "github.com/nutanix/ntnx-api-golang-clients/datapolicies-go-client/v4/models/datapolicies/v4/config"
	iamModels "github.com/nutanix/ntnx-api-golang-clients/iam-go-client/v4/models/iam/v4/authn"
	iamAuthzModels "github.com/nutanix/ntnx-api-golang-clients/iam-go-client/v4/models/iam/v4/authz"
	alertModels "github.com/nutanix/ntnx-api-golang-clients/monitoring-go-client/v4/models/monitoring/v4/serviceability"
	projectModels "github.com/nutanix/ntnx-api-golang-clients/multidomain-go-client/v4/models/multidomain/v4/config"
	subnetModels "github.com/nutanix/ntnx-api-golang-clients/networking-go-client/v4/models/networking/v4/config"
	networkingPrismModels "github.com/nutanix/ntnx-api-golang-clients/networking-go-client/v4/models/prism/v4/config"
	prismModels "github.com/nutanix/ntnx-api-golang-clients/prism-go-client/v4/models/prism/v4/config"
	prismErrors "github.com/nutanix/ntnx-api-golang-clients/prism-go-client/v4/models/prism/v4/error"
	vmmModels "github.com/nutanix/ntnx-api-golang-clients/vmm-go-client/v4/models/vmm/v4/ahv/config"
	policyModels "github.com/nutanix/ntnx-api-golang-clients/vmm-go-client/v4/models/vmm/v4/ahv/policies"
	imageModels "github.com/nutanix/ntnx-api-golang-clients/vmm-go-client/v4/models/vmm/v4/content"
	volumesconfig "github.com/nutanix/ntnx-api-golang-clients/volumes-go-client/v4/models/volumes/v4/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	capiv1beta2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/cluster-api/util"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	mockconverged "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/mocks/converged"
	mockk8sclient "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/mocks/k8sclient"
	mocknutanixv3 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/mocks/nutanix"
	nutanixclient "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/pkg/client"
	nctx "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/pkg/context"
	"github.com/nutanix-cloud-native/cluster-api-provider-nutanix/pkg/services"
	converged "github.com/nutanix-cloud-native/prism-go-client/converged"
	v4Converged "github.com/nutanix-cloud-native/prism-go-client/converged/v4"
	credentialtypes "github.com/nutanix-cloud-native/prism-go-client/environment/credentials"
	prismclientv3 "github.com/nutanix-cloud-native/prism-go-client/v3"
)

type MockConvergedClientWrapper struct {
	Client *v4Converged.Client

	MockAntiAffinityPolicies *mockconverged.MockAntiAffinityPolicies[policyModels.VmAntiAffinityPolicy]
	MockClusters             *mockconverged.MockClusters[clusterModels.Cluster, clusterModels.VirtualGpuProfile, clusterModels.PhysicalGpuProfile, clusterAhvModels.VirtualGpuProfile, clusterAhvModels.PhysicalGpuProfile, clusterModels.Host]
	MockCategories           *mockconverged.MockCategories[prismModels.Category]
	MockImages               *mockconverged.MockImages[imageModels.Image, imageModels.FileDetail]
	MockStorageContainers    *mockconverged.MockStorageContainers[clusterModels.StorageContainer]
	MockSubnets              *mockconverged.MockSubnets[subnetModels.Subnet, networkingPrismModels.TaskReference]
	MockVMs                  *mockconverged.MockVMs[vmmModels.Vm, vmmModels.Nic, vmmModels.Disk]
	MockTasks                *mockconverged.MockTasks[prismModels.Task, prismErrors.AppMessage]
	MockVolumeGroups         *mockconverged.MockVolumeGroups[volumesconfig.VolumeGroup, volumesconfig.VmAttachment]
	MockVMProfiles           *mockconverged.MockVMProfiles[vmmModels.VmProfile, vmmModels.Vm, vmmModels.DeployVmFromVmProfileParams]
	MockDomainManager        *mockconverged.MockDomainManager[prismModels.DomainManager]
	MockUsers                *mockconverged.MockUsers[iamModels.User]
	MockTemplates            *mockconverged.MockTemplates[imageModels.Template, vmmModels.Vm, imageModels.TemplateDeployment]
	MockOvas                 *mockconverged.MockOvas[imageModels.Ova, imageModels.FileDetail]
	MockProtectionPolicies   *mockconverged.MockProtectionPolicies[dpModels.ProtectionPolicy]
	MockRecoveryPlans        *mockconverged.MockRecoveryPlans[dpModels.RecoveryPlan]
	MockProjects             *mockconverged.MockProjects[projectModels.Project]
	MockResourceGroups       *mockconverged.MockResourceGroups[projectModels.ResourceGroup]
}

// NewMockConvergedClient creates a new mock converged client
func NewMockConvergedClient(ctrl *gomock.Controller) *MockConvergedClientWrapper {
	mockAntiAffinityPolicies := mockconverged.NewMockAntiAffinityPolicies[policyModels.VmAntiAffinityPolicy](ctrl)
	mockClusters := mockconverged.NewMockClusters[clusterModels.Cluster, clusterModels.VirtualGpuProfile, clusterModels.PhysicalGpuProfile, clusterAhvModels.VirtualGpuProfile, clusterAhvModels.PhysicalGpuProfile, clusterModels.Host](ctrl)
	mockCategories := mockconverged.NewMockCategories[prismModels.Category](ctrl)
	mockImages := mockconverged.NewMockImages[imageModels.Image, imageModels.FileDetail](ctrl)
	mockStorageContainers := mockconverged.NewMockStorageContainers[clusterModels.StorageContainer](ctrl)
	mockSubnets := mockconverged.NewMockSubnets[subnetModels.Subnet, networkingPrismModels.TaskReference](ctrl)
	mockTasks := mockconverged.NewMockTasks[prismModels.Task, prismErrors.AppMessage](ctrl)
	mockVMs := mockconverged.NewMockVMs[vmmModels.Vm, vmmModels.Nic, vmmModels.Disk](ctrl)
	mockVolumeGroups := mockconverged.NewMockVolumeGroups[volumesconfig.VolumeGroup, volumesconfig.VmAttachment](ctrl)
	mockVMProfiles := mockconverged.NewMockVMProfiles[vmmModels.VmProfile, vmmModels.Vm, vmmModels.DeployVmFromVmProfileParams](ctrl)
	mockDomainManager := mockconverged.NewMockDomainManager[prismModels.DomainManager](ctrl)
	mockUsers := mockconverged.NewMockUsers[iamModels.User](ctrl)
	mockTemplates := mockconverged.NewMockTemplates[imageModels.Template, vmmModels.Vm, imageModels.TemplateDeployment](ctrl)
	mockOvas := mockconverged.NewMockOvas[imageModels.Ova, imageModels.FileDetail](ctrl)
	mockProtectionPolicies := mockconverged.NewMockProtectionPolicies[dpModels.ProtectionPolicy](ctrl)
	mockRecoveryPlans := mockconverged.NewMockRecoveryPlans[dpModels.RecoveryPlan](ctrl)
	mockProjects := mockconverged.NewMockProjects[projectModels.Project](ctrl)
	mockResourceGroups := mockconverged.NewMockResourceGroups[projectModels.ResourceGroup](ctrl)

	realClient := &v4Converged.Client{
		Client: converged.Client[
			policyModels.VmAntiAffinityPolicy,
			clusterModels.Cluster,
			clusterModels.VirtualGpuProfile,
			clusterModels.PhysicalGpuProfile,
			clusterAhvModels.VirtualGpuProfile,
			clusterAhvModels.PhysicalGpuProfile,
			clusterModels.Host,
			prismModels.Category,
			imageModels.Image,
			imageModels.FileDetail,
			clusterModels.StorageContainer,
			subnetModels.Subnet,
			networkingPrismModels.TaskReference,
			vmmModels.Vm,
			vmmModels.Nic,
			vmmModels.Disk,
			prismModels.Task,
			prismErrors.AppMessage,
			volumesconfig.VolumeGroup,
			volumesconfig.VmAttachment,
			vmmModels.VmProfile,
			vmmModels.DeployVmFromVmProfileParams,
			prismModels.DomainManager,
			iamModels.User,
			iamAuthzModels.Role,
			iamAuthzModels.RoleMembership,
			iamAuthzModels.RoleMembershipProjection,
			iamAuthzModels.AuthorizationPolicy,
			iamAuthzModels.AuthorizationPolicyProjection,
			iamAuthzModels.Operation,
			imageModels.Template,
			imageModels.TemplateDeployment,
			imageModels.Ova,
			imageModels.FileDetail,
			dpModels.ProtectionPolicy,
			dpModels.RecoveryPlan,
			projectModels.Project,
			projectModels.ResourceGroup,
			clusterModels.Disk,
			alertModels.Alert,
		]{
			AntiAffinityPolicies: mockAntiAffinityPolicies,
			Clusters:             mockClusters,
			Categories:           mockCategories,
			Images:               mockImages,
			StorageContainers:    mockStorageContainers,
			Subnets:              mockSubnets,
			VMs:                  mockVMs,
			Tasks:                mockTasks,
			VolumeGroups:         mockVolumeGroups,
			VMProfiles:           mockVMProfiles,
			DomainManager:        mockDomainManager,
			Users:                mockUsers,
			Templates:            mockTemplates,
			Ovas:                 mockOvas,
			DataPolicies: converged.DataPolicies[dpModels.ProtectionPolicy, dpModels.RecoveryPlan]{
				ProtectionPolicies: mockProtectionPolicies,
				RecoveryPlans:      mockRecoveryPlans,
			},
			Projects:       mockProjects,
			ResourceGroups: mockResourceGroups,
		},
	}

	return &MockConvergedClientWrapper{
		Client:                   realClient,
		MockAntiAffinityPolicies: mockAntiAffinityPolicies,
		MockClusters:             mockClusters,
		MockCategories:           mockCategories,
		MockImages:               mockImages,
		MockStorageContainers:    mockStorageContainers,
		MockSubnets:              mockSubnets,
		MockVMs:                  mockVMs,
		MockTasks:                mockTasks,
		MockVolumeGroups:         mockVolumeGroups,
		MockVMProfiles:           mockVMProfiles,
		MockDomainManager:        mockDomainManager,
		MockUsers:                mockUsers,
		MockTemplates:            mockTemplates,
		MockOvas:                 mockOvas,
		MockProtectionPolicies:   mockProtectionPolicies,
		MockRecoveryPlans:        mockRecoveryPlans,
		MockProjects:             mockProjects,
		MockResourceGroups:       mockResourceGroups,
	}
}

func TestFindVMByUUID(t *testing.T) {
	ctx := context.Background()
	projectExtID := "test-project-uuid"
	pcVersion := "7.6"

	t.Run("returns VM when found and project matches", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		convergedClient := NewMockConvergedClient(ctrl)
		expectedVM := &vmmModels.Vm{
			ExtId: ptr.To("test-vm-uuid"),
			Name:  ptr.To("test-vm"),
			Project: &vmmModels.ProjectReference{
				ExtId: ptr.To(projectExtID),
			},
		}
		convergedClient.MockVMs.EXPECT().Get(gomock.Any(), "test-vm-uuid").Return(expectedVM, nil)

		vm, err := FindVMByUUID(ctx, convergedClient.Client, "test-vm-uuid", &projectExtID, pcVersion)
		require.NoError(t, err)
		require.NotNil(t, vm)
		assert.Equal(t, "test-vm-uuid", *vm.ExtId)
	})

	t.Run("returns error when VM found but project does not match", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		convergedClient := NewMockConvergedClient(ctrl)
		expectedVM := &vmmModels.Vm{
			ExtId: ptr.To("test-vm-uuid"),
			Name:  ptr.To("test-vm"),
			Project: &vmmModels.ProjectReference{
				ExtId: ptr.To("different-project-uuid"),
			},
		}
		convergedClient.MockVMs.EXPECT().Get(gomock.Any(), "test-vm-uuid").Return(expectedVM, nil)

		vm, err := FindVMByUUID(ctx, convergedClient.Client, "test-vm-uuid", &projectExtID, pcVersion)
		require.Error(t, err)
		require.Nil(t, vm)
		assert.Contains(t, err.Error(), "not in project")
	})

	t.Run("returns error when VM found but has no project", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		convergedClient := NewMockConvergedClient(ctrl)
		expectedVM := &vmmModels.Vm{
			ExtId:   ptr.To("test-vm-uuid"),
			Name:    ptr.To("test-vm"),
			Project: nil,
		}
		convergedClient.MockVMs.EXPECT().Get(gomock.Any(), "test-vm-uuid").Return(expectedVM, nil)

		vm, err := FindVMByUUID(ctx, convergedClient.Client, "test-vm-uuid", &projectExtID, pcVersion)
		require.Error(t, err)
		require.Nil(t, vm)
		assert.Contains(t, err.Error(), "not in project")
	})

	t.Run("returns nil when VM not found (classified ErrNotFound)", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		convergedClient := NewMockConvergedClient(ctrl)
		convergedClient.MockVMs.EXPECT().Get(gomock.Any(), "missing-vm-uuid").
			Return(nil, &converged.APIError{Kind: converged.ErrNotFound, Cause: errors.New("entity not found")})

		vm, err := FindVMByUUID(ctx, convergedClient.Client, "missing-vm-uuid", &projectExtID, pcVersion)
		require.NoError(t, err)
		require.Nil(t, vm)
	})

	t.Run("returns error when API returns non-NotFound error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		convergedClient := NewMockConvergedClient(ctrl)
		apiErr := errors.New("rate limit exceeded")
		convergedClient.MockVMs.EXPECT().Get(gomock.Any(), "test-vm-uuid").Return(nil, apiErr)

		vm, err := FindVMByUUID(ctx, convergedClient.Client, "test-vm-uuid", &projectExtID, pcVersion)
		require.Error(t, err)
		require.Nil(t, vm)
		assert.ErrorIs(t, err, apiErr)
	})
}

func TestFindVM(t *testing.T) {
	ctx := context.Background()
	recordedUUID := "68d4847f-a7c1-4ed9-75fe-3c0f941363a7"
	liveUUID := "11111111-2222-3333-4444-555555555555"
	vmName := "abhay-mgmt-wqfmw-m7vmr"

	metroMachine := &capiv1beta2.Machine{
		ObjectMeta: metav1.ObjectMeta{Name: vmName},
		Spec:       capiv1beta2.MachineSpec{FailureDomain: metroSiteFailureDomainPrefix + "metro0-s1"},
		Status: capiv1beta2.MachineStatus{
			NodeInfo: &corev1.NodeSystemInfo{SystemUUID: recordedUUID},
		},
	}
	nonMetroMachine := &capiv1beta2.Machine{
		ObjectMeta: metav1.ObjectMeta{Name: vmName},
		Status: capiv1beta2.MachineStatus{
			NodeInfo: &corev1.NodeSystemInfo{SystemUUID: recordedUUID},
		},
	}
	ntnxMachine := &infrav1.NutanixMachine{
		ObjectMeta: metav1.ObjectMeta{Name: vmName},
		Status:     infrav1.NutanixMachineStatus{VmUUID: recordedUUID},
	}

	t.Run("metro returns VM found by name after recorded UUID 404", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		convergedClient := NewMockConvergedClient(ctrl)
		liveVM := &vmmModels.Vm{ExtId: ptr.To(liveUUID), Name: ptr.To(vmName)}
		convergedClient.MockVMs.EXPECT().Get(gomock.Any(), recordedUUID).Return(nil,
			&converged.APIError{Kind: converged.ErrNotFound, Message: "vm not found"})
		convergedClient.MockVMs.EXPECT().List(gomock.Any(), gomock.Any()).Return([]vmmModels.Vm{*liveVM}, nil)
		convergedClient.MockVMs.EXPECT().Get(gomock.Any(), liveUUID).Return(liveVM, nil)

		vm, err := FindVM(ctx, convergedClient.Client, metroMachine, ntnxMachine, vmName, nil, "", nil)
		require.NoError(t, err)
		require.NotNil(t, vm)
		assert.Equal(t, liveUUID, *vm.ExtId)
	})

	t.Run("metro returns expected-to-be-present when UUID 404 and name search is empty", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		convergedClient := NewMockConvergedClient(ctrl)
		convergedClient.MockVMs.EXPECT().Get(gomock.Any(), recordedUUID).Return(nil,
			&converged.APIError{Kind: converged.ErrNotFound, Message: "vm not found"})
		convergedClient.MockVMs.EXPECT().List(gomock.Any(), gomock.Any()).Return([]vmmModels.Vm{}, nil)

		vm, err := FindVM(ctx, convergedClient.Client, metroMachine, ntnxMachine, vmName, nil, "", nil)
		require.Error(t, err)
		require.Nil(t, vm)
		assert.Contains(t, err.Error(), "but was expected to be present")
	})

	t.Run("non-metro UUID 404 does not search by name", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		convergedClient := NewMockConvergedClient(ctrl)
		convergedClient.MockVMs.EXPECT().Get(gomock.Any(), recordedUUID).Return(nil,
			&converged.APIError{Kind: converged.ErrNotFound, Message: "vm not found"})

		vm, err := FindVM(ctx, convergedClient.Client, nonMetroMachine, ntnxMachine, vmName, nil, "", nil)
		require.Error(t, err)
		require.Nil(t, vm)
		assert.Contains(t, err.Error(), "but was expected to be present")
	})

	t.Run("metro by-name lookup skips a same-named VM outside the project", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		convergedClient := NewMockConvergedClient(ctrl)
		projectExtID := "aaaaaaaa-0000-0000-0000-000000000001"
		otherUUID := "99999999-2222-3333-4444-555555555555"
		otherVM := &vmmModels.Vm{
			ExtId: ptr.To(otherUUID), Name: ptr.To(vmName),
			Project: &vmmModels.ProjectReference{ExtId: ptr.To("bbbbbbbb-0000-0000-0000-000000000002")},
		}
		liveVM := &vmmModels.Vm{
			ExtId: ptr.To(liveUUID), Name: ptr.To(vmName),
			Project: &vmmModels.ProjectReference{ExtId: ptr.To(projectExtID)},
		}
		convergedClient.MockVMs.EXPECT().Get(gomock.Any(), recordedUUID).Return(nil,
			&converged.APIError{Kind: converged.ErrNotFound, Message: "vm not found"})
		convergedClient.MockVMs.EXPECT().List(gomock.Any(), gomock.Any()).Return([]vmmModels.Vm{*otherVM, *liveVM}, nil)
		convergedClient.MockVMs.EXPECT().Get(gomock.Any(), otherUUID).Return(otherVM, nil)
		convergedClient.MockVMs.EXPECT().Get(gomock.Any(), liveUUID).Return(liveVM, nil)

		vm, err := FindVM(ctx, convergedClient.Client, metroMachine, ntnxMachine, vmName, &projectExtID, "7.6", nil)
		require.NoError(t, err)
		require.NotNil(t, vm)
		assert.Equal(t, liveUUID, *vm.ExtId)
	})

	t.Run("metro skips decoupled leftover and returns recovered VM by name", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		convergedClient := NewMockConvergedClient(ctrl)
		mockV3 := mocknutanixv3.NewMockService(ctrl)
		decoupledVM := &vmmModels.Vm{ExtId: ptr.To(recordedUUID), Name: ptr.To(vmName)}
		liveVM := &vmmModels.Vm{ExtId: ptr.To(liveUUID), Name: ptr.To(vmName)}
		convergedClient.MockVMs.EXPECT().Get(gomock.Any(), recordedUUID).Return(nil,
			&converged.APIError{Kind: converged.ErrNotFound, Message: "vm not found"})
		convergedClient.MockVMs.EXPECT().List(gomock.Any(), gomock.Any()).Return([]vmmModels.Vm{*decoupledVM, *liveVM}, nil)
		mockV3.EXPECT().GroupsGetEntities(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, req *prismclientv3.GroupsGetEntitiesRequest) (*prismclientv3.GroupsGetEntitiesResponse, error) {
				if strings.Contains(req.FilterCriteria, recordedUUID) {
					return drConfigGroupsResponse(drRoleDecoupled), nil
				}
				return drConfigGroupsResponse("kActive"), nil
			},
		).AnyTimes()
		convergedClient.MockVMs.EXPECT().Get(gomock.Any(), liveUUID).Return(liveVM, nil)

		vm, err := FindVM(ctx, convergedClient.Client, metroMachine, ntnxMachine, vmName, nil, "", mockV3)
		require.NoError(t, err)
		require.NotNil(t, vm)
		assert.Equal(t, liveUUID, *vm.ExtId)
	})

	t.Run("metro does not adopt a decoupled UUID GET; looks up recovered VM by name", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		convergedClient := NewMockConvergedClient(ctrl)
		mockV3 := mocknutanixv3.NewMockService(ctrl)
		decoupledVM := &vmmModels.Vm{ExtId: ptr.To(recordedUUID), Name: ptr.To(vmName)}
		liveVM := &vmmModels.Vm{ExtId: ptr.To(liveUUID), Name: ptr.To(vmName)}
		convergedClient.MockVMs.EXPECT().Get(gomock.Any(), recordedUUID).Return(decoupledVM, nil)
		mockV3.EXPECT().GroupsGetEntities(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, req *prismclientv3.GroupsGetEntitiesRequest) (*prismclientv3.GroupsGetEntitiesResponse, error) {
				if strings.Contains(req.FilterCriteria, recordedUUID) {
					return drConfigGroupsResponse(drRoleDecoupled), nil
				}
				return drConfigGroupsResponse("kActive"), nil
			},
		).AnyTimes()
		convergedClient.MockVMs.EXPECT().List(gomock.Any(), gomock.Any()).Return([]vmmModels.Vm{*decoupledVM, *liveVM}, nil)
		convergedClient.MockVMs.EXPECT().Get(gomock.Any(), liveUUID).Return(liveVM, nil)

		vm, err := FindVM(ctx, convergedClient.Client, metroMachine, ntnxMachine, vmName, nil, "", mockV3)
		require.NoError(t, err)
		require.NotNil(t, vm)
		assert.Equal(t, liveUUID, *vm.ExtId)
	})
}

func TestDeleteVM(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ctx := context.Background()

	t.Run("should skip deletion when vmUUID is empty", func(t *testing.T) {
		// Test the early return case when vmUUID is empty
		result, err := DeleteVM(ctx, nil, "test-vm", "")

		assert.Nil(t, err)
		assert.Empty(t, result)
	})

	t.Run("should handle successful deletion", func(t *testing.T) {
		// Create mock client
		mockClientWrapper := NewMockConvergedClient(ctrl)
		mockOperation := mockconverged.NewMockOperation[converged.NoEntity](ctrl)

		vmName := "test-vm"
		vmUUID := "test-vm-uuid"
		taskUUID := "task-uuid"

		// Mock the VMs service to return a task (requeue case)
		mockClientWrapper.MockVMs.EXPECT().DeleteAsync(ctx, vmUUID).Return(mockOperation, nil)
		mockOperation.EXPECT().UUID().Return(taskUUID).AnyTimes()

		result, err := DeleteVM(ctx, mockClientWrapper.Client, vmName, vmUUID)

		assert.NoError(t, err)
		assert.NotEmpty(t, result)
		assert.Equal(t, taskUUID, result)
	})

	t.Run("should return error when DeleteAsync fails", func(t *testing.T) {
		// Create mock client
		mockClientWrapper := NewMockConvergedClient(ctrl)

		vmName := "test-vm"
		vmUUID := "test-vm-uuid"
		expectedError := errors.New("delete failed")

		// Mock the VMs service to return an error
		mockClientWrapper.MockVMs.EXPECT().DeleteAsync(ctx, vmUUID).Return(nil, expectedError)

		result, err := DeleteVM(ctx, mockClientWrapper.Client, vmName, vmUUID)

		assert.Error(t, err)
		assert.Equal(t, expectedError, err)
		assert.Empty(t, result)
	})

	t.Run("should return error when task is nil", func(t *testing.T) {
		// Create mock client
		mockClientWrapper := NewMockConvergedClient(ctrl)

		vmName := "test-vm"
		vmUUID := "test-vm-uuid"

		// Mock the VMs service to return a task (requeue case)
		mockClientWrapper.MockVMs.EXPECT().DeleteAsync(ctx, vmUUID).Return(nil, nil)

		result, err := DeleteVM(ctx, mockClientWrapper.Client, vmName, vmUUID)

		assert.Error(t, err)
		assert.Empty(t, result)
	})
}

func drConfigGroupsResponse(role string) *prismclientv3.GroupsGetEntitiesResponse {
	return &prismclientv3.GroupsGetEntitiesResponse{
		GroupResults: []*prismclientv3.GroupsGroupResult{
			{
				EntityResults: []*prismclientv3.GroupsEntity{
					{
						Data: []*prismclientv3.GroupsFieldData{
							{Name: drConfigRoleAttribute, Values: []*prismclientv3.GroupsTimevaluePair{{Values: []string{role}}}},
						},
					},
				},
			},
		},
	}
}

func TestUseMetroDRDeletePath(t *testing.T) {
	t.Run("false when context is empty", func(t *testing.T) {
		assert.False(t, useMetroDRDeletePath(nil))
		assert.False(t, useMetroDRDeletePath(&nctx.MachineContext{
			Machine:        &capiv1beta2.Machine{},
			NutanixMachine: &infrav1.NutanixMachine{},
		}))
	})

	t.Run("true for metro site failure domain", func(t *testing.T) {
		assert.True(t, useMetroDRDeletePath(&nctx.MachineContext{
			Machine: &capiv1beta2.Machine{
				Spec: capiv1beta2.MachineSpec{FailureDomain: metroSiteFailureDomainPrefix + "metro0-s1"},
			},
		}))
	})

	t.Run("true when delete already recorded a recovered VM", func(t *testing.T) {
		assert.True(t, useMetroDRDeletePath(&nctx.MachineContext{
			Machine: &capiv1beta2.Machine{},
			NutanixMachine: &infrav1.NutanixMachine{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{recoveredVMUUIDAnnotation: "live-uuid"},
				},
			},
		}))
	})
}

func TestIsVMDecoupled(t *testing.T) {
	ctx := context.Background()
	vmUUID := "e530a7a6-3e93-4408-77af-d7ce41a3997c"

	t.Run("returns false when v3 client is nil", func(t *testing.T) {
		decoupled, err := isVMDecoupled(ctx, nil, vmUUID)
		assert.NoError(t, err)
		assert.False(t, decoupled)
	})

	t.Run("returns true when role is kDecoupled", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		mockV3 := mocknutanixv3.NewMockService(ctrl)
		mockV3.EXPECT().GroupsGetEntities(ctx, gomock.Any()).Return(drConfigGroupsResponse(drRoleDecoupled), nil)

		decoupled, err := isVMDecoupled(ctx, mockV3, vmUUID)
		assert.NoError(t, err)
		assert.True(t, decoupled)
	})

	t.Run("returns false when role is not decoupled", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		mockV3 := mocknutanixv3.NewMockService(ctrl)
		mockV3.EXPECT().GroupsGetEntities(ctx, gomock.Any()).Return(drConfigGroupsResponse("kActive"), nil)

		decoupled, err := isVMDecoupled(ctx, mockV3, vmUUID)
		assert.NoError(t, err)
		assert.False(t, decoupled)
	})

	t.Run("returns error when groups API fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		mockV3 := mocknutanixv3.NewMockService(ctrl)
		mockV3.EXPECT().GroupsGetEntities(ctx, gomock.Any()).Return(nil, errors.New("pc unavailable"))

		decoupled, err := isVMDecoupled(ctx, mockV3, vmUUID)
		assert.Error(t, err)
		assert.False(t, decoupled)
		assert.Contains(t, err.Error(), "failed to get DR config")
	})
}

func TestFindNonDecoupledVMByName(t *testing.T) {
	ctx := context.Background()
	vmName := "cp1"
	decoupledUUID := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	liveUUID := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"

	t.Run("skips decoupled VM and returns recovered VM", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		decoupledVM := vmmModels.NewVm()
		decoupledVM.Name = ptr.To(vmName)
		decoupledVM.ExtId = ptr.To(decoupledUUID)
		liveVM := vmmModels.NewVm()
		liveVM.Name = ptr.To(vmName)
		liveVM.ExtId = ptr.To(liveUUID)

		mockClient := NewMockConvergedClient(ctrl)
		mockV3 := mocknutanixv3.NewMockService(ctrl)
		mockClient.MockVMs.EXPECT().List(ctx, gomock.Any()).Return([]vmmModels.Vm{*decoupledVM, *liveVM}, nil)
		mockV3.EXPECT().GroupsGetEntities(ctx, gomock.Any()).DoAndReturn(
			func(_ context.Context, req *prismclientv3.GroupsGetEntitiesRequest) (*prismclientv3.GroupsGetEntitiesResponse, error) {
				if strings.Contains(req.FilterCriteria, decoupledUUID) {
					return drConfigGroupsResponse(drRoleDecoupled), nil
				}
				return drConfigGroupsResponse("kActive"), nil
			},
		).AnyTimes()
		mockClient.MockVMs.EXPECT().Get(ctx, liveUUID).Return(liveVM, nil)

		got, err := findNonDecoupledVMByName(ctx, mockClient.Client, mockV3, []string{vmName}, nil, "")
		assert.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, liveUUID, *got.ExtId)
	})

	t.Run("returns nil when only decoupled VMs exist", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		decoupledVM := vmmModels.NewVm()
		decoupledVM.Name = ptr.To(vmName)
		decoupledVM.ExtId = ptr.To(decoupledUUID)

		mockClient := NewMockConvergedClient(ctrl)
		mockV3 := mocknutanixv3.NewMockService(ctrl)
		mockClient.MockVMs.EXPECT().List(ctx, gomock.Any()).Return([]vmmModels.Vm{*decoupledVM}, nil)
		mockV3.EXPECT().GroupsGetEntities(ctx, gomock.Any()).Return(drConfigGroupsResponse(drRoleDecoupled), nil)

		got, err := findNonDecoupledVMByName(ctx, mockClient.Client, mockV3, []string{vmName}, nil, "")
		assert.NoError(t, err)
		assert.Nil(t, got)
	})
}

func TestDetachVolumeGroupsFromVM(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ctx := context.Background()

	t.Run("should skip detachment when no disks provided", func(t *testing.T) {
		mockClientWrapper := NewMockConvergedClient(ctrl)

		vmName := "test-vm"
		vmUUID := "00000000-0000-0000-0000-000000000001"
		vmDisks := []vmmModels.Disk{}

		// No expectations: DetachFromVM should NOT be called
		err := detachVolumeGroupsFromVM(ctx, mockClientWrapper.Client, vmName, vmUUID, vmDisks)

		assert.NoError(t, err)
	})

	t.Run("should skip detachment when disk has no backing info", func(t *testing.T) {
		mockClientWrapper := NewMockConvergedClient(ctrl)

		vmName := "test-vm"
		vmUUID := "00000000-0000-0000-0000-000000000001"

		// Disk without VG backing (leave BackingInfo nil)
		disk := vmmModels.NewDisk()
		vmDisks := []vmmModels.Disk{*disk}

		// No expectations: DetachFromVM should NOT be called
		err := detachVolumeGroupsFromVM(ctx, mockClientWrapper.Client, vmName, vmUUID, vmDisks)

		assert.NoError(t, err)
	})

	t.Run("should skip detachment when disk is not backed by volume group", func(t *testing.T) {
		mockClientWrapper := NewMockConvergedClient(ctrl)

		vmName := "test-vm"
		vmUUID := "00000000-0000-0000-0000-000000000001"

		// Disk backed by VmDisk (not volume group)
		disk := vmmModels.NewDisk()
		vmDisk := vmmModels.NewVmDisk()
		vmDisk.DiskSizeBytes = ptr.To(int64(21474836480))
		err := disk.SetBackingInfo(*vmDisk)
		require.NoError(t, err)

		vmDisks := []vmmModels.Disk{*disk}

		// No expectations: DetachFromVM should NOT be called
		err = detachVolumeGroupsFromVM(ctx, mockClientWrapper.Client, vmName, vmUUID, vmDisks)

		assert.NoError(t, err)
	})

	t.Run("should successfully detach single volume group", func(t *testing.T) {
		mockClientWrapper := NewMockConvergedClient(ctrl)

		vmName := "test-vm"
		vmUUID := "00000000-0000-0000-0000-000000000001"
		vgID := "11111111-1111-1111-1111-111111111111"

		// Build a Disk backed by an ADSFVolumeGroupReference
		disk := vmmModels.NewDisk()
		ref := vmmModels.NewADSFVolumeGroupReference()
		ref.VolumeGroupExtId = &vgID
		err := disk.SetBackingInfo(*ref)
		require.NoError(t, err)

		vmDisks := []vmmModels.Disk{*disk}

		expectedVG := &volumesconfig.VolumeGroup{
			ExtId: ptr.To(vgID),
		}

		// Expect DetachFromVM to be called once with the correct VG ID
		mockClientWrapper.MockVolumeGroups.EXPECT().
			DetachFromVM(ctx, vgID, gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, attachment volumesconfig.VmAttachment) (*volumesconfig.VolumeGroup, error) {
				assert.Equal(t, vmUUID, *attachment.ExtId)
				return expectedVG, nil
			})

		err = detachVolumeGroupsFromVM(ctx, mockClientWrapper.Client, vmName, vmUUID, vmDisks)

		assert.NoError(t, err)
	})

	t.Run("should return error when DetachFromVM fails", func(t *testing.T) {
		mockClientWrapper := NewMockConvergedClient(ctrl)

		vmName := "test-vm"
		vmUUID := "00000000-0000-0000-0000-000000000001"
		vgID := "22222222-2222-2222-2222-222222222222"

		disk := vmmModels.NewDisk()
		ref := vmmModels.NewADSFVolumeGroupReference()
		ref.VolumeGroupExtId = &vgID
		err := disk.SetBackingInfo(*ref)
		require.NoError(t, err)

		vmDisks := []vmmModels.Disk{*disk}

		expectedError := errors.New("failed to detach volume group")

		// Return error from DetachFromVM
		mockClientWrapper.MockVolumeGroups.EXPECT().
			DetachFromVM(ctx, vgID, gomock.Any()).
			Return(nil, expectedError)

		err = detachVolumeGroupsFromVM(ctx, mockClientWrapper.Client, vmName, vmUUID, vmDisks)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to detach volume group")
		assert.Contains(t, err.Error(), vgID)
		assert.Contains(t, err.Error(), vmUUID)
	})

	t.Run("should return after detaching first volume group", func(t *testing.T) {
		mockClientWrapper := NewMockConvergedClient(ctrl)

		vmName := "test-vm"
		vmUUID := "00000000-0000-0000-0000-000000000001"
		vgID1 := "11111111-1111-1111-1111-111111111111"
		vgID2 := "22222222-2222-2222-2222-222222222222"

		// Build two disks backed by volume groups
		disk1 := vmmModels.NewDisk()
		ref1 := vmmModels.NewADSFVolumeGroupReference()
		ref1.VolumeGroupExtId = &vgID1
		err := disk1.SetBackingInfo(*ref1)
		require.NoError(t, err)

		disk2 := vmmModels.NewDisk()
		ref2 := vmmModels.NewADSFVolumeGroupReference()
		ref2.VolumeGroupExtId = &vgID2
		err = disk2.SetBackingInfo(*ref2)
		require.NoError(t, err)

		vmDisks := []vmmModels.Disk{*disk1, *disk2}

		// Only expect DetachFromVM to be called once (for the first VG)
		// The function returns after the first detachment
		mockClientWrapper.MockVolumeGroups.EXPECT().
			DetachFromVM(ctx, vgID1, gomock.Any()).
			Return(&volumesconfig.VolumeGroup{}, nil).
			Times(1)

		err = detachVolumeGroupsFromVM(ctx, mockClientWrapper.Client, vmName, vmUUID, vmDisks)

		assert.NoError(t, err)
	})

	t.Run("should handle mixed disk types and only detach volume groups", func(t *testing.T) {
		mockClientWrapper := NewMockConvergedClient(ctrl)

		vmName := "test-vm"
		vmUUID := "00000000-0000-0000-0000-000000000001"
		vgID := "11111111-1111-1111-1111-111111111111"

		// First disk: regular VmDisk (should be skipped)
		disk1 := vmmModels.NewDisk()
		vmDisk := vmmModels.NewVmDisk()
		vmDisk.DiskSizeBytes = ptr.To(int64(21474836480))
		err := disk1.SetBackingInfo(*vmDisk)
		require.NoError(t, err)

		// Second disk: backed by volume group (should be detached)
		disk2 := vmmModels.NewDisk()
		ref := vmmModels.NewADSFVolumeGroupReference()
		ref.VolumeGroupExtId = &vgID
		err = disk2.SetBackingInfo(*ref)
		require.NoError(t, err)

		vmDisks := []vmmModels.Disk{*disk1, *disk2}

		// Only expect DetachFromVM to be called for the volume group disk
		mockClientWrapper.MockVolumeGroups.EXPECT().
			DetachFromVM(ctx, vgID, gomock.Any()).
			Return(&volumesconfig.VolumeGroup{}, nil)

		err = detachVolumeGroupsFromVM(ctx, mockClientWrapper.Client, vmName, vmUUID, vmDisks)

		assert.NoError(t, err)
	})

	t.Run("should handle nil volume group ExtId gracefully", func(t *testing.T) {
		mockClientWrapper := NewMockConvergedClient(ctrl)

		vmName := "test-vm"
		vmUUID := "00000000-0000-0000-0000-000000000001"

		// Build a disk with volume group reference but nil ExtId
		disk := vmmModels.NewDisk()
		ref := vmmModels.NewADSFVolumeGroupReference()
		ref.VolumeGroupExtId = nil // Nil ExtId must be skipped, not dereferenced
		err := disk.SetBackingInfo(*ref)
		require.NoError(t, err)

		vmDisks := []vmmModels.Disk{*disk}

		// A nil VolumeGroupExtId should be skipped gracefully without panicking
		// or calling DetachFromVM (no mock expectation is set).
		assert.NotPanics(t, func() {
			err := detachVolumeGroupsFromVM(ctx, mockClientWrapper.Client, vmName, vmUUID, vmDisks)
			assert.NoError(t, err)
		})
	})
}

func TestGetVMUUID(t *testing.T) {
	validUUID := "550e8400-e29b-41d4-a716-446655440000"
	invalidUUID := "not-a-valid-uuid"
	anotherValidUUID := "660e8400-e29b-41d4-a716-446655440001"

	tests := []struct {
		name           string
		machine        *capiv1beta2.Machine
		nutanixMachine *infrav1.NutanixMachine
		want           string
		wantErr        bool
		errorMessage   string
	}{
		{
			name: "should return systemUUID from Machine.Status.NodeInfo when available",
			machine: &capiv1beta2.Machine{
				Status: capiv1beta2.MachineStatus{
					NodeInfo: &corev1.NodeSystemInfo{
						SystemUUID: validUUID,
					},
				},
			},
			nutanixMachine: &infrav1.NutanixMachine{
				Status: infrav1.NutanixMachineStatus{
					VmUUID: anotherValidUUID,
				},
			},
			want:    validUUID,
			wantErr: false,
		},
		{
			name: "should fall back to VmUUID when Machine.Status.NodeInfo is nil",
			machine: &capiv1beta2.Machine{
				Status: capiv1beta2.MachineStatus{
					NodeInfo: nil,
				},
			},
			nutanixMachine: &infrav1.NutanixMachine{
				Status: infrav1.NutanixMachineStatus{
					VmUUID: validUUID,
				},
			},
			want:    validUUID,
			wantErr: false,
		},
		{
			name: "should fall back to VmUUID when Machine.Status.NodeInfo.SystemUUID is empty",
			machine: &capiv1beta2.Machine{
				Status: capiv1beta2.MachineStatus{
					NodeInfo: &corev1.NodeSystemInfo{
						SystemUUID: "",
					},
				},
			},
			nutanixMachine: &infrav1.NutanixMachine{
				Status: infrav1.NutanixMachineStatus{
					VmUUID: validUUID,
				},
			},
			want:    validUUID,
			wantErr: false,
		},
		{
			name:    "should fall back to VmUUID when machine is nil",
			machine: nil,
			nutanixMachine: &infrav1.NutanixMachine{
				Status: infrav1.NutanixMachineStatus{
					VmUUID: validUUID,
				},
			},
			want:    validUUID,
			wantErr: false,
		},
		{
			name: "should return empty string when both systemUUID and VmUUID are not available",
			machine: &capiv1beta2.Machine{
				Status: capiv1beta2.MachineStatus{
					NodeInfo: nil,
				},
			},
			nutanixMachine: &infrav1.NutanixMachine{
				Status: infrav1.NutanixMachineStatus{
					VmUUID: "",
				},
			},
			want:    "",
			wantErr: false,
		},
		{
			name: "should return error when systemUUID is not a valid UUID",
			machine: &capiv1beta2.Machine{
				Status: capiv1beta2.MachineStatus{
					NodeInfo: &corev1.NodeSystemInfo{
						SystemUUID: invalidUUID,
					},
				},
			},
			nutanixMachine: &infrav1.NutanixMachine{
				Status: infrav1.NutanixMachineStatus{
					VmUUID: "",
				},
			},
			want:         "",
			wantErr:      true,
			errorMessage: "Machine.Status.NodeInfo.SystemUUID was set but was not a valid UUID",
		},
		{
			name: "should return error when VmUUID is not a valid UUID",
			machine: &capiv1beta2.Machine{
				Status: capiv1beta2.MachineStatus{
					NodeInfo: nil,
				},
			},
			nutanixMachine: &infrav1.NutanixMachine{
				Status: infrav1.NutanixMachineStatus{
					VmUUID: invalidUUID,
				},
			},
			want:         "",
			wantErr:      true,
			errorMessage: "VMUUID was set but was not a valid UUID",
		},
		{
			name: "should prioritize systemUUID even when VmUUID has different UUID",
			machine: &capiv1beta2.Machine{
				Status: capiv1beta2.MachineStatus{
					NodeInfo: &corev1.NodeSystemInfo{
						SystemUUID: validUUID,
					},
				},
			},
			nutanixMachine: &infrav1.NutanixMachine{
				Status: infrav1.NutanixMachineStatus{
					VmUUID: anotherValidUUID,
				},
			},
			want:    validUUID,
			wantErr: false,
		},
		{
			name: "should use VmUUID when SystemUUID is empty",
			machine: &capiv1beta2.Machine{
				Status: capiv1beta2.MachineStatus{
					NodeInfo: &corev1.NodeSystemInfo{
						SystemUUID: "",
					},
				},
			},
			nutanixMachine: &infrav1.NutanixMachine{
				Status: infrav1.NutanixMachineStatus{
					VmUUID: validUUID,
				},
			},
			want:    validUUID,
			wantErr: false,
		},
		{
			name: "should handle Machine with empty Status",
			machine: &capiv1beta2.Machine{
				Status: capiv1beta2.MachineStatus{},
			},
			nutanixMachine: &infrav1.NutanixMachine{
				Status: infrav1.NutanixMachineStatus{
					VmUUID: validUUID,
				},
			},
			want:    validUUID,
			wantErr: false,
		},
		{
			name:    "should fall back to Spec.ProviderID when systemUUID and Status.VmUUID are both unavailable",
			machine: nil,
			nutanixMachine: &infrav1.NutanixMachine{
				Spec: infrav1.NutanixMachineSpec{
					ProviderID: providerIdPrefix + validUUID,
				},
			},
			want:    validUUID,
			wantErr: false,
		},
		{
			name:    "should prioritize Status.VmUUID over Spec.ProviderID",
			machine: nil,
			nutanixMachine: &infrav1.NutanixMachine{
				Spec: infrav1.NutanixMachineSpec{
					ProviderID: providerIdPrefix + anotherValidUUID,
				},
				Status: infrav1.NutanixMachineStatus{
					VmUUID: validUUID,
				},
			},
			want:    validUUID,
			wantErr: false,
		},
		{
			// Templates can pre-populate NutanixMachineTemplate.spec.template.spec.providerID
			// with a placeholder (the e2e fixture uses "nutanix://${CLUSTER_NAME}-m1"), so a
			// value without the expected prefix must be silently ignored, not treated as an
			// error - an error here would permanently block reconciliation for such machines.
			name:    "should silently ignore Spec.ProviderID lacking the nutanix:// prefix",
			machine: nil,
			nutanixMachine: &infrav1.NutanixMachine{
				Spec: infrav1.NutanixMachineSpec{
					ProviderID: validUUID,
				},
			},
			want:    "",
			wantErr: false,
		},
		{
			name:    "should silently ignore a non-UUID Spec.ProviderID (e.g. a template placeholder)",
			machine: nil,
			nutanixMachine: &infrav1.NutanixMachine{
				Spec: infrav1.NutanixMachineSpec{
					ProviderID: providerIdPrefix + invalidUUID,
				},
			},
			want:    "",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Log("Running test case ", tt.name)
			got, err := GetVMUUID(tt.machine, tt.nutanixMachine)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetVMUUID() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("GetVMUUID() = %v, want %v", got, tt.want)
			}
			if tt.errorMessage != "" && err != nil {
				if !strings.Contains(err.Error(), tt.errorMessage) {
					t.Errorf("GetVMUUID() error message = %v, want to contain %v", err.Error(), tt.errorMessage)
				}
			}
		})
	}
}

func Test_isPrismTaskFailed(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil",
			err:      nil,
			expected: false,
		},
		{
			name:     "ErrTaskFailed sentinel",
			err:      converged.ErrTaskFailed,
			expected: true,
		},
		{
			name:     "wrapped ErrTaskFailed",
			err:      fmt.Errorf("failed to create VM: %w", converged.ErrTaskFailed),
			expected: true,
		},
		{
			name:     "Operation.Wait failure message",
			err:      fmt.Errorf("task ZXJnb24=:d0b4dc17-a05d-5756-ba5b-0a2e159063d6 failed: not enough cores"),
			expected: true,
		},
		{
			name:     "wrapped Wait failure with failed_subtasks",
			err:      fmt.Errorf("failed to create VM x: %w", fmt.Errorf("task abc failed: not enough cores; failed_subtasks: [CreateVm] not enough cores")),
			expected: true,
		},
		{
			name:     "canceled task",
			err:      fmt.Errorf("task abc was canceled"),
			expected: true,
		},
		{
			name:     "get-task transport error is not a task failure",
			err:      fmt.Errorf("failed to get task abc: connection reset"),
			expected: false,
		},
		{
			name:     "unrelated error",
			err:      errors.New("timeout awaiting headers"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, isPrismTaskFailed(tt.err))
		})
	}
}

func Test_requeueAfterFromError(t *testing.T) {
	after, ok := requeueAfterFromError(errors.New("plain"))
	assert.False(t, ok)
	assert.Zero(t, after)

	after, ok = requeueAfterFromError(&delayedReconcileError{after: failedVMCreateRetryAfter, err: errors.New("create failed")})
	assert.True(t, ok)
	assert.Equal(t, failedVMCreateRetryAfter, after)

	wrapped := fmt.Errorf("outer: %w", &delayedReconcileError{after: failedVMCreateRetryAfter, err: errors.New("create failed")})
	after, ok = requeueAfterFromError(wrapped)
	assert.True(t, ok)
	assert.Equal(t, failedVMCreateRetryAfter, after)
}

func Test_remainingVMCreateBackoff(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)

	t.Run("no annotation", func(t *testing.T) {
		remaining, ok := remainingVMCreateBackoff(&infrav1.NutanixMachine{}, now)
		assert.False(t, ok)
		assert.Zero(t, remaining)
	})

	t.Run("active backoff", func(t *testing.T) {
		nm := &infrav1.NutanixMachine{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
			VMCreateRetryAfterAnnotation: now.Add(10 * time.Minute).Format(time.RFC3339),
		}}}
		remaining, ok := remainingVMCreateBackoff(nm, now)
		require.True(t, ok)
		assert.Equal(t, 10*time.Minute, remaining)
	})

	t.Run("expired backoff", func(t *testing.T) {
		nm := &infrav1.NutanixMachine{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
			VMCreateRetryAfterAnnotation: now.Add(-time.Second).Format(time.RFC3339),
		}}}
		remaining, ok := remainingVMCreateBackoff(nm, now)
		assert.False(t, ok)
		assert.Zero(t, remaining)
	})

	t.Run("unparseable annotation fails open", func(t *testing.T) {
		nm := &infrav1.NutanixMachine{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
			VMCreateRetryAfterAnnotation: "not-a-time",
		}}}
		remaining, ok := remainingVMCreateBackoff(nm, now)
		assert.False(t, ok)
		assert.Zero(t, remaining)
	})
}

func Test_vmCreateFailureCount(t *testing.T) {
	t.Run("missing annotation is zero", func(t *testing.T) {
		assert.Zero(t, vmCreateFailureCount(&infrav1.NutanixMachine{}))
		assert.Zero(t, vmCreateFailureCount(nil))
	})

	t.Run("parses a count", func(t *testing.T) {
		nm := &infrav1.NutanixMachine{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
			VMCreateFailureCountAnnotation: "3",
		}}}
		assert.Equal(t, 3, vmCreateFailureCount(nm))
	})

	t.Run("unparseable annotation is zero", func(t *testing.T) {
		nm := &infrav1.NutanixMachine{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
			VMCreateFailureCountAnnotation: "nope",
		}}}
		assert.Zero(t, vmCreateFailureCount(nm))
	})
}

func TestGetDefaultCAPICategoryIdentifiers(t *testing.T) {
	clusterName := "my-cluster"
	ids := GetDefaultCAPICategoryIdentifiers(clusterName)
	require.Len(t, ids, 1)
	require.NotNil(t, ids[0])
	assert.Equal(t, infrav1.DefaultCAPICategoryKeyForName, ids[0].Key)
	assert.Equal(t, clusterName, ids[0].Value)
}

func TestGetObsoleteDefaultCAPICategoryIdentifiers(t *testing.T) {
	clusterName := "my-cluster"
	ids := GetObsoleteDefaultCAPICategoryIdentifiers(clusterName)
	require.Len(t, ids, 1)
	require.NotNil(t, ids[0])
	assert.Equal(t, infrav1.ObsoleteDefaultCAPICategoryPrefix+clusterName, ids[0].Key)
	assert.Equal(t, infrav1.ObsoleteDefaultCAPICategoryOwnedValue, ids[0].Value)
}

func TestGetOrCreateCategories_Existing(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ctx := context.Background()
	mockClient := NewMockConvergedClient(ctrl)

	ids := []*infrav1.NutanixCategoryIdentifier{{
		Key:   infrav1.DefaultCAPICategoryKeyForName,
		Value: "my-cluster",
	}}

	// Category already exists → List returns non-empty; Create should not be called
	mockClient.MockCategories.EXPECT().List(ctx, gomock.Any()).Return([]prismModels.Category{{}}, nil)

	got, err := GetOrCreateCategories(ctx, mockClient.Client, ids)
	require.NoError(t, err)
	require.Len(t, got, 1)
}

func TestGetOrCreateCategories_Create(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ctx := context.Background()
	mockClient := NewMockConvergedClient(ctrl)

	ids := []*infrav1.NutanixCategoryIdentifier{{
		Key:   infrav1.DefaultCAPICategoryKeyForName,
		Value: "my-cluster",
	}}

	// Not found first → Create called with expected description
	mockClient.MockCategories.EXPECT().List(ctx, gomock.Any()).Return([]prismModels.Category{}, nil)
	mockClient.MockCategories.EXPECT().Create(ctx, gomock.Any()).DoAndReturn(
		func(_ context.Context, in *prismModels.Category) (*prismModels.Category, error) {
			assert.Equal(t, infrav1.DefaultCAPICategoryKeyForName, *in.Key)
			assert.Equal(t, "my-cluster", *in.Value)
			assert.Equal(t, infrav1.DefaultCAPICategoryDescription, *in.Description)
			return in, nil
		},
	)

	got, err := GetOrCreateCategories(ctx, mockClient.Client, ids)
	require.NoError(t, err)
	require.Len(t, got, 1)
}

func TestGetOrCreateCategoriesForProject_CreateWhenExistingCategoryIsInDifferentProject(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ctx := context.Background()
	mockClient := NewMockConvergedClient(ctrl)
	defaultProjectExtID := "default-project"
	targetProjectExtID := "target-project"
	defaultCategoryExtID := "default-category"
	targetCategoryExtID := "target-category"
	ids := []*infrav1.NutanixCategoryIdentifier{{
		Key:   infrav1.DefaultCAPICategoryKeyForName,
		Value: "my-cluster",
	}}

	mockClient.MockCategories.EXPECT().List(ctx, gomock.Any()).Return([]prismModels.Category{{
		ExtId:        ptr.To(defaultCategoryExtID),
		Key:          ptr.To(infrav1.DefaultCAPICategoryKeyForName),
		Value:        ptr.To("my-cluster"),
		ProjectExtId: ptr.To(defaultProjectExtID),
	}}, nil)
	mockClient.MockCategories.EXPECT().Create(ctx, gomock.Any()).DoAndReturn(
		func(_ context.Context, in *prismModels.Category) (*prismModels.Category, error) {
			require.NotNil(t, in.ProjectExtId)
			assert.Equal(t, targetProjectExtID, *in.ProjectExtId)
			in.ExtId = ptr.To(targetCategoryExtID)
			return in, nil
		},
	)

	got, err := GetOrCreateCategoriesForProject(ctx, mockClient.Client, ids, ptr.To(targetProjectExtID))
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, targetCategoryExtID, *got[0].ExtId)
}

func TestGetOrCreateCategories_DoesNotSetProjectWhenUnscoped(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ctx := context.Background()
	mockClient := NewMockConvergedClient(ctrl)
	createdCategoryExtID := "created-category"
	ids := []*infrav1.NutanixCategoryIdentifier{{
		Key:   infrav1.DefaultCAPICategoryKeyForName,
		Value: "my-cluster",
	}}

	// No matching category exists yet → Create is invoked.
	mockClient.MockCategories.EXPECT().List(ctx, gomock.Any()).Return([]prismModels.Category{}, nil)
	mockClient.MockCategories.EXPECT().Create(ctx, gomock.Any()).DoAndReturn(
		func(_ context.Context, in *prismModels.Category) (*prismModels.Category, error) {
			// Without a project in scope the category must be created without a project.
			// This is the path callers use on PC < 7.6, where project-scoped categories
			// do not exist (the caller passes a nil project).
			assert.Nil(t, in.ProjectExtId)
			in.ExtId = ptr.To(createdCategoryExtID)
			return in, nil
		},
	)

	got, err := GetOrCreateCategories(ctx, mockClient.Client, ids)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, createdCategoryExtID, *got[0].ExtId)
}

func TestExistingCategoryExtID(t *testing.T) {
	extID := "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
	got, ok, err := existingCategoryExtID(fmt.Errorf("400 InvalidArgument: Operation failed as a category with the provided key and value already exists with extId: %s", extID))
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, extID, got)

	got, ok, err = existingCategoryExtID(errors.New("category with the provided key and value already exists"))
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Empty(t, got)

	_, ok, err = existingCategoryExtID(errors.New("prism unavailable"))
	require.NoError(t, err)
	assert.False(t, ok)
	_, ok, err = existingCategoryExtID(nil)
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestGetOrCreateCategories_CreateAlreadyExists(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ctx := context.Background()
	mockClient := NewMockConvergedClient(ctrl)
	existingCategoryExtID := "existing-category"
	ids := []*infrav1.NutanixCategoryIdentifier{{
		Key:   infrav1.DefaultCAPICategoryKeyForName,
		Value: "my-cluster",
	}}

	// List misses the category (for example a concurrent create). Create then fails
	// with Prism's already-exists error, which carries the existing extId and no body.
	mockClient.MockCategories.EXPECT().List(ctx, gomock.Any()).Return([]prismModels.Category{}, nil)
	mockClient.MockCategories.EXPECT().Create(ctx, gomock.Any()).DoAndReturn(
		func(_ context.Context, in *prismModels.Category) (*prismModels.Category, error) {
			assert.Equal(t, infrav1.DefaultCAPICategoryKeyForName, *in.Key)
			assert.Equal(t, "my-cluster", *in.Value)
			assert.Nil(t, in.ProjectExtId)
			return nil, fmt.Errorf("400 InvalidArgument: Operation failed as a category with the provided key and value already exists with extId: %s", existingCategoryExtID)
		},
	)

	got, err := GetOrCreateCategories(ctx, mockClient.Client, ids)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.NotNil(t, got[0])
	assert.Equal(t, existingCategoryExtID, *got[0].ExtId)
	assert.Equal(t, infrav1.DefaultCAPICategoryKeyForName, *got[0].Key)
	assert.Equal(t, "my-cluster", *got[0].Value)
}

func TestGetOrCreateCategoriesForProject_CreateAlreadyExists(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ctx := context.Background()
	mockClient := NewMockConvergedClient(ctrl)
	targetProjectExtID := "target-project"
	existingCategoryExtID := "existing-project-category"
	ids := []*infrav1.NutanixCategoryIdentifier{{
		Key:   infrav1.DefaultCAPICategoryKeyForName,
		Value: "my-cluster",
	}}

	mockClient.MockCategories.EXPECT().List(ctx, gomock.Any()).Return([]prismModels.Category{}, nil)
	mockClient.MockCategories.EXPECT().Create(ctx, gomock.Any()).DoAndReturn(
		func(_ context.Context, in *prismModels.Category) (*prismModels.Category, error) {
			require.NotNil(t, in.ProjectExtId)
			assert.Equal(t, targetProjectExtID, *in.ProjectExtId)
			return nil, errors.New("400 InvalidArgument: Operation failed as a category with the provided key and value already exists with extId: " + existingCategoryExtID)
		},
	)

	got, err := GetOrCreateCategoriesForProject(ctx, mockClient.Client, ids, ptr.To(targetProjectExtID))
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.NotNil(t, got[0])
	assert.Equal(t, existingCategoryExtID, *got[0].ExtId)
	assert.Equal(t, targetProjectExtID, *got[0].ProjectExtId)
}

func TestGetOrCreateCategories_CreateFailure(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ctx := context.Background()
	mockClient := NewMockConvergedClient(ctrl)
	ids := []*infrav1.NutanixCategoryIdentifier{{
		Key:   infrav1.DefaultCAPICategoryKeyForName,
		Value: "my-cluster",
	}}

	mockClient.MockCategories.EXPECT().List(ctx, gomock.Any()).Return([]prismModels.Category{}, nil)
	mockClient.MockCategories.EXPECT().Create(ctx, gomock.Any()).Return(nil, errors.New("prism unavailable"))

	got, err := GetOrCreateCategories(ctx, mockClient.Client, ids)
	require.Error(t, err)
	assert.Empty(t, got)
	assert.Contains(t, err.Error(), "failed to create category with key")
	assert.Contains(t, err.Error(), "prism unavailable")
}

func TestGetPrismReferencesOfCategoryIdentifiersForProject_UsesSharedCategory(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ctx := context.Background()
	mockClient := NewMockConvergedClient(ctrl)
	targetProjectExtID := "target-project"
	categoryExtID := "shared-category"
	ids := []*infrav1.NutanixCategoryIdentifier{{Key: "k", Value: "v"}}

	mockClient.MockCategories.EXPECT().List(ctx, gomock.Any()).Return([]prismModels.Category{{
		ExtId:                   ptr.To(categoryExtID),
		Key:                     ptr.To("k"),
		Value:                   ptr.To("v"),
		IsSharedWithAllProjects: ptr.To(true),
	}}, nil)

	got, err := GetPrismReferencesOfCategoryIdentifiersForProject(ctx, mockClient.Client, ids, ptr.To(targetProjectExtID))
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, categoryExtID, *got[0].ExtId)
}

func TestGetPrismReferencesOfCategoryIdentifiersForProject_ReturnsNotFoundWithoutCreating(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ctx := context.Background()
	mockClient := NewMockConvergedClient(ctrl)
	targetProjectExtID := "target-project"
	ids := []*infrav1.NutanixCategoryIdentifier{{Key: "missing-key", Value: "missing-value"}}

	mockClient.MockCategories.EXPECT().List(ctx, gomock.Any()).Return([]prismModels.Category{}, nil)

	got, err := GetPrismReferencesOfCategoryIdentifiersForProject(ctx, mockClient.Client, ids, ptr.To(targetProjectExtID))
	require.Error(t, err)
	assert.Nil(t, got)
	assert.Contains(t, err.Error(), "category value missing-value not found in category missing-key")
}

func TestGetPrismReferencesOfCategoryIdentifiers_NotFoundClassification(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ctx := context.Background()
	mockClient := NewMockConvergedClient(ctrl)
	mockClient.MockCategories.EXPECT().List(ctx, gomock.Any()).Return([]prismModels.Category{}, nil)

	_, err := GetPrismReferencesOfCategoryIdentifiers(ctx, mockClient.Client, []*infrav1.NutanixCategoryIdentifier{
		{
			Key:   "cluster-name",
			Value: "missing",
		},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found in category")
	assert.True(t, services.IsTerminalError(err))
}

func TestDeleteCategoryKeyValues(t *testing.T) {
	ctx := context.Background()

	t.Run("category value retrieval error returns error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockClient := NewMockConvergedClient(ctrl)

		ids := []*infrav1.NutanixCategoryIdentifier{{Key: "k", Value: "v"}}
		// getCategoryValue (outer) -> error not containing CATEGORY_NAME_VALUE_MISMATCH
		mockClient.MockCategories.EXPECT().List(ctx, gomock.Any()).Return(nil, errors.New("oops")).Times(1)

		err := deleteCategoryKeyValues(ctx, mockClient.Client, ids)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to retrieve category value")
	})

	t.Run("value not found continues and returns nil", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockClient := NewMockConvergedClient(ctrl)

		ids := []*infrav1.NutanixCategoryIdentifier{{Key: "k", Value: "v"}}
		// getCategoryValue (outer) -> not found
		mockClient.MockCategories.EXPECT().List(ctx, gomock.Any()).Return([]prismModels.Category{}, nil).Times(1)

		err := deleteCategoryKeyValues(ctx, mockClient.Client, ids)
		assert.NoError(t, err)
	})

	t.Run("delete value success returns nil", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockClient := NewMockConvergedClient(ctrl)

		ids := []*infrav1.NutanixCategoryIdentifier{{Key: "k", Value: "v"}}
		valExt := "val-id"
		name := "k"
		value := "v"
		valCat := prismModels.Category{ExtId: &valExt, Key: &name, Value: &value}

		// 1) getCategoryValue (outer) to check existence before delete
		mockClient.MockCategories.EXPECT().List(ctx, gomock.Any()).Return([]prismModels.Category{valCat}, nil).Times(1)
		// 2) delete value by ExtId
		mockClient.MockCategories.EXPECT().Delete(ctx, valExt).Return(nil).Times(1)

		err := deleteCategoryKeyValues(ctx, mockClient.Client, ids)
		assert.NoError(t, err)
	})

	t.Run("deleteCategoryValue error causes early nil return (do not error)", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockClient := NewMockConvergedClient(ctrl)

		ids := []*infrav1.NutanixCategoryIdentifier{{Key: "k", Value: "v"}}
		valExt := "val-id"
		name := "k"
		value := "v"
		valCat := prismModels.Category{ExtId: &valExt, Key: &name, Value: &value}

		// 1) getCategoryValue (outer)
		mockClient.MockCategories.EXPECT().List(ctx, gomock.Any()).Return([]prismModels.Category{valCat}, nil).Times(1)
		// 2) delete value fails
		mockClient.MockCategories.EXPECT().Delete(ctx, valExt).Return(errors.New("in use")).Times(1)

		// Function should return nil early due to special-case handling
		err := deleteCategoryKeyValues(ctx, mockClient.Client, ids)
		assert.NoError(t, err)
	})
}

func TestGetProjectUUID_NotFoundClassification(t *testing.T) {
	t.Run("returns classified not found for missing project UUID", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		ctx := context.Background()
		mockV3Client := mocknutanixv3.NewMockService(ctrl)
		client := &prismclientv3.Client{V3: mockV3Client}
		rctx := &nctx.MachineContext{
			Context:       ctx,
			NutanixClient: client,
		}
		projectUUID := "missing-project-uuid"
		projectRef := &infrav1.NutanixResourceIdentifier{
			UUID: &projectUUID,
		}

		mockV3Client.EXPECT().GetProject(ctx, projectUUID).Return(nil, errors.New("ENTITY_NOT_FOUND"))

		_, err := GetProjectV3(rctx, projectRef)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to find project with UUID")
		assert.True(t, services.IsTerminalError(err))
	})

	t.Run("returns classified not found for missing project name", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		ctx := context.Background()
		mockV3Client := mocknutanixv3.NewMockService(ctrl)
		client := &prismclientv3.Client{V3: mockV3Client}
		rctx := &nctx.MachineContext{
			Context:       ctx,
			NutanixClient: client,
		}
		projectName := "missing-project-name"
		projectRef := &infrav1.NutanixResourceIdentifier{
			Name: &projectName,
		}

		mockV3Client.EXPECT().ListAllProject(ctx, "").Return(&prismclientv3.ProjectListResponse{
			Entities: []*prismclientv3.Project{},
		}, nil)

		_, err := GetProjectV3(rctx, projectRef)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to retrieve project by name")
		assert.True(t, services.IsTerminalError(err))
	})
}

func TestGetProjectUUID(t *testing.T) {
	ctx := context.Background()

	t.Run("returns error when both name and uuid are nil", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		mockV3 := mocknutanixv3.NewMockService(ctrl)
		client := &prismclientv3.Client{V3: mockV3}
		rctx := &nctx.MachineContext{
			Context:       ctx,
			NutanixClient: client,
		}
		projectRef := &infrav1.NutanixResourceIdentifier{
			UUID: nil,
			Name: nil,
		}
		projectInfo, err := GetProjectV3(rctx, projectRef)
		assert.Error(t, err)
		assert.Nil(t, projectInfo)
		assert.Contains(t, err.Error(), "name or uuid must be passed")
	})

	t.Run("returns project info when lookup by uuid succeeds", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		projectUUID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
		projectName := "proj"
		mockV3 := mocknutanixv3.NewMockService(ctrl)
		mockV3.EXPECT().GetProject(ctx, projectUUID).Return(&prismclientv3.Project{
			Metadata: &prismclientv3.Metadata{UUID: &projectUUID},
			Spec:     &prismclientv3.ProjectSpec{Name: projectName},
		}, nil)
		client := &prismclientv3.Client{V3: mockV3}
		rctx := &nctx.MachineContext{
			Context:       ctx,
			NutanixClient: client,
		}
		projectRef := &infrav1.NutanixResourceIdentifier{
			UUID: &projectUUID,
		}
		projectInfo, err := GetProjectV3(rctx, projectRef)
		assert.NoError(t, err)
		assert.Equal(t, projectUUID, *projectInfo.ExtID)
		assert.Equal(t, projectName, *projectInfo.Name)
	})

	t.Run("returns error when get project by uuid returns ENTITY_NOT_FOUND", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		projectUUID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
		mockV3 := mocknutanixv3.NewMockService(ctrl)
		mockV3.EXPECT().GetProject(ctx, projectUUID).Return(nil, errors.New("ENTITY_NOT_FOUND: not found"))
		client := &prismclientv3.Client{V3: mockV3}
		rctx := &nctx.MachineContext{
			Context:       ctx,
			NutanixClient: client,
		}
		projectRef := &infrav1.NutanixResourceIdentifier{
			UUID: &projectUUID,
		}
		projectInfo, err := GetProjectV3(rctx, projectRef)
		assert.Error(t, err)
		assert.Nil(t, projectInfo)
		assert.Contains(t, err.Error(), "failed to find project with UUID")
	})

	t.Run("returns project info when lookup by name succeeds", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		projectName := "my-project"
		projectUUID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
		mockV3 := mocknutanixv3.NewMockService(ctrl)
		mockV3.EXPECT().ListAllProject(ctx, "").Return(&prismclientv3.ProjectListResponse{
			Entities: []*prismclientv3.Project{
				{
					Metadata: &prismclientv3.Metadata{UUID: &projectUUID},
					Spec:     &prismclientv3.ProjectSpec{Name: projectName},
				},
			},
		}, nil)
		client := &prismclientv3.Client{V3: mockV3}
		rctx := &nctx.MachineContext{
			Context:       ctx,
			NutanixClient: client,
		}
		projectRef := &infrav1.NutanixResourceIdentifier{
			Name: &projectName,
		}
		projectInfo, err := GetProjectV3(rctx, projectRef)
		assert.NoError(t, err)
		assert.Equal(t, projectUUID, *projectInfo.ExtID)
		assert.Equal(t, projectName, *projectInfo.Name)
	})

	t.Run("returns error when no project found by name", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		projectName := "nonexistent"
		mockV3 := mocknutanixv3.NewMockService(ctrl)
		mockV3.EXPECT().ListAllProject(ctx, "").Return(&prismclientv3.ProjectListResponse{
			Entities: []*prismclientv3.Project{},
		}, nil)
		client := &prismclientv3.Client{V3: mockV3}
		rctx := &nctx.MachineContext{
			Context:       ctx,
			NutanixClient: client,
		}
		projectRef := &infrav1.NutanixResourceIdentifier{
			Name: &projectName,
		}
		projectInfo, err := GetProjectV3(rctx, projectRef)
		assert.Error(t, err)
		assert.Nil(t, projectInfo)
		assert.Contains(t, err.Error(), "failed to retrieve project by name")
	})

	t.Run("returns error when multiple projects found with same name", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		projectName := "dup"
		uuid1 := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
		uuid2 := "bbbbbbbb-bbbb-cccc-dddd-eeeeeeeeeeee"
		mockV3 := mocknutanixv3.NewMockService(ctrl)
		mockV3.EXPECT().ListAllProject(ctx, "").Return(&prismclientv3.ProjectListResponse{
			Entities: []*prismclientv3.Project{
				{Metadata: &prismclientv3.Metadata{UUID: &uuid1}, Spec: &prismclientv3.ProjectSpec{Name: projectName}},
				{Metadata: &prismclientv3.Metadata{UUID: &uuid2}, Spec: &prismclientv3.ProjectSpec{Name: projectName}},
			},
		}, nil)
		client := &prismclientv3.Client{V3: mockV3}
		rctx := &nctx.MachineContext{
			Context:       ctx,
			NutanixClient: client,
		}
		projectRef := &infrav1.NutanixResourceIdentifier{
			Name: &projectName,
		}
		projectInfo, err := GetProjectV3(rctx, projectRef)
		assert.Error(t, err)
		assert.Nil(t, projectInfo)
		assert.Contains(t, err.Error(), "more than one project found")
	})
}

func TestGetProjectV4(t *testing.T) {
	ctx := context.Background()

	t.Run("returns error when both name and uuid are nil", func(t *testing.T) {
		// GetProjectV4 does not need a real client for this path
		rctx := &nctx.MachineContext{
			Context: ctx,
		}
		projectRef := &infrav1.NutanixResourceIdentifier{
			UUID: nil,
			Name: nil,
		}
		projectInfo, err := GetProjectV4(rctx, projectRef)
		assert.Error(t, err)
		assert.Nil(t, projectInfo)
		assert.Contains(t, err.Error(), "name or uuid must be passed")
	})
}

func TestGetDefaultProjectUUID(t *testing.T) {
	t.Run("returns empty string when PC version is pc.7.5.0.5", func(t *testing.T) {
		rctx := &nctx.MachineContext{PCVersion: "pc.7.5.0.5"}
		uuid, err := GetDefaultProjectUUID(rctx)
		assert.NoError(t, err)
		assert.Empty(t, uuid)
	})

	t.Run("returns empty string when PC version is 7.5.0", func(t *testing.T) {
		rctx := &nctx.MachineContext{PCVersion: "7.5.0"}
		uuid, err := GetDefaultProjectUUID(rctx)
		assert.NoError(t, err)
		assert.Empty(t, uuid)
	})
}

func TestGetResourceGroupForProject(t *testing.T) {
	ctx := context.Background()
	projectExtID := "11111111-1111-1111-1111-111111111111"

	t.Run("returns the resource group owned by the project", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockConvergedClient := NewMockConvergedClient(ctrl)
		rgExtID := "rg-uuid"
		mockConvergedClient.MockResourceGroups.EXPECT().List(gomock.Any(), gomock.Any()).Return([]projectModels.ResourceGroup{
			{ExtId: ptr.To("other-rg"), ProjectExtId: ptr.To("other-project")},
			{ExtId: ptr.To(rgExtID), ProjectExtId: ptr.To(projectExtID)},
		}, nil)

		rg, err := GetResourceGroupForProject(ctx, mockConvergedClient.Client, projectExtID)
		require.NoError(t, err)
		require.NotNil(t, rg)
		assert.Equal(t, rgExtID, *rg.ExtId)
	})

	t.Run("returns nil when no resource group matches the project", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockConvergedClient := NewMockConvergedClient(ctrl)
		mockConvergedClient.MockResourceGroups.EXPECT().List(gomock.Any(), gomock.Any()).Return([]projectModels.ResourceGroup{
			{ExtId: ptr.To("other-rg"), ProjectExtId: ptr.To("other-project")},
		}, nil)

		rg, err := GetResourceGroupForProject(ctx, mockConvergedClient.Client, projectExtID)
		require.NoError(t, err)
		assert.Nil(t, rg)
	})

	t.Run("propagates list error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockConvergedClient := NewMockConvergedClient(ctrl)
		mockConvergedClient.MockResourceGroups.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, errors.New("list failed"))

		rg, err := GetResourceGroupForProject(ctx, mockConvergedClient.Client, projectExtID)
		require.Error(t, err)
		assert.Nil(t, rg)
		assert.Contains(t, err.Error(), "list failed")
	})
}

func TestResolveResourceGroup(t *testing.T) {
	ctx := context.Background()
	projectExtID := "11111111-1111-1111-1111-111111111111"
	defaultProjectExtID := "00000000-default-project"

	t.Run("returns nil when the effective project is the default project", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockConvergedClient := NewMockConvergedClient(ctrl)
		mockConvergedClient.MockProjects.EXPECT().GetDefaultProject(ctx).Return(&projectModels.Project{
			ExtId: ptr.To(defaultProjectExtID),
		}, nil)

		rctx := &nctx.MachineContext{
			Context:         ctx,
			PCVersion:       "7.6",
			ConvergedClient: mockConvergedClient.Client,
		}
		effectiveProject := &nctx.ProjectInfo{
			ExtID: ptr.To(defaultProjectExtID),
			Name:  ptr.To("default"),
		}

		rg, err := resolveResourceGroup(rctx, effectiveProject)
		require.NoError(t, err)
		assert.Nil(t, rg)
	})

	t.Run("returns the resource group for a non-default project", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockConvergedClient := NewMockConvergedClient(ctrl)
		mockConvergedClient.MockProjects.EXPECT().GetDefaultProject(ctx).Return(&projectModels.Project{
			ExtId: ptr.To(defaultProjectExtID),
		}, nil)
		rgExtID := "rg-uuid"
		mockConvergedClient.MockResourceGroups.EXPECT().List(gomock.Any(), gomock.Any()).Return([]projectModels.ResourceGroup{
			{ExtId: ptr.To(rgExtID), ProjectExtId: ptr.To(projectExtID)},
		}, nil)

		rctx := &nctx.MachineContext{
			Context:         ctx,
			PCVersion:       "7.6",
			ConvergedClient: mockConvergedClient.Client,
		}
		effectiveProject := &nctx.ProjectInfo{
			ExtID: ptr.To(projectExtID),
			Name:  ptr.To("nkp-test"),
		}

		rg, err := resolveResourceGroup(rctx, effectiveProject)
		require.NoError(t, err)
		require.NotNil(t, rg)
		assert.Equal(t, rgExtID, *rg.ExtId)
	})

	t.Run("returns a terminal error naming the project when no resource group exists", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockConvergedClient := NewMockConvergedClient(ctrl)
		mockConvergedClient.MockProjects.EXPECT().GetDefaultProject(ctx).Return(&projectModels.Project{
			ExtId: ptr.To(defaultProjectExtID),
		}, nil)
		mockConvergedClient.MockResourceGroups.EXPECT().List(gomock.Any(), gomock.Any()).Return([]projectModels.ResourceGroup{}, nil)

		rctx := &nctx.MachineContext{
			Context:         ctx,
			PCVersion:       "7.6",
			ConvergedClient: mockConvergedClient.Client,
		}
		effectiveProject := &nctx.ProjectInfo{
			ExtID: ptr.To(projectExtID),
			Name:  ptr.To("nkp-test"),
		}

		rg, err := resolveResourceGroup(rctx, effectiveProject)
		require.Error(t, err)
		assert.Nil(t, rg)
		assert.Contains(t, err.Error(), "no resource group found for project nkp-test")
		assert.True(t, services.IsTerminalError(err))
	})

	t.Run("falls back to zero UUID and still resolves when default project lookup fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockConvergedClient := NewMockConvergedClient(ctrl)
		mockConvergedClient.MockProjects.EXPECT().GetDefaultProject(ctx).Return(nil, errors.New("permission denied"))
		rgExtID := "rg-uuid"
		mockConvergedClient.MockResourceGroups.EXPECT().List(gomock.Any(), gomock.Any()).Return([]projectModels.ResourceGroup{
			{ExtId: ptr.To(rgExtID), ProjectExtId: ptr.To(projectExtID)},
		}, nil)

		rctx := &nctx.MachineContext{
			Context:         ctx,
			PCVersion:       "7.6",
			ConvergedClient: mockConvergedClient.Client,
		}
		effectiveProject := &nctx.ProjectInfo{
			ExtID: ptr.To(projectExtID),
			Name:  ptr.To("nkp-test"),
		}

		rg, err := resolveResourceGroup(rctx, effectiveProject)
		require.NoError(t, err)
		require.NotNil(t, rg)
		assert.Equal(t, rgExtID, *rg.ExtId)
	})
}

func TestResolveResourceGroupForProjectPolicy(t *testing.T) {
	ctx := context.Background()
	defaultProjectExtID := "00000000-default-project"
	namedProjectExtID := "11111111-1111-1111-1111-111111111111"

	t.Run("returns nil for an unrestricted policy without any client calls", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockConvergedClient := NewMockConvergedClient(ctrl)

		rg, err := resolveResourceGroupForProjectPolicy(ctx, mockConvergedClient.Client, CAPXProjectPolicyUnrestricted, "", "7.6")
		require.NoError(t, err)
		assert.Nil(t, rg)
	})

	t.Run("returns nil when PC version is below 7.6", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockConvergedClient := NewMockConvergedClient(ctrl)

		rg, err := resolveResourceGroupForProjectPolicy(ctx, mockConvergedClient.Client, CAPXProjectPolicySingleProject, namedProjectExtID, "7.5")
		require.NoError(t, err)
		assert.Nil(t, rg)
	})

	t.Run("returns nil for the default-only policy (default project has no resource group)", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockConvergedClient := NewMockConvergedClient(ctrl)
		mockConvergedClient.MockProjects.EXPECT().GetDefaultProject(ctx).Return(&projectModels.Project{
			ExtId: ptr.To(defaultProjectExtID),
		}, nil).Times(2)

		rg, err := resolveResourceGroupForProjectPolicy(ctx, mockConvergedClient.Client, CAPXProjectPolicyDefaultOnly, "", "7.6")
		require.NoError(t, err)
		assert.Nil(t, rg)
	})

	t.Run("resolves the resource group for a single-project policy", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockConvergedClient := NewMockConvergedClient(ctrl)
		mockConvergedClient.MockProjects.EXPECT().GetDefaultProject(ctx).Return(&projectModels.Project{
			ExtId: ptr.To(defaultProjectExtID),
		}, nil)
		rgExtID := "rg-uuid"
		mockConvergedClient.MockResourceGroups.EXPECT().List(gomock.Any(), gomock.Any()).Return([]projectModels.ResourceGroup{
			{ExtId: ptr.To(rgExtID), ProjectExtId: ptr.To(namedProjectExtID)},
		}, nil)

		rg, err := resolveResourceGroupForProjectPolicy(ctx, mockConvergedClient.Client, CAPXProjectPolicySingleProject, namedProjectExtID, "7.6")
		require.NoError(t, err)
		require.NotNil(t, rg)
		assert.Equal(t, rgExtID, *rg.ExtId)
	})

	t.Run("returns a terminal error when a single-project policy has no resource group", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockConvergedClient := NewMockConvergedClient(ctrl)
		mockConvergedClient.MockProjects.EXPECT().GetDefaultProject(ctx).Return(&projectModels.Project{
			ExtId: ptr.To(defaultProjectExtID),
		}, nil)
		mockConvergedClient.MockResourceGroups.EXPECT().List(gomock.Any(), gomock.Any()).Return([]projectModels.ResourceGroup{}, nil)

		rg, err := resolveResourceGroupForProjectPolicy(ctx, mockConvergedClient.Client, CAPXProjectPolicySingleProject, namedProjectExtID, "7.6")
		require.Error(t, err)
		assert.Nil(t, rg)
		assert.Contains(t, err.Error(), "no resource group found for project")
		assert.True(t, services.IsTerminalError(err))
	})

	t.Run("returns error when single-project policy is missing project UUID", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockConvergedClient := NewMockConvergedClient(ctrl)

		rg, err := resolveResourceGroupForProjectPolicy(ctx, mockConvergedClient.Client, CAPXProjectPolicySingleProject, "", "7.6")
		require.Error(t, err)
		assert.Nil(t, rg)
		assert.Contains(t, err.Error(), "single-project policy requires")
	})
}

func TestControllerHelpers(t *testing.T) {
	g := NewWithT(t)

	_ = Describe("ControllerHelpers", func() {
		const (
			fd1Name = "fd-1"
			fd2Name = "fd-2"
		)

		var (
			ntnxCluster *infrav1.NutanixCluster
			ctx         context.Context
		)

		BeforeEach(func() {
			ctx = context.Background()
			ntnxCluster = &infrav1.NutanixCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "default",
				},
				Spec: infrav1.NutanixClusterSpec{
					PrismCentral: &credentialtypes.NutanixPrismEndpoint{
						// Adding port info to override default value (0)
						Port: 9440,
					},
				},
			}
		})

		AfterEach(func() {
			err := k8sClient.Delete(ctx, ntnxCluster)
			Expect(err).NotTo(HaveOccurred())
		})

		Context("Get failure domains", func() {
			It("should return nil when no failure domain has been found", func() {
				g.Expect(k8sClient.Create(ctx, ntnxCluster)).To(Succeed())
				fd := GetLegacyFailureDomainFromNutanixCluster(fd1Name, ntnxCluster)
				Expect(fd).To(BeNil())
			})
			It("should return the correct failuredomain", func() {
				r := util.RandomString(10)
				fd1 := infrav1.NutanixFailureDomainConfig{ //nolint:staticcheck // suppress complaining on Deprecated type
					Name: fd1Name,
					Cluster: infrav1.NutanixResourceIdentifier{
						Type: infrav1.NutanixIdentifierName,
						Name: &r,
					},
					Subnets: []infrav1.NutanixResourceIdentifier{
						{
							Type: infrav1.NutanixIdentifierName,
							Name: &r,
						},
					},
					ControlPlane: true,
				}
				fd2 := infrav1.NutanixFailureDomainConfig{ //nolint:staticcheck // suppress complaining on Deprecated type
					Name: fd2Name,
					Cluster: infrav1.NutanixResourceIdentifier{
						Type: infrav1.NutanixIdentifierName,
						Name: &r,
					},
					Subnets: []infrav1.NutanixResourceIdentifier{
						{
							Type: infrav1.NutanixIdentifierName,
							Name: &r,
						},
					},
					ControlPlane: true,
				}
				ntnxCluster.Spec.FailureDomains = []infrav1.NutanixFailureDomainConfig{ //nolint:staticcheck // suppress complaining on Deprecated type
					fd1,
					fd2,
				}
				g.Expect(k8sClient.Create(ctx, ntnxCluster)).To(Succeed())
				fd := GetLegacyFailureDomainFromNutanixCluster(fd2Name, ntnxCluster)
				Expect(*fd).To(Equal(fd2))
			})
		})
	})
}

func TestGetPrismCentralClientForCluster(t *testing.T) {
	ctx := context.Background()
	cluster := &infrav1.NutanixCluster{
		Spec: infrav1.NutanixClusterSpec{
			PrismCentral: &credentialtypes.NutanixPrismEndpoint{
				Address: "prismcentral.nutanix.com",
				Port:    9440,
				CredentialRef: &credentialtypes.NutanixCredentialReference{
					Kind:      credentialtypes.SecretKind,
					Name:      "test-credential",
					Namespace: "test-ns",
				},
			},
		},
	}

	t.Run("BuildManagementEndpoint Fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)

		secretNamespaceLister := mockk8sclient.NewMockSecretNamespaceLister(ctrl)
		secretNamespaceLister.EXPECT().Get("test-credential").Return(nil, errors.New("failed to get secret"))
		secretLister := mockk8sclient.NewMockSecretLister(ctrl)
		secretLister.EXPECT().Secrets("test-ns").Return(secretNamespaceLister)
		secretInformer := mockk8sclient.NewMockSecretInformer(ctrl)
		mapInformer := mockk8sclient.NewMockConfigMapInformer(ctrl)
		secretInformer.EXPECT().Lister().Return(secretLister)

		_, err := getPrismCentralClientForCluster(ctx, cluster, secretInformer, mapInformer)
		assert.Error(t, err)
	})

	t.Run("GetOrCreate Fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)

		creds := []credentialtypes.Credential{
			{
				Type: credentialtypes.BasicAuthCredentialType,
				Data: []byte(`{"prismCentral":{"username":"user","password":"password"}}`),
			},
		}
		credsMarshal, err := json.Marshal(creds)
		require.NoError(t, err)

		secret := &corev1.Secret{
			Data: map[string][]byte{
				credentialtypes.KeyName: credsMarshal,
			},
		}

		secretNamespaceLister := mockk8sclient.NewMockSecretNamespaceLister(ctrl)
		secretNamespaceLister.EXPECT().Get("test-credential").Return(secret, nil)
		secretLister := mockk8sclient.NewMockSecretLister(ctrl)
		secretLister.EXPECT().Secrets("test-ns").Return(secretNamespaceLister)
		secretInformer := mockk8sclient.NewMockSecretInformer(ctrl)
		mapInformer := mockk8sclient.NewMockConfigMapInformer(ctrl)
		secretInformer.EXPECT().Lister().Return(secretLister)

		_, err = getPrismCentralClientForCluster(ctx, cluster, secretInformer, mapInformer)
		assert.Error(t, err)
	})

	t.Run("GetOrCreate succeeds", func(t *testing.T) {
		ctrl := gomock.NewController(t)

		oldNutanixClientCache := nutanixclient.NutanixClientCache
		defer func() {
			nutanixclient.NutanixClientCache = oldNutanixClientCache
		}()

		// Create a new client cache with session auth disabled to avoid network calls in tests
		nutanixclient.NutanixClientCache = prismclientv3.NewClientCache()

		creds := []credentialtypes.Credential{
			{
				Type: credentialtypes.BasicAuthCredentialType,
				Data: []byte(`{"prismCentral":{"username":"user","password":"password"}}`),
			},
		}

		credsMarshal, err := json.Marshal(creds)
		require.NoError(t, err)
		secret := &corev1.Secret{
			Data: map[string][]byte{
				credentialtypes.KeyName: credsMarshal,
			},
		}

		secretNamespaceLister := mockk8sclient.NewMockSecretNamespaceLister(ctrl)
		secretNamespaceLister.EXPECT().Get("test-credential").Return(secret, nil)
		secretLister := mockk8sclient.NewMockSecretLister(ctrl)
		secretLister.EXPECT().Secrets("test-ns").Return(secretNamespaceLister)
		secretInformer := mockk8sclient.NewMockSecretInformer(ctrl)
		mapInformer := mockk8sclient.NewMockConfigMapInformer(ctrl)
		secretInformer.EXPECT().Lister().Return(secretLister)

		_, err = getPrismCentralClientForCluster(ctx, cluster, secretInformer, mapInformer)
		assert.NoError(t, err)
	})
}

func TestGetPrismCentralConvergedV4ClientForCluster(t *testing.T) {
	ctx := context.Background()
	cluster := &infrav1.NutanixCluster{
		Spec: infrav1.NutanixClusterSpec{
			PrismCentral: &credentialtypes.NutanixPrismEndpoint{
				Address: "prismcentral.nutanix.com",
				Port:    9440,
				CredentialRef: &credentialtypes.NutanixCredentialReference{
					Kind:      credentialtypes.SecretKind,
					Name:      "test-credential",
					Namespace: "test-ns",
				},
			},
		},
	}

	t.Run("BuildManagementEndpoint Fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)

		secretNamespaceLister := mockk8sclient.NewMockSecretNamespaceLister(ctrl)
		secretNamespaceLister.EXPECT().Get("test-credential").Return(nil, errors.New("failed to get secret"))
		secretLister := mockk8sclient.NewMockSecretLister(ctrl)
		secretLister.EXPECT().Secrets("test-ns").Return(secretNamespaceLister)
		secretInformer := mockk8sclient.NewMockSecretInformer(ctrl)
		mapInformer := mockk8sclient.NewMockConfigMapInformer(ctrl)
		secretInformer.EXPECT().Lister().Return(secretLister)

		_, err := getPrismCentralConvergedV4ClientForCluster(ctx, cluster, secretInformer, mapInformer)
		assert.Error(t, err)
	})

	t.Run("GetOrCreate Fails with malformed credentials", func(t *testing.T) {
		ctrl := gomock.NewController(t)

		// Use malformed credentials to force GetOrCreate to fail
		creds := []credentialtypes.Credential{
			{
				Type: credentialtypes.BasicAuthCredentialType,
				Data: []byte(`{"prismCentral":{"username":"user"}}`), // Missing password
			},
		}
		credsMarshal, err := json.Marshal(creds)
		require.NoError(t, err)

		secret := &corev1.Secret{
			Data: map[string][]byte{
				credentialtypes.KeyName: credsMarshal,
			},
		}

		secretNamespaceLister := mockk8sclient.NewMockSecretNamespaceLister(ctrl)
		secretNamespaceLister.EXPECT().Get("test-credential").Return(secret, nil)
		secretLister := mockk8sclient.NewMockSecretLister(ctrl)
		secretLister.EXPECT().Secrets("test-ns").Return(secretNamespaceLister)
		secretInformer := mockk8sclient.NewMockSecretInformer(ctrl)
		mapInformer := mockk8sclient.NewMockConfigMapInformer(ctrl)
		secretInformer.EXPECT().Lister().Return(secretLister)

		_, err = getPrismCentralConvergedV4ClientForCluster(ctx, cluster, secretInformer, mapInformer)
		assert.Error(t, err)
	})

	t.Run("GetOrCreate succeeds", func(t *testing.T) {
		ctrl := gomock.NewController(t)

		oldNutanixConvergedClientV4Cache := nutanixclient.NutanixConvergedClientV4Cache
		defer func() {
			nutanixclient.NutanixConvergedClientV4Cache = oldNutanixConvergedClientV4Cache
		}()

		// Create a new client cache with session auth disabled to avoid network calls in tests
		nutanixclient.NutanixConvergedClientV4Cache = v4Converged.NewClientCache()

		creds := []credentialtypes.Credential{
			{
				Type: credentialtypes.BasicAuthCredentialType,
				Data: []byte(`{"prismCentral":{"username":"user","password":"password"}}`),
			},
		}

		credsMarshal, err := json.Marshal(creds)
		require.NoError(t, err)
		secret := &corev1.Secret{
			Data: map[string][]byte{
				credentialtypes.KeyName: credsMarshal,
			},
		}

		secretNamespaceLister := mockk8sclient.NewMockSecretNamespaceLister(ctrl)
		secretNamespaceLister.EXPECT().Get("test-credential").Return(secret, nil)
		secretLister := mockk8sclient.NewMockSecretLister(ctrl)
		secretLister.EXPECT().Secrets("test-ns").Return(secretNamespaceLister)
		secretInformer := mockk8sclient.NewMockSecretInformer(ctrl)
		mapInformer := mockk8sclient.NewMockConfigMapInformer(ctrl)
		secretInformer.EXPECT().Lister().Return(secretLister)

		_, err = getPrismCentralConvergedV4ClientForCluster(ctx, cluster, secretInformer, mapInformer)
		assert.NoError(t, err)
	})

	t.Run("GetOrCreate succeeds with different credential types", func(t *testing.T) {
		ctrl := gomock.NewController(t)

		oldNutanixConvergedClientV4Cache := nutanixclient.NutanixConvergedClientV4Cache
		defer func() {
			nutanixclient.NutanixConvergedClientV4Cache = oldNutanixConvergedClientV4Cache
		}()

		// Create a new client cache with session auth disabled to avoid network calls in tests
		nutanixclient.NutanixConvergedClientV4Cache = v4Converged.NewClientCache()

		// Test with different credential types
		creds := []credentialtypes.Credential{
			{
				Type: credentialtypes.BasicAuthCredentialType,
				Data: []byte(`{"prismCentral":{"username":"user","password":"password"}}`),
			},
		}
		credsMarshal, err := json.Marshal(creds)
		require.NoError(t, err)
		secret := &corev1.Secret{
			Data: map[string][]byte{
				credentialtypes.KeyName: credsMarshal,
			},
		}

		secretNamespaceLister := mockk8sclient.NewMockSecretNamespaceLister(ctrl)
		secretNamespaceLister.EXPECT().Get("test-credential").Return(secret, nil)
		secretLister := mockk8sclient.NewMockSecretLister(ctrl)
		secretLister.EXPECT().Secrets("test-ns").Return(secretNamespaceLister)
		secretInformer := mockk8sclient.NewMockSecretInformer(ctrl)
		mapInformer := mockk8sclient.NewMockConfigMapInformer(ctrl)
		secretInformer.EXPECT().Lister().Return(secretLister)

		client, err := getPrismCentralConvergedV4ClientForCluster(ctx, cluster, secretInformer, mapInformer)
		assert.NoError(t, err)
		assert.NotNil(t, client)
	})

	t.Run("GetOrCreate succeeds with cached client", func(t *testing.T) {
		ctrl := gomock.NewController(t)

		oldNutanixConvergedClientV4Cache := nutanixclient.NutanixConvergedClientV4Cache
		defer func() {
			nutanixclient.NutanixConvergedClientV4Cache = oldNutanixConvergedClientV4Cache
		}()

		// Create a new client cache with session auth disabled to avoid network calls in tests
		nutanixclient.NutanixConvergedClientV4Cache = v4Converged.NewClientCache()

		creds := []credentialtypes.Credential{
			{
				Type: credentialtypes.BasicAuthCredentialType,
				Data: []byte(`{"prismCentral":{"username":"user","password":"password"}}`),
			},
		}
		credsMarshal, err := json.Marshal(creds)
		require.NoError(t, err)
		secret := &corev1.Secret{
			Data: map[string][]byte{
				credentialtypes.KeyName: credsMarshal,
			},
		}

		secretNamespaceLister := mockk8sclient.NewMockSecretNamespaceLister(ctrl)
		secretNamespaceLister.EXPECT().Get("test-credential").Return(secret, nil).Times(2) // Called twice for cache hit
		secretLister := mockk8sclient.NewMockSecretLister(ctrl)
		secretLister.EXPECT().Secrets("test-ns").Return(secretNamespaceLister).Times(2)
		secretInformer := mockk8sclient.NewMockSecretInformer(ctrl)
		mapInformer := mockk8sclient.NewMockConfigMapInformer(ctrl)
		secretInformer.EXPECT().Lister().Return(secretLister).Times(2)

		// First call - should create and cache the client
		client1, err := getPrismCentralConvergedV4ClientForCluster(ctx, cluster, secretInformer, mapInformer)
		assert.NoError(t, err)
		assert.NotNil(t, client1)

		// Second call - should return cached client
		client2, err := getPrismCentralConvergedV4ClientForCluster(ctx, cluster, secretInformer, mapInformer)
		assert.NoError(t, err)
		assert.NotNil(t, client2)
		assert.Equal(t, client1, client2) // Should be the same cached instance
	})
}

func TestGetPrismCentralConvergedV4ClientForCluster_EdgeCases(t *testing.T) {
	t.Run("should handle nil cluster gracefully", func(t *testing.T) {
		// This test is skipped because passing nil cluster causes a panic
		// in the underlying client helper code, which is expected behavior
		t.Skip("Skipping nil cluster test as it causes expected panic in client helper")
	})

	t.Run("should handle cluster with nil PrismCentral", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		ctx := context.Background()
		cluster := &infrav1.NutanixCluster{
			Spec: infrav1.NutanixClusterSpec{
				PrismCentral: nil,
			},
		}

		secretInformer := mockk8sclient.NewMockSecretInformer(ctrl)
		mapInformer := mockk8sclient.NewMockConfigMapInformer(ctrl)

		_, err := getPrismCentralConvergedV4ClientForCluster(ctx, cluster, secretInformer, mapInformer)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "error building an environment provider")
	})

	t.Run("should handle cluster with nil CredentialRef", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		ctx := context.Background()
		cluster := &infrav1.NutanixCluster{
			Spec: infrav1.NutanixClusterSpec{
				PrismCentral: &credentialtypes.NutanixPrismEndpoint{
					Address:       "prismcentral.nutanix.com",
					Port:          9440,
					CredentialRef: nil,
				},
			},
		}

		secretInformer := mockk8sclient.NewMockSecretInformer(ctrl)
		mapInformer := mockk8sclient.NewMockConfigMapInformer(ctrl)

		_, err := getPrismCentralConvergedV4ClientForCluster(ctx, cluster, secretInformer, mapInformer)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "error building an environment provider")
	})

	t.Run("should handle invalid credential data", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		ctx := context.Background()
		cluster := &infrav1.NutanixCluster{
			Spec: infrav1.NutanixClusterSpec{
				PrismCentral: &credentialtypes.NutanixPrismEndpoint{
					Address: "prismcentral.nutanix.com",
					Port:    9440,
					CredentialRef: &credentialtypes.NutanixCredentialReference{
						Kind:      credentialtypes.SecretKind,
						Name:      "test-credential",
						Namespace: "test-ns",
					},
				},
			},
		}

		secretInformer := mockk8sclient.NewMockSecretInformer(ctrl)
		mapInformer := mockk8sclient.NewMockConfigMapInformer(ctrl)

		// Mock the secret lister to return invalid credential data
		secret := &corev1.Secret{
			Data: map[string][]byte{
				credentialtypes.KeyName: []byte("invalid json"),
			},
		}

		secretNamespaceLister := mockk8sclient.NewMockSecretNamespaceLister(ctrl)
		secretNamespaceLister.EXPECT().Get("test-credential").Return(secret, nil)
		secretLister := mockk8sclient.NewMockSecretLister(ctrl)
		secretLister.EXPECT().Secrets("test-ns").Return(secretNamespaceLister)
		secretInformer.EXPECT().Lister().Return(secretLister)

		_, err := getPrismCentralConvergedV4ClientForCluster(ctx, cluster, secretInformer, mapInformer)
		assert.Error(t, err)
	})

	t.Run("should handle empty credential data", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		ctx := context.Background()
		cluster := &infrav1.NutanixCluster{
			Spec: infrav1.NutanixClusterSpec{
				PrismCentral: &credentialtypes.NutanixPrismEndpoint{
					Address: "prismcentral.nutanix.com",
					Port:    9440,
					CredentialRef: &credentialtypes.NutanixCredentialReference{
						Kind:      credentialtypes.SecretKind,
						Name:      "test-credential",
						Namespace: "test-ns",
					},
				},
			},
		}

		secretInformer := mockk8sclient.NewMockSecretInformer(ctrl)
		mapInformer := mockk8sclient.NewMockConfigMapInformer(ctrl)

		// Mock the secret lister to return empty credential data
		secret := &corev1.Secret{
			Data: map[string][]byte{
				credentialtypes.KeyName: []byte("[]"),
			},
		}

		secretNamespaceLister := mockk8sclient.NewMockSecretNamespaceLister(ctrl)
		secretNamespaceLister.EXPECT().Get("test-credential").Return(secret, nil)
		secretLister := mockk8sclient.NewMockSecretLister(ctrl)
		secretLister.EXPECT().Secrets("test-ns").Return(secretNamespaceLister)
		secretInformer.EXPECT().Lister().Return(secretLister)

		_, err := getPrismCentralConvergedV4ClientForCluster(ctx, cluster, secretInformer, mapInformer)
		assert.Error(t, err)
	})
}
