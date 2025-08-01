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
	"reflect"
	"testing"
	"time"

	credentialtypes "github.com/nutanix-cloud-native/prism-go-client/environment/credentials"
	prismclientv3 "github.com/nutanix-cloud-native/prism-go-client/v3"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/cluster-api/util"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	mockk8sclient "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/mocks/k8sclient"
	mocknutanixv3 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/mocks/nutanix"
	nutanixclient "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/pkg/client"
)

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

func TestGetImageByNameOrUUID(t *testing.T) {
	tests := []struct {
		name          string
		clientBuilder func() *prismclientv3.Client
		id            infrav1.NutanixResourceIdentifier
		want          *prismclientv3.ImageIntentResponse
		wantErr       bool
	}{
		{
			name: "missing name and UUID in the input",
			clientBuilder: func() *prismclientv3.Client {
				mockctrl := gomock.NewController(t)
				mockv3Service := mocknutanixv3.NewMockService(mockctrl)

				return &prismclientv3.Client{V3: mockv3Service}
			},
			id:      infrav1.NutanixResourceIdentifier{},
			wantErr: true,
		},
		{
			name: "image UUID not found",
			clientBuilder: func() *prismclientv3.Client {
				mockctrl := gomock.NewController(t)
				mockv3Service := mocknutanixv3.NewMockService(mockctrl)
				mockv3Service.EXPECT().GetImage(gomock.Any(), gomock.Any()).Return(nil, errors.New("ENTITY_NOT_FOUND"))

				return &prismclientv3.Client{V3: mockv3Service}
			},
			id: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr.To("32432daf-fb0e-4202-b444-2439f43a24c5"),
			},
			wantErr: true,
		},
		{
			name: "image name query fails",
			clientBuilder: func() *prismclientv3.Client {
				mockctrl := gomock.NewController(t)
				mockv3Service := mocknutanixv3.NewMockService(mockctrl)
				mockv3Service.EXPECT().ListAllImage(gomock.Any(), gomock.Any()).Return(nil, errors.New("fake error"))

				return &prismclientv3.Client{V3: mockv3Service}
			},
			id: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierName,
				Name: ptr.To("example"),
			},
			wantErr: true,
		},
		{
			name: "image UUID found",
			clientBuilder: func() *prismclientv3.Client {
				mockctrl := gomock.NewController(t)
				mockv3Service := mocknutanixv3.NewMockService(mockctrl)
				mockv3Service.EXPECT().GetImage(gomock.Any(), gomock.Any()).Return(
					&prismclientv3.ImageIntentResponse{
						Spec: &prismclientv3.Image{
							Name: ptr.To("example"),
						},
					}, nil,
				)

				return &prismclientv3.Client{V3: mockv3Service}
			},
			id: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr.To("32432daf-fb0e-4202-b444-2439f43a24c5"),
			},
			want: &prismclientv3.ImageIntentResponse{
				Spec: &prismclientv3.Image{
					Name: ptr.To("example"),
				},
			},
		},
		{
			name: "image name found",
			clientBuilder: func() *prismclientv3.Client {
				mockctrl := gomock.NewController(t)
				mockv3Service := mocknutanixv3.NewMockService(mockctrl)
				mockv3Service.EXPECT().ListAllImage(gomock.Any(), gomock.Any()).Return(
					&prismclientv3.ImageListIntentResponse{
						Entities: []*prismclientv3.ImageIntentResponse{
							{
								Spec: &prismclientv3.Image{
									Name: ptr.To("example"),
								},
							},
						},
					}, nil,
				)

				return &prismclientv3.Client{V3: mockv3Service}
			},
			id: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierName,
				Name: ptr.To("example"),
			},
			want: &prismclientv3.ImageIntentResponse{
				Spec: &prismclientv3.Image{
					Name: ptr.To("example"),
				},
			},
		},
		{
			name: "image name matches multiple images",
			clientBuilder: func() *prismclientv3.Client {
				mockctrl := gomock.NewController(t)
				mockv3Service := mocknutanixv3.NewMockService(mockctrl)
				mockv3Service.EXPECT().ListAllImage(gomock.Any(), gomock.Any()).Return(
					&prismclientv3.ImageListIntentResponse{
						Entities: []*prismclientv3.ImageIntentResponse{
							{
								Spec: &prismclientv3.Image{
									Name: ptr.To("example"),
								},
							},
							{
								Spec: &prismclientv3.Image{
									Name: ptr.To("example"),
								},
							},
						},
					}, nil,
				)

				return &prismclientv3.Client{V3: mockv3Service}
			},
			id: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierName,
				Name: ptr.To("example"),
			},
			wantErr: true,
		},
		{
			name: "image name matches zero images",
			clientBuilder: func() *prismclientv3.Client {
				mockctrl := gomock.NewController(t)
				mockv3Service := mocknutanixv3.NewMockService(mockctrl)
				mockv3Service.EXPECT().ListAllImage(gomock.Any(), gomock.Any()).Return(
					&prismclientv3.ImageListIntentResponse{
						Entities: []*prismclientv3.ImageIntentResponse{},
					}, nil,
				)

				return &prismclientv3.Client{V3: mockv3Service}
			},
			id: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierName,
				Name: ptr.To("example"),
			},
			wantErr: true,
		},
		{
			name: "image name matches one image",
			clientBuilder: func() *prismclientv3.Client {
				mockctrl := gomock.NewController(t)
				mockv3Service := mocknutanixv3.NewMockService(mockctrl)
				mockv3Service.EXPECT().ListAllImage(gomock.Any(), gomock.Any()).Return(
					&prismclientv3.ImageListIntentResponse{
						Entities: []*prismclientv3.ImageIntentResponse{
							{
								Spec: &prismclientv3.Image{
									Name: ptr.To("example"),
								},
							},
						},
					}, nil,
				)

				return &prismclientv3.Client{V3: mockv3Service}
			},
			id: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierName,
				Name: ptr.To("example"),
			},
			want: &prismclientv3.ImageIntentResponse{
				Spec: &prismclientv3.Image{
					Name: ptr.To("example"),
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Log("Running test case ", tt.name)
			ctx := context.Background()
			got, err := GetImage(ctx, tt.clientBuilder(), tt.id)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetImageByNameOrUUID() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetImageByNameOrUUID() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetImageByLookup(t *testing.T) {
	tests := []struct {
		name          string
		clientBuilder func() *prismclientv3.Client
		baseOS        string
		imageTemplate string
		k8sVersion    string
		want          *prismclientv3.ImageIntentResponse
		wantErr       bool
	}{
		{
			name: "successful image lookup",
			clientBuilder: func() *prismclientv3.Client {
				mockctrl := gomock.NewController(t)
				mockv3Service := mocknutanixv3.NewMockService(mockctrl)
				mockv3Service.EXPECT().ListAllImage(gomock.Any(), gomock.Any()).Return(
					&prismclientv3.ImageListIntentResponse{
						Entities: []*prismclientv3.ImageIntentResponse{
							{
								Spec: &prismclientv3.Image{
									Name: ptr.To("capx-ubuntu-1.31.4"),
								},
							},
						},
					}, nil,
				)
				return &prismclientv3.Client{V3: mockv3Service}
			},
			baseOS:        "ubuntu",
			imageTemplate: "capx-{{.BaseOS}}-{{.K8sVersion}}",
			k8sVersion:    "v1.31.4",
			want: &prismclientv3.ImageIntentResponse{
				Spec: &prismclientv3.Image{
					Name: ptr.To("capx-ubuntu-1.31.4"),
				},
			},
			wantErr: false,
		},
		{
			name: "failed template parsing",
			clientBuilder: func() *prismclientv3.Client {
				mockctrl := gomock.NewController(t)
				mockv3Service := mocknutanixv3.NewMockService(mockctrl)
				return &prismclientv3.Client{V3: mockv3Service}
			},
			baseOS:        "ubuntu",
			imageTemplate: "invalid-template-{{.InvalidField}}",
			k8sVersion:    "v1.31.4",
			want:          nil,
			wantErr:       true,
		},
		{
			name: "no matching image found",
			clientBuilder: func() *prismclientv3.Client {
				mockctrl := gomock.NewController(t)
				mockv3Service := mocknutanixv3.NewMockService(mockctrl)
				mockv3Service.EXPECT().ListAllImage(gomock.Any(), gomock.Any()).Return(
					&prismclientv3.ImageListIntentResponse{
						Entities: []*prismclientv3.ImageIntentResponse{},
					}, nil,
				)
				return &prismclientv3.Client{V3: mockv3Service}
			},
			baseOS:        "ubuntu",
			imageTemplate: "capx-{{.BaseOS}}-{{.K8sVersion}}",
			k8sVersion:    "v1.31.4",
			want:          nil,
			wantErr:       true,
		},
		{
			name: "multiple images, return latest by creation time",
			clientBuilder: func() *prismclientv3.Client {
				mockctrl := gomock.NewController(t)
				mockv3Service := mocknutanixv3.NewMockService(mockctrl)
				mockv3Service.EXPECT().ListAllImage(gomock.Any(), gomock.Any()).Return(
					&prismclientv3.ImageListIntentResponse{
						Entities: []*prismclientv3.ImageIntentResponse{
							{
								Spec: &prismclientv3.Image{
									Name: ptr.To("capx-ubuntu-1.31.4"),
								},
								Metadata: &prismclientv3.Metadata{
									CreationTime: ptr.To(time.Date(2023, 10, 1, 0, 0, 0, 0, time.UTC)),
								},
							},
							{
								Spec: &prismclientv3.Image{
									Name: ptr.To("capx-ubuntu-1.31.4"),
								},
								Metadata: &prismclientv3.Metadata{
									CreationTime: ptr.To(time.Date(2023, 10, 2, 0, 0, 0, 0, time.UTC)),
								},
							},
							{
								Spec: &prismclientv3.Image{
									Name: ptr.To("capx-ubuntu-1.31.4"),
								},
								Metadata: &prismclientv3.Metadata{
									CreationTime: ptr.To(time.Date(2023, 10, 3, 0, 0, 0, 0, time.UTC)),
								},
							},
						},
					}, nil,
				)
				return &prismclientv3.Client{V3: mockv3Service}
			},
			baseOS:        "ubuntu",
			imageTemplate: "capx-{{.BaseOS}}-{{.K8sVersion}}",
			k8sVersion:    "v1.31.4",
			want: &prismclientv3.ImageIntentResponse{
				Spec: &prismclientv3.Image{
					Name: ptr.To("capx-ubuntu-1.31.4"),
				},
				Metadata: &prismclientv3.Metadata{
					CreationTime: ptr.To(time.Date(2023, 10, 3, 0, 0, 0, 0, time.UTC)),
				},
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Log("Running test case ", tt.name)
			ctx := context.Background()
			got, err := GetImageByLookup(
				ctx,
				tt.clientBuilder(),
				&tt.imageTemplate,
				&tt.baseOS,
				&tt.k8sVersion,
			)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetImageByLookup() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetImageByLookup() = %v, want %v", got, tt.want)
			}
		})
	}
}

func defaultStorageContainerGroupsEntities() *prismclientv3.GroupsGetEntitiesResponse {
	return &prismclientv3.GroupsGetEntitiesResponse{
		FilteredGroupCount: 1,
		GroupResults: []*prismclientv3.GroupsGroupResult{
			{
				EntityResults: []*prismclientv3.GroupsEntity{
					{
						EntityID: "0019a4fa-125e-4cf4-a360-f1da91a52624",
						Data: []*prismclientv3.GroupsFieldData{
							{
								Name: "container_name",
								Values: []*prismclientv3.GroupsTimevaluePair{
									{
										Time: 1739804185661881,
										Values: []string{
											"objectslbdfd30fa4bc64b897f24251f6733b294",
										},
									},
								},
							},
							{
								Name: "cluster_name",
								Values: []*prismclientv3.GroupsTimevaluePair{
									{
										Time: 1739804185661881,
										Values: []string{
											"pe_cluster",
										},
									},
								},
							},
							{
								Name: "cluster",
								Values: []*prismclientv3.GroupsTimevaluePair{
									{
										Time: 1739804185661881,
										Values: []string{
											"00062e56-b9ac-7253-1946-7cc25586eeee",
										},
									},
								},
							},
						},
					},
					{
						EntityID: "0318bb5a-f8c3-45c1-ae01-9495d82226c4",
						Data: []*prismclientv3.GroupsFieldData{
							{
								Name: "container_name",
								Values: []*prismclientv3.GroupsTimevaluePair{
									{
										Time: 1739804029501281,
										Values: []string{
											"NutanixMetadataContainer",
										},
									},
								},
							},
							{
								Name: "cluster_name",
								Values: []*prismclientv3.GroupsTimevaluePair{
									{
										Time: 1739804029501281,
										Values: []string{
											"pe_cluster",
										},
									},
								},
							},
							{
								Name: "cluster",
								Values: []*prismclientv3.GroupsTimevaluePair{
									{
										Time: 1739804029501281,
										Values: []string{
											"00062e56-b9ac-7253-1946-7cc25586eeee",
										},
									},
								},
							},
						},
					},
					{
						EntityID: "06b1ce03-f384-4488-9ba1-ae17ebcf1f91",
						Data: []*prismclientv3.GroupsFieldData{
							{
								Name: "container_name",
								Values: []*prismclientv3.GroupsTimevaluePair{
									{
										Time: 1739804029501281,
										Values: []string{
											"default-container-82941575027230",
										},
									},
								},
							},
							{
								Name: "cluster_name",
								Values: []*prismclientv3.GroupsTimevaluePair{
									{
										Time: 1739804029501281,
										Values: []string{
											"pe_cluster",
										},
									},
								},
							},
							{
								Name: "cluster",
								Values: []*prismclientv3.GroupsTimevaluePair{
									{
										Time: 1739804029501281,
										Values: []string{
											"00062e56-b9ac-7253-1946-7cc25586eeee",
										},
									},
								},
							},
						},
					},
					{
						EntityID: "2a61b02a-54a6-475e-93b9-5efc895b48e3",
						Data: []*prismclientv3.GroupsFieldData{
							{
								Name: "container_name",
								Values: []*prismclientv3.GroupsTimevaluePair{
									{
										Time: 1739804029501281,
										Values: []string{
											"SelfServiceContainer",
										},
									},
								},
							},
							{
								Name: "cluster_name",
								Values: []*prismclientv3.GroupsTimevaluePair{
									{
										Time: 1739804029501281,
										Values: []string{
											"pe_cluster",
										},
									},
								},
							},
							{
								Name: "cluster",
								Values: []*prismclientv3.GroupsTimevaluePair{
									{
										Time: 1739804029501281,
										Values: []string{
											"00062e56-b9ac-7253-1946-7cc25586eeee",
										},
									},
								},
							},
						},
					},
					{
						EntityID: "eedfc1ea-d3b5-47a1-9286-9ccab494f911",
						Data: []*prismclientv3.GroupsFieldData{
							{
								Name: "container_name",
								Values: []*prismclientv3.GroupsTimevaluePair{
									{
										Time: 1739804029501281,
										Values: []string{
											"NutanixManagementShare",
										},
									},
								},
							},
							{
								Name: "cluster_name",
								Values: []*prismclientv3.GroupsTimevaluePair{
									{
										Time: 1739804029501281,
										Values: []string{
											"pe_cluster",
										},
									},
								},
							},
							{
								Name: "cluster",
								Values: []*prismclientv3.GroupsTimevaluePair{
									{
										Time: 1739804029501281,
										Values: []string{
											"00062e56-b9ac-7253-1946-7cc25586eeee",
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

func defaultStorageContainerIntentResponse() []*StorageContainerIntentResponse {
	return []*StorageContainerIntentResponse{
		{
			Name:        ptr.To("objectslbdfd30fa4bc64b897f24251f6733b294"),
			ClusterUUID: ptr.To("00062e56-b9ac-7253-1946-7cc25586eeee"),
			ClusterName: ptr.To("pe_cluster"),
			UUID:        ptr.To("0019a4fa-125e-4cf4-a360-f1da91a52624"),
		},
		{
			Name:        ptr.To("NutanixMetadataContainer"),
			ClusterUUID: ptr.To("00062e56-b9ac-7253-1946-7cc25586eeee"),
			ClusterName: ptr.To("pe_cluster"),
			UUID:        ptr.To("0318bb5a-f8c3-45c1-ae01-9495d82226c4"),
		},
		{
			Name:        ptr.To("default-container-82941575027230"),
			ClusterUUID: ptr.To("00062e56-b9ac-7253-1946-7cc25586eeee"),
			ClusterName: ptr.To("pe_cluster"),
			UUID:        ptr.To("06b1ce03-f384-4488-9ba1-ae17ebcf1f91"),
		},
		{
			Name:        ptr.To("SelfServiceContainer"),
			ClusterUUID: ptr.To("00062e56-b9ac-7253-1946-7cc25586eeee"),
			ClusterName: ptr.To("pe_cluster"),
			UUID:        ptr.To("2a61b02a-54a6-475e-93b9-5efc895b48e3"),
		},
		{
			Name:        ptr.To("NutanixManagementShare"),
			ClusterUUID: ptr.To("00062e56-b9ac-7253-1946-7cc25586eeee"),
			ClusterName: ptr.To("pe_cluster"),
			UUID:        ptr.To("eedfc1ea-d3b5-47a1-9286-9ccab494f911"),
		},
	}
}

func TestListStorageContainers(t *testing.T) {
	mockctrl := gomock.NewController(t)

	emptyStorageContainerIntentResponse := make([]*StorageContainerIntentResponse, 0)
	emptyStorageContainerGroupsEntities := &prismclientv3.GroupsGetEntitiesResponse{
		FilteredGroupCount: 0,
		GroupResults:       []*prismclientv3.GroupsGroupResult{},
	}

	tests := []struct {
		name         string
		mockBuilder  func() *prismclientv3.Client
		want         []*StorageContainerIntentResponse
		wantErr      bool
		errorMessage string
	}{
		{
			name: "ListStorageContrainer succeeds",
			mockBuilder: func() *prismclientv3.Client {
				groupEntitiesResponse := defaultStorageContainerGroupsEntities()
				mockPrismv3Service := mocknutanixv3.NewMockService(mockctrl)
				mockPrismv3Service.EXPECT().GroupsGetEntities(gomock.Any(), gomock.Any()).Return(groupEntitiesResponse, nil)
				return &prismclientv3.Client{V3: mockPrismv3Service}
			},
			want:         defaultStorageContainerIntentResponse(),
			wantErr:      false,
			errorMessage: "",
		},
		{
			name: "ListStorageContrainer fails",
			mockBuilder: func() *prismclientv3.Client {
				mockPrismv3Service := mocknutanixv3.NewMockService(mockctrl)
				mockPrismv3Service.EXPECT().GroupsGetEntities(gomock.Any(), gomock.Any()).Return(nil, errors.New("fake error"))
				return &prismclientv3.Client{V3: mockPrismv3Service}
			},
			want:         nil,
			wantErr:      true,
			errorMessage: "fake error",
		},
		{
			name: "ListStorageContrainer succeed with empty response",
			mockBuilder: func() *prismclientv3.Client {
				mockPrismv3Service := mocknutanixv3.NewMockService(mockctrl)
				mockPrismv3Service.EXPECT().GroupsGetEntities(gomock.Any(), gomock.Any()).Return(emptyStorageContainerGroupsEntities, nil)
				return &prismclientv3.Client{V3: mockPrismv3Service}
			},
			want:         emptyStorageContainerIntentResponse,
			wantErr:      false,
			errorMessage: "",
		},
		{
			name: "ListStorageContainers fails with GroupsTotalCount > 1",
			mockBuilder: func() *prismclientv3.Client {
				mockPrismv3Service := mocknutanixv3.NewMockService(mockctrl)
				groupEntities := &prismclientv3.GroupsGetEntitiesResponse{
					FilteredGroupCount: 2,
					GroupResults: []*prismclientv3.GroupsGroupResult{
						{
							EntityResults: []*prismclientv3.GroupsEntity{},
						},
						{
							EntityResults: []*prismclientv3.GroupsEntity{},
						},
					},
				}
				mockPrismv3Service.EXPECT().GroupsGetEntities(gomock.Any(), gomock.Any()).Return(groupEntities, nil)
				return &prismclientv3.Client{V3: mockPrismv3Service}
			},
			want:         nil,
			wantErr:      true,
			errorMessage: "unexpected number of group results",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			got, err := ListStorageContainers(ctx, tt.mockBuilder())
			if (err != nil) != tt.wantErr {
				t.Errorf("ListStorageContainers() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ListStorageContainers() = %v, want %v", got, tt.want)
			}
			if tt.errorMessage != "" {
				assert.Contains(t, err.Error(), tt.errorMessage)
			}
		})
	}
}

func TestGetStorageContainerByNtnxResourceIdentifier(t *testing.T) {
	mockctl := gomock.NewController(t)

	tests := []struct {
		name         string
		mockBuilder  func() *prismclientv3.Client
		id           infrav1.NutanixResourceIdentifier
		want         *StorageContainerIntentResponse
		wantErr      bool
		errorMessage string
	}{
		{
			name: "GetStorageContainerByNtnxResourceIdentifier succeeds with ID UUID",
			mockBuilder: func() *prismclientv3.Client {
				mockPrismv3Service := mocknutanixv3.NewMockService(mockctl)
				mockPrismv3Service.EXPECT().GroupsGetEntities(gomock.Any(), gomock.Any()).Return(defaultStorageContainerGroupsEntities(), nil)
				return &prismclientv3.Client{V3: mockPrismv3Service}
			},
			id: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr.To("06b1ce03-f384-4488-9ba1-ae17ebcf1f91"),
			},
			want: &StorageContainerIntentResponse{
				Name:        ptr.To("default-container-82941575027230"),
				ClusterUUID: ptr.To("00062e56-b9ac-7253-1946-7cc25586eeee"),
				ClusterName: ptr.To("pe_cluster"),
				UUID:        ptr.To("06b1ce03-f384-4488-9ba1-ae17ebcf1f91"),
			},
			wantErr:      false,
			errorMessage: "",
		},
		{
			name: "GetStorageContainerByNtnxResourceIdentifier succeeds with ID Name",
			mockBuilder: func() *prismclientv3.Client {
				mockPrismv3Service := mocknutanixv3.NewMockService(mockctl)
				mockPrismv3Service.EXPECT().GroupsGetEntities(gomock.Any(), gomock.Any()).Return(defaultStorageContainerGroupsEntities(), nil)
				return &prismclientv3.Client{V3: mockPrismv3Service}
			},
			id: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierName,
				Name: ptr.To("default-container-82941575027230"),
			},
			want: &StorageContainerIntentResponse{
				Name:        ptr.To("default-container-82941575027230"),
				ClusterUUID: ptr.To("00062e56-b9ac-7253-1946-7cc25586eeee"),
				ClusterName: ptr.To("pe_cluster"),
				UUID:        ptr.To("06b1ce03-f384-4488-9ba1-ae17ebcf1f91"),
			},
			wantErr:      false,
			errorMessage: "",
		},
		{
			name: "GetStorageContainerByNtnxResourceIdentifier fails",
			mockBuilder: func() *prismclientv3.Client {
				mockPrismv3Service := mocknutanixv3.NewMockService(mockctl)
				mockPrismv3Service.EXPECT().GroupsGetEntities(gomock.Any(), gomock.Any()).Return(nil, errors.New("fake error"))
				return &prismclientv3.Client{V3: mockPrismv3Service}
			},
			id: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr.To("06b1ce03-f384-4488-9ba1-ae17ebcf1f91"),
			},
			want:         nil,
			wantErr:      true,
			errorMessage: "fake error",
		},
		{
			name: "GetStorageContainerByNtnxResourceIdentifier fails with empty response with ID UUID",
			mockBuilder: func() *prismclientv3.Client {
				mockPrismv3Service := mocknutanixv3.NewMockService(mockctl)
				mockPrismv3Service.EXPECT().GroupsGetEntities(gomock.Any(), gomock.Any()).Return(defaultStorageContainerGroupsEntities(), nil)
				return &prismclientv3.Client{V3: mockPrismv3Service}
			},
			id: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr.To("01010101-0101-0101-0101-010101010101"),
			},
			want:         nil,
			wantErr:      true,
			errorMessage: "failed to find storage container",
		},
		{
			name: "GetStorageContainerByNtnxResourceIdentifier fails with empty response with ID Name",
			mockBuilder: func() *prismclientv3.Client {
				mockPrismv3Service := mocknutanixv3.NewMockService(mockctl)
				mockPrismv3Service.EXPECT().GroupsGetEntities(gomock.Any(), gomock.Any()).Return(defaultStorageContainerGroupsEntities(), nil)
				return &prismclientv3.Client{V3: mockPrismv3Service}
			},
			id: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierName,
				Name: ptr.To("non-existing-name"),
			},
			want:         nil,
			wantErr:      true,
			errorMessage: "failed to find storage container",
		},
		{
			name: "GetStorageContainerByNtnxResourceIdentifier fails with wrong identifier type",
			mockBuilder: func() *prismclientv3.Client {
				mockPrismv3Service := mocknutanixv3.NewMockService(mockctl)
				mockPrismv3Service.EXPECT().GroupsGetEntities(gomock.Any(), gomock.Any()).Return(defaultStorageContainerGroupsEntities(), nil)
				return &prismclientv3.Client{V3: mockPrismv3Service}
			},
			id: infrav1.NutanixResourceIdentifier{
				Type: "qweqweqwe",
			},
			want:         nil,
			wantErr:      true,
			errorMessage: "storage container identifier is missing both name and uuid",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			got, err := GetStorageContainerByNtnxResourceIdentifier(ctx, tt.mockBuilder(), tt.id)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetStorageContainerByNtnxResourceIdentifier() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetStorageContainerByNtnxResourceIdentifier() = %v, want %v", got, tt.want)
			}
			if tt.errorMessage != "" {
				assert.Contains(t, err.Error(), tt.errorMessage)
			}
		})
	}
}

func TestGetStorageContainerInCluster(t *testing.T) {
	mockctl := gomock.NewController(t)

	tests := []struct {
		name               string
		mockBuilder        func() *prismclientv3.Client
		storageContainerId infrav1.NutanixResourceIdentifier
		clusterId          infrav1.NutanixResourceIdentifier
		want               *StorageContainerIntentResponse
		wantErr            bool
		errorMessage       string
	}{
		{
			name: "GetStorageContainerInCluster succeeds with ID UUID",
			mockBuilder: func() *prismclientv3.Client {
				mockPrismv3Service := mocknutanixv3.NewMockService(mockctl)
				mockPrismv3Service.EXPECT().GroupsGetEntities(gomock.Any(), gomock.Any()).Return(defaultStorageContainerGroupsEntities(), nil)
				return &prismclientv3.Client{V3: mockPrismv3Service}
			},
			clusterId: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr.To("00062e56-b9ac-7253-1946-7cc25586eeee"),
			},
			storageContainerId: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr.To("06b1ce03-f384-4488-9ba1-ae17ebcf1f91"),
			},
			want: &StorageContainerIntentResponse{
				Name:        ptr.To("default-container-82941575027230"),
				ClusterUUID: ptr.To("00062e56-b9ac-7253-1946-7cc25586eeee"),
				ClusterName: ptr.To("pe_cluster"),
				UUID:        ptr.To("06b1ce03-f384-4488-9ba1-ae17ebcf1f91"),
			},
			wantErr:      false,
			errorMessage: "",
		},
		{
			name: "GetStorageContainerInCluster succeeds with ID Name",
			mockBuilder: func() *prismclientv3.Client {
				mockPrismv3Service := mocknutanixv3.NewMockService(mockctl)
				mockPrismv3Service.EXPECT().GroupsGetEntities(gomock.Any(), gomock.Any()).Return(defaultStorageContainerGroupsEntities(), nil)
				return &prismclientv3.Client{V3: mockPrismv3Service}
			},
			clusterId: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr.To("00062e56-b9ac-7253-1946-7cc25586eeee"),
			},
			storageContainerId: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierName,
				Name: ptr.To("default-container-82941575027230"),
			},
			want: &StorageContainerIntentResponse{
				Name:        ptr.To("default-container-82941575027230"),
				ClusterUUID: ptr.To("00062e56-b9ac-7253-1946-7cc25586eeee"),
				ClusterName: ptr.To("pe_cluster"),
				UUID:        ptr.To("06b1ce03-f384-4488-9ba1-ae17ebcf1f91"),
			},
			wantErr:      false,
			errorMessage: "",
		},
		{
			name: "GetStorageContainerInCluster succeeds with ID UUID and cluster name",
			mockBuilder: func() *prismclientv3.Client {
				mockPrismv3Service := mocknutanixv3.NewMockService(mockctl)
				mockPrismv3Service.EXPECT().GroupsGetEntities(gomock.Any(), gomock.Any()).Return(defaultStorageContainerGroupsEntities(), nil)
				return &prismclientv3.Client{V3: mockPrismv3Service}
			},
			clusterId: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierName,
				Name: ptr.To("pe_cluster"),
			},
			storageContainerId: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr.To("06b1ce03-f384-4488-9ba1-ae17ebcf1f91"),
			},
			want: &StorageContainerIntentResponse{
				Name:        ptr.To("default-container-82941575027230"),
				ClusterUUID: ptr.To("00062e56-b9ac-7253-1946-7cc25586eeee"),
				ClusterName: ptr.To("pe_cluster"),
				UUID:        ptr.To("06b1ce03-f384-4488-9ba1-ae17ebcf1f91"),
			},
			wantErr:      false,
			errorMessage: "",
		},
		{
			name: "GetStorageContainerInCluster succeeds with ID Name and cluster name",
			mockBuilder: func() *prismclientv3.Client {
				mockPrismv3Service := mocknutanixv3.NewMockService(mockctl)
				mockPrismv3Service.EXPECT().GroupsGetEntities(gomock.Any(), gomock.Any()).Return(defaultStorageContainerGroupsEntities(), nil)
				return &prismclientv3.Client{V3: mockPrismv3Service}
			},
			clusterId: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierName,
				Name: ptr.To("pe_cluster"),
			},
			storageContainerId: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierName,
				Name: ptr.To("default-container-82941575027230"),
			},
			want: &StorageContainerIntentResponse{
				Name:        ptr.To("default-container-82941575027230"),
				ClusterUUID: ptr.To("00062e56-b9ac-7253-1946-7cc25586eeee"),
				ClusterName: ptr.To("pe_cluster"),
				UUID:        ptr.To("06b1ce03-f384-4488-9ba1-ae17ebcf1f91"),
			},
			wantErr:      false,
			errorMessage: "",
		},
		{
			name: "GetStorageContainerInCluster fails",
			mockBuilder: func() *prismclientv3.Client {
				mockPrismv3Service := mocknutanixv3.NewMockService(mockctl)
				mockPrismv3Service.EXPECT().GroupsGetEntities(gomock.Any(), gomock.Any()).Return(nil, errors.New("fake error"))
				return &prismclientv3.Client{V3: mockPrismv3Service}
			},
			clusterId: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr.To("00062e56-b9ac-7253-1946-7cc25586eeee"),
			},
			storageContainerId: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr.To("06b1ce03-f384-4488-9ba1-ae17ebcf1f91"),
			},
			want:         nil,
			wantErr:      true,
			errorMessage: "fake error",
		},
		{
			name: "GetStorageContainerInCluster fails with empty response with non existent ID UUID",
			mockBuilder: func() *prismclientv3.Client {
				mockPrismv3Service := mocknutanixv3.NewMockService(mockctl)
				mockPrismv3Service.EXPECT().GroupsGetEntities(gomock.Any(), gomock.Any()).Return(defaultStorageContainerGroupsEntities(), nil)
				return &prismclientv3.Client{V3: mockPrismv3Service}
			},
			clusterId: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr.To("00062e56-b9ac-7253-1946-7cc25586eeee"),
			},
			storageContainerId: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr.To("01010101-0101-0101-0101-010101010101"),
			},
			want:         nil,
			wantErr:      true,
			errorMessage: "failed to find storage container",
		},
		{
			name: "GetStorageContainerInCluster fails with empty response with non existent ID Name",
			mockBuilder: func() *prismclientv3.Client {
				mockPrismv3Service := mocknutanixv3.NewMockService(mockctl)
				mockPrismv3Service.EXPECT().GroupsGetEntities(gomock.Any(), gomock.Any()).Return(defaultStorageContainerGroupsEntities(), nil)
				return &prismclientv3.Client{V3: mockPrismv3Service}
			},
			clusterId: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr.To("00062e56-b9ac-7253-1946-7cc25586eeee"),
			},
			storageContainerId: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierName,
				Name: ptr.To("non-existing-name"),
			},
			want:         nil,
			wantErr:      true,
			errorMessage: "failed to find storage container",
		},
		{
			name: "GetStorageContainerInCluster fails with empty response with non existent cluster ID UUID",
			mockBuilder: func() *prismclientv3.Client {
				mockPrismv3Service := mocknutanixv3.NewMockService(mockctl)
				mockPrismv3Service.EXPECT().GroupsGetEntities(gomock.Any(), gomock.Any()).Return(defaultStorageContainerGroupsEntities(), nil)
				return &prismclientv3.Client{V3: mockPrismv3Service}
			},
			clusterId: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr.To("01010101-0101-0101-0101-010101010101"),
			},
			storageContainerId: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr.To("06b1ce03-f384-4488-9ba1-ae17ebcf1f91"),
			},
			want:         nil,
			wantErr:      true,
			errorMessage: "failed to find storage container",
		},
		{
			name: "GetStorageContainerInCluster fails with empty response with non existent cluster ID Name",
			mockBuilder: func() *prismclientv3.Client {
				mockPrismv3Service := mocknutanixv3.NewMockService(mockctl)
				mockPrismv3Service.EXPECT().GroupsGetEntities(gomock.Any(), gomock.Any()).Return(defaultStorageContainerGroupsEntities(), nil)
				return &prismclientv3.Client{V3: mockPrismv3Service}
			},
			clusterId: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierName,
				Name: ptr.To("non-existing-name"),
			},
			storageContainerId: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr.To("06b1ce03-f384-4488-9ba1-ae17ebcf1f91"),
			},
			want:         nil,
			wantErr:      true,
			errorMessage: "failed to find storage container",
		},
		{
			name: "GetStorageContainerInCluster fails with wrong cluster and storage container identifier type",
			mockBuilder: func() *prismclientv3.Client {
				mockPrismv3Service := mocknutanixv3.NewMockService(mockctl)
				mockPrismv3Service.EXPECT().GroupsGetEntities(gomock.Any(), gomock.Any()).Return(defaultStorageContainerGroupsEntities(), nil)
				return &prismclientv3.Client{V3: mockPrismv3Service}
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
			mockBuilder: func() *prismclientv3.Client {
				mockPrismv3Service := mocknutanixv3.NewMockService(mockctl)
				mockPrismv3Service.EXPECT().GroupsGetEntities(gomock.Any(), gomock.Any()).Return(defaultStorageContainerGroupsEntities(), nil)
				return &prismclientv3.Client{V3: mockPrismv3Service}
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
			mockBuilder: func() *prismclientv3.Client {
				mockPrismv3Service := mocknutanixv3.NewMockService(mockctl)
				mockPrismv3Service.EXPECT().GroupsGetEntities(gomock.Any(), gomock.Any()).Return(defaultStorageContainerGroupsEntities(), nil)
				return &prismclientv3.Client{V3: mockPrismv3Service}
			},
			clusterId: infrav1.NutanixResourceIdentifier{
				Type: "qweqweqwe",
			},
			storageContainerId: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierName,
				Name: ptr.To("default-container-82941575027230"),
			},
			want:         nil,
			wantErr:      true,
			errorMessage: "cluster identifier is missing both name and uuid",
		},
		{
			name: "GetStorageContainerInCluster fails with wrong storage container identifier type and cluster ID UUID",
			mockBuilder: func() *prismclientv3.Client {
				mockPrismv3Service := mocknutanixv3.NewMockService(mockctl)
				mockPrismv3Service.EXPECT().GroupsGetEntities(gomock.Any(), gomock.Any()).Return(defaultStorageContainerGroupsEntities(), nil)
				return &prismclientv3.Client{V3: mockPrismv3Service}
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
			mockBuilder: func() *prismclientv3.Client {
				mockPrismv3Service := mocknutanixv3.NewMockService(mockctl)
				mockPrismv3Service.EXPECT().GroupsGetEntities(gomock.Any(), gomock.Any()).Return(defaultStorageContainerGroupsEntities(), nil)
				return &prismclientv3.Client{V3: mockPrismv3Service}
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			got, err := GetStorageContainerInCluster(ctx, tt.mockBuilder(), tt.storageContainerId, tt.clusterId)
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
		})
	}
}
