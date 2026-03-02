/*
Copyright 2026 Nutanix

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

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	coreinformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/utils/ptr"
	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta1"                              //nolint:staticcheck // suppress complaining on Deprecated package
	v1beta1conditions "sigs.k8s.io/cluster-api/util/deprecated/v1beta1/conditions" //nolint:staticcheck // suppress complaining on Deprecated package
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	ctrlutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
)

// NutanixMetroVMPlacementReconciler reconciles an NutanixMetroVMPlacement object
type NutanixMetroVMPlacementReconciler struct {
	client.Client
	SecretInformer    coreinformers.SecretInformer
	ConfigMapInformer coreinformers.ConfigMapInformer
	Scheme            *runtime.Scheme
	controllerConfig  *ControllerConfig
}

func NewNutanixMetroVMPlacementReconciler(client client.Client, secretInformer coreinformers.SecretInformer, configMapInformer coreinformers.ConfigMapInformer, scheme *runtime.Scheme, copts ...ControllerConfigOpts) (*NutanixMetroVMPlacementReconciler, error) {
	controllerConf := &ControllerConfig{}
	for _, opt := range copts {
		if err := opt(controllerConf); err != nil {
			return nil, err
		}
	}

	return &NutanixMetroVMPlacementReconciler{
		Client:            client,
		SecretInformer:    secretInformer,
		ConfigMapInformer: configMapInformer,
		Scheme:            scheme,
		controllerConfig:  controllerConf,
	}, nil
}

// SetupWithManager sets up the NutanixMetroVMPlacement controller with the Manager.
func (r *NutanixMetroVMPlacementReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	copts := controller.Options{
		MaxConcurrentReconciles: r.controllerConfig.MaxConcurrentReconciles,
		RateLimiter:             r.controllerConfig.RateLimiter,
		SkipNameValidation:      ptr.To(r.controllerConfig.SkipNameValidation),
	}

	return ctrl.NewControllerManagedBy(mgr).
		Named("nutanixvmplacement-controller").
		For(&infrav1.NutanixMetroVMPlacement{}).
		Watches(
			&capiv1.Machine{},
			handler.EnqueueRequestsFromMapFunc(
				r.mapMachineToNutanixMetroVMPlacement(),
			),
		).
		WithOptions(copts).
		Complete(r)
}

func (r *NutanixMetroVMPlacementReconciler) mapMachineToNutanixMetroVMPlacement() handler.MapFunc {
	return func(ctx context.Context, o client.Object) []ctrl.Request {
		log := ctrl.LoggerFrom(ctx)
		machine, ok := o.(*capiv1.Machine)
		if !ok {
			log.Error(fmt.Errorf("expected a Machine object but was %T", o), "unexpected type")
			return nil
		}

		reqs := make([]ctrl.Request, 0)
		// Fetch the NutanixMetroVMPlacement object in the local namespace
		fdStr := machine.Spec.FailureDomain
		vmPlacementName := getMetroVMPlacementObjectName(fdStr)
		if vmPlacementName == nil || *vmPlacementName == "" {
			return reqs
		}

		metroVMPlacement := &infrav1.NutanixMetroVMPlacement{}
		if err := r.Get(ctx, client.ObjectKey{Name: *vmPlacementName, Namespace: machine.Namespace}, metroVMPlacement); err != nil {
			log.Error(err, "Failed to fetch the NutanixMetroVMPlacement object %q referenced with Machine %q spec.failureDomain: %s", *vmPlacementName, machine.Name, *fdStr)
			return nil
		}

		objKey := client.ObjectKey{Name: metroVMPlacement.Name, Namespace: metroVMPlacement.Namespace}
		reqs = append(reqs, ctrl.Request{NamespacedName: objKey})
		return reqs
	}
}

// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=machines;machines/status,verbs=get;list;watch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nutanixmetrovmplacements,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nutanixmetrovmplacements/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nutanixmetrovmplacements/finalizers,verbs=get;update;patch

func (r *NutanixMetroVMPlacementReconciler) Reconcile(ctx context.Context, req ctrl.Request) (res ctrl.Result, reterr error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Reconciling the NutanixMetroVMPlacement")

	// Fetch the NutanixMetroVMPlacement object
	metroVMPlacement := &infrav1.NutanixMetroVMPlacement{}
	if err := r.Get(ctx, req.NamespacedName, metroVMPlacement); err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			log.Info("The NutanixMetroVMPlacement object not found. Ignoring since object must be deleted.")
			return reconcile.Result{}, nil
		}

		// Error reading the object - requeue the request.
		log.Error(err, "failed to fetch the NutanixMetroVMPlacement object")
		return reconcile.Result{}, err
	}

	// Initialize the patch helper.
	patchHelper, err := patch.NewHelper(metroVMPlacement, r.Client)
	if err != nil {
		log.Error(err, "Failed to configure the patch helper")
		return ctrl.Result{Requeue: true}, nil
	}

	defer func() {
		// Always attempt to Patch the NutanixMetroVMPlacement object and its status after each reconciliation.
		if err := patchHelper.Patch(ctx, metroVMPlacement); err != nil {
			reterr = kerrors.NewAggregate([]error{reterr, err})
			log.Error(reterr, "Failed to patch NutanixMetroVMPlacement.")
		} else {
			log.Info("Patched NutanixMetroVMPlacement.", "status", metroVMPlacement.Status, "finalizers", metroVMPlacement.Finalizers)
		}
	}()

	// Add finalizer first if not set yet
	if !ctrlutil.ContainsFinalizer(metroVMPlacement, infrav1.NutanixMetroVMPlacementFinalizer) {
		if ctrlutil.AddFinalizer(metroVMPlacement, infrav1.NutanixMetroVMPlacementFinalizer) {
			// Add finalizer first avoid the race condition between init and delete.
			log.Info("Added the finalizer to the object", "finalizers", metroVMPlacement.Finalizers)
			return reconcile.Result{}, nil
		}
	}

	// Handle deletion
	if !metroVMPlacement.DeletionTimestamp.IsZero() {
		// List the Machines in the same namespace as metroVMPlacement.
		machineList := &capiv1.MachineList{}
		if err := r.List(ctx, machineList, client.InNamespace(metroVMPlacement.Namespace)); err != nil {
			return ctrl.Result{}, err
		}

		err = r.reconcileDelete(ctx, metroVMPlacement, machineList.Items)
		return ctrl.Result{}, err
	}

	err = r.reconcileNormal(ctx, metroVMPlacement)
	return ctrl.Result{}, err
}

func (r *NutanixMetroVMPlacementReconciler) reconcileDelete(ctx context.Context, metroVMPlacement *infrav1.NutanixMetroVMPlacement, machines []capiv1.Machine) error {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Handling NutanixMetroVMPlacement deletion")

	// Check if there are Machine objects reference this NutanixMetroVMPlacement in their spec.failureDomain.
	// usedItems is to hold the names of the machine/cluster using this metroVMPlacement.
	usedItems := []string{}

	for _, m := range machines {
		if !m.DeletionTimestamp.IsZero() {
			continue
		}

		if isMetroVMPlacementFailureDomain(m.Spec.FailureDomain) {
			usedItems = append(usedItems, fmt.Sprintf("machine:%s,cluster:%s", m.Name, m.Spec.ClusterName))
		}
	}

	if len(usedItems) == 0 {
		v1beta1conditions.MarkTrue(metroVMPlacement, infrav1.MetroVMPlacementSafeForDeletionCondition)

		// Remove the finalizer from the NutanixMetroVMPlacement object
		ctrlutil.RemoveFinalizer(metroVMPlacement, infrav1.NutanixMetroVMPlacementFinalizer)

		return nil
	}

	errMsg := fmt.Sprintf("The NutanixMetroVMPlacement object is used by machines: %v", usedItems)
	v1beta1conditions.MarkFalse(metroVMPlacement, infrav1.MetroVMPlacementSafeForDeletionCondition,
		infrav1.MetroVMPlacementInUseReason, capiv1.ConditionSeverityError, "%s", errMsg)

	reterr := fmt.Errorf("the NutanixMetroVMPlacement %q is not safe for deletion since it is in use", metroVMPlacement.Name)
	log.Error(reterr, errMsg)
	return reterr
}

func (r *NutanixMetroVMPlacementReconciler) reconcileNormal(ctx context.Context, metroVMPlacement *infrav1.NutanixMetroVMPlacement) error {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Handling NutanixMetroVMPlacement reconciling")

	// validate spec.preferredFailureDomain
	if metroVMPlacement.Spec.PlacementStrategy == infrav1.PreferredStrategy {
		if metroVMPlacement.Spec.PreferredFailureDomain == nil || *metroVMPlacement.Spec.PreferredFailureDomain == "" {
			return fmt.Errorf("preferredFailureDomain is required for Preferred strategy")
		}

		preferredFD := *metroVMPlacement.Spec.PreferredFailureDomain
		found := false
		for _, fd := range metroVMPlacement.Spec.FailureDomains {
			if fd.Name == preferredFD {
				found = true
				break
			}
		}

		if !found {
			return fmt.Errorf("preferredFailureDomain %q is invalid. It must be one of the NutanixMetroVMPlacement object's spec.failureDomains name", preferredFD)
		}
	}

	return nil
}
