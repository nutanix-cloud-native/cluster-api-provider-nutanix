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

package v1beta1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

const (
	// Kind represents the Kind of NutanixPrismCentral
	NutanixPrismCentralKind = "NutanixPrismCentral"

	// NutanixPrismCentralFinalizer is the finalizer used by the NutanixPrismCentral controller to block
	// deletion of the NutanixPrismCentral object if there are references to this object by other resources.
	NutanixPrismCentralFinalizer = "infrastructure.cluster.x-k8s.io/nutanixprismcentral"
)

// NutanixPrismCentralSpec defines the desired state of NutanixPrismCentral.
// It includes a Nutanix API endpoint with reference to credentials.
// Credentials are stored in Kubernetes secrets.
type NutanixPrismCentralSpec struct {
	// address is the endpoint address (DNS name or IP address) of the Nutanix Prism Central
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MaxLength=256
	Address string `json:"address"`

	// port is the port number to access the Nutanix Prism Central or Element (cluster)
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +kubebuilder:default=9440
	Port int32 `json:"port"`

	// insecure indicates the connection to Prism endpoint is insecure
	// +kubebuilder:default=false
	// +optional
	Insecure bool `json:"insecure"`

	// AdditionalTrustBundle is a PEM encoded x509 cert for the RootCA that was used to create the certificate
	// for a Prism Central that uses certificates that were issued by a non-publicly trusted RootCA. The trust
	// bundle is added to the cert pool used to authenticate the TLS connection to the Prism Central.
	// +optional
	AdditionalTrustBundle *string `json:"additionalTrustBundle,omitempty"`

	// credentialRef is the reference to the credentials secret object that holds the credentials data for the PC
	// +optional
	CredentialSecretRef *corev1.LocalObjectReference `json:"credentialSecretRef,omitempty"`
}

// NutanixPrismCentralStatus defines the observed state of NutanixPrismCentral resource.
type NutanixPrismCentralStatus struct {
	// conditions represent the latest states of the NutanixPrismCentral.
	// +optional
	Conditions []capiv1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:path=nutanixprismcentrals,scope=Namespaced,categories=cluster-api
// +kubebuilder:subresource:status
// +kubebuilder:storageversion
// +kubebuilder:metadata:labels=clusterctl.cluster.x-k8s.io/move=

// NutanixPrismCentral is the Schema for the NutanixPrismCentrals API.
type NutanixPrismCentral struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NutanixPrismCentralSpec   `json:"spec,omitempty"`
	Status NutanixPrismCentralStatus `json:"status,omitempty"`
}

// GetConditions returns the set of conditions for this object.
func (z *NutanixPrismCentral) GetConditions() capiv1.Conditions {
	return z.Status.Conditions
}

// SetConditions sets the conditions on this object.
func (z *NutanixPrismCentral) SetConditions(conditions capiv1.Conditions) {
	z.Status.Conditions = conditions
}

// +kubebuilder:object:root=true

// NutanixPrismCentralList contains a list of NutanixPrismCentral resources
type NutanixPrismCentralList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NutanixPrismCentral `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NutanixPrismCentral{}, &NutanixPrismCentralList{})
}
