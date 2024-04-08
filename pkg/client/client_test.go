/*
Copyright 2024 Nutanix

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

package client

import (
	"context"
	_ "embed"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/nutanix-cloud-native/prism-go-client/environment/credentials"
	envTypes "github.com/nutanix-cloud-native/prism-go-client/environment/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"
	ctrl "sigs.k8s.io/controller-runtime"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
)

var (
	//go:embed testdata/validTestCredentials.json
	validTestCredentials string
	//go:embed testdata/validTestManagerCredentials.json
	validTestManagerCredentials string

	certBundleKey = "ca.crt"
	//go:embed testdata/validTestCA.pem
	validTestCA string
	//go:embed testdata/validTestManagerCA.pem
	validTestManagerCA string

	//go:embed testdata/validTestConfig.json
	validTestConfig string
	//go:embed testdata/invalidTestConfigMissingCredentialRef.json
	invalidTestConfigMissingCredentialRef string
)

var (
	testSecrets = []corev1.Secret{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "creds",
				Namespace: "test",
			},
			Data: map[string][]byte{
				credentials.KeyName: []byte(validTestCredentials),
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "capx-nutanix-creds",
				Namespace: "capx-system",
			},
			Data: map[string][]byte{
				credentials.KeyName: []byte(validTestManagerCredentials),
			},
		},
	}
	testConfigMaps = []corev1.ConfigMap{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "cm",
				Namespace: "test",
			},
			BinaryData: map[string][]byte{
				certBundleKey: []byte(validTestCA),
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "cm",
				Namespace: "capx-system",
			},
			BinaryData: map[string][]byte{
				certBundleKey: []byte(validTestManagerCA),
			},
		},
	}
)

// setup a single controller context to be shared in unit tests
// otherwise it gets prematurely closed
var controllerCtx = ctrl.SetupSignalHandler()

func Test_buildManagementEndpoint(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                       string
		helper                     *NutanixClientHelper
		nutanixCluster             *infrav1.NutanixCluster
		expectedManagementEndpoint *envTypes.ManagementEndpoint
		expectedErr                error
	}{
		{
			name: "all information set in NutanixCluster",
			helper: testHelperWithFakedInformers(testSecrets, testConfigMaps).withCustomNutanixPrismEndpointReader(
				func() (*credentials.NutanixPrismEndpoint, error) {
					return &credentials.NutanixPrismEndpoint{
						Address: "manager-endpoint",
						Port:    9440,
						CredentialRef: &credentials.NutanixCredentialReference{
							Kind:      credentials.SecretKind,
							Name:      "capx-nutanix-creds",
							Namespace: "capx-system",
						},
						AdditionalTrustBundle: &credentials.NutanixTrustBundleReference{
							Kind:      credentials.NutanixTrustBundleKindConfigMap,
							Name:      "cm",
							Namespace: "capx-system",
						},
					}, nil
				},
			),
			nutanixCluster: &infrav1.NutanixCluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: corev1.NamespaceDefault,
				},
				Spec: infrav1.NutanixClusterSpec{
					PrismCentral: &credentials.NutanixPrismEndpoint{
						Address: "cluster-endpoint",
						Port:    9440,
						CredentialRef: &credentials.NutanixCredentialReference{
							Kind:      credentials.SecretKind,
							Name:      "creds",
							Namespace: "test",
						},
						AdditionalTrustBundle: &credentials.NutanixTrustBundleReference{
							Kind:      credentials.NutanixTrustBundleKindConfigMap,
							Name:      "cm",
							Namespace: "test",
						},
					},
				},
			},
			expectedManagementEndpoint: &envTypes.ManagementEndpoint{
				ApiCredentials: envTypes.ApiCredentials{
					Username: "user",
					Password: "password",
				},
				Address: &url.URL{
					Scheme: "https",
					Host:   "cluster-endpoint:9440",
				},
				AdditionalTrustBundle: validTestCA,
			},
		},
		{
			name: "information missing in NutanixCluster, fallback to management",
			helper: testHelperWithFakedInformers(testSecrets, testConfigMaps).withCustomNutanixPrismEndpointReader(
				func() (*credentials.NutanixPrismEndpoint, error) {
					return &credentials.NutanixPrismEndpoint{
						Address: "manager-endpoint",
						Port:    9440,
						CredentialRef: &credentials.NutanixCredentialReference{
							Kind:      credentials.SecretKind,
							Name:      "capx-nutanix-creds",
							Namespace: "capx-system",
						},
						AdditionalTrustBundle: &credentials.NutanixTrustBundleReference{
							Kind:      credentials.NutanixTrustBundleKindConfigMap,
							Name:      "cm",
							Namespace: "capx-system",
						},
					}, nil
				},
			),
			nutanixCluster: &infrav1.NutanixCluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: corev1.NamespaceDefault,
				},
				Spec: infrav1.NutanixClusterSpec{},
			},
			expectedManagementEndpoint: &envTypes.ManagementEndpoint{
				ApiCredentials: envTypes.ApiCredentials{
					Username: "admin",
					Password: "adminpassword",
				},
				Address: &url.URL{
					Scheme: "https",
					Host:   "manager-endpoint:9440",
				},
				AdditionalTrustBundle: validTestManagerCA,
			},
		},
		{
			name: "NutanixCluster missing required information, should fail",
			helper: testHelperWithFakedInformers(testSecrets, testConfigMaps).withCustomNutanixPrismEndpointReader(
				func() (*credentials.NutanixPrismEndpoint, error) {
					return &credentials.NutanixPrismEndpoint{
						Address: "manager-endpoint",
						Port:    9440,
						CredentialRef: &credentials.NutanixCredentialReference{
							Kind:      credentials.SecretKind,
							Name:      "capx-nutanix-creds",
							Namespace: "capx-system",
						},
						AdditionalTrustBundle: &credentials.NutanixTrustBundleReference{
							Kind:      credentials.NutanixTrustBundleKindConfigMap,
							Name:      "cm",
							Namespace: "capx-system",
						},
					}, nil
				},
			),
			nutanixCluster: &infrav1.NutanixCluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: corev1.NamespaceDefault,
				},
				Spec: infrav1.NutanixClusterSpec{
					PrismCentral: &credentials.NutanixPrismEndpoint{
						Port: 9440,
						CredentialRef: &credentials.NutanixCredentialReference{
							Kind:      credentials.SecretKind,
							Name:      "creds",
							Namespace: "test",
						},
						AdditionalTrustBundle: &credentials.NutanixTrustBundleReference{
							Kind:      credentials.NutanixTrustBundleKindConfigMap,
							Name:      "cm",
							Namespace: "test",
						},
					},
				},
			},
			expectedErr: fmt.Errorf("error building an environment provider from NutanixCluster: %w", ErrPrismAddressNotSet),
		},
		{
			name: "could not read management configuration, should fail",
			helper: testHelperWithFakedInformers(testSecrets, testConfigMaps).withCustomNutanixPrismEndpointReader(
				func() (*credentials.NutanixPrismEndpoint, error) {
					return nil, fmt.Errorf("could not read config")
				},
			),
			nutanixCluster: &infrav1.NutanixCluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: corev1.NamespaceDefault,
				},
				Spec: infrav1.NutanixClusterSpec{},
			},
			expectedErr: fmt.Errorf("error building an environment provider from file: %w", fmt.Errorf("failed to create prism endpoint: %w", fmt.Errorf("could not read config"))),
		},
	}
	for _, tt := range tests {
		tt := tt // Capture range variable.
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			me, err := tt.helper.BuildManagementEndpoint(context.TODO(), tt.nutanixCluster)
			assert.Equal(t, tt.expectedManagementEndpoint, me)
			assert.Equal(t, tt.expectedErr, err)
		})
	}
}

func Test_buildProviderFromNutanixCluster(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                  string
		helper                *NutanixClientHelper
		nutanixCluster        *infrav1.NutanixCluster
		expectProviderToBeNil bool
		expectedErr           error
	}{
		{
			name:   "all information set",
			helper: testHelper(),
			nutanixCluster: &infrav1.NutanixCluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: corev1.NamespaceDefault,
				},
				Spec: infrav1.NutanixClusterSpec{
					PrismCentral: &credentials.NutanixPrismEndpoint{
						Address: "cluster-endpoint",
						Port:    9440,
						CredentialRef: &credentials.NutanixCredentialReference{
							Kind:      credentials.SecretKind,
							Name:      "creds",
							Namespace: "test",
						},
						AdditionalTrustBundle: &credentials.NutanixTrustBundleReference{
							Kind:      credentials.NutanixTrustBundleKindConfigMap,
							Name:      "cm",
							Namespace: "test",
						},
					},
				},
			},
			expectProviderToBeNil: false,
		},
		{
			name:   "namespace not set, should not fail",
			helper: testHelper(),
			nutanixCluster: &infrav1.NutanixCluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: corev1.NamespaceDefault,
				},
				Spec: infrav1.NutanixClusterSpec{
					PrismCentral: &credentials.NutanixPrismEndpoint{
						Address: "cluster-endpoint",
						Port:    9440,
						CredentialRef: &credentials.NutanixCredentialReference{
							Kind: credentials.SecretKind,
							Name: "creds",
						},
						AdditionalTrustBundle: &credentials.NutanixTrustBundleReference{
							Kind: credentials.NutanixTrustBundleKindConfigMap,
							Name: "cm",
						},
					},
				},
			},
			expectProviderToBeNil: false,
		},
		{
			name:   "address is empty, should fail",
			helper: testHelper(),
			nutanixCluster: &infrav1.NutanixCluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: corev1.NamespaceDefault,
				},
				Spec: infrav1.NutanixClusterSpec{
					PrismCentral: &credentials.NutanixPrismEndpoint{
						Address: "",
						Port:    9440,
						CredentialRef: &credentials.NutanixCredentialReference{
							Kind:      credentials.SecretKind,
							Name:      "creds",
							Namespace: "test",
						},
						AdditionalTrustBundle: &credentials.NutanixTrustBundleReference{
							Kind:      credentials.NutanixTrustBundleKindConfigMap,
							Name:      "cm",
							Namespace: "test",
						},
					},
				},
			},
			expectProviderToBeNil: true,
			expectedErr:           ErrPrismAddressNotSet,
		},
		{
			name:   "port is not set, should fail",
			helper: testHelper(),
			nutanixCluster: &infrav1.NutanixCluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: corev1.NamespaceDefault,
				},
				Spec: infrav1.NutanixClusterSpec{
					PrismCentral: &credentials.NutanixPrismEndpoint{
						Address: "cluster-endpoint",
						CredentialRef: &credentials.NutanixCredentialReference{
							Kind:      credentials.SecretKind,
							Name:      "creds",
							Namespace: "test",
						},
						AdditionalTrustBundle: &credentials.NutanixTrustBundleReference{
							Kind:      credentials.NutanixTrustBundleKindConfigMap,
							Name:      "cm",
							Namespace: "test",
						},
					},
				},
			},
			expectProviderToBeNil: true,
			expectedErr:           ErrPrismPortNotSet,
		},
		{
			name:   "CredentialRef is not set, should fail",
			helper: testHelper(),
			nutanixCluster: &infrav1.NutanixCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: corev1.NamespaceDefault,
				},
				Spec: infrav1.NutanixClusterSpec{
					PrismCentral: &credentials.NutanixPrismEndpoint{
						Address: "cluster-endpoint",
						Port:    9440,
						AdditionalTrustBundle: &credentials.NutanixTrustBundleReference{
							Kind:      credentials.NutanixTrustBundleKindConfigMap,
							Name:      "cm",
							Namespace: "test",
						},
					},
				},
			},
			expectProviderToBeNil: true,
			expectedErr:           fmt.Errorf("credentialRef must be set on prismCentral attribute for cluster %s in namespace %s", "test", corev1.NamespaceDefault),
		},
	}
	for _, tt := range tests {
		tt := tt // Capture range variable.
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			provider, err := tt.helper.buildProviderFromNutanixCluster(tt.nutanixCluster)
			if tt.expectProviderToBeNil {
				assert.Nil(t, provider)
			} else {
				assert.NotNil(t, provider)
			}
			assert.Equal(t, tt.expectedErr, err)
		})
	}
}

func Test_buildProviderFromFile(t *testing.T) {
	tests := []struct {
		name                  string
		helper                *NutanixClientHelper
		expectProviderToBeNil bool
		envs                  map[string]string
		expectedErr           error
	}{
		{
			name: "all information set",
			helper: testHelper().withCustomNutanixPrismEndpointReader(
				func() (*credentials.NutanixPrismEndpoint, error) {
					return &credentials.NutanixPrismEndpoint{
						Address: "manager-endpoint",
						Port:    9440,
						CredentialRef: &credentials.NutanixCredentialReference{
							Kind:      credentials.SecretKind,
							Name:      "capx-nutanix-creds",
							Namespace: "capx-system",
						},
						AdditionalTrustBundle: &credentials.NutanixTrustBundleReference{
							Kind:      credentials.NutanixTrustBundleKindConfigMap,
							Name:      "cm",
							Namespace: "capx-system",
						},
					}, nil
				},
			),
			expectProviderToBeNil: false,
		},
		{
			name: "namespace not set with env, should not fail",
			helper: testHelper().withCustomNutanixPrismEndpointReader(
				func() (*credentials.NutanixPrismEndpoint, error) {
					return &credentials.NutanixPrismEndpoint{
						Address: "manager-endpoint",
						Port:    9440,
						CredentialRef: &credentials.NutanixCredentialReference{
							Kind: credentials.SecretKind,
							Name: "capx-nutanix-creds",
						},
						AdditionalTrustBundle: &credentials.NutanixTrustBundleReference{
							Kind: credentials.NutanixTrustBundleKindConfigMap,
							Name: "cm",
						},
					}, nil
				},
			),
			envs:                  map[string]string{capxNamespaceKey: "test"},
			expectProviderToBeNil: false,
		},
		{
			name: "credentialRef namespace not set, should fail",
			helper: testHelper().withCustomNutanixPrismEndpointReader(
				func() (*credentials.NutanixPrismEndpoint, error) {
					return &credentials.NutanixPrismEndpoint{
						Address: "cluster-endpoint",
						Port:    9440,
						CredentialRef: &credentials.NutanixCredentialReference{
							Kind: credentials.SecretKind,
							Name: "capx-nutanix-creds",
						},
						AdditionalTrustBundle: &credentials.NutanixTrustBundleReference{
							Kind:      credentials.NutanixTrustBundleKindConfigMap,
							Name:      "cm",
							Namespace: "capx-system",
						},
					}, nil
				},
			),
			expectProviderToBeNil: true,
			expectedErr:           fmt.Errorf("failed to retrieve capx-namespace. Make sure %s env variable is set", capxNamespaceKey),
		},
		{
			name: "additionalTrustBundle namespace not set, should fail",
			helper: testHelper().withCustomNutanixPrismEndpointReader(
				func() (*credentials.NutanixPrismEndpoint, error) {
					return &credentials.NutanixPrismEndpoint{
						Address: "cluster-endpoint",
						Port:    9440,
						CredentialRef: &credentials.NutanixCredentialReference{
							Kind:      credentials.SecretKind,
							Name:      "creds",
							Namespace: "capx-system",
						},
						AdditionalTrustBundle: &credentials.NutanixTrustBundleReference{
							Kind: credentials.NutanixTrustBundleKindConfigMap,
							Name: "cm",
						},
					}, nil
				},
			),
			expectProviderToBeNil: true,
			expectedErr:           fmt.Errorf("failed to retrieve capx-namespace. Make sure %s env variable is set", capxNamespaceKey),
		},
		{
			name: "reader returns an error, should fail",
			helper: testHelper().withCustomNutanixPrismEndpointReader(
				func() (*credentials.NutanixPrismEndpoint, error) {
					return nil, fmt.Errorf("could not read config")
				},
			),
			expectProviderToBeNil: true,
			expectedErr:           fmt.Errorf("failed to create prism endpoint: %w", fmt.Errorf("could not read config")),
		},
	}
	for _, tt := range tests {
		tt := tt // Capture range variable.
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.envs {
				t.Setenv(k, v)
			}
			provider, err := tt.helper.buildProviderFromFile()
			if tt.expectProviderToBeNil {
				assert.Nil(t, provider)
			} else {
				assert.NotNil(t, provider)
			}
			assert.Equal(t, tt.expectedErr, err)
		})
	}
}

func Test_readManagerNutanixPrismEndpointFromFile(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name              string
		config            []byte
		expectedEndpoint  *credentials.NutanixPrismEndpoint
		expectedErrString string
	}{
		{
			name:   "valid config",
			config: []byte(validTestConfig),
			expectedEndpoint: &credentials.NutanixPrismEndpoint{
				Address: "cluster-endpoint",
				Port:    9440,
				CredentialRef: &credentials.NutanixCredentialReference{
					Kind:      credentials.SecretKind,
					Name:      "creds",
					Namespace: "test",
				},
			},
		},
		{
			name:              "invalid config, expect an error",
			config:            []byte("{{}"),
			expectedErrString: "failed to unmarshal config: invalid character '{' looking for beginning of object key string",
		},
		{
			name:              "missing CredentialRef, expect an error",
			config:            []byte(invalidTestConfigMissingCredentialRef),
			expectedErrString: "credentialRef must be set on CAPX manager",
		},
	}
	for _, tt := range tests {
		tt := tt // Capture range variable.
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f, err := os.CreateTemp(t.TempDir(), "config-*.json")
			require.NoError(t, err, "error creating temp file")
			_, err = f.Write(tt.config)
			require.NoError(t, err, "error writing temp file")

			endpoint, err := readManagerNutanixPrismEndpointFromFile(f.Name())
			if err != nil {
				assert.ErrorContains(t, err, tt.expectedErrString)
			}
			assert.Equal(t, tt.expectedEndpoint, endpoint)
		})
	}
}

func Test_readManagerNutanixPrismEndpointFromFile_IsNotExist(t *testing.T) {
	_, err := readManagerNutanixPrismEndpointFromFile("filedoesnotexist.json")
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func testHelper() *NutanixClientHelper {
	helper := NutanixClientHelper{}
	return &helper
}

func testHelperWithFakedInformers(secrets []corev1.Secret, configMaps []corev1.ConfigMap) *NutanixClientHelper {
	objects := make([]runtime.Object, 0)
	for i := range secrets {
		secret := secrets[i]
		objects = append(objects, &secret)
	}
	for i := range configMaps {
		cm := configMaps[i]
		objects = append(objects, &cm)
	}

	fakeClient := fake.NewSimpleClientset(objects...)
	informerFactory := informers.NewSharedInformerFactory(fakeClient, time.Minute)
	secretInformer := informerFactory.Core().V1().Secrets()
	informer := secretInformer.Informer()
	go informer.Run(controllerCtx.Done())
	cache.WaitForCacheSync(controllerCtx.Done(), informer.HasSynced)

	configMapInformer := informerFactory.Core().V1().ConfigMaps()
	informer = configMapInformer.Informer()
	go informer.Run(controllerCtx.Done())
	cache.WaitForCacheSync(controllerCtx.Done(), informer.HasSynced)

	return NewHelper(secretInformer, configMapInformer)
}
