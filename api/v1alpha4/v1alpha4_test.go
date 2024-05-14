package v1alpha4

import (
	"testing"

	"github.com/nutanix-cloud-native/prism-go-client/environment/credentials"
	"github.com/nutanix-cloud-native/prism-go-client/utils"
	"github.com/stretchr/testify/assert"
	"sigs.k8s.io/cluster-api/api/v1alpha4" //nolint:staticcheck // Ignoring v1alpha4 deprecation linter warnings due to inactive dev branch
)

func TestNutanixCluster(t *testing.T) {
	// Tests DeepCopyObject on NutanixCluster
	clusterSpec := NutanixClusterSpec{
		ControlPlaneEndpoint: v1alpha4.APIEndpoint{},
		PrismCentral:         &credentials.NutanixPrismEndpoint{},
		FailureDomains:       []NutanixFailureDomain{},
	}

	clusterStatus := NutanixClusterStatus{
		FailureDomains: map[string]v1alpha4.FailureDomainSpec{},
	}

	cluster := NutanixCluster{
		Spec:   clusterSpec,
		Status: clusterStatus,
	}

	obj := cluster.DeepCopyObject()
	assert.NotNil(t, obj)
}

func TestNutanixMachine(t *testing.T) {
	// Tests DeepCopyObject on NutanixMachine
	machineSpec := NutanixMachineSpec{
		Image: NutanixResourceIdentifier{
			Type: NutanixIdentifierName,
			Name: utils.StringPtr("test-image"),
		},
		AdditionalCategories: []NutanixCategoryIdentifier{
			{
				Key:   "key",
				Value: "value",
			},
		},
	}

	machineStatus := NutanixMachineStatus{
		Addresses: []v1alpha4.MachineAddress{},
	}

	machine := NutanixMachine{
		Spec:   machineSpec,
		Status: machineStatus,
	}

	obj := machine.DeepCopyObject()
	assert.NotNil(t, obj)
}
