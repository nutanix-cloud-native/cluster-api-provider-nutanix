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
			&capiv1beta2.MachineDeployment{},
			handler.EnqueueRequestsFromMapFunc(
				r.mapMachineDeploymentToNutanixMetro(),
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

		fdStr := machine.Spec.FailureDomain
		reqs := make([]ctrl.Request, 0, 1)
		switch {
		case isNutanixMetroFailureDomain(fdStr):
			reqs = append(reqs, ctrl.Request{
				NamespacedName: client.ObjectKey{Name: fdStr[len(metroFailureDomainPrefix):], Namespace: machine.Namespace},
			})
		case isNutanixMetroSiteFailureDomain(fdStr):
			siteName := fdStr[len(metroSiteFailureDomainPrefix):]
			site := &infrav1.NutanixMetroSite{}
			if err := r.Get(ctx, client.ObjectKey{Name: siteName, Namespace: machine.Namespace}, site); err != nil {
				log.Error(err, "failed to fetch NutanixMetroSite while mapping Machine to metro", "site", siteName)
				return nil
			}
			reqs = append(reqs, ctrl.Request{
				NamespacedName: client.ObjectKey{Name: site.Spec.MetroRef.Name, Namespace: machine.Namespace},
			})
		}
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

		// Derive the set of referenced NutanixMetro names from ControlPlaneFailureDomains
		// and from MachineDeployments owned by this cluster.
		seen := map[string]struct{}{}

		// Helper to resolve a type-prefixed failure domain string to a metro name.
		resolveMetroName := func(fdStr string) {
			switch {
			case isNutanixMetroFailureDomain(fdStr):
				seen[fdStr[len(metroFailureDomainPrefix):]] = struct{}{}
			case isNutanixMetroSiteFailureDomain(fdStr):
				siteName := fdStr[len(metroSiteFailureDomainPrefix):]
				site := &infrav1.NutanixMetroSite{}
				if err := r.Get(ctx, client.ObjectKey{Name: siteName, Namespace: ntnxCluster.Namespace}, site); err != nil {
					log.Error(err, "failed to fetch NutanixMetroSite while mapping cluster to metro", "site", siteName)
					return
				}
				seen[site.Spec.MetroRef.Name] = struct{}{}
			}
		}

		for _, fdRef := range ntnxCluster.Spec.ControlPlaneFailureDomains {
			resolveMetroName(fdRef.Name)
		}

		// Also scan MachineDeployments owned by this cluster for worker metros.
		mdList := &capiv1beta2.MachineDeploymentList{}
		if err := r.List(ctx, mdList,
			client.InNamespace(ntnxCluster.Namespace),
			client.MatchingLabels{capiv1beta2.ClusterNameLabel: ntnxCluster.Name},
		); err != nil {
			log.Error(err, "failed to list MachineDeployments while mapping cluster to metro")
		} else {
			for _, md := range mdList.Items {
				resolveMetroName(md.Spec.Template.Spec.FailureDomain)
			}
		}

		reqs := make([]ctrl.Request, 0, len(seen))
		for metroName := range seen {
			reqs = append(reqs, ctrl.Request{NamespacedName: client.ObjectKey{Name: metroName, Namespace: ntnxCluster.Namespace}})
		}
		return reqs
	}
}

// mapMachineDeploymentToNutanixMetro enqueues reconcile requests for any NutanixMetro
// referenced by a MachineDeployment's spec.template.spec.failureDomain. This ensures
// metros used only by worker nodepools (not in ControlPlaneFailureDomains) are also
// reconciled when a MachineDeployment is created, updated, or deleted.
func (r *NutanixMetroReconciler) mapMachineDeploymentToNutanixMetro() handler.MapFunc {
	return func(ctx context.Context, o client.Object) []ctrl.Request {
		log := ctrl.LoggerFrom(ctx)
		md, ok := o.(*capiv1beta2.MachineDeployment)
		if !ok {
			log.Error(fmt.Errorf("expected a MachineDeployment but got %T", o), "unexpected type")
			return nil
		}

		fdStr := md.Spec.Template.Spec.FailureDomain
		if fdStr == "" {
			return nil
		}

		reqs := make([]ctrl.Request, 0, 1)
		switch {
		case isNutanixMetroFailureDomain(fdStr):
			reqs = append(reqs, ctrl.Request{
				NamespacedName: client.ObjectKey{Name: fdStr[len(metroFailureDomainPrefix):], Namespace: md.Namespace},
			})
		case isNutanixMetroSiteFailureDomain(fdStr):
			siteName := fdStr[len(metroSiteFailureDomainPrefix):]
			site := &infrav1.NutanixMetroSite{}
			if err := r.Get(ctx, client.ObjectKey{Name: siteName, Namespace: md.Namespace}, site); err != nil {
				log.Error(err, "failed to fetch NutanixMetroSite while mapping MachineDeployment to metro", "site", siteName)
				return nil
			}
			reqs = append(reqs, ctrl.Request{
				NamespacedName: client.ObjectKey{Name: site.Spec.MetroRef.Name, Namespace: md.Namespace},
			})
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
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=machinedeployments,verbs=get;list;watch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nutanixclusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nutanixmetros,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nutanixmetros/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nutanixmetros/finalizers,verbs=get;update;patch
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
		return ctrl.Result{}, err
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

		// List the MachineDeployments in the same namespace.
		mdList := &capiv1beta2.MachineDeploymentList{}
		if err := r.List(ctx, mdList, client.InNamespace(metro.Namespace)); err != nil {
			return ctrl.Result{}, err
		}

		err = r.reconcileDelete(ctx, metro, machineList.Items, nclList.Items, metrositeList.Items, mdList.Items)
		return ctrl.Result{}, err
	}

	err = r.reconcileNormal(ctx, metro)
	return ctrl.Result{}, err
}

func (r *NutanixMetroReconciler) reconcileDelete(ctx context.Context, metro *infrav1.NutanixMetro, machines []capiv1beta2.Machine, ntxClusters []infrav1.NutanixCluster, metroSites []infrav1.NutanixMetroSite, machineDeployments []capiv1beta2.MachineDeployment) error {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Handling NutanixMetro deletion")

	// Check if there are other objects reference this object in the same namespace.
	// usedItems is to hold info of the objects using this Metro.
	usedItems := []string{}

	// A NutanixCluster references a metro if any ControlPlaneFailureDomains entry resolves to it:
	// either directly via "NutanixMetro/<name>" or transitively via a MetroSite whose metroRef matches.
	for _, ncl := range ntxClusters {
		if !ncl.DeletionTimestamp.IsZero() {
			continue
		}
		if nutanixClusterReferencesMetro(ncl, metro.Name, metroSites) {
			usedItems = append(usedItems, fmt.Sprintf("nutanixCluster:%s", ncl.Name))
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
		if failureDomainReferencesMetro(m.Spec.FailureDomain, metro.Name, metroSites) {
			usedItems = append(usedItems, fmt.Sprintf("machine:%s,cluster:%s", m.Name, m.Spec.ClusterName))
		}
	}

	// A scaled-to-zero MD still declares its failure domain intent and should block metro deletion.
	for _, md := range machineDeployments {
		if !md.DeletionTimestamp.IsZero() {
			continue
		}
		if failureDomainReferencesMetro(md.Spec.Template.Spec.FailureDomain, metro.Name, metroSites) {
			usedItems = append(usedItems, fmt.Sprintf("machineDeployment:%s,cluster:%s", md.Name, md.Spec.ClusterName))
		}
	}

	if len(usedItems) == 0 {
		conditions.Set(metro, metav1.Condition{
			Type:   infrav1.MetroSafeForDeletionCondition,
			Status: metav1.ConditionTrue,
			Reason: infrav1.MetroNotInUseReason,
		})
		// Remove the finalizer from the NutanixMetro object
		ctrlutil.RemoveFinalizer(metro, infrav1.NutanixMetroFinalizer)

		return nil
	}

	errMsg := fmt.Sprintf("The NutanixMetro object is referenced by other resources in the same namespace: %v", usedItems)
	conditions.Set(metro, metav1.Condition{
		Type:    infrav1.MetroSafeForDeletionCondition,
		Status:  metav1.ConditionFalse,
		Reason:  infrav1.MetroInUseReason,
		Message: errMsg,
	})

	reterr := fmt.Errorf("the NutanixMetro %s is not safe for deletion since it is in use", metro.Name)
	log.Error(reterr, errMsg)
	return reterr
}

// failureDomainReferencesMetro returns true if the given failure domain string resolves to the
// given metro name — either directly ("NutanixMetro/<name>") or transitively via a
// NutanixMetroSite whose spec.metroRef.name matches.
func failureDomainReferencesMetro(fdStr, metroName string, metroSites []infrav1.NutanixMetroSite) bool {
	switch {
	case isNutanixMetroFailureDomain(fdStr):
		return fdStr[len(metroFailureDomainPrefix):] == metroName
	case isNutanixMetroSiteFailureDomain(fdStr):
		siteName := fdStr[len(metroSiteFailureDomainPrefix):]
		for _, site := range metroSites {
			if site.Name == siteName && site.Spec.MetroRef.Name == metroName {
				return true
			}
		}
	}
	return false
}

// nutanixClusterReferencesMetro returns true if any ControlPlaneFailureDomains entry on the
// cluster resolves to the given metro name — either directly ("NutanixMetro/<name>") or
// transitively via a NutanixMetroSite whose spec.metroRef.name matches.
func nutanixClusterReferencesMetro(ncl infrav1.NutanixCluster, metroName string, metroSites []infrav1.NutanixMetroSite) bool {
	for _, fdRef := range ncl.Spec.ControlPlaneFailureDomains {
		switch {
		case isNutanixMetroFailureDomain(fdRef.Name):
			if fdRef.Name[len(metroFailureDomainPrefix):] == metroName {
				return true
			}
		case isNutanixMetroSiteFailureDomain(fdRef.Name):
			siteName := fdRef.Name[len(metroSiteFailureDomainPrefix):]
			for _, site := range metroSites {
				if site.Name == siteName && site.Spec.MetroRef.Name == metroName {
					return true
				}
			}
		}
	}
	return false
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
		conditions.Set(metro, metav1.Condition{
			Type:    infrav1.MetroValidatedCondition,
			Status:  metav1.ConditionFalse,
			Reason:  infrav1.MetroMisconfiguredReason,
			Message: err.Error(),
		})
		return err
	}

	conditions.Set(metro, metav1.Condition{
		Type:   infrav1.MetroValidatedCondition,
		Status: metav1.ConditionTrue,
		Reason: infrav1.MetroSpecValidReason,
	})
	return nil
}
