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
	"fmt"
	"reflect"
	"strings"
	"testing"

	clusterModels "github.com/nutanix/ntnx-api-golang-clients/clustermgmt-go-client/v4/models/clustermgmt/v4/config"
	projectModels "github.com/nutanix/ntnx-api-golang-clients/multidomain-go-client/v4/models/multidomain/v4/config"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	converged "github.com/nutanix-cloud-native/prism-go-client/converged"
	v4Converged "github.com/nutanix-cloud-native/prism-go-client/converged/v4"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	"k8s.io/utils/ptr"
)

func TestGetPEUUID(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name          string
		clientBuilder func() *v4Converged.Client
		peName        *string
		peUUID        *string
		want          string
		wantErr       bool
		errorContains string
	}{
		{
			name: "PE cluster UUID not found (classified ErrNotFound)",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				convergedClient.MockClusters.EXPECT().Get(gomock.Any(), "missing-pe-uuid").
					Return(nil, &converged.APIError{Kind: converged.ErrNotFound, Cause: errors.New("entity not found")})
				return convergedClient.Client
			},
			peUUID:        ptr.To("missing-pe-uuid"),
			wantErr:       true,
			errorContains: "failed to find Prism Element cluster with UUID",
		},
		{
			name: "PE cluster UUID found",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				convergedClient.MockClusters.EXPECT().Get(gomock.Any(), "found-pe-uuid").Return(
					&clusterModels.Cluster{
						ExtId: ptr.To("found-pe-uuid"),
						Name:  ptr.To("my-cluster"),
					}, nil)
				return convergedClient.Client
			},
			peUUID:  ptr.To("found-pe-uuid"),
			want:    "found-pe-uuid",
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetPEUUID(ctx, tt.clientBuilder(), nil, tt.peName, tt.peUUID)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetPEUUID() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && tt.errorContains != "" && (err == nil || !strings.Contains(err.Error(), tt.errorContains)) {
				t.Errorf("GetPEUUID() error = %v, want to contain %q", err, tt.errorContains)
				return
			}
			if got != tt.want {
				t.Errorf("GetPEUUID() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetStorageContainerInCluster(t *testing.T) {
	storageContainers := []clusterModels.StorageContainer{
		{
			ClusterName:    ptr.To("pe_cluster"),
			ClusterExtId:   ptr.To("00062e56-b9ac-7253-1946-7cc25586eeee"),
			Name:           ptr.To("SelfServiceContainer"),
			ContainerExtId: ptr.To("2a61b02a-54a6-475e-93b9-5efc895b48e3"),
		},
	}

	mockctl := gomock.NewController(t)
	defer mockctl.Finish()

	tests := []struct {
		name               string
		mockBuilder        func() *v4Converged.Client
		rg                 *projectModels.ResourceGroup
		storageContainerId infrav1.NutanixResourceIdentifier
		clusterId          infrav1.NutanixResourceIdentifier
		want               *clusterModels.StorageContainer
		wantErr            bool
		errorMessage       string
		assertNotFound     bool
	}{
		{
			name: "GetStorageContainerInCluster succeeds with ID UUID",
			mockBuilder: func() *v4Converged.Client {
				mockClientWrapper := NewMockConvergedClient(mockctl)
				mockClientWrapper.MockStorageContainers.EXPECT().List(gomock.Any(), gomock.Any()).Return(storageContainers, nil)
				return mockClientWrapper.Client
			},
			clusterId: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr.To("00062e56-b9ac-7253-1946-7cc25586eeee"),
			},
			storageContainerId: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr.To("2a61b02a-54a6-475e-93b9-5efc895b48e3"),
			},
			want:         &storageContainers[0],
			wantErr:      false,
			errorMessage: "",
		},
		{
			name: "GetStorageContainerInCluster succeeds with ID Name",
			mockBuilder: func() *v4Converged.Client {
				mockClientWrapper := NewMockConvergedClient(mockctl)
				mockClientWrapper.MockStorageContainers.EXPECT().List(gomock.Any(), gomock.Any()).Return(storageContainers, nil)
				return mockClientWrapper.Client
			},
			clusterId: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr.To("00062e56-b9ac-7253-1946-7cc25586eeee"),
			},
			storageContainerId: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierName,
				Name: ptr.To("SelfServiceContainer"),
			},
			want:         &storageContainers[0],
			wantErr:      false,
			errorMessage: "",
		},
		{
			name: "GetStorageContainerInCluster succeeds with ID UUID and cluster name",
			mockBuilder: func() *v4Converged.Client {
				mockClientWrapper := NewMockConvergedClient(mockctl)
				mockClientWrapper.MockStorageContainers.EXPECT().List(gomock.Any(), gomock.Any()).Return(storageContainers, nil)
				return mockClientWrapper.Client
			},
			clusterId: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierName,
				Name: ptr.To("pe_cluster"),
			},
			storageContainerId: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr.To("2a61b02a-54a6-475e-93b9-5efc895b48e3"),
			},
			want:         &storageContainers[0],
			wantErr:      false,
			errorMessage: "",
		},
		{
			name: "GetStorageContainerInCluster succeeds with ID Name and cluster name",
			mockBuilder: func() *v4Converged.Client {
				mockClientWrapper := NewMockConvergedClient(mockctl)
				mockClientWrapper.MockStorageContainers.EXPECT().List(gomock.Any(), gomock.Any()).Return(storageContainers, nil)
				return mockClientWrapper.Client
			},
			clusterId: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierName,
				Name: ptr.To("pe_cluster"),
			},
			storageContainerId: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierName,
				Name: ptr.To("SelfServiceContainer"),
			},
			want:         &storageContainers[0],
			wantErr:      false,
			errorMessage: "",
		},
		{
			name: "GetStorageContainerInCluster returns classified not found when no containers match",
			mockBuilder: func() *v4Converged.Client {
				mockClientWrapper := NewMockConvergedClient(mockctl)
				mockClientWrapper.MockStorageContainers.EXPECT().List(gomock.Any(), gomock.Any()).Return([]clusterModels.StorageContainer{}, nil)
				return mockClientWrapper.Client
			},
			clusterId: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr.To("00062e56-b9ac-7253-1946-7cc25586eeee"),
			},
			storageContainerId: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr.To("2a61b02a-54a6-475e-93b9-5efc895b48e3"),
			},
			want:           nil,
			wantErr:        true,
			errorMessage:   "found no storage container using filter",
			assertNotFound: true,
		},
		{
			name: "GetStorageContainerInCluster fails",
			mockBuilder: func() *v4Converged.Client {
				mockClientWrapper := NewMockConvergedClient(mockctl)
				mockClientWrapper.MockStorageContainers.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, errors.New("fake error"))
				return mockClientWrapper.Client
			},
			clusterId: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr.To("00062e56-b9ac-7253-1946-7cc25586eeee"),
			},
			storageContainerId: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr.To("2a61b02a-54a6-475e-93b9-5efc895b48e3"),
			},
			want:         nil,
			wantErr:      true,
			errorMessage: "fake error",
		},
		{
			name: "GetStorageContainerInCluster fails with wrong cluster and storage container identifier type",
			mockBuilder: func() *v4Converged.Client {
				mockClientWrapper := NewMockConvergedClient(mockctl)
				return mockClientWrapper.Client
			},
			clusterId: infrav1.NutanixResourceIdentifier{
				Type: "qweqweqwe",
			},
			storageContainerId: infrav1.NutanixResourceIdentifier{
				Type: "qweqweqwe",
			},
			want:         nil,
			wantErr:      true,
			errorMessage: "storage container identifier is missing both name and uuid",
		},
		{
			name: "GetStorageContainerInCluster fails with wrong cluster identifier type and storage container ID UUID",
			mockBuilder: func() *v4Converged.Client {
				mockClientWrapper := NewMockConvergedClient(mockctl)
				return mockClientWrapper.Client
			},
			clusterId: infrav1.NutanixResourceIdentifier{
				Type: "qweqweqwe",
			},
			storageContainerId: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr.To("06b1ce03-f384-4488-9ba1-ae17ebcf1f91"),
			},
			want:         nil,
			wantErr:      true,
			errorMessage: "cluster identifier is missing both name and uuid",
		},
		{
			name: "GetStorageContainerInCluster fails with wrong cluster identifier type and storage container ID Name",
			mockBuilder: func() *v4Converged.Client {
				mockClientWrapper := NewMockConvergedClient(mockctl)
				return mockClientWrapper.Client
			},
			clusterId: infrav1.NutanixResourceIdentifier{
				Type: "qweqweqwe",
			},
			storageContainerId: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierName,
				Name: ptr.To("SelfServiceContainer"),
			},
			want:         nil,
			wantErr:      true,
			errorMessage: "cluster identifier is missing both name and uuid",
		},
		{
			name: "GetStorageContainerInCluster fails with wrong storage container identifier type and cluster ID UUID",
			mockBuilder: func() *v4Converged.Client {
				mockClientWrapper := NewMockConvergedClient(mockctl)
				return mockClientWrapper.Client
			},
			clusterId: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr.To("00062e56-b9ac-7253-1946-7cc25586eeee"),
			},
			storageContainerId: infrav1.NutanixResourceIdentifier{
				Type: "qweqweqwe",
			},
			want:         nil,
			wantErr:      true,
			errorMessage: "storage container identifier is missing both name and uuid",
		},
		{
			name: "GetStorageContainerInCluster fails with wrong storage container identifier type and cluster ID Name",
			mockBuilder: func() *v4Converged.Client {
				mockClientWrapper := NewMockConvergedClient(mockctl)
				return mockClientWrapper.Client
			},
			clusterId: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr.To("00062e56-b9ac-7253-1946-7cc25586eeee"),
			},
			storageContainerId: infrav1.NutanixResourceIdentifier{
				Type: "qweqweqwe",
			},
			want:         nil,
			wantErr:      true,
			errorMessage: "storage container identifier is missing both name and uuid",
		},
		{
			name: "GetStorageContainerInCluster succeeds via resource group with SC UUID and PE UUID",
			mockBuilder: func() *v4Converged.Client {
				mockClientWrapper := NewMockConvergedClient(mockctl)
				mockClientWrapper.MockResourceGroups.EXPECT().ListStorageContainers(gomock.Any(), "rg-uuid").Return(
					[]converged.StorageContainerInfo{
						{
							ExtId: "2a61b02a-54a6-475e-93b9-5efc895b48e3",
							Name:  "SelfServiceContainer",
							PrismElement: converged.PrismElementInfo{
								ExtId: "00062e56-b9ac-7253-1946-7cc25586eeee",
								Name:  "pe_cluster",
							},
						},
					}, nil)
				return mockClientWrapper.Client
			},
			rg: &projectModels.ResourceGroup{ExtId: ptr.To("rg-uuid")},
			clusterId: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr.To("00062e56-b9ac-7253-1946-7cc25586eeee"),
			},
			storageContainerId: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr.To("2a61b02a-54a6-475e-93b9-5efc895b48e3"),
			},
			want: &clusterModels.StorageContainer{
				ContainerExtId: ptr.To("2a61b02a-54a6-475e-93b9-5efc895b48e3"),
				Name:           ptr.To("SelfServiceContainer"),
				ClusterExtId:   ptr.To("00062e56-b9ac-7253-1946-7cc25586eeee"),
				ClusterName:    ptr.To("pe_cluster"),
			},
			wantErr: false,
		},
		{
			name: "GetStorageContainerInCluster succeeds via resource group with SC Name and PE Name",
			mockBuilder: func() *v4Converged.Client {
				mockClientWrapper := NewMockConvergedClient(mockctl)
				mockClientWrapper.MockResourceGroups.EXPECT().ListStorageContainers(gomock.Any(), "rg-uuid").Return(
					[]converged.StorageContainerInfo{
						{
							ExtId: "2a61b02a-54a6-475e-93b9-5efc895b48e3",
							Name:  "SelfServiceContainer",
							PrismElement: converged.PrismElementInfo{
								ExtId: "00062e56-b9ac-7253-1946-7cc25586eeee",
								Name:  "pe_cluster",
							},
						},
					}, nil)
				return mockClientWrapper.Client
			},
			rg: &projectModels.ResourceGroup{ExtId: ptr.To("rg-uuid")},
			clusterId: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierName,
				Name: ptr.To("pe_cluster"),
			},
			storageContainerId: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierName,
				Name: ptr.To("SelfServiceContainer"),
			},
			want: &clusterModels.StorageContainer{
				ContainerExtId: ptr.To("2a61b02a-54a6-475e-93b9-5efc895b48e3"),
				Name:           ptr.To("SelfServiceContainer"),
				ClusterExtId:   ptr.To("00062e56-b9ac-7253-1946-7cc25586eeee"),
				ClusterName:    ptr.To("pe_cluster"),
			},
			wantErr: false,
		},
		{
			name: "GetStorageContainerInCluster via resource group fails when storage container not in PE",
			mockBuilder: func() *v4Converged.Client {
				mockClientWrapper := NewMockConvergedClient(mockctl)
				mockClientWrapper.MockResourceGroups.EXPECT().ListStorageContainers(gomock.Any(), "rg-uuid").Return(
					[]converged.StorageContainerInfo{
						{
							ExtId: "2a61b02a-54a6-475e-93b9-5efc895b48e3",
							Name:  "SelfServiceContainer",
							PrismElement: converged.PrismElementInfo{
								ExtId: "00062e56-b9ac-7253-1946-7cc25586eeee",
								Name:  "pe_cluster",
							},
						},
					}, nil)
				return mockClientWrapper.Client
			},
			rg: &projectModels.ResourceGroup{ExtId: ptr.To("rg-uuid")},
			clusterId: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierName,
				Name: ptr.To("pe_cluster"),
			},
			storageContainerId: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierName,
				Name: ptr.To("missing-container"),
			},
			want:           nil,
			wantErr:        true,
			errorMessage:   "found in resource group which is associated with prism element",
			assertNotFound: true,
		},
		{
			name: "GetStorageContainerInCluster via resource group fails when PE not authorized",
			mockBuilder: func() *v4Converged.Client {
				mockClientWrapper := NewMockConvergedClient(mockctl)
				mockClientWrapper.MockResourceGroups.EXPECT().ListStorageContainers(gomock.Any(), "rg-uuid").Return(
					[]converged.StorageContainerInfo{
						{
							ExtId: "2a61b02a-54a6-475e-93b9-5efc895b48e3",
							Name:  "SelfServiceContainer",
							PrismElement: converged.PrismElementInfo{
								ExtId: "00062e56-b9ac-7253-1946-7cc25586eeee",
								Name:  "pe_cluster",
							},
						},
					}, nil)
				return mockClientWrapper.Client
			},
			rg: &projectModels.ResourceGroup{ExtId: ptr.To("rg-uuid")},
			clusterId: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierName,
				Name: ptr.To("other_pe"),
			},
			storageContainerId: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierName,
				Name: ptr.To("SelfServiceContainer"),
			},
			want:           nil,
			wantErr:        true,
			errorMessage:   "found in resource group which is associated with prism element",
			assertNotFound: true,
		},
		{
			name: "GetStorageContainerInCluster via resource group fails on list error",
			mockBuilder: func() *v4Converged.Client {
				mockClientWrapper := NewMockConvergedClient(mockctl)
				mockClientWrapper.MockResourceGroups.EXPECT().ListStorageContainers(gomock.Any(), "rg-uuid").Return(
					nil, fmt.Errorf("boom"))
				return mockClientWrapper.Client
			},
			rg: &projectModels.ResourceGroup{ExtId: ptr.To("rg-uuid")},
			clusterId: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr.To("00062e56-b9ac-7253-1946-7cc25586eeee"),
			},
			storageContainerId: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr.To("2a61b02a-54a6-475e-93b9-5efc895b48e3"),
			},
			want:         nil,
			wantErr:      true,
			errorMessage: "failed to list storage containers for resource group rg-uuid",
		},
		{
			name: "GetStorageContainerInCluster via resource group fails when rg has no ExtId",
			mockBuilder: func() *v4Converged.Client {
				mockClientWrapper := NewMockConvergedClient(mockctl)
				return mockClientWrapper.Client
			},
			rg: &projectModels.ResourceGroup{},
			clusterId: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr.To("00062e56-b9ac-7253-1946-7cc25586eeee"),
			},
			storageContainerId: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr.To("2a61b02a-54a6-475e-93b9-5efc895b48e3"),
			},
			want:         nil,
			wantErr:      true,
			errorMessage: "resource group has no ExtId",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			got, err := GetStorageContainerInCluster(ctx, tt.mockBuilder(), tt.rg, tt.storageContainerId, tt.clusterId)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetStorageContainerInCluster() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetStorageContainerInCluster() = %v, want %v", got, tt.want)
			}
			if tt.errorMessage != "" {
				assert.Contains(t, err.Error(), tt.errorMessage)
			}
			if tt.assertNotFound {
				assert.True(t, IsTerminalError(err))
			}
		})
	}
}
