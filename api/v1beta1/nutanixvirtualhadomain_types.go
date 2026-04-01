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
)

const (
	// Kind represents the Kind of NutanixVirtualHADomain
	NutanixVirtualHADomainKind = "NutanixVirtualHADomain"

	// NutanixVirtualHADomainFinalizer is the finalizer used by the NutanixVirtualHADomain controller to block
	// deletion of the NutanixVirtualHADomain object if there are references to this object by other resources.
	NutanixVirtualHADomainFinalizer = "infrastructure.cluster.x-k8s.io/nutanixvirtualhadomain"
)

// NutanixVirtualHADomainSpec defines the desired state of NutanixVirtualHADomain
// +kubebuilder:validation:XValidation:rule="(!has(self.protectionPolicy) && !has(self.categories) && !has(self.recoveryPlans)) || (has(self.protectionPolicy) && size(self.categories)==2 && size(self.recoveryPlans)==2)",message="protectionPolicy, categories and recoveryPlans must be either all set or all unset. When all set, categories and recoveryPlans must have two items."

type NutanixVirtualHADomainSpec struct {
	// metroRef holds reference to the NutanixMetro object corresponding to this vHA domain.
	// +kubebuilder:validation:XValidation:rule=`has(self.name) && self.name != ""`,message="metroRef.name must not be empty"
	// +kubebuilder:validation:Required
	MetroRef corev1.LocalObjectReference `json:"metroRef"`

	// protectionPolicy is the identifier (name or uuid) of the ProtectionPolicy PC resource of the vHA domain.
	// This field is not set at CR creation time and the reconciler will create the ProtectionPolicy PC resource
	// and set this field with the resource's identifier (name or uuid).
	// +optional
	ProtectionPolicy *NutanixResourceIdentifier `json:"protectionPolicy,omitempty"`

	// categories are the identifiers (key/value pairs) of the Category PC resources of the vHA domain.
	// This field is not set at CR creation time and the reconciler will create the Category PC resources
	// and set this field with the resources' identifiers (key/value pairs).
	// +kubebuilder:validation:MaxItems=2
	// +listType=map
	// +listMapKey=value
	// +optional
	Categories []NutanixCategoryIdentifier `json:"categories,omitempty"`

	// recoveryPlans are the identifiers (name or uuid) of the RecoveryPlan PC resources of the vHA domain.
	// This field is not set at CR creation time and the reconciler will create the RecoveryPlan PC resources
	// and set this field with the resources' identifiers (name or uuid).
	// +kubebuilder:validation:MaxItems=2
	// +optional
	RecoveryPlans []NutanixResourceIdentifier `json:"recoveryPlans,omitempty"`
}

// NutanixVirtualHADomainStatus defines the state of the NutanixVirtualHADomain resource.
type NutanixVirtualHADomainStatus struct {
	// conditions represent the latest states of the vHA domain,
	// including if the vHA domain PC resources (categories, protection policy,
	// and recovery plans) are successfully created or validated.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ready is set to true when the vHA domain PC resources (categories, protection
	// policy, and recovery plan) are valid and ready.
	// +kubebuilder:default=false
	Ready bool `json:"ready"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:path=nutanixvirtualhadomains,shortName=nvha,scope=Namespaced,categories=cluster-api
// +kubebuilder:subresource:status
// +kubebuilder:storageversion
// +kubebuilder:metadata:labels=clusterctl.cluster.x-k8s.io/move=
// +kubebuilder:printcolumn:name="Metro",type="string",JSONPath=".spec.metroRef",description="Reference of NutnaixMetro object"
// +kubebuilder:printcolumn:name="Ready",type="boolean",JSONPath=".status.ready",description="the vHA domain PC resources are ready or not"

// NutanixVirtualHADomain is the Schema for the NutanixVirtualHADomains API.
type NutanixVirtualHADomain struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NutanixVirtualHADomainSpec   `json:"spec,omitempty"`
	Status NutanixVirtualHADomainStatus `json:"status,omitempty"`
}

// GetConditions returns the set of conditions for this object.
func (d *NutanixVirtualHADomain) GetConditions() []metav1.Condition {
	return d.Status.Conditions
}

// SetConditions sets the conditions on this object.
func (d *NutanixVirtualHADomain) SetConditions(conditions []metav1.Condition) {
	d.Status.Conditions = conditions
}

// +kubebuilder:object:root=true

// NutanixVirtualHADomainList contains a list of NutanixVirtualHADomain resources
type NutanixVirtualHADomainList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NutanixVirtualHADomain `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NutanixVirtualHADomain{}, &NutanixVirtualHADomainList{})
}
