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
	// Kind represents the Kind of NutanixMetroVMPlacement
	NutanixMetroVMPlacementKind = "NutanixMetroVMPlacement"

	// NutanixMetroVMPlacementFinalizer is the finalizer used by the NutanixMetroVMPlacement controller to block
	// deletion of the NutanixMetroVMPlacement object if it is referenced by other resources.
	NutanixMetroVMPlacementFinalizer = "infrastructure.cluster.x-k8s.io/nutanixmetrovmplacements"
)

// NutanixMetroVMPlacementSpec defines the desired state of NutanixMetroVMPlacement
// +kubebuilder:validation:XValidation:rule=`self.placementStrategy != "Preferred" || (has(self.preferredFailureDomain) && self.failureDomains.exists(fd, fd.name == self.preferredFailureDomain))`,message="preferredFailureDomain is required for Preferred placementStrategy, and it must be referenced in spec.failureDomains."
type NutanixMetroVMPlacementSpec struct {
	// failureDomains are the references to the two failure domains of the Nutanix Metro Domain.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=2
	// +kubebuilder:validation:MaxItems=2
	// +listType=map
	// +listMapKey=name
	FailureDomains []corev1.LocalObjectReference `json:"failureDomains"`

	// placementStrategy defines the VM placement strategy of Preferred or Random, with Preferred as the default.
	// +kubebuilder:validation:Required
	// +kubebuilder:default="Preferred"
	// +kubebuilder:validation:Enum:=Preferred;Random
	PlacementStrategy VMPlacementStrategy `json:"placementStrategy"`

	// preferredFailureDomain is the preferred failureDomain name, specifying the failure domain the VM should be provisioned.
	// When the preferred failure domain is not available, or being evacuated (e.g. in maintainence), the VM
	// will be provisioned to the remaining failure domain.
	// This field is required when the VM placement strategy is Preferred.
	// +optional
	PreferredFailureDomain *string `json:"preferredFailureDomain,omitempty"`
}

// VMPlacementStrategy is an enumeration of VM placement strategies.
type VMPlacementStrategy string

const (
	// PreferredStrategy is the VM placement strategy with the specified preferred failure domain.
	PreferredStrategy VMPlacementStrategy = "Preferred"

	// RandomStrategy is the VM placement strategy with random choice of failure domain.
	RandomStrategy VMPlacementStrategy = "Random"
)

// NutanixMetroVMPlacementStatus defines the observed state of NutanixMetroVMPlacement resource.
type NutanixMetroVMPlacementStatus struct {
	// conditions represent the latest states of the NutanixMetroVMPlacement.
	// +optional
	Conditions []capiv1beta1.Condition `json:"conditions,omitempty"`

	// v1beta2 groups all the fields that will be added or modified in NutanixMetroVMPlacement's status with the v1beta2 version.
	// +optional
	V1Beta2 *NutanixMetroVMPlacementV1Beta2Status `json:"v1beta2,omitempty"`
}

// NutanixMetroVMPlacementV1Beta2Status groups all the fields that will be added or modified in NutanixMetroVMPlacementStatus with the v1beta2 version.
// See https://github.com/kubernetes-sigs/cluster-api/blob/main/docs/proposals/20240916-improve-status-in-CAPI-resources.md for more context.
type NutanixMetroVMPlacementV1Beta2Status struct {
	// conditions represents the observations of a NutanixMetroVMPlacement's current state.
	// +optional
	// +listType=map
	// +listMapKey=type
	// +kubebuilder:validation:MaxItems=32
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:path=nutanixmetrovmplacements,scope=Namespaced,categories=cluster-api
// +kubebuilder:subresource:status
// +kubebuilder:storageversion
// +kubebuilder:metadata:labels=clusterctl.cluster.x-k8s.io/move=
// +kubebuilder:printcolumn:name="PlacementStrategy",type="string",JSONPath=".spec.placementStrategy",description="VM Placement Strategy"
// +kubebuilder:printcolumn:name="PreferredFailureDomain",type="string",JSONPath=".spec.preferredFailureDomain",description="Preferred failureDomain for Preferred Strategy"

// NutanixMetroVMPlacement is the Schema for the NutanixMetroVMPlacement API.
type NutanixMetroVMPlacement struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NutanixMetroVMPlacementSpec   `json:"spec,omitempty"`
	Status NutanixMetroVMPlacementStatus `json:"status,omitempty"`
}

// GetConditions returns the set of conditions for this object.
func (z *NutanixMetroVMPlacement) GetConditions() capiv1beta1.Conditions {
	return z.Status.Conditions
}

// SetConditions sets the conditions on this object.
func (z *NutanixMetroVMPlacement) SetConditions(conditions capiv1beta1.Conditions) {
	z.Status.Conditions = conditions
}

// GetV1Beta2Conditions returns the set of conditions for this object.
func (z *NutanixMetroVMPlacement) GetV1Beta2Conditions() []metav1.Condition {
	if z.Status.V1Beta2 == nil {
		return nil
	}
	return z.Status.V1Beta2.Conditions
}

// SetV1Beta2Conditions sets the v1beta2 conditions on this object.
func (z *NutanixMetroVMPlacement) SetV1Beta2Conditions(conditions []metav1.Condition) {
	if z.Status.V1Beta2 == nil {
		z.Status.V1Beta2 = &NutanixMetroVMPlacementV1Beta2Status{}
	}
	z.Status.V1Beta2.Conditions = conditions
}

// +kubebuilder:object:root=true

// NutanixMetroVMPlacementList contains a list of NutanixMetroVMPlacement resources
type NutanixMetroVMPlacementList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NutanixMetroVMPlacement `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NutanixMetroVMPlacement{}, &NutanixMetroVMPlacementList{})
}
