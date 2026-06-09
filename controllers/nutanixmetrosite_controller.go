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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	coreinformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/utils/ptr"
	capiv1beta2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	ctrlutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// NutanixMetroSiteReconciler reconciles an NutanixMetroSite object
type NutanixMetroSiteReconciler struct {
	client.Client
	SecretInformer    coreinformers.SecretInformer
	ConfigMapInformer coreinformers.ConfigMapInformer
	Scheme            *runtime.Scheme
	controllerConfig  *ControllerConfig
}

func NewNutanixMetroSiteReconciler(client client.Client, secretInformer coreinformers.SecretInformer, configMapInformer coreinformers.ConfigMapInformer, scheme *runtime.Scheme, copts ...ControllerConfigOpts) (*NutanixMetroSiteReconciler, error) {
	controllerConf := &ControllerConfig{}
	for _, opt := range copts {
		if err := opt(controllerConf); err != nil {
			return nil, err
		}
	}

	return &NutanixMetroSiteReconciler{
		Client:            client,
		SecretInformer:    secretInformer,
		ConfigMapInformer: configMapInformer,
		Scheme:            scheme,
		controllerConfig:  controllerConf,
	}, nil
}

// SetupWithManager sets up the NutanixMetroSite controller with the Manager.
func (r *NutanixMetroSiteReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	copts := controller.Options{
		MaxConcurrentReconciles: r.controllerConfig.MaxConcurrentReconciles,
		RateLimiter:             r.controllerConfig.RateLimiter,
		SkipNameValidation:      ptr.To(r.controllerConfig.SkipNameValidation),
	}

	return ctrl.NewControllerManagedBy(mgr).
		Named("NutanixMetroSite-controller").
		For(&infrav1.NutanixMetroSite{}).
		Watches(
			&capiv1beta2.Machine{},
			handler.EnqueueRequestsFromMapFunc(
				r.mapMachineToNutanixMetroSite(),
			),
		).
		Watches(
			&capiv1beta2.MachineDeployment{},
			handler.EnqueueRequestsFromMapFunc(
				r.mapMachineDeploymentToNutanixMetroSite(),
			),
		).
		Watches(
			&infrav1.NutanixCluster{},
			handler.EnqueueRequestsFromMapFunc(
				r.mapNutanixClusterToNutanixMetroSite(),
			),
		).
		WithOptions(copts).
		Complete(r)
}

func (r *NutanixMetroSiteReconciler) mapMachineToNutanixMetroSite() handler.MapFunc {
	return func(ctx context.Context, o client.Object) []ctrl.Request {
		log := ctrl.LoggerFrom(ctx)
		machine, ok := o.(*capiv1beta2.Machine)
		if !ok {
			log.Error(fmt.Errorf("expected a Machine object but was %T", o), "unexpected type")
			return nil
		}

		reqs := make([]ctrl.Request, 0, 1)
		fdStr := machine.Spec.FailureDomain
		if !isNutanixMetroSiteFailureDomain(fdStr) {
			return reqs
		}

		reqs = append(reqs, ctrl.Request{
			NamespacedName: client.ObjectKey{Name: fdStr[len(metroSiteFailureDomainPrefix):], Namespace: machine.Namespace},
		})
		return reqs
	}
}

// mapMachineDeploymentToNutanixMetroSite enqueues reconcile requests for any NutanixMetroSite
// referenced by a MachineDeployment's spec.template.spec.failureDomain. This ensures that
// sites used only by worker nodepools are reconciled when a MachineDeployment changes, and
// that a scaled-to-zero MD still prevents site deletion.
func (r *NutanixMetroSiteReconciler) mapMachineDeploymentToNutanixMetroSite() handler.MapFunc {
	return func(ctx context.Context, o client.Object) []ctrl.Request {
		log := ctrl.LoggerFrom(ctx)
		md, ok := o.(*capiv1beta2.MachineDeployment)
		if !ok {
			log.Error(fmt.Errorf("expected a MachineDeployment but got %T", o), "unexpected type")
			return nil
		}

		fdStr := md.Spec.Template.Spec.FailureDomain
		if !isNutanixMetroSiteFailureDomain(fdStr) {
			return nil
		}

		return []ctrl.Request{{
			NamespacedName: client.ObjectKey{Name: fdStr[len(metroSiteFailureDomainPrefix):], Namespace: md.Namespace},
		}}
	}
}

// mapNutanixClusterToNutanixMetroSite enqueues reconcile requests for any NutanixMetroSite
// referenced by a NutanixCluster's ControlPlaneFailureDomains or by its MachineDeployments.
func (r *NutanixMetroSiteReconciler) mapNutanixClusterToNutanixMetroSite() handler.MapFunc {
	return func(ctx context.Context, o client.Object) []ctrl.Request {
		log := ctrl.LoggerFrom(ctx)
		ntnxCluster, ok := o.(*infrav1.NutanixCluster)
		if !ok {
			log.Error(fmt.Errorf("expected a NutanixCluster but got %T", o), "unexpected type")
			return nil
		}

		seen := map[string]struct{}{}
		for _, fdRef := range ntnxCluster.Spec.ControlPlaneFailureDomains {
			if isNutanixMetroSiteFailureDomain(fdRef.Name) {
				seen[fdRef.Name[len(metroSiteFailureDomainPrefix):]] = struct{}{}
			}
		}

		mdList := &capiv1beta2.MachineDeploymentList{}
		if err := r.Client.List(ctx, mdList,
			client.InNamespace(ntnxCluster.Namespace),
			client.MatchingLabels{capiv1beta2.ClusterNameLabel: ntnxCluster.Name},
		); err != nil {
			log.Error(err, "failed to list MachineDeployments while mapping NutanixCluster to MetroSite")
		} else {
			for _, md := range mdList.Items {
				fdStr := md.Spec.Template.Spec.FailureDomain
				if isNutanixMetroSiteFailureDomain(fdStr) {
					seen[fdStr[len(metroSiteFailureDomainPrefix):]] = struct{}{}
				}
			}
		}

		reqs := make([]ctrl.Request, 0, len(seen))
		for siteName := range seen {
			reqs = append(reqs, ctrl.Request{
				NamespacedName: client.ObjectKey{Name: siteName, Namespace: ntnxCluster.Namespace},
			})
		}
		return reqs
	}
}

// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=machines;machines/status,verbs=get;list;watch
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=machinedeployments,verbs=get;list;watch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nutanixclusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nutanixmetrosites,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nutanixmetrosites/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nutanixmetrosites/finalizers,verbs=get;update;patch

func (r *NutanixMetroSiteReconciler) Reconcile(ctx context.Context, req ctrl.Request) (res ctrl.Result, reterr error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Reconciling the NutanixMetroSite")

	// Fetch the NutanixMetroSite object
	metroSite := &infrav1.NutanixMetroSite{}
	if err := r.Get(ctx, req.NamespacedName, metroSite); err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			log.Info("The NutanixMetroSite object not found. Ignoring since object must be deleted.")
			return reconcile.Result{}, nil
		}

		// Error reading the object - requeue the request.
		log.Error(err, "failed to fetch the NutanixMetroSite object")
		return reconcile.Result{}, err
	}

	// Initialize the patch helper.
	patchHelper, err := patch.NewHelper(metroSite, r.Client)
	if err != nil {
		log.Error(err, "Failed to configure the patch helper")
		return ctrl.Result{Requeue: true}, nil
	}

	defer func() {
		// Always attempt to Patch the NutanixMetroSite object and its status after each reconciliation.
		if err := patchHelper.Patch(ctx, metroSite); err != nil {
			reterr = kerrors.NewAggregate([]error{reterr, err})
			log.Error(reterr, "Failed to patch NutanixMetroSite.")
		} else {
			log.Info("Patched NutanixMetroSite.", "status", metroSite.Status, "finalizers", metroSite.Finalizers)
		}
	}()

	// Add finalizer first if not set yet
	if !ctrlutil.ContainsFinalizer(metroSite, infrav1.NutanixMetroSiteFinalizer) {
		if ctrlutil.AddFinalizer(metroSite, infrav1.NutanixMetroSiteFinalizer) {
			// Add finalizer first avoid the race condition between init and delete.
			log.Info("Added the finalizer to the object", "finalizers", metroSite.Finalizers)
			return reconcile.Result{}, nil
		}
	}

	// Handle deletion
	if !metroSite.DeletionTimestamp.IsZero() {
		machineList := &capiv1beta2.MachineList{}
		if err := r.List(ctx, machineList, client.InNamespace(metroSite.Namespace)); err != nil {
			return ctrl.Result{}, err
		}

		mdList := &capiv1beta2.MachineDeploymentList{}
		if err := r.List(ctx, mdList, client.InNamespace(metroSite.Namespace)); err != nil {
			return ctrl.Result{}, err
		}

		nclList := &infrav1.NutanixClusterList{}
		if err := r.List(ctx, nclList, client.InNamespace(metroSite.Namespace)); err != nil {
			return ctrl.Result{}, err
		}

		err = r.reconcileDelete(ctx, metroSite, machineList.Items, mdList.Items, nclList.Items)
		return ctrl.Result{}, err
	}

	err = r.reconcileNormal(ctx, metroSite)
	return ctrl.Result{}, err
}

func (r *NutanixMetroSiteReconciler) reconcileDelete(ctx context.Context, metroSite *infrav1.NutanixMetroSite, machines []capiv1beta2.Machine, machineDeployments []capiv1beta2.MachineDeployment, ntxClusters []infrav1.NutanixCluster) error {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Handling NutanixMetroSite deletion")

	usedItems := []string{}

	for _, m := range machines {
		if !m.DeletionTimestamp.IsZero() {
			continue
		}
		if isNutanixMetroSiteFailureDomain(m.Spec.FailureDomain) &&
			m.Spec.FailureDomain[len(metroSiteFailureDomainPrefix):] == metroSite.Name {
			usedItems = append(usedItems, fmt.Sprintf("machine:%s,cluster:%s", m.Name, m.Spec.ClusterName))
		}
	}

	// A scaled-to-zero MD still declares its intent via spec.template.spec.failureDomain
	// and should block site deletion.
	for _, md := range machineDeployments {
		if !md.DeletionTimestamp.IsZero() {
			continue
		}
		if isNutanixMetroSiteFailureDomain(md.Spec.Template.Spec.FailureDomain) &&
			md.Spec.Template.Spec.FailureDomain[len(metroSiteFailureDomainPrefix):] == metroSite.Name {
			usedItems = append(usedItems, fmt.Sprintf("machineDeployment:%s,cluster:%s", md.Name, md.Spec.ClusterName))
		}
	}

	// A NutanixCluster that lists this site in ControlPlaneFailureDomains must also block deletion.
	for _, ncl := range ntxClusters {
		if !ncl.DeletionTimestamp.IsZero() {
			continue
		}
		for _, fdRef := range ncl.Spec.ControlPlaneFailureDomains {
			if isNutanixMetroSiteFailureDomain(fdRef.Name) &&
				fdRef.Name[len(metroSiteFailureDomainPrefix):] == metroSite.Name {
				usedItems = append(usedItems, fmt.Sprintf("nutanixCluster:%s", ncl.Name))
				break
			}
		}
	}

	if len(usedItems) == 0 {
		conditions.Set(metroSite, metav1.Condition{
			Type:   infrav1.MetroSiteSafeForDeletionCondition,
			Status: metav1.ConditionTrue,
			Reason: infrav1.MetroSiteNotInUseReason,
		})

		// Remove the finalizer from the NutanixMetroSite object
		ctrlutil.RemoveFinalizer(metroSite, infrav1.NutanixMetroSiteFinalizer)
		return nil
	}

	errMsg := fmt.Sprintf("The NutanixMetroSite object is referenced by Machines in the same namespace: %v", usedItems)
	conditions.Set(metroSite, metav1.Condition{
		Type:    infrav1.MetroSiteSafeForDeletionCondition,
		Status:  metav1.ConditionFalse,
		Reason:  infrav1.MetroSiteInUseReason,
		Message: errMsg,
	})

	reterr := fmt.Errorf("the NutanixMetroSite %s is not safe for deletion since it is in use", metroSite.Name)
	log.Error(reterr, errMsg)
	return reterr
}

func (r *NutanixMetroSiteReconciler) reconcileNormal(ctx context.Context, metroSite *infrav1.NutanixMetroSite) (reterr error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Handling NutanixMetroSite reconciling")

	defer func() {
		if reterr != nil {
			conditions.Set(metroSite, metav1.Condition{
				Type:    infrav1.MetroSiteValidatedCondition,
				Status:  metav1.ConditionFalse,
				Reason:  infrav1.MetroSiteMisconfiguredReason,
				Message: reterr.Error(),
			})
		} else {
			conditions.Set(metroSite, metav1.Condition{
				Type:   infrav1.MetroSiteValidatedCondition,
				Status: metav1.ConditionTrue,
				Reason: infrav1.MetroSiteSpecValidReason,
			})
		}
	}()

	// validate the referenced NutanixMetro CR exist
	metroObj, err := getNutanixMetroObject(ctx, r.Client, metroSite.Spec.MetroRef.Name, metroSite.Namespace)
	if err != nil {
		reterr = err
		return err
	}

	// validate the referenced NutanixFailureDomain objects exist
	if _, err := getNutanixFailureDomainObject(ctx, r.Client, metroSite.Spec.PreferredFailureDomain.Name, metroSite.Namespace); err != nil {
		reterr = err
		return err
	}

	for _, fdRef := range metroObj.Spec.FailureDomains {
		if fdRef.Name == metroSite.Spec.PreferredFailureDomain.Name {
			reterr = nil
			return nil
		}
	}

	reterr = fmt.Errorf("metroSite spec.preferredFailureDomain %s is not in the referred NutanixMetro's failureDomains: %+v", metroSite.Spec.PreferredFailureDomain.Name, metroObj.Spec.FailureDomains)
	return reterr
}
