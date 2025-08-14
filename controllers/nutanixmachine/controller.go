/*
Copyright 2025 Nutanix Inc.

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

package nutanixmachine

import (
	"context"
	"fmt"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	"github.com/nutanix-cloud-native/cluster-api-provider-nutanix/controllers"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	capiutil "sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/annotations"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	projectKind = "project"

	deviceTypeCDROM = "CDROM"
	adapterTypeIDE  = "IDE"

	// NutanixMachine condition types extracted from controller comments
	NutanixMachineVmNeedsDeletion            capiv1.ConditionType = "VmNeedsDeletion"
	NutanixMachineFinalizerRemoved           capiv1.ConditionType = "FinalizerRemoved"
	NutanixMachineVolumeGroupDetachNeeded    capiv1.ConditionType = "VolumeGroupDetachNeeded"
	NutanixMachineVolumeGroupDetach          capiv1.ConditionType = "VolumeGroupDetach"
	NutanixMachineVmDelete                   capiv1.ConditionType = "VmDelete"
	NutanixMachineAddFinalizer               capiv1.ConditionType = "AddFinalizer"
	NutanixMachineProviderIdReady            capiv1.ConditionType = "ProviderIdReady"
	NutanixMachineClusterInfrastructureReady capiv1.ConditionType = "ClusterInfrastructureReady"
	NutanixMachineBootstrapDataReady         capiv1.ConditionType = "BootstrapDataReady"
	NutanixMachineVMReady                    capiv1.ConditionType = "VMReady"
	NutanixMachineFailureDomainReady         capiv1.ConditionType = "FailureDomainReady"
	NutanixMachineIPAddressesAssigned        capiv1.ConditionType = "IPAddressesAssigned"
)

var (
	minMachineSystemDiskSize resource.Quantity
	minMachineDataDiskSize   resource.Quantity
	minMachineMemorySize     resource.Quantity
	minVCPUsPerSocket        = 1
	minVCPUSockets           = 1
)

func init() {
	minMachineSystemDiskSize = resource.MustParse("20Gi")
	minMachineDataDiskSize = resource.MustParse("1Gi")
	minMachineMemorySize = resource.MustParse("2Gi")
}

//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;update;delete
//+kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch;patch
//+kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;update;delete
//+kubebuilder:rbac:groups=cluster.x-k8s.io,resources=machines;machines/status,verbs=get;list;watch
//+kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nutanixmachines,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nutanixmachines/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nutanixmachines/finalizers,verbs=update
//+kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nutanixclusters,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=bootstrap.cluster.x-k8s.io,resources=kubeadmconfigs,verbs=get;list;watch;update;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the NutanixMachine object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.8.3/pkg/reconcile
func (r *NutanixMachineReconciler) Reconcile(ctx context.Context, req ctrl.Request) (res ctrl.Result, reterr error) {
	log := log.FromContext(ctx)
	log.Info("Reconciling the NutanixMachine.")

	// Get the NutanixMachine resource for this request.
	ntxMachine := &infrav1.NutanixMachine{}
	err := r.Get(ctx, req.NamespacedName, ntxMachine)
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("NutanixMachine not found. Ignoring since object must be deleted.")
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		log.Error(err, "Failed to fetch the NutanixMachine object")
		return reconcile.Result{}, err
	}

	// Fetch the CAPI Machine.
	machine, err := capiutil.GetOwnerMachine(ctx, r.Client, ntxMachine.ObjectMeta)
	if err != nil {
		log.Error(err, "Failed to fetch the owner CAPI Machine object")
		return reconcile.Result{}, err
	}
	if machine == nil {
		log.Info("Waiting for capi Machine Controller to set OwnerRef on NutanixMachine")
		return reconcile.Result{}, nil
	}
	log.Info(fmt.Sprintf("Fetched the owner Machine: %s", machine.Name))

	// Fetch the CAPI Cluster.
	cluster, err := capiutil.GetClusterFromMetadata(ctx, r.Client, machine.ObjectMeta)
	if err != nil {
		log.Error(err, "Machine is missing cluster label or cluster does not exist")
		return reconcile.Result{}, nil
	}

	// Stop reconciling if the cluster is paused.
	if annotations.IsPaused(cluster, machine) {
		log.V(1).Info("linked to a cluster that is paused")
		return reconcile.Result{}, nil
	}

	// Fetch the NutanixCluster
	ntxCluster := &infrav1.NutanixCluster{}
	nclKey := client.ObjectKey{
		Namespace: cluster.Namespace,
		Name:      cluster.Spec.InfrastructureRef.Name,
	}
	err = r.Get(ctx, nclKey, ntxCluster)
	if err != nil {
		log.Error(err, "Waiting for NutanixCluster")
		return reconcile.Result{}, nil
	}

	// Initialize the patch helper.
	patchHelper, err := patch.NewHelper(ntxMachine, r.Client)
	if err != nil {
		log.Error(err, "failed to configure the patch helper")
		return ctrl.Result{Requeue: true}, nil
	}

	// Create a Nutanix clients for the NutanixCluster.
	clients, err := controllers.CreateNutanixClients(ctx, ntxCluster, controllers.Informers{
		SecretInformer:    r.SecretInformer,
		ConfigMapInformer: r.ConfigMapInformer,
	})
	if err != nil {
		log.Error(err, "failed to create Nutanix clients")
		return ctrl.Result{Requeue: true}, nil
	}

	// Create a NutanixExtendedContext.
	nctx := &controllers.NutanixExtendedContext{
		ExtendedContext: controllers.ExtendedContext{
			Context:     ctx,
			Client:      r.Client,
			PatchHelper: patchHelper,
		},
		NutanixClients: clients,
	}

	// Create a NutanixMachineScope.
	scope := NewNutanixMachineScope(ntxCluster, ntxMachine, cluster, machine)

	// Handle deleted machines
	if !ntxMachine.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(nctx, scope)
	}

	// Handle non-deleted machines
	return r.reconcileNormal(nctx, scope)
}

func (r *NutanixMachineReconciler) reconcileDelete(nctx *controllers.NutanixExtendedContext, scope *NutanixMachineScope) (reconcile.Result, error) {
	ctx := nctx.Context
	log := ctrl.LoggerFrom(ctx)

	log.Info("Reconciling NutanixMachine deletion", "NutanixMachine", scope.NutanixMachine.Name)

	uowBatch := r.GetUoWDeleteBatch()
	result, err := uowBatch.Execute(nctx, scope)
	if err != nil {
		log.Error(err, "failed to execute UoW batch")
		return ctrl.Result{Requeue: true}, err
	}

	return result, nil
}

func (r *NutanixMachineReconciler) reconcileNormal(nctx *controllers.NutanixExtendedContext, scope *NutanixMachineScope) (reconcile.Result, error) {
	ctx := nctx.Context
	log := ctrl.LoggerFrom(ctx)

	log.Info("Reconciling NutanixMachine normal", "NutanixMachine", scope.NutanixMachine.Name)

	uowBatch := r.GetUoWNormalBatch()
	result, err := uowBatch.Execute(nctx, scope)
	if err != nil {
		log.Error(err, "failed to execute UoW batch")
		return ctrl.Result{Requeue: true}, err
	}

	return result, nil
}
