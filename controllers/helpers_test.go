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
	"testing"

	credentialtypes "github.com/nutanix-cloud-native/prism-go-client/environment/credentials"
	prismclientv3 "github.com/nutanix-cloud-native/prism-go-client/v3"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/cluster-api/util"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	mockk8sclient "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/mocks/k8sclient"
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
			It("should error when passing empty failure domain name", func() {
				g.Expect(k8sClient.Create(ctx, ntnxCluster)).To(Succeed())
				_, err := GetFailureDomain("", ntnxCluster)
				Expect(err).To(HaveOccurred())
			})
			It("should error when passing nil cluster", func() {
				g.Expect(k8sClient.Create(ctx, ntnxCluster)).To(Succeed())
				_, err := GetFailureDomain(fd1Name, nil)
				Expect(err).To(HaveOccurred())
			})
			It("should error when no failure domain has been found", func() {
				g.Expect(k8sClient.Create(ctx, ntnxCluster)).To(Succeed())
				_, err := GetFailureDomain(fd1Name, ntnxCluster)
				Expect(err).To(HaveOccurred())
			})
			It("should return the correct failuredomain", func() {
				r := util.RandomString(10)
				fd1 := infrav1.NutanixFailureDomain{
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
				}
				fd2 := infrav1.NutanixFailureDomain{
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
				}
				ntnxCluster.Spec.FailureDomains = []infrav1.NutanixFailureDomain{
					fd1,
					fd2,
				}
				g.Expect(k8sClient.Create(ctx, ntnxCluster)).To(Succeed())
				fd, err := GetFailureDomain(fd2Name, ntnxCluster)
				Expect(err).ToNot(HaveOccurred())
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
