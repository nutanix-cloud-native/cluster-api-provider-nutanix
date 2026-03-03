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
	capiv1beta1 "sigs.k8s.io/cluster-api/api/core/v1beta1" //nolint:staticcheck // suppress complaining on Deprecated package
)

const (
	// Kind represents the Kind of NutanixMetroDomain
	NutanixMetroDomainKind = "NutanixMetroDomain"

	// NutanixMetroDomainFinalizer is the finalizer used by the NutanixMetroDomain controller to block
	// deletion of the NutanixMetroDomain object if there are references to this object by other resources.
	NutanixMetroDomainFinalizer = "infrastructure.cluster.x-k8s.io/nutanixmetrodomain"
)

// NutanixMetroDomainSpec defines the desired state of NutanixMetroDomain
// +kubebuilder:validation:XValidation:rule=`(has(self.protectionPolicy) && self.sites.all(s, has(s.recoveryPlan))) || (!has(self.protectionPolicy) && self.sites.all(s, !has(s.recoveryPlan)))`,message="protectionPolicy and each site's recoveryPlan must set or non-set simultaniously"
type NutanixMetroDomainSpec struct {
	// sites defines the failureDomains and the corresponding PC entities (categories and recoveryPlans) of the metro domain.
	// +kubebuilder:validation:XValidation:rule=`self.all(x, self.exists_one(y, x.failureDomain.name == y.failureDomain.name))`,message="each site's failureDomain.name must be unique"
	// +kubebuilder:validation:MinItems=2
	// +kubebuilder:validation:MaxItems=2
	// +listType=map
	// +listMapKey=name
	Sites []MetroSite `json:"sites"`

	// protectionPolicy is the resource identifier (name or uuid) of the ProtectionPolicy of the metro domain.
	// If it is not set, the reconciler will create the ProtectionPolicy PC entity, and set this field.
	// If set, the reconciler will validate that the configured ProtectionPolicy entity exists in PC
	// and meets the sites configuration.
	// +optional
	ProtectionPolicy *NutanixResourceIdentifier `json:"protectionPolicy,omitempty"`
}

// MetroSite defines a failureDomain and its corresponding PC entities (categories and recoveryPlans) of the metro domain.
type MetroSite struct {
	// name is unique name of the MetroSite
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=64
	Name string `json:"name"`

	// failureDomain is the local reference to the NutanixFailureDomain object
	// +kubebuilder:validation:Required
	FailureDomain corev1.LocalObjectReference `json:"failureDomain"`

	// category of the metro domain corresponding to the failureDomain PE.
	// +kubebuilder:validation:XValidation:rule=`has(self.key) && has(self.value)`,message="Metro site's category key and value must set"
	// +kubebuilder:validation:Required
	Category NutanixCategoryIdentifier `json:"category,omitempty"`

	// recoveryPlan is the resource identifier (name or uuid) of the RecoveryPlan corresponding to the failureDomain PE.
	// If not set, the reconciler will create the RecoveryPlan PC entity, and set this field with its identifier.
	// If set, the reconciler will validate that the RecoveryPlan entity with the identifier exists in PC
	// and meets the MetroSite configuration.
	// +optional
	RecoveryPlan *NutanixResourceIdentifier `json:"recoveryPlan,omitempty"`
}

// NutanixMetroDomainStatus defines the state of the NutanixMetroDomain resource.
type NutanixMetroDomainStatus struct {
	// conditions represent the latest states of the metro domain,
	// including if the metro domain PC entities (categories, protection policy,
	// and recovery plans) are successfully created or validated.
	// +optional
	Conditions []capiv1beta1.Condition `json:"conditions,omitempty"`

	// failureReason will be set in case of reconciling failure of the NutanixMetroDomain resource.
	// +optional
	FailureReason *string `json:"failureReason,omitempty"`

	// failureMessage will be set in case of reconciling failure of the NutanixMetroDomain resource.
	// +optional
	FailureMessage *string `json:"failureMessage,omitempty"`

	// ready is set to true when the metro domain PC entities (categories, protection
	// policy, and recovery plan) are successfully created or validated.
	// +kubebuilder:default=false
	Ready bool `json:"ready"`

	// v1beta2 groups all the fields that will be added or modified in AHVMetroZone's status with the v1beta2 version.
	// +optional
	V1Beta2 *NutanixMetroDomainV1Beta2Status `json:"v1beta2,omitempty"`
}

// NutanixMetroDomainV1Beta2Status groups all the fields that will be added or modified in NutanixMetroDomainStatus with the v1beta2 version.
// See https://github.com/kubernetes-sigs/cluster-api/blob/main/docs/proposals/20240916-improve-status-in-CAPI-resources.md for more context.
type NutanixMetroDomainV1Beta2Status struct {
	// conditions represents the observations of a NutanixMetroDomain's current state.
	// +optional
	// +listType=map
	// +listMapKey=type
	// +kubebuilder:validation:MaxItems=32
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:path=nutanixmetrodomains,scope=Namespaced,categories=cluster-api
// +kubebuilder:subresource:status
// +kubebuilder:storageversion
// +kubebuilder:metadata:labels=clusterctl.cluster.x-k8s.io/move=
// +kubebuilder:printcolumn:name="Ready",type="boolean",JSONPath=".status.ready",description="the metro domain PC entities are ready"

// NutanixMetroDomain is the Schema for the NutanixMetroDomains API.
type NutanixMetroDomain struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NutanixMetroDomainSpec   `json:"spec,omitempty"`
	Status NutanixMetroDomainStatus `json:"status,omitempty"`
}

// GetConditions returns the set of conditions for this object.
func (d *NutanixMetroDomain) GetConditions() capiv1beta1.Conditions {
	return d.Status.Conditions
}

// SetConditions sets the conditions on this object.
func (d *NutanixMetroDomain) SetConditions(conditions capiv1beta1.Conditions) {
	d.Status.Conditions = conditions
}

// GetV1Beta2Conditions returns the set of conditions for this object.
func (d *NutanixMetroDomain) GetV1Beta2Conditions() []metav1.Condition {
	if d.Status.V1Beta2 == nil {
		return nil
	}
	return d.Status.V1Beta2.Conditions
}

// SetV1Beta2Conditions sets the v1beta2 conditions on this object.
func (d *NutanixMetroDomain) SetV1Beta2Conditions(conditions []metav1.Condition) {
	if d.Status.V1Beta2 == nil {
		d.Status.V1Beta2 = &NutanixMetroDomainV1Beta2Status{}
	}
	d.Status.V1Beta2.Conditions = conditions
}

// +kubebuilder:object:root=true

// NutanixMetroDomainList contains a list of NutanixMetroDomain resources
type NutanixMetroDomainList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NutanixMetroDomain `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NutanixMetroDomain{}, &NutanixMetroDomainList{})
}
