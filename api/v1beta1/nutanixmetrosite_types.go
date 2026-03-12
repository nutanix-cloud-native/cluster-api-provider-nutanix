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
	// Kind represents the Kind of NutanixMetroSite
	NutanixMetroSiteKind = "NutanixMetroSite"

	// NutanixMetroSiteFinalizer is the finalizer used by the NutanixMetroSite controller to block
	// deletion of the NutanixMetroSite object if there are references to this object by other resources.
	NutanixMetroSiteFinalizer = "infrastructure.cluster.x-k8s.io/nutanixmetrosite"
)

// NutanixMetroSiteSpec defines the desired state of NutanixMetroSite
type NutanixMetroSiteSpec struct {
	// failureDomain holds reference to the NutanixFailureDomain object of the NutanixMetroSite.
	// +kubebuilder:validation:Required
	FailureDomainRef corev1.LocalObjectReference `json:"failureDomainRef"`

	// metroRef holds reference to the NutanixMetro object this site belongs to.
	// +kubebuilder:validation:Required
	MetroRef corev1.LocalObjectReference `json:"metroRef"`

	// groupNameLabel optionally provides name for a group of worker nodes normally anchored to a topology segment.
	// +optional
	GroupNameLabel *string `json:"groupNameLabel,omitempty"`
}

// NutanixMetroSiteStatus defines the state of the NutanixMetroSite resource.
type NutanixMetroSiteStatus struct {
	// conditions represent the latest states of the metro,
	// +optional
	Conditions []capiv1beta1.Condition `json:"conditions,omitempty"`

	// v1beta2 groups all the fields that will be added or modified in NutanixMetroSite's status with the v1beta2 version.
	// +optional
	V1Beta2 *NutanixMetroSiteV1Beta2Status `json:"v1beta2,omitempty"`
}

// NutanixMetroSiteV1Beta2Status groups all the fields that will be added or modified in NutanixMetroSiteStatus with the v1beta2 version.
// See https://github.com/kubernetes-sigs/cluster-api/blob/main/docs/proposals/20240916-improve-status-in-CAPI-resources.md for more context.
type NutanixMetroSiteV1Beta2Status struct {
	// conditions represents the observations of a NutanixMetroSite's current state.
	// +optional
	// +listType=map
	// +listMapKey=type
	// +kubebuilder:validation:MaxItems=32
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:path=nutanixmetrosites,scope=Namespaced,categories=cluster-api
// +kubebuilder:subresource:status
// +kubebuilder:storageversion
// +kubebuilder:metadata:labels=clusterctl.cluster.x-k8s.io/move=
// +kubebuilder:printcolumn:name="FailureDomain",type="string",JSONPath=".spec.failureDomainRef",description="Reference of NutnaixFailureDomain objects"
// +kubebuilder:printcolumn:name="Metro",type="string",JSONPath=".spec.metroRef",description="Reference of NutnaixMetro object"

// NutanixMetroSite is the Schema for the NutanixMetroSite API.
type NutanixMetroSite struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NutanixMetroSiteSpec   `json:"spec,omitempty"`
	Status NutanixMetroSiteStatus `json:"status,omitempty"`
}

// GetConditions returns the set of conditions for this object.
func (m *NutanixMetroSite) GetConditions() capiv1beta1.Conditions {
	return m.Status.Conditions
}

// SetConditions sets the conditions on this object.
func (m *NutanixMetroSite) SetConditions(conditions capiv1beta1.Conditions) {
	m.Status.Conditions = conditions
}

// GetV1Beta2Conditions returns the set of conditions for this object.
func (m *NutanixMetroSite) GetV1Beta2Conditions() []metav1.Condition {
	if m.Status.V1Beta2 == nil {
		return nil
	}
	return m.Status.V1Beta2.Conditions
}

// SetV1Beta2Conditions sets the v1beta2 conditions on this object.
func (m *NutanixMetroSite) SetV1Beta2Conditions(conditions []metav1.Condition) {
	if m.Status.V1Beta2 == nil {
		m.Status.V1Beta2 = &NutanixMetroSiteV1Beta2Status{}
	}
	m.Status.V1Beta2.Conditions = conditions
}

// +kubebuilder:object:root=true

// NutanixMetroSiteList contains a list of NutanixMetroSite resources
type NutanixMetroSiteList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NutanixMetroSite `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NutanixMetroSite{}, &NutanixMetroSiteList{})
}
