/*
Copyright 2026 Nutanix

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
	"testing"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	mocknutanixv3 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/mocks/nutanix"
	nctx "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/pkg/context"
	prismclientv3 "github.com/nutanix-cloud-native/prism-go-client/v3"
	"github.com/nutanix-cloud-native/prism-go-client/v3/models"
	clustermgmtconfig "github.com/nutanix/ntnx-api-golang-clients/clustermgmt-go-client/v4/models/clustermgmt/v4/config"
	prismModels "github.com/nutanix/ntnx-api-golang-clients/prism-go-client/v4/models/prism/v4/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	capiv1beta2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestVhaCategoryKey(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple name",
			input:    "my-vha",
			expected: "capx-vha-my-vha",
		},
		{
			name:     "empty name",
			input:    "",
			expected: "capx-vha-",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, vhaCategoryKey(tt.input))
		})
	}
}

func TestVhaProtectionPolicyName(t *testing.T) {
	assert.Equal(t, "capx-vha-test-domain", vhaProtectionPolicyName("test-domain"))
	assert.Equal(t, "capx-vha-", vhaProtectionPolicyName(""))
}

func TestVhaRecoveryPlanName(t *testing.T) {
	tests := []struct {
		name     string
		domain   string
		index    int
		expected string
	}{
		{
			name:     "index 0",
			domain:   "my-vha",
			index:    0,
			expected: "capx-vha-my-vha-rp-0",
		},
		{
			name:     "index 1",
			domain:   "my-vha",
			index:    1,
			expected: "capx-vha-my-vha-rp-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, vhaRecoveryPlanName(tt.domain, tt.index))
		})
	}
}

func TestFindProtectionRuleByName(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	mockV3Service := mocknutanixv3.NewMockService(mockCtrl)
	v3Client := &prismclientv3.Client{V3: mockV3Service}
	ctx := context.Background()

	rctx := &nctx.VHADomainContext{
		Context:       ctx,
		NutanixClient: v3Client,
		VHADomain: &infrav1.NutanixVirtualHADomain{
			ObjectMeta: metav1.ObjectMeta{Name: "test-vha", Namespace: "default"},
		},
	}

	t.Run("returns existing protection rule when found", func(t *testing.T) {
		ppName := "capx-vha-test-vha"
		expectedUUID := "pp-uuid-1234"
		mockV3Service.EXPECT().ListAllProtectionRules(gomock.Any(), gomock.Any()).Return(
			&prismclientv3.ProtectionRulesListResponse{
				Entities: []*prismclientv3.ProtectionRuleResponse{
					{
						Metadata: &prismclientv3.Metadata{UUID: ptr.To(expectedUUID)},
						Spec:     &prismclientv3.ProtectionRuleSpec{Name: ppName},
					},
				},
			}, nil)

		result, err := findProtectionRuleByName(rctx, ppName)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, expectedUUID, *result.Metadata.UUID)
	})

	t.Run("returns nil when no matching rule found", func(t *testing.T) {
		mockV3Service.EXPECT().ListAllProtectionRules(gomock.Any(), gomock.Any()).Return(
			&prismclientv3.ProtectionRulesListResponse{
				Entities: []*prismclientv3.ProtectionRuleResponse{
					{
						Metadata: &prismclientv3.Metadata{UUID: ptr.To("other-uuid")},
						Spec:     &prismclientv3.ProtectionRuleSpec{Name: "other-rule"},
					},
				},
			}, nil)

		result, err := findProtectionRuleByName(rctx, "capx-vha-nonexistent")
		require.NoError(t, err)
		assert.Nil(t, result)
	})

	t.Run("returns nil when list is empty", func(t *testing.T) {
		mockV3Service.EXPECT().ListAllProtectionRules(gomock.Any(), gomock.Any()).Return(
			&prismclientv3.ProtectionRulesListResponse{
				Entities: []*prismclientv3.ProtectionRuleResponse{},
			}, nil)

		result, err := findProtectionRuleByName(rctx, "capx-vha-test-vha")
		require.NoError(t, err)
		assert.Nil(t, result)
	})

	t.Run("returns error when API fails", func(t *testing.T) {
		mockV3Service.EXPECT().ListAllProtectionRules(gomock.Any(), gomock.Any()).Return(
			nil, errors.New("API error"))

		result, err := findProtectionRuleByName(rctx, "capx-vha-test-vha")
		require.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "failed to list protection rules")
	})
}

func TestFindRecoveryPlanByName(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	mockV3Service := mocknutanixv3.NewMockService(mockCtrl)
	v3Client := &prismclientv3.Client{V3: mockV3Service}
	ctx := context.Background()

	rctx := &nctx.VHADomainContext{
		Context:       ctx,
		NutanixClient: v3Client,
		VHADomain: &infrav1.NutanixVirtualHADomain{
			ObjectMeta: metav1.ObjectMeta{Name: "test-vha", Namespace: "default"},
		},
	}

	t.Run("returns existing recovery plan when found", func(t *testing.T) {
		rpName := "capx-vha-test-vha-rp-0"
		expectedUUID := "rp-uuid-5678"
		mockV3Service.EXPECT().ListAllRecoveryPlans(gomock.Any(), gomock.Any()).Return(
			&models.RecoveryPlanListIntentResponse{
				Entities: []*models.RecoveryPlanIntentResource{
					{
						Metadata: &models.RecoveryPlanMetadata{UUID: expectedUUID},
						Spec:     &models.RecoveryPlan{Name: ptr.To(rpName)},
					},
				},
			}, nil)

		result, err := findRecoveryPlanByName(rctx, rpName)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, expectedUUID, result.Metadata.UUID)
	})

	t.Run("returns nil when no matching plan found", func(t *testing.T) {
		mockV3Service.EXPECT().ListAllRecoveryPlans(gomock.Any(), gomock.Any()).Return(
			&models.RecoveryPlanListIntentResponse{
				Entities: []*models.RecoveryPlanIntentResource{
					{
						Metadata: &models.RecoveryPlanMetadata{UUID: "other-uuid"},
						Spec:     &models.RecoveryPlan{Name: ptr.To("other-plan")},
					},
				},
			}, nil)

		result, err := findRecoveryPlanByName(rctx, "capx-vha-nonexistent")
		require.NoError(t, err)
		assert.Nil(t, result)
	})

	t.Run("returns error when API fails", func(t *testing.T) {
		mockV3Service.EXPECT().ListAllRecoveryPlans(gomock.Any(), gomock.Any()).Return(
			nil, errors.New("API error"))

		result, err := findRecoveryPlanByName(rctx, "capx-vha-test-vha-rp-0")
		require.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "failed to list recovery plans")
	})
}

func TestDeleteVHADomainPCResources(t *testing.T) {
	const (
		rpUUID0 = "rp-uuid-0"
		rpUUID1 = "rp-uuid-1"
		ppUUID  = "pp-uuid-1234"
	)

	newVHADomain := func() *infrav1.NutanixVirtualHADomain {
		return &infrav1.NutanixVirtualHADomain{
			ObjectMeta: metav1.ObjectMeta{Name: "test-vha", Namespace: "default"},
			Spec: infrav1.NutanixVirtualHADomainSpec{
				MetroRef: corev1.LocalObjectReference{Name: "test-metro"},
				ProtectionPolicy: &infrav1.NutanixResourceIdentifier{
					Type: infrav1.NutanixIdentifierUUID,
					UUID: ptr.To(ppUUID),
				},
				Categories: []infrav1.NutanixCategoryIdentifier{
					{Key: "capx-vha-test-vha", Value: "fd-1"},
					{Key: "capx-vha-test-vha", Value: "fd-2"},
				},
				RecoveryPlans: []infrav1.NutanixResourceIdentifier{
					{Type: infrav1.NutanixIdentifierUUID, UUID: ptr.To(rpUUID0)},
					{Type: infrav1.NutanixIdentifierUUID, UUID: ptr.To(rpUUID1)},
				},
			},
		}
	}

	t.Run("deletes all resources in order", func(t *testing.T) {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		mockV3Service := mocknutanixv3.NewMockService(mockCtrl)
		mockConverged := NewMockConvergedClient(mockCtrl)
		ctx := context.Background()

		gomock.InOrder(
			mockV3Service.EXPECT().DeleteRecoveryPlan(gomock.Any(), rpUUID0).Return(&prismclientv3.DeleteResponse{}, nil),
			mockV3Service.EXPECT().DeleteRecoveryPlan(gomock.Any(), rpUUID1).Return(&prismclientv3.DeleteResponse{}, nil),
			mockV3Service.EXPECT().DeleteProtectionRule(gomock.Any(), ppUUID).Return(&prismclientv3.DeleteResponse{}, nil),
		)
		mockConverged.MockCategories.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			[]prismModels.Category{
				{ExtId: ptr.To("cat-ext-id-1")},
				{ExtId: ptr.To("cat-ext-id-2")},
			}, nil,
		).Times(2)
		mockConverged.MockCategories.EXPECT().Delete(gomock.Any(), gomock.Any()).Return(nil).Times(2)

		rctx := &nctx.VHADomainContext{
			Context:         ctx,
			NutanixClient:   &prismclientv3.Client{V3: mockV3Service},
			ConvergedClient: mockConverged.Client,
			VHADomain:       newVHADomain(),
		}

		reconciler := &NutanixVirtualHADomainReconciler{}
		err := reconciler.deleteVHADomainPCResources(rctx)
		require.NoError(t, err)
	})

	t.Run("handles ENTITY_NOT_FOUND for recovery plans", func(t *testing.T) {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		mockV3Service := mocknutanixv3.NewMockService(mockCtrl)
		mockConverged := NewMockConvergedClient(mockCtrl)
		ctx := context.Background()

		mockV3Service.EXPECT().DeleteRecoveryPlan(gomock.Any(), rpUUID0).Return(
			nil, errors.New("ENTITY_NOT_FOUND: recovery plan not found"))
		mockV3Service.EXPECT().DeleteRecoveryPlan(gomock.Any(), rpUUID1).Return(
			nil, errors.New("ENTITY_NOT_FOUND: recovery plan not found"))
		mockV3Service.EXPECT().DeleteProtectionRule(gomock.Any(), ppUUID).Return(&prismclientv3.DeleteResponse{}, nil)
		mockConverged.MockCategories.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			[]prismModels.Category{{ExtId: ptr.To("cat-ext-id-1")}}, nil,
		).Times(2)
		mockConverged.MockCategories.EXPECT().Delete(gomock.Any(), gomock.Any()).Return(nil).Times(2)

		rctx := &nctx.VHADomainContext{
			Context:         ctx,
			NutanixClient:   &prismclientv3.Client{V3: mockV3Service},
			ConvergedClient: mockConverged.Client,
			VHADomain:       newVHADomain(),
		}

		reconciler := &NutanixVirtualHADomainReconciler{}
		err := reconciler.deleteVHADomainPCResources(rctx)
		require.NoError(t, err)
	})

	t.Run("returns error on recovery plan delete failure", func(t *testing.T) {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		mockV3Service := mocknutanixv3.NewMockService(mockCtrl)
		ctx := context.Background()

		mockV3Service.EXPECT().DeleteRecoveryPlan(gomock.Any(), rpUUID0).Return(
			nil, errors.New("permission denied"))

		rctx := &nctx.VHADomainContext{
			Context:       ctx,
			NutanixClient: &prismclientv3.Client{V3: mockV3Service},
			VHADomain:     newVHADomain(),
		}

		reconciler := &NutanixVirtualHADomainReconciler{}
		err := reconciler.deleteVHADomainPCResources(rctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to delete recovery plan")
	})

	t.Run("returns error on protection policy delete failure", func(t *testing.T) {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		mockV3Service := mocknutanixv3.NewMockService(mockCtrl)
		ctx := context.Background()

		mockV3Service.EXPECT().DeleteRecoveryPlan(gomock.Any(), rpUUID0).Return(&prismclientv3.DeleteResponse{}, nil)
		mockV3Service.EXPECT().DeleteRecoveryPlan(gomock.Any(), rpUUID1).Return(&prismclientv3.DeleteResponse{}, nil)
		mockV3Service.EXPECT().DeleteProtectionRule(gomock.Any(), ppUUID).Return(
			nil, errors.New("internal server error"))

		rctx := &nctx.VHADomainContext{
			Context:       ctx,
			NutanixClient: &prismclientv3.Client{V3: mockV3Service},
			VHADomain:     newVHADomain(),
		}

		reconciler := &NutanixVirtualHADomainReconciler{}
		err := reconciler.deleteVHADomainPCResources(rctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to delete protection policy")
	})

	t.Run("skips empty spec gracefully", func(t *testing.T) {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		ctx := context.Background()

		rctx := &nctx.VHADomainContext{
			Context:       ctx,
			NutanixClient: &prismclientv3.Client{V3: mocknutanixv3.NewMockService(mockCtrl)},
			VHADomain: &infrav1.NutanixVirtualHADomain{
				ObjectMeta: metav1.ObjectMeta{Name: "empty-vha", Namespace: "default"},
				Spec:       infrav1.NutanixVirtualHADomainSpec{MetroRef: corev1.LocalObjectReference{Name: "metro"}},
			},
		}

		reconciler := &NutanixVirtualHADomainReconciler{}
		err := reconciler.deleteVHADomainPCResources(rctx)
		require.NoError(t, err)
	})
}

func TestEnsureVHADomainPCResources_Idempotent(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	ctx := context.Background()
	vhaDomain := &infrav1.NutanixVirtualHADomain{
		ObjectMeta: metav1.ObjectMeta{Name: "test-vha", Namespace: "default"},
		Spec: infrav1.NutanixVirtualHADomainSpec{
			MetroRef: corev1.LocalObjectReference{Name: "test-metro"},
			ProtectionPolicy: &infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr.To("existing-pp-uuid"),
			},
			Categories: []infrav1.NutanixCategoryIdentifier{
				{Key: "capx-vha-test-vha", Value: "fd-1"},
				{Key: "capx-vha-test-vha", Value: "fd-2"},
			},
			RecoveryPlans: []infrav1.NutanixResourceIdentifier{
				{Type: infrav1.NutanixIdentifierUUID, UUID: ptr.To("rp-0")},
				{Type: infrav1.NutanixIdentifierUUID, UUID: ptr.To("rp-1")},
			},
		},
	}

	rctx := &nctx.VHADomainContext{
		Context:   ctx,
		VHADomain: vhaDomain,
	}

	reconciler := &NutanixVirtualHADomainReconciler{}
	err := reconciler.ensureVHADomainPCResources(rctx, nil, nil)
	require.NoError(t, err)
}

func TestEnsureVHADomainPCResources_CreatesAll(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	mockV3Service := mocknutanixv3.NewMockService(mockCtrl)
	mockConverged := NewMockConvergedClient(mockCtrl)
	ctx := context.Background()

	peUUID1 := "pe-uuid-1111"
	peUUID2 := "pe-uuid-2222"

	cluster1 := clustermgmtconfigCluster(peUUID1, "pe-1")
	cluster2 := clustermgmtconfigCluster(peUUID2, "pe-2")

	mockConverged.MockClusters.EXPECT().Get(gomock.Any(), gomock.Any()).Return(&cluster1, nil)
	mockConverged.MockClusters.EXPECT().Get(gomock.Any(), gomock.Any()).Return(&cluster2, nil)

	mockConverged.MockCategories.EXPECT().List(gomock.Any(), gomock.Any()).Return(
		[]prismModels.Category{{ExtId: ptr.To("cat-1"), Key: ptr.To("capx-vha-test-vha"), Value: ptr.To("fd-1")}}, nil,
	)
	mockConverged.MockCategories.EXPECT().List(gomock.Any(), gomock.Any()).Return(
		[]prismModels.Category{{ExtId: ptr.To("cat-2"), Key: ptr.To("capx-vha-test-vha"), Value: ptr.To("fd-2")}}, nil,
	)

	ppUUID := "created-pp-uuid"
	mockV3Service.EXPECT().ListAllProtectionRules(gomock.Any(), gomock.Any()).Return(
		&prismclientv3.ProtectionRulesListResponse{Entities: []*prismclientv3.ProtectionRuleResponse{}}, nil)
	mockV3Service.EXPECT().CreateProtectionRule(gomock.Any(), gomock.Any()).Return(
		&prismclientv3.ProtectionRuleResponse{
			Metadata: &prismclientv3.Metadata{UUID: ptr.To(ppUUID)},
		}, nil)

	rpUUID0 := "created-rp-uuid-0"
	rpUUID1 := "created-rp-uuid-1"
	mockV3Service.EXPECT().ListAllRecoveryPlans(gomock.Any(), gomock.Any()).Return(
		&models.RecoveryPlanListIntentResponse{Entities: []*models.RecoveryPlanIntentResource{}}, nil)
	mockV3Service.EXPECT().CreateRecoveryPlan(gomock.Any(), gomock.Any()).Return(
		&models.RecoveryPlanIntentResponse{
			Metadata: &models.RecoveryPlanMetadata{UUID: rpUUID0},
		}, nil)
	mockV3Service.EXPECT().ListAllRecoveryPlans(gomock.Any(), gomock.Any()).Return(
		&models.RecoveryPlanListIntentResponse{Entities: []*models.RecoveryPlanIntentResource{}}, nil)
	mockV3Service.EXPECT().CreateRecoveryPlan(gomock.Any(), gomock.Any()).Return(
		&models.RecoveryPlanIntentResponse{
			Metadata: &models.RecoveryPlanMetadata{UUID: rpUUID1},
		}, nil)

	vhaDomain := &infrav1.NutanixVirtualHADomain{
		ObjectMeta: metav1.ObjectMeta{Name: "test-vha", Namespace: "default"},
		Spec: infrav1.NutanixVirtualHADomainSpec{
			MetroRef: corev1.LocalObjectReference{Name: "test-metro"},
		},
	}

	metroObj := &infrav1.NutanixMetro{
		ObjectMeta: metav1.ObjectMeta{Name: "test-metro", Namespace: "default"},
		Spec: infrav1.NutanixMetroSpec{
			FailureDomains: []corev1.LocalObjectReference{
				{Name: "fd-1"},
				{Name: "fd-2"},
			},
		},
	}

	failureDomains := []*infrav1.NutanixFailureDomain{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "fd-1", Namespace: "default"},
			Spec: infrav1.NutanixFailureDomainSpec{
				PrismElementCluster: infrav1.NutanixResourceIdentifier{
					Type: infrav1.NutanixIdentifierUUID,
					UUID: ptr.To(peUUID1),
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "fd-2", Namespace: "default"},
			Spec: infrav1.NutanixFailureDomainSpec{
				PrismElementCluster: infrav1.NutanixResourceIdentifier{
					Type: infrav1.NutanixIdentifierUUID,
					UUID: ptr.To(peUUID2),
				},
			},
		},
	}

	rctx := &nctx.VHADomainContext{
		Context:         ctx,
		NutanixClient:   &prismclientv3.Client{V3: mockV3Service},
		ConvergedClient: mockConverged.Client,
		VHADomain:       vhaDomain,
	}

	reconciler := &NutanixVirtualHADomainReconciler{}
	err := reconciler.ensureVHADomainPCResources(rctx, metroObj, failureDomains)
	require.NoError(t, err)

	assert.Len(t, vhaDomain.Spec.Categories, 2)
	assert.Equal(t, "capx-vha-test-vha", vhaDomain.Spec.Categories[0].Key)
	assert.Equal(t, "fd-1", vhaDomain.Spec.Categories[0].Value)
	assert.Equal(t, "capx-vha-test-vha", vhaDomain.Spec.Categories[1].Key)
	assert.Equal(t, "fd-2", vhaDomain.Spec.Categories[1].Value)

	require.NotNil(t, vhaDomain.Spec.ProtectionPolicy)
	assert.Equal(t, ppUUID, *vhaDomain.Spec.ProtectionPolicy.UUID)

	assert.Len(t, vhaDomain.Spec.RecoveryPlans, 2)
	assert.Equal(t, rpUUID0, *vhaDomain.Spec.RecoveryPlans[0].UUID)
	assert.Equal(t, rpUUID1, *vhaDomain.Spec.RecoveryPlans[1].UUID)
}

func TestEnsureVHADomainPCResources_ReusesExisting(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	mockV3Service := mocknutanixv3.NewMockService(mockCtrl)
	mockConverged := NewMockConvergedClient(mockCtrl)
	ctx := context.Background()

	peUUID1 := "pe-uuid-1111"
	peUUID2 := "pe-uuid-2222"

	cluster1 := clustermgmtconfigCluster(peUUID1, "pe-1")
	cluster2 := clustermgmtconfigCluster(peUUID2, "pe-2")

	mockConverged.MockClusters.EXPECT().Get(gomock.Any(), gomock.Any()).Return(&cluster1, nil)
	mockConverged.MockClusters.EXPECT().Get(gomock.Any(), gomock.Any()).Return(&cluster2, nil)

	mockConverged.MockCategories.EXPECT().List(gomock.Any(), gomock.Any()).Return(
		[]prismModels.Category{{ExtId: ptr.To("cat-1")}}, nil,
	).Times(2)

	existingPPUUID := "existing-pp-uuid"
	mockV3Service.EXPECT().ListAllProtectionRules(gomock.Any(), gomock.Any()).Return(
		&prismclientv3.ProtectionRulesListResponse{
			Entities: []*prismclientv3.ProtectionRuleResponse{
				{
					Metadata: &prismclientv3.Metadata{UUID: ptr.To(existingPPUUID)},
					Spec:     &prismclientv3.ProtectionRuleSpec{Name: "capx-vha-test-vha"},
				},
			},
		}, nil)

	existingRP0UUID := "existing-rp-uuid-0"
	existingRP1UUID := "existing-rp-uuid-1"
	mockV3Service.EXPECT().ListAllRecoveryPlans(gomock.Any(), gomock.Any()).Return(
		&models.RecoveryPlanListIntentResponse{
			Entities: []*models.RecoveryPlanIntentResource{
				{
					Metadata: &models.RecoveryPlanMetadata{UUID: existingRP0UUID},
					Spec:     &models.RecoveryPlan{Name: ptr.To("capx-vha-test-vha-rp-0")},
				},
			},
		}, nil)
	mockV3Service.EXPECT().ListAllRecoveryPlans(gomock.Any(), gomock.Any()).Return(
		&models.RecoveryPlanListIntentResponse{
			Entities: []*models.RecoveryPlanIntentResource{
				{
					Metadata: &models.RecoveryPlanMetadata{UUID: existingRP1UUID},
					Spec:     &models.RecoveryPlan{Name: ptr.To("capx-vha-test-vha-rp-1")},
				},
			},
		}, nil)

	vhaDomain := &infrav1.NutanixVirtualHADomain{
		ObjectMeta: metav1.ObjectMeta{Name: "test-vha", Namespace: "default"},
		Spec: infrav1.NutanixVirtualHADomainSpec{
			MetroRef: corev1.LocalObjectReference{Name: "test-metro"},
		},
	}

	metroObj := &infrav1.NutanixMetro{
		Spec: infrav1.NutanixMetroSpec{
			FailureDomains: []corev1.LocalObjectReference{{Name: "fd-1"}, {Name: "fd-2"}},
		},
	}

	failureDomains := []*infrav1.NutanixFailureDomain{
		{Spec: infrav1.NutanixFailureDomainSpec{PrismElementCluster: infrav1.NutanixResourceIdentifier{Type: infrav1.NutanixIdentifierUUID, UUID: ptr.To(peUUID1)}}},
		{Spec: infrav1.NutanixFailureDomainSpec{PrismElementCluster: infrav1.NutanixResourceIdentifier{Type: infrav1.NutanixIdentifierUUID, UUID: ptr.To(peUUID2)}}},
	}

	rctx := &nctx.VHADomainContext{
		Context:         ctx,
		NutanixClient:   &prismclientv3.Client{V3: mockV3Service},
		ConvergedClient: mockConverged.Client,
		VHADomain:       vhaDomain,
	}

	reconciler := &NutanixVirtualHADomainReconciler{}
	err := reconciler.ensureVHADomainPCResources(rctx, metroObj, failureDomains)
	require.NoError(t, err)

	assert.Equal(t, existingPPUUID, *vhaDomain.Spec.ProtectionPolicy.UUID)
	assert.Equal(t, existingRP0UUID, *vhaDomain.Spec.RecoveryPlans[0].UUID)
	assert.Equal(t, existingRP1UUID, *vhaDomain.Spec.RecoveryPlans[1].UUID)
}

func TestReconcileDelete(t *testing.T) {
	t.Run("blocks delete when NutanixCluster not in deletion", func(t *testing.T) {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		ctx := context.Background()
		rctx := &nctx.VHADomainContext{
			Context: ctx,
			NutanixCluster: &infrav1.NutanixCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster-1", Namespace: "default"},
			},
			VHADomain: &infrav1.NutanixVirtualHADomain{
				ObjectMeta: metav1.ObjectMeta{Name: "test-vha", Namespace: "default"},
			},
		}

		reconciler := &NutanixVirtualHADomainReconciler{}
		err := reconciler.reconcileDelete(rctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot delete the vHADomain because its corresponding NutanixCluster")
	})

	t.Run("removes finalizer on successful delete", func(t *testing.T) {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		mockV3Service := mocknutanixv3.NewMockService(mockCtrl)
		ctx := context.Background()
		now := metav1.Now()

		vhaDomain := &infrav1.NutanixVirtualHADomain{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "test-vha",
				Namespace:  "default",
				Finalizers: []string{infrav1.NutanixVirtualHADomainFinalizer},
			},
			Spec: infrav1.NutanixVirtualHADomainSpec{
				MetroRef: corev1.LocalObjectReference{Name: "test-metro"},
			},
		}

		rctx := &nctx.VHADomainContext{
			Context: ctx,
			NutanixCluster: &infrav1.NutanixCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "cluster-1",
					Namespace:         "default",
					DeletionTimestamp: &now,
				},
			},
			NutanixClient: &prismclientv3.Client{V3: mockV3Service},
			VHADomain:     vhaDomain,
		}

		reconciler := &NutanixVirtualHADomainReconciler{}
		err := reconciler.reconcileDelete(rctx)
		require.NoError(t, err)
		assert.NotContains(t, vhaDomain.Finalizers, infrav1.NutanixVirtualHADomainFinalizer)
	})
}

func TestReconcileNormal_StatusReady(t *testing.T) {
	t.Run("sets ready=true when all resources exist", func(t *testing.T) {
		vhaDomain := &infrav1.NutanixVirtualHADomain{
			ObjectMeta: metav1.ObjectMeta{Name: "test-vha", Namespace: "default"},
			Spec: infrav1.NutanixVirtualHADomainSpec{
				MetroRef: corev1.LocalObjectReference{Name: "test-metro"},
				ProtectionPolicy: &infrav1.NutanixResourceIdentifier{
					Type: infrav1.NutanixIdentifierUUID,
					UUID: ptr.To("pp-uuid"),
				},
				Categories: []infrav1.NutanixCategoryIdentifier{
					{Key: "k", Value: "v1"},
					{Key: "k", Value: "v2"},
				},
				RecoveryPlans: []infrav1.NutanixResourceIdentifier{
					{Type: infrav1.NutanixIdentifierUUID, UUID: ptr.To("rp-0")},
					{Type: infrav1.NutanixIdentifierUUID, UUID: ptr.To("rp-1")},
				},
			},
		}

		metroObj := &infrav1.NutanixMetro{
			ObjectMeta: metav1.ObjectMeta{Name: "test-metro", Namespace: "default"},
			Spec: infrav1.NutanixMetroSpec{
				FailureDomains: []corev1.LocalObjectReference{{Name: "fd-1"}, {Name: "fd-2"}},
			},
		}

		Expect := func(got *infrav1.NutanixVirtualHADomain) {
			assert.True(t, got.Status.Ready)
		}

		cluster := &capiv1beta2.Cluster{
			ObjectMeta: metav1.ObjectMeta{Name: "cl", Namespace: "default"},
		}
		ntnxCluster := &infrav1.NutanixCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "ncl", Namespace: "default"},
		}
		fd1 := &infrav1.NutanixFailureDomain{
			ObjectMeta: metav1.ObjectMeta{Name: "fd-1", Namespace: "default"},
		}
		fd2 := &infrav1.NutanixFailureDomain{
			ObjectMeta: metav1.ObjectMeta{Name: "fd-2", Namespace: "default"},
		}

		scheme, err := infrav1.SchemeBuilder.Build()
		require.NoError(t, err)
		require.NoError(t, capiv1beta2.AddToScheme(scheme))

		fakeClient, err := buildFakeClient(scheme, metroObj, fd1, fd2, vhaDomain, cluster, ntnxCluster)
		require.NoError(t, err)

		rctx := &nctx.VHADomainContext{
			Context:        context.Background(),
			Cluster:        cluster,
			NutanixCluster: ntnxCluster,
			VHADomain:      vhaDomain,
		}

		reconciler := &NutanixVirtualHADomainReconciler{Client: fakeClient}
		err = reconciler.reconcileNormal(rctx)
		require.NoError(t, err)
		Expect(vhaDomain)
	})
}

func buildFakeClient(scheme *runtime.Scheme, objs ...client.Object) (client.Client, error) {
	builder := fake.NewClientBuilder().WithScheme(scheme)
	for _, obj := range objs {
		builder = builder.WithObjects(obj)
	}
	return builder.Build(), nil
}

func clustermgmtconfigCluster(uuid, name string) clustermgmtconfig.Cluster {
	return clustermgmtconfig.Cluster{
		ExtId: ptr.To(uuid),
		Name:  ptr.To(name),
	}
}
