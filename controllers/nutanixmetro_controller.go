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

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	coreinformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/utils/ptr"                                     //nolint:staticcheck // suppress complaining on Deprecated package
	capiv1beta1 "sigs.k8s.io/cluster-api/api/core/v1beta1" //nolint:staticcheck // suppress complaining on Deprecated package
	capiv1beta2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	v1beta1conditions "sigs.k8s.io/cluster-api/util/deprecated/v1beta1/conditions" //nolint:staticcheck // suppress complaining on Deprecated package
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	ctrlutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// NutanixMetroReconciler reconciles an NutanixMetro object
type NutanixMetroReconciler struct {
	client.Client
	SecretInformer    coreinformers.SecretInformer
	ConfigMapInformer coreinformers.ConfigMapInformer
	Scheme            *runtime.Scheme
	controllerConfig  *ControllerConfig
}

func NewNutanixMetroReconciler(client client.Client, secretInformer coreinformers.SecretInformer, configMapInformer coreinformers.ConfigMapInformer, scheme *runtime.Scheme, copts ...ControllerConfigOpts) (*NutanixMetroReconciler, error) {
	controllerConf := &ControllerConfig{}
	for _, opt := range copts {
		if err := opt(controllerConf); err != nil {
			return nil, err
		}
	}

	return &NutanixMetroReconciler{
		Client:            client,
		SecretInformer:    secretInformer,
		ConfigMapInformer: configMapInformer,
		Scheme:            scheme,
		controllerConfig:  controllerConf,
	}, nil
}

// SetupWithManager sets up the NutanixMetro controller with the Manager.
func (r *NutanixMetroReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	copts := controller.Options{
		MaxConcurrentReconciles: r.controllerConfig.MaxConcurrentReconciles,
		RateLimiter:             r.controllerConfig.RateLimiter,
		SkipNameValidation:      ptr.To(r.controllerConfig.SkipNameValidation),
	}

	return ctrl.NewControllerManagedBy(mgr).
		Named("nutanixmetro-controller").
		For(&infrav1.NutanixMetro{}).
		Watches(
			&capiv1beta2.Machine{},
			handler.EnqueueRequestsFromMapFunc(
				r.mapMachineToNutanixMetro(),
			),
		).
		Watches(
			&infrav1.NutanixCluster{},
			handler.EnqueueRequestsFromMapFunc(
				r.mapNutanixClusterToNutanixMetro(),
			),
		).
		Watches(
			&infrav1.NutanixMetroSite{},
			handler.EnqueueRequestsFromMapFunc(
				r.mapNutanixMetroSiteToNutanixMetro(),
			),
		).
		WithOptions(copts).
		Complete(r)
}

func (r *NutanixMetroReconciler) mapMachineToNutanixMetro() handler.MapFunc {
	return func(ctx context.Context, o client.Object) []ctrl.Request {
		log := ctrl.LoggerFrom(ctx)
		machine, ok := o.(*capiv1beta2.Machine)
		if !ok {
			log.Error(fmt.Errorf("expected a Machine object but was %T", o), "unexpected type")
			return nil
		}

		reqs := make([]ctrl.Request, 0)
		fdStr := machine.Spec.FailureDomain
		if !isNutanixMetroFailureDomain(fdStr) {
			return reqs
		}

		objKey := client.ObjectKey{Name: fdStr[len(metroFailureDomainPrefix):], Namespace: machine.Namespace}
		reqs = append(reqs, ctrl.Request{NamespacedName: objKey})
		return reqs
	}
}

func (r *NutanixMetroReconciler) mapNutanixClusterToNutanixMetro() handler.MapFunc {
	return func(ctx context.Context, o client.Object) []ctrl.Request {
		log := ctrl.LoggerFrom(ctx)
		ntnxCluster, ok := o.(*infrav1.NutanixCluster)
		if !ok {
			log.Error(fmt.Errorf("expected a NutanixCluster object but was %T", o), "unexpected type")
			return nil
		}

		reqs := make([]ctrl.Request, 0)
		if len(ntnxCluster.Spec.MetroRefs) == 0 {
			return reqs
		}

		for _, metroRef := range ntnxCluster.Spec.MetroRefs {
			objKey := client.ObjectKey{Name: metroRef.Name, Namespace: ntnxCluster.Namespace}
			reqs = append(reqs, ctrl.Request{NamespacedName: objKey})
		}

		return reqs
	}
}

func (r *NutanixMetroReconciler) mapNutanixMetroSiteToNutanixMetro() handler.MapFunc {
	return func(ctx context.Context, o client.Object) []ctrl.Request {
		log := ctrl.LoggerFrom(ctx)
		metroSite, ok := o.(*infrav1.NutanixMetroSite)
		if !ok {
			log.Error(fmt.Errorf("expected a NutanixMetroSite object but was %T", o), "unexpected type")
			return nil
		}

		reqs := make([]ctrl.Request, 0)
		objKey := client.ObjectKey{Name: metroSite.Spec.MetroRef.Name, Namespace: metroSite.Namespace}
		reqs = append(reqs, ctrl.Request{NamespacedName: objKey})

		return reqs
	}
}

// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=machines;machines/status,verbs=get;list;watch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nutanixclusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nutanixmetroes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nutanixmetroes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nutanixmetroes/finalizers,verbs=get;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nutanixmetrosites,verbs=get;list;watch

func (r *NutanixMetroReconciler) Reconcile(ctx context.Context, req ctrl.Request) (res ctrl.Result, reterr error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Reconciling the NutanixMetro")

	// Fetch the NutanixMetro object
	metro := &infrav1.NutanixMetro{}
	if err := r.Get(ctx, req.NamespacedName, metro); err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			log.Info("The NutanixMetro object not found. Ignoring since object must be deleted.")
			return reconcile.Result{}, nil
		}

		// Error reading the object - requeue the request.
		log.Error(err, "failed to fetch the NutanixMetro object")
		return reconcile.Result{}, err
	}

	// Initialize the patch helper.
	patchHelper, err := patch.NewHelper(metro, r.Client)
	if err != nil {
		log.Error(err, "Failed to configure the patch helper")
		return ctrl.Result{Requeue: true}, nil
	}

	defer func() {
		// Always attempt to Patch the NutanixMetro object and its status after each reconciliation.
		if err := patchHelper.Patch(ctx, metro); err != nil {
			reterr = kerrors.NewAggregate([]error{reterr, err})
			log.Error(reterr, "Failed to patch NutanixMetro.")
		} else {
			log.Info("Patched NutanixMetro.", "status", metro.Status, "finalizers", metro.Finalizers)
		}
	}()

	// Add finalizer first if not set yet
	if !ctrlutil.ContainsFinalizer(metro, infrav1.NutanixMetroFinalizer) {
		if ctrlutil.AddFinalizer(metro, infrav1.NutanixMetroFinalizer) {
			// Add finalizer first avoid the race condition between init and delete.
			log.Info("Added the finalizer to the object", "finalizers", metro.Finalizers)
			return reconcile.Result{}, nil
		}
	}

	// Handle deletion
	if !metro.DeletionTimestamp.IsZero() {
		// List the Machines in the same namespace.
		machineList := &capiv1beta2.MachineList{}
		if err := r.List(ctx, machineList, client.InNamespace(metro.Namespace)); err != nil {
			return ctrl.Result{}, err
		}

		// List the NutanixClusters in the same namespace.
		nclList := &infrav1.NutanixClusterList{}
		if err := r.List(ctx, nclList, client.InNamespace(metro.Namespace)); err != nil {
			return ctrl.Result{}, err
		}

		// List the NutanixMetroSites in the same namespace.
		metrositeList := &infrav1.NutanixMetroSiteList{}
		if err := r.List(ctx, metrositeList, client.InNamespace(metro.Namespace)); err != nil {
			return ctrl.Result{}, err
		}

		err = r.reconcileDelete(ctx, metro, machineList.Items, nclList.Items, metrositeList.Items)
		return ctrl.Result{}, err
	}

	err = r.reconcileNormal(ctx, metro)
	return ctrl.Result{}, err
}

func (r *NutanixMetroReconciler) reconcileDelete(ctx context.Context, metro *infrav1.NutanixMetro, machines []capiv1beta2.Machine, ntxClusters []infrav1.NutanixCluster, metroSites []infrav1.NutanixMetroSite) error {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Handling NutanixMetro deletion")

	// Check if there are other objects reference this object in the same namespace.
	// usedItems is to hold the names of these machine objects using this Metro.
	usedItems := []string{}

	for _, ncl := range ntxClusters {
		if !ncl.DeletionTimestamp.IsZero() {
			continue
		}
		for _, metroRef := range ncl.Spec.MetroRefs {
			if metroRef.Name == metro.Name {
				usedItems = append(usedItems, fmt.Sprintf("nutanixCluster:%s", ncl.Name))
				break
			}
		}
	}

	for _, site := range metroSites {
		if !site.DeletionTimestamp.IsZero() {
			continue
		}
		if site.Spec.MetroRef.Name == metro.Name {
			usedItems = append(usedItems, fmt.Sprintf("metroSite:%s", site.Name))
		}
	}

	for _, m := range machines {
		if !m.DeletionTimestamp.IsZero() {
			continue
		}
		if isNutanixMetroFailureDomain(m.Spec.FailureDomain) &&
			m.Spec.FailureDomain[len(metroFailureDomainPrefix):] == metro.Name {
			usedItems = append(usedItems, fmt.Sprintf("machine:%s,cluster:%s", m.Name, m.Spec.ClusterName))
		}
	}

	if len(usedItems) == 0 {
		v1beta1conditions.MarkTrue(metro, infrav1.MetroSafeForDeletionCondition)
		// Remove the finalizer from the NutanixMetro object
		ctrlutil.RemoveFinalizer(metro, infrav1.NutanixMetroFinalizer)

		return nil
	}

	errMsg := fmt.Sprintf("The NutanixMetro object is referenced by other resources in the same namespace: %v", usedItems)
	v1beta1conditions.MarkFalse(metro, infrav1.MetroSafeForDeletionCondition,
		infrav1.MetroInUseReason, capiv1beta1.ConditionSeverityError, "%s", errMsg)

	reterr := fmt.Errorf("the NutanixMetro %s is not safe for deletion since it is in use", metro.Name)
	log.Error(reterr, errMsg)
	return reterr
}

func (r *NutanixMetroReconciler) reconcileNormal(ctx context.Context, metro *infrav1.NutanixMetro) error {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Handling NutanixMetro reconciling")

	errs := []error{}
	// validate the referenced NutanixFailureDomain objects exist
	for _, fdRef := range metro.Spec.FailureDomains {
		if _, err := getNutanixFailureDomainObject(ctx, r.Client, fdRef.Name, metro.Namespace); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		err := kerrors.NewAggregate(errs)
		v1beta1conditions.MarkFalse(metro, infrav1.MetroValidatedCondition, infrav1.MetroMisconfiguredReason,
			capiv1beta1.ConditionSeverityError, "%s", err.Error())

		return err
	}

	v1beta1conditions.MarkTrue(metro, infrav1.MetroValidatedCondition)
	return nil
}
