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
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	capiv1beta2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

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

const (
	// CapacityAnnotationCPU is the annotation key for CPU capacity on MachineDeployments,
	// consumed by the cluster-autoscaler for scale-from-zero decisions.
	CapacityAnnotationCPU = "capacity.cluster-autoscaler.kubernetes.io/cpu"

	// CapacityAnnotationMemory is the annotation key for memory capacity on MachineDeployments,
	// consumed by the cluster-autoscaler for scale-from-zero decisions.
	CapacityAnnotationMemory = "capacity.cluster-autoscaler.kubernetes.io/memory"

	// CapacityAnnotationGPUCount is the annotation key for GPU count on MachineDeployments.
	CapacityAnnotationGPUCount = "capacity.cluster-autoscaler.kubernetes.io/gpu-count"

	// CapacityAnnotationGPUType is the annotation key for GPU type on MachineDeployments.
	CapacityAnnotationGPUType = "capacity.cluster-autoscaler.kubernetes.io/gpu-type"

	nutanixMachineTemplateKind = "NutanixMachineTemplate"
	infraAPIGroup              = "infrastructure.cluster.x-k8s.io"
)

//+kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nutanixmachinetemplates,verbs=get;list;watch
//+kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nutanixmachinetemplates/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=cluster.x-k8s.io,resources=machinedeployments,verbs=get;list;watch;patch

// SetupWithManager sets up the NutanixMachineTemplate controller with the Manager.
// It also watches MachineDeployments so that capacity annotations are propagated
// whenever a MachineDeployment is created or updated with a reference to a NutanixMachineTemplate.
func (r *NutanixMachineTemplateReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	copts := controller.Options{
		MaxConcurrentReconciles: r.controllerConfig.MaxConcurrentReconciles,
		RateLimiter:             r.controllerConfig.RateLimiter,
		SkipNameValidation:      ptr.To(r.controllerConfig.SkipNameValidation),
	}

	return ctrl.NewControllerManagedBy(mgr).
		Named("nutanixmachinetemplate-controller").
		For(&infrav1.NutanixMachineTemplate{}).
		Watches(
			&capiv1beta2.MachineDeployment{},
			handler.EnqueueRequestsFromMapFunc(r.machineDeploymentToNutanixMachineTemplate),
		).
		WithOptions(copts).
		Complete(r)
}

// machineDeploymentToNutanixMachineTemplate maps a MachineDeployment to the
// NutanixMachineTemplate it references via infrastructureRef, so that template
// reconciliation is triggered when a MachineDeployment is created or updated.
func (r *NutanixMachineTemplateReconciler) machineDeploymentToNutanixMachineTemplate(
	ctx context.Context, obj client.Object,
) []reconcile.Request {
	md, ok := obj.(*capiv1beta2.MachineDeployment)
	if !ok {
		return nil
	}

	ref := md.Spec.Template.Spec.InfrastructureRef
	if ref.Kind != nutanixMachineTemplateKind {
		return nil
	}

	return []reconcile.Request{
		{NamespacedName: types.NamespacedName{
			Name:      ref.Name,
			Namespace: md.Namespace,
		}},
	}
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

	// Propagate capacity as annotations on MachineDeployments that reference this template.
	// This enables the cluster-autoscaler to perform scale-from-zero decisions.
	//
	// Users must still add the autoscaling range annotations themselves:
	//   cluster.x-k8s.io/cluster-api-autoscaler-node-group-min-size
	//   cluster.x-k8s.io/cluster-api-autoscaler-node-group-max-size
	if err := r.propagateCapacityAnnotations(ctx, nmt, capacity); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to propagate capacity annotations: %w", err)
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

// propagateCapacityAnnotations sets capacity.cluster-autoscaler.kubernetes.io/* annotations
// on every MachineDeployment in the same namespace whose infrastructureRef points at this
// NutanixMachineTemplate. This enables the cluster-autoscaler to make scale-from-zero
// decisions without requiring users to compute and set capacity annotations manually.
func (r *NutanixMachineTemplateReconciler) propagateCapacityAnnotations(
	ctx context.Context,
	nmt *infrav1.NutanixMachineTemplate,
	capacity corev1.ResourceList,
) error {
	log := ctrl.LoggerFrom(ctx)

	mdList := &capiv1beta2.MachineDeploymentList{}
	if err := r.List(ctx, mdList, client.InNamespace(nmt.Namespace)); err != nil {
		return fmt.Errorf("failed to list MachineDeployments in namespace %s: %w", nmt.Namespace, err)
	}

	desired := buildCapacityAnnotations(capacity)

	for i := range mdList.Items {
		md := &mdList.Items[i]
		ref := md.Spec.Template.Spec.InfrastructureRef
		if ref.Kind != nutanixMachineTemplateKind || ref.Name != nmt.Name {
			continue
		}

		if annotationsUpToDate(md.Annotations, desired) {
			continue
		}

		patch := client.MergeFrom(md.DeepCopy())
		if md.Annotations == nil {
			md.Annotations = make(map[string]string, len(desired))
		}
		for k, v := range desired {
			md.Annotations[k] = v
		}

		if err := r.Patch(ctx, md, patch); err != nil {
			return fmt.Errorf("failed to patch MachineDeployment %s/%s annotations: %w", md.Namespace, md.Name, err)
		}
		log.Info("Propagated capacity annotations to MachineDeployment",
			"machineDeployment", md.Name, "annotations", desired)
	}

	return nil
}

// buildCapacityAnnotations converts a ResourceList into the annotation map expected
// by the cluster-autoscaler for scale-from-zero.
func buildCapacityAnnotations(capacity corev1.ResourceList) map[string]string {
	annotations := make(map[string]string)

	if cpu, ok := capacity[corev1.ResourceCPU]; ok {
		annotations[CapacityAnnotationCPU] = cpu.String()
	}
	if mem, ok := capacity[corev1.ResourceMemory]; ok {
		annotations[CapacityAnnotationMemory] = mem.String()
	}
	if gpuCount, ok := capacity[corev1.ResourceName("nvidia.com/gpu")]; ok {
		annotations[CapacityAnnotationGPUCount] = gpuCount.String()
	}

	return annotations
}

// annotationsUpToDate returns true if every key in desired is present in current
// with the same value.
func annotationsUpToDate(current map[string]string, desired map[string]string) bool {
	if current == nil && len(desired) > 0 {
		return false
	}
	for k, v := range desired {
		if current[k] != v {
			return false
		}
	}
	return true
}
