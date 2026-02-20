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

// AHVMetroZoneReconciler reconciles an AHVMetroZone object
type AHVMetroZoneReconciler struct {
	client.Client
	SecretInformer    coreinformers.SecretInformer
	ConfigMapInformer coreinformers.ConfigMapInformer
	Scheme            *runtime.Scheme
	controllerConfig  *ControllerConfig
}

func NewAHVMetroZoneReconciler(client client.Client, secretInformer coreinformers.SecretInformer, configMapInformer coreinformers.ConfigMapInformer, scheme *runtime.Scheme, copts ...ControllerConfigOpts) (*AHVMetroZoneReconciler, error) {
	controllerConf := &ControllerConfig{}
	for _, opt := range copts {
		if err := opt(controllerConf); err != nil {
			return nil, err
		}
	}

	return &AHVMetroZoneReconciler{
		Client:            client,
		SecretInformer:    secretInformer,
		ConfigMapInformer: configMapInformer,
		Scheme:            scheme,
		controllerConfig:  controllerConf,
	}, nil
}

// SetupWithManager sets up the AHVMetroZone controller with the Manager.
func (r *AHVMetroZoneReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	copts := controller.Options{
		MaxConcurrentReconciles: r.controllerConfig.MaxConcurrentReconciles,
		RateLimiter:             r.controllerConfig.RateLimiter,
		SkipNameValidation:      ptr.To(r.controllerConfig.SkipNameValidation),
	}

	return ctrl.NewControllerManagedBy(mgr).
		Named("ahvmetrozone-controller").
		For(&infrav1.AHVMetroZone{}).
		Watches(
			&capiv1.Machine{},
			handler.EnqueueRequestsFromMapFunc(
				r.mapMachineToAHVMetroZone(),
			),
		).
		WithOptions(copts).
		Complete(r)
}

func (r *AHVMetroZoneReconciler) mapMachineToAHVMetroZone() handler.MapFunc {
	return func(ctx context.Context, o client.Object) []ctrl.Request {
		log := ctrl.LoggerFrom(ctx)
		machine, ok := o.(*capiv1.Machine)
		if !ok {
			log.Error(fmt.Errorf("expected a Machine object but was %T", o), "unexpected type")
			return nil
		}

		reqs := make([]ctrl.Request, 0)
		// Fetch the AHVMetroZone object in the local namespace
		fdStr := machine.Spec.FailureDomain
		zoneName := getMetroZoneObjectName(fdStr)
		if zoneName == nil || *zoneName == "" {
			return reqs
		}

		metroZone := &infrav1.AHVMetroZone{}
		if err := r.Get(ctx, client.ObjectKey{Name: *zoneName, Namespace: machine.Namespace}, metroZone); err != nil {
			log.Error(err, "Failed to fetch the AHVMetroZone object %q referenced with Machine %q spec.failureDomain: %s", *zoneName, machine.Name, *fdStr)
			return nil
		}

		objKey := client.ObjectKey{Name: metroZone.Name, Namespace: metroZone.Namespace}
		reqs = append(reqs, ctrl.Request{NamespacedName: objKey})
		return reqs
	}
}

// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=machines;machines/status,verbs=get;list;watch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=ahvmetrozones,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=ahvmetrozones/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=ahvmetrozones/finalizers,verbs=get;update;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the AHVMetroZone object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.8.3/pkg/reconcile
func (r *AHVMetroZoneReconciler) Reconcile(ctx context.Context, req ctrl.Request) (res ctrl.Result, reterr error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Reconciling the AHVMetroZone")

	// Fetch the AHVMetroZone object
	metroZone := &infrav1.AHVMetroZone{}
	if err := r.Get(ctx, req.NamespacedName, metroZone); err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			log.Info("The AHVMetroZone object not found. Ignoring since object must be deleted.")
			return reconcile.Result{}, nil
		}

		// Error reading the object - requeue the request.
		log.Error(err, "failed to fetch the AHVMetroZone object")
		return reconcile.Result{}, err
	}

	// Initialize the patch helper.
	patchHelper, err := patch.NewHelper(metroZone, r.Client)
	if err != nil {
		log.Error(err, "Failed to configure the patch helper")
		return ctrl.Result{Requeue: true}, nil
	}

	defer func() {
		// Always attempt to Patch the AHVMetroZone object and its status after each reconciliation.
		if err := patchHelper.Patch(ctx, metroZone); err != nil {
			reterr = kerrors.NewAggregate([]error{reterr, err})
			log.Error(reterr, "Failed to patch AHVMetroZone.")
		} else {
			log.Info("Patched AHVMetroZone.", "status", metroZone.Status, "finalizers", metroZone.Finalizers)
		}
	}()

	// Add finalizer first if not set yet
	if !ctrlutil.ContainsFinalizer(metroZone, infrav1.AHVMetroZoneFinalizer) {
		if ctrlutil.AddFinalizer(metroZone, infrav1.AHVMetroZoneFinalizer) {
			// Add finalizer first avoid the race condition between init and delete.
			log.Info("Added the finalizer to the object", "finalizers", metroZone.Finalizers)
			return reconcile.Result{}, nil
		}
	}

	// Handle deletion
	if !metroZone.DeletionTimestamp.IsZero() {
		// List the Machines in the same namespace as metroZone.
		machineList := &capiv1.MachineList{}
		if err := r.List(ctx, machineList, client.InNamespace(metroZone.Namespace)); err != nil {
			return ctrl.Result{}, err
		}

		err = r.reconcileDelete(ctx, metroZone, machineList.Items)
		return ctrl.Result{}, err
	}

	err = r.reconcileNormal(ctx, metroZone)
	return ctrl.Result{}, err
}

func (r *AHVMetroZoneReconciler) reconcileDelete(ctx context.Context, metroZone *infrav1.AHVMetroZone, machines []capiv1.Machine) error {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Handling AHVMetroZone deletion")

	// Check if there are Machine objects reference this AHVMetroZone in their spec.failureDomain.
	// usedItems is to hold the names of the machine/cluster using this metroZone.
	usedItems := []string{}

	for _, m := range machines {
		if !m.DeletionTimestamp.IsZero() {
			continue
		}

		if isMetroZoneFailureDomain(m.Spec.FailureDomain) {
			usedItems = append(usedItems, fmt.Sprintf("machine:%s,cluster:%s", m.Name, m.Spec.ClusterName))
		}
	}

	if len(usedItems) == 0 {
		v1beta1conditions.MarkTrue(metroZone, infrav1.MetroZoneSafeForDeletionCondition)

		// Remove the finalizer from the AHVMetroZone object
		ctrlutil.RemoveFinalizer(metroZone, infrav1.AHVMetroZoneFinalizer)
		return nil
	}

	errMsg := fmt.Sprintf("The AHVMetroZone object is used by machines: %v", usedItems)
	v1beta1conditions.MarkFalse(metroZone, infrav1.MetroZoneSafeForDeletionCondition,
		infrav1.MetroZoneInUseReason, capiv1.ConditionSeverityError, "%s", errMsg)

	reterr := fmt.Errorf("the AHVMetroZone %q is not safe for deletion since it is in use", metroZone.Name)
	log.Error(reterr, errMsg)
	return reterr
}

func (r *AHVMetroZoneReconciler) reconcileNormal(ctx context.Context, metroZone *infrav1.AHVMetroZone) error {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Handling AHVMetroZone reconciling")

	// validate spec.placement.preferredZone
	if metroZone.Spec.Placement.Strategy == infrav1.PreferredStrategy {
		if metroZone.Spec.Placement.PreferredZone == nil || *metroZone.Spec.Placement.PreferredZone == "" {
			return fmt.Errorf("preferredZone is required for Preferred strategy")
		}

		preferredZone := *metroZone.Spec.Placement.PreferredZone
		found := false
		for _, zone := range metroZone.Spec.Zones {
			if zone.Name == preferredZone {
				found = true
				break
			}
		}

		if !found {
			return fmt.Errorf("preferredZone %q is invalid. It must be one of the AHVMetroZone object's spec.zones.name", preferredZone)
		}
	}

	return nil
}
