/*
Copyright 2021.

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

package v1alpha4

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1alpha4"
	"sigs.k8s.io/cluster-api/errors"
)

const (
	// NutanixClusterFinalizer allows NutanixClusterReconciler to clean up AHV
	// resources associated with NutanixCluster before removing it from the
	// API Server.
	NutanixClusterFinalizer = "nutanixcluster.infrastructure.cluster.x-k8s.io"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// NutanixClusterSpec defines the desired state of NutanixCluster
type NutanixClusterSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// ControlPlaneEndpoint represents the endpoint used to communicate with the control plane.
	// host can be either DNS name or ip address
	// +optional
	ControlPlaneEndpoint capiv1.APIEndpoint `json:"controlPlaneEndpoint"`
}

// NutanixClusterStatus defines the observed state of NutanixCluster
type NutanixClusterStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// +optional
	Ready bool `json:"ready,omitempty"`

	FailureDomains capiv1.FailureDomains `json:"failureDomains,omitempty"`

	// Conditions defines current service state of the NutanixCluster.
	// +optional
	Conditions capiv1.Conditions `json:"conditions,omitempty"`

	// Will be set in case of failure of Cluster instance
	// +optional
	FailureReason *errors.ClusterStatusError `json:"failureReason,omitempty"`

	// Will be set in case of failure of Cluster instance
	// +optional
	FailureMessage *string `json:"failureMessage,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:resource:path=nutanixclusters,shortName=ncl,scope=Namespaced,categories=cluster-api
//+kubebuilder:subresource:status
//+kubebuilder:printcolumn:name="ControlplaneEndpoint",type="string",JSONPath=".spec.controlPlaneEndpoint.host",description="ControlplaneEndpoint"
//+kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.ready",description="in ready status"

// NutanixCluster is the Schema for the nutanixclusters API
type NutanixCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NutanixClusterSpec   `json:"spec,omitempty"`
	Status NutanixClusterStatus `json:"status,omitempty"`
}

// GetConditions returns the set of conditions for this object.
func (ncl *NutanixCluster) GetConditions() capiv1.Conditions {
	return ncl.Status.Conditions
}

// SetConditions sets the conditions on this object.
func (ncl *NutanixCluster) SetConditions(conditions capiv1.Conditions) {
	ncl.Status.Conditions = conditions
}

//+kubebuilder:object:root=true

// NutanixClusterList contains a list of NutanixCluster
type NutanixClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NutanixCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NutanixCluster{}, &NutanixClusterList{})
}
