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
	nctx "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/pkg/context"
	"k8s.io/apimachinery/pkg/runtime"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	coreinformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/utils/ptr"
	capiutil "sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	ctrlutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// NutanixVirtualHADomainReconciler reconciles an NutanixVirtualHADomain object
type NutanixVirtualHADomainReconciler struct {
	client.Client
	SecretInformer    coreinformers.SecretInformer
	ConfigMapInformer coreinformers.ConfigMapInformer
	Scheme            *runtime.Scheme
	controllerConfig  *ControllerConfig
}

func NewNutanixVirtualHADomainReconciler(client client.Client, secretInformer coreinformers.SecretInformer, configMapInformer coreinformers.ConfigMapInformer, scheme *runtime.Scheme, copts ...ControllerConfigOpts) (*NutanixVirtualHADomainReconciler, error) {
	controllerConf := &ControllerConfig{}
	for _, opt := range copts {
		if err := opt(controllerConf); err != nil {
			return nil, err
		}
	}

	return &NutanixVirtualHADomainReconciler{
		Client:            client,
		SecretInformer:    secretInformer,
		ConfigMapInformer: configMapInformer,
		Scheme:            scheme,
		controllerConfig:  controllerConf,
	}, nil
}

// SetupWithManager sets up the NutanixVirtualHADomain controller with the Manager.
func (r *NutanixVirtualHADomainReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	copts := controller.Options{
		MaxConcurrentReconciles: r.controllerConfig.MaxConcurrentReconciles,
		RateLimiter:             r.controllerConfig.RateLimiter,
		SkipNameValidation:      ptr.To(r.controllerConfig.SkipNameValidation),
	}

	return ctrl.NewControllerManagedBy(mgr).
		Named("nutanixvirtualhadomain-controller").
		For(&infrav1.NutanixVirtualHADomain{}).
		WithOptions(copts).
		Complete(r)
}

// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nutanixclusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nutanixvirtualhadomains,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nutanixvirtualhadomains/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nutanixvirtualhadomains/finalizers,verbs=get;update;patch

func (r *NutanixVirtualHADomainReconciler) Reconcile(ctx context.Context, req ctrl.Request) (res ctrl.Result, reterr error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Reconciling the NutanixVirtualHADomain")

	// Fetch the NutanixVirtualHADomain object
	vHADomain, err := getNutanixVHADomainObject(ctx, r.Client, req.Name, req.Namespace)
	if err != nil {
		log.Error(err, "Failed at reconciling")
		return reconcile.Result{}, err
	}

	// keep the original object for restoring its spec if reconciling fails
	vHADomainOrig := vHADomain.DeepCopy()

	// Initialize the patch helper.
	patchHelper, err := patch.NewHelper(vHADomain, r.Client)
	if err != nil {
		log.Error(err, "Failed to configure the patch helper")
		return ctrl.Result{Requeue: true}, nil
	}

	defer func() {
		// Always attempt to Patch the NutanixVirtualHADomain object and its status after each reconciliation.
		if err := patchHelper.Patch(ctx, vHADomain); err != nil {
			reterr = kerrors.NewAggregate([]error{reterr, err})
			log.Error(reterr, "Failed to patch NutanixVirtualHADomain")
		} else {
			log.Info("Patched NutanixVirtualHADomain", "status", vHADomain.Status, "finalizers", vHADomain.Finalizers)
		}
	}()

	// Add finalizer first if not set yet
	if !ctrlutil.ContainsFinalizer(vHADomain, infrav1.NutanixVirtualHADomainFinalizer) {
		if ctrlutil.AddFinalizer(vHADomain, infrav1.NutanixVirtualHADomainFinalizer) {
			// Add finalizer first avoid the race condition between init and delete.
			log.Info("Added the finalizer to the object", "finalizers", vHADomain.Finalizers)
			return reconcile.Result{}, nil
		}
	}

	// Fetch the CAPI Cluster.
	cluster, err := capiutil.GetClusterFromMetadata(ctx, r.Client, vHADomain.ObjectMeta)
	if err != nil {
		log.Error(err, "vHADomain is missing cluster label or cluster does not exist")
		return reconcile.Result{}, err
	}

	// Fetch the NutanixCluster
	ntnxCluster := &infrav1.NutanixCluster{}
	nclKey := client.ObjectKey{
		Namespace: cluster.Namespace,
		Name:      cluster.Spec.InfrastructureRef.Name,
	}
	err = r.Get(ctx, nclKey, ntnxCluster)
	if err != nil {
		log.Error(err, "Failed to fetch the NutanixCluster")
		return reconcile.Result{}, err
	}

	// Set the NutanixCluster as the vHADomain's controller owner if not set yet
	hasOwner, err := ctrlutil.HasOwnerReference(vHADomain.OwnerReferences, ntnxCluster, r.Scheme)
	if err != nil {
		log.Error(err, "Failed to check if the NutanixCluster is the owner")
		return reconcile.Result{}, err
	}
	if !hasOwner {
		if err = ctrlutil.SetControllerReference(ntnxCluster, vHADomain, r.Scheme); err != nil {
			log.Error(err, "Failed to set the NutanixCluster as the owner of the vHADomain")
			return reconcile.Result{}, err
		}
	}

	v3Client, err := getPrismCentralClientForCluster(ctx, ntnxCluster, r.SecretInformer, r.ConfigMapInformer)
	if err != nil {
		log.Error(err, "error occurred while fetching prism central client")
		return reconcile.Result{}, err
	}
	convergedClient, err := getPrismCentralConvergedV4ClientForCluster(ctx, ntnxCluster, r.SecretInformer, r.ConfigMapInformer)
	if err != nil {
		log.Error(err, "error occurred while fetching prism central converged client")
		return reconcile.Result{}, err
	}
	rctx := &nctx.VHADomainContext{
		Context:         ctx,
		Cluster:         cluster,
		NutanixCluster:  ntnxCluster,
		VHADomain:       vHADomain,
		NutanixClient:   v3Client,
		ConvergedClient: convergedClient,
	}

	// Handle deletion
	if !vHADomain.DeletionTimestamp.IsZero() {
		if err = r.reconcileDelete(rctx); err != nil {
			log.Error(err, "failed at deletion reconciling")
		}
		return ctrl.Result{}, err
	}

	err = r.reconcileNormal(rctx)
	if err != nil {
		log.Error(err, "failed at normal reconciling, restore to original spec.")
		vHADomain.Spec = vHADomainOrig.Spec
	}
	return ctrl.Result{}, err
}

func (r *NutanixVirtualHADomainReconciler) reconcileDelete(rctx *nctx.VHADomainContext) error {
	log := ctrl.LoggerFrom(rctx.Context)
	log.Info("Handling NutanixVirtualHADomain deletion")

	if rctx.NutanixCluster.DeletionTimestamp.IsZero() {
		return fmt.Errorf("cannot delete the vHADomain because its corresponding NutanixCluster %s is not in deletion", rctx.NutanixCluster.Name)
	}

	// FIXME: add logic to clean up the PC resources (using the UUIDs in the spec) of the vHA domain

	// Remove the finalizer from the NutanixVirtualHADomain object
	ctrlutil.RemoveFinalizer(rctx.VHADomain, infrav1.NutanixVirtualHADomainFinalizer)
	log.Info("Removed finalizer %q from the vHADomain object", infrav1.NutanixVirtualHADomainFinalizer)

	return nil
}

func (r *NutanixVirtualHADomainReconciler) reconcileNormal(rctx *nctx.VHADomainContext) error {
	log := ctrl.LoggerFrom(rctx.Context)
	log.Info("Handling NutanixVirtualHADomain reconciling")

	// Make sure it is owned by a NutanixCluster
	namespace := rctx.VHADomain.Namespace

	// fetch NutanixMetro and its failureDomain CRs
	metroObj, err := getNutanixMetroObject(rctx.Context, r.Client, rctx.VHADomain.Spec.MetroRef.Name, namespace)
	if err != nil {
		rctx.VHADomain.Status.Ready = false
		return err
	}

	for _, fdRef := range metroObj.Spec.FailureDomains {
		_, err = getNutanixFailureDomainObject(rctx.Context, r.Client, fdRef.Name, namespace)
		if err != nil {
			rctx.VHADomain.Status.Ready = false
			return err
		}
	}

	// FIXME: create the PC resouces of the vHA domain and set the UUIDs in the spec

	rctx.VHADomain.Status.Ready = len(rctx.VHADomain.Spec.Categories) == 2 &&
		rctx.VHADomain.Spec.ProtectionPolicy != nil &&
		len(rctx.VHADomain.Spec.RecoveryPlans) == 2

	return nil
}
