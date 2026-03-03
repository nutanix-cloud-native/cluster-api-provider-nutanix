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
	"k8s.io/utils/ptr"
	capiv1beta1 "sigs.k8s.io/cluster-api/api/core/v1beta1"                         //nolint:staticcheck // suppress complaining on Deprecated package
	v1beta1conditions "sigs.k8s.io/cluster-api/util/deprecated/v1beta1/conditions" //nolint:staticcheck // suppress complaining on Deprecated package
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	ctrlutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// NutanixMetroDomainReconciler reconciles an NutanixMetroDomain object
type NutanixMetroDomainReconciler struct {
	client.Client
	SecretInformer    coreinformers.SecretInformer
	ConfigMapInformer coreinformers.ConfigMapInformer
	Scheme            *runtime.Scheme
	controllerConfig  *ControllerConfig
}

func NewNutanixMetroDomainReconciler(client client.Client, secretInformer coreinformers.SecretInformer, configMapInformer coreinformers.ConfigMapInformer, scheme *runtime.Scheme, copts ...ControllerConfigOpts) (*NutanixMetroDomainReconciler, error) {
	controllerConf := &ControllerConfig{}
	for _, opt := range copts {
		if err := opt(controllerConf); err != nil {
			return nil, err
		}
	}

	return &NutanixMetroDomainReconciler{
		Client:            client,
		SecretInformer:    secretInformer,
		ConfigMapInformer: configMapInformer,
		Scheme:            scheme,
		controllerConfig:  controllerConf,
	}, nil
}

// SetupWithManager sets up the NutanixMetroDomain controller with the Manager.
func (r *NutanixMetroDomainReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	copts := controller.Options{
		MaxConcurrentReconciles: r.controllerConfig.MaxConcurrentReconciles,
		RateLimiter:             r.controllerConfig.RateLimiter,
		SkipNameValidation:      ptr.To(r.controllerConfig.SkipNameValidation),
	}

	return ctrl.NewControllerManagedBy(mgr).
		Named("NutanixMetroDomain-controller").
		For(&infrav1.NutanixMetroDomain{}).
		Watches(
			&infrav1.NutanixCluster{},
			handler.EnqueueRequestsFromMapFunc(
				r.mapNutanixClusterToNutanixMetroDomain(),
			),
		).
		WithOptions(copts).
		Complete(r)
}

func (r *NutanixMetroDomainReconciler) mapNutanixClusterToNutanixMetroDomain() handler.MapFunc {
	return func(ctx context.Context, o client.Object) []ctrl.Request {
		log := ctrl.LoggerFrom(ctx)
		ncl, ok := o.(*infrav1.NutanixCluster)
		if !ok {
			log.Error(fmt.Errorf("expected a NutanixCluster object but was %T", o), "unexpected type")
			return nil
		}

		reqs := make([]ctrl.Request, 0)
		for _, ref := range ncl.Spec.MetroDomains {
			// Fetch the NutanixMetroDomain object in the local namespace
			metroDomainObj := &infrav1.NutanixMetroDomain{}
			metroDomainkey := client.ObjectKey{Namespace: ncl.Namespace, Name: ref.Name}

			if err := r.Get(ctx, metroDomainkey, metroDomainObj); err != nil {
				log.Error(err, fmt.Sprintf("Failed to fetch the NutanixMetroDomain object %q referenced by NutanixCluster %q spec.metroDomains", ref.Name, ncl.Name))
				continue
			}

			objKey := client.ObjectKey{Name: metroDomainObj.Name, Namespace: metroDomainObj.Namespace}
			reqs = append(reqs, ctrl.Request{NamespacedName: objKey})
		}
		return reqs
	}
}

// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nutanixclusters;nutanixclusters/status,verbs=get;list;watch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nutanixmetrodomains,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nutanixmetrodomains/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nutanixmetrodomains/finalizers,verbs=get;update;patch

func (r *NutanixMetroDomainReconciler) Reconcile(ctx context.Context, req ctrl.Request) (res ctrl.Result, reterr error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Reconciling the NutanixMetroDomain")

	// Fetch the NutanixMetroDomain object
	metroDomain := &infrav1.NutanixMetroDomain{}
	if err := r.Get(ctx, req.NamespacedName, metroDomain); err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			log.Info("The NutanixMetroDomain object not found. Ignoring since object must be deleted.")
			return reconcile.Result{}, nil
		}

		// Error reading the object - requeue the request.
		log.Error(err, "failed to fetch the NutanixMetroDomain object")
		return reconcile.Result{}, err
	}

	// Initialize the patch helper.
	patchHelper, err := patch.NewHelper(metroDomain, r.Client)
	if err != nil {
		log.Error(err, "Failed to configure the patch helper")
		return ctrl.Result{Requeue: true}, nil
	}

	defer func() {
		// Always attempt to Patch the NutanixMetroDomain object and its status after each reconciliation.
		if err := patchHelper.Patch(ctx, metroDomain); err != nil {
			reterr = kerrors.NewAggregate([]error{reterr, err})
			log.Error(reterr, "Failed to patch NutanixMetroDomain.")
		} else {
			log.Info("Patched NutanixMetroDomain.", "status", metroDomain.Status, "finalizers", metroDomain.Finalizers)
		}
	}()

	// Add finalizer first if not set yet
	if !ctrlutil.ContainsFinalizer(metroDomain, infrav1.NutanixMetroDomainFinalizer) {
		if ctrlutil.AddFinalizer(metroDomain, infrav1.NutanixMetroDomainFinalizer) {
			// Add finalizer first avoid the race condition between init and delete.
			log.Info("Added the finalizer to the object", "finalizers", metroDomain.Finalizers)
			return reconcile.Result{}, nil
		}
	}

	// Handle deletion
	if !metroDomain.DeletionTimestamp.IsZero() {
		// List the NutanixCluster in the same namespace as metroDomain.
		nclList := &infrav1.NutanixClusterList{}
		if err := r.List(ctx, nclList, client.InNamespace(metroDomain.Namespace)); err != nil {
			return ctrl.Result{}, err
		}

		err = r.reconcileDelete(ctx, metroDomain, nclList.Items)
		return ctrl.Result{}, err
	}

	err = r.reconcileNormal(ctx, metroDomain)
	return ctrl.Result{}, err
}

func (r *NutanixMetroDomainReconciler) reconcileDelete(ctx context.Context, metroDomain *infrav1.NutanixMetroDomain, ncls []infrav1.NutanixCluster) error {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Handling NutanixMetroDomain deletion")

	// Check if there are NutanixCluster objects reference this NutanixMetroDomain in their spec.metroDomains.
	// nclNames is the name list of the NutanixCluster having reference to this metroZone.
	nclNames := []string{}

	for _, ncl := range ncls {
		if !ncl.DeletionTimestamp.IsZero() || ncl.Spec.MetroDomains == nil {
			continue
		}

		for _, ref := range ncl.Spec.MetroDomains {
			if ref.Name == metroDomain.Name {
				nclNames = append(nclNames, ncl.Name)
				break
			}
		}
	}

	if len(nclNames) == 0 {
		v1beta1conditions.MarkTrue(metroDomain, infrav1.MetroDomainSafeForDeletionCondition)

		// Remove the finalizer from the NutanixMetroDomain object
		ctrlutil.RemoveFinalizer(metroDomain, infrav1.NutanixMetroDomainFinalizer)
		log.Info("Removed finalizer %q from the NutanixMetroDomain object %q", infrav1.NutanixMetroDomainFinalizer, metroDomain.Name)
		return nil
	}

	errMsg := fmt.Sprintf("The NutanixMetroDomain object is used by NutanixCusters: %v", nclNames)
	v1beta1conditions.MarkFalse(metroDomain, infrav1.MetroDomainSafeForDeletionCondition,
		infrav1.MetroDomainInUseReason, capiv1beta1.ConditionSeverityError, "%s", errMsg)

	reterr := fmt.Errorf("the NutanixMetroDomain %q is not safe for deletion since it is in use", metroDomain.Name)
	log.Error(reterr, errMsg)
	return reterr
}

func (r *NutanixMetroDomainReconciler) reconcileNormal(ctx context.Context, metroDomain *infrav1.NutanixMetroDomain) error {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Handling NutanixMetroDomain reconciling")

	return nil
}
