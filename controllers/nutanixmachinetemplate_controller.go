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

package controllers

import (
	"context"
	"fmt"
	"reflect"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
)

// NutanixMachineTemplateReconciler reconciles NutanixMachineTemplate objects
// to publish resource capacity in status for autoscaler scale-from-zero support.
type NutanixMachineTemplateReconciler struct {
	client.Client
	Scheme           *runtime.Scheme
	controllerConfig *ControllerConfig
}

func NewNutanixMachineTemplateReconciler(
	client client.Client,
	scheme *runtime.Scheme,
	copts ...ControllerConfigOpts,
) (*NutanixMachineTemplateReconciler, error) {
	controllerConf := &ControllerConfig{}
	for _, opt := range copts {
		if err := opt(controllerConf); err != nil {
			return nil, err
		}
	}

	return &NutanixMachineTemplateReconciler{
		Client:           client,
		Scheme:           scheme,
		controllerConfig: controllerConf,
	}, nil
}

//+kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nutanixmachinetemplates,verbs=get;list;watch
//+kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nutanixmachinetemplates/status,verbs=get;update;patch

// SetupWithManager sets up the NutanixMachineTemplate controller with the Manager.
func (r *NutanixMachineTemplateReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	copts := controller.Options{
		MaxConcurrentReconciles: r.controllerConfig.MaxConcurrentReconciles,
		RateLimiter:             r.controllerConfig.RateLimiter,
		SkipNameValidation:      ptr.To(r.controllerConfig.SkipNameValidation),
	}

	return ctrl.NewControllerManagedBy(mgr).
		Named("nutanixmachinetemplate-controller").
		For(&infrav1.NutanixMachineTemplate{}).
		WithOptions(copts).
		Complete(r)
}

// Reconcile computes and publishes resource capacity on NutanixMachineTemplate.Status.Capacity.
// This capacity is consumed by the Cluster Autoscaler for scale-from-zero decisions
// as defined in the CAPI opt-in autoscaling from zero enhancement proposal.
func (r *NutanixMachineTemplateReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Reconciling NutanixMachineTemplate")

	nmt := &infrav1.NutanixMachineTemplate{}
	if err := r.Get(ctx, req.NamespacedName, nmt); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	machineSpec := nmt.Spec.Template.Spec
	capacity := make(corev1.ResourceList)
	computeDirectCapacity(machineSpec, capacity)

	if !reflect.DeepEqual(nmt.Status.Capacity, capacity) {
		log.Info("Updating capacity", "capacity", capacity)
		nmt.Status.Capacity = capacity
		if err := r.Status().Update(ctx, nmt); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update NutanixMachineTemplate status: %w", err)
		}
	}

	return ctrl.Result{}, nil
}

// computeDirectCapacity calculates capacity from the inline spec fields (CPU, memory, GPUs).
func computeDirectCapacity(machineSpec infrav1.NutanixMachineSpec, capacity corev1.ResourceList) {
	totalCPUs := int64(machineSpec.VCPUsPerSocket) * int64(machineSpec.VCPUSockets)
	capacity[corev1.ResourceCPU] = *resource.NewQuantity(totalCPUs, resource.DecimalSI)
	capacity[corev1.ResourceMemory] = machineSpec.MemorySize.DeepCopy()

	if len(machineSpec.GPUs) > 0 {
		capacity[corev1.ResourceName("nvidia.com/gpu")] = *resource.NewQuantity(int64(len(machineSpec.GPUs)), resource.DecimalSI)
	}
}
