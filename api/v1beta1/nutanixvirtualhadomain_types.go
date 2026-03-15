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
	// Kind represents the Kind of NutanixVirtualHADomain
	NutanixVirtualHADomainKind = "NutanixVirtualHADomain"

	// NutanixVirtualHADomainFinalizer is the finalizer used by the NutanixVirtualHADomain controller to block
	// deletion of the NutanixVirtualHADomain object if there are references to this object by other resources.
	NutanixVirtualHADomainFinalizer = "infrastructure.cluster.x-k8s.io/nutanixvirtualhadomain"
)

// NutanixVirtualHADomainSpec defines the desired state of NutanixVirtualHADomain
// +kubebuilder:validation:XValidation:rule="(!has(self.protectionPolicUUID) && !has(self.categoryUUIDs) && !has(self.recoveryPlanUUIDs)) || (has(self.protectionPolicUUID) && size(self.categoryUUIDs)==2 && size(self.recoveryPlanUUIDs)==2)",message="protectionPolicUUID, categoryUUIDs and recoveryPlanUUIDs must be either all set or all unset. When all set, categoryUUIDs and recoveryPlanUUIDs must have two items."

type NutanixVirtualHADomainSpec struct {
	// metroRef holds reference to the NutanixMetro object corresponding to this vHA domain.
	// +kubebuilder:validation:XValidation:rule=`has(self.name) && self.name != ""`,message="metroRef.name must not be empty"
	// +kubebuilder:validation:Required
	MetroRef corev1.LocalObjectReference `json:"metroRef"`

	// protectionPolicyUUID is the uuid of the ProtectionPolicy PC resource of the vHA domain.
	// This field is not set at CR creation time and the reconciler will create the ProtectionPolicy PC resource
	// and set this field with the resource's uuid.
	// +optional
	ProtectionPolicyUUID *string `json:"protectionPolicUUID,omitempty"`

	// categoryUUIDs are the uuids of the Category PC resources of the vHA domain.
	// This field is not set at CR creation time and the reconciler will create the Category PC resources
	// and set this field with the resources' uuids.
	// +kubebuilder:validation:MaxItems=2
	// +optional
	CategoryUUIDs []string `json:"categoryUUIDs,omitempty"`

	// recoveryPlanUUIDs are the uuids of the RecoveryPlan PC resources of the vHA domain.
	// This field is not set at CR creation time and the reconciler will create the RecoveryPlan PC resources
	// and set this field with the resources' uuids.
	// +kubebuilder:validation:MaxItems=2
	// +optional
	RecoveryPlanUUIDs []string `json:"recoveryPlanUUIDs,omitempty"`
}

// NutanixVirtualHADomainStatus defines the state of the NutanixVirtualHADomain resource.
type NutanixVirtualHADomainStatus struct {
	// conditions represent the latest states of the vHA domain,
	// including if the vHA domain PC resources (categories, protection policy,
	// and recovery plans) are successfully created or validated.
	// +optional
	Conditions []capiv1beta1.Condition `json:"conditions,omitempty"`

	// failureReason will be set in case of reconciling failure of the NutanixVirtualHADomain resource.
	// +optional
	FailureReason *string `json:"failureReason,omitempty"`

	// failureMessage will be set in case of reconciling failure of the NutanixVirtualHADomain resource.
	// +optional
	FailureMessage *string `json:"failureMessage,omitempty"`

	// ready is set to true when the vHA domain PC resources (categories, protection
	// policy, and recovery plan) are valid and ready.
	// +kubebuilder:default=false
	Ready bool `json:"ready"`

	// v1beta2 groups all the fields that will be added or modified in NutanixVirtualHADomain's status with the v1beta2 version.
	// +optional
	V1Beta2 *NutanixVirtualHADomainV1Beta2Status `json:"v1beta2,omitempty"`
}

// NutanixVirtualHADomainV1Beta2Status groups all the fields that will be added or modified in NutanixVirtualHADomainStatus with the v1beta2 version.
// See https://github.com/kubernetes-sigs/cluster-api/blob/main/docs/proposals/20240916-improve-status-in-CAPI-resources.md for more context.
type NutanixVirtualHADomainV1Beta2Status struct {
	// conditions represents the observations of a NutanixVirtualHADomain's current state.
	// +optional
	// +listType=map
	// +listMapKey=type
	// +kubebuilder:validation:MaxItems=32
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:path=nutanixvirtualhadomains,shortName=nvha,scope=Namespaced,categories=cluster-api
// +kubebuilder:subresource:status
// +kubebuilder:storageversion
// +kubebuilder:metadata:labels=clusterctl.cluster.x-k8s.io/move=
// +kubebuilder:printcolumn:name="Metro",type="string",JSONPath=".spec.metroRef",description="Reference of NutnaixMetro object"
// +kubebuilder:printcolumn:name="Ready",type="boolean",JSONPath=".status.ready",description="the vHA domain PC resources are ready"

// NutanixVirtualHADomain is the Schema for the NutanixVirtualHADomains API.
type NutanixVirtualHADomain struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NutanixVirtualHADomainSpec   `json:"spec,omitempty"`
	Status NutanixVirtualHADomainStatus `json:"status,omitempty"`
}

// GetConditions returns the set of conditions for this object.
func (d *NutanixVirtualHADomain) GetConditions() capiv1beta1.Conditions {
	return d.Status.Conditions
}

// SetConditions sets the conditions on this object.
func (d *NutanixVirtualHADomain) SetConditions(conditions capiv1beta1.Conditions) {
	d.Status.Conditions = conditions
}

// GetV1Beta2Conditions returns the set of conditions for this object.
func (d *NutanixVirtualHADomain) GetV1Beta2Conditions() []metav1.Condition {
	if d.Status.V1Beta2 == nil {
		return nil
	}
	return d.Status.V1Beta2.Conditions
}

// SetV1Beta2Conditions sets the v1beta2 conditions on this object.
func (d *NutanixVirtualHADomain) SetV1Beta2Conditions(conditions []metav1.Condition) {
	if d.Status.V1Beta2 == nil {
		d.Status.V1Beta2 = &NutanixVirtualHADomainV1Beta2Status{}
	}
	d.Status.V1Beta2.Conditions = conditions
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
