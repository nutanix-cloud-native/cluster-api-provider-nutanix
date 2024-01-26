package context

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
)

func TestIsControlPlaneMachine(t *testing.T) {
	tests := []struct {
		name     string
		machine  *infrav1.NutanixMachine
		expected bool
	}{
		{
			name:     "control plane machine",
			expected: true,
			machine: &infrav1.NutanixMachine{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						capiv1.MachineControlPlaneLabelName: "",
					},
				},
			},
		},
		{
			name:     "worker machine",
			expected: false,
			machine: &infrav1.NutanixMachine{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{},
				},
			},
		},
		{
			name:     "nil machine",
			expected: false,
			machine:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsControlPlaneMachine(tt.machine); got != tt.expected {
				t.Errorf("IsControlPlaneMachine() = %v, want %v", got, tt.expected)
			}
		})
	}
}
