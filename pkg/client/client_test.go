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
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	prismgoclient "github.com/nutanix-cloud-native/prism-go-client"
	"github.com/nutanix-cloud-native/prism-go-client/environment/credentials"
	envTypes "github.com/nutanix-cloud-native/prism-go-client/environment/types"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"
	ctrl "sigs.k8s.io/controller-runtime"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
)

const (
	validTestCredentials = `[
      {
        "type": "basic_auth",
        "data": {
          "prismCentral":{
            "username": "user",
            "password": "password"
          }
        }
      }
    ]`
	validTestManagerCredentials = `[
      {
        "type": "basic_auth",
        "data": {
          "prismCentral":{
            "username": "admin",
            "password": "adminpassword"
          }
        }
      }
    ]`

	certBundleKey = "ca.crt"
	validTestCA   = `-----BEGIN CERTIFICATE-----
MIIDdTCCAl2gAwIBAgILBAAAAAABFUtaw5QwDQYJKoZIhvcNAQEFBQAwVzELMAkGA1UEBhMCQkUx
GTAXBgNVBAoTEEdsb2JhbFNpZ24gbnYtc2ExEDAOBgNVBAsTB1Jvb3QgQ0ExGzAZBgNVBAMTEkds
b2JhbFNpZ24gUm9vdCBDQTAeFw05ODA5MDExMjAwMDBaFw0yODAxMjgxMjAwMDBaMFcxCzAJBgNV
BAYTAkJFMRkwFwYDVQQKExBHbG9iYWxTaWduIG52LXNhMRAwDgYDVQQLEwdSb290IENBMRswGQYD
VQQDExJHbG9iYWxTaWduIFJvb3QgQ0EwggEiMA0GCSqGSIb3DQEBAQUAA4IBDwAwggEKAoIBAQDa
DuaZjc6j40+Kfvvxi4Mla+pIH/EqsLmVEQS98GPR4mdmzxzdzxtIK+6NiY6arymAZavpxy0Sy6sc
THAHoT0KMM0VjU/43dSMUBUc71DuxC73/OlS8pF94G3VNTCOXkNz8kHp1Wrjsok6Vjk4bwY8iGlb
Kk3Fp1S4bInMm/k8yuX9ifUSPJJ4ltbcdG6TRGHRjcdGsnUOhugZitVtbNV4FpWi6cgKOOvyJBNP
c1STE4U6G7weNLWLBYy5d4ux2x8gkasJU26Qzns3dLlwR5EiUWMWea6xrkEmCMgZK9FGqkjWZCrX
gzT/LCrBbBlDSgeF59N89iFo7+ryUp9/k5DPAgMBAAGjQjBAMA4GA1UdDwEB/wQEAwIBBjAPBgNV
HRMBAf8EBTADAQH/MB0GA1UdDgQWBBRge2YaRQ2XyolQL30EzTSo//z9SzANBgkqhkiG9w0BAQUF
AAOCAQEA1nPnfE920I2/7LqivjTFKDK1fPxsnCwrvQmeU79rXqoRSLblCKOzyj1hTdNGCbM+w6Dj
Y1Ub8rrvrTnhQ7k4o+YviiY776BQVvnGCv04zcQLcFGUl5gE38NflNUVyRRBnMRddWQVDf9VMOyG
j/8N7yy5Y0b2qvzfvGn9LhJIZJrglfCm7ymPAbEVtQwdpf5pLGkkeB6zpxxxYu7KyJesF12KwvhH
hm4qxFYxldBniYUr+WymXUadDKqC5JlR3XC321Y9YeRq4VzW9v493kHMB65jUr9TU/Qr6cf9tveC
X4XSQRjbgbMEHMUfpIBvFSDJ3gyICh3WZlXi/EjJKSZp4A==
-----END CERTIFICATE-----`
	validTestManagerCA = `-----BEGIN CERTIFICATE-----
MIIEKjCCAxKgAwIBAgIEOGPe+DANBgkqhkiG9w0BAQUFADCBtDEUMBIGA1UEChMLRW50cnVzdC5u
ZXQxQDA+BgNVBAsUN3d3dy5lbnRydXN0Lm5ldC9DUFNfMjA0OCBpbmNvcnAuIGJ5IHJlZi4gKGxp
bWl0cyBsaWFiLikxJTAjBgNVBAsTHChjKSAxOTk5IEVudHJ1c3QubmV0IExpbWl0ZWQxMzAxBgNV
BAMTKkVudHJ1c3QubmV0IENlcnRpZmljYXRpb24gQXV0aG9yaXR5ICgyMDQ4KTAeFw05OTEyMjQx
NzUwNTFaFw0yOTA3MjQxNDE1MTJaMIG0MRQwEgYDVQQKEwtFbnRydXN0Lm5ldDFAMD4GA1UECxQ3
d3d3LmVudHJ1c3QubmV0L0NQU18yMDQ4IGluY29ycC4gYnkgcmVmLiAobGltaXRzIGxpYWIuKTEl
MCMGA1UECxMcKGMpIDE5OTkgRW50cnVzdC5uZXQgTGltaXRlZDEzMDEGA1UEAxMqRW50cnVzdC5u
ZXQgQ2VydGlmaWNhdGlvbiBBdXRob3JpdHkgKDIwNDgpMIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8A
MIIBCgKCAQEArU1LqRKGsuqjIAcVFmQqK0vRvwtKTY7tgHalZ7d4QMBzQshowNtTK91euHaYNZOL
Gp18EzoOH1u3Hs/lJBQesYGpjX24zGtLA/ECDNyrpUAkAH90lKGdCCmziAv1h3edVc3kw37XamSr
hRSGlVuXMlBvPci6Zgzj/L24ScF2iUkZ/cCovYmjZy/Gn7xxGWC4LeksyZB2ZnuU4q941mVTXTzW
nLLPKQP5L6RQstRIzgUyVYr9smRMDuSYB3Xbf9+5CFVghTAp+XtIpGmG4zU/HoZdenoVve8AjhUi
VBcAkCaTvA5JaJG/+EfTnZVCwQ5N328mz8MYIWJmQ3DW1cAH4QIDAQABo0IwQDAOBgNVHQ8BAf8E
BAMCAQYwDwYDVR0TAQH/BAUwAwEB/zAdBgNVHQ4EFgQUVeSB0RGAvtiJuQijMfmhJAkWuXAwDQYJ
KoZIhvcNAQEFBQADggEBADubj1abMOdTmXx6eadNl9cZlZD7Bh/KM3xGY4+WZiT6QBshJ8rmcnPy
T/4xmf3IDExoU8aAghOY+rat2l098c5u9hURlIIM7j+VrxGrD9cv3h8Dj1csHsm7mhpElesYT6Yf
zX1XEC+bBAlahLVu2B064dae0Wx5XnkcFMXj0EyTO2U87d89vqbllRrDtRnDvV5bu/8j72gZyxKT
J1wDLW8w0B62GqzeWvfRqqgnpv55gcR5mTNXuhKwqeBCbJPKVt7+bYQLCIt+jerXmCHG8+c8eS9e
nNFMFY3h7CI3zJpDC5fcgJCNs2ebb0gIFVbPv/ErfF6adulZkMV8gzURZVE=
-----END CERTIFICATE-----`

	validTestConfig = `{
      "address": "cluster-endpoint",
      "port": 9440,
	  "credentialRef": {
        "kind": "Secret",
        "name": "creds",
        "namespace": "test"
	  }
	}`
	invalidTestConfigMissingCredentialRef = `{
      "address": "cluster-endpoint",
      "port": 9440
	}`
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
			me, err := tt.helper.buildManagementEndpoint(context.TODO(), tt.nutanixCluster)
			assert.Equal(t, tt.expectedManagementEndpoint, me)
			assert.Equal(t, tt.expectedErr, err)
		})
	}
}

func Test_buildClientFromCredentials(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                  string
		creds                 prismgoclient.Credentials
		additionalTrustBundle string
		expectClientToBeNil   bool
		expectedErr           error
	}{
		{
			name: "all information set",
			creds: prismgoclient.Credentials{
				Endpoint: "cluster-endpoint",
				Port:     "9440",
				Username: "user",
				Password: "password",
			},
			additionalTrustBundle: validTestCA,
		},
		{
			name: "some information set, expect defaults",
			creds: prismgoclient.Credentials{
				Endpoint: "cluster-endpoint",
				Username: "user",
				Password: "password",
			},
		},
		{
			name: "missing username",
			creds: prismgoclient.Credentials{
				Endpoint: "cluster-endpoint",
				Port:     "9440",
				Password: "password",
			},
			additionalTrustBundle: validTestCA,
			expectClientToBeNil:   true,
			expectedErr:           ErrPrismIUsernameNotSet,
		},
		{
			name: "missing password",
			creds: prismgoclient.Credentials{
				Endpoint: "cluster-endpoint",
				Port:     "9440",
				Username: "user",
			},
			additionalTrustBundle: validTestCA,
			expectClientToBeNil:   true,
			expectedErr:           ErrPrismIPasswordNotSet,
		},
	}
	for _, tt := range tests {
		tt := tt // Capture range variable.
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			client, err := buildClientFromCredentials(tt.creds, tt.additionalTrustBundle)
			if tt.expectClientToBeNil {
				assert.Nil(t, client)
			} else {
				assert.NotNil(t, client)
			}
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
			assert.NoError(t, err, "error creating temp file")
			_, err = f.Write(tt.config)
			assert.NoError(t, err, "error writing temp file")

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
	assert.ErrorIs(t, err, os.ErrNotExist, err)
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
