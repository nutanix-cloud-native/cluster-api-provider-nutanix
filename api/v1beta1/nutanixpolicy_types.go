/*
Copyright 2025 Nutanix

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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

const (
	NutanixVMAntiAffinityPolicyKind               = "NutanixVMAntiAffinityPolicy"
	NutanixVMAntiAfiinityPolicyNameAnnotation     = "infrastructure.cluster.x-k8s.io/vm-antiaffinity-policy-name"
	NutanixVMAntiAfiinityPolicyStrategyAnnotation = "infrastructure.cluster.x-k8s.io/vm-antiaffinity-policy-strategy"
)

// NutanixVMAntiAffinityPolicySpec defines the desired state of NutanixVMAntiAffinityPolicy
type NutanixVMAntiAffinityPolicySpec struct {
	// Name of the policy
	// +kubebuilder:validation:Optional
	Name string `json:"name,omitempty"`

	// Description of the policy
	// +kubebuilder:validation:Optional
	Description string `json:"description,omitempty"`

	// Category of the policy
	// +kubebuilder:validation:Optional
	Categories []NutanixCategoryIdentifier `json:"categories,omitempty"`
}

// NutanixVMAntiAffinityPolicyStatus defines the observed state of NutanixVMAntiAffinityPolicy
type NutanixVMAntiAffinityPolicyStatus struct {
	// Conditions of the policy
	// +optional
	// +kubebuilder:validation:Optional
	Conditions capiv1.Conditions `json:"conditions,omitempty"`

	// Message of the policy
	// +optional
	// +kubebuilder:validation:Optional
	Message *string `json:"message,omitempty"`

	// UUID of the policy
	// +optional
	// +kubebuilder:validation:Optional
	UUID string `json:"uuid,omitempty"`

	// CleanupList for categories that need to be cleaned up
	// +optional
	// +kubebuilder:validation:Optional
	CategoriesCleanupList []NutanixCategoryIdentifier `json:"categoriesCleanupList,omitempty"`

	// CleanupPolicy indicates if the policy should be cleaned up
	// +optional
	// +kubebuilder:validation:Optional
	CleanupPolicy bool `json:"cleanupPolicy,omitempty"`
}

// NutanixVMAntiAffinityPolicy is the Schema for the nutanixvmaffinitypolicies API
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Name",type="string",JSONPath=".spec.name"
// +kubebuilder:printcolumn:name="Description",type="string",JSONPath=".spec.description"
// +kubebuilder:printcolumn:name="State",type="string",JSONPath=".status.state"
// +kubebuilder:printcolumn:name="Message",type="string",JSONPath=".status.message"
type NutanixVMAntiAffinityPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              NutanixVMAntiAffinityPolicySpec   `json:"spec,omitempty"`
	Status            NutanixVMAntiAffinityPolicyStatus `json:"status,omitempty"`
}

// NutanixVMAntiAffinityPolicyList contains a list of NutanixVMAntiAffinityPolicy
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
type NutanixVMAntiAffinityPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NutanixVMAntiAffinityPolicy `json:"items"`
}

func (p *NutanixVMAntiAffinityPolicy) GetConditions() capiv1.Conditions {
	return p.Status.Conditions
}

func (p *NutanixVMAntiAffinityPolicy) SetConditions(conditions capiv1.Conditions) {
	p.Status.Conditions = conditions
}

func init() {
	SchemeBuilder.Register(&NutanixVMAntiAffinityPolicy{}, &NutanixVMAntiAffinityPolicyList{})
}
