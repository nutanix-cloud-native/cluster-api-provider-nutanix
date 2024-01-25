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

package v1beta1

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/nutanix-cloud-native/prism-go-client/environment/credentials"
)

func TestGetCredentialRefForCluster(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                   string
		nutanixCluster         *NutanixCluster
		expectedCredentialsRef *credentials.NutanixCredentialReference
		expectedErr            error
	}{
		{
			name: "all info is set",
			nutanixCluster: &NutanixCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: corev1.NamespaceDefault,
				},
				Spec: NutanixClusterSpec{
					PrismCentral: &credentials.NutanixPrismEndpoint{
						Address: "address",
						Port:    9440,
						CredentialRef: &credentials.NutanixCredentialReference{
							Kind:      credentials.SecretKind,
							Name:      "creds",
							Namespace: corev1.NamespaceDefault,
						},
					},
				},
			},
			expectedCredentialsRef: &credentials.NutanixCredentialReference{
				Kind:      credentials.SecretKind,
				Name:      "creds",
				Namespace: corev1.NamespaceDefault,
			},
		},
		{
			name: "prismCentralInfo is nil, should not fail",
			nutanixCluster: &NutanixCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: corev1.NamespaceDefault,
				},
				Spec: NutanixClusterSpec{},
			},
		},
		{
			name: "CredentialRef kind is not kind Secret, should not fail",
			nutanixCluster: &NutanixCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: corev1.NamespaceDefault,
				},
				Spec: NutanixClusterSpec{
					PrismCentral: &credentials.NutanixPrismEndpoint{
						CredentialRef: &credentials.NutanixCredentialReference{
							Kind: "unknown",
						},
					},
				},
			},
		},
		{
			name: "prismCentralInfo is not nil but CredentialRef is nil, should fail",
			nutanixCluster: &NutanixCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: corev1.NamespaceDefault,
				},
				Spec: NutanixClusterSpec{
					PrismCentral: &credentials.NutanixPrismEndpoint{
						Address: "address",
					},
				},
			},
			expectedErr: fmt.Errorf("credentialRef must be set on prismCentral attribute for cluster test in namespace default"),
		},
	}
	for _, tt := range tests {
		tt := tt // Capture range variable.
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ref, err := tt.nutanixCluster.GetPrismCentralCredentialRef()
			assert.Equal(t, tt.expectedCredentialsRef, ref)
			assert.Equal(t, tt.expectedErr, err)
		})
	}
}
