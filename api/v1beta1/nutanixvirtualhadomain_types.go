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
	// NutanixVirtualHADomainKind represents the Kind of NutanixVirtualHADomain
	NutanixVirtualHADomainKind = "NutanixVirtualHADomain"

	// NutanixVirtualHADomainFinalizer is the finalizer used by the NutanixVirtualHADomain controller to block
	// deletion of the NutanixVirtualHADomain object if there are references to this object by other resources.
	NutanixVirtualHADomainFinalizer = "infrastructure.cluster.x-k8s.io/nutanixvirtualhadomain"
)

// NutanixVirtualHADomainSpec defines the desired state of NutanixVirtualHADomain.
type NutanixVirtualHADomainSpec struct {
	// metroRef is a reference to the NutanixMetro object that this virtual HA domain belongs to.
	// +kubebuilder:validation:XValidation:rule=`self.name != ""`,message="metroRef.name must not be empty"
	// +kubebuilder:validation:Required
	MetroRef corev1.LocalObjectReference `json:"metroRef"`

	// protectionGroup identifies the protection policy and category applied to this virtual HA domain.
	// +optional
	ProtectionGroup *NutanixProtectionGroup `json:"protectionGroup,omitempty"`

	// movementGroups defines the named groups of entities that move together within this
	// virtual HA domain. Each key is a user-defined group name (for example "default") and
	// the value describes the entities belonging to that group.
	// +optional
	MovementGroups map[string]NutanixMovementGroup `json:"movementGroups,omitempty"`
}

// NutanixProtectionGroup defines the protection policy and category that protect a virtual HA domain.
type NutanixProtectionGroup struct {
	// protectionPolicy identifies the protection policy applied to this virtual HA domain.
	// +required
	ProtectionPolicy *NutanixResourceIdentifier `json:"protectionPolicy,omitempty"`

	// category is the category (key/value) used for the protection group.
	// +required
	Category *NutanixCategoryIdentifier `json:"category,omitempty"`
}

// NutanixMovementGroup defines a group of entities that are moved together as part of a
// virtual HA domain failover or migration.
type NutanixMovementGroup struct {
	// categories is the list of category key/value pairs whose member entities
	// belong to this movement group.
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:MinItems=1
	Categories []NutanixCategoryIdentifier `json:"categories,omitempty"`

	// recoveryPlans is the list of recovery plans associated with this movement group.
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:MinItems=1
	RecoveryPlans []NutanixResourceIdentifier `json:"recoveryPlans,omitempty"`
}

// NutanixMovementGroupStatus captures the observed state of a movement group within a virtual HA domain.
type NutanixMovementGroupStatus struct {
	// ready is set to true when the movement group PC resources (categories, recovery plans) are valid and ready.
	// +kubebuilder:default=false
	Ready bool `json:"ready"`

	// metroSite represents the vHA resources associated with each metro site.
	// +optional
	MetroSite *NutanixVHADomainMetroSiteStatus `json:"metroSite,omitempty"`
}

// NutanixVirtualHADomainStatus defines the observed state of NutanixVirtualHADomain.
type NutanixVirtualHADomainStatus struct {
	// conditions represent the latest states of the virtual HA domain.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ready is set to true when the vHA domain PC resources (categories, protection
	// policy, and recovery plan) are valid and ready.
	// +kubebuilder:default=false
	Ready bool `json:"ready"`

	// movementGroups captures the observed state of each movement group defined in the
	// spec, keyed by the movement group name.
	// +optional
	MovementGroups map[string]NutanixMovementGroupStatus `json:"movementGroups,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:path=nutanixvirtualhadomains,shortName=nvha,scope=Namespaced,categories=cluster-api
// +kubebuilder:subresource:status
// +kubebuilder:storageversion
// +kubebuilder:metadata:labels=clusterctl.cluster.x-k8s.io/move=
// +kubebuilder:printcolumn:name="Metro",type="string",JSONPath=".spec.metroRef.name",description="Reference of NutanixMetro object"
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

// NutanixVHADomainMetroSiteStatus captures per-site resources in a vHA domain.
type NutanixVHADomainMetroSiteStatus struct {
	// failureDomains captures vHA resources keyed by NutanixFailureDomain name.
	// +optional
	FailureDomains map[string]*NutanixVHADomainMetroSiteResourceStatus `json:"failureDomains,omitempty"`
}

// NutanixVHADomainMetroSiteResourceStatus describes vHA resources for one metro site.
type NutanixVHADomainMetroSiteResourceStatus struct {
	// recoveryPlan is the recovery plan identifier for the site.
	// +optional
	RecoveryPlan string `json:"recoveryPlan,omitempty"`

	// category is the category (key/value) used for the site.
	// +optional
	Category string `json:"category,omitempty"`

	// pe is the Prism Element cluster identifier used for the site.
	// +optional
	PE string `json:"pe,omitempty"`
}

func init() {
	SchemeBuilder.Register(&NutanixVirtualHADomain{}, &NutanixVirtualHADomainList{})
}
