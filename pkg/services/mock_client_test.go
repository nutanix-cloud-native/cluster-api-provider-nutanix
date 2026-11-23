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

package services

import (
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

	mockconverged "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/mocks/converged"
	converged "github.com/nutanix-cloud-native/prism-go-client/converged"
	v4Converged "github.com/nutanix-cloud-native/prism-go-client/converged/v4"

	"go.uber.org/mock/gomock"
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
