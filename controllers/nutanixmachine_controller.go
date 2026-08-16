/*
Copyright 2022 Nutanix

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
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/google/uuid"
	multidomainModels "github.com/nutanix/ntnx-api-golang-clients/multidomain-go-client/v4/models/multidomain/v4/config"
	vmmconfig "github.com/nutanix/ntnx-api-golang-clients/vmm-go-client/v4/models/vmm/v4/ahv/config"
	imageModels "github.com/nutanix/ntnx-api-golang-clients/vmm-go-client/v4/models/vmm/v4/content"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	apitypes "k8s.io/apimachinery/pkg/types"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	coreinformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/utils/ptr"
	capiv1beta1 "sigs.k8s.io/cluster-api/api/core/v1beta1" //nolint:staticcheck // suppress complaining on Deprecated package
	capiv1beta2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	capiutil "sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/annotations"
	v1beta1conditions "sigs.k8s.io/cluster-api/util/deprecated/v1beta1/conditions"         //nolint:staticcheck // suppress complaining on Deprecated package
	v1beta2conditions "sigs.k8s.io/cluster-api/util/deprecated/v1beta1/conditions/v1beta2" //nolint:staticcheck // suppress complaining on Deprecated package
	v1beta1patch "sigs.k8s.io/cluster-api/util/deprecated/v1beta1/patch"                   //nolint:staticcheck // suppress complaining on Deprecated package

	"sigs.k8s.io/cluster-api/util/predicates"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	ctrlutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	nctx "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/pkg/context"
	v4Converged "github.com/nutanix-cloud-native/prism-go-client/converged/v4"
	prismclientv3 "github.com/nutanix-cloud-native/prism-go-client/v3"
)

var (
	minMachineSystemDiskSize resource.Quantity
	minMachineDataDiskSize   resource.Quantity
	minMachineMemorySize     resource.Quantity
	minVCPUsPerSocket        = 1
	minVCPUSockets           = 1
)

const (
	vmCustomAttributePrefix4ProviderID = "providerid:"
)

func init() {
	minMachineSystemDiskSize = resource.MustParse("20Gi")
	minMachineDataDiskSize = resource.MustParse("1Gi")
	minMachineMemorySize = resource.MustParse("2Gi")
}

// NutanixMachineReconciler reconciles a NutanixMachine object
type NutanixMachineReconciler struct {
	client.Client
	// APIReader reads directly from the API server (bypassing the cache). Metro VM placement uses it
	// to enumerate sibling NutanixMachines so concurrent reconciles all observe the same set and
	// compute the same balanced placement, instead of racing on a stale informer cache.
	APIReader         client.Reader
	SecretInformer    coreinformers.SecretInformer
	ConfigMapInformer coreinformers.ConfigMapInformer
	Scheme            *runtime.Scheme
	controllerConfig  *ControllerConfig
}

func NewNutanixMachineReconciler(client client.Client, secretInformer coreinformers.SecretInformer, configMapInformer coreinformers.ConfigMapInformer, scheme *runtime.Scheme, copts ...ControllerConfigOpts) (*NutanixMachineReconciler, error) {
	controllerConf := &ControllerConfig{}
	for _, opt := range copts {
		if err := opt(controllerConf); err != nil {
			return nil, err
		}
	}

	return &NutanixMachineReconciler{
		Client:            client,
		SecretInformer:    secretInformer,
		ConfigMapInformer: configMapInformer,
		Scheme:            scheme,
		controllerConfig:  controllerConf,
	}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *NutanixMachineReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	if r.APIReader == nil {
		r.APIReader = mgr.GetAPIReader()
	}
	copts := controller.Options{
		MaxConcurrentReconciles: r.controllerConfig.MaxConcurrentReconciles,
		RateLimiter:             r.controllerConfig.RateLimiter,
		SkipNameValidation:      ptr.To(r.controllerConfig.SkipNameValidation),
	}

	clusterToObjectFunc, err := capiutil.ClusterToTypedObjectsMapper(r.Client, &infrav1.NutanixMachineList{}, mgr.GetScheme())
	if err != nil {
		return fmt.Errorf("failed to create mapper for Cluster to NutanixMachine: %s", err)
	}

	return ctrl.NewControllerManagedBy(mgr).
		Named("nutanixmachine-controller").
		For(&infrav1.NutanixMachine{}).
		// Watch the CAPI resource that owns this infrastructure resource.
		Watches(
			&capiv1beta2.Machine{},
			handler.EnqueueRequestsFromMapFunc(
				capiutil.MachineToInfrastructureMapFunc(
					infrav1.GroupVersion.WithKind("NutanixMachine"),
				),
			),
		).
		Watches(
			&infrav1.NutanixCluster{},
			handler.EnqueueRequestsFromMapFunc(
				r.mapNutanixClusterToNutanixMachines(),
			),
		).
		Watches(
			&capiv1beta2.Cluster{},
			handler.EnqueueRequestsFromMapFunc(clusterToObjectFunc),
			builder.WithPredicates(predicates.ClusterPausedTransitionsOrInfrastructureProvisioned(r.Scheme, ctrl.LoggerFrom(ctx))),
		).
		WithOptions(copts).
		Complete(r)
}

func (r *NutanixMachineReconciler) mapNutanixClusterToNutanixMachines() handler.MapFunc {
	return func(ctx context.Context, o client.Object) []ctrl.Request {
		log := ctrl.LoggerFrom(ctx)
		nutanixCluster, ok := o.(*infrav1.NutanixCluster)
		if !ok {
			log.Error(fmt.Errorf("expected a NutanixCluster object in mapNutanixClusterToNutanixMachines but was %T", o), "unexpected type")
			return nil
		}

		cluster, err := capiutil.GetOwnerCluster(ctx, r.Client, nutanixCluster.ObjectMeta)
		if apierrors.IsNotFound(err) || cluster == nil {
			log.V(1).Info(fmt.Sprintf("CAPI cluster for NutanixCluster %s not found", nutanixCluster.Name))
			return nil
		}
		if err != nil {
			log.Error(err, "error occurred finding CAPI cluster for NutanixCluster")
			return nil
		}
		searchLabels := map[string]string{capiv1beta2.ClusterNameLabel: cluster.Name}
		machineList := &capiv1beta2.MachineList{}
		if err := r.List(ctx, machineList, client.InNamespace(cluster.Namespace), client.MatchingLabels(searchLabels)); err != nil {
			log.V(1).Error(err, "failed to list machines for cluster")
			return nil
		}
		requests := make([]ctrl.Request, 0)
		for _, m := range machineList.Items {
			if m.Spec.InfrastructureRef.Name == "" || m.Spec.InfrastructureRef.Kind != "NutanixMachine" {
				continue
			}

			name := client.ObjectKey{Namespace: m.Namespace, Name: m.Spec.InfrastructureRef.Name}
			requests = append(requests, ctrl.Request{NamespacedName: name})
		}

		return requests
	}
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
	patchHelper, err := v1beta1patch.NewHelper(ntxMachine, r.Client)
	if err != nil {
		log.Error(err, "failed to configure the patch helper")
		return ctrl.Result{Requeue: true}, nil
	}

	log.Info(fmt.Sprintf("Reconciling NutanixMachine %s in namespace %s", ntxMachine.Name, ntxMachine.Namespace))
	// Create a Nutanix client for the NutanixCluster.
	v3Client, err := getPrismCentralClientForCluster(ctx, ntxCluster, r.SecretInformer, r.ConfigMapInformer)
	if err != nil {
		log.Error(err, "error occurred while fetching prism central client")
		return reconcile.Result{}, err
	}

	convergedClient, err := getPrismCentralConvergedV4ClientForCluster(ctx, ntxCluster, r.SecretInformer, r.ConfigMapInformer)
	if err != nil {
		log.Error(err, "error occurred while fetching prism central converged client")
		return reconcile.Result{}, err
	}

	rctx := &nctx.MachineContext{
		Context:         ctx,
		Cluster:         cluster,
		Machine:         machine,
		NutanixCluster:  ntxCluster,
		NutanixMachine:  ntxMachine,
		NutanixClient:   v3Client,
		ConvergedClient: convergedClient,
		Datastore:       map[string]*string{},
	}

	defer func() {
		if err == nil {
			// Always attempt to Patch the NutanixMachine object and its status after each reconciliation.
			if err := patchHelper.Patch(ctx, ntxMachine); err != nil {
				log.Error(err, "failed to patch NutanixMachine")
				reterr = kerrors.NewAggregate([]error{reterr, err})
			}
			log.V(1).Info(fmt.Sprintf("Patched NutanixMachine. Spec: %+v. Status: %+v.",
				ntxMachine.Spec, ntxMachine.Status))
		} else {
			log.Error(err, "not patching NutanixMachine since error occurred")
		}
	}()

	// Handle deleted machines
	if !ntxMachine.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(rctx)
	}

	// Handle non-deleted machines
	return r.reconcileNormal(rctx)
}

func (r *NutanixMachineReconciler) reconcileDelete(rctx *nctx.MachineContext) (reconcile.Result, error) {
	ctx := rctx.Context
	log := ctrl.LoggerFrom(ctx)
	convergedClient := rctx.ConvergedClient
	vmName := rctx.Machine.Name
	log.Info(fmt.Sprintf("Handling deletion of VM: %s", vmName))
	v1beta1conditions.MarkFalse(rctx.NutanixMachine, infrav1.VMProvisionedCondition, capiv1beta1.DeletingReason, capiv1beta1.ConditionSeverityInfo, "")
	v1beta2conditions.Set(rctx.NutanixMachine, metav1.Condition{
		Type:   string(infrav1.VMProvisionedCondition),
		Status: metav1.ConditionFalse,
		Reason: capiv1beta1.DeletingReason,
	})

	// Project resolution/validation is intentionally skipped during delete.
	// We already have the VM UUID, so we delete that specific VM regardless of
	// project membership. Resolving the project here would only add failure
	// modes (e.g. project lookup errors) that can block deletion indefinitely.
	vmUUID, err := GetVMUUID(rctx.Machine, rctx.NutanixMachine)
	if err != nil {
		errorMsg := fmt.Errorf("failed to get VM UUID during delete: %w", err)
		log.Error(errorMsg, "failed to delete VM")
		return reconcile.Result{}, errorMsg
	}

	// Check if VMUUID is absent
	if vmUUID == "" {
		log.Info(fmt.Sprintf("VM UUID was not found in spec for VM %s. Skipping delete", vmName))
		log.Info(fmt.Sprintf("Removing finalizers for VM %s during delete reconciliation", vmName))
		ctrlutil.RemoveFinalizer(rctx.NutanixMachine, infrav1.NutanixMachineFinalizer)
		ctrlutil.RemoveFinalizer(rctx.NutanixMachine, infrav1.DeprecatedNutanixMachineFinalizer)
		return reconcile.Result{}, nil
	}

	vm, err := FindVMByUUID(ctx, convergedClient, vmUUID, nil, rctx.PCVersion)
	if err != nil {
		errorMsg := fmt.Errorf("error finding VM %s with UUID %s: %w", vmName, vmUUID, err)
		log.Error(errorMsg, "error finding VM")
		v1beta1conditions.MarkFalse(rctx.NutanixMachine, infrav1.VMProvisionedCondition, infrav1.DeletionFailed, capiv1beta1.ConditionSeverityWarning, "%s", errorMsg.Error())
		v1beta2conditions.Set(rctx.NutanixMachine, metav1.Condition{
			Type:    string(infrav1.VMProvisionedCondition),
			Status:  metav1.ConditionFalse,
			Reason:  infrav1.DeletionFailed,
			Message: errorMsg.Error(),
		})
		return reconcile.Result{}, errorMsg
	}

	if vm == nil {
		log.Info(fmt.Sprintf("no VM found with UUID %s: assuming it is already deleted; skipping delete", vmUUID))
		log.Info(fmt.Sprintf("removing finalizers for VM %s during delete reconciliation", vmName))
		ctrlutil.RemoveFinalizer(rctx.NutanixMachine, infrav1.NutanixMachineFinalizer)
		ctrlutil.RemoveFinalizer(rctx.NutanixMachine, infrav1.DeprecatedNutanixMachineFinalizer)
		return reconcile.Result{}, nil
	}

	// Check if the VM name matches the Machine name or the NutanixMachine name.
	// Earlier, we were creating VMs with the same name as the NutanixMachine name.
	// Now, we create VMs with the same name as the Machine name in line with other CAPI providers.
	// This check is to ensure that we are deleting the correct VM for both cases as older CAPX VMs
	// will have the NutanixMachine name as the VM name.
	if *vm.Name != vmName && *vm.Name != rctx.NutanixMachine.Name {
		return reconcile.Result{}, fmt.Errorf("found VM with UUID %s but name %s did not match Machine name %s or NutanixMachineName %s", vmUUID, *vm.Name, vmName, rctx.NutanixMachine.Name)
	}

	log.V(1).Info(fmt.Sprintf("Found VM %s with UUID %s.", *vm.Name, vmUUID))

	taskInProgress, err := VmHasTaskInProgress(ctx, convergedClient, vmUUID, nil, rctx.PCVersion)
	if err != nil {
		errorMsg := fmt.Errorf("error occurred while fetching running task from VM: %w", err)
		log.Error(errorMsg, "error fetching running task from VM")
		v1beta1conditions.MarkFalse(rctx.NutanixMachine, infrav1.VMProvisionedCondition, infrav1.DeletionFailed, capiv1beta1.ConditionSeverityWarning, "%s", errorMsg.Error())
		v1beta2conditions.Set(rctx.NutanixMachine, metav1.Condition{
			Type:    string(infrav1.VMProvisionedCondition),
			Status:  metav1.ConditionFalse,
			Reason:  infrav1.DeletionFailed,
			Message: errorMsg.Error(),
		})
		return reconcile.Result{}, errorMsg
	}
	if taskInProgress {
		log.Info(fmt.Sprintf("VM %s has tasks in progress. Requeuing", vmName))
		return reconcile.Result{RequeueAfter: 5 * time.Second}, nil
	} else {
		log.V(1).Info(fmt.Sprintf("no running tasks anymore... Initiating delete for VM %s with UUID %s", vmName, vmUUID))
	}

	var vgDetachNeeded bool
	for _, disk := range vm.Disks {
		if isBackedByVolumeGroupReference(&disk) {
			vgDetachNeeded = true
			break
		}
	}
	if vgDetachNeeded {
		if err := r.detachVolumeGroups(rctx, vmName, vmUUID, vm.Disks); err != nil {
			err := fmt.Errorf("failed to detach volume groups from VM %s with UUID %s: %w", vmName, vmUUID, err)
			log.Error(err, "failed to detach volume groups from VM")
			v1beta1conditions.MarkFalse(rctx.NutanixMachine, infrav1.VMProvisionedCondition, infrav1.VolumeGroupDetachFailed, capiv1beta1.ConditionSeverityWarning, "%s", err.Error())
			v1beta2conditions.Set(rctx.NutanixMachine, metav1.Condition{
				Type:    string(infrav1.VMProvisionedCondition),
				Status:  metav1.ConditionFalse,
				Reason:  infrav1.VolumeGroupDetachFailed,
				Message: err.Error(),
			})

			return reconcile.Result{}, err
		}

		// Requeue to wait for volume group detach tasks to complete. This is done instead of blocking on task
		// completion to avoid long-running reconcile loops.
		log.Info(fmt.Sprintf("detaching volume groups from VM %s with UUID %s; requeueing again after %s", vmName, vmUUID, detachVGRequeueAfter))
		return reconcile.Result{RequeueAfter: detachVGRequeueAfter}, nil
	}

	// Delete the VM since the VM was found (err was nil)
	deleteTaskUUID, err := DeleteVM(ctx, convergedClient, vmName, vmUUID)
	if err != nil {
		err := fmt.Errorf("failed to delete VM %s with UUID %s: %w", vmName, vmUUID, err)
		log.Error(err, "failed to delete VM")
		v1beta1conditions.MarkFalse(rctx.NutanixMachine, infrav1.VMProvisionedCondition, infrav1.DeletionFailed, capiv1beta1.ConditionSeverityWarning, "%s", err.Error())
		v1beta2conditions.Set(rctx.NutanixMachine, metav1.Condition{
			Type:    string(infrav1.VMProvisionedCondition),
			Status:  metav1.ConditionFalse,
			Reason:  infrav1.DeletionFailed,
			Message: err.Error(),
		})

		return reconcile.Result{}, err
	}
	log.Info(fmt.Sprintf("Deletion task with UUID %s received for vm %s with UUID %s. Requeueing", deleteTaskUUID, vmName, vmUUID))
	return reconcile.Result{RequeueAfter: 5 * time.Second}, nil
}

func (r *NutanixMachineReconciler) detachVolumeGroups(rctx *nctx.MachineContext, vmName string, vmUUID string, vmDiskList []vmmconfig.Disk) error {
	if err := detachVolumeGroupsFromVM(rctx.Context, rctx.ConvergedClient, vmName, vmUUID, vmDiskList); err != nil {
		return fmt.Errorf("failed to detach volume groups from VM %s with UUID %s: %w", vmName, vmUUID, err)
	}
	return nil
}

//nolint:gocognit // reconcileNormal orchestrates the full VM provisioning flow; splitting it would reduce readability
func (r *NutanixMachineReconciler) reconcileNormal(rctx *nctx.MachineContext) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(rctx.Context)
	if rctx.NutanixMachine.Status.FailureReason != nil || rctx.NutanixMachine.Status.FailureMessage != nil {
		log.Error(fmt.Errorf("nutanix machine has failed. Will not reconcile"), "nutanix machine failed")
		return reconcile.Result{}, nil
	}
	log.Info("Handling NutanixMachine reconciling")
	var err error

	// Add finalizer first if not exist to avoid the race condition between init and delete
	if !ctrlutil.ContainsFinalizer(rctx.NutanixMachine, infrav1.NutanixMachineFinalizer) {
		ctrlutil.AddFinalizer(rctx.NutanixMachine, infrav1.NutanixMachineFinalizer)
	}
	ctrlutil.RemoveFinalizer(rctx.NutanixMachine, infrav1.DeprecatedNutanixMachineFinalizer)

	log.V(1).Info(fmt.Sprintf("Checking current machine status for machine %s: Status %+v Spec %+v", rctx.NutanixMachine.Name, rctx.NutanixMachine.Status, rctx.NutanixMachine.Spec))
	if rctx.NutanixMachine.Status.Ready {
		infraReady := rctx.Cluster.Status.Initialization.InfrastructureProvisioned != nil && *rctx.Cluster.Status.Initialization.InfrastructureProvisioned
		if !infraReady || rctx.Machine.Spec.ProviderID == "" {
			log.Info("The NutanixMachine is ready, wait for the owner Machine's update.")
			return reconcile.Result{RequeueAfter: 5 * time.Second}, nil
		}
		log.Info(fmt.Sprintf("The NutanixMachine is ready, providerID: %s", rctx.NutanixMachine.Spec.ProviderID))

		// Sync VmUUID with SystemUUID if they differ
		if err := r.syncVmUUID(rctx, rctx.NutanixMachine.Status.VmUUID); err != nil {
			log.Error(err, "Failed to sync VmUUID with SystemUUID")
			return reconcile.Result{}, err
		}

		return reconcile.Result{}, nil
	}

	// Make sure Cluster.Status.InfrastructureReady is true
	log.Info("Checking if cluster infrastructure is ready")
	infraReady := rctx.Cluster.Status.Initialization.InfrastructureProvisioned != nil && *rctx.Cluster.Status.Initialization.InfrastructureProvisioned
	if !infraReady {
		log.Info("The cluster infrastructure is not ready yet")
		v1beta1conditions.MarkFalse(rctx.NutanixMachine, infrav1.VMProvisionedCondition, infrav1.ClusterInfrastructureNotReady, capiv1beta1.ConditionSeverityInfo, "")
		v1beta2conditions.Set(rctx.NutanixMachine, metav1.Condition{
			Type:   string(infrav1.VMProvisionedCondition),
			Status: metav1.ConditionFalse,
			Reason: infrav1.ClusterInfrastructureNotReady,
		})
		return reconcile.Result{}, nil
	}

	// Make sure bootstrap data is available and populated.
	if ready := r.ensureBootstrapRef(rctx); !ready {
		return reconcile.Result{}, nil
	}

	// Get the PC version
	pcVersion, err := rctx.ConvergedClient.DomainManager.GetPrismCentralVersion(rctx.Context)
	if err != nil {
		log.Error(err, fmt.Sprintf("failed to get the PC version for cluster %s", rctx.NutanixCluster.Name))
		return reconcile.Result{}, fmt.Errorf("failed to get the PC version for cluster %s: %w", rctx.NutanixCluster.Name, err)
	}

	rctx.PCVersion = pcVersion
	log.V(1).Info(fmt.Sprintf("PC version %s", pcVersion))

	// Get project policy annotation and set it on the NutanixMachine object
	projectPolicy, ok := rctx.Cluster.Annotations[CAPXProjectPolicyAnnotation]
	if !ok {
		projectPolicy = CAPXProjectPolicyUnrestricted
	}

	rctx.ProjectPolicy = projectPolicy
	log.V(1).Info(fmt.Sprintf("Project policy %s on the NutanixMachine object: %s", projectPolicy, rctx.NutanixMachine.Name))

	// Resolve effective project early for project-scoped lookups
	effectiveProject, err := r.resolveEffectiveProject(rctx)
	if err != nil {
		errorMsg := fmt.Errorf("error occurred while resolving project for VM %s: %w", rctx.Machine.Name, err)
		if !isRetryableAPIError(err) {
			rctx.SetFailureStatus(createErrorFailureReason, errorMsg)
		}
		return reconcile.Result{}, errorMsg
	}
	if err := r.validateProjectPolicy(rctx, effectiveProject); err != nil {
		errorMsg := fmt.Errorf("project policy validation failed for VM %s: %w", rctx.Machine.Name, err)
		if !isRetryableAPIError(err) {
			rctx.SetFailureStatus(createErrorFailureReason, errorMsg)
		}
		return reconcile.Result{}, errorMsg
	}
	if effectiveProject != nil {
		log.V(1).Info(fmt.Sprintf("Effective project ExtID for VM %s: %s", rctx.Machine.Name, *effectiveProject.ExtID))
	} else {
		log.V(1).Info(fmt.Sprintf("No project validation for VM %s (PC < 7.6)", rctx.Machine.Name))
	}

	// Resolve the project-scoped resource group (PC 7.6+ only). When non-nil,
	// downstream helpers use it instead of cluster-wide APIs. Resource groups are a
	// PC 7.6+ concept, so skip resolution entirely on older PC versions even when an
	// explicit (v3-resolved) project is present.
	var resourceGroup *multidomainModels.ResourceGroup
	if isPCVersionHigherThan75(rctx.PCVersion) && effectiveProject != nil && effectiveProject.ExtID != nil {
		resourceGroup, err = resolveResourceGroup(rctx, effectiveProject)
		if err != nil {
			errorMsg := fmt.Errorf("failed to resolve resource group for VM %s: %w", rctx.Machine.Name, err)
			if !isRetryableAPIError(err) {
				rctx.SetFailureStatus(createErrorFailureReason, errorMsg)
			}
			return reconcile.Result{}, errorMsg
		}
		if resourceGroup != nil {
			log.V(1).Info(fmt.Sprintf("Resolved resource group %s for VM %s", *resourceGroup.ExtId, rctx.Machine.Name))
		}
	}

	// Create or get existing VM
	vm, err := r.getOrCreateVM(rctx, effectiveProject, resourceGroup)
	if err != nil {
		log.Error(err, fmt.Sprintf("Failed to create VM %s.", rctx.Machine.Name))
		return reconcile.Result{}, err
	}
	log.V(1).Info(fmt.Sprintf("Found VM with name: %s, vmUUID: %s", rctx.Machine.Name, *vm.ExtId))

	// API errors are retried on the next loop without blocking VM provisioning progress.
	if err := r.addCustomAttributes(rctx, vm); err != nil {
		log.Error(err, fmt.Sprintf("Failed to add custom attributes to VM %s.", rctx.Machine.Name))
		return reconcile.Result{}, err
	}

	// Power-on is an explicit reconcile step after VM discovery/creation.
	if vm.PowerState == nil || *vm.PowerState != vmmconfig.POWERSTATE_ON {
		vm, err = r.powerOnVM(rctx, *vm.ExtId, rctx.Machine.Name, effectiveProject)
		if err != nil {
			log.Error(err, fmt.Sprintf("Failed to power on VM %s.", rctx.Machine.Name))
			return reconcile.Result{}, err
		}
	}

	// Set and sync VmUUID with SystemUUID to ensure consistency
	if err := r.syncVmUUID(rctx, *vm.ExtId); err != nil {
		log.Error(err, "Failed to sync VmUUID")
		return reconcile.Result{}, err
	}

	// Snapshot before checkFailureDomainStatus/checkVHADomainCategory mutate the object, so the
	// patchMachine call below has a valid pre-mutation baseline to diff against - see patchMachine.
	beforeFailureDomainAndVHACheck := rctx.NutanixMachine.DeepCopy()

	// Set the NutanixMachine.status.failureDomain if the Machine is created with failureDomain
	if err = r.checkFailureDomainStatus(rctx); err != nil {
		log.Error(err, "Failed to check/set status.failureDomain")
		return reconcile.Result{}, err
	}

	// In case of the Metro use case, make sure the VM has one and only one category with the
	// vHADomain key "k8s-vha-native-site".
	if err = r.checkVHADomainCategory(rctx, vm); err != nil {
		log.Error(err, "Failed to check the VHADomainCategory for the VM")
		return reconcile.Result{}, err
	}

	log.V(1).Info(fmt.Sprintf("Patching machine post creation vmUUID: %s", rctx.NutanixMachine.Status.VmUUID))
	if err := r.patchMachine(rctx, beforeFailureDomainAndVHACheck); err != nil {
		errorMsg := fmt.Errorf("failed to patch NutanixMachine %s after creation: %w", rctx.NutanixMachine.Name, err)
		log.Error(errorMsg, "failed to patch")
		return reconcile.Result{}, errorMsg
	}

	log.Info(fmt.Sprintf("Assigning IP addresses to VM with name: %s, vmUUID: %s", rctx.NutanixMachine.Name, rctx.NutanixMachine.Status.VmUUID))
	if err := r.assignAddressesToMachine(rctx, vm); err != nil {
		errorMsg := fmt.Errorf("failed to assign addresses to VM %s with UUID %s: %w", rctx.Machine.Name, rctx.NutanixMachine.Status.VmUUID, err)
		log.Error(errorMsg, "failed to assign addresses")
		v1beta1conditions.MarkFalse(rctx.NutanixMachine, infrav1.VMAddressesAssignedCondition, infrav1.VMAddressesFailed, capiv1beta1.ConditionSeverityError, "%s", err.Error())
		v1beta2conditions.Set(rctx.NutanixMachine, metav1.Condition{
			Type:    string(infrav1.VMAddressesAssignedCondition),
			Status:  metav1.ConditionFalse,
			Reason:  infrav1.VMAddressesFailed,
			Message: err.Error(),
		})
		return reconcile.Result{}, errorMsg
	}

	v1beta1conditions.MarkTrue(rctx.NutanixMachine, infrav1.VMAddressesAssignedCondition)
	v1beta2conditions.Set(rctx.NutanixMachine, metav1.Condition{
		Type:   string(infrav1.VMAddressesAssignedCondition),
		Status: metav1.ConditionTrue,
		Reason: infrav1.Succeeded,
	})

	rctx.NutanixMachine.Status.Ready = true
	log.V(1).Info(fmt.Sprintf("Created VM %s for cluster %s, update NutanixMachine spec.providerID to %s, and machinespec %+v, vmUuid: %s",
		rctx.Machine.Name, rctx.NutanixCluster.Name, rctx.NutanixMachine.Spec.ProviderID,
		rctx.NutanixMachine, rctx.NutanixMachine.Status.VmUUID))
	return reconcile.Result{}, nil
}

// ensureBootstrapRef checks that the bootstrap data reference is populated on
// the NutanixMachine. Returns true when the ref is ready and reconciliation
// can proceed, or false when the caller should return early and wait.
func (r *NutanixMachineReconciler) ensureBootstrapRef(rctx *nctx.MachineContext) bool {
	log := ctrl.LoggerFrom(rctx.Context)

	if rctx.NutanixMachine.Spec.BootstrapRef != nil {
		return true
	}

	if rctx.Machine.Spec.Bootstrap.DataSecretName == nil {
		controlPlaneInitialized := rctx.Cluster.Status.Initialization.ControlPlaneInitialized != nil && *rctx.Cluster.Status.Initialization.ControlPlaneInitialized
		if !nctx.IsControlPlaneMachine(rctx.NutanixMachine) && !controlPlaneInitialized {
			log.Info("Waiting for the control plane to be initialized")
			v1beta1conditions.MarkFalse(rctx.NutanixMachine, infrav1.VMProvisionedCondition, infrav1.ControlplaneNotInitialized, capiv1beta1.ConditionSeverityInfo, "")
			v1beta2conditions.Set(rctx.NutanixMachine, metav1.Condition{
				Type:   string(infrav1.VMProvisionedCondition),
				Status: metav1.ConditionFalse,
				Reason: infrav1.ControlplaneNotInitialized,
			})
		} else {
			v1beta1conditions.MarkFalse(rctx.NutanixMachine, infrav1.VMProvisionedCondition, infrav1.BootstrapDataNotReady, capiv1beta1.ConditionSeverityInfo, "")
			v1beta2conditions.Set(rctx.NutanixMachine, metav1.Condition{
				Type:   string(infrav1.VMProvisionedCondition),
				Status: metav1.ConditionFalse,
				Reason: infrav1.BootstrapDataNotReady,
			})
			log.Info("Waiting for bootstrap data to be available")
		}
		return false
	}

	rctx.NutanixMachine.Spec.BootstrapRef = &corev1.ObjectReference{
		APIVersion: "v1",
		Kind:       "Secret",
		Name:       *rctx.Machine.Spec.Bootstrap.DataSecretName,
		Namespace:  rctx.Machine.Namespace,
	}
	log.V(1).Info(fmt.Sprintf("Added the spec.bootstrapRef to NutanixMachine object: %v", rctx.NutanixMachine.Spec.BootstrapRef))
	return true
}

// syncVmUUID sets and synchronizes the NutanixMachine.Status.VmUUID with Machine.Status.NodeInfo.SystemUUID
// if available. The SystemUUID from the CAPI Machine is the source of truth as it comes from the actual node.
// If SystemUUID is not available, it falls back to using the provided vmExtId.
func (r *NutanixMachineReconciler) syncVmUUID(rctx *nctx.MachineContext, vmExtId string) error {
	log := ctrl.LoggerFrom(rctx.Context)

	targetUUID := vmExtId
	if rctx.Machine.Status.NodeInfo != nil && rctx.Machine.Status.NodeInfo.SystemUUID != "" {
		systemUUID := rctx.Machine.Status.NodeInfo.SystemUUID
		if _, err := uuid.Parse(systemUUID); err != nil {
			log.Error(err, fmt.Sprintf("Machine.Status.NodeInfo.SystemUUID is not a valid UUID: %s, falling back to vmExtId", systemUUID))
		} else {
			targetUUID = systemUUID
		}
	}

	// Update and patch if needed
	if rctx.NutanixMachine.Status.VmUUID != targetUUID {
		before := rctx.NutanixMachine.DeepCopy()
		rctx.NutanixMachine.Status.VmUUID = targetUUID
		log.Info("Updated NutanixMachine VmUUID status", "vmUUID", targetUUID)

		if err := r.patchMachine(rctx, before); err != nil {
			return fmt.Errorf("failed to patch NutanixMachine %s after setting VmUUID from %s: %w", rctx.NutanixMachine.Name, targetUUID, err)
		}
	}

	return nil
}

// checkFailureDomainStatus checks and sets the NutanixMachine.status.failureDomain if necessary
func (r *NutanixMachineReconciler) checkFailureDomainStatus(rctx *nctx.MachineContext) error {
	if rctx.Machine.Spec.FailureDomain == "" {
		return nil
	}

	fd := rctx.Machine.Spec.FailureDomain
	// Fetch the referent failure domain object
	fdSpec, err := r.getFailureDomainSpec(rctx, fd)
	if err != nil {
		return err
	}

	// Determine what PE to validate against:
	// - For Metro/MetroSite failure domains with recovery placement, validate against the active placement annotation
	// - Otherwise, validate against the native failure domain's PE
	//
	// metro.nutanix.com/active-placement-pe stores the PE cluster identifier (name or uuid string) where the VM
	// is actually placed when it differs from the native failure domain due to recovery/maintenance.
	var clusterValidationErr string
	if rctx.NutanixMachine.Annotations != nil && rctx.NutanixMachine.Annotations[metroActivePlacementPEAnnotation] != "" {
		// Recovery placement scenario: validate against the active placement PE (string comparison)
		actualPE := rctx.NutanixMachine.Spec.Cluster.String()
		expectedPE := rctx.NutanixMachine.Annotations[metroActivePlacementPEAnnotation]
		if actualPE != expectedPE {
			clusterValidationErr = fmt.Sprintf(
				"NutanixMachine.spec.cluster=%s, expected annotation %q=%s",
				rctx.NutanixMachine.Spec.Cluster.DisplayString(),
				metroActivePlacementPEAnnotation,
				expectedPE,
			)
		}
	} else {
		// Normal scenario: validate against the native failure domain's PE
		if !rctx.NutanixMachine.Spec.Cluster.EqualTo(&fdSpec.PrismElementCluster) {
			clusterValidationErr = fmt.Sprintf(
				"NutanixMachine.spec.cluster=%s, NutanixFailureDomain.spec.prismElementCluster=%s",
				rctx.NutanixMachine.Spec.Cluster.DisplayString(),
				fdSpec.PrismElementCluster.DisplayString(),
			)
		}
	}

	// Validate the NutanixMachine machine spec is consistent with the expected configuration
	// Note: Subnet validation still uses fdSpec.Subnets since subnets are symmetric across Metro sites
	errMessages := []string{}
	if clusterValidationErr != "" {
		errMessages = append(errMessages, clusterValidationErr)
	}
	if !resourceIdsEquals(rctx.NutanixMachine.Spec.Subnets, fdSpec.Subnets) {
		errMessages = append(
			errMessages,
			fmt.Sprintf(
				"NutanixMachine.spec.subnets=%v, NutanixFailureDomain.spec.subnets=%v",
				rctx.NutanixMachine.Spec.Subnets,
				fdSpec.Subnets,
			),
		)
	}
	if len(errMessages) > 0 {
		return fmt.Errorf(
			"the NutanixMachine is not consistent with the referenced NutanixFailureDomain %q: %s",
			rctx.Machine.Spec.FailureDomain,
			strings.Join(errMessages, "; "),
		)
	}

	// Set the NutanixMachine.status.failureDomain
	rctx.NutanixMachine.Status.FailureDomain = &fd

	return nil
}

// checkVHADomainCategory enforces the implicit contract that a Metro VM carries one and only one
// category with the vHADomain key "k8s-vha-native-site". This contract is relied on by CSI and NKP
// for k8s-HA. It is a no-op when the Machine is not configured with a NutanixMetro/NutanixMetroSite
// failureDomain.
func (r *NutanixMachineReconciler) checkVHADomainCategory(rctx *nctx.MachineContext, vm *vmmconfig.Vm) error {
	if !isNutanixMetroFailureDomain(rctx.Machine.Spec.FailureDomain) &&
		!isNutanixMetroSiteFailureDomain(rctx.Machine.Spec.FailureDomain) {
		return nil
	}

	count, err := countVMVHADomainCategories(rctx, r.Client, vm)
	if err != nil {
		return err
	}

	if count != 1 {
		return fmt.Errorf(
			"the Metro VM %s must have one and only one category with the vHADomain key %q from the cluster's vHADomain, but found %d",
			rctx.Machine.Name, VHADomainDefaultCategoryKey, count,
		)
	}

	return nil
}

func (r *NutanixMachineReconciler) getFailureDomainSpec(rctx *nctx.MachineContext, fdName string) (*infrav1.NutanixFailureDomainSpec, error) {
	failureDomainName := rctx.Machine.Spec.FailureDomain

	// handling the NutanixMetro failure domain
	if isNutanixMetroFailureDomain(failureDomainName) {
		return r.getMetroFailureDomainSpec(rctx, failureDomainName[len(metroFailureDomainPrefix):])
	}

	// handling the NutanixMetroSite failure domain
	if isNutanixMetroSiteFailureDomain(failureDomainName) {
		return r.getMetroSiteFailureDomainSpec(rctx, failureDomainName[len(metroSiteFailureDomainPrefix):])
	}

	// TODO: @faiq -- to handle the legacy failure domains this function checks to see if fdName
	// is present in the legacy embedded field. if it is, we return a "dummy" spec for the new failure domain
	// CR with the subnets and cluster info
	if rctx.NutanixCluster != nil && len(rctx.NutanixCluster.Spec.FailureDomains) > 0 { //nolint:staticcheck // this handles old field
		failureDomain := GetLegacyFailureDomainFromNutanixCluster(failureDomainName, rctx.NutanixCluster)
		if failureDomain != nil {
			cluster := failureDomain.Cluster
			subnets := failureDomain.Subnets
			fdSpec := &infrav1.NutanixFailureDomainSpec{
				PrismElementCluster: cluster,
				Subnets:             subnets,
			}
			return fdSpec, nil
		}
	}
	// if the old field wasn't set or the failure domain name referenced isn't present there, we
	// can assume that it is refering to the new CRD so we make a get
	fdObj := &infrav1.NutanixFailureDomain{}
	fdKey := client.ObjectKey{Name: fdName, Namespace: rctx.NutanixMachine.Namespace}
	if err := r.Get(rctx.Context, fdKey, fdObj); err != nil {
		return nil, fmt.Errorf("failed to fetch the referent failure domain object %q: %w", fdName, err)
	}
	return &fdObj.Spec, nil
}

func (r *NutanixMachineReconciler) validateFailureDomainSpec(rctx *nctx.MachineContext, fdSpec *infrav1.NutanixFailureDomainSpec, effectiveProject *nctx.ProjectInfo, resourceGroup *multidomainModels.ResourceGroup) error {
	// Validate the failure domain configuration.
	pe := fdSpec.PrismElementCluster
	peUUID, err := GetPEUUID(rctx.Context, rctx.ConvergedClient, resourceGroup, pe.Name, pe.UUID)
	if err != nil {
		return err
	}

	subnets := fdSpec.Subnets
	_, err = GetSubnetUUIDList(rctx.Context, rctx.ConvergedClient, subnets, peUUID, effectiveProject, rctx.PCVersion)
	if err != nil {
		return err
	}

	return nil
}

// getMetroFailureDomainSpec resolves a NutanixMetro failure domain to one of its two referenced
// NutanixFailureDomains, validating PE availability and deterministically selecting the preferred one
// via computeMetroPlacementIndex (greedy least-count, per-nodepool balancing).
func (r *NutanixMachineReconciler) getMetroFailureDomainSpec(rctx *nctx.MachineContext, metroName string) (*infrav1.NutanixFailureDomainSpec, error) {
	log := ctrl.LoggerFrom(rctx.Context)
	namespace := rctx.Machine.Namespace

	// Fetch the NutanixMetro and its referenced NutanixFailureDomain CRs
	metroObj, err := getNutanixMetroObject(rctx.Context, r.Client, metroName, namespace)
	if err != nil {
		return nil, err
	}

	// When the NutanixMachine's label "metro.nutanix.com/native-failuredomain" is set
	nativeFdName := ""
	if nativeFd, ok := rctx.NutanixMachine.Labels[metroNativeFailureDomainLabelKey]; ok {
		nativeFdName = nativeFd
	}

	fdCount := len(metroObj.Spec.FailureDomains)
	fdObjs := make([]*infrav1.NutanixFailureDomain, fdCount)
	for i, fdRef := range metroObj.Spec.FailureDomains {
		fdObj, err := getNutanixFailureDomainObject(rctx.Context, r.Client, fdRef.Name, namespace)
		if err != nil {
			return nil, err
		}

		// return the failureDomain spec if it is the native-failuredomain. The placement was already
		// decided on a previous reconcile, but we must still repopulate the reconcile Datastore
		// (preferred failureDomain + PE) so a VM (re)created on this reconcile still receives its
		// vHADomain category and metro custom attributes.
		if nativeFdName == fdObj.Name {
			r.storeMetroPlacementSelection(rctx, fdObj)
			return &fdObj.Spec, nil
		}

		fdObjs[i] = fdObj
	}

	if fdCount == 0 {
		return nil, fmt.Errorf("the NutanixMetro %s has no failureDomains", metroName)
	}

	// Round-robin: deterministically select the failureDomain for this machine, the other is the
	// remaining. The selection balances placement across failureDomains and is concurrency-safe
	// without serializing reconciles (see computeMetroPlacementIndex).
	idx, err := r.computeMetroPlacementIndex(rctx, fdObjs)
	if err != nil {
		return nil, err
	}

	var selectedFd, remainingFd *infrav1.NutanixFailureDomain
	for i, fdObj := range fdObjs {
		if i == idx {
			selectedFd = fdObj
		} else {
			remainingFd = fdObj
		}
	}

	// Persist native placement semantics first, then derive active placement for VM creation.
	r.storeMetroPlacementSelection(rctx, selectedFd)
	placementFd, perr := r.resolveMetroPlacementFailureDomainFromRecoveryPlanJob(rctx, metroName, selectedFd, fdObjs)
	if perr != nil {
		log.Error(perr, "Failed to resolve Metro active placement from Recovery Plan Job, fallback to native selection", "metro", metroName)
		placementFd = selectedFd
	}
	if placementFd == nil {
		placementFd = selectedFd
	}

	// Metro failure domains always use the default project (nil effectiveProject and resourceGroup)
	if err = r.validateFailureDomainSpec(rctx, &placementFd.Spec, nil, nil); err != nil {
		log.Error(err, fmt.Sprintf("The selected failureDomain %s failed at validation. Try with the other failureDomain.", placementFd.Name))

		if remainingFd == nil {
			return nil, err
		}
		if err = r.validateFailureDomainSpec(rctx, &remainingFd.Spec, nil, nil); err != nil {
			log.Error(err, fmt.Sprintf("Both failureDomains of the NutanixMetro %s failed at validation.", metroName))
			return nil, err
		}
		placementFd = remainingFd
	}

	// Set MetroRecoveryPlacement status if VM is being placed on a different site than native
	r.setMetroRecoveryPlacementStatus(rctx, selectedFd, placementFd)

	return &placementFd.Spec, nil
}

// getMetroSiteFailureDomainSpec resolves a NutanixMetroSite failure domain to its preferred
// NutanixFailureDomain (falling back to the other on validation failure).
func (r *NutanixMachineReconciler) getMetroSiteFailureDomainSpec(rctx *nctx.MachineContext, metrositeName string) (*infrav1.NutanixFailureDomainSpec, error) {
	log := ctrl.LoggerFrom(rctx.Context)
	namespace := rctx.Machine.Namespace

	// Fetch the NutanixMetroSite and its referenced NutanixMetro and NutanixFailureDomain CRs
	metrositeObj, err := getNutanixMetroSiteObject(rctx.Context, r.Client, metrositeName, namespace)
	if err != nil {
		return nil, err
	}

	metroObj, err := getNutanixMetroObject(rctx.Context, r.Client, metrositeObj.Spec.MetroRef.Name, namespace)
	if err != nil {
		return nil, err
	}

	// Keep the MetroSite's groupNameLabel in the context Datastore. This must happen before the
	// native-failuredomain early-return below so a VM (re)created on a later reconcile still gets the
	// metro node-group custom attribute.
	if metrositeObj.Spec.GroupNameLabel != nil && *metrositeObj.Spec.GroupNameLabel != "" {
		if rctx.Datastore == nil {
			rctx.Datastore = map[string]*string{}
		}
		rctx.Datastore[nctx.MetroNodeGroupNameLabel] = metrositeObj.Spec.GroupNameLabel
	}

	// When the NutanixMachine's label "metro.nutanix.com/native-failuredomain" is set
	nativeFdName := ""
	if nativeFd, ok := rctx.NutanixMachine.Labels[metroNativeFailureDomainLabelKey]; ok {
		nativeFdName = nativeFd
	}

	var selectedFd, remainingFd *infrav1.NutanixFailureDomain
	for _, fdRef := range metroObj.Spec.FailureDomains {
		fdObj, err := getNutanixFailureDomainObject(rctx.Context, r.Client, fdRef.Name, namespace)
		if err != nil {
			return nil, err
		}

		// return the failureDomain spec if it is the native-failuredomain
		if nativeFdName == fdObj.Name {
			r.storeMetroPlacementSelection(rctx, fdObj)
			return &fdObj.Spec, nil
		}

		if fdObj.Name == metrositeObj.Spec.PreferredFailureDomain.Name {
			selectedFd = fdObj
		} else {
			remainingFd = fdObj
		}
	}

	if selectedFd == nil {
		return nil, fmt.Errorf("the NutanixMetroSite %s preferredFailureDomain %s is not in the NutanixMetro %s failureDomains", metrositeName, metrositeObj.Spec.PreferredFailureDomain.Name, metroObj.Name)
	}

	// Persist native placement semantics first, then derive active placement for VM creation.
	r.storeMetroPlacementSelection(rctx, selectedFd)
	placementFd, perr := r.resolveMetroPlacementFailureDomainFromRecoveryPlanJob(rctx, metroObj.Name, selectedFd, []*infrav1.NutanixFailureDomain{selectedFd, remainingFd})
	if perr != nil {
		log.Error(perr, "Failed to resolve MetroSite active placement from Recovery Plan Job, fallback to preferred failureDomain", "metrosite", metrositeName)
		placementFd = selectedFd
	}
	if placementFd == nil {
		placementFd = selectedFd
	}

	// The selected is the preferred/native failureDomain. Only when placement failed at validation, try the remaining one.
	// MetroSite failure domains always use the default project (nil effectiveProject and resourceGroup)
	if err = r.validateFailureDomainSpec(rctx, &placementFd.Spec, nil, nil); err != nil {
		log.Error(err, fmt.Sprintf("The preferred failureDomain %s failed at validation. Try with the other failureDomain.", placementFd.Name))

		if remainingFd == nil {
			return nil, err
		}
		if err = r.validateFailureDomainSpec(rctx, &remainingFd.Spec, nil, nil); err != nil {
			log.Error(err, fmt.Sprintf("Both failureDomains of the NutanixMetro %s failed at validation.", metroObj.Name))
			return nil, err
		}
		placementFd = remainingFd
	}

	// Set MetroRecoveryPlacement status if VM is being placed on a different site than native
	r.setMetroRecoveryPlacementStatus(rctx, selectedFd, placementFd)

	return &placementFd.Spec, nil
}

func (r *NutanixMachineReconciler) resolveMetroPlacementFailureDomainFromRecoveryPlanJob(
	rctx *nctx.MachineContext,
	metroName string,
	nativeFd *infrav1.NutanixFailureDomain,
	fdObjs []*infrav1.NutanixFailureDomain,
) (*infrav1.NutanixFailureDomain, error) {
	if rctx == nil || rctx.NutanixClient == nil || rctx.NutanixClient.V3 == nil || nativeFd == nil {
		return nil, nil
	}

	rpUUID, err := findMetroRecoveryPlanUUIDByFailureDomain(rctx, r.Client, metroName, nativeFd.Name)
	if err != nil {
		return nil, err
	}
	if rpUUID == "" {
		return nil, nil
	}

	latestJob, err := latestRecoveryPlanJob(rctx.Context, rctx.NutanixClient.V3, rpUUID)
	if err != nil {
		return nil, err
	}
	if latestJob == nil {
		return nil, nil
	}

	activePEUUID := activePlacementPEUUIDFromRecoveryPlanJob(latestJob)
	if activePEUUID == "" {
		return nil, nil
	}

	for i := range fdObjs {
		fdObj := fdObjs[i]
		if fdObj == nil {
			continue
		}
		peUUID, err := resolveFailureDomainPEUUID(rctx, &fdObj.Spec)
		if err != nil {
			continue
		}
		if peUUID == activePEUUID {
			return fdObj, nil
		}
	}

	return nil, nil
}

func findMetroRecoveryPlanUUIDByFailureDomain(
	mctx *nctx.MachineContext,
	ctlclient client.Client,
	metroName, failureDomainName string,
) (string, error) {
	vHADomains, err := getOwnedVHADomains(mctx.Context, ctlclient, mctx.NutanixCluster)
	if err != nil {
		return "", err
	}

	for _, vhaDomain := range vHADomains {
		if vhaDomain.Spec.MetroRef.Name != metroName {
			continue
		}
		for _, mg := range vhaDomain.Spec.MovementGroups {
			if mg.Name != clusterScopeMovementGroupName {
				continue
			}
			for i := range mg.CategoryRecoveryPlans {
				crp := mg.CategoryRecoveryPlans[i]
				if crp.FailureDomainRef.Name != failureDomainName {
					continue
				}
				if crp.RecoveryPlan.UUID != nil && *crp.RecoveryPlan.UUID != "" {
					return *crp.RecoveryPlan.UUID, nil
				}
				return "", nil
			}
		}
	}

	return "", nil
}

func latestRecoveryPlanJob(ctx context.Context, v3Client prismclientv3.Service, recoveryPlanUUID string) (*prismclientv3.RecoveryPlanJobIntentResponse, error) {
	kind := "recovery_plan_job"
	sortAttr := "start_time_secs"
	sortOrder := "DESCENDING"
	offset := int64(0)
	length := int64(1)
	filter := fmt.Sprintf("recovery_plan_uuid==%s", recoveryPlanUUID)

	resp, err := v3Client.ListRecoveryPlanJobs(ctx, &prismclientv3.DSMetadata{
		Kind:          &kind,
		SortAttribute: &sortAttr,
		SortOrder:     &sortOrder,
		Offset:        &offset,
		Length:        &length,
		Filter:        &filter,
	})
	if err != nil {
		return nil, err
	}
	if resp == nil || len(resp.Entities) == 0 {
		return nil, nil
	}

	for i := range resp.Entities {
		job := resp.Entities[i]
		if job == nil || job.Status == nil || job.Status.Resources == nil || job.Status.Resources.RecoveryPlanReference == nil {
			continue
		}
		rpRef := job.Status.Resources.RecoveryPlanReference
		if rpRef.UUID == nil || *rpRef.UUID != recoveryPlanUUID {
			continue
		}
		return job, nil
	}

	return nil, nil
}

func activePlacementPEUUIDFromRecoveryPlanJob(job *prismclientv3.RecoveryPlanJobIntentResponse) string {
	if job == nil || job.Status == nil || job.Status.Resources == nil || job.Status.Resources.ExecutionParameters == nil {
		return ""
	}
	azList := job.Status.Resources.ExecutionParameters.RecoveryAvailabilityZoneList
	for i := range azList {
		az := azList[i]
		if az == nil {
			continue
		}
		for j := range az.ClusterReferenceList {
			clusterRef := az.ClusterReferenceList[j]
			if clusterRef != nil && clusterRef.UUID != nil && *clusterRef.UUID != "" {
				return *clusterRef.UUID
			}
		}
	}
	return ""
}

func resolveFailureDomainPEUUID(rctx *nctx.MachineContext, fdSpec *infrav1.NutanixFailureDomainSpec) (string, error) {
	pe := fdSpec.PrismElementCluster
	peCluster, err := GetPEClusterByIdentifier(rctx.Context, rctx.ConvergedClient, pe.Name, pe.UUID)
	if err != nil {
		return "", err
	}
	return ptr.Deref(peCluster.ExtId, ""), nil
}

// setMetroRecoveryPlacementStatus sets the active placement annotation and the
// MetroRecoveryPlacement condition when a VM is placed on a different site than its native
// failure domain due to maintenance or disaster recovery.
func (r *NutanixMachineReconciler) setMetroRecoveryPlacementStatus(
	rctx *nctx.MachineContext,
	nativeFd *infrav1.NutanixFailureDomain,
	placementFd *infrav1.NutanixFailureDomain,
) {
	log := ctrl.LoggerFrom(rctx.Context)

	// Only set recovery placement status if placement differs from native
	if nativeFd == nil || placementFd == nil || nativeFd.Name == placementFd.Name {
		return
	}

	// Set the active placement annotation.
	if rctx.NutanixMachine.Annotations == nil {
		rctx.NutanixMachine.Annotations = map[string]string{}
	}
	rctx.NutanixMachine.Annotations[metroActivePlacementPEAnnotation] = placementFd.Spec.PrismElementCluster.String()

	// Set the MetroRecoveryPlacement condition (v1beta1)
	v1beta1conditions.MarkTrue(rctx.NutanixMachine, infrav1.MetroRecoveryPlacementCondition)

	// Set the MetroRecoveryPlacement condition (v1beta2)
	v1beta2conditions.Set(rctx.NutanixMachine, metav1.Condition{
		Type:    string(infrav1.MetroRecoveryPlacementCondition),
		Status:  metav1.ConditionTrue,
		Reason:  infrav1.MetroRecoveryPlacementSiteMaintenanceReason,
		Message: fmt.Sprintf("Placed on paired site (%s) due to maintenance/disaster recovery activity on native site (%s)", placementFd.Name, nativeFd.Name),
	})

	log.Info("Set Metro recovery placement status: VM placed on paired site due to maintenance/disaster recovery",
		"nativeFailureDomain", nativeFd.Name,
		"nativePE", nativeFd.Spec.PrismElementCluster.String(),
		"placementFailureDomain", placementFd.Name,
		"placementPE", placementFd.Spec.PrismElementCluster.String(),
	)
}

// computeMetroPlacementIndex deterministically selects, for the machine being reconciled, the index
// into fdObjs (the metro's failureDomains) on which it should be placed. It balances placement
// without serializing reconciles (no concurrentReconciles=1) and without a name hash.
//
// Why it is concurrency-safe: every machine's NutanixMachine object already exists before its
// reconcile runs, so all concurrent reconciles enumerate the same set of siblings, sort the
// not-yet-placed ("pending") machines by name, and run the identical greedy least-count simulation.
// Each machine therefore lands in a distinct, balanced slot regardless of reconcile interleaving.
//
//   - balancing is scoped to the machine's group (MachineSet, then MachineDeployment, then
//     MachinePool, or the control plane; see metroPlacementGroupKey), so each group is spread evenly
//     across the metro's failureDomains independently of the others. MachineSet is preferred over
//     MachineDeployment so a surge-first rolling upgrade balances the new generation independently of
//     the old one still being torn down (otherwise ties skew the new generation, e.g. 3-1).
//   - already-placed machines (carry the native-failuredomain label) seed per-FD counts, so the
//     result self-heals after uneven scale-downs.
//   - terminating machines are skipped so in-flight scale-downs free up their slot.
//   - the sibling list is read uncached (APIReader) so a lagging informer cache cannot make two
//     machines pick the same slot.
//
// Ties (equal counts) are broken by failureDomain order in fdObjs.
func (r *NutanixMachineReconciler) computeMetroPlacementIndex(rctx *nctx.MachineContext, fdObjs []*infrav1.NutanixFailureDomain) (int, error) {
	fdIndex := make(map[string]int, len(fdObjs))
	for i, fdObj := range fdObjs {
		fdIndex[fdObj.Name] = i
	}
	counts := make([]int, len(fdObjs))

	reader := client.Reader(r.Client)
	if r.APIReader != nil {
		reader = r.APIReader
	}

	// Resolve the nodepool of the machine being reconciled and map every sibling NutanixMachine to
	// its nodepool via the owning CAPI Machine (the authoritative carrier of the nodepool labels).
	groupKey := ""
	if rctx.Machine != nil {
		groupKey = metroPlacementGroupKey(rctx.Machine.Labels)
	}
	nmGroupKeys, err := r.metroMachineGroupKeys(rctx, reader)
	if err != nil {
		return 0, err
	}

	// NutanixMachines do not carry CAPI's nodepool labels (those live on the owning CAPI Machine),
	// so we cannot filter them server-side by nodepool. We list them cluster-scoped and restrict to
	// the current nodepool in-memory via nmGroupKeys (already filtered to the nodepool above).
	machineList := &infrav1.NutanixMachineList{}
	if err := reader.List(rctx.Context, machineList,
		client.InNamespace(rctx.NutanixCluster.Namespace),
		client.MatchingLabels{capiv1beta2.ClusterNameLabel: rctx.Cluster.Name},
	); err != nil {
		return 0, err
	}

	pending := make([]string, 0, len(machineList.Items))
	for i := range machineList.Items {
		nm := &machineList.Items[i]
		// only balance within the same nodepool (when the current machine's nodepool is known)
		if groupKey != "" && nm.Name != rctx.NutanixMachine.Name && nmGroupKeys[nm.Name] != groupKey {
			continue
		}
		// skip machines being deleted so the balancer reacts to in-flight scale-downs
		if !nm.DeletionTimestamp.IsZero() && nm.Name != rctx.NutanixMachine.Name {
			continue
		}
		if fdName, ok := nm.Labels[metroNativeFailureDomainLabelKey]; ok {
			if idx, tracked := fdIndex[fdName]; tracked {
				counts[idx]++
			}
			continue
		}
		pending = append(pending, nm.Name)
	}
	// Ensure the machine being reconciled participates even if the list does not include it yet.
	if !slices.Contains(pending, rctx.NutanixMachine.Name) {
		pending = append(pending, rctx.NutanixMachine.Name)
	}
	slices.Sort(pending)

	// Greedy least-count assignment over pending machines in name order; the slot computed when we
	// reach this machine is its placement. Ties resolve to the lowest failureDomain index.
	for _, name := range pending {
		idx := 0
		minCount := -1
		for i, c := range counts {
			if minCount < 0 || c < minCount {
				minCount = c
				idx = i
			}
		}
		if name == rctx.NutanixMachine.Name {
			return idx, nil
		}
		counts[idx]++
	}

	return 0, nil
}

// metroPlacementGroupOwnerLabels is the ordered list of CAPI owner labels used to attribute a machine
// to a balancing group, most specific first.
//
// MachineSet is intentionally preferred over MachineDeployment: during a surge-first rolling upgrade
// (the default MachineDeployment strategy, maxSurge=1/maxUnavailable=0) the replacement machine is
// created before the machine it supersedes is deleted. If we balanced by MachineDeployment, the
// not-yet-deleted old machines would keep the per-FD counts looking balanced and ties would keep
// resolving to the first FD, skewing the new generation (e.g. 3-1 instead of 2-2). Each rollout
// generation is its own MachineSet (distinct name), so scoping to MachineSet lets the new generation
// balance independently of the old one being torn down. In steady state a MachineDeployment has
// exactly one MachineSet, so this is equivalent to MachineDeployment scoping.
var metroPlacementGroupOwnerLabels = []string{
	capiv1beta2.MachineSetNameLabel,
	capiv1beta2.MachineDeploymentNameLabel,
	capiv1beta2.MachinePoolNameLabel,
}

// metroPlacementGroupLabel returns the single owner label (key, value) that identifies the balancing
// group a machine belongs to, most specific first (see metroPlacementGroupOwnerLabels), falling back
// to the control-plane label (whose value is conventionally empty). ok is false when the machine
// cannot be attributed to any group.
func metroPlacementGroupLabel(labels map[string]string) (key, value string, ok bool) {
	for _, k := range metroPlacementGroupOwnerLabels {
		if v, present := labels[k]; present && v != "" {
			return k, v, true
		}
	}
	if v, present := labels[capiv1beta2.MachineControlPlaneLabel]; present {
		return capiv1beta2.MachineControlPlaneLabel, v, true
	}
	return "", "", false
}

// metroPlacementGroupKey returns a stable string key identifying the machine's balancing group. Metro
// placement balances within a group rather than across the whole cluster. An empty string means the
// machine could not be attributed to a group (it is then balanced cluster-wide, preserving prior
// behavior).
func metroPlacementGroupKey(labels map[string]string) string {
	key, value, ok := metroPlacementGroupLabel(labels)
	switch {
	case !ok:
		return ""
	case value == "": // e.g. the control-plane label carries no value
		return key
	default:
		return key + "=" + value
	}
}

// metroPlacementGroupSelector returns the single label that scopes a List to the machine's balancing
// group. The second return value is false when the machine cannot be attributed to a group, in which
// case callers should fall back to a cluster-wide List.
func metroPlacementGroupSelector(labels map[string]string) (client.MatchingLabels, bool) {
	key, value, ok := metroPlacementGroupLabel(labels)
	if !ok {
		return nil, false
	}
	return client.MatchingLabels{key: value}, true
}

// metroMachineGroupKeys maps each NutanixMachine name to its nodepool key, resolved via the owning
// CAPI Machine (which carries the nodepool labels). The NutanixMachine name equals the Machine's
// infrastructureRef name. When the machine being reconciled is attributed to a nodepool, the CAPI
// Machine List is filtered server-side to that nodepool so we only fetch the siblings we actually
// balance against; otherwise we fall back to listing the whole cluster.
func (r *NutanixMachineReconciler) metroMachineGroupKeys(rctx *nctx.MachineContext, reader client.Reader) (map[string]string, error) {
	listOpts := []client.ListOption{
		client.InNamespace(rctx.NutanixCluster.Namespace),
		client.MatchingLabels{capiv1beta2.ClusterNameLabel: rctx.Cluster.Name},
	}
	if rctx.Machine != nil {
		if selector, ok := metroPlacementGroupSelector(rctx.Machine.Labels); ok {
			listOpts = append(listOpts, selector)
		}
	}

	machineList := &capiv1beta2.MachineList{}
	if err := reader.List(rctx.Context, machineList, listOpts...); err != nil {
		return nil, err
	}

	keys := make(map[string]string, len(machineList.Items))
	for i := range machineList.Items {
		m := &machineList.Items[i]
		if name := m.Spec.InfrastructureRef.Name; name != "" {
			keys[name] = metroPlacementGroupKey(m.Labels)
		}
	}
	return keys, nil
}

// storeMetroPlacementSelection records the selected preferred failureDomain and its PE in the
// reconcile Datastore and on the NutanixMachine labels for Metro/MetroSite VM placement.
func (r *NutanixMachineReconciler) storeMetroPlacementSelection(rctx *nctx.MachineContext, selectedFd *infrav1.NutanixFailureDomain) {
	if rctx.Datastore == nil {
		rctx.Datastore = map[string]*string{}
	}
	rctx.Datastore[nctx.MetroPreferredFailureDomainName] = ptr.To(selectedFd.Name)
	rctx.Datastore[nctx.MetroPreferredPE] = ptr.To(selectedFd.Spec.PrismElementCluster.String())

	if rctx.NutanixMachine.Labels == nil {
		rctx.NutanixMachine.Labels = map[string]string{}
	}
	rctx.NutanixMachine.Labels[metroNativeFailureDomainLabelKey] = selectedFd.Name
	rctx.NutanixMachine.Labels[metroNativePELabelKey] = selectedFd.Spec.PrismElementCluster.String()
}

func (r *NutanixMachineReconciler) validateMachineConfig(rctx *nctx.MachineContext, effectiveProject *nctx.ProjectInfo, resourceGroup *multidomainModels.ResourceGroup) error {
	log := ctrl.LoggerFrom(rctx.Context)
	fdName := rctx.Machine.Spec.FailureDomain
	if fdName != "" {
		log.WithValues("failureDomain", fdName)
		fdSpec, err := r.getFailureDomainSpec(rctx, fdName)
		if err != nil {
			log.Error(err, fmt.Sprintf("Failed to get the failure domain %s", fdName))
			return err
		}
		if err := r.validateFailureDomainSpec(rctx, fdSpec, effectiveProject, resourceGroup); err != nil {
			log.Error(err, fmt.Sprintf("Failed to validate the failure domain %v", fdSpec))
			return err
		}
		// Update the NutanixMachine machine config based on the failure domain spec
		rctx.NutanixMachine.Spec.Cluster = fdSpec.PrismElementCluster
		rctx.NutanixMachine.Spec.Subnets = fdSpec.Subnets
		rctx.NutanixMachine.Status.FailureDomain = &fdName
		log.Info(fmt.Sprintf("Updated the NutanixMachine %s machine config from the failure domain %s configuration.", rctx.NutanixMachine.Name, fdName))
	}

	if len(rctx.NutanixMachine.Spec.Subnets) == 0 {
		return fmt.Errorf("at least one subnet is needed to create the VM %s", rctx.NutanixMachine.Name)
	}
	if (rctx.NutanixMachine.Spec.Cluster.Name == nil || *rctx.NutanixMachine.Spec.Cluster.Name == "") &&
		(rctx.NutanixMachine.Spec.Cluster.UUID == nil || *rctx.NutanixMachine.Spec.Cluster.UUID == "") {
		return fmt.Errorf("cluster name or uuid are required to create the VM %s", rctx.NutanixMachine.Name)
	}

	diskSize := rctx.NutanixMachine.Spec.SystemDiskSize
	// Validate disk size
	if diskSize.Cmp(minMachineSystemDiskSize) < 0 {
		diskSizeMib := GetMibValueOfQuantity(diskSize)
		minMachineSystemDiskSizeMib := GetMibValueOfQuantity(minMachineSystemDiskSize)
		return fmt.Errorf("minimum systemDiskSize is %vMib but given %vMib", minMachineSystemDiskSizeMib, diskSizeMib)
	}

	// Only validate CPU and memory if VMProfile is not set
	if rctx.NutanixMachine.Spec.VMProfile == nil {
		memorySize := rctx.NutanixMachine.Spec.MemorySize
		// Validate memory size
		if memorySize.Cmp(minMachineMemorySize) < 0 {
			memorySizeMib := GetMibValueOfQuantity(memorySize)
			minMachineMemorySizeMib := GetMibValueOfQuantity(minMachineMemorySize)
			return fmt.Errorf("minimum memorySize is %vMib but given %vMib", minMachineMemorySizeMib, memorySizeMib)
		}

		vcpusPerSocket := rctx.NutanixMachine.Spec.VCPUsPerSocket
		if vcpusPerSocket < int32(minVCPUsPerSocket) {
			return fmt.Errorf("minimum vcpus per socket is %v but given %v", minVCPUsPerSocket, vcpusPerSocket)
		}

		vcpuSockets := rctx.NutanixMachine.Spec.VCPUSockets
		if vcpuSockets < int32(minVCPUSockets) {
			return fmt.Errorf("minimum vcpu sockets is %v but given %v", minVCPUSockets, vcpuSockets)
		}
	}

	dataDisks := rctx.NutanixMachine.Spec.DataDisks
	if dataDisks != nil {
		if err := r.validateDataDisks(dataDisks); err != nil {
			return err
		}
	}

	return nil
}

func (r *NutanixMachineReconciler) validateDataDisks(dataDisks []infrav1.NutanixMachineVMDisk) error {
	errors := []error{}
	usedDeviceIndexByAdapter := make(map[string]map[int32]struct{})
	for _, disk := range dataDisks {

		if disk.DiskSize.Cmp(minMachineDataDiskSize) < 0 {
			diskSizeMib := GetMibValueOfQuantity(disk.DiskSize)
			minMachineDataDiskSizeMib := GetMibValueOfQuantity(minMachineDataDiskSize)
			errors = append(errors, fmt.Errorf("minimum data disk size is %vMib but given %vMib", minMachineDataDiskSizeMib, diskSizeMib))
		}

		if disk.DeviceProperties != nil {
			errors = validateDataDiskDeviceProperties(disk, errors)

			// DeviceIndex 0 means "unspecified" and is auto-assigned later, so we
			// only detect duplicates for explicitly set non-zero indexes.
			if disk.DeviceProperties.DeviceIndex != 0 {
				adapterType := string(disk.DeviceProperties.AdapterType)
				if _, ok := usedDeviceIndexByAdapter[adapterType]; !ok {
					usedDeviceIndexByAdapter[adapterType] = make(map[int32]struct{})
				}
				if _, ok := usedDeviceIndexByAdapter[adapterType][disk.DeviceProperties.DeviceIndex]; ok {
					errors = append(errors, fmt.Errorf("index '%d' is already in use", disk.DeviceProperties.DeviceIndex))
				} else {
					usedDeviceIndexByAdapter[adapterType][disk.DeviceProperties.DeviceIndex] = struct{}{}
				}
			}
		}

		if disk.DataSource != nil {
			errors = validateDataDiskDataSource(disk, errors)
		}

		if disk.StorageConfig != nil {
			errors = validateDataDiskStorageConfig(disk, errors)
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("data disks validation errors: %v", errors)
	}

	return nil
}

func validateDataDiskStorageConfig(disk infrav1.NutanixMachineVMDisk, errors []error) []error {
	if disk.StorageConfig.StorageContainer != nil && disk.StorageConfig.StorageContainer.IsUUID() {
		if disk.StorageConfig.StorageContainer.UUID == nil {
			errors = append(errors, fmt.Errorf("name or uuid is required for storage container in data disk"))
		} else {
			if _, err := uuid.Parse(*disk.StorageConfig.StorageContainer.UUID); err != nil {
				errors = append(errors, fmt.Errorf("invalid UUID for storage container in data disk: %v", err))
			}
		}
	}

	if disk.StorageConfig.StorageContainer != nil &&
		disk.StorageConfig.StorageContainer.IsName() &&
		disk.StorageConfig.StorageContainer.Name == nil {
		errors = append(errors, fmt.Errorf("name or uuid is required for storage container in data disk"))
	}

	if disk.StorageConfig.DiskMode != infrav1.NutanixMachineDiskModeFlash && disk.StorageConfig.DiskMode != infrav1.NutanixMachineDiskModeStandard {
		errors = append(errors, fmt.Errorf("invalid disk mode %s for data disk", disk.StorageConfig.DiskMode))
	}
	return errors
}

func validateDataDiskDataSource(disk infrav1.NutanixMachineVMDisk, errors []error) []error {
	if disk.DataSource.Type == infrav1.NutanixIdentifierUUID && disk.DataSource.UUID == nil {
		errors = append(errors, fmt.Errorf("UUID is required for data disk with UUID source"))
	}

	if disk.DataSource.Type == infrav1.NutanixIdentifierName && disk.DataSource.Name == nil {
		errors = append(errors, fmt.Errorf("name is required for data disk with name source"))
	}
	return errors
}

func validateDataDiskDeviceProperties(disk infrav1.NutanixMachineVMDisk, errors []error) []error {
	validAdapterTypes := map[infrav1.NutanixMachineDiskAdapterType]bool{
		infrav1.NutanixMachineDiskAdapterTypeIDE:   false,
		infrav1.NutanixMachineDiskAdapterTypeSCSI:  false,
		infrav1.NutanixMachineDiskAdapterTypeSATA:  false,
		infrav1.NutanixMachineDiskAdapterTypePCI:   false,
		infrav1.NutanixMachineDiskAdapterTypeSPAPR: false,
	}

	switch disk.DeviceProperties.DeviceType {
	case infrav1.NutanixMachineDiskDeviceTypeDisk:
		validAdapterTypes[infrav1.NutanixMachineDiskAdapterTypeSCSI] = true
		validAdapterTypes[infrav1.NutanixMachineDiskAdapterTypePCI] = true
		validAdapterTypes[infrav1.NutanixMachineDiskAdapterTypeSPAPR] = true
		validAdapterTypes[infrav1.NutanixMachineDiskAdapterTypeSATA] = true
		validAdapterTypes[infrav1.NutanixMachineDiskAdapterTypeIDE] = true
	case infrav1.NutanixMachineDiskDeviceTypeCDRom:
		validAdapterTypes[infrav1.NutanixMachineDiskAdapterTypeIDE] = true
		validAdapterTypes[infrav1.NutanixMachineDiskAdapterTypePCI] = true
	default:
		errors = append(errors, fmt.Errorf("invalid device type %s for data disk", disk.DeviceProperties.DeviceType))
	}

	if !validAdapterTypes[disk.DeviceProperties.AdapterType] {
		errors = append(errors, fmt.Errorf("invalid adapter type %s for data disk", disk.DeviceProperties.AdapterType))
	}

	if disk.DeviceProperties.DeviceIndex < 0 {
		errors = append(errors, fmt.Errorf("invalid device index %d for data disk", disk.DeviceProperties.DeviceIndex))
	}
	return errors
}

// setMetroCustomAttributes sets the metro placement customAttributes on the VM
// for Metro/MetroSite failure domains.
func setMetroCustomAttributes(rctx *nctx.MachineContext, vm *vmmconfig.Vm) {
	if isNutanixMetroFailureDomain(rctx.Machine.Spec.FailureDomain) || isNutanixMetroSiteFailureDomain(rctx.Machine.Spec.FailureDomain) {
		if preferredPE := rctx.Datastore[nctx.MetroPreferredPE]; preferredPE != nil {
			vm.CustomAttributes = []string{
				vmCustomAttributePrefix4MetroPreferredPE + *preferredPE,
			}
		}
	}
	if isNutanixMetroSiteFailureDomain(rctx.Machine.Spec.FailureDomain) {
		if groupNameLabel := rctx.Datastore[nctx.MetroNodeGroupNameLabel]; groupNameLabel != nil {
			vm.CustomAttributes = append(vm.CustomAttributes, vmCustomAttributePrefix4MetroNodeGroupNameLabel+*groupNameLabel)
		}
	}
}

// getOrMintVMCreationRequestID returns the idempotency key to use for the VM Create call.
// If the NutanixMachine doesn't already have one recorded, a new UUID is minted and durably
// persisted as an annotation before this returns, so every later reconcile of this object -
// including one that races immediately behind this one, or one that resumes after a
// controller restart - reuses the exact same key. Reusing it turns a retried Create into a
// no-op that returns the original task's result instead of creating a second VM.
//
// It is stored as an annotation rather than in status because clusterctl move drops status
// on Create for objects with the status subresource enabled, while metadata (including
// annotations) passes through unchanged - see the Spec.ProviderID fallback in GetVMUUID for
// the same reasoning applied to the VM's identity itself.
func (r *NutanixMachineReconciler) getOrMintVMCreationRequestID(rctx *nctx.MachineContext) (string, error) {
	if requestID := rctx.NutanixMachine.Annotations[VMCreationRequestIDAnnotation]; requestID != "" {
		return requestID, nil
	}

	// Snapshot the object *before* mutating it: patchMachine builds its diff baseline from
	// rctx.NutanixMachine at the time it's called, so if we mutated it first, the baseline
	// would already contain the new annotation and the resulting patch would be a no-op -
	// silently defeating the "persist before anything else" guarantee this function exists
	// to provide.
	before := rctx.NutanixMachine.DeepCopy()

	requestID := uuid.NewString()
	if rctx.NutanixMachine.Annotations == nil {
		rctx.NutanixMachine.Annotations = map[string]string{}
	}
	rctx.NutanixMachine.Annotations[VMCreationRequestIDAnnotation] = requestID

	if err := r.Patch(rctx.Context, rctx.NutanixMachine, client.MergeFrom(before)); err != nil {
		return "", fmt.Errorf("failed to persist vm creation request id: %w", err)
	}

	return requestID, nil
}

// GetOrCreateVM creates a VM and is invoked by the NutanixMachineReconciler
//
//nolint:gocognit // VM creation has multiple provider-specific setup steps.
func (r *NutanixMachineReconciler) getOrCreateVM(rctx *nctx.MachineContext, effectiveProject *nctx.ProjectInfo, resourceGroup *multidomainModels.ResourceGroup) (*vmmconfig.Vm, error) {
	var err error
	ctx := rctx.Context
	log := ctrl.LoggerFrom(ctx)
	vmName := rctx.Machine.Name
	convergedClient := rctx.ConvergedClient

	var effectiveProjectExtID *string
	if effectiveProject != nil {
		effectiveProjectExtID = effectiveProject.ExtID
	}

	// Check if the VM already exists
	vmFound, err := FindVM(ctx, convergedClient, rctx.Machine, rctx.NutanixMachine, vmName, effectiveProjectExtID, rctx.PCVersion)
	if err != nil {
		log.Error(err, fmt.Sprintf("error occurred finding VM %s by name or uuid", vmName))
		return nil, err
	}

	// if VM exists
	if vmFound != nil {
		log.Info(fmt.Sprintf("vm %s found with UUID %s", *vmFound.Name, rctx.NutanixMachine.Status.VmUUID))

		v1beta1conditions.MarkTrue(rctx.NutanixMachine, infrav1.VMProvisionedCondition)
		v1beta2conditions.Set(rctx.NutanixMachine, metav1.Condition{
			Type:   string(infrav1.VMProvisionedCondition),
			Status: metav1.ConditionTrue,
			Reason: capiv1beta1.ProvisionedV1Beta2Reason,
		})
		return vmFound, nil
	}

	log.Info(fmt.Sprintf("No existing VM found. Starting creation process of VM %s.", vmName))

	// Mint (or recall) the idempotency key before doing anything else, so it is durably
	// persisted ahead of the actual Create call below. See getOrMintVMCreationRequestID.
	requestID, err := r.getOrMintVMCreationRequestID(rctx)
	if err != nil {
		return nil, err
	}

	err = r.validateMachineConfig(rctx, effectiveProject, resourceGroup)
	if err != nil {
		rctx.SetFailureStatus(createErrorFailureReason, err)
		return nil, err
	}

	peUUID, subnetUUIDs, err := r.GetSubnetAndPEUUIDs(rctx, effectiveProject, resourceGroup)
	if err != nil {
		log.Error(err, fmt.Sprintf("failed to get the config for VM %s.", vmName))
		rctx.SetFailureStatus(createErrorFailureReason, err)
		return nil, err
	}

	// Check if VMProfile is set
	if rctx.NutanixMachine.Spec.VMProfile != nil {
		return r.deployVMFromProfile(rctx, vmName, peUUID, subnetUUIDs, effectiveProject)
	}

	// Traditional VM creation path (without VMProfile)
	vm := &vmmconfig.Vm{
		Name:                  &vmName,
		MemorySizeBytes:       ptr.To(rctx.NutanixMachine.Spec.MemorySize.Value()),
		NumCoresPerSocket:     ptr.To(int(rctx.NutanixMachine.Spec.VCPUsPerSocket)),
		NumSockets:            ptr.To(int(rctx.NutanixMachine.Spec.VCPUSockets)),
		HardwareClockTimezone: ptr.To("UTC"),
	}

	// Set the metro placement customAttributes on the VM for Metro/MetroSite failure domains.
	setMetroCustomAttributes(rctx, vm)

	// Set cluster reference
	vm.Cluster = vmmconfig.NewClusterReference()
	vm.Cluster.ExtId = &peUUID

	// Set Nics
	nics := make([]vmmconfig.Nic, len(subnetUUIDs))
	for idx, subnetUUID := range subnetUUIDs {
		vmNic := vmmconfig.NewNic()
		vmNic.NetworkInfo = vmmconfig.NewNicNetworkInfo()
		vmNic.NetworkInfo.Subnet = vmmconfig.NewSubnetReference()
		vmNic.NetworkInfo.Subnet.ExtId = &subnetUUID
		nics[idx] = *vmNic
	}
	vm.Nics = nics

	// Project-scoped categories only exist on PC 7.6+. Pass a nil project on older PC
	// versions so the category layer uses the non-project lookup, even when an explicit
	// (v3-resolved) project ext ID is present.
	var categoryProjectExtID *string
	if isPCVersionHigherThan75(rctx.PCVersion) {
		categoryProjectExtID = effectiveProjectExtID
	}

	defaultCategoryIdentifiers := GetDefaultCAPICategoryIdentifiers(rctx.Cluster.Name)
	if _, err := GetOrCreateCategoriesForProject(ctx, rctx.ConvergedClient, defaultCategoryIdentifiers, categoryProjectExtID); err != nil {
		errorMsg := fmt.Errorf("error occurred while creating category spec for vm %s: %w", vmName, err)
		if !isRetryableAPIError(err) {
			rctx.SetFailureStatus(createErrorFailureReason, errorMsg)
		}
		if cerr := r.markClusterCategoryCreationFailed(rctx, errorMsg); cerr != nil {
			log.Error(cerr, "failed to mark ClusterCategoryCreatedCondition=False on NutanixCluster; continuing")
		}
		return nil, errorMsg
	}
	if err := r.markClusterCategoryCreated(rctx); err != nil {
		log.Error(err, "failed to mark ClusterCategoryCreatedCondition on NutanixCluster; continuing")
	}

	// Set categories on VM
	categoryIdentifiers, err := r.getMachineCategoryIdentifiers(rctx)
	if err != nil {
		errorMsg := fmt.Errorf("error occurred while getting category identifiers for vm %s: %w", vmName, err)
		if !isRetryableAPIError(err) {
			rctx.SetFailureStatus(createErrorFailureReason, errorMsg)
		}
		return nil, errorMsg
	}

	categoryReferences, err := GetPrismReferencesOfCategoryIdentifiersForProject(
		ctx,
		rctx.ConvergedClient,
		categoryIdentifiers,
		categoryProjectExtID,
	)
	if err != nil {
		errorMsg := fmt.Errorf("error occurred while creating category spec for vm %s: %w", vmName, err)
		if !isRetryableAPIError(err) {
			rctx.SetFailureStatus(createErrorFailureReason, errorMsg)
		}
		return nil, errorMsg
	}
	vm.Categories = categoryReferences

	// Set Project in VM Spec before creating VM
	err = r.addVMToProject(rctx, vm, effectiveProjectExtID)
	if err != nil {
		errorMsg := fmt.Errorf("error occurred while trying to add VM %s to project: %w", vmName, err)
		if !isRetryableAPIError(err) {
			rctx.SetFailureStatus(createErrorFailureReason, errorMsg)
		}
		return nil, errorMsg
	}

	// Get GPU list
	gpus, err := GetGPUList(ctx, convergedClient, rctx.NutanixMachine.Spec.GPUs, peUUID, rctx.PCVersion)
	if err != nil {
		errorMsg := fmt.Errorf("failed to get the GPU list to create the VM %s: %w", vmName, err)
		if !isRetryableAPIError(err) {
			rctx.SetFailureStatus(createErrorFailureReason, errorMsg)
		}
		return nil, errorMsg
	}
	vm.Gpus = gpus

	disks, cdRoms, err := getDiskList(rctx, peUUID, effectiveProject, resourceGroup)
	if err != nil {
		errorMsg := fmt.Errorf("failed to get the disk list to create the VM %s: %w", vmName, err)
		if !isRetryableAPIError(err) {
			rctx.SetFailureStatus(createErrorFailureReason, errorMsg)
		}
		return nil, errorMsg
	}
	vm.Disks = disks
	vm.CdRoms = cdRoms

	if err := r.addGuestCustomizationToVM(rctx, vm); err != nil {
		errorMsg := fmt.Errorf("error occurred while adding guest customization to vm spec: %w", err)
		rctx.SetFailureStatus(createErrorFailureReason, errorMsg)
		return nil, errorMsg
	}

	// Set BootType in VM Spec before creating VM
	err = r.addBootTypeToVM(rctx, vm)
	if err != nil {
		errorMsg := fmt.Errorf("error occurred while adding boot type to vm spec: %w", err)
		rctx.SetFailureStatus(createErrorFailureReason, errorMsg)
		return nil, errorMsg
	}

	// Create the actual VM/Machine
	log.Info(fmt.Sprintf("Creating VM with name %s for cluster %s", vmName, rctx.NutanixCluster.Name))
	vm, err = convergedClient.VMs.Create(v4Converged.WithRequestID(ctx, requestID), vm)
	if err != nil {
		errorMsg := fmt.Errorf("failed to create VM %s: %w", vmName, err)
		if !isRetryableAPIError(err) {
			rctx.SetFailureStatus(createErrorFailureReason, errorMsg)
		}
		return nil, errorMsg
	}

	vmUuid := *vm.ExtId
	powerState := "UNKNOWN"
	if vm.PowerState != nil {
		powerState = vm.PowerState.GetName()
	}
	log.V(1).Info(fmt.Sprintf("Created VM %s. Got the vm UUID: %s, power state: %s", vmName, vmUuid, powerState))

	// set the VM UUID on the nutanix machine as soon as it is available. VM UUID can be used for cleanup in case of failure
	before := rctx.NutanixMachine.DeepCopy()
	rctx.NutanixMachine.Spec.ProviderID = GenerateProviderID(vmUuid)
	rctx.NutanixMachine.Status.VmUUID = vmUuid

	err = r.patchMachine(rctx, before)
	if err != nil {
		log.Error(err, "failed to patch NutanixMachine after setting VmUUID")
		return nil, err
	}

	v1beta1conditions.MarkTrue(rctx.NutanixMachine, infrav1.VMProvisionedCondition)
	v1beta2conditions.Set(rctx.NutanixMachine, metav1.Condition{
		Type:   string(infrav1.VMProvisionedCondition),
		Status: metav1.ConditionTrue,
		Reason: capiv1beta1.ProvisionedV1Beta2Reason,
	})
	return vm, nil
}

// deployVMFromProfile deploys a VM from a VM profile
func (r *NutanixMachineReconciler) deployVMFromProfile(rctx *nctx.MachineContext, vmName string, peUUID string, subnetUUIDs []string, effectiveProject *nctx.ProjectInfo) (*vmmconfig.Vm, error) {
	ctx := rctx.Context
	log := ctrl.LoggerFrom(ctx)
	convergedClient := rctx.ConvergedClient

	vmProfile, vmProfileUUID, err := r.getVMProfileForDeploy(rctx, vmName, effectiveProject)
	if err != nil {
		return nil, err
	}

	deployParams, err := r.buildDeployParamsFromProfile(rctx, vmName, peUUID, subnetUUIDs, vmProfile, effectiveProject)
	if err != nil {
		return nil, err
	}

	deployParamsJSON, err := json.MarshalIndent(deployParams, "", "  ")
	if err != nil {
		errorMsg := fmt.Errorf("failed to marshal deploy params: %w", err)
		rctx.SetFailureStatus(createErrorFailureReason, errorMsg)
		return nil, err
	}
	// Deploy VM from profile using DeployVmWithVmProfile
	log.Info(fmt.Sprintf("Deploying VM with name %s from profile %s for cluster %s: %s", vmName, vmProfileUUID, rctx.NutanixCluster.Name, deployParamsJSON))
	vmOp, err := convergedClient.VMProfiles.DeployVmWithVmProfile(ctx, vmProfileUUID, deployParams)
	if err != nil {
		errorMsg := fmt.Errorf("failed to deploy VM %s from profile %s. error: %w", vmName, vmProfileUUID, err)
		if !isRetryableAPIError(err) {
			rctx.SetFailureStatus(createErrorFailureReason, errorMsg)
		}
		return nil, errorMsg
	}

	// Wait for the operation to complete
	vmCreatedList, err := vmOp.Wait(ctx)
	if err != nil {
		errorMsg := fmt.Errorf("failed to wait for VM %s deployment from profile %s. error: %w", vmName, vmProfileUUID, err)
		if !isRetryableAPIError(err) {
			rctx.SetFailureStatus(createErrorFailureReason, errorMsg)
		}
		return nil, errorMsg
	}
	if len(vmCreatedList) == 0 {
		errorMsg := fmt.Errorf("no VM returned from deployment operation for VM %s from profile %s", vmName, vmProfileUUID)
		rctx.SetFailureStatus(createErrorFailureReason, errorMsg)
		return nil, errorMsg
	}
	vm := vmCreatedList[0]

	vmUuid := *vm.ExtId
	powerState := "UNKNOWN"
	if vm.PowerState != nil {
		powerState = vm.PowerState.GetName()
	}
	log.V(1).Info(fmt.Sprintf("Deployed VM %s from profile. Got the vm UUID: %s, power state: %s", vmName, vmUuid, powerState))

	// set the VM UUID on the nutanix machine as soon as it is available. VM UUID can be used for cleanup in case of failure
	rctx.NutanixMachine.Spec.ProviderID = GenerateProviderID(vmUuid)
	rctx.NutanixMachine.Status.VmUUID = vmUuid

	v1beta1conditions.MarkTrue(rctx.NutanixMachine, infrav1.VMProvisionedCondition)
	v1beta2conditions.Set(rctx.NutanixMachine, metav1.Condition{
		Type:   string(infrav1.VMProvisionedCondition),
		Status: metav1.ConditionTrue,
		Reason: capiv1beta1.ProvisionedV1Beta2Reason,
	})
	return vm, nil
}

// addCustomAttributes sets custom attributes on the VM, including the provider ID.
// It is a no-op if the desired attributes are already present on the VM.
func (r *NutanixMachineReconciler) addCustomAttributes(rctx *nctx.MachineContext, vm *vmmconfig.Vm) error {
	ctx := rctx.Context
	log := ctrl.LoggerFrom(ctx)
	convergedClient := rctx.ConvergedClient

	vmName := *vm.Name

	if slices.ContainsFunc(vm.CustomAttributes, func(attr string) bool {
		return strings.HasPrefix(attr, vmCustomAttributePrefix4ProviderID)
	}) {
		log.V(1).Info(fmt.Sprintf("Custom attributes already present on VM %s, skipping update", vmName))
		return nil
	}

	vmUUID := *vm.ExtId
	desiredAttr := vmCustomAttributePrefix4ProviderID + vmUUID

	log.V(1).Info(fmt.Sprintf("Updating custom attributes on VM %s: %v", vmName, []string{desiredAttr}))
	_, err := convergedClient.VMs.AddVmCustomAttributes(ctx, vmUUID, []string{desiredAttr})
	if err != nil {
		errMsg := fmt.Errorf("failed to update custom attributes on VM %s with UUID %s: %w", vmName, vmUUID, err)
		if !isRetryableAPIError(err) {
			rctx.SetFailureStatus(createErrorFailureReason, errMsg)
		}
		return errMsg
	}
	return nil
}

func (r *NutanixMachineReconciler) getVMProfileForDeploy(rctx *nctx.MachineContext, vmName string, effectiveProject *nctx.ProjectInfo) (*vmmconfig.VmProfile, string, error) {
	ctx := rctx.Context
	log := ctrl.LoggerFrom(ctx)
	convergedClient := rctx.ConvergedClient

	log.Info(fmt.Sprintf("Validating VM profile for VM %s", vmName))
	vmProfile, err := GetVMProfile(ctx, convergedClient, *rctx.NutanixMachine.Spec.VMProfile, effectiveProject)
	if err != nil {
		errorMsg := fmt.Errorf("failed to validate VM profile for VM %s: %w", vmName, err)
		if !isRetryableAPIError(err) {
			rctx.SetFailureStatus(createErrorFailureReason, errorMsg)
		}
		return nil, "", errorMsg
	}
	if vmProfile.ExtId == nil {
		errorMsg := fmt.Errorf("VM profile has no UUID")
		rctx.SetFailureStatus(createErrorFailureReason, errorMsg)
		return nil, "", errorMsg
	}
	vmProfileUUID := *vmProfile.ExtId
	log.Info(fmt.Sprintf("VM profile validated with UUID %s", vmProfileUUID))
	return vmProfile, vmProfileUUID, nil
}

func (r *NutanixMachineReconciler) buildDeployParamsFromProfile(
	rctx *nctx.MachineContext,
	vmName string,
	peUUID string,
	subnetUUIDs []string,
	vmProfile *vmmconfig.VmProfile,
	effectiveProject *nctx.ProjectInfo,
) (*vmmconfig.DeployVmFromVmProfileParams, error) {
	ctx := rctx.Context
	convergedClient := rctx.ConvergedClient

	// Create DeployVmFromVmProfileParams with only configurable fields
	// Note: CPU, memory, bootType, and GPUs come from the VM profile, so they're not in params
	deployParams := vmmconfig.NewDeployVmFromVmProfileParams()
	deployParams.VmName = &vmName
	deployParams.Cluster = vmmconfig.NewClusterReference()
	deployParams.Cluster.ExtId = &peUUID

	// Project-scoped categories only exist on PC 7.6+. Pass a nil project on older PC
	// versions so the category layer uses the non-project lookup, even when an explicit
	// (v3-resolved) project ext ID is present.
	var categoryProjectExtID *string
	if effectiveProject != nil {
		deployParams.ProjectExtId = effectiveProject.ExtID
		if isPCVersionHigherThan75(rctx.PCVersion) {
			categoryProjectExtID = effectiveProject.ExtID
		}
		v1beta1conditions.MarkTrue(rctx.NutanixMachine, infrav1.ProjectAssignedCondition)
		v1beta2conditions.Set(rctx.NutanixMachine, metav1.Condition{
			Type:   string(infrav1.ProjectAssignedCondition),
			Status: metav1.ConditionTrue,
			Reason: infrav1.Succeeded,
		})
	}
	defaultCategoryIdentifiers := GetDefaultCAPICategoryIdentifiers(rctx.Cluster.Name)
	if _, err := GetOrCreateCategoriesForProject(ctx, convergedClient, defaultCategoryIdentifiers, categoryProjectExtID); err != nil {
		errorMsg := fmt.Errorf("error occurred while creating category spec for vm %s: %w", vmName, err)
		if !isRetryableAPIError(err) {
			rctx.SetFailureStatus(createErrorFailureReason, errorMsg)
		}
		if cerr := r.markClusterCategoryCreationFailed(rctx, errorMsg); cerr != nil {
			ctrl.LoggerFrom(ctx).Error(cerr, "failed to mark ClusterCategoryCreatedCondition=False on NutanixCluster; continuing")
		}
		return nil, errorMsg
	}
	if err := r.markClusterCategoryCreated(rctx); err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "failed to mark ClusterCategoryCreatedCondition on NutanixCluster; continuing")
	}

	nics, err := r.buildDeployNicsFromProfile(rctx, vmName, subnetUUIDs, vmProfile)
	if err != nil {
		return nil, err
	}
	deployParams.Nics = nics

	categoryIdentifiers, err := r.getMachineCategoryIdentifiers(rctx)
	if err != nil {
		errorMsg := fmt.Errorf("error occurred while getting category identifiers for vm %s: %w", vmName, err)
		if !isRetryableAPIError(err) {
			rctx.SetFailureStatus(createErrorFailureReason, errorMsg)
		}
		return nil, errorMsg
	}

	categoryReferences, err := GetPrismReferencesOfCategoryIdentifiersForProject(
		ctx,
		convergedClient,
		categoryIdentifiers,
		categoryProjectExtID,
	)
	if err != nil {
		errorMsg := fmt.Errorf("error occurred while creating category spec for vm %s: %w", vmName, err)
		if !isRetryableAPIError(err) {
			rctx.SetFailureStatus(createErrorFailureReason, errorMsg)
		}
		return nil, errorMsg
	}
	deployParams.Categories = make([]vmmconfig.CategoryReference, len(categoryReferences))
	for i, cat := range categoryReferences {
		deployParams.Categories[i] = vmmconfig.CategoryReference{ExtId: cat.ExtId}
	}

	// Note: GPUs come from the VM profile, so they're not in deploy params

	deployDisks, err := getVmProfileDeployDisks(rctx, effectiveProject)
	if err != nil {
		errorMsg := fmt.Errorf("failed to get the disk list to create the VM %s: %w", vmName, err)
		if !isRetryableAPIError(err) {
			rctx.SetFailureStatus(createErrorFailureReason, errorMsg)
		}
		return nil, err
	}
	deployParams.Disks = deployDisks

	if err := r.addGuestCustomizationToDeployParams(rctx, deployParams); err != nil {
		errorMsg := fmt.Errorf("error occurred while adding guest customization to vm spec: %w", err)
		rctx.SetFailureStatus(createErrorFailureReason, errorMsg)
		return nil, err
	}

	return deployParams, nil
}

func (r *NutanixMachineReconciler) buildDeployNicsFromProfile(
	rctx *nctx.MachineContext,
	vmName string,
	subnetUUIDs []string,
	vmProfile *vmmconfig.VmProfile,
) ([]vmmconfig.VmProfileDeployVmNic, error) {
	log := ctrl.LoggerFrom(rctx.Context)

	if vmProfile.VmConfiguration == nil || len(vmProfile.VmConfiguration.Nics) == 0 {
		// TODO: NICs can still be added after the VM is created
		log.Info("vmProfile has no NICs defined; cannot map vmProfileNicExtId")
		return nil, nil
	}
	if len(subnetUUIDs) > len(vmProfile.VmConfiguration.Nics) {
		log.Info(fmt.Sprintf("vmProfile has %d NICs but %d subnets provided; cannot map vmProfileNicExtId", len(vmProfile.VmConfiguration.Nics), len(subnetUUIDs)))
		err := fmt.Errorf("vmProfile has %d NICs but %d subnets provided; cannot map vmProfileNicExtId", len(vmProfile.VmConfiguration.Nics), len(subnetUUIDs))
		rctx.SetFailureStatus(createErrorFailureReason, err)
		return nil, err
	}

	nics := make([]vmmconfig.VmProfileDeployVmNic, len(subnetUUIDs))
	for idx, subnetUUID := range subnetUUIDs {
		profileNic := vmProfile.VmConfiguration.Nics[idx]
		if profileNic.ExtId == nil || *profileNic.ExtId == "" {
			err := fmt.Errorf("vmProfile NIC extId missing for nic index %d", idx)
			rctx.SetFailureStatus(createErrorFailureReason, err)
			return nil, err
		}

		vmNic := vmmconfig.NewVmProfileDeployVmNic()
		subnetRef := vmmconfig.NewSubnetReference()
		subnetRef.ExtId = &subnetUUID
		vmNic.Subnet = subnetRef
		// Carry the NIC extId from the profile so AHV accepts the deploy payload
		vmNic.VmProfileNicExtId = profileNic.ExtId

		nics[idx] = *vmNic
	}

	r.logProfileNicMapping(log, vmProfile, nics)
	return nics, nil
}

func (r *NutanixMachineReconciler) logProfileNicMapping(
	log logr.Logger,
	vmProfile *vmmconfig.VmProfile,
	nics []vmmconfig.VmProfileDeployVmNic,
) {
	profileNicExtIDs := make([]string, len(vmProfile.VmConfiguration.Nics))
	for i, nic := range vmProfile.VmConfiguration.Nics {
		if nic.ExtId != nil {
			profileNicExtIDs[i] = *nic.ExtId
		}
	}
	deployNicInfo := make([]string, len(nics))
	for i, nic := range nics {
		subnetID := ""
		if nic.Subnet != nil && nic.Subnet.ExtId != nil {
			subnetID = *nic.Subnet.ExtId
		}
		extID := ""
		if nic.VmProfileNicExtId != nil {
			extID = *nic.VmProfileNicExtId
		}
		deployNicInfo[i] = fmt.Sprintf("nic[%d]: subnet=%s, profileNicExtId=%s", i, subnetID, extID)
	}
	log.Info(fmt.Sprintf("VM profile NIC extIds: %v; deploy NIC mapping: %v", profileNicExtIDs, deployNicInfo))
}

// addGuestCustomizationToDeployParams adds guest customization to DeployVmFromVmProfileParams
func (r *NutanixMachineReconciler) addGuestCustomizationToDeployParams(rctx *nctx.MachineContext, params *vmmconfig.DeployVmFromVmProfileParams) error {
	// Get the bootstrapData
	bootstrapRef := rctx.NutanixMachine.Spec.BootstrapRef
	if bootstrapRef.Kind == infrav1.NutanixMachineBootstrapRefKindSecret {
		bootstrapData, err := r.getBootstrapData(rctx)
		if err != nil {
			return err
		}

		// TODO: Remove this once AOS 7.3 is no longer supported
		// Remove the jinja template line to fix AOS 7.3 regression where VMM service
		// incorrectly checks for #cloud-config being the prefix. The bootstrap data typically
		// starts with "## template: jinja\n#cloud-config\n" but AOS 7.3 expects #cloud-config first.
		bootstrapData = bytes.TrimPrefix(bootstrapData, []byte("## template: jinja\n"))
		// TODO: Remove this once AOS 7.3 is no longer supported
		// substitute {{ ds.meta_data.hostname }} with the machine name
		// to fix AOS 7.3 regression where VMM service
		// incorrectly checks for #cloud-config being the prefix. The bootstrap data typically
		// starts with "## template: jinja\n#cloud-config\n" but AOS 7.3 expects #cloud-config first.
		bootstrapData = bytes.ReplaceAll(bootstrapData, []byte("{{ ds.meta_data.hostname }}"), []byte(rctx.Machine.Name))
		// Encode the bootstrapData by base64
		bsdataEncoded := base64.StdEncoding.EncodeToString(bootstrapData)
		metadata := fmt.Sprintf("{\"hostname\": \"%s\", \"uuid\": \"%s\"}", rctx.Machine.Name, uuid.New())
		metadataEncoded := base64.StdEncoding.EncodeToString([]byte(metadata))

		cloudInit := vmmconfig.NewCloudInit()
		cloudInit.Metadata = ptr.To(metadataEncoded)
		cloudInit.DatasourceType = vmmconfig.CLOUDINITDATASOURCETYPE_CONFIG_DRIVE_V2.Ref()
		userData := vmmconfig.NewUserdata()
		userData.Value = ptr.To(bsdataEncoded)
		err = cloudInit.SetCloudInitScript(*userData)
		if err != nil {
			return err
		}
		cloudInit.CloudInitScriptItemDiscriminator_ = nil

		// Use VmProfileDeployVmGuestCustomizationParams for deploy params
		guestCustomization := vmmconfig.NewVmProfileDeployVmGuestCustomizationParams()
		err = guestCustomization.SetConfig(*cloudInit)
		if err != nil {
			return err
		}
		params.GuestCustomization = guestCustomization
	}
	return nil
}

// powerOnVM powers on the VM, waits for the task to complete, and returns the
// re-fetched VM. Both the "existing VM found off" and "newly created VM" paths
// use this so power-on error handling stays in one place.
func (r *NutanixMachineReconciler) powerOnVM(rctx *nctx.MachineContext, vmUUID, vmName string, effectiveProject *nctx.ProjectInfo) (*vmmconfig.Vm, error) {
	ctx := rctx.Context
	log := ctrl.LoggerFrom(ctx)
	convergedClient := rctx.ConvergedClient

	var effectiveProjectExtID *string
	if effectiveProject != nil {
		effectiveProjectExtID = effectiveProject.ExtID
	}

	log.Info(fmt.Sprintf("Powering on VM %s", vmName))
	powerOnTask, err := convergedClient.VMs.PowerOnVM(ctx, vmUUID)
	if err != nil {
		errMsg := fmt.Errorf("error occured while powering on VM %s: %w", vmName, err)
		if !isRetryableAPIError(err) {
			rctx.SetFailureStatus(powerOnErrorFailureReason, errMsg)
		}
		return nil, errMsg
	}
	_, err = powerOnTask.Wait(ctx)
	if err != nil {
		errMsg := fmt.Errorf("error occured while waiting for VM %s to power on: %w", vmName, err)
		if !isRetryableAPIError(err) {
			rctx.SetFailureStatus(powerOnErrorFailureReason, errMsg)
		}
		return nil, errMsg
	}

	log.Info(fmt.Sprintf("Fetching VM %s after power on", vmName))
	vm, err := FindVMByUUID(ctx, convergedClient, vmUUID, effectiveProjectExtID, rctx.PCVersion)
	if err != nil {
		errorMsg := fmt.Errorf("error occurred while getting VM %s after power on: %w", vmName, err)
		if !isRetryableAPIError(err) {
			rctx.SetFailureStatus(powerOnErrorFailureReason, errorMsg)
		}
		return nil, errorMsg
	}
	return vm, nil
}

func (r *NutanixMachineReconciler) addGuestCustomizationToVM(rctx *nctx.MachineContext, vm *vmmconfig.Vm) error {
	// Get the bootstrapData
	bootstrapRef := rctx.NutanixMachine.Spec.BootstrapRef
	if bootstrapRef.Kind == infrav1.NutanixMachineBootstrapRefKindSecret {
		bootstrapData, err := r.getBootstrapData(rctx)
		if err != nil {
			return err
		}

		// TODO: Remove this once AOS 7.3 is no longer supported
		// Remove the jinja template line to fix AOS 7.3 regression where VMM service
		// incorrectly checks for #cloud-config being the prefix. The bootstrap data typically
		// starts with "## template: jinja\n#cloud-config\n" but AOS 7.3 expects #cloud-config first.
		bootstrapData = bytes.TrimPrefix(bootstrapData, []byte("## template: jinja\n"))
		// TODO: Remove this once AOS 7.3 is no longer supported
		// substitute {{ ds.meta_data.hostname }} with the machine name
		// to fix AOS 7.3 regression where VMM service
		// incorrectly checks for #cloud-config being the prefix. The bootstrap data typically
		// starts with "## template: jinja\n#cloud-config\n" but AOS 7.3 expects #cloud-config first.
		bootstrapData = bytes.ReplaceAll(bootstrapData, []byte("{{ ds.meta_data.hostname }}"), []byte(rctx.Machine.Name))
		// Encode the bootstrapData by base64
		bsdataEncoded := base64.StdEncoding.EncodeToString(bootstrapData)
		metadata := fmt.Sprintf("{\"hostname\": \"%s\", \"uuid\": \"%s\"}", rctx.Machine.Name, uuid.New())
		metadataEncoded := base64.StdEncoding.EncodeToString([]byte(metadata))

		cloudInit := vmmconfig.NewCloudInit()
		cloudInit.Metadata = ptr.To(metadataEncoded)
		cloudInit.DatasourceType = vmmconfig.CLOUDINITDATASOURCETYPE_CONFIG_DRIVE_V2.Ref()
		userData := vmmconfig.NewUserdata()
		userData.Value = ptr.To(bsdataEncoded)
		err = cloudInit.SetCloudInitScript(*userData)
		if err != nil {
			return err
		}
		cloudInit.CloudInitScriptItemDiscriminator_ = nil

		vm.GuestCustomization = vmmconfig.NewGuestCustomizationParams()
		err = vm.GuestCustomization.SetConfig(*cloudInit)
		if err != nil {
			return err
		}
	}

	return nil
}

func getVmProfileDeployDisks(rctx *nctx.MachineContext, effectiveProject *nctx.ProjectInfo) ([]vmmconfig.VmProfileDeployVmDisk, error) {
	// Build a VmProfileDeployVmDisk that references the OS image via the VMProfile-specific backing info type.
	var nodeOSImage *imageModels.Image
	var err error
	if rctx.NutanixMachine.Spec.Image != nil {
		nodeOSImage, err = GetImage(
			rctx.Context,
			rctx.ConvergedClient,
			*rctx.NutanixMachine.Spec.Image,
			effectiveProject,
			rctx.PCVersion,
		)
	} else if rctx.NutanixMachine.Spec.ImageLookup != nil {
		nodeOSImage, err = GetImageByLookup(
			rctx.Context,
			rctx.ConvergedClient,
			rctx.NutanixMachine.Spec.ImageLookup.Format,
			&rctx.NutanixMachine.Spec.ImageLookup.BaseOS,
			&rctx.Machine.Spec.Version,
			effectiveProject,
			rctx.PCVersion,
		)
	} else {
		return nil, fmt.Errorf("image must be specified for VM profile deploy")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get system disk image %q: %w", rctx.NutanixMachine.Spec.Image, err)
	}
	markedForDeletion, err := ImageMarkedForDeletion(rctx.Context, rctx.ConvergedClient, nodeOSImage)
	if err != nil {
		return nil, err
	}
	if markedForDeletion {
		return nil, fmt.Errorf("system disk image %s is being deleted", *nodeOSImage.ExtId)
	}

	systemDiskSizeInBytes := rctx.NutanixMachine.Spec.SystemDiskSize.Value()

	backing := *vmmconfig.NewVmProfileDeployVmDiskBackingInfo()
	backing.DiskSizeBytes = ptr.To(systemDiskSizeInBytes)

	ds := vmmconfig.NewVmProfileDeployVmDiskDataSource()
	imgRef := *vmmconfig.NewVmProfileDeployVmDiskImageDataSourceReference()
	imgRef.ImageExtId = nodeOSImage.ExtId
	if err := ds.SetReference(imgRef); err != nil {
		return nil, err
	}
	// Clear discriminator fields to avoid schema validation errors
	ds.ReferenceItemDiscriminator_ = nil
	backing.DataSource = ds

	deployDisk := vmmconfig.NewVmProfileDeployVmDisk()
	if err := deployDisk.SetBackingInfo(backing); err != nil {
		return nil, err
	}
	deployDisk.BackingInfoItemDiscriminator_ = nil

	return []vmmconfig.VmProfileDeployVmDisk{*deployDisk}, nil
}

func getDiskList(rctx *nctx.MachineContext, peUUID string, effectiveProject *nctx.ProjectInfo, resourceGroup *multidomainModels.ResourceGroup) ([]vmmconfig.Disk, []vmmconfig.CdRom, error) {
	disks := make([]vmmconfig.Disk, 0)
	cdRoms := make([]vmmconfig.CdRom, 0)

	systemDisk, err := getSystemDisk(rctx, effectiveProject)
	if err != nil {
		return nil, nil, err
	}
	disks = append(disks, *systemDisk)

	bootstrapRef := rctx.NutanixMachine.Spec.BootstrapRef
	if bootstrapRef != nil && bootstrapRef.Kind == infrav1.NutanixMachineBootstrapRefKindImage {
		bootstrapDisk, err := getBootstrapDisk(rctx, effectiveProject)
		if err != nil {
			return nil, nil, err
		}

		cdRoms = append(cdRoms, *bootstrapDisk)
	}

	dataDisks, dataCdRoms, err := getDataDisks(rctx, peUUID, effectiveProject, resourceGroup)
	if err != nil {
		return nil, nil, err
	}
	disks = append(disks, dataDisks...)
	cdRoms = append(cdRoms, dataCdRoms...)

	return disks, cdRoms, nil
}

func getSystemDisk(rctx *nctx.MachineContext, effectiveProject *nctx.ProjectInfo) (*vmmconfig.Disk, error) {
	var nodeOSImage *imageModels.Image
	var err error
	if rctx.NutanixMachine.Spec.Image != nil {
		nodeOSImage, err = GetImage(
			rctx.Context,
			rctx.ConvergedClient,
			*rctx.NutanixMachine.Spec.Image,
			effectiveProject,
			rctx.PCVersion,
		)
	} else if rctx.NutanixMachine.Spec.ImageLookup != nil {
		nodeOSImage, err = GetImageByLookup(
			rctx.Context,
			rctx.ConvergedClient,
			rctx.NutanixMachine.Spec.ImageLookup.Format,
			&rctx.NutanixMachine.Spec.ImageLookup.BaseOS,
			&rctx.Machine.Spec.Version,
			effectiveProject,
			rctx.PCVersion,
		)
	}
	if err != nil {
		errorMsg := fmt.Errorf("failed to get system disk image %q: %w", rctx.NutanixMachine.Spec.Image, err)
		if !isRetryableAPIError(err) {
			rctx.SetFailureStatus(createErrorFailureReason, errorMsg)
		}
		return nil, errorMsg
	}
	// Consider this a precaution. If the image is marked for deletion after we
	// create the "VM create" task, then that task will fail. We will handle that
	// failure separately.
	markedForDeletion, err := ImageMarkedForDeletion(rctx.Context, rctx.ConvergedClient, nodeOSImage)
	if err != nil {
		return nil, err
	}
	if markedForDeletion {
		err := fmt.Errorf("system disk image %s is being deleted", *nodeOSImage.ExtId)
		rctx.SetFailureStatus(createErrorFailureReason, err)
		return nil, err
	}

	systemDiskSizeInBytes := rctx.NutanixMachine.Spec.SystemDiskSize.Value()
	systemDisk, err := CreateSystemDiskSpec(*nodeOSImage.ExtId, systemDiskSizeInBytes)
	if err != nil {
		errorMsg := fmt.Errorf("error occurred while creating system disk spec: %w", err)
		rctx.SetFailureStatus(createErrorFailureReason, errorMsg)
		return nil, errorMsg
	}

	return systemDisk, nil
}

func getBootstrapDisk(rctx *nctx.MachineContext, effectiveProject *nctx.ProjectInfo) (*vmmconfig.CdRom, error) {
	bootstrapImageRef := infrav1.NutanixResourceIdentifier{
		Type: infrav1.NutanixIdentifierName,
		Name: ptr.To(rctx.NutanixMachine.Spec.BootstrapRef.Name),
	}
	bootstrapImage, err := GetImage(rctx.Context, rctx.ConvergedClient, bootstrapImageRef, effectiveProject, rctx.PCVersion)
	if err != nil {
		errorMsg := fmt.Errorf("failed to get bootstrap disk image %q: %w", bootstrapImageRef, err)
		if !isRetryableAPIError(err) {
			rctx.SetFailureStatus(createErrorFailureReason, errorMsg)
		}
		return nil, errorMsg
	}
	// Consider this a precaution. If the image is marked for deletion after we
	// create the "VM create" task, then that task will fail. We will handle that
	// failure separately.
	markedForDeletion, err := ImageMarkedForDeletion(rctx.Context, rctx.ConvergedClient, bootstrapImage)
	if err != nil {
		return nil, err
	}
	if markedForDeletion {
		err := fmt.Errorf("bootstrap disk image %s is being deleted", *bootstrapImage.ExtId)
		rctx.SetFailureStatus(createErrorFailureReason, err)
		return nil, err
	}

	cdRom := vmmconfig.NewCdRom()
	cdRom.DiskAddress = vmmconfig.NewCdRomAddress()
	cdRom.DiskAddress.Index = ptr.To(0)
	cdRom.DiskAddress.BusType = vmmconfig.CDROMBUSTYPE_IDE.Ref()
	cdRom.BackingInfo = newVmDiskWithImageRef(bootstrapImage.ExtId, 0)

	return cdRom, nil
}

func getDataDisks(rctx *nctx.MachineContext, peUUID string, effectiveProject *nctx.ProjectInfo, resourceGroup *multidomainModels.ResourceGroup) ([]vmmconfig.Disk, []vmmconfig.CdRom, error) {
	dataDisks, dataCdRoms, err := CreateDataDiskList(rctx.Context, rctx.ConvergedClient, rctx.NutanixMachine.Spec.DataDisks, peUUID, effectiveProject, rctx.PCVersion, resourceGroup)
	if err != nil {
		errorMsg := fmt.Errorf("error occurred while creating data disk spec: %w", err)
		if !isRetryableAPIError(err) {
			rctx.SetFailureStatus(createErrorFailureReason, errorMsg)
		}
		return nil, nil, errorMsg
	}

	return dataDisks, dataCdRoms, nil
}

// getBootstrapData returns the Bootstrap data from the ref secret
func (r *NutanixMachineReconciler) getBootstrapData(rctx *nctx.MachineContext) ([]byte, error) {
	if rctx.NutanixMachine.Spec.BootstrapRef == nil {
		return nil, errors.New("NutanixMachine spec.BootstrapRef is nil.")
	}

	secretName := rctx.NutanixMachine.Spec.BootstrapRef.Name
	secret := &corev1.Secret{}
	secretKey := apitypes.NamespacedName{
		Namespace: rctx.NutanixMachine.Spec.BootstrapRef.Namespace,
		Name:      secretName,
	}
	if err := r.Get(rctx.Context, secretKey, secret); err != nil {
		return nil, errors.Wrapf(err, "failed to retrieve bootstrap data secret %s", secretName)
	}

	value, ok := secret.Data["value"]
	if !ok {
		return nil, errors.New("error retrieving bootstrap data: secret value key is missing")
	}

	return value, nil
}

// patchMachine persists rctx.NutanixMachine's current state, diffing it against before - a
// snapshot the caller must take with DeepCopy() *before* mutating the object. v1beta1patch.Helper
// computes its diff from whatever object it was constructed with, so building the helper from the
// already-mutated object (as opposed to a pre-mutation snapshot) makes the diff empty and the
// resulting patch a silent no-op.
func (r *NutanixMachineReconciler) patchMachine(rctx *nctx.MachineContext, before *infrav1.NutanixMachine) error {
	log := ctrl.LoggerFrom(rctx.Context)
	patchHelper, err := v1beta1patch.NewHelper(before, r.Client)
	if err != nil {
		errorMsg := fmt.Errorf("failed to create patch helper to patch machine %s: %w", rctx.NutanixMachine.Name, err)
		return errorMsg
	}
	err = patchHelper.Patch(rctx.Context, rctx.NutanixMachine)
	if err != nil {
		errorMsg := fmt.Errorf("failed to patch machine %s: %w", rctx.NutanixMachine.Name, err)
		return errorMsg
	}
	log.V(1).Info(fmt.Sprintf("Patched machine %s: Status %+v Spec %+v", rctx.NutanixMachine.Name, rctx.NutanixMachine.Status, rctx.NutanixMachine.Spec))
	return nil
}

func getIpsFromIpv4Info(config *vmmconfig.Ipv4Info) []capiv1beta1.MachineAddress {
	addresses := []capiv1beta1.MachineAddress{}
	if config == nil {
		return addresses
	}

	for _, ip := range config.LearnedIpAddresses {
		if ip.Value == nil {
			continue
		}

		addresses = append(addresses, capiv1beta1.MachineAddress{
			Type:    capiv1beta1.MachineInternalIP,
			Address: *ip.Value,
		})
	}

	return addresses
}

// getAddressesFromNic extracts IP addresses from a NIC, preferring the new
// NicNetworkInfo over the deprecated NetworkInfo. SR-IOV NICs lack IP fields
// and fall back to the deprecated NetworkInfo.
func getAddressesFromNic(nic vmmconfig.Nic) []capiv1beta1.MachineAddress {
	var ipv4Config *vmmconfig.Ipv4Config
	var ipv4Info *vmmconfig.Ipv4Info

	if nicInfo := nic.GetNicNetworkInfo(); nicInfo != nil {
		switch info := nicInfo.(type) {
		case vmmconfig.VirtualEthernetNicNetworkInfo:
			ipv4Config = info.Ipv4Config
			ipv4Info = info.Ipv4Info
		case vmmconfig.DpOffloadNicNetworkInfo:
			ipv4Config = info.Ipv4Config
			ipv4Info = info.Ipv4Info
		case vmmconfig.SriovNicNetworkInfo:
			// SR-IOV NICs only carry VlanId; fall through to deprecated NetworkInfo.
			if nic.NetworkInfo != nil {
				ipv4Config = nic.NetworkInfo.Ipv4Config
				ipv4Info = nic.NetworkInfo.Ipv4Info
			}
		}
	} else if nic.NetworkInfo != nil {
		ipv4Config = nic.NetworkInfo.Ipv4Config
		ipv4Info = nic.NetworkInfo.Ipv4Info
	}

	if ipv4Config != nil && ipv4Config.IpAddress != nil && ipv4Config.IpAddress.Value != nil {
		return []capiv1beta1.MachineAddress{{
			Type:    capiv1beta1.MachineInternalIP,
			Address: *ipv4Config.IpAddress.Value,
		}}
	}

	return getIpsFromIpv4Info(ipv4Info)
}

func (r *NutanixMachineReconciler) assignAddressesToMachine(rctx *nctx.MachineContext, vm *vmmconfig.Vm) error {
	var addresses []capiv1beta1.MachineAddress
	for _, nic := range vm.Nics {
		addresses = append(addresses, getAddressesFromNic(nic)...)
	}

	if len(addresses) == 0 {
		return fmt.Errorf("unable to determine network interfaces from VM: %s. Retrying", *vm.Name)
	}

	addresses = append(addresses, capiv1beta1.MachineAddress{
		Type:    capiv1beta1.MachineHostName,
		Address: *vm.Name,
	})

	rctx.IP = addresses[0].Address
	rctx.NutanixMachine.Status.Addresses = addresses
	return nil
}

func (r *NutanixMachineReconciler) getMachineCategoryIdentifiers(rctx *nctx.MachineContext) ([]*infrav1.NutanixCategoryIdentifier, error) {
	log := ctrl.LoggerFrom(rctx.Context)
	categoryIdentifiers := GetDefaultCAPICategoryIdentifiers(rctx.Cluster.Name)

	additionalCategories := rctx.NutanixMachine.Spec.AdditionalCategories
	if len(additionalCategories) > 0 {
		for _, at := range additionalCategories {
			additionalCat := at
			categoryIdentifiers = append(categoryIdentifiers, &additionalCat)
		}
	}

	// Add the vHADomain category if the machine is configured with a NutanixMetro/NutanixMetroSite
	// failureDomain. The vHADomain category is applied only at VM creation and is never re-synced onto
	// an already-created VM, so this must succeed before the VM is created. Returning the error (rather
	// than swallowing it) requeues the reconcile until the vHADomain and its Prism Central category are
	// ready; otherwise a VM created before the vHADomain is ready (e.g. the first control-plane node on
	// a fresh metro cluster) would be permanently left out of its metro protection policy/recovery plan.
	if isNutanixMetroFailureDomain(rctx.Machine.Spec.FailureDomain) ||
		isNutanixMetroSiteFailureDomain(rctx.Machine.Spec.FailureDomain) {
		vhaCategory, err := getVHADomainCategory(rctx, r.Client)
		if err != nil {
			return nil, err
		}
		categoryIdentifiers = append(categoryIdentifiers, vhaCategory)
		log.Info(fmt.Sprintf("Adding the vHADomain category (key: %s, value %s) to VM", vhaCategory.Key, vhaCategory.Value))
	}

	return categoryIdentifiers, nil
}

func (r *NutanixMachineReconciler) addBootTypeToVM(rctx *nctx.MachineContext, vm *vmmconfig.Vm) error {
	bootType := rctx.NutanixMachine.Spec.BootType
	// Defaults to legacy if boot type is not set.
	if bootType != "" {
		if bootType != infrav1.NutanixBootTypeLegacy && bootType != infrav1.NutanixBootTypeUEFI {
			errorMsg := fmt.Errorf("boot type must be %s or %s but was %s", string(infrav1.NutanixBootTypeLegacy), string(infrav1.NutanixBootTypeUEFI), bootType)
			v1beta1conditions.MarkFalse(rctx.NutanixMachine, infrav1.VMProvisionedCondition, infrav1.VMBootTypeInvalid, capiv1beta1.ConditionSeverityError, "%s", errorMsg.Error())
			v1beta2conditions.Set(rctx.NutanixMachine, metav1.Condition{
				Type:    string(infrav1.VMProvisionedCondition),
				Status:  metav1.ConditionFalse,
				Reason:  infrav1.VMBootTypeInvalid,
				Message: errorMsg.Error(),
			})
			return errorMsg
		}

		bootOrder := []vmmconfig.BootDeviceType{vmmconfig.BOOTDEVICETYPE_CDROM, vmmconfig.BOOTDEVICETYPE_DISK, vmmconfig.BOOTDEVICETYPE_NETWORK}

		// Only modify VM spec if boot type is UEFI. Otherwise, assume default Legacy mode
		vm.BootConfig = vmmconfig.NewOneOfVmBootConfig()
		if bootType == infrav1.NutanixBootTypeUEFI {
			uefi := vmmconfig.NewUefiBoot()
			uefi.BootOrder = bootOrder
			err := vm.BootConfig.SetValue(*uefi)
			if err != nil {
				return err
			}
		} else {
			legacy := vmmconfig.NewLegacyBoot()
			legacy.BootOrder = bootOrder
			err := vm.BootConfig.SetValue(*legacy)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

type GetProjectFunc func(rctx *nctx.MachineContext, projectRef *infrav1.NutanixResourceIdentifier) (*nctx.ProjectInfo, error)

func (r *NutanixMachineReconciler) resolveEffectiveProject(rctx *nctx.MachineContext) (*nctx.ProjectInfo, error) {
	log := ctrl.LoggerFrom(rctx.Context)
	vmName := ""
	if rctx.Machine != nil {
		vmName = rctx.Machine.Name
	}

	projectRef := rctx.NutanixMachine.Spec.Project

	if projectRef != nil {
		// PC < 7.6 doesn't expose the v4 projects API, so resolve the
		// explicitly-requested project via the v3 client instead.
		var project *nctx.ProjectInfo
		var err error
		if isPCVersionHigherThan75(rctx.PCVersion) {
			project, err = GetProjectV4(rctx, projectRef)
		} else {
			project, err = GetProjectV3(rctx, projectRef)
		}
		if err != nil {
			errorMsg := fmt.Errorf("error occurred while searching for project for VM %s: %w", vmName, err)
			log.Error(errorMsg, "error occurred while searching for project")
			r.markProjectAssignationFailed(rctx, errorMsg)
			return nil, errorMsg
		}
		return project, nil
	}

	// No project specified - PC < 7.6 doesn't support default project concept
	if !isPCVersionHigherThan75(rctx.PCVersion) {
		log.Info("PC version < 7.6, no project specified - skipping project validation")
		return nil, nil
	}

	// Use default project for PC >= 7.6
	defaultProjectUUID, err := GetDefaultProjectUUID(rctx)
	if err != nil {
		errorMsg := fmt.Errorf("error occurred while getting default project: %v", err)
		log.Error(errorMsg, "error getting default project")
		r.markProjectAssignationFailed(rctx, errorMsg)
		return nil, errorMsg
	}
	internalName := nctx.InternalProjectName
	return &nctx.ProjectInfo{
		ExtID: &defaultProjectUUID,
		Name:  &internalName,
	}, nil
}

func (r *NutanixMachineReconciler) validateProjectPolicy(
	rctx *nctx.MachineContext,
	effectiveProject *nctx.ProjectInfo,
) error {
	log := ctrl.LoggerFrom(rctx.Context)

	switch rctx.ProjectPolicy {
	case CAPXProjectPolicyUnrestricted:
		return nil

	case CAPXProjectPolicyDefaultOnly:
		defaultUUID, err := GetDefaultProjectUUID(rctx)
		if err != nil {
			errorMsg := fmt.Errorf("failed to get default project for policy validation: %w", err)
			log.Error(errorMsg, "error getting default project")
			r.markProjectAssignationFailed(rctx, errorMsg)
			return errorMsg
		}
		if effectiveProject != nil && *effectiveProject.ExtID != defaultUUID {
			errorMsg := fmt.Errorf("project policy violation: machine %s uses project %q but cluster policy requires default project %s",
				rctx.NutanixMachine.Name, *effectiveProject.Name, defaultUUID)
			log.Error(errorMsg, "project policy violation attempt")
			r.markProjectAssignationFailed(rctx, errorMsg)
			return &terminalError{message: errorMsg.Error()}
		}
		return nil

	case CAPXProjectPolicySingleProject:
		policyProjectUUID, ok := rctx.Cluster.Annotations[CAPXProjectUUIDAnnotation]
		if !ok {
			errorMsg := fmt.Errorf("single-project policy requires %s annotation on the Cluster", CAPXProjectUUIDAnnotation)
			log.Error(errorMsg, "missing project-uuid annotation for single-project policy")
			r.markProjectAssignationFailed(rctx, errorMsg)
			return &terminalError{message: errorMsg.Error()}
		}

		if effectiveProject != nil && *effectiveProject.ExtID != policyProjectUUID {
			errorMsg := fmt.Errorf("single-project policy violation: machine %s uses project %q but cluster policy requires project with uuid %q",
				rctx.NutanixMachine.Name, *effectiveProject.Name, policyProjectUUID)
			log.Error(errorMsg, "project policy violation attempt")
			r.markProjectAssignationFailed(rctx, errorMsg)
			return &terminalError{message: errorMsg.Error()}
		}
		return nil

	default:
		errorMsg := fmt.Errorf("invalid project policy %q", rctx.ProjectPolicy)
		log.Error(errorMsg, "unknown project policy")
		r.markProjectAssignationFailed(rctx, errorMsg)
		return &terminalError{message: errorMsg.Error()}
	}
}

func (r *NutanixMachineReconciler) addVMToProject(rctx *nctx.MachineContext, vm *vmmconfig.Vm, projectExtID *string) error {
	log := ctrl.LoggerFrom(rctx.Context)
	vmName := rctx.Machine.Name

	if vm == nil {
		errorMsg := fmt.Errorf("VM cannot be nil when adding VM %s to project", vmName)
		log.Error(errorMsg, "failed to add vm to project")
		r.markProjectAssignationFailed(rctx, errorMsg)
		return errorMsg
	}
	// PC < 7.6 has no project concept; skip project assignment.
	if projectExtID == nil {
		log.V(1).Info(fmt.Sprintf("No project to assign for VM %s (PC < 7.6)", vmName))
		return nil
	}

	projRef := vmmconfig.NewProjectReference()
	projRef.ExtId = projectExtID
	vm.Project = projRef

	v1beta1conditions.MarkTrue(rctx.NutanixMachine, infrav1.ProjectAssignedCondition)
	v1beta2conditions.Set(rctx.NutanixMachine, metav1.Condition{
		Type:   string(infrav1.ProjectAssignedCondition),
		Status: metav1.ConditionTrue,
		Reason: infrav1.Succeeded,
	})
	return nil
}

func (r *NutanixMachineReconciler) markProjectAssignationFailed(rctx *nctx.MachineContext, err error) {
	v1beta1conditions.MarkFalse(rctx.NutanixMachine, infrav1.ProjectAssignedCondition, infrav1.ProjectAssignationFailed, capiv1beta1.ConditionSeverityError, "%s", err.Error())
	v1beta2conditions.Set(rctx.NutanixMachine, metav1.Condition{
		Type:    string(infrav1.ProjectAssignedCondition),
		Status:  metav1.ConditionFalse,
		Reason:  infrav1.ProjectAssignationFailed,
		Message: err.Error(),
	})
}

func (r *NutanixMachineReconciler) GetSubnetAndPEUUIDs(rctx *nctx.MachineContext, effectiveProject *nctx.ProjectInfo, resourceGroup *multidomainModels.ResourceGroup) (string, []string, error) {
	if rctx == nil {
		return "", nil, fmt.Errorf("cannot create machine config if machine context is nil")
	}

	peUUID, err := GetPEUUID(rctx.Context, rctx.ConvergedClient, resourceGroup, rctx.NutanixMachine.Spec.Cluster.Name, rctx.NutanixMachine.Spec.Cluster.UUID)
	if err != nil {
		return "", nil, err
	}

	subnetUUIDs, err := GetSubnetUUIDList(rctx.Context, rctx.ConvergedClient, rctx.NutanixMachine.Spec.Subnets, peUUID, effectiveProject, rctx.PCVersion)
	if err != nil {
		return "", nil, err
	}

	return peUUID, subnetUUIDs, nil
}

// markClusterCategoryCreated sets ClusterCategoryCreatedCondition=True on the
// owning NutanixCluster after default categories are created/found.
func (r *NutanixMachineReconciler) markClusterCategoryCreated(rctx *nctx.MachineContext) error {
	if v1beta1conditions.IsTrue(rctx.NutanixCluster, infrav1.ClusterCategoryCreatedCondition) {
		return nil
	}

	patchHelper, err := v1beta1patch.NewHelper(rctx.NutanixCluster, r.Client)
	if err != nil {
		return fmt.Errorf("failed to init patch helper for NutanixCluster %s/%s: %w", rctx.NutanixCluster.Namespace, rctx.NutanixCluster.Name, err)
	}

	v1beta1conditions.MarkTrue(rctx.NutanixCluster, infrav1.ClusterCategoryCreatedCondition)
	v1beta2conditions.Set(rctx.NutanixCluster, metav1.Condition{
		Type:   string(infrav1.ClusterCategoryCreatedCondition),
		Status: metav1.ConditionTrue,
		Reason: infrav1.Succeeded,
	})

	if err := patchHelper.Patch(rctx.Context, rctx.NutanixCluster); err != nil {
		return fmt.Errorf("failed to patch ClusterCategoryCreatedCondition on NutanixCluster %s/%s: %w", rctx.NutanixCluster.Namespace, rctx.NutanixCluster.Name, err)
	}
	return nil
}

// markClusterCategoryCreationFailed sets ClusterCategoryCreatedCondition=False
// with reason ClusterCategoryCreationFailed. Skipped if already True so a
// transient failure on one machine doesn't downgrade a prior success.
func (r *NutanixMachineReconciler) markClusterCategoryCreationFailed(rctx *nctx.MachineContext, cause error) error {
	if v1beta1conditions.IsTrue(rctx.NutanixCluster, infrav1.ClusterCategoryCreatedCondition) {
		return nil
	}

	patchHelper, err := v1beta1patch.NewHelper(rctx.NutanixCluster, r.Client)
	if err != nil {
		return fmt.Errorf("failed to init patch helper for NutanixCluster %s/%s: %w", rctx.NutanixCluster.Namespace, rctx.NutanixCluster.Name, err)
	}

	v1beta1conditions.MarkFalse(rctx.NutanixCluster, infrav1.ClusterCategoryCreatedCondition, infrav1.ClusterCategoryCreationFailed, capiv1beta1.ConditionSeverityError, "%s", cause.Error())
	v1beta2conditions.Set(rctx.NutanixCluster, metav1.Condition{
		Type:    string(infrav1.ClusterCategoryCreatedCondition),
		Status:  metav1.ConditionFalse,
		Reason:  infrav1.ClusterCategoryCreationFailed,
		Message: cause.Error(),
	})

	if err := patchHelper.Patch(rctx.Context, rctx.NutanixCluster); err != nil {
		return fmt.Errorf("failed to patch ClusterCategoryCreatedCondition on NutanixCluster %s/%s: %w", rctx.NutanixCluster.Namespace, rctx.NutanixCluster.Name, err)
	}
	return nil
}
