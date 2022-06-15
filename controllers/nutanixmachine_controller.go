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
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	apitypes "k8s.io/apimachinery/pkg/types"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/klog/v2"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	capierrors "sigs.k8s.io/cluster-api/errors"
	capiutil "sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/annotations"
	"sigs.k8s.io/cluster-api/util/conditions"
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
	nutanixClientV3 "github.com/nutanix-cloud-native/prism-go-client/pkg/nutanix/v3"
	"github.com/nutanix-cloud-native/prism-go-client/pkg/utils"
)

const (
	// provideridFmt is "nutanix://<vmUUID"
	provideridFmt = "nutanix://%s"
	projectKind   = "project"
)

// NutanixMachineReconciler reconciles a NutanixMachine object
type NutanixMachineReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// SetupWithManager sets up the controller with the Manager.
func (r *NutanixMachineReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&infrav1.NutanixMachine{}).
		// Watch the CAPI resource that owns this infrastructure resource.
		Watches(
			&source.Kind{Type: &capiv1.Machine{}},
			handler.EnqueueRequestsFromMapFunc(
				capiutil.MachineToInfrastructureMapFunc(infrav1.GroupVersion.WithKind("NutanixMachine"))),
		).
		Complete(r)
}

//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;update;delete
//+kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch;patch
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
	//_ = log.FromContext(ctx)
	logPrefix := fmt.Sprintf("NutanixMachine[namespace: %s, name: %s]", req.Namespace, req.Name)
	klog.Infof("%s Reconciling the NutanixMachine.", logPrefix)

	// Get the NutanixMachine resource for this request.
	ntxMachine := &infrav1.NutanixMachine{}
	err := r.Client.Get(ctx, req.NamespacedName, ntxMachine)
	if err != nil {
		if apierrors.IsNotFound(err) {
			klog.Infof("%s NutanixMachine not found. Ignoring since object must be deleted.", logPrefix)
			return reconcile.Result{}, nil
		}

		// Error reading the object - requeue the request.
		klog.Errorf("%s Failed to fetch the NutanixMachine object. %v", logPrefix, err)
		return reconcile.Result{}, err
	}

	// Fetch the CAPI Machine.
	machine, err := capiutil.GetOwnerMachine(ctx, r.Client, ntxMachine.ObjectMeta)
	if err != nil {
		klog.Errorf("%s Failed to fetch the owner CAPI Machine object. %v", logPrefix, err)
		return reconcile.Result{}, err
	}
	if machine == nil {
		klog.Infof("%s Waiting for capi Machine Controller to set OwnerRef on NutanixMachine", logPrefix)
		return reconcile.Result{}, nil
	}
	klog.Infof("%s Fetched the owner Machine: %s", logPrefix, machine.Name)

	// Fetch the CAPI Cluster.
	cluster, err := capiutil.GetClusterFromMetadata(ctx, r.Client, machine.ObjectMeta)
	if err != nil {
		klog.Errorf("%s Machine is missing cluster label or cluster does not exist. %v", logPrefix, err)
		return reconcile.Result{}, nil
	}
	if annotations.IsPaused(cluster, machine) {
		klog.V(4).Infof("%s linked to a cluster that is paused", logPrefix)
		return reconcile.Result{}, nil
	}

	// Fetch the NutanixCluster
	ntxCluster := &infrav1.NutanixCluster{}
	nclKey := client.ObjectKey{
		Namespace: cluster.Namespace,
		Name:      cluster.Spec.InfrastructureRef.Name,
	}
	err = r.Client.Get(ctx, nclKey, ntxCluster)
	if err != nil {
		klog.Infof("%s Waiting for NutanixCluster: %v", logPrefix, err)
		return reconcile.Result{}, nil
	}

	// Initialize the patch helper.
	patchHelper, err := patch.NewHelper(ntxMachine, r.Client)
	if err != nil {
		klog.Errorf("%s Failed to configure the patch helper. %v", logPrefix, err)
		return ctrl.Result{Requeue: true}, nil
	}

	client, err := CreateNutanixClient(ctx, r.Client, ntxCluster)
	if err != nil {
		conditions.MarkFalse(ntxMachine, infrav1.PrismCentralClientCondition, infrav1.PrismCentralClientInitializationFailed, capiv1.ConditionSeverityError, err.Error())
		return ctrl.Result{Requeue: true}, fmt.Errorf("Client Auth error: %v", err)
	}
	conditions.MarkTrue(ntxMachine, infrav1.PrismCentralClientCondition)
	rctx := &nctx.MachineContext{
		Context:        ctx,
		Cluster:        cluster,
		Machine:        machine,
		NutanixCluster: ntxCluster,
		NutanixMachine: ntxMachine,
		LogPrefix:      logPrefix,
		NutanixClient:  client,
	}

	defer func() {
		if err == nil {
			// Always attempt to Patch the NutanixMachine object and its status after each reconciliation.
			if err := patchHelper.Patch(ctx, ntxMachine); err != nil {
				klog.Errorf("%s Failed to patch NutanixMachine. %v", rctx.LogPrefix, err)
				reterr = kerrors.NewAggregate([]error{reterr, err})
			}
			klog.Infof("%s Patched NutanixMachine. Spec: %+v. Status: %+v.",
				rctx.LogPrefix, ntxMachine.Spec, ntxMachine.Status)
		} else {
			klog.Infof("%s Not patching vm since error occurred: %v", rctx.LogPrefix, err)
		}
	}()

	// Handle deleted machines
	if !machine.ObjectMeta.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(rctx)
	}

	// Handle non-deleted machines
	return r.reconcileNormal(rctx)
}

func (r *NutanixMachineReconciler) reconcileDelete(rctx *nctx.MachineContext) (reconcile.Result, error) {
	client := rctx.NutanixClient
	vmName := rctx.NutanixMachine.Name
	klog.Infof("%s Handling NutanixMachine deletion of VM: %s", rctx.LogPrefix, vmName)
	conditions.MarkFalse(rctx.NutanixMachine, infrav1.VMProvisionedCondition, capiv1.DeletingReason, capiv1.ConditionSeverityInfo, "")
	//Check if VMUUID is absent
	if rctx.NutanixMachine.Status.VmUUID == "" {
		klog.Warningf("%s VMUUID was not found in spec for VM %s. Skipping delete", rctx.LogPrefix, vmName)
	} else {
		//Search for VM by UUID
		vmUUID := rctx.NutanixMachine.Status.VmUUID
		vm, err := findVMByUUID(client, vmUUID)
		// Error while finding VM
		if err != nil {
			errorMsg := fmt.Errorf("%v: error finding vm %s with uuid %s: %v", rctx.LogPrefix, vmName, vmUUID, err)
			klog.Error(errorMsg)
			conditions.MarkFalse(rctx.NutanixMachine, infrav1.VMProvisionedCondition, infrav1.DeletionFailed, capiv1.ConditionSeverityWarning, errorMsg.Error())
			return reconcile.Result{}, errorMsg
		}
		// Vm not found
		if vm == nil {
			klog.Infof("%s No vm found with UUID %s ... Already deleted? Skipping delete", rctx.LogPrefix, vmUUID)
		} else {
			klog.Infof("%s VM %s with UUID %s was found.", rctx.LogPrefix, vmName, vmUUID)
			lastTaskUUID, err := getTaskUUIDFromVM(vm)
			if err != nil {
				errorMsg := fmt.Errorf("error occurred fetching task UUID from vm: %v", err)
				klog.Error(errorMsg)
				conditions.MarkFalse(rctx.NutanixMachine, infrav1.VMProvisionedCondition, infrav1.DeletionFailed, capiv1.ConditionSeverityWarning, errorMsg.Error())
				return reconcile.Result{}, errorMsg
			}
			klog.Infof("%s checking if VM %s with UUID %s has in progress tasks", rctx.LogPrefix, vmName, vmUUID)
			taskInProgress, err := hasTaskInProgress(rctx.NutanixClient, lastTaskUUID)
			if err != nil {
				klog.Warningf("%s error occurred while checking task %s for VM %s... err: %v ....Trying to delete VM", rctx.LogPrefix, lastTaskUUID, vmName, vmUUID, err)
			}
			if taskInProgress {
				klog.Infof("VM %s task with UUID %s still in progress. Requeuing", vmName, vmUUID)
				return reconcile.Result{RequeueAfter: 5 * time.Second}, nil
			}
			klog.Infof("%s No running tasks anymore... Initiating delete for vm %s with UUID %s", rctx.LogPrefix, vmName, vmUUID)
			// Delete the VM since the VM was found (err was nil)
			deleteTaskUUID, err := deleteVM(client, vmName, vmUUID)
			if err != nil {
				errorMsg := fmt.Errorf("Failed to delete VM %s with UUID %s: %v", vmName, vmUUID, err)
				conditions.MarkFalse(rctx.NutanixMachine, infrav1.VMProvisionedCondition, infrav1.DeletionFailed, capiv1.ConditionSeverityWarning, errorMsg.Error())
				klog.Errorf("%s %v", rctx.LogPrefix, errorMsg)
				return reconcile.Result{}, err
			}
			klog.Infof("%s Deletion task with UUID %s received for vm %s with UUID %s. Requeueing", rctx.LogPrefix, deleteTaskUUID, vmName, vmUUID)
			return reconcile.Result{RequeueAfter: 5 * time.Second}, nil
		}
	}

	// Remove the finalizer from the NutanixMachine object
	klog.Errorf("%s Removing finalizers for VM %s during delete reconciliation", rctx.LogPrefix, vmName)
	ctrlutil.RemoveFinalizer(rctx.NutanixMachine, infrav1.NutanixMachineFinalizer)

	return reconcile.Result{}, nil
}

func (r *NutanixMachineReconciler) reconcileNormal(rctx *nctx.MachineContext) (reconcile.Result, error) {

	if rctx.NutanixMachine.Status.FailureReason != nil || rctx.NutanixMachine.Status.FailureMessage != nil {
		klog.Errorf("Nutanix Machine has failed. Will not reconcile %s", rctx.NutanixMachine.Name)
		return reconcile.Result{}, nil
	}
	klog.Infof("%s Handling NutanixMachine reconciling", rctx.LogPrefix)
	var err error

	// Add finalizer first if not exist to avoid the race condition between init and delete
	if !ctrlutil.ContainsFinalizer(rctx.NutanixMachine, infrav1.NutanixMachineFinalizer) {
		ctrlutil.AddFinalizer(rctx.NutanixMachine, infrav1.NutanixMachineFinalizer)
	}

	klog.Infof("%s Checking current machine status for machine %s: Status %+v Spec %+v", rctx.LogPrefix, rctx.NutanixMachine.Name, rctx.NutanixMachine.Status, rctx.NutanixMachine.Spec)
	if rctx.NutanixMachine.Status.Ready {
		if !rctx.Machine.Status.InfrastructureReady || rctx.Machine.Spec.ProviderID == nil {
			klog.Infof("%s The NutanixMachine is ready, wait for the owner Machine's update.", rctx.LogPrefix)
			return reconcile.Result{RequeueAfter: 5 * time.Second}, nil
		}
		klog.Infof("%s The NutanixMachine is already ready, providerID: %s", rctx.LogPrefix, rctx.NutanixMachine.Spec.ProviderID)

		if rctx.NutanixMachine.Status.NodeRef == nil {
			err = r.reconcileNode(rctx)
			if err != nil {
				klog.Errorf("%s Failed to reconcile the workload cluster node. %v", rctx.LogPrefix, err)
				return reconcile.Result{}, err
			}
		}

		return reconcile.Result{}, nil
	}

	// Make sure Cluster.Status.InfrastructureReady is true
	klog.Infof("%s Checking if cluster infrastructure is ready", rctx.LogPrefix)
	if !rctx.Cluster.Status.InfrastructureReady {
		klog.Infof("%s The cluster infrastructure is not ready yet", rctx.LogPrefix)
		return reconcile.Result{}, nil
	}

	// Make sure bootstrap data is available and populated.
	if rctx.NutanixMachine.Spec.BootstrapRef == nil {
		if rctx.Machine.Spec.Bootstrap.DataSecretName == nil {
			if !nctx.IsControlPlaneMachine(rctx.NutanixMachine) &&
				!conditions.IsTrue(rctx.Cluster, capiv1.ControlPlaneInitializedCondition) {
				klog.Infof("%s Waiting for the control plane to be initialized", rctx.LogPrefix)
			} else {
				klog.Infof("%s Waiting for bootstrap data to be available", rctx.LogPrefix)
			}
			return reconcile.Result{}, nil
		}

		rctx.NutanixMachine.Spec.BootstrapRef = &corev1.ObjectReference{
			APIVersion: "v1",
			Kind:       "Secret",
			Name:       *rctx.Machine.Spec.Bootstrap.DataSecretName,
			Namespace:  rctx.Machine.ObjectMeta.Namespace,
		}
		klog.Infof("%s Added the spec.bootstrapRef to NutanixMachine object: %v", rctx.LogPrefix, rctx.NutanixMachine.Spec.BootstrapRef)
	}

	// Create the  or get existing VM
	vm, err := r.getOrCreateVM(rctx)
	if err != nil {
		klog.Errorf("%s Failed to create VM %s.", rctx.LogPrefix, rctx.NutanixMachine.Name)
		return reconcile.Result{}, err
	}
	klog.Infof("%s Found VM with name: %s, vmUUID: %s", rctx.LogPrefix, rctx.NutanixMachine.Name, *vm.Metadata.UUID)
	rctx.NutanixMachine.Status.VmUUID = *vm.Metadata.UUID
	klog.Infof("%s Patching machine post creation name: %s, vmUUID: %s", rctx.LogPrefix, rctx.NutanixMachine.Name, rctx.NutanixMachine.Status.VmUUID)
	err = r.patchMachine(rctx)
	if err != nil {
		errorMsg := fmt.Errorf("%s Failed to patch NutanixMachine %s after creation. %v", rctx.LogPrefix, rctx.NutanixMachine.Name, err)
		klog.Error(errorMsg)
		return reconcile.Result{}, errorMsg
	}
	klog.Infof("%s Assigning IP addresses to VM with name: %s, vmUUID: %s", rctx.LogPrefix, rctx.NutanixMachine.Name, rctx.NutanixMachine.Status.VmUUID)
	err = r.assignAddressesToMachine(rctx, vm)
	if err != nil {
		errorMsg := fmt.Errorf("Failed to assign addresses to VM %s with UUID %s...: %v", rctx.NutanixMachine.Name, rctx.NutanixMachine.Status.VmUUID, err)
		klog.Error(errorMsg)
		conditions.MarkFalse(rctx.NutanixMachine, infrav1.VMAddressesAssignedCondition, infrav1.VMAddressesFailed, capiv1.ConditionSeverityError, err.Error())
		return reconcile.Result{}, errorMsg
	}
	conditions.MarkTrue(rctx.NutanixMachine, infrav1.VMAddressesAssignedCondition)
	// Update the NutanixMachine Spec.ProviderID
	rctx.NutanixMachine.Spec.ProviderID = fmt.Sprintf(provideridFmt, rctx.NutanixMachine.Status.VmUUID)
	rctx.NutanixMachine.Status.Ready = true
	klog.Infof("%s Created VM %s for cluster %s, update NutanixMachine spec.providerID to %s, and machinespec %+v, vmUuid: %s",
		rctx.LogPrefix, rctx.NutanixMachine.Name, rctx.NutanixCluster.Name, rctx.NutanixMachine.Spec.ProviderID,
		rctx.NutanixMachine, rctx.NutanixMachine.Status.VmUUID)
	return reconcile.Result{}, nil
}

// reconcileNode makes sure the NutanixMachine corresponding workload cluster node
// is ready and set its spec.providerID
func (r *NutanixMachineReconciler) reconcileNode(rctx *nctx.MachineContext) error {

	klog.Infof("%s Reconcile the workload cluster node to set its spec.providerID", rctx.LogPrefix)

	clusterKey := apitypes.NamespacedName{
		Namespace: rctx.Cluster.Namespace,
		Name:      rctx.Cluster.Name,
	}
	remoteClient, err := nctx.GetRemoteClient(rctx.Context, r.Client, clusterKey)
	if err != nil {
		klog.Errorf("%s Failed to get the client to access remote workload cluster %s. %v", rctx.LogPrefix, rctx.Cluster.Name, err)
		return err
	}

	// Retrieve the remote node
	nodeName := rctx.NutanixMachine.Name
	node := &corev1.Node{}
	nodeKey := apitypes.NamespacedName{
		Namespace: "",
		Name:      nodeName,
	}

	for {
		err = remoteClient.Get(rctx.Context, nodeKey, node)
		if err == nil {
			break
		}

		if apierrors.IsNotFound(err) {
			errorMessage := fmt.Sprintf("%s workload node %s not yet ready ... Retrying", rctx.LogPrefix, nodeName)
			klog.Errorf(errorMessage)
			return fmt.Errorf(errorMessage)
		} else {
			klog.Errorf("%s Failed to retrieve the remote workload cluster node %s", rctx.LogPrefix, nodeName)
			return err
		}
	}

	// Set the NutanixMachine Status.NodeRef
	if rctx.NutanixMachine.Status.NodeRef == nil {
		rctx.NutanixMachine.Status.NodeRef = &corev1.ObjectReference{
			Kind:       node.Kind,
			APIVersion: node.APIVersion,
			Name:       node.Name,
			UID:        node.UID,
		}
		klog.Infof("%s Set NutanixMachine's status.nodeRef: %v", rctx.LogPrefix, *rctx.NutanixMachine.Status.NodeRef)
	}

	// Update the node's Spec.ProviderID
	patchHelper, err := patch.NewHelper(node, remoteClient)
	if err != nil {
		klog.Errorf("%s Failed to create patchHelper for the workload cluster node %s. %v", rctx.LogPrefix, nodeName, err)
		return err
	}

	node.Spec.ProviderID = rctx.NutanixMachine.Spec.ProviderID
	err = patchHelper.Patch(rctx.Context, node)
	if err != nil {
		klog.Errorf("%s Failed to patch the remote workload cluster node %s's spec.providerID. %v", rctx.LogPrefix, nodeName, err)
		return err
	}
	klog.Infof("%s Patched the workload node %s spec.providerID: %s", rctx.LogPrefix, nodeName, node.Spec.ProviderID)

	return nil
}

// GetOrCreateVM creates a VM and is invoked by the NutanixMachineReconciler
func (r *NutanixMachineReconciler) getOrCreateVM(rctx *nctx.MachineContext) (*nutanixClientV3.VMIntentResponse, error) {

	var err error
	var vm *nutanixClientV3.VMIntentResponse
	vmName := rctx.NutanixMachine.Name
	client := rctx.NutanixClient

	// Check if the VM already exists
	vm, err = findVM(client, rctx.NutanixMachine)
	if err != nil {
		klog.Errorf("%s error occurred finding VM %s by name or uuid %s: %v", rctx.LogPrefix, vmName, err)
		return nil, err
	}
	if vm != nil {
		klog.Infof("%s vm %s found with UUID %s", rctx.LogPrefix, *vm.Spec.Name, rctx.NutanixMachine.Status.VmUUID)
	} else {
		klog.Infof("%s No existing VM found. Starting creation process of VM %s.", rctx.LogPrefix, vmName)
		// Get PE UUID
		peUUID, err := getPEUUID(client, rctx.NutanixMachine.Spec.Cluster.Name, rctx.NutanixMachine.Spec.Cluster.UUID)
		if err != nil {
			errorMsg := fmt.Errorf("Failed to get the Prism Element Cluster UUID to create the VM %s. %v", vmName, err)
			rctx.SetFailureStatus(capierrors.CreateMachineError, errorMsg)
			klog.Errorf("%s %v", rctx.LogPrefix, errorMsg)
			return nil, err
		}
		// Get Subnet UUIDs
		subnetUUIDs, err := getSubnetUUIDList(client, rctx.NutanixMachine.Spec.Subnets, peUUID)
		if err != nil {
			errorMsg := fmt.Errorf("Failed to get the subnet UUIDs to create the VM %s. %v", vmName, err)
			klog.Errorf("%s %v", rctx.LogPrefix, vmName, err)
			rctx.SetFailureStatus(capierrors.CreateMachineError, errorMsg)
			return nil, err
		}
		// Get Image UUID
		imageUUID, err := getImageUUID(
			client,
			rctx.NutanixMachine.Spec.Image.Name,
			rctx.NutanixMachine.Spec.Image.UUID,
		)
		if err != nil {
			errorMsg := fmt.Errorf("Failed to get the image UUID to create the VM %s. %v", vmName, err)
			klog.Errorf("%s %v", rctx.LogPrefix, errorMsg)
			rctx.SetFailureStatus(capierrors.CreateMachineError, errorMsg)
			return nil, err
		}
		// Get the bootstrapData from the referenced secret
		bootstrapData, err := r.getBootstrapData(rctx)
		if err != nil {
			klog.Errorf("%s Failed to get the bootstrap data for create the VM %s. %v", rctx.LogPrefix, vmName, err)
			return nil, err
		}
		// Encode the bootstrapData by base64
		bsdataEncoded := base64.StdEncoding.EncodeToString(bootstrapData)
		klog.Infof("%s Retrieved the bootstrap data from secret %s (before encoding size: %d, encoded string size:%d)",
			rctx.LogPrefix, rctx.NutanixMachine.Spec.BootstrapRef.Name, len(bootstrapData), len(bsdataEncoded))

		// Create the VM
		klog.Infof("%s To create VM with name %s for cluster %s.", rctx.LogPrefix,
			rctx.NutanixMachine.Name, rctx.NutanixCluster.Name)
		vmInput := nutanixClientV3.VMIntentInput{}
		vmSpec := nutanixClientV3.VM{Name: utils.StringPtr(vmName)}
		nicList := []*nutanixClientV3.VMNic{}
		for _, subnetUUID := range subnetUUIDs {
			nicList = append(nicList, &nutanixClientV3.VMNic{
				SubnetReference: &nutanixClientV3.Reference{
					UUID: utils.StringPtr(subnetUUID),
					Kind: utils.StringPtr("subnet"),
				}})
		}
		diskSize := rctx.NutanixMachine.Spec.SystemDiskSize
		diskSizeMib := getMibValueOfQuantity(diskSize)
		systemDisk, err := createSystemDiskSpec(imageUUID, diskSizeMib)
		if err != nil {
			errorMsg := fmt.Errorf("error occurred while creating system disk spec: %v", err)
			rctx.SetFailureStatus(capierrors.CreateMachineError, errorMsg)
			return nil, errorMsg
		}
		diskList := []*nutanixClientV3.VMDisk{
			systemDisk,
		}
		categories, err := getCategoryVMSpec(client, r.getMachineCategoryIdentifiers(rctx))
		if err != nil {
			errorMsg := fmt.Errorf("error occurred while creating category spec for vm %s: %v", vmName, err)
			rctx.SetFailureStatus(capierrors.CreateMachineError, errorMsg)
			return nil, errorMsg
		}
		vmMetadata := nutanixClientV3.Metadata{
			Kind:        utils.StringPtr("vm"),
			SpecVersion: utils.Int64Ptr(1),
			Categories:  categories,
		}

		vmMetadataPtr := &vmMetadata
		err = r.addVMToProject(rctx, vmMetadataPtr)
		if err != nil {
			errorMsg := fmt.Errorf("error occurred while trying to add VM %s to project: %v", vmName, err)
			rctx.SetFailureStatus(capierrors.CreateMachineError, errorMsg)
			klog.Errorf("%s %v", rctx.LogPrefix, errorMsg)
			return nil, err
		}

		vmSpec.Resources = &nutanixClientV3.VMResources{
			PowerState:            utils.StringPtr("ON"),
			HardwareClockTimezone: utils.StringPtr("UTC"),
			NumVcpusPerSocket:     utils.Int64Ptr(int64(rctx.NutanixMachine.Spec.VCPUsPerSocket)),
			NumSockets:            utils.Int64Ptr(int64(rctx.NutanixMachine.Spec.VCPUSockets)),
			MemorySizeMib:         utils.Int64Ptr(getMibValueOfQuantity(rctx.NutanixMachine.Spec.MemorySize)),
			NicList:               nicList,
			DiskList:              diskList,
			GuestCustomization: &nutanixClientV3.GuestCustomization{
				IsOverridable: utils.BoolPtr(true),
				CloudInit:     &nutanixClientV3.GuestCustomizationCloudInit{UserData: utils.StringPtr(bsdataEncoded)}},
		}
		vmSpec.ClusterReference = &nutanixClientV3.Reference{
			Kind: utils.StringPtr("cluster"),
			UUID: utils.StringPtr(peUUID),
		}
		vmSpecPtr := &vmSpec
		err = r.addBootTypeToVM(rctx, vmSpecPtr)
		if err != nil {
			errorMsg := fmt.Errorf("error occurred while adding boot type to vm spec: %v", err)
			rctx.SetFailureStatus(capierrors.CreateMachineError, errorMsg)
			klog.Errorf("%s %v", rctx.LogPrefix, errorMsg)
			return nil, err
		}
		vmInput.Spec = vmSpecPtr
		vmInput.Metadata = vmMetadataPtr

		vmResponse, err := client.V3.CreateVM(&vmInput)
		if err != nil {
			errorMsg := fmt.Errorf("Failed to create VM %s. error: %v", vmName, err)
			rctx.SetFailureStatus(capierrors.CreateMachineError, errorMsg)
			klog.Errorf("%s %v", rctx.LogPrefix, errorMsg)
			return nil, err
		}
		vmUuid := *vmResponse.Metadata.UUID
		klog.Infof("%s Sent the post request to create VM %s. Got the vm UUID: %s, status.state: %s", rctx.LogPrefix,
			rctx.NutanixMachine.Name, vmUuid, *vmResponse.Status.State)
		klog.Infof("%s Getting task uuid for VM %s", rctx.LogPrefix,
			rctx.NutanixMachine.Name)
		lastTaskUUID, err := getTaskUUIDFromVM(vmResponse)
		if err != nil {
			errorMsg := fmt.Errorf("%s error occurred fetching task UUID from vm %s after creation: %v", rctx.LogPrefix, rctx.NutanixMachine.Name, err)
			klog.Error(errorMsg)
			return nil, errorMsg
		}
		klog.Infof("%s Waiting for task %s to get completed for VM %s", rctx.LogPrefix,
			lastTaskUUID, rctx.NutanixMachine.Name)
		err = nutanixClient.WaitForTaskCompletion(client, lastTaskUUID)
		if err != nil {
			errorMsg := fmt.Errorf("%s  error occurred while waiting for task %s to start: %v", rctx.LogPrefix, lastTaskUUID, err)
			klog.Error(errorMsg)
			rctx.SetFailureStatus(capierrors.CreateMachineError, errorMsg)
			return nil, errorMsg
		}
		klog.Infof("%s Fetching VM after creation %s", rctx.LogPrefix,
			lastTaskUUID, rctx.NutanixMachine.Name)
		vm, err = findVMByUUID(client, vmUuid)
		if err != nil {
			errorMsg := fmt.Errorf("%s  error occurred while getting VM %s after creation: %v", rctx.LogPrefix, rctx.NutanixMachine.Name, err)
			klog.Error(errorMsg)
			rctx.SetFailureStatus(capierrors.CreateMachineError, errorMsg)
			return nil, errorMsg
		}
	}
	conditions.MarkTrue(rctx.NutanixMachine, infrav1.VMProvisionedCondition)
	return vm, nil
}

// getBootstrapData returns the Bootstrap data from the ref secret
func (r *NutanixMachineReconciler) getBootstrapData(rctx *nctx.MachineContext) ([]byte, error) {
	if rctx.NutanixMachine.Spec.BootstrapRef == nil {
		klog.Errorf("%s NutanixMachine spec.BootstrapRef is nil.", rctx.LogPrefix)
		return nil, errors.New("NutanixMachine spec.BootstrapRef is nil.")
	}

	secretName := rctx.NutanixMachine.Spec.BootstrapRef.Name
	secret := &corev1.Secret{}
	secretKey := apitypes.NamespacedName{
		Namespace: rctx.NutanixMachine.Spec.BootstrapRef.Namespace,
		Name:      secretName,
	}
	if err := r.Client.Get(rctx.Context, secretKey, secret); err != nil {
		klog.Errorf("%s failed to retrieve bootstrap data secret %s. %v", rctx.LogPrefix, secretName, err)
		return nil, errors.Wrapf(err, "failed to retrieve bootstrap data secret %s", secretName)
	}

	value, ok := secret.Data["value"]
	if !ok {
		klog.Errorf("%s failed to retrieve bootstrap data secret %s. Secret 'value' key is missing", rctx.LogPrefix, secretName)
		return nil, errors.New("error retrieving bootstrap data: secret value key is missing")
	}
	klog.V(6).Infof("%s Retrieved the NutanixMachine bootstrap data (size: %d):\n%s", rctx.LogPrefix, len(value), string(value))

	return value, nil
}

func (r *NutanixMachineReconciler) patchMachine(rctx *nctx.MachineContext) error {
	patchHelper, err := patch.NewHelper(rctx.NutanixMachine, r.Client)
	if err != nil {
		errorMsg := fmt.Errorf("%s Failed to create patch helper to patch machine %s: %v", rctx.LogPrefix, rctx.NutanixMachine.Name, err)
		klog.Error(errorMsg)
		return errorMsg
	}
	err = patchHelper.Patch(rctx.Context, rctx.NutanixMachine)
	if err != nil {
		errorMsg := fmt.Errorf("%s Failed to patch machine %s: %v", rctx.LogPrefix, rctx.NutanixMachine.Name, err)
		klog.Error(errorMsg)
		return errorMsg
	}
	klog.Infof("%s Patched machine %s: Status %+v Spec %+v", rctx.LogPrefix, rctx.NutanixMachine.Name, rctx.NutanixMachine.Status, rctx.NutanixMachine.Spec)
	return nil
}

func (r *NutanixMachineReconciler) assignAddressesToMachine(rctx *nctx.MachineContext, vm *nutanixClientV3.VMIntentResponse) error {
	rctx.NutanixMachine.Status.Addresses = []capiv1.MachineAddress{}
	if vm.Status == nil || vm.Status.Resources == nil {
		return fmt.Errorf("unable to fetch network interfaces from VM. Retrying")
	}
	foundIPs := 0
	for _, nic := range vm.Status.Resources.NicList {
		for _, ipEndpoint := range nic.IPEndpointList {
			if ipEndpoint.IP != nil {
				rctx.NutanixMachine.Status.Addresses = append(rctx.NutanixMachine.Status.Addresses, capiv1.MachineAddress{
					Type:    capiv1.MachineInternalIP,
					Address: *ipEndpoint.IP,
				})
				foundIPs++
			}
		}
	}
	if foundIPs == 0 {
		return fmt.Errorf("unable to determine network interfaces from VM. Retrying")
	}
	rctx.IP = rctx.NutanixMachine.Status.Addresses[0].Address
	rctx.NutanixMachine.Status.Addresses = append(rctx.NutanixMachine.Status.Addresses, capiv1.MachineAddress{
		Type:    capiv1.MachineHostName,
		Address: *vm.Spec.Name,
	})
	return nil
}

func (r *NutanixMachineReconciler) getMachineCategoryIdentifiers(rctx *nctx.MachineContext) []*infrav1.NutanixCategoryIdentifier {
	categoryIdentifiers := getDefaultCAPICategoryIdentifiers(rctx.Cluster.Name)
	additionalCategories := rctx.NutanixMachine.Spec.AdditionalCategories
	if additionalCategories != nil && len(additionalCategories) > 0 {
		for _, at := range additionalCategories {
			var additionalCat infrav1.NutanixCategoryIdentifier
			additionalCat = at
			categoryIdentifiers = append(categoryIdentifiers, &additionalCat)
		}
	}
	return categoryIdentifiers
}

func (r *NutanixMachineReconciler) addBootTypeToVM(rctx *nctx.MachineContext, vmSpec *nutanixClientV3.VM) error {
	bootType := rctx.NutanixMachine.Spec.BootType
	// Defaults to legacy if boot type is not set.
	if bootType != "" {
		if bootType != string(infrav1.NutanixIdentifierBootTypeLegacy) && bootType != string(infrav1.NutanixIdentifierBootTypeUEFI) {
			errorMsg := fmt.Errorf("%s boot type must be %s or %s but was %s", rctx.LogPrefix, string(infrav1.NutanixIdentifierBootTypeLegacy), string(infrav1.NutanixIdentifierBootTypeUEFI), bootType)
			klog.Error(errorMsg)
			conditions.MarkFalse(rctx.NutanixMachine, infrav1.VMProvisionedCondition, infrav1.VMBootTypeInvalid, capiv1.ConditionSeverityError, errorMsg.Error())
			return errorMsg
		}

		// Only modify VM spec if boot type is UEFI. Otherwise assume default Legacy mode
		if bootType == string(infrav1.NutanixIdentifierBootTypeUEFI) {
			vmSpec.Resources.BootConfig = &nutanixClientV3.VMBootConfig{
				BootType: utils.StringPtr(strings.ToUpper(bootType)),
			}
		}
	}

	return nil
}

func (r *NutanixMachineReconciler) addVMToProject(rctx *nctx.MachineContext, vmMetadata *nutanixClientV3.Metadata) error {

	vmName := rctx.NutanixMachine.Name
	projectRef := rctx.NutanixMachine.Spec.Project
	if projectRef == nil {
		klog.Infof("%s Not linking VM %s to a project", rctx.LogPrefix, vmName)
		return nil
	}
	if vmMetadata == nil {
		errorMsg := fmt.Errorf("%s metadata cannot be nil when adding VM %s to project", rctx.LogPrefix, vmName)
		klog.Error(errorMsg)
		conditions.MarkFalse(rctx.NutanixMachine, infrav1.ProjectAssignedCondition, infrav1.ProjectAssignationFailed, capiv1.ConditionSeverityError, errorMsg.Error())
		return errorMsg
	}
	projectUUID, err := getProjectUUID(rctx.NutanixClient, projectRef.Name, projectRef.UUID)
	if err != nil {
		errorMsg := fmt.Errorf("%s error occurred while searching for project for VM %s: %v", rctx.LogPrefix, vmName, err)
		klog.Error(errorMsg)
		conditions.MarkFalse(rctx.NutanixMachine, infrav1.ProjectAssignedCondition, infrav1.ProjectAssignationFailed, capiv1.ConditionSeverityError, errorMsg.Error())
		return errorMsg
	}
	vmMetadata.ProjectReference = &nutanixClientV3.Reference{
		Kind: utils.StringPtr(projectKind),
		UUID: utils.StringPtr(projectUUID),
	}
	conditions.MarkTrue(rctx.NutanixMachine, infrav1.ProjectAssignedCondition)
	return nil
}
