/*
Copyright 2022 Nutanix

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
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

const (
	// NutanixVMAntiAffinityPolicyFinalizer allows NutanixVMAntiAffinityPolicyReconciler to clean up resources associated with NutanixVMAntiAffinityPolicy before
	// removing it from the apiserver.
	NutanixVMAntiAffinityPolicyFinalizer = "nutanixvmantiaffinitypolicy.infrastructure.cluster.x-k8s.io"
)

// NutanixVMAntiAffinityPolicySpec defines the desired state of NutanixVMAntiAffinityPolicy
type NutanixVMAntiAffinityPolicySpec struct {
	// IdentityRef is a reference to the NutanixPrismIdentity object
	// +kubebuilder:validation:Required
	IdentityRef string `json:"identityRef"`

	// Name is the name of the anti-affinity policy in Prism
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Description is the description of the anti-affinity policy
	// +optional
	Description string `json:"description,omitempty"`

	// Categories is the list of categories configured for the VM-VM anti-affinity policy
	// +optional
	Categories []NutanixCategoryIdentifier `json:"categories,omitempty"`
}

// NutanixVMAntiAffinityPolicyStatus defines the observed state of NutanixVMAntiAffinityPolicy
type NutanixVMAntiAffinityPolicyStatus struct {
	// Ready denotes that the anti-affinity policy has been successfully created or updated
	// +optional
	Ready bool `json:"ready,omitempty"`

	// FailureMessage will be set in the event that there is a terminal problem
	// reconciling the state and will be set to a descriptive error message.
	// +optional
	FailureMessage *string `json:"failureMessage,omitempty"`

	// PolicyUUID is the UUID of the anti-affinity policy in Prism
	// +optional
	PolicyUUID string `json:"policyUUID,omitempty"`

	// VMs is the list of VM identifiers managed by this policy
	// +optional
	VMs []NutanixResourceIdentifier `json:"vms,omitempty"`

	// NumCompliantVms is the number of compliant VMs which are part of the VM-VM anti-affinity policy
	// +optional
	NumCompliantVms *int64 `json:"numCompliantVms,omitempty"`

	// NumNonCompliantVms is the number of non-compliant VMs which are part of the VM-VM anti-affinity policy
	// +optional
	NumNonCompliantVms *int64 `json:"numNonCompliantVms,omitempty"`

	// NumPendingVms is the number of VMs with compliance state as pending, which are part of the VM-VM anti-affinity policy
	// +optional
	NumPendingVms *int64 `json:"numPendingVms,omitempty"`

	// CreateTime is the VM-VM anti-affinity policy creation time
	// +optional
	CreateTime *metav1.Time `json:"createTime,omitempty"`

	// UpdateTime is the VM-VM anti-affinity policy last updated time
	// +optional
	UpdateTime *metav1.Time `json:"updateTime,omitempty"`

	// Conditions defines current service state of the NutanixVMAntiAffinityPolicy
	// +optional
	Conditions clusterv1.Conditions `json:"conditions,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.ready",description="Policy created in Prism"
//+kubebuilder:printcolumn:name="PolicyUUID",type="string",JSONPath=".status.policyUUID",description="UUID of the policy in Prism"
//+kubebuilder:printcolumn:name="CompliantVMs",type="integer",JSONPath=".status.numCompliantVms",description="Number of compliant VMs"
//+kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp",description="Time duration since creation of NutanixVMAntiAffinityPolicy"

// NutanixVMAntiAffinityPolicy is the Schema for the nutanixvmantiaffinitypolicies API
type NutanixVMAntiAffinityPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NutanixVMAntiAffinityPolicySpec   `json:"spec,omitempty"`
	Status NutanixVMAntiAffinityPolicyStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// NutanixVMAntiAffinityPolicyList contains a list of NutanixVMAntiAffinityPolicy
type NutanixVMAntiAffinityPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NutanixVMAntiAffinityPolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NutanixVMAntiAffinityPolicy{}, &NutanixVMAntiAffinityPolicyList{})
}