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
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	coreinformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	ctrlutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
)

// NutanixMachineTemplateReconciler reconciles a NutanixMachineTemplate object
type NutanixMachineTemplateReconciler struct {
	client.Client
	SecretInformer    coreinformers.SecretInformer
	ConfigMapInformer coreinformers.ConfigMapInformer
	Scheme            *runtime.Scheme
	controllerConfig  *ControllerConfig
}

func NewNutanixMachineTemplateReconciler(client client.Client, secretInformer coreinformers.SecretInformer, configMapInformer coreinformers.ConfigMapInformer, scheme *runtime.Scheme, copts ...ControllerConfigOpts) (*NutanixMachineTemplateReconciler, error) {
	controllerConf := &ControllerConfig{}
	for _, opt := range copts {
		if err := opt(controllerConf); err != nil {
			return nil, err
		}
	}

	return &NutanixMachineTemplateReconciler{
		Client:            client,
		SecretInformer:    secretInformer,
		ConfigMapInformer: configMapInformer,
		Scheme:            scheme,
		controllerConfig:  controllerConf,
	}, nil
}

//+kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nutanixmachinetemplates,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nutanixmachinetemplates/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nutanixmachinetemplates/finalizers,verbs=update

// Reconcile handles the reconciliation of NutanixMachineTemplate resources
func (r *NutanixMachineTemplateReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, retErr error) {
	log := log.FromContext(ctx)
	log.Info("[RECONCILE] Starting reconciliation", "namespacedName", req.NamespacedName, "namespace", req.Namespace, "name", req.Name)

	defer func() {
		log.Info("[RECONCILE] Finished reconciliation", "result", result, "error", retErr)
	}()

	// Fetch the NutanixMachineTemplate instance
	log.Info("[RECONCILE] Fetching NutanixMachineTemplate", "namespacedName", req.NamespacedName)
	nxMachineTemplate := &infrav1.NutanixMachineTemplate{}
	if err := r.Get(ctx, req.NamespacedName, nxMachineTemplate); err != nil {
		if errors.IsNotFound(err) {
			log.Info("[RECONCILE] NutanixMachineTemplate not found, ignoring since object must be deleted", "namespacedName", req.NamespacedName)
			return ctrl.Result{}, nil
		}
		log.Error(err, "[RECONCILE] Failed to get NutanixMachineTemplate", "namespacedName", req.NamespacedName)
		return ctrl.Result{}, err
	}

	log.Info("[RECONCILE] Successfully fetched NutanixMachineTemplate",
		"name", nxMachineTemplate.Name,
		"namespace", nxMachineTemplate.Namespace,
		"uid", nxMachineTemplate.UID,
		"generation", nxMachineTemplate.Generation,
		"resourceVersion", nxMachineTemplate.ResourceVersion,
		"deletionTimestamp", nxMachineTemplate.DeletionTimestamp,
		"finalizers", nxMachineTemplate.Finalizers)

	// Initialize the patch helper
	log.Info("[RECONCILE] Initializing patch helper")
	patchHelper, err := patch.NewHelper(nxMachineTemplate, r.Client)
	if err != nil {
		log.Error(err, "[RECONCILE] Failed to init patch helper", "name", nxMachineTemplate.Name)
		return ctrl.Result{}, err
	}
	log.Info("[RECONCILE] Successfully initialized patch helper")

	// Always attempt to patch the object and status after each reconciliation
	defer func() {
		log.Info("[RECONCILE] [DEFER] Executing defer function for patching",
			"name", nxMachineTemplate.Name,
			"generation", nxMachineTemplate.Generation,
			"finalizers", nxMachineTemplate.Finalizers,
			"capacity", nxMachineTemplate.Status.Capacity)

		if err := patchHelper.Patch(ctx, nxMachineTemplate); err != nil {
			log.Error(err, "[RECONCILE] [DEFER] Failed to patch NutanixMachineTemplate",
				"name", nxMachineTemplate.Name,
				"originalError", retErr)
			reterr := kerrors.NewAggregate([]error{retErr, err})
			retErr = reterr
		} else {
			log.Info("[RECONCILE] [DEFER] Successfully patched NutanixMachineTemplate", "name", nxMachineTemplate.Name)
		}
	}()

	// Handle deleted NutanixMachineTemplate
	if !nxMachineTemplate.DeletionTimestamp.IsZero() {
		log.Info("[RECONCILE] NutanixMachineTemplate is being deleted",
			"name", nxMachineTemplate.Name,
			"deletionTimestamp", nxMachineTemplate.DeletionTimestamp)
		return r.reconcileDelete(ctx, nxMachineTemplate)
	}

	// Handle non-deleted NutanixMachineTemplate
	log.Info("[RECONCILE] NutanixMachineTemplate is not being deleted, proceeding with normal reconciliation", "name", nxMachineTemplate.Name)
	return r.reconcileNormal(ctx, nxMachineTemplate)
}

func (r *NutanixMachineTemplateReconciler) reconcileNormal(ctx context.Context, nxMachineTemplate *infrav1.NutanixMachineTemplate) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	log.Info("[RECONCILE_NORMAL] Starting normal reconciliation",
		"name", nxMachineTemplate.Name,
		"namespace", nxMachineTemplate.Namespace,
		"finalizers", nxMachineTemplate.Finalizers)

	defer func() {
		log.Info("[RECONCILE_NORMAL] Finished normal reconciliation", "name", nxMachineTemplate.Name)
	}()

	// Add finalizer if it doesn't exist
	log.Info("[RECONCILE_NORMAL] Checking finalizers",
		"currentFinalizers", nxMachineTemplate.Finalizers,
		"expectedFinalizer", infrav1.NutanixMachineTemplateFinalizer)

	if !ctrlutil.ContainsFinalizer(nxMachineTemplate, infrav1.NutanixMachineTemplateFinalizer) {
		log.Info("[RECONCILE_NORMAL] Adding finalizer",
			"finalizer", infrav1.NutanixMachineTemplateFinalizer,
			"name", nxMachineTemplate.Name)
		ctrlutil.AddFinalizer(nxMachineTemplate, infrav1.NutanixMachineTemplateFinalizer)
		log.Info("[RECONCILE_NORMAL] Finalizer added, requeuing", "finalizers", nxMachineTemplate.Finalizers)
		return ctrl.Result{}, nil
	}
	log.Info("[RECONCILE_NORMAL] Finalizer already exists", "finalizers", nxMachineTemplate.Finalizers)

	// Calculate and update capacity based on machine template spec
	log.Info("[RECONCILE_NORMAL] Updating capacity", "name", nxMachineTemplate.Name)
	if err := r.updateCapacity(ctx, nxMachineTemplate); err != nil {
		log.Error(err, "[RECONCILE_NORMAL] Failed to update capacity",
			"name", nxMachineTemplate.Name,
			"spec", nxMachineTemplate.Spec.Template.Spec)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, err
	}

	log.Info("[RECONCILE_NORMAL] Normal reconciliation completed successfully",
		"name", nxMachineTemplate.Name,
		"capacity", nxMachineTemplate.Status.Capacity,
		"finalizers", nxMachineTemplate.Finalizers)

	log.Info("[RECONCILE_NORMAL] Successfully reconciled NutanixMachineTemplate")
	return ctrl.Result{}, nil
}

func (r *NutanixMachineTemplateReconciler) reconcileDelete(ctx context.Context, nxMachineTemplate *infrav1.NutanixMachineTemplate) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	log.Info("[RECONCILE_DELETE] Starting deletion reconciliation",
		"name", nxMachineTemplate.Name,
		"namespace", nxMachineTemplate.Namespace,
		"deletionTimestamp", nxMachineTemplate.DeletionTimestamp,
		"finalizers", nxMachineTemplate.Finalizers)

	defer func() {
		log.Info("[RECONCILE_DELETE] Finished deletion reconciliation", "name", nxMachineTemplate.Name)
	}()

	// Remove the finalizer to allow the object to be deleted
	log.Info("[RECONCILE_DELETE] Removing finalizer",
		"finalizer", infrav1.NutanixMachineTemplateFinalizer,
		"currentFinalizers", nxMachineTemplate.Finalizers,
		"name", nxMachineTemplate.Name)

	ctrlutil.RemoveFinalizer(nxMachineTemplate, infrav1.NutanixMachineTemplateFinalizer)

	log.Info("[RECONCILE_DELETE] Finalizer removed",
		"remainingFinalizers", nxMachineTemplate.Finalizers,
		"name", nxMachineTemplate.Name)

	log.Info("[RECONCILE_DELETE] Successfully reconciled NutanixMachineTemplate deletion")
	return ctrl.Result{}, nil
}

// updateCapacity calculates and updates the capacity field in the NutanixMachineTemplate status
func (r *NutanixMachineTemplateReconciler) updateCapacity(ctx context.Context, nxMachineTemplate *infrav1.NutanixMachineTemplate) error {
	log := log.FromContext(ctx)
	log.Info("[UPDATE_CAPACITY] Starting capacity update", "name", nxMachineTemplate.Name)

	defer func() {
		log.Info("[UPDATE_CAPACITY] Finished capacity update", "name", nxMachineTemplate.Name)
	}()

	// Initialize capacity if nil
	log.Info("[UPDATE_CAPACITY] Checking current capacity status",
		"currentCapacity", nxMachineTemplate.Status.Capacity,
		"isNil", nxMachineTemplate.Status.Capacity == nil)

	if nxMachineTemplate.Status.Capacity == nil {
		log.Info("[UPDATE_CAPACITY] Initializing capacity map", "name", nxMachineTemplate.Name)
		nxMachineTemplate.Status.Capacity = make(corev1.ResourceList)
	} else {
		log.Info("[UPDATE_CAPACITY] Capacity map already exists", "currentCapacity", nxMachineTemplate.Status.Capacity)
	}

	// Extract resource information from the machine template spec
	machineSpec := nxMachineTemplate.Spec.Template.Spec
	log.Info("[UPDATE_CAPACITY] Extracted machine spec",
		"vcpusPerSocket", machineSpec.VCPUsPerSocket,
		"vcpuSockets", machineSpec.VCPUSockets,
		"memorySize", machineSpec.MemorySize.String(),
		"name", nxMachineTemplate.Name)

	// Set CPU capacity
	totalCPUs := machineSpec.VCPUsPerSocket * machineSpec.VCPUSockets
	log.Info("[UPDATE_CAPACITY] Calculating CPU capacity",
		"vcpusPerSocket", machineSpec.VCPUsPerSocket,
		"vcpuSockets", machineSpec.VCPUSockets,
		"totalCPUs", totalCPUs)

	cpuQuantity := resource.NewQuantity(int64(totalCPUs), resource.DecimalSI)
	nxMachineTemplate.Status.Capacity[infrav1.AutoscalerResourceCPU] = *cpuQuantity
	log.Info("[UPDATE_CAPACITY] Set CPU capacity", "cpuQuantity", cpuQuantity.String())

	// Set Memory capacity (convert from MemorySize to memory in bytes)
	log.Info("[UPDATE_CAPACITY] Setting memory capacity", "originalMemorySize", machineSpec.MemorySize.String())
	memoryQuantity := machineSpec.MemorySize.DeepCopy()
	nxMachineTemplate.Status.Capacity[infrav1.AutoscalerResourceMemory] = memoryQuantity
	log.Info("[UPDATE_CAPACITY] Set memory capacity", "memoryQuantity", memoryQuantity.String())

	log.Info("[UPDATE_CAPACITY] Successfully updated NutanixMachineTemplate capacity",
		"name", nxMachineTemplate.Name,
		"CPU", cpuQuantity.String(),
		"Memory", memoryQuantity.String(),
		"fullCapacity", nxMachineTemplate.Status.Capacity)

	return nil
}

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
