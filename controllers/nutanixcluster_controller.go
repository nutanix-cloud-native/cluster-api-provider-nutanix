/*
Copyright 2021.

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

	//"reflect"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	apitypes "k8s.io/apimachinery/pkg/types"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/klog/v2"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	capierrors "sigs.k8s.io/cluster-api/errors"
	capiutil "sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/annotations"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	nutanixClient "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/pkg/client"
	nctx "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/pkg/context"
)

// NutanixClusterReconciler reconciles a NutanixCluster object
type NutanixClusterReconciler struct {
	Client client.Client
	Scheme *runtime.Scheme
}

// SetupWithManager sets up the NutanixCluster controller with the Manager.
func (r *NutanixClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {

	return ctrl.NewControllerManagedBy(mgr).
		// Watch the controlled, infrastructure resource.
		For(&infrav1.NutanixCluster{}).
		// Watch the CAPI resource that owns this infrastructure resource.
		Watches(
			&source.Kind{Type: &capiv1.Cluster{}},
			handler.EnqueueRequestsFromMapFunc(
				capiutil.ClusterToInfrastructureMapFunc(
					infrav1.GroupVersion.WithKind("NutanixCluster"))),
		).
		Complete(r)
}

//+kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters;clusters/status,verbs=get;list;watch
//+kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nutanixclusters,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nutanixclusters/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nutanixclusters/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the NutanixCluster object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.8.3/pkg/reconcile
func (r *NutanixClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (res ctrl.Result, reterr error) {
	//log := r.Logger.WithValues("namespace", req.Namespace, "name", req.Name)
	logPrefix := fmt.Sprintf("NutanixCluster[namespace: %s, name: %s]", req.Namespace, req.Name)
	klog.Infof("%s Reconciling the NutanixCluster.", logPrefix)

	var err error

	// Fetch the NutanixCluster instance
	cluster := &infrav1.NutanixCluster{}
	err = r.Client.Get(ctx, req.NamespacedName, cluster)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			klog.Infof("%s NutanixCluster not found. Ignoring since object must be deleted.", logPrefix)
			return reconcile.Result{}, nil
		}

		// Error reading the object - requeue the request.
		klog.Errorf("%s Failed to fetch the NutanixCluster object. %v", logPrefix, err)
		return reconcile.Result{}, err
	}

	// Fetch the CAPI Cluster.
	capiCluster, err := capiutil.GetOwnerCluster(ctx, r.Client, cluster.ObjectMeta)
	if err != nil {
		klog.Errorf("%s Failed to fetch the owner CAPI Cluster object. %v", logPrefix, err)
		return reconcile.Result{}, err
	}
	if capiCluster == nil {
		klog.Infof("%s Waiting for Cluster Controller to set OwnerRef for the NutanixCluster object", logPrefix)
		return reconcile.Result{}, nil
	}
	if annotations.IsPaused(capiCluster, cluster) {
		klog.Infof("%s The NutanixCluster object linked to a cluster that is paused", logPrefix)
		return reconcile.Result{}, nil
	}
	klog.Infof("%s Fetched the owner Cluster: %s", logPrefix, capiCluster.Name)

	client, err := nutanixClient.Client(nutanixClient.ClientOptions{})
	if err != nil {
		return ctrl.Result{Requeue: true}, fmt.Errorf("Client Auth error: %v", err)
	}

	rctx := &nctx.ClusterContext{
		Context:        ctx,
		Cluster:        capiCluster,
		NutanixCluster: cluster,
		LogPrefix:      logPrefix,
		NutanixClient:  client,
	}

	// Initialize the patch helper.
	patchHelper, err := patch.NewHelper(cluster, r.Client)
	if err != nil {
		klog.Errorf("%s Failed to configure the patch helper. %v", logPrefix, err)
		return ctrl.Result{Requeue: true}, nil
	}

	defer func() {
		// Always attempt to Patch the NutanixCluster object and its status after each reconciliation.
		if err := patchHelper.Patch(ctx, cluster); err != nil {
			klog.Errorf("%s Failed to patch NutanixCluster. %v", rctx.LogPrefix, err)
			reterr = kerrors.NewAggregate([]error{reterr, err})
		}
		klog.Infof("%s Patched NutanixCluster. Status: %+v",
			rctx.LogPrefix, cluster.Status)
	}()

	// Check for request action
	if !cluster.DeletionTimestamp.IsZero() {
		// NutanixCluster is being deleted
		return r.reconcileDelete(rctx)
	}

	return r.reconcileNormal(rctx)
}

func (r *NutanixClusterReconciler) reconcileDelete(rctx *nctx.ClusterContext) (reconcile.Result, error) {
	klog.Infof("%s Handling NutanixCluster deletion", rctx.LogPrefix)

	err := r.reconcileCategoriesDelete(rctx)
	if err != nil {
		klog.Errorf("%s error occurred while running deletion of categories: %v", rctx.LogPrefix, err)
		return reconcile.Result{}, err
	}
	// Remove the finalizer from the NutanixCluster object
	ctrlutil.RemoveFinalizer(rctx.NutanixCluster, infrav1.NutanixClusterFinalizer)

	// Remove the workload cluster client from cache
	clusterKey := apitypes.NamespacedName{
		Namespace: rctx.Cluster.Namespace,
		Name:      rctx.Cluster.Name,
	}
	nctx.RemoveRemoteClient(clusterKey)

	return reconcile.Result{}, nil
}

func (r *NutanixClusterReconciler) reconcileNormal(rctx *nctx.ClusterContext) (reconcile.Result, error) {

	if rctx.NutanixCluster.Status.FailureReason != nil || rctx.NutanixCluster.Status.FailureMessage != nil {
		klog.Errorf("Nutanix Cluster has failed. Will not reconcile %s", rctx.NutanixCluster.Name)
		return reconcile.Result{}, nil
	}
	klog.Infof("%s Handling NutanixCluster reconciling", rctx.LogPrefix)

	// Add finalizer first if not exist to avoid the race condition between init and delete
	if !ctrlutil.ContainsFinalizer(rctx.NutanixCluster, infrav1.NutanixClusterFinalizer) {
		ctrlutil.AddFinalizer(rctx.NutanixCluster, infrav1.NutanixClusterFinalizer)
	}

	if rctx.NutanixCluster.Status.Ready {
		klog.Infof("%s NutanixCluster is already in ready status.", rctx.LogPrefix)
		return reconcile.Result{}, nil
	}

	err := r.reconcileCategories(rctx)
	if err != nil {
		errorMsg := fmt.Errorf("Failed to reconcile categories for cluster %s", rctx.Cluster.Name)
		klog.Errorf("%s %v", rctx.LogPrefix, errorMsg)
		rctx.SetFailureStatus(capierrors.CreateClusterError, errorMsg)
		return reconcile.Result{}, err
	}

	rctx.NutanixCluster.Status.Ready = true
	return reconcile.Result{}, nil
}

func (r *NutanixClusterReconciler) reconcileCategories(rctx *nctx.ClusterContext) error {
	klog.Infof("%s Reconciling categories for cluster %s", rctx.LogPrefix, rctx.Cluster.Name)
	defaultCategories := getDefaultCAPICategoryIdentifiers(rctx.Cluster.Name)
	_, err := getOrCreateCategories(rctx.NutanixClient, defaultCategories)
	if err != nil {
		return err
	}
	return nil
}

func (r *NutanixClusterReconciler) reconcileCategoriesDelete(rctx *nctx.ClusterContext) error {
	klog.Infof("%s Reconciling deletion of categories for cluster %s", rctx.LogPrefix, rctx.Cluster.Name)
	defaultCategories := getDefaultCAPICategoryIdentifiers(rctx.Cluster.Name)
	err := deleteCategories(rctx.NutanixClient, defaultCategories)
	if err != nil {
		return err
	}
	return nil
}
