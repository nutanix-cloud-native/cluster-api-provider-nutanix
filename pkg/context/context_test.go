package context

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capiv1beta1 "sigs.k8s.io/cluster-api/api/core/v1beta1" //nolint:staticcheck // suppress complaining on Deprecated package
	ctlclient "sigs.k8s.io/controller-runtime/pkg/client"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	mockctlclient "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/mocks/ctlclient"
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
						capiv1beta1.MachineControlPlaneNameLabel: "",
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

func TestGetNutanixMachinesInCluster(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	t.Run("should return error when unable to list machines", func(t *testing.T) {
		mockClient := mockctlclient.NewMockClient(ctrl)
		ctx := context.Background()
		mockClient.EXPECT().List(ctx, gomock.Any(), gomock.Any(), gomock.Any()).Return(fmt.Errorf("list error"))

		clctx := &ClusterContext{
			Context: ctx,
			NutanixCluster: &infrav1.NutanixCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "default",
				},
			},
		}

		_, err := clctx.GetNutanixMachinesInCluster(mockClient)
		assert.Error(t, err)
	})

	t.Run("should return list of NutanixMachines when successful", func(t *testing.T) {
		mockClient := mockctlclient.NewMockClient(ctrl)
		ctx := context.Background()
		mockClient.EXPECT().List(ctx, gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
			func(ctx context.Context, list ctlclient.ObjectList, opts ...ctlclient.ListOption) error {
				machineList := list.(*infrav1.NutanixMachineList)
				machineList.Items = []infrav1.NutanixMachine{
					{ObjectMeta: metav1.ObjectMeta{Name: "test1", Namespace: "default"}},
					{ObjectMeta: metav1.ObjectMeta{Name: "test2", Namespace: "default"}},
				}
				return nil
			})

		clctx := &ClusterContext{
			Context: ctx,
			NutanixCluster: &infrav1.NutanixCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "default",
				},
			},
		}

		machines, err := clctx.GetNutanixMachinesInCluster(mockClient)
		assert.NoError(t, err)
		assert.Equal(t, 2, len(machines))
	})
}
