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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/errors"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

const (
	// NutanixMachineKind represents the Kind of NutanixMachine
	NutanixMachineKind = "NutanixMachine"

	// NutanixMachineFinalizer allows NutanixMachineReconciler to clean up AHV
	// resources associated with NutanixMachine before removing it from the
	// API Server.
	NutanixMachineFinalizer = "nutanixmachine.infrastructure.cluster.x-k8s.io"

	// NutanixMachineBootstrapRefKindSecret represents the Kind of Secret
	// referenced by NutanixMachine's BootstrapRef.
	NutanixMachineBootstrapRefKindSecret = "Secret"

	// NutanixMachineBootstrapRefKindImage represents the Kind of Image
	// referenced by NutanixMachine's BootstrapRef. If the BootstrapRef.Kind is set
	// to Image, the NutanixMachine will be created with the image mounted
	// as a CD-ROM.
	NutanixMachineBootstrapRefKindImage = "Image"
)

// NutanixImageLookup defines how to fetch images for the cluster
// using the fields combined.
type NutanixImageLookup struct {
	// Format is the naming format to look up the image for this
	// machine It will be ignored if an explicit image is set. Supports
	// substitutions for {{.BaseOS}} and {{.K8sVersion}} with the base OS and
	// kubernetes version, respectively. The BaseOS will be the value in
	// BaseOS and the K8sVersion is the value in the Machine .spec.version, with the v prefix removed.
	// This is effectively the defined by the packages produced by kubernetes/release without v as a
	// prefix: 1.13.0, 1.12.5-mybuild.1, or 1.17.3. For example, the default
	// image format of {{.BaseOS}}-?{{.K8sVersion}}-* and BaseOS as "rhel-8.10" will end up
	// searching for images that match the pattern rhel-8.10-1.30.5-* for a
	// Machine that is targeting kubernetes v1.30.5. See
	// also: https://golang.org/pkg/text/template/
	// +kubebuilder:default:="capx-{{.BaseOS}}-{{.K8sVersion}}-*"
	Format *string `json:"format,omitempty"`
	// BaseOS is the name of the base operating system to use for
	// image lookup.
	// +kubebuilder:validation:MinLength:=1
	BaseOS string `json:"baseOS"`
}

// NutanixMachineSpec defines the desired state of NutanixMachine
// +kubebuilder:validation:XValidation:rule="has(self.image) != has(self.imageLookup)",message="Either 'image' or 'imageLookup' must be set, but not both"
type NutanixMachineSpec struct {
	// SPEC FIELDS - desired state of NutanixMachine
	// Important: Run "make" to regenerate code after modifying this file

	// ProviderID is the unique identifier as specified by the cloud provider.
	// +optional
	ProviderID string `json:"providerID,omitempty"`
	// vcpusPerSocket is the number of vCPUs per socket of the VM
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	VCPUsPerSocket int32 `json:"vcpusPerSocket"`
	// vcpuSockets is the number of vCPU sockets of the VM
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	VCPUSockets int32 `json:"vcpuSockets"`
	// memorySize is the memory size (in Quantity format) of the VM
	// The minimum memorySize is 2Gi bytes
	// +kubebuilder:validation:Required
	MemorySize resource.Quantity `json:"memorySize"`
	// image is to identify the nutanix machine image uploaded to the Prism Central (PC)
	// The image identifier (uuid or name) can be obtained from the Prism Central console
	// or using the prism_central API.
	// +kubebuilder:validation:Optional
	// +optional
	Image *NutanixResourceIdentifier `json:"image,omitempty"`
	// imageLookup is a container that holds how to look up rhcos images for the cluster.
	// +kubebuilder:validation:Optional
	// +optional
	ImageLookup *NutanixImageLookup `json:"imageLookup,omitempty"`
	// cluster is to identify the cluster (the Prism Element under management
	// of the Prism Central), in which the Machine's VM will be created.
	// The cluster identifier (uuid or name) can be obtained from the Prism Central console
	// or using the prism_central API.
	// +kubebuilder:validation:Optional
	Cluster NutanixResourceIdentifier `json:"cluster"`
	// subnet is to identify the cluster's network subnet to use for the Machine's VM
	// The cluster identifier (uuid or name) can be obtained from the Prism Central console
	// or using the prism_central API.
	// +kubebuilder:validation:Optional
	Subnets []NutanixResourceIdentifier `json:"subnet"`
	// List of categories that need to be added to the machines. Categories must already exist in Prism Central
	// +kubebuilder:validation:Optional
	AdditionalCategories []NutanixCategoryIdentifier `json:"additionalCategories,omitempty"`
	// Add the machine resources to a Prism Central project
	// +optional
	Project *NutanixResourceIdentifier `json:"project,omitempty"`
	// Defines the boot type of the virtual machine. Only supports UEFI and Legacy
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum:=legacy;uefi
	BootType NutanixBootType `json:"bootType,omitempty"`
	// systemDiskSize is size (in Quantity format) of the system disk of the VM
	// The minimum systemDiskSize is 20Gi bytes
	// +kubebuilder:validation:Required
	SystemDiskSize resource.Quantity `json:"systemDiskSize"`
	// BootstrapRef is a reference to a bootstrap provider-specific resource
	// that holds configuration details.
	// +optional
	BootstrapRef *corev1.ObjectReference `json:"bootstrapRef,omitempty"`
	// List of GPU devices that need to be added to the machines.
	// +kubebuilder:validation:Optional
	GPUs []NutanixGPU `json:"gpus,omitempty"`
}

// NutanixMachineStatus defines the observed state of NutanixMachine
type NutanixMachineStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Ready is true when the provider resource is ready.
	// +optional
	Ready bool `json:"ready"`

	// Addresses contains the Nutanix VM associated addresses.
	// Address type is one of Hostname, ExternalIP, InternalIP, ExternalDNS, InternalDNS
	Addresses []capiv1.MachineAddress `json:"addresses,omitempty"`

	// The Nutanix VM's UUID
	// +optional
	VmUUID string `json:"vmUUID,omitempty"`

	// NodeRef is a reference to the corresponding workload cluster Node if it exists.
	// Deprecated: Do not use. Will be removed in a future release.
	// +optional
	NodeRef *corev1.ObjectReference `json:"nodeRef,omitempty"`

	// Conditions defines current service state of the NutanixMachine.
	// +optional
	Conditions capiv1.Conditions `json:"conditions,omitempty"`

	// Will be set in case of failure of Machine instance
	// +optional
	FailureReason *errors.MachineStatusError `json:"failureReason,omitempty"`

	// Will be set in case of failure of Machine instance
	// +optional
	FailureMessage *string `json:"failureMessage,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:path=nutanixmachines,shortName=nma,scope=Namespaced,categories=cluster-api
// +kubebuilder:subresource:status
// +kubebuilder:storageversion
// +kubebuilder:printcolumn:name="Address",type="string",JSONPath=".status.addresses[0].address",description="The VM address"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.ready",description="NutanixMachine ready status"
// +kubebuilder:printcolumn:name="ProviderID",type="string",JSONPath=".spec.providerID",description="NutanixMachine instance ID"
// NutanixMachine is the Schema for the nutanixmachines API
type NutanixMachine struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NutanixMachineSpec   `json:"spec,omitempty"`
	Status NutanixMachineStatus `json:"status,omitempty"`
}

// GetConditions returns the set of conditions for this object.
func (nm *NutanixMachine) GetConditions() capiv1.Conditions {
	return nm.Status.Conditions
}

// SetConditions sets the conditions on this object.
func (nm *NutanixMachine) SetConditions(conditions capiv1.Conditions) {
	nm.Status.Conditions = conditions
}

//+kubebuilder:object:root=true

// NutanixMachineList contains a list of NutanixMachine
type NutanixMachineList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NutanixMachine `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NutanixMachine{}, &NutanixMachineList{})
}
