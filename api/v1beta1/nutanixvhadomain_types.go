package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

const (
	// NutanixVHADomainKind represents the Kind of NutanixVHADomain
	NutanixVHADomainKind = "NutanixVHADomain"
)

// NutanixVHADomainSpec defines the desired state of NutanixVHADomain
type NutanixVHADomainSpec struct {
	// sites defines the Prism Element sites of the vHA domain.
	// +kubebuilder:validation:MinItems=2
	Sites []Site `json:"sites"`
}

// NutanixVHADomain is the Schema for the vHA Domain API.
type NutanixVHADomain struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              NutanixVHADomainSpec   `json:"spec,omitempty"`
	Status            NutanixVHADomainStatus `json:"status,omitempty"`
}

type Site struct {
	// prismElement is the identifier of the Prism Element cluster
	// of the vHA Domain Site.
	// +kubebuilder:validation:Required
	PrismElement NutanixResourceIdentifier `json:"prismElement"`

	// subnets holds a list of identifiers (one or more) of the vHA Domain Site.
	// The subnets should already exist in PC.
	// +kubebuilder:validation:MinItems=1
	Subnets []NutanixResourceIdentifier `json:"subnets"`

	// If the below fields (categories, protectionPolicyUUID and recoveryPlanUUIDs)
	// are not set, the reconciler should create these PC constructs, before
	// provisioning the K8s cluster to the vHA Domain Site. Otherwise, it should
	// validate that the corresponding PC constructs are valid.

	// categories of the vHA domain corresponding to the vHADomain Site.
	// If this field is not set,the default will be added with the format:
	// "ahvMetro-nkp-cluster-<clusterName>:<peName>".
	// +optional
	Categories []NutanixCategoryIdentifier `json:"categories,omitempty"`

	// The created vHADomain Protection Policy's UUID.
	// The field will be set once the Protection Policy is created in PC.
	// +optional
	ProtectionPolicyUUID *string `json:"protectionPolicyUUID,omitempty"`

	// The vHADomain Recovery Plan UUID corresponding to the vHADomain Site
	// The field will be set once the Recovery Plan is created in PC.
	// +optional
	RecoveryPlanUUID *string `json:"recoveryPlanUUIDs,omitempty"`
}

// NutanixVHADomainStatus defines the state of the NutanixVHADomain resource.
type NutanixVHADomainStatus struct {
	// conditions represent the latest states of the vHA domain,
	// including if the vHA domain PC entities (categories, protection policy,
	// and recovery plans) are successfully created.
	// +optional
	Conditions []capiv1.Condition `json:"conditions,omitempty"`

	// Will be set in case of reconciling failure of the NutanixVHADomain resource.
	// +optional
	FailureReason *string `json:"failureReason,omitempty"`

	// Will be set in case of reconciling failure of the NutanixVHADomain resource.
	// +optional
	FailureMessage *string `json:"failureMessage,omitempty"`

	// ready is set to true when the vHA domain PC entities (categories, protection
	// policy, and recovery plan) are successfully created and/or validated.
	Ready bool `json:"ready"`
}
