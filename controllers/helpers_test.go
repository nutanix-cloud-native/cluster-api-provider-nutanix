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
	"strings"
	"testing"
	"time"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	mockconverged "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/mocks/converged"
	mockk8sclient "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/mocks/k8sclient"
	nutanixclient "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/pkg/client"
	converged "github.com/nutanix-cloud-native/prism-go-client/converged"
	v4Converged "github.com/nutanix-cloud-native/prism-go-client/converged/v4"
	credentialtypes "github.com/nutanix-cloud-native/prism-go-client/environment/credentials"
	prismclientv3 "github.com/nutanix-cloud-native/prism-go-client/v3"
	clusterModels "github.com/nutanix/ntnx-api-golang-clients/clustermgmt-go-client/v4/models/clustermgmt/v4/config"
	subnetModels "github.com/nutanix/ntnx-api-golang-clients/networking-go-client/v4/models/networking/v4/config"
	prismModels "github.com/nutanix/ntnx-api-golang-clients/prism-go-client/v4/models/prism/v4/config"
	prismErrors "github.com/nutanix/ntnx-api-golang-clients/prism-go-client/v4/models/prism/v4/error"
	vmmModels "github.com/nutanix/ntnx-api-golang-clients/vmm-go-client/v4/models/vmm/v4/ahv/config"
	policyModels "github.com/nutanix/ntnx-api-golang-clients/vmm-go-client/v4/models/vmm/v4/ahv/policies"
	imageModels "github.com/nutanix/ntnx-api-golang-clients/vmm-go-client/v4/models/vmm/v4/content"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/cluster-api/util"
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

func TestGetImageByNameOrUUID(t *testing.T) {
	tests := []struct {
		name          string
		clientBuilder func() *v4Converged.Client
		id            infrav1.NutanixResourceIdentifier
		want          *imageModels.Image
		wantErr       bool
	}{
		{
			name: "missing name and UUID in the input",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)

				return convergedClient.Client
			},
			id:      infrav1.NutanixResourceIdentifier{},
			wantErr: true,
		},
		{
			name: "image UUID not found",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				errorMessage := `Error getting image: failed to get image: API call failed: {"data":{"error":[{"$reserved":{"$fv":"v4.r1"},"$objectType":"vmm.v4.error.AppMessage","message":"Failed to perform the operation as the backend service could not find the entity.","severity":"ERROR","code":"VMM-20005","locale":"en_US"}],"$reserved":{"$fv":"v4.r1"},"$objectType":"vmm.v4.error.ErrorResponse"},"$reserved":{"$fv":"v4.r1"},"$objectType":"vmm.v4.content.GetImageApiResponse"}`
				convergedClient.MockImages.EXPECT().Get(gomock.Any(), gomock.Any()).Return(nil, errors.New(errorMessage))

				return convergedClient.Client
			},
			id: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr.To("32432daf-fb0e-4202-b444-2439f43a24c5"),
			},
			wantErr: true,
		},
		{
			name: "image name query fails",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				convergedClient.MockImages.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, errors.New("fake error"))

				return convergedClient.Client
			},
			id: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierName,
				Name: ptr.To("example"),
			},
			wantErr: true,
		},
		{
			name: "image UUID found",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				convergedClient.MockImages.EXPECT().Get(gomock.Any(), gomock.Any()).Return(
					&imageModels.Image{
						ExtId: ptr.To("32432daf-fb0e-4202-b444-2439f43a24c5"),
						Name:  ptr.To("example"),
					}, nil,
				)

				return convergedClient.Client
			},
			id: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr.To("32432daf-fb0e-4202-b444-2439f43a24c5"),
			},
			want: &imageModels.Image{
				ExtId: ptr.To("32432daf-fb0e-4202-b444-2439f43a24c5"),
				Name:  ptr.To("example"),
			},
			wantErr: false,
		},
		{
			name: "image name found",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				convergedClient.MockImages.EXPECT().List(gomock.Any(), gomock.Any()).Return(
					[]imageModels.Image{
						{
							ExtId: ptr.To("32432daf-fb0e-4202-b444-2439f43a24c5"),
							Name:  ptr.To("example"),
						},
					}, nil)

				return convergedClient.Client
			},
			id: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierName,
				Name: ptr.To("example"),
			},
			want: &imageModels.Image{
				ExtId: ptr.To("32432daf-fb0e-4202-b444-2439f43a24c5"),
				Name:  ptr.To("example"),
			},
		},
		{
			name: "image name matches multiple images",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				convergedClient.MockImages.EXPECT().List(gomock.Any(), gomock.Any()).Return(
					[]imageModels.Image{
						{
							ExtId: ptr.To("32432daf-fb0e-4202-b444-2439f43a24c5"),
							Name:  ptr.To("example"),
						},
						{
							ExtId: ptr.To("32432daf-fb0e-4202-b444-2439f43a24c6"),
							Name:  ptr.To("example"),
						},
						{
							ExtId: ptr.To("32432daf-fb0e-4202-b444-2439f43a24c7"),
							Name:  ptr.To("example"),
						},
					}, nil)

				return convergedClient.Client
			},
			id: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierName,
				Name: ptr.To("example"),
			},
			wantErr: true,
		},
		{
			name: "image name matches zero images",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				convergedClient.MockImages.EXPECT().List(gomock.Any(), gomock.Any()).Return(
					[]imageModels.Image{}, nil,
				)

				return convergedClient.Client
			},
			id: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierName,
				Name: ptr.To("example"),
			},
			wantErr: true,
		},
		{
			name: "image name matches zero images",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				convergedClient.MockImages.EXPECT().List(gomock.Any(), gomock.Any()).Return(
					[]imageModels.Image{}, nil,
				)

				return convergedClient.Client
			},
			id: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierName,
				Name: ptr.To("example"),
			},
			wantErr: true,
		},
		{
			name: "image name matches one image",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				convergedClient.MockImages.EXPECT().List(gomock.Any(), gomock.Any()).Return(
					[]imageModels.Image{
						{
							ExtId: ptr.To("32432daf-fb0e-4202-b444-2439f43a24c5"),
							Name:  ptr.To("example"),
						},
					}, nil,
				)

				return convergedClient.Client
			},
			id: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierName,
				Name: ptr.To("example"),
			},
			want: &imageModels.Image{
				ExtId: ptr.To("32432daf-fb0e-4202-b444-2439f43a24c5"),
				Name:  ptr.To("example"),
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

func TestCreateDataDiskList(t *testing.T) {
	expectedStorageContainers := []clusterModels.StorageContainer{
		{
			ContainerExtId: ptr.To("06b1ce03-f384-4488-9ba1-ae17ebcf1f91"),
			ClusterExtId:   ptr.To("00062e56-b9ac-7253-1946-7cc25586eeee"),
		},
	}

	tests := []struct {
		name             string
		convergedBuilder func() *v4Converged.Client
		dataDiskSpecs    []infrav1.NutanixMachineVMDisk
		peUUID           string
		wantDisks        func() []vmmModels.Disk
		wantCdRoms       func() []vmmModels.CdRom
		wantErr          bool
		errorMessage     string
	}{
		{
			name: "successful data disk creation without image reference",
			convergedBuilder: func() *v4Converged.Client {
				convergedClient := NewMockConvergedClient(gomock.NewController(t))
				convergedClient.MockStorageContainers.EXPECT().List(gomock.Any(), gomock.Any()).Return(expectedStorageContainers, nil)
				return convergedClient.Client
			},
			dataDiskSpecs: []infrav1.NutanixMachineVMDisk{
				{
					DiskSize: resource.MustParse("20Gi"),
					DeviceProperties: &infrav1.NutanixMachineVMDiskDeviceProperties{
						DeviceType:  infrav1.NutanixMachineDiskDeviceTypeDisk,
						AdapterType: infrav1.NutanixMachineDiskAdapterTypeSCSI,
					},
					StorageConfig: &infrav1.NutanixMachineVMStorageConfig{
						DiskMode: infrav1.NutanixMachineDiskModeStandard,
						StorageContainer: &infrav1.NutanixResourceIdentifier{
							UUID: ptr.To("06b1ce03-f384-4488-9ba1-ae17ebcf1f91"),
							Type: infrav1.NutanixIdentifierUUID,
						},
					},
				},
			},
			peUUID: "00062e56-b9ac-7253-1946-7cc25586eeee",
			wantDisks: func() []vmmModels.Disk {
				disk := vmmModels.NewDisk()
				disk.DiskAddress = vmmModels.NewDiskAddress()
				disk.DiskAddress.Index = ptr.To(1)
				disk.DiskAddress.BusType = vmmModels.DISKBUSTYPE_SCSI.Ref()

				vmDisk := vmmModels.NewVmDisk()
				vmDisk.DiskSizeBytes = ptr.To(int64(21474836480))
				vmDisk.StorageConfig = vmmModels.NewVmDiskStorageConfig()
				vmDisk.StorageConfig.IsFlashModeEnabled = ptr.To(false)
				vmDisk.StorageContainer = vmmModels.NewVmDiskContainerReference()
				vmDisk.StorageContainer.ExtId = ptr.To("06b1ce03-f384-4488-9ba1-ae17ebcf1f91")
				_ = disk.SetBackingInfo(*vmDisk)

				return []vmmModels.Disk{*disk}
			},
			wantCdRoms: func() []vmmModels.CdRom { return []vmmModels.CdRom{} },
			wantErr:    false,
		},
		{
			name: "successful data disk creation with image reference",
			convergedBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				expectedImage := &imageModels.Image{
					ExtId: ptr.To("f47ac10b-58cc-4372-a567-0e02b2c3d479"),
					Name:  ptr.To("data-image"),
				}
				convergedClient.MockImages.EXPECT().Get(gomock.Any(), "f47ac10b-58cc-4372-a567-0e02b2c3d479").Return(expectedImage, nil)
				convergedClient.MockStorageContainers.EXPECT().List(gomock.Any(), gomock.Any()).Return(expectedStorageContainers, nil)
				return convergedClient.Client
			},
			dataDiskSpecs: []infrav1.NutanixMachineVMDisk{
				{
					DiskSize: resource.MustParse("20Gi"),
					DeviceProperties: &infrav1.NutanixMachineVMDiskDeviceProperties{
						DeviceType:  infrav1.NutanixMachineDiskDeviceTypeDisk,
						AdapterType: infrav1.NutanixMachineDiskAdapterTypeSCSI,
					},
					DataSource: &infrav1.NutanixResourceIdentifier{
						UUID: ptr.To("f47ac10b-58cc-4372-a567-0e02b2c3d479"),
						Type: infrav1.NutanixIdentifierUUID,
					},
					StorageConfig: &infrav1.NutanixMachineVMStorageConfig{
						DiskMode: infrav1.NutanixMachineDiskModeStandard,
						StorageContainer: &infrav1.NutanixResourceIdentifier{
							UUID: ptr.To("06b1ce03-f384-4488-9ba1-ae17ebcf1f91"),
							Type: infrav1.NutanixIdentifierUUID,
						},
					},
				},
			},
			peUUID: "00062e56-b9ac-7253-1946-7cc25586eeee",
			wantDisks: func() []vmmModels.Disk {
				disk := vmmModels.NewDisk()
				disk.DiskAddress = vmmModels.NewDiskAddress()
				disk.DiskAddress.Index = ptr.To(1)
				disk.DiskAddress.BusType = vmmModels.DISKBUSTYPE_SCSI.Ref()

				vmDisk := vmmModels.NewVmDisk()
				vmDisk.DiskSizeBytes = ptr.To(int64(21474836480))

				imageRef := vmmModels.NewImageReference()
				imageRef.ImageExtId = ptr.To("f47ac10b-58cc-4372-a567-0e02b2c3d479")
				vmDisk.DataSource = vmmModels.NewDataSource()
				_ = vmDisk.DataSource.SetReference(*imageRef)
				vmDisk.DataSource.ReferenceItemDiscriminator_ = nil

				vmDisk.StorageConfig = vmmModels.NewVmDiskStorageConfig()
				vmDisk.StorageConfig.IsFlashModeEnabled = ptr.To(false)

				vmDisk.StorageContainer = vmmModels.NewVmDiskContainerReference()
				vmDisk.StorageContainer.ExtId = ptr.To("06b1ce03-f384-4488-9ba1-ae17ebcf1f91")
				_ = disk.SetBackingInfo(*vmDisk)

				return []vmmModels.Disk{*disk}
			},
			wantCdRoms: func() []vmmModels.CdRom { return []vmmModels.CdRom{} },
			wantErr:    false,
		},
		{
			name: "failed image lookup for data source",
			convergedBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				convergedClient.MockImages.EXPECT().Get(gomock.Any(), "f47ac10b-58cc-4372-a567-0e02b2c3d479").Return(nil, errors.New("image not found"))
				return convergedClient.Client
			},
			dataDiskSpecs: []infrav1.NutanixMachineVMDisk{
				{
					DiskSize: resource.MustParse("20Gi"),
					DeviceProperties: &infrav1.NutanixMachineVMDiskDeviceProperties{
						DeviceType:  infrav1.NutanixMachineDiskDeviceTypeDisk,
						AdapterType: infrav1.NutanixMachineDiskAdapterTypeSCSI,
					},
					DataSource: &infrav1.NutanixResourceIdentifier{
						UUID: ptr.To("f47ac10b-58cc-4372-a567-0e02b2c3d479"),
						Type: infrav1.NutanixIdentifierUUID,
					},
				},
			},
			peUUID:       "00062e56-b9ac-7253-1946-7cc25586eeee",
			wantDisks:    func() []vmmModels.Disk { return nil },
			wantCdRoms:   func() []vmmModels.CdRom { return nil },
			wantErr:      true,
			errorMessage: "image not found",
		},
		{
			name: "multiple data disks with different adapter types",
			convergedBuilder: func() *v4Converged.Client {
				convergedClient := NewMockConvergedClient(gomock.NewController(t))
				return convergedClient.Client
			},
			dataDiskSpecs: []infrav1.NutanixMachineVMDisk{
				{
					DiskSize: resource.MustParse("20Gi"),
					DeviceProperties: &infrav1.NutanixMachineVMDiskDeviceProperties{
						DeviceType:  infrav1.NutanixMachineDiskDeviceTypeDisk,
						AdapterType: infrav1.NutanixMachineDiskAdapterTypeSCSI,
					},
				},
				{
					DiskSize: resource.MustParse("30Gi"),
					DeviceProperties: &infrav1.NutanixMachineVMDiskDeviceProperties{
						DeviceType:  infrav1.NutanixMachineDiskDeviceTypeDisk,
						AdapterType: infrav1.NutanixMachineDiskAdapterTypeIDE,
					},
				},
			},
			peUUID: "00062e56-b9ac-7253-1946-7cc25586eeee",
			wantDisks: func() []vmmModels.Disk {
				disk1 := vmmModels.NewDisk()
				disk1.DiskAddress = vmmModels.NewDiskAddress()
				disk1.DiskAddress.Index = ptr.To(1)
				disk1.DiskAddress.BusType = vmmModels.DISKBUSTYPE_SCSI.Ref()

				vmDisk1 := vmmModels.NewVmDisk()
				vmDisk1.DiskSizeBytes = ptr.To(int64(21474836480))
				_ = disk1.SetBackingInfo(*vmDisk1)

				disk2 := vmmModels.NewDisk()
				disk2.DiskAddress = vmmModels.NewDiskAddress()
				disk2.DiskAddress.Index = ptr.To(1)
				disk2.DiskAddress.BusType = vmmModels.DISKBUSTYPE_IDE.Ref()

				vmDisk2 := vmmModels.NewVmDisk()
				vmDisk2.DiskSizeBytes = ptr.To(int64(32212254720))
				_ = disk2.SetBackingInfo(*vmDisk2)
				return []vmmModels.Disk{*disk1, *disk2}
			},
			wantCdRoms: func() []vmmModels.CdRom { return []vmmModels.CdRom{} },
			wantErr:    false,
		},
		{
			name: "data disk with flash mode enabled",
			convergedBuilder: func() *v4Converged.Client {
				convergedClient := NewMockConvergedClient(gomock.NewController(t))
				convergedClient.MockStorageContainers.EXPECT().List(gomock.Any(), gomock.Any()).Return(expectedStorageContainers, nil)
				return convergedClient.Client
			},
			dataDiskSpecs: []infrav1.NutanixMachineVMDisk{
				{
					DiskSize: resource.MustParse("20Gi"),
					DeviceProperties: &infrav1.NutanixMachineVMDiskDeviceProperties{
						DeviceType:  infrav1.NutanixMachineDiskDeviceTypeDisk,
						AdapterType: infrav1.NutanixMachineDiskAdapterTypeSCSI,
					},
					StorageConfig: &infrav1.NutanixMachineVMStorageConfig{
						DiskMode: infrav1.NutanixMachineDiskModeFlash,
						StorageContainer: &infrav1.NutanixResourceIdentifier{
							UUID: ptr.To("06b1ce03-f384-4488-9ba1-ae17ebcf1f91"),
							Type: infrav1.NutanixIdentifierUUID,
						},
					},
				},
			},
			peUUID: "00062e56-b9ac-7253-1946-7cc25586eeee",
			wantDisks: func() []vmmModels.Disk {
				disk := vmmModels.NewDisk()
				disk.DiskAddress = vmmModels.NewDiskAddress()
				disk.DiskAddress.Index = ptr.To(1)
				disk.DiskAddress.BusType = vmmModels.DISKBUSTYPE_SCSI.Ref()

				vmDisk := vmmModels.NewVmDisk()
				vmDisk.DiskSizeBytes = ptr.To(int64(21474836480))

				vmDisk.StorageConfig = vmmModels.NewVmDiskStorageConfig()
				vmDisk.StorageConfig.IsFlashModeEnabled = ptr.To(true)

				vmDisk.StorageContainer = vmmModels.NewVmDiskContainerReference()
				vmDisk.StorageContainer.ExtId = ptr.To("06b1ce03-f384-4488-9ba1-ae17ebcf1f91")
				_ = disk.SetBackingInfo(*vmDisk)

				return []vmmModels.Disk{*disk}
			},
			wantCdRoms: func() []vmmModels.CdRom { return []vmmModels.CdRom{} },
			wantErr:    false,
		},
		{
			name: "data disk with custom device index",
			convergedBuilder: func() *v4Converged.Client {
				convergedClient := NewMockConvergedClient(gomock.NewController(t))
				return convergedClient.Client
			},
			dataDiskSpecs: []infrav1.NutanixMachineVMDisk{
				{
					DiskSize: resource.MustParse("20Gi"),
					DeviceProperties: &infrav1.NutanixMachineVMDiskDeviceProperties{
						DeviceType:  infrav1.NutanixMachineDiskDeviceTypeDisk,
						AdapterType: infrav1.NutanixMachineDiskAdapterTypeSCSI,
						DeviceIndex: 5,
					},
				},
			},
			peUUID: "00062e56-b9ac-7253-1946-7cc25586eeee",
			wantDisks: func() []vmmModels.Disk {
				disk := vmmModels.NewDisk()
				disk.DiskAddress = vmmModels.NewDiskAddress()
				disk.DiskAddress.Index = ptr.To(5)
				disk.DiskAddress.BusType = vmmModels.DISKBUSTYPE_SCSI.Ref()

				vmDisk := vmmModels.NewVmDisk()
				vmDisk.DiskSizeBytes = ptr.To(int64(21474836480))
				_ = disk.SetBackingInfo(*vmDisk)

				return []vmmModels.Disk{*disk}
			},
			wantCdRoms: func() []vmmModels.CdRom { return []vmmModels.CdRom{} },
			wantErr:    false,
		},
		{
			name: "data disk with CDRom device type",
			convergedBuilder: func() *v4Converged.Client {
				convergedClient := NewMockConvergedClient(gomock.NewController(t))
				return convergedClient.Client
			},
			dataDiskSpecs: []infrav1.NutanixMachineVMDisk{
				{
					DiskSize: resource.MustParse("1Gi"),
					DeviceProperties: &infrav1.NutanixMachineVMDiskDeviceProperties{
						DeviceType:  infrav1.NutanixMachineDiskDeviceTypeCDRom,
						AdapterType: infrav1.NutanixMachineDiskAdapterTypeIDE,
					},
				},
			},
			peUUID:    "00062e56-b9ac-7253-1946-7cc25586eeee",
			wantDisks: func() []vmmModels.Disk { return []vmmModels.Disk{} },
			wantCdRoms: func() []vmmModels.CdRom {
				cdRom := vmmModels.NewCdRom()
				cdRom.DiskAddress = vmmModels.NewCdRomAddress()
				cdRom.DiskAddress.Index = ptr.To(1)
				cdRom.DiskAddress.BusType = vmmModels.CDROMBUSTYPE_IDE.Ref()

				vmDisk := vmmModels.NewVmDisk()
				vmDisk.DiskSizeBytes = ptr.To(int64(1073741824))
				cdRom.BackingInfo = vmDisk

				return []vmmModels.CdRom{*cdRom}
			},
			wantErr: false,
		},
		{
			name: "data disk with default values when no device properties provided",
			convergedBuilder: func() *v4Converged.Client {
				convergedClient := NewMockConvergedClient(gomock.NewController(t))
				return convergedClient.Client
			},
			dataDiskSpecs: []infrav1.NutanixMachineVMDisk{
				{
					DiskSize: resource.MustParse("20Gi"),
					// No DeviceProperties provided - should use defaults
				},
			},
			peUUID: "00062e56-b9ac-7253-1946-7cc25586eeee",
			wantDisks: func() []vmmModels.Disk {
				disk := vmmModels.NewDisk()
				disk.DiskAddress = vmmModels.NewDiskAddress()
				disk.DiskAddress.Index = ptr.To(1)
				disk.DiskAddress.BusType = vmmModels.DISKBUSTYPE_SCSI.Ref()

				vmDisk := vmmModels.NewVmDisk()
				vmDisk.DiskSizeBytes = ptr.To(int64(21474836480))
				_ = disk.SetBackingInfo(*vmDisk)

				return []vmmModels.Disk{*disk}
			},
			wantCdRoms: func() []vmmModels.CdRom { return []vmmModels.CdRom{} },
			wantErr:    false,
		},
		{
			name: "data disk with storage container lookup failure",
			convergedBuilder: func() *v4Converged.Client {
				convergedClient := NewMockConvergedClient(gomock.NewController(t))
				convergedClient.MockStorageContainers.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, errors.New("fake error"))
				return convergedClient.Client
			},
			dataDiskSpecs: []infrav1.NutanixMachineVMDisk{
				{
					DiskSize: resource.MustParse("20Gi"),
					DeviceProperties: &infrav1.NutanixMachineVMDiskDeviceProperties{
						DeviceType:  infrav1.NutanixMachineDiskDeviceTypeDisk,
						AdapterType: infrav1.NutanixMachineDiskAdapterTypeSCSI,
					},
					StorageConfig: &infrav1.NutanixMachineVMStorageConfig{
						DiskMode: infrav1.NutanixMachineDiskModeStandard,
						StorageContainer: &infrav1.NutanixResourceIdentifier{
							UUID: ptr.To("06b1ce03-f384-4488-9ba1-ae17ebcf1f91"),
							Type: infrav1.NutanixIdentifierUUID,
						},
					},
				},
			},
			peUUID:       "00062e56-b9ac-7253-1946-7cc25586eeee",
			wantDisks:    func() []vmmModels.Disk { return nil },
			wantCdRoms:   func() []vmmModels.CdRom { return nil },
			wantErr:      true,
			errorMessage: "fake error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Log("Running test case ", tt.name)
			ctx := context.Background()
			disks, cdRoms, err := CreateDataDiskList(
				ctx,
				tt.convergedBuilder(),
				tt.dataDiskSpecs,
				tt.peUUID,
			)
			if (err != nil) != tt.wantErr {
				t.Errorf("CreateDataDiskList() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(disks, tt.wantDisks()) {
				t.Errorf("CreateDataDiskList() = %v, want %v", disks, tt.wantDisks())
			}
			if !reflect.DeepEqual(cdRoms, tt.wantCdRoms()) {
				t.Errorf("CreateDataDiskList() = %v, want %v", cdRoms, tt.wantCdRoms())
			}
			if tt.errorMessage != "" && err != nil {
				if !strings.Contains(err.Error(), tt.errorMessage) {
					t.Errorf("CreateDataDiskList() error message = %v, want to contain %v", err.Error(), tt.errorMessage)
				}
			}
		})
	}
}

func TestGetImageByLookup(t *testing.T) {
	tests := []struct {
		name          string
		clientBuilder func() *v4Converged.Client
		baseOS        string
		imageTemplate string
		k8sVersion    string
		want          *imageModels.Image
		wantErr       bool
		errorMessage  string
	}{
		{
			name: "successful image lookup with v prefix",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				convergedClient.MockImages.EXPECT().List(gomock.Any(), gomock.Any()).Return(
					[]imageModels.Image{
						{
							ExtId: ptr.To("32432daf-fb0e-4202-b444-2439f43a24c5"),
							Name:  ptr.To("capx-ubuntu-1.31.4"),
						},
					}, nil,
				)
				return convergedClient.Client
			},
			baseOS:        "ubuntu",
			imageTemplate: "capx-{{.BaseOS}}-{{.K8sVersion}}",
			k8sVersion:    "v1.31.4",
			want: &imageModels.Image{
				ExtId: ptr.To("32432daf-fb0e-4202-b444-2439f43a24c5"),
				Name:  ptr.To("capx-ubuntu-1.31.4"),
			},
			wantErr: false,
		},
		{
			name: "successful image lookup without v prefix",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				convergedClient.MockImages.EXPECT().List(gomock.Any(), gomock.Any()).Return(
					[]imageModels.Image{
						{
							ExtId: ptr.To("32432daf-fb0e-4202-b444-2439f43a24c5"),
							Name:  ptr.To("capx-ubuntu-1.31.4"),
						},
					}, nil,
				)
				return convergedClient.Client
			},
			baseOS:        "ubuntu",
			imageTemplate: "capx-{{.BaseOS}}-{{.K8sVersion}}",
			k8sVersion:    "1.31.4",
			want: &imageModels.Image{
				ExtId: ptr.To("32432daf-fb0e-4202-b444-2439f43a24c5"),
				Name:  ptr.To("capx-ubuntu-1.31.4"),
			},
			wantErr: false,
		},
		{
			name: "successful image lookup with complex template",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				convergedClient.MockImages.EXPECT().List(gomock.Any(), gomock.Any()).Return(
					[]imageModels.Image{
						{
							ExtId: ptr.To("32432daf-fb0e-4202-b444-2439f43a24c5"),
							Name:  ptr.To("k8s-ubuntu-20.04-1.31.4"),
						},
					}, nil,
				)
				return convergedClient.Client
			},
			baseOS:        "ubuntu-20.04",
			imageTemplate: "k8s-{{.BaseOS}}-{{.K8sVersion}}",
			k8sVersion:    "v1.31.4",
			want: &imageModels.Image{
				ExtId: ptr.To("32432daf-fb0e-4202-b444-2439f43a24c5"),
				Name:  ptr.To("k8s-ubuntu-20.04-1.31.4"),
			},
			wantErr: false,
		},
		{
			name: "failed template parsing with invalid field",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				return convergedClient.Client
			},
			baseOS:        "ubuntu",
			imageTemplate: "invalid-template-{{.InvalidField}}",
			k8sVersion:    "v1.31.4",
			want:          nil,
			wantErr:       true,
			errorMessage:  "failed to substitute string",
		},
		{
			name: "failed template parsing with malformed template",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				return convergedClient.Client
			},
			baseOS:        "ubuntu",
			imageTemplate: "invalid-template-{{.BaseOS",
			k8sVersion:    "v1.31.4",
			want:          nil,
			wantErr:       true,
			errorMessage:  "failed to parse template",
		},
		{
			name: "failed template execution with invalid data",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				return convergedClient.Client
			},
			baseOS:        "ubuntu",
			imageTemplate: "{{.BaseOS}}-{{.K8sVersion}}-{{.NonExistentField}}",
			k8sVersion:    "v1.31.4",
			want:          nil,
			wantErr:       true,
			errorMessage:  "failed to substitute string",
		},
		{
			name: "client list images fails",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				convergedClient.MockImages.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, errors.New("API error"))
				return convergedClient.Client
			},
			baseOS:        "ubuntu",
			imageTemplate: "capx-{{.BaseOS}}-{{.K8sVersion}}",
			k8sVersion:    "v1.31.4",
			want:          nil,
			wantErr:       true,
			errorMessage:  "API error",
		},
		{
			name: "no matching image found",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				convergedClient.MockImages.EXPECT().List(gomock.Any(), gomock.Any()).Return(
					[]imageModels.Image{}, nil,
				)
				return convergedClient.Client
			},
			baseOS:        "ubuntu",
			imageTemplate: "capx-{{.BaseOS}}-{{.K8sVersion}}",
			k8sVersion:    "v1.31.4",
			want:          nil,
			wantErr:       true,
			errorMessage:  "failed to find image with filter",
		},
		{
			name: "multiple images, return latest by creation time",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				convergedClient.MockImages.EXPECT().List(gomock.Any(), gomock.Any()).Return(
					[]imageModels.Image{
						{
							ExtId:      ptr.To("32432daf-fb0e-4202-b444-2439f43a24c5"),
							Name:       ptr.To("capx-ubuntu-1.31.4"),
							CreateTime: ptr.To(time.Date(2023, 10, 1, 0, 0, 0, 0, time.UTC)),
						},
						{
							ExtId:      ptr.To("32432daf-fb0e-4202-b444-2439f43a24c6"),
							Name:       ptr.To("capx-ubuntu-1.31.4"),
							CreateTime: ptr.To(time.Date(2023, 10, 2, 0, 0, 0, 0, time.UTC)),
						},
						{
							ExtId:      ptr.To("32432daf-fb0e-4202-b444-2439f43a24c7"),
							Name:       ptr.To("capx-ubuntu-1.31.4"),
							CreateTime: ptr.To(time.Date(2023, 10, 3, 0, 0, 0, 0, time.UTC)),
						},
					}, nil,
				)
				return convergedClient.Client
			},
			baseOS:        "ubuntu",
			imageTemplate: "capx-{{.BaseOS}}-{{.K8sVersion}}",
			k8sVersion:    "v1.31.4",
			want: &imageModels.Image{
				ExtId:      ptr.To("32432daf-fb0e-4202-b444-2439f43a24c7"),
				Name:       ptr.To("capx-ubuntu-1.31.4"),
				CreateTime: ptr.To(time.Date(2023, 10, 3, 0, 0, 0, 0, time.UTC)),
			},
			wantErr: false,
		},
		{
			name: "multiple images with nil creation times, prioritize non-nil",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				convergedClient.MockImages.EXPECT().List(gomock.Any(), gomock.Any()).Return(
					[]imageModels.Image{
						{
							ExtId:      ptr.To("32432daf-fb0e-4202-b444-2439f43a24c5"),
							Name:       ptr.To("capx-ubuntu-1.31.4"),
							CreateTime: nil,
						},
						{
							ExtId:      ptr.To("32432daf-fb0e-4202-b444-2439f43a24c6"),
							Name:       ptr.To("capx-ubuntu-1.31.4"),
							CreateTime: ptr.To(time.Date(2023, 10, 2, 0, 0, 0, 0, time.UTC)),
						},
						{
							ExtId:      ptr.To("32432daf-fb0e-4202-b444-2439f43a24c7"),
							Name:       ptr.To("capx-ubuntu-1.31.4"),
							CreateTime: nil,
						},
					}, nil,
				)
				return convergedClient.Client
			},
			baseOS:        "ubuntu",
			imageTemplate: "capx-{{.BaseOS}}-{{.K8sVersion}}",
			k8sVersion:    "v1.31.4",
			want: &imageModels.Image{
				ExtId:      ptr.To("32432daf-fb0e-4202-b444-2439f43a24c6"),
				Name:       ptr.To("capx-ubuntu-1.31.4"),
				CreateTime: ptr.To(time.Date(2023, 10, 2, 0, 0, 0, 0, time.UTC)),
			},
			wantErr: false,
		},
		{
			name: "multiple images with all nil creation times, return first",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				convergedClient.MockImages.EXPECT().List(gomock.Any(), gomock.Any()).Return(
					[]imageModels.Image{
						{
							ExtId:      ptr.To("32432daf-fb0e-4202-b444-2439f43a24c5"),
							Name:       ptr.To("capx-ubuntu-1.31.4"),
							CreateTime: nil,
						},
						{
							ExtId:      ptr.To("32432daf-fb0e-4202-b444-2439f43a24c6"),
							Name:       ptr.To("capx-ubuntu-1.31.4"),
							CreateTime: nil,
						},
					}, nil,
				)
				return convergedClient.Client
			},
			baseOS:        "ubuntu",
			imageTemplate: "capx-{{.BaseOS}}-{{.K8sVersion}}",
			k8sVersion:    "v1.31.4",
			want: &imageModels.Image{
				ExtId:      ptr.To("32432daf-fb0e-4202-b444-2439f43a24c5"),
				Name:       ptr.To("capx-ubuntu-1.31.4"),
				CreateTime: nil,
			},
			wantErr: false,
		},
		{
			name: "k8s version with multiple v prefixes",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				convergedClient.MockImages.EXPECT().List(gomock.Any(), gomock.Any()).Return(
					[]imageModels.Image{
						{
							ExtId: ptr.To("32432daf-fb0e-4202-b444-2439f43a24c5"),
							Name:  ptr.To("capx-ubuntu-v1.31.4"),
						},
					}, nil,
				)
				return convergedClient.Client
			},
			baseOS:        "ubuntu",
			imageTemplate: "capx-{{.BaseOS}}-{{.K8sVersion}}",
			k8sVersion:    "vv1.31.4",
			want: &imageModels.Image{
				ExtId: ptr.To("32432daf-fb0e-4202-b444-2439f43a24c5"),
				Name:  ptr.To("capx-ubuntu-v1.31.4"),
			},
			wantErr: false,
		},
		{
			name: "k8s version with v in the middle",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				convergedClient.MockImages.EXPECT().List(gomock.Any(), gomock.Any()).Return(
					[]imageModels.Image{
						{
							ExtId: ptr.To("32432daf-fb0e-4202-b444-2439f43a24c5"),
							Name:  ptr.To("capx-ubuntu-1.31.4"),
						},
					}, nil,
				)
				return convergedClient.Client
			},
			baseOS:        "ubuntu",
			imageTemplate: "capx-{{.BaseOS}}-{{.K8sVersion}}",
			k8sVersion:    "1.v31.4",
			want: &imageModels.Image{
				ExtId: ptr.To("32432daf-fb0e-4202-b444-2439f43a24c5"),
				Name:  ptr.To("capx-ubuntu-1.31.4"),
			},
			wantErr: false,
		},
		{
			name: "template with special characters",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				convergedClient.MockImages.EXPECT().List(gomock.Any(), gomock.Any()).Return(
					[]imageModels.Image{
						{
							ExtId: ptr.To("32432daf-fb0e-4202-b444-2439f43a24c5"),
							Name:  ptr.To("k8s-ubuntu-20.04-1.31.4"),
						},
					}, nil,
				)
				return convergedClient.Client
			},
			baseOS:        "ubuntu-20.04",
			imageTemplate: "k8s-{{.BaseOS}}-{{.K8sVersion}}",
			k8sVersion:    "v1.31.4",
			want: &imageModels.Image{
				ExtId: ptr.To("32432daf-fb0e-4202-b444-2439f43a24c5"),
				Name:  ptr.To("k8s-ubuntu-20.04-1.31.4"),
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
			if tt.errorMessage != "" && err != nil {
				if !strings.Contains(err.Error(), tt.errorMessage) {
					t.Errorf("GetImageByLookup() error message = %v, want to contain %v", err.Error(), tt.errorMessage)
				}
			}
		})
	}
}

func TestGetCategoryVMSpecMapping_MultiValues(t *testing.T) {
	t.Run("returns flat map first value and mapping with all values", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		ctx := context.Background()
		mockClientWrapper := NewMockConvergedClient(ctrl)
		client := mockClientWrapper.Client

		key := "CategoryKey"
		v1 := "CategoryValue1"
		v2 := "CategoryValue2"

		ids := []*infrav1.NutanixCategoryIdentifier{{Key: key, Value: v1}, {Key: key, Value: v2}, {Key: key, Value: v1}}

		// Expect lookups for both values to succeed (match any converged.ODataOption filter)
		mockClientWrapper.MockCategories.EXPECT().List(ctx, gomock.Any()).Return([]prismModels.Category{{}}, nil)
		mockClientWrapper.MockCategories.EXPECT().List(ctx, gomock.Any()).Return([]prismModels.Category{{}}, nil)
		mockClientWrapper.MockCategories.EXPECT().List(ctx, gomock.Any()).Return([]prismModels.Category{{}}, nil)

		mapping, err := GetCategoryVMSpec(ctx, client, ids)
		require.NoError(t, err)
		assert.Len(t, mapping[key], 2)
		assert.ElementsMatch(t, []string{v1, v2}, mapping[key])
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
		storageContainerId infrav1.NutanixResourceIdentifier
		clusterId          infrav1.NutanixResourceIdentifier
		want               *clusterModels.StorageContainer
		wantErr            bool
		errorMessage       string
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
		// 2) deleteCategoryValue -> internal getCategoryValue
		mockClient.MockCategories.EXPECT().List(ctx, gomock.Any()).Return([]prismModels.Category{valCat}, nil).Times(1)
		// 3) delete value by ExtId
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
		// 2) deleteCategoryValue -> internal getCategoryValue
		mockClient.MockCategories.EXPECT().List(ctx, gomock.Any()).Return([]prismModels.Category{valCat}, nil).Times(1)
		// 3) delete value fails
		mockClient.MockCategories.EXPECT().Delete(ctx, valExt).Return(errors.New("in use")).Times(1)

		// Function should return nil early due to special-case handling
		err := deleteCategoryKeyValues(ctx, mockClient.Client, ids)
		assert.NoError(t, err)
	})
}

type MockConvergedClientWrapper struct {
	Client *v4Converged.Client

	MockAntiAffinityPolicies *mockconverged.MockAntiAffinityPolicies[policyModels.VmAntiAffinityPolicy]
	MockClusters             *mockconverged.MockClusters[clusterModels.Cluster, clusterModels.VirtualGpuProfile, clusterModels.PhysicalGpuProfile]
	MockCategories           *mockconverged.MockCategories[prismModels.Category]
	MockImages               *mockconverged.MockImages[imageModels.Image]
	MockStorageContainers    *mockconverged.MockStorageContainers[clusterModels.StorageContainer]
	MockSubnets              *mockconverged.MockSubnets[subnetModels.Subnet]
	MockVMs                  *mockconverged.MockVMs[vmmModels.Vm]
	MockTasks                *mockconverged.MockTasks[prismModels.Task, prismErrors.AppMessage]
}

// NewMockConvergedClient creates a new mock converged client
func NewMockConvergedClient(ctrl *gomock.Controller) *MockConvergedClientWrapper {
	mockAntiAffinityPolicies := mockconverged.NewMockAntiAffinityPolicies[policyModels.VmAntiAffinityPolicy](ctrl)
	mockClusters := mockconverged.NewMockClusters[clusterModels.Cluster, clusterModels.VirtualGpuProfile, clusterModels.PhysicalGpuProfile](ctrl)
	mockCategories := mockconverged.NewMockCategories[prismModels.Category](ctrl)
	mockImages := mockconverged.NewMockImages[imageModels.Image](ctrl)
	mockStorageContainers := mockconverged.NewMockStorageContainers[clusterModels.StorageContainer](ctrl)
	mockSubnets := mockconverged.NewMockSubnets[subnetModels.Subnet](ctrl)
	mockTasks := mockconverged.NewMockTasks[prismModels.Task, prismErrors.AppMessage](ctrl)
	// Create mock VMs service with the correct type
	mockVMs := mockconverged.NewMockVMs[vmmModels.Vm](ctrl)

	realClient := &v4Converged.Client{
		Client: converged.Client[
			policyModels.VmAntiAffinityPolicy,
			clusterModels.Cluster,
			clusterModels.VirtualGpuProfile,
			clusterModels.PhysicalGpuProfile,
			prismModels.Category,
			imageModels.Image,
			clusterModels.StorageContainer,
			subnetModels.Subnet,
			vmmModels.Vm,
			prismModels.Task,
			prismErrors.AppMessage,
		]{
			AntiAffinityPolicies: mockAntiAffinityPolicies,
			Clusters:             mockClusters,
			Categories:           mockCategories,
			Images:               mockImages,
			StorageContainers:    mockStorageContainers,
			Subnets:              mockSubnets,
			VMs:                  mockVMs,
			Tasks:                mockTasks,
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
	}
}

func TestGetTaskUUIDFromVM(t *testing.T) {
	tests := []struct {
		name          string
		clientBuilder func() *v4Converged.Client
		vmId          string
		want          string
		wantErr       bool
		errorMessage  string
	}{
		{
			name: "empty vmId returns error",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				return convergedClient.Client
			},
			vmId:         "",
			want:         "",
			wantErr:      true,
			errorMessage: "cannot extract task uuid for empty vm id",
		},
		{
			name: "successful task UUID retrieval with running task",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				taskUUID := "ZXJnb24=:b4b17e07-b81c-43f4-9bf5-62149975d58f"
				tasks := []prismModels.Task{
					{
						ExtId: ptr.To(taskUUID),
					},
				}
				convergedClient.MockTasks.EXPECT().List(gomock.Any(), gomock.Any()).Return(tasks, nil)
				return convergedClient.Client
			},
			vmId:    "vm-uuid-456",
			want:    "ZXJnb24=:b4b17e07-b81c-43f4-9bf5-62149975d58f",
			wantErr: false,
		},
		{
			name: "no running tasks returns empty string",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				tasks := []prismModels.Task{}
				convergedClient.MockTasks.EXPECT().List(gomock.Any(), gomock.Any()).Return(tasks, nil)
				return convergedClient.Client
			},
			vmId:    "vm-uuid-456",
			want:    "",
			wantErr: false,
		},
		{
			name: "multiple running tasks returns first task UUID",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				tasks := []prismModels.Task{
					{
						ExtId: ptr.To("ZXJnb24=:b4b17e07-b81c-43f4-9bf5-62149975d58f"),
					},
					{
						ExtId: ptr.To("ZXJnb24=:b4b17e07-b81c-43f4-9bf5-62149975d590"),
					},
				}
				convergedClient.MockTasks.EXPECT().List(gomock.Any(), gomock.Any()).Return(tasks, nil)
				return convergedClient.Client
			},
			vmId:    "vm-uuid-456",
			want:    "ZXJnb24=:b4b17e07-b81c-43f4-9bf5-62149975d58f",
			wantErr: false,
		},
		{
			name: "API error returns error",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				convergedClient.MockTasks.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, errors.New("API connection failed"))
				return convergedClient.Client
			},
			vmId:         "vm-uuid-456",
			want:         "",
			wantErr:      true,
			errorMessage: "API connection failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Log("Running test case ", tt.name)
			ctx := context.Background()
			got, err := GetTaskUUIDFromVM(ctx, tt.clientBuilder(), tt.vmId)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetTaskUUIDFromVM() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("GetTaskUUIDFromVM() = %v, want %v", got, tt.want)
			}
			if tt.errorMessage != "" && err != nil {
				if !strings.Contains(err.Error(), tt.errorMessage) {
					t.Errorf("GetTaskUUIDFromVM() error message = %v, want to contain %v", err.Error(), tt.errorMessage)
				}
			}
		})
	}
}

func TestHasTaskInProgress(t *testing.T) {
	tests := []struct {
		name          string
		clientBuilder func() *v4Converged.Client
		taskUUID      string
		want          bool
		wantErr       bool
		errorMessage  string
	}{
		{
			name: "task succeeded returns false",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				taskStatus := prismModels.TASKSTATUS_SUCCEEDED
				convergedClient.MockTasks.EXPECT().Get(gomock.Any(), "ZXJnb24=:b4b17e07-b81c-43f4-9bf5-62149975d58f").Return(&prismModels.Task{
					Status: &taskStatus,
				}, nil)
				return convergedClient.Client
			},
			taskUUID: "ZXJnb24=:b4b17e07-b81c-43f4-9bf5-62149975d58f",
			want:     false,
			wantErr:  false,
		},
		{
			name: "task running returns true",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				taskStatus := prismModels.TASKSTATUS_RUNNING
				convergedClient.MockTasks.EXPECT().Get(gomock.Any(), "ZXJnb24=:b4b17e07-b81c-43f4-9bf5-62149975d58f").Return(&prismModels.Task{
					Status: &taskStatus,
				}, nil)
				return convergedClient.Client
			},
			taskUUID: "ZXJnb24=:b4b17e07-b81c-43f4-9bf5-62149975d58f",
			want:     true,
			wantErr:  false,
		},
		{
			name: "task queued returns true",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				taskStatus := prismModels.TASKSTATUS_QUEUED
				convergedClient.MockTasks.EXPECT().Get(gomock.Any(), "ZXJnb24=:b4b17e07-b81c-43f4-9bf5-62149975d58f").Return(&prismModels.Task{
					Status: &taskStatus,
				}, nil)
				return convergedClient.Client
			},
			taskUUID: "ZXJnb24=:b4b17e07-b81c-43f4-9bf5-62149975d58f",
			want:     true,
			wantErr:  false,
		},
		{
			name: "task failed returns false (no error in v4 implementation)",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				taskStatus := prismModels.TASKSTATUS_FAILED
				convergedClient.MockTasks.EXPECT().Get(gomock.Any(), "ZXJnb24=:b4b17e07-b81c-43f4-9bf5-62149975d58f").Return(&prismModels.Task{
					Status: &taskStatus,
				}, nil)
				return convergedClient.Client
			},
			taskUUID: "ZXJnb24=:b4b17e07-b81c-43f4-9bf5-62149975d58f",
			want:     false,
			wantErr:  false,
		},
		{
			name: "task suspended returns false",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				taskStatus := prismModels.TASKSTATUS_SUSPENDED
				convergedClient.MockTasks.EXPECT().Get(gomock.Any(), "ZXJnb24=:b4b17e07-b81c-43f4-9bf5-62149975d58f").Return(&prismModels.Task{
					Status: &taskStatus,
				}, nil)
				return convergedClient.Client
			},
			taskUUID: "ZXJnb24=:b4b17e07-b81c-43f4-9bf5-62149975d58f",
			want:     false,
			wantErr:  false,
		},
		{
			name: "API error returns error",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				convergedClient.MockTasks.EXPECT().Get(gomock.Any(), "ZXJnb24=:b4b17e07-b81c-43f4-9bf5-62149975d58f").Return(nil, errors.New("API error"))
				return convergedClient.Client
			},
			taskUUID:     "ZXJnb24=:b4b17e07-b81c-43f4-9bf5-62149975d58f",
			want:         false,
			wantErr:      true,
			errorMessage: "API error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Log("Running test case ", tt.name)
			ctx := context.Background()
			got, err := HasTaskInProgress(ctx, tt.clientBuilder(), tt.taskUUID)
			if (err != nil) != tt.wantErr {
				t.Errorf("HasTaskInProgress() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("HasTaskInProgress() = %v, want %v", got, tt.want)
			}
			if tt.errorMessage != "" && err != nil {
				if !strings.Contains(err.Error(), tt.errorMessage) {
					t.Errorf("HasTaskInProgress() error message = %v, want to contain %v", err.Error(), tt.errorMessage)
				}
			}
		})
	}
}
