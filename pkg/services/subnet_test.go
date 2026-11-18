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
	"context"
	"errors"
	"testing"

	clusterModels "github.com/nutanix/ntnx-api-golang-clients/clustermgmt-go-client/v4/models/clustermgmt/v4/config"
	subnetModels "github.com/nutanix/ntnx-api-golang-clients/networking-go-client/v4/models/networking/v4/config"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	nctx "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/pkg/context"
	converged "github.com/nutanix-cloud-native/prism-go-client/converged"
	v4Converged "github.com/nutanix-cloud-native/prism-go-client/converged/v4"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"k8s.io/utils/ptr"
)

func TestGetSubnetUUID(t *testing.T) {
	ctx := context.Background()
	peUUID := "11111111-1111-1111-1111-111111111111"

	tests := []struct {
		name          string
		clientBuilder func() *v4Converged.Client
		peUUID        string
		subnetName    *string
		subnetUUID    *string
		project       *nctx.ProjectInfo
		pcVersion     string
		want          string
		wantErr       bool
	}{
		{
			name: "missing name and UUID in the input",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				return convergedClient.Client
			},
			peUUID:  peUUID,
			wantErr: true,
		},
		{
			name: "subnet UUID not found (classified ErrNotFound)",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				convergedClient.MockSubnets.EXPECT().Get(gomock.Any(), "missing-uuid").
					Return(nil, &converged.APIError{Kind: converged.ErrNotFound, Cause: errors.New("entity not found")})
				return convergedClient.Client
			},
			peUUID:     peUUID,
			subnetUUID: ptr.To("missing-uuid"),
			wantErr:    true,
		},
		{
			name: "subnet UUID not found (unclassified error)",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				convergedClient.MockSubnets.EXPECT().Get(gomock.Any(), "test-uuid").Return(nil, errors.New("generic API error"))
				return convergedClient.Client
			},
			peUUID:     peUUID,
			subnetUUID: ptr.To("test-uuid"),
			wantErr:    true,
		},
		{
			name: "subnet UUID found",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				convergedClient.MockSubnets.EXPECT().Get(gomock.Any(), "found-subnet-uuid").Return(
					&subnetModels.Subnet{
						ExtId: ptr.To("found-subnet-uuid"),
						Name:  ptr.To("my-subnet"),
					}, nil)
				return convergedClient.Client
			},
			peUUID:     peUUID,
			subnetUUID: ptr.To("found-subnet-uuid"),
			want:       "found-subnet-uuid",
			wantErr:    false,
		},
		{
			name: "subnet name query fails",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				convergedClient.MockSubnets.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, errors.New("fake error"))
				return convergedClient.Client
			},
			peUUID:     peUUID,
			subnetName: ptr.To("my-subnet"),
			wantErr:    true,
		},
		{
			name: "subnet name found - overlay subnet",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				subnetType := subnetModels.SUBNETTYPE_OVERLAY
				convergedClient.MockSubnets.EXPECT().List(gomock.Any(), gomock.Any()).Return(
					[]subnetModels.Subnet{
						{
							ExtId:      ptr.To("overlay-subnet-uuid"),
							Name:       ptr.To("my-overlay"),
							SubnetType: &subnetType,
						},
					}, nil)
				return convergedClient.Client
			},
			peUUID:     peUUID,
			subnetName: ptr.To("my-overlay"),
			want:       "overlay-subnet-uuid",
			wantErr:    false,
		},
		{
			name: "subnet name found - VLAN subnet with ClusterReference",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				subnetType := subnetModels.SUBNETTYPE_VLAN
				convergedClient.MockSubnets.EXPECT().List(gomock.Any(), gomock.Any()).Return(
					[]subnetModels.Subnet{
						{
							ExtId:            ptr.To("vlan-subnet-uuid"),
							Name:             ptr.To("my-vlan"),
							SubnetType:       &subnetType,
							ClusterReference: ptr.To(peUUID),
						},
					}, nil)
				return convergedClient.Client
			},
			peUUID:     peUUID,
			subnetName: ptr.To("my-vlan"),
			want:       "vlan-subnet-uuid",
			wantErr:    false,
		},
		{
			name: "subnet name matches zero subnets",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				convergedClient.MockSubnets.EXPECT().List(gomock.Any(), gomock.Any()).Return([]subnetModels.Subnet{}, nil)
				return convergedClient.Client
			},
			peUUID:     peUUID,
			subnetName: ptr.To("nonexistent"),
			wantErr:    true,
		},
		{
			name: "subnet name matches multiple subnets",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				subnetType := subnetModels.SUBNETTYPE_OVERLAY
				convergedClient.MockSubnets.EXPECT().List(gomock.Any(), gomock.Any()).Return(
					[]subnetModels.Subnet{
						{ExtId: ptr.To("uuid-1"), Name: ptr.To("dup"), SubnetType: &subnetType},
						{ExtId: ptr.To("uuid-2"), Name: ptr.To("dup"), SubnetType: &subnetType},
					}, nil)
				return convergedClient.Client
			},
			peUUID:     peUUID,
			subnetName: ptr.To("dup"),
			wantErr:    true,
		},
		{
			name: "project-aware UUID lookup succeeds when subnet owned by project",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				convergedClient.MockSubnets.EXPECT().Get(gomock.Any(), "owned-subnet-uuid").Return(
					&subnetModels.Subnet{
						ExtId:        ptr.To("owned-subnet-uuid"),
						Name:         ptr.To("my-subnet"),
						ProjectExtId: ptr.To("project-a"),
					}, nil)
				return convergedClient.Client
			},
			peUUID:     peUUID,
			subnetUUID: ptr.To("owned-subnet-uuid"),
			project:    &nctx.ProjectInfo{ExtID: ptr.To("project-a"), Name: ptr.To("project-a-name")},
			pcVersion:  "7.6",
			want:       "owned-subnet-uuid",
			wantErr:    false,
		},
		{
			name: "project-aware UUID lookup succeeds when subnet shared with project",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				convergedClient.MockSubnets.EXPECT().Get(gomock.Any(), "shared-subnet-uuid").Return(
					&subnetModels.Subnet{
						ExtId:              ptr.To("shared-subnet-uuid"),
						Name:               ptr.To("my-subnet"),
						ProjectExtId:       ptr.To("project-owner"),
						SharedWithProjects: []string{"project-a"},
					}, nil)
				return convergedClient.Client
			},
			peUUID:     peUUID,
			subnetUUID: ptr.To("shared-subnet-uuid"),
			project:    &nctx.ProjectInfo{ExtID: ptr.To("project-a"), Name: ptr.To("project-a-name")},
			pcVersion:  "7.6",
			want:       "shared-subnet-uuid",
			wantErr:    false,
		},
		{
			name: "project-aware UUID lookup fails when subnet not accessible in project",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				convergedClient.MockSubnets.EXPECT().Get(gomock.Any(), "other-subnet-uuid").Return(
					&subnetModels.Subnet{
						ExtId:        ptr.To("other-subnet-uuid"),
						Name:         ptr.To("my-subnet"),
						ProjectExtId: ptr.To("project-b"),
					}, nil)
				return convergedClient.Client
			},
			peUUID:     peUUID,
			subnetUUID: ptr.To("other-subnet-uuid"),
			project:    &nctx.ProjectInfo{ExtID: ptr.To("project-a"), Name: ptr.To("project-a-name")},
			pcVersion:  "7.6",
			wantErr:    true,
		},
		{
			name: "project-aware name lookup prefers project-owned over shared subnet",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				subnetType := subnetModels.SUBNETTYPE_OVERLAY
				convergedClient.MockSubnets.EXPECT().List(gomock.Any(), gomock.Any()).Return(
					[]subnetModels.Subnet{
						{
							ExtId:              ptr.To("shared-uuid"),
							Name:               ptr.To("dup"),
							SubnetType:         &subnetType,
							ProjectExtId:       ptr.To("project-owner"),
							SharedWithProjects: []string{"project-a"},
						},
						{
							ExtId:        ptr.To("owned-uuid"),
							Name:         ptr.To("dup"),
							SubnetType:   &subnetType,
							ProjectExtId: ptr.To("project-a"),
						},
					}, nil)
				return convergedClient.Client
			},
			peUUID:     peUUID,
			subnetName: ptr.To("dup"),
			project:    &nctx.ProjectInfo{ExtID: ptr.To("project-a"), Name: ptr.To("project-a-name")},
			pcVersion:  "7.6",
			want:       "owned-uuid",
			wantErr:    false,
		},
		{
			name: "project-aware name lookup falls back to shared subnet when none owned",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				subnetType := subnetModels.SUBNETTYPE_OVERLAY
				convergedClient.MockSubnets.EXPECT().List(gomock.Any(), gomock.Any()).Return(
					[]subnetModels.Subnet{
						{
							ExtId:              ptr.To("shared-uuid"),
							Name:               ptr.To("my-subnet"),
							SubnetType:         &subnetType,
							ProjectExtId:       ptr.To("project-owner"),
							SharedWithProjects: []string{"project-a"},
						},
					}, nil)
				return convergedClient.Client
			},
			peUUID:     peUUID,
			subnetName: ptr.To("my-subnet"),
			project:    &nctx.ProjectInfo{ExtID: ptr.To("project-a"), Name: ptr.To("project-a-name")},
			pcVersion:  "7.6",
			want:       "shared-uuid",
			wantErr:    false,
		},
		{
			name: "project-aware name lookup fails when no subnet accessible in project",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				subnetType := subnetModels.SUBNETTYPE_OVERLAY
				convergedClient.MockSubnets.EXPECT().List(gomock.Any(), gomock.Any()).Return(
					[]subnetModels.Subnet{
						{
							ExtId:        ptr.To("other-uuid"),
							Name:         ptr.To("my-subnet"),
							SubnetType:   &subnetType,
							ProjectExtId: ptr.To("project-b"),
						},
					}, nil)
				return convergedClient.Client
			},
			peUUID:     peUUID,
			subnetName: ptr.To("my-subnet"),
			project:    &nctx.ProjectInfo{ExtID: ptr.To("project-a"), Name: ptr.To("project-a-name")},
			pcVersion:  "7.6",
			wantErr:    true,
		},
		{
			name: "project-aware name lookup fails when multiple owned subnets",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				subnetType := subnetModels.SUBNETTYPE_OVERLAY
				convergedClient.MockSubnets.EXPECT().List(gomock.Any(), gomock.Any()).Return(
					[]subnetModels.Subnet{
						{ExtId: ptr.To("owned-1"), Name: ptr.To("dup"), SubnetType: &subnetType, ProjectExtId: ptr.To("project-a")},
						{ExtId: ptr.To("owned-2"), Name: ptr.To("dup"), SubnetType: &subnetType, ProjectExtId: ptr.To("project-a")},
					}, nil)
				return convergedClient.Client
			},
			peUUID:     peUUID,
			subnetName: ptr.To("dup"),
			project:    &nctx.ProjectInfo{ExtID: ptr.To("project-a"), Name: ptr.To("project-a-name")},
			pcVersion:  "7.6",
			wantErr:    true,
		},
		{
			name: "PC below 7.6 ignores project scoping (legacy behavior)",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				convergedClient.MockSubnets.EXPECT().Get(gomock.Any(), "legacy-subnet-uuid").Return(
					&subnetModels.Subnet{
						ExtId:        ptr.To("legacy-subnet-uuid"),
						Name:         ptr.To("my-subnet"),
						ProjectExtId: ptr.To("project-b"),
					}, nil)
				return convergedClient.Client
			},
			peUUID:     peUUID,
			subnetUUID: ptr.To("legacy-subnet-uuid"),
			project:    &nctx.ProjectInfo{ExtID: ptr.To("project-a"), Name: ptr.To("project-a-name")},
			pcVersion:  "7.5",
			want:       "legacy-subnet-uuid",
			wantErr:    false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetSubnetUUID(ctx, tt.clientBuilder(), tt.peUUID, tt.subnetName, tt.subnetUUID, tt.project, tt.pcVersion)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetSubnetUUID() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("GetSubnetUUID() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetSubnetUUIDList(t *testing.T) {
	ctx := context.Background()
	peUUID := "11111111-1111-1111-1111-111111111111"

	t.Run("returns list of subnet UUIDs", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		convergedClient := NewMockConvergedClient(ctrl)
		subnetUUID1 := "subnet-uuid-1"
		subnetUUID2 := "subnet-uuid-2"

		convergedClient.MockSubnets.EXPECT().Get(gomock.Any(), subnetUUID1).Return(
			&subnetModels.Subnet{ExtId: &subnetUUID1}, nil)
		convergedClient.MockSubnets.EXPECT().Get(gomock.Any(), subnetUUID2).Return(
			&subnetModels.Subnet{ExtId: &subnetUUID2}, nil)

		machineSubnets := []infrav1.NutanixResourceIdentifier{
			{Type: infrav1.NutanixIdentifierUUID, UUID: &subnetUUID1},
			{Type: infrav1.NutanixIdentifierUUID, UUID: &subnetUUID2},
		}

		got, err := GetSubnetUUIDList(ctx, convergedClient.Client, machineSubnets, peUUID, nil, "")
		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, subnetUUID1, got[0])
		assert.Equal(t, subnetUUID2, got[1])
	})

	t.Run("returns error when GetSubnetUUID fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		convergedClient := NewMockConvergedClient(ctrl)
		convergedClient.MockSubnets.EXPECT().Get(gomock.Any(), gomock.Any()).
			Return(nil, &converged.APIError{Kind: converged.ErrNotFound, Cause: errors.New("entity not found")})

		machineSubnets := []infrav1.NutanixResourceIdentifier{
			{Type: infrav1.NutanixIdentifierUUID, UUID: ptr.To("missing-uuid")},
		}

		got, err := GetSubnetUUIDList(ctx, convergedClient.Client, machineSubnets, peUUID, nil, "")
		require.Error(t, err)
		assert.Empty(t, got)
	})
}

func TestSubnetNetworkKey(t *testing.T) {
	vlan := subnetModels.SUBNETTYPE_VLAN
	overlay := subnetModels.SUBNETTYPE_OVERLAY

	tests := []struct {
		name string
		in   *subnetModels.Subnet
		want string
	}{
		{
			name: "vlan with id and cidr",
			in: &subnetModels.Subnet{
				SubnetType: &vlan,
				NetworkId:  ptr.To(41),
				IpPrefix:   ptr.To("10.0.0.0/24"),
			},
			want: "VLAN|41|10.0.0.0/24",
		},
		{
			name: "overlay with vni",
			in: &subnetModels.Subnet{
				SubnetType: &overlay,
				NetworkId:  ptr.To(5000),
				IpPrefix:   ptr.To("192.168.0.0/16"),
			},
			want: "OVERLAY|5000|192.168.0.0/16",
		},
		{
			name: "nil subnet",
			in:   nil,
			want: "UNKNOWN||",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, subnetNetworkKey(tt.in))
		})
	}
}

func TestMetroSubnetsMatch(t *testing.T) {
	ctx := context.Background()
	pe0 := "00000000-0000-0000-0000-000000000010"
	pe1 := "00000000-0000-0000-0000-000000000011"
	subnet0 := "00000000-0000-0000-0000-0000000000a1"
	subnet1 := "00000000-0000-0000-0000-0000000000a2"
	vlan := subnetModels.SUBNETTYPE_VLAN

	pe0ID := infrav1.NutanixResourceIdentifier{Type: infrav1.NutanixIdentifierUUID, UUID: ptr.To(pe0)}
	pe1ID := infrav1.NutanixResourceIdentifier{Type: infrav1.NutanixIdentifierUUID, UUID: ptr.To(pe1)}
	subnet0ID := []infrav1.NutanixResourceIdentifier{{Type: infrav1.NutanixIdentifierUUID, UUID: ptr.To(subnet0)}}
	subnet1ID := []infrav1.NutanixResourceIdentifier{{Type: infrav1.NutanixIdentifierUUID, UUID: ptr.To(subnet1)}}

	t.Run("matches when VLAN ID and CIDR are equal across differently named subnets", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		client := NewMockConvergedClient(ctrl)
		client.MockClusters.EXPECT().Get(gomock.Any(), pe0).Return(&clusterModels.Cluster{ExtId: ptr.To(pe0)}, nil)
		client.MockClusters.EXPECT().Get(gomock.Any(), pe1).Return(&clusterModels.Cluster{ExtId: ptr.To(pe1)}, nil)
		client.MockSubnets.EXPECT().Get(gomock.Any(), subnet0).Return(&subnetModels.Subnet{
			ExtId: ptr.To(subnet0), Name: ptr.To("Vlan-041-site-01"), SubnetType: &vlan, NetworkId: ptr.To(41), IpPrefix: ptr.To("10.0.0.0/24"),
		}, nil)
		client.MockSubnets.EXPECT().Get(gomock.Any(), subnet1).Return(&subnetModels.Subnet{
			ExtId: ptr.To(subnet1), Name: ptr.To("Vlan-041-site-02"), SubnetType: &vlan, NetworkId: ptr.To(41), IpPrefix: ptr.To("10.0.0.0/24"),
		}, nil)

		match, machineKeys, fdKeys, err := MetroSubnetsMatch(ctx, client.Client, subnet1ID, subnet0ID, pe1ID, pe0ID)
		require.NoError(t, err)
		assert.True(t, match)
		assert.Equal(t, []string{"VLAN|41|10.0.0.0/24"}, machineKeys)
		assert.Equal(t, []string{"VLAN|41|10.0.0.0/24"}, fdKeys)
	})

	t.Run("does not match when VLAN IDs differ", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		client := NewMockConvergedClient(ctrl)
		client.MockClusters.EXPECT().Get(gomock.Any(), pe0).Return(&clusterModels.Cluster{ExtId: ptr.To(pe0)}, nil)
		client.MockClusters.EXPECT().Get(gomock.Any(), pe1).Return(&clusterModels.Cluster{ExtId: ptr.To(pe1)}, nil)
		client.MockSubnets.EXPECT().Get(gomock.Any(), subnet0).Return(&subnetModels.Subnet{
			ExtId: ptr.To(subnet0), Name: ptr.To("Vlan-041-site-01"), SubnetType: &vlan, NetworkId: ptr.To(41), IpPrefix: ptr.To("10.0.0.0/24"),
		}, nil)
		client.MockSubnets.EXPECT().Get(gomock.Any(), subnet1).Return(&subnetModels.Subnet{
			ExtId: ptr.To(subnet1), Name: ptr.To("Vlan-100-site-02"), SubnetType: &vlan, NetworkId: ptr.To(100), IpPrefix: ptr.To("10.0.1.0/24"),
		}, nil)

		match, machineKeys, fdKeys, err := MetroSubnetsMatch(ctx, client.Client, subnet1ID, subnet0ID, pe1ID, pe0ID)
		require.NoError(t, err)
		assert.False(t, match)
		assert.Equal(t, []string{"VLAN|100|10.0.1.0/24"}, machineKeys)
		assert.Equal(t, []string{"VLAN|41|10.0.0.0/24"}, fdKeys)
	})
}

func TestSubnetBelongsToCluster(t *testing.T) {
	peUUID := "11111111-1111-1111-1111-111111111111"
	otherUUID := "22222222-2222-2222-2222-222222222222"
	anotherUUID := "33333333-3333-3333-3333-333333333333"

	tests := []struct {
		name           string
		subnet         *subnetModels.Subnet
		peUUID         string
		expectedResult bool
		description    string
	}{
		{
			name: "subnet with matching ClusterReference",
			subnet: &subnetModels.Subnet{
				ClusterReference: ptr.To(peUUID),
			},
			peUUID:         peUUID,
			expectedResult: true,
			description:    "Should return true when ClusterReference matches the PE UUID",
		},
		{
			name: "subnet with non-matching ClusterReference",
			subnet: &subnetModels.Subnet{
				ClusterReference: ptr.To(otherUUID),
			},
			peUUID:         peUUID,
			expectedResult: false,
			description:    "Should return false when ClusterReference does not match the PE UUID",
		},
		{
			name: "subnet with matching UUID in ClusterReferenceList",
			subnet: &subnetModels.Subnet{
				ClusterReferenceList: []string{otherUUID, peUUID, anotherUUID},
			},
			peUUID:         peUUID,
			expectedResult: true,
			description:    "Should return true when PE UUID is in ClusterReferenceList",
		},
		{
			name: "subnet with non-matching ClusterReferenceList",
			subnet: &subnetModels.Subnet{
				ClusterReferenceList: []string{otherUUID, anotherUUID},
			},
			peUUID:         peUUID,
			expectedResult: false,
			description:    "Should return false when PE UUID is not in ClusterReferenceList",
		},
		{
			name: "subnet with both ClusterReference and ClusterReferenceList matching",
			subnet: &subnetModels.Subnet{
				ClusterReference:     ptr.To(peUUID),
				ClusterReferenceList: []string{peUUID, otherUUID},
			},
			peUUID:         peUUID,
			expectedResult: true,
			description:    "Should return true when both ClusterReference and ClusterReferenceList contain PE UUID",
		},
		{
			name: "subnet with ClusterReference matching but ClusterReferenceList not matching",
			subnet: &subnetModels.Subnet{
				ClusterReference:     ptr.To(peUUID),
				ClusterReferenceList: []string{otherUUID},
			},
			peUUID:         peUUID,
			expectedResult: true,
			description:    "Should return true when ClusterReference matches even if ClusterReferenceList doesn't",
		},
		{
			name: "subnet with ClusterReferenceList matching but ClusterReference not matching",
			subnet: &subnetModels.Subnet{
				ClusterReference:     ptr.To(otherUUID),
				ClusterReferenceList: []string{peUUID},
			},
			peUUID:         peUUID,
			expectedResult: true,
			description:    "Should return true when ClusterReferenceList matches even if ClusterReference doesn't",
		},
		{
			name: "subnet with nil ClusterReference and nil ClusterReferenceList",
			subnet: &subnetModels.Subnet{
				ClusterReference:     nil,
				ClusterReferenceList: nil,
			},
			peUUID:         peUUID,
			expectedResult: false,
			description:    "Should return false when both ClusterReference and ClusterReferenceList are nil",
		},
		{
			name: "subnet with nil ClusterReference and empty ClusterReferenceList",
			subnet: &subnetModels.Subnet{
				ClusterReference:     nil,
				ClusterReferenceList: []string{},
			},
			peUUID:         peUUID,
			expectedResult: false,
			description:    "Should return false when ClusterReference is nil and ClusterReferenceList is empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := subnetBelongsToCluster(tt.subnet, tt.peUUID)
			assert.Equal(t, tt.expectedResult, result, tt.description)
		})
	}
}
