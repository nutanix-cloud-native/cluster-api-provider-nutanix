/*
Copyright 2025 Nutanix

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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	coreinformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/utils/ptr"
	capiv1beta1 "sigs.k8s.io/cluster-api/api/core/v1beta1" //nolint:staticcheck // suppress complaining on Deprecated package
	capiv1beta2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	v1beta1conditions "sigs.k8s.io/cluster-api/util/deprecated/v1beta1/conditions"         //nolint:staticcheck // suppress complaining on Deprecated package
	v1beta2conditions "sigs.k8s.io/cluster-api/util/deprecated/v1beta1/conditions/v1beta2" //nolint:staticcheck // suppress complaining on Deprecated package
	v1beta1patch "sigs.k8s.io/cluster-api/util/deprecated/v1beta1/patch"                   //nolint:staticcheck // suppress complaining on Deprecated package
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	ctrlutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// NutanixFailureDomainReconciler reconciles a NutanixFailureDomain object
type NutanixFailureDomainReconciler struct {
	client.Client
	SecretInformer    coreinformers.SecretInformer
	ConfigMapInformer coreinformers.ConfigMapInformer
	Scheme            *runtime.Scheme
	controllerConfig  *ControllerConfig
}

func NewNutanixFailureDomainReconciler(client client.Client, secretInformer coreinformers.SecretInformer, configMapInformer coreinformers.ConfigMapInformer, scheme *runtime.Scheme, copts ...ControllerConfigOpts) (*NutanixFailureDomainReconciler, error) {
	controllerConf := &ControllerConfig{}
	for _, opt := range copts {
		if err := opt(controllerConf); err != nil {
			return nil, err
		}
	}

	return &NutanixFailureDomainReconciler{
		Client:            client,
		SecretInformer:    secretInformer,
		ConfigMapInformer: configMapInformer,
		Scheme:            scheme,
		controllerConfig:  controllerConf,
	}, nil
}

// SetupWithManager sets up the NutanixFailureDomain controller with the Manager.
func (r *NutanixFailureDomainReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	copts := controller.Options{
		MaxConcurrentReconciles: r.controllerConfig.MaxConcurrentReconciles,
		RateLimiter:             r.controllerConfig.RateLimiter,
		SkipNameValidation:      ptr.To(r.controllerConfig.SkipNameValidation),
	}

	return ctrl.NewControllerManagedBy(mgr).
		Named("nutanixfailuredomain-controller").
		For(&infrav1.NutanixFailureDomain{}).
		Watches(
			&capiv1beta2.Machine{},
			handler.EnqueueRequestsFromMapFunc(
				r.mapMachineToNutanixFailureDomain(),
			),
		).
		Watches(
			&infrav1.NutanixMetro{},
			handler.EnqueueRequestsFromMapFunc(
				r.mapNutanixMetroToNutanixFailureDomain(),
			),
		).
		Watches(
			&infrav1.NutanixMetroSite{},
			handler.EnqueueRequestsFromMapFunc(
				r.mapNutanixMetroSiteToNutanixFailureDomain(),
			),
		).
		WithOptions(copts).
		Complete(r)
}

func (r *NutanixFailureDomainReconciler) mapMachineToNutanixFailureDomain() handler.MapFunc {
	return func(ctx context.Context, o client.Object) []ctrl.Request {
		log := ctrl.LoggerFrom(ctx)
		machine, ok := o.(*capiv1beta2.Machine)
		if !ok {
			log.Error(fmt.Errorf("expected a Machine object but was %T", o), "unexpected type")
			return nil
		}

		reqs := make([]ctrl.Request, 0)
		fdName := machine.Spec.FailureDomain
		if fdName == "" {
			return reqs
		}

		objKey := client.ObjectKey{Name: fdName, Namespace: machine.Namespace}
		reqs = append(reqs, ctrl.Request{NamespacedName: objKey})
		return reqs
	}
}

func (r *NutanixFailureDomainReconciler) mapNutanixMetroToNutanixFailureDomain() handler.MapFunc {
	return func(ctx context.Context, o client.Object) []ctrl.Request {
		log := ctrl.LoggerFrom(ctx)
		metro, ok := o.(*infrav1.NutanixMetro)
		if !ok {
			log.Error(fmt.Errorf("expected a NutanixMetro object but was %T", o), "unexpected type")
			return nil
		}

		reqs := make([]ctrl.Request, 0)
		for _, fdRef := range metro.Spec.FailureDomains {
			objKey := client.ObjectKey{Name: fdRef.Name, Namespace: metro.Namespace}
			reqs = append(reqs, ctrl.Request{NamespacedName: objKey})
		}

		return reqs
	}
}

func (r *NutanixFailureDomainReconciler) mapNutanixMetroSiteToNutanixFailureDomain() handler.MapFunc {
	return func(ctx context.Context, o client.Object) []ctrl.Request {
		log := ctrl.LoggerFrom(ctx)
		metroSite, ok := o.(*infrav1.NutanixMetroSite)
		if !ok {
			log.Error(fmt.Errorf("expected a NutanixMetroSite object but was %T", o), "unexpected type")
			return nil
		}

		reqs := make([]ctrl.Request, 0)
		objKey := client.ObjectKey{Name: metroSite.Spec.PreferredFailureDomain.Name, Namespace: metroSite.Namespace}
		reqs = append(reqs, ctrl.Request{NamespacedName: objKey})

		return reqs
	}
}

//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=machines;machines/status,verbs=get;list;watch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nutanixmetros,verbs=get;list;watch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nutanixmatrosites,verbs=get;list;watch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nutanixfailuredomains,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nutanixfailuredomains/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nutanixfailuredomains/finalizers,verbs=get;update;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the NutanixFailureDomain object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.8.3/pkg/reconcile
func (r *NutanixFailureDomainReconciler) Reconcile(ctx context.Context, req ctrl.Request) (res ctrl.Result, reterr error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Reconciling the NutanixFailureDomain")

	// Fetch the NutanixFailureDomain instance
	fd := &infrav1.NutanixFailureDomain{}
	if err := r.Get(ctx, req.NamespacedName, fd); err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			log.Info("The NutanixFailureDomain object not found. Ignoring since object must be deleted.")
			return reconcile.Result{}, nil
		}

		// Error reading the object - requeue the request.
		log.Error(err, "failed to fetch the NutanixFailureDomain object")
		return reconcile.Result{}, err
	}

	// Initialize the patch helper.
	patchHelper, err := v1beta1patch.NewHelper(fd, r.Client)
	if err != nil {
		log.Error(err, "Failed to configure the patch helper")
		return ctrl.Result{Requeue: true}, nil
	}

	defer func() {
		// Always attempt to Patch the NutanixFailureDomain object and its status after each reconciliation.
		if err := patchHelper.Patch(ctx, fd); err != nil {
			reterr = kerrors.NewAggregate([]error{reterr, err})
			log.Error(reterr, "Failed to patch NutanixFailureDomain.")
		} else {
			log.Info("Patched NutanixFailureDomain.", "status", fd.Status, "finalizers", fd.Finalizers)
		}
	}()

	// Add finalizer first if not set yet
	if !ctrlutil.ContainsFinalizer(fd, infrav1.NutanixFailureDomainFinalizer) {
		if ctrlutil.AddFinalizer(fd, infrav1.NutanixFailureDomainFinalizer) {
			// Add finalizer first avoid the race condition between init and delete.
			log.Info("Added the finalizer to the object", "finalizers", fd.Finalizers)
			return reconcile.Result{}, nil
		}
	}

	// Handle deletion
	if !fd.DeletionTimestamp.IsZero() {
		// List the Machines in the same namespace.
		machineList := &capiv1beta2.MachineList{}
		if err := r.List(ctx, machineList, client.InNamespace(fd.Namespace)); err != nil {
			return ctrl.Result{}, err
		}

		// List the NutanixMetroes in the same namespace.
		metroList := &infrav1.NutanixMetroList{}
		if err := r.List(ctx, metroList, client.InNamespace(fd.Namespace)); err != nil {
			return ctrl.Result{}, err
		}

		// List the NutanixMetroSites in the same namespace.
		metrositeList := &infrav1.NutanixMetroSiteList{}
		if err := r.List(ctx, metrositeList, client.InNamespace(fd.Namespace)); err != nil {
			return ctrl.Result{}, err
		}

		err = r.reconcileDelete(ctx, fd, machineList.Items, metroList.Items, metrositeList.Items)
		return ctrl.Result{}, err
	}

	err = r.reconcileNormal(ctx, fd)
	return ctrl.Result{}, err
}

func (r *NutanixFailureDomainReconciler) reconcileDelete(ctx context.Context, fd *infrav1.NutanixFailureDomain, machines []capiv1beta2.Machine, metroes []infrav1.NutanixMetro, metroSites []infrav1.NutanixMetroSite) error {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Handling NutanixFailureDomain deletion")

	// Check if there are other objects reference this object in the same namespace.
	// usedItems is to hold the names of these machine objects using this Metro.
	usedItems := []string{}

	for _, m := range machines {
		if !m.DeletionTimestamp.IsZero() {
			continue
		}
		if m.Spec.FailureDomain == fd.Name {
			usedItems = append(usedItems, fmt.Sprintf("machine:%s,cluster:%s", m.Name, m.Spec.ClusterName))
		}
	}

	for _, metro := range metroes {
		if !metro.DeletionTimestamp.IsZero() {
			continue
		}
		for _, fdRef := range metro.Spec.FailureDomains {
			if fdRef.Name == fd.Name {
				usedItems = append(usedItems, fmt.Sprintf("nutanixMetro:%s", metro.Name))
				break
			}
		}
	}

	for _, metroSite := range metroSites {
		if !metroSite.DeletionTimestamp.IsZero() {
			continue
		}
		if metroSite.Spec.PreferredFailureDomain.Name == fd.Name {
			usedItems = append(usedItems, fmt.Sprintf("nutanixMetroSite:%s", metroSite.Name))
		}
	}

	if len(usedItems) == 0 {
		v1beta1conditions.MarkTrue(fd, infrav1.FailureDomainSafeForDeletionCondition)
		v1beta2conditions.Set(fd, metav1.Condition{
			Type:   string(infrav1.FailureDomainSafeForDeletionCondition),
			Status: metav1.ConditionTrue,
			Reason: capiv1beta1.ReadyV1Beta2Reason,
		})

		// Remove the finalizer from the failure domain object
		ctrlutil.RemoveFinalizer(fd, infrav1.NutanixFailureDomainFinalizer)
		return nil
	}

	errMsg := fmt.Sprintf("The failure domain is used by other resources in the same namespace: %v", usedItems)
	v1beta1conditions.MarkFalse(fd, infrav1.FailureDomainSafeForDeletionCondition,
		infrav1.FailureDomainInUseReason, capiv1beta1.ConditionSeverityError, "%s", errMsg)
	v1beta2conditions.Set(fd, metav1.Condition{
		Type:    string(infrav1.FailureDomainSafeForDeletionCondition),
		Status:  metav1.ConditionFalse,
		Reason:  infrav1.FailureDomainInUseReason,
		Message: errMsg,
	})

	err := fmt.Errorf("the failure domain %q is not safe for deletion since it is in use", fd.Name)
	log.Error(err, errMsg)
	return err
}

func (r *NutanixFailureDomainReconciler) reconcileNormal(ctx context.Context, fd *infrav1.NutanixFailureDomain) error {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Handling NutanixFailureDomain reconciling")

	// Remove the FailureDomainSafeForDeletionCondition if there are any
	v1beta1conditions.Delete(fd, infrav1.FailureDomainSafeForDeletionCondition)
	v1beta2conditions.Delete(fd, string(infrav1.FailureDomainSafeForDeletionCondition))

	return nil
}
