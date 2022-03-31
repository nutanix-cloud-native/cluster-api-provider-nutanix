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
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	apitypes "k8s.io/apimachinery/pkg/types"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/klog/v2"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
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

	infrav1 "github.com/nutanix-core/cluster-api-provider-nutanix/api/v1beta1"
	nutanixClient "github.com/nutanix-core/cluster-api-provider-nutanix/pkg/client"
	nctx "github.com/nutanix-core/cluster-api-provider-nutanix/pkg/context"
	nutanixClientV3 "github.com/nutanix-core/cluster-api-provider-nutanix/pkg/nutanix/v3"
	"github.com/nutanix-core/cluster-api-provider-nutanix/pkg/utils"
)

const (
	// provideridFmt is "nutanix://<vmUUID"
	provideridFmt = "nutanix://%s"
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

//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
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

	rctx := &nctx.MachineContext{
		Context:        ctx,
		Cluster:        cluster,
		Machine:        machine,
		NutanixCluster: ntxCluster,
		NutanixMachine: ntxMachine,
		LogPrefix:      logPrefix,
	}

	defer func() {
		// Always attempt to Patch the NutanixMachine object and its status after each reconciliation.
		if err := patchHelper.Patch(ctx, ntxMachine); err != nil {
			klog.Errorf("%s Failed to patch NutanixMachine. %v", rctx.LogPrefix, err)
			reterr = kerrors.NewAggregate([]error{reterr, err})
		}
		klog.Infof("%s Patched NutanixMachine. Status: %+v",
			rctx.LogPrefix, ntxMachine.Status)
	}()

	// Handle deleted machines
	if !machine.ObjectMeta.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(rctx)
	}

	// Handle non-deleted machines
	return r.reconcileNormal(rctx)
}

func (r *NutanixMachineReconciler) reconcileDelete(rctx *nctx.MachineContext) (reconcile.Result, error) {

	vmName := rctx.NutanixMachine.Name
	klog.Infof("%s Handling NutanixMachine deletion of VM: %s", rctx.LogPrefix, vmName)

	// Delete the VM
	err := deleteVM(rctx)
	if err != nil {
		klog.Errorf("%s Failed to delete VM %s: %v", rctx.LogPrefix, vmName, err)
		return reconcile.Result{}, err
	}

	// Remove the finalizer from the NutanixMachine object
	klog.Errorf("%s Removing finalizers for VM %s during delete reconciliation", rctx.LogPrefix, vmName)
	ctrlutil.RemoveFinalizer(rctx.NutanixMachine, infrav1.NutanixMachineFinalizer)

	return reconcile.Result{}, nil
}

func (r *NutanixMachineReconciler) reconcileNormal(rctx *nctx.MachineContext) (reconcile.Result, error) {

	klog.Infof("%s Handling NutanixMachine reconciling", rctx.LogPrefix)
	var err error

	// Add finalizer first if not exist to avoid the race condition between init and delete
	if !ctrlutil.ContainsFinalizer(rctx.NutanixMachine, infrav1.NutanixMachineFinalizer) {
		ctrlutil.AddFinalizer(rctx.NutanixMachine, infrav1.NutanixMachineFinalizer)
	}

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

	// Create the VM
	err = r.createVM(rctx)
	if err != nil {
		klog.Errorf("%s Failed to create VM %s.", rctx.LogPrefix, rctx.NutanixMachine.Name)
		return reconcile.Result{}, err
	}
	klog.Infof("%s Created VM with name: %s, vmUUID: %s", rctx.LogPrefix, rctx.NutanixMachine.Name, *rctx.NutanixMachine.Status.VmUUID)

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
			klog.Infof("%s Wait for the workload node %s to get ready ...", rctx.LogPrefix, nodeName)
			time.Sleep(5 * time.Second)
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

// CreateVM creates a VM and is invoked by the NutanixMachineReconciler
func (r *NutanixMachineReconciler) createVM(rctx *nctx.MachineContext) error {

	var err error

	client, err := nutanixClient.Client(nutanixClient.ClientOptions{})
	if err != nil {
		return fmt.Errorf("Client Auth error: %v", err)
	}
	rctx.NutanixClient = client

	var vm *nutanixClientV3.VMIntentResponse
	var vmUuid string
	vmName := rctx.NutanixMachine.Name

	// Check if the VM already exists
	if rctx.NutanixMachine.Status.VmUUID != nil {
		// Try to find the vm by uuid
		vm, err = findVMByUUID(rctx, *rctx.NutanixMachine.Status.VmUUID)
		if err == nil {
			klog.Infof("%s The VM with UUID %s already exists. No need to create one.", rctx.LogPrefix, *rctx.NutanixMachine.Status.VmUUID)
			vmUuid = *vm.Metadata.UUID
		}
	}

	if len(vmUuid) == 0 {
		klog.Infof("%s Starting creation process of VM %s.", rctx.LogPrefix, vmName)
		// Get PE UUID
		peUUID, err := getPEUUID(rctx)
		if err != nil {
			klog.Errorf("%s Failed to get the Prism Element Cluster UUID to create the VM %s. %v", rctx.LogPrefix, vmName, err)
			return err
		}
		// Get Subnet UUID
		subnetUUID, err := getSubnetUUID(rctx, peUUID)
		if err != nil {
			klog.Errorf("%s Failed to get the subnet UUID to create the VM %s. %v", rctx.LogPrefix, vmName, err)
			return err
		}
		// Get Image UUID
		imageUUID, err := getImageUUID(rctx)
		if err != nil {
			klog.Errorf("%s Failed to get the image UUID to create the VM %s. %v", rctx.LogPrefix, vmName, err)
			return err
		}
		// Get the bootstrapData from the referenced secret
		bootstrapData, err := r.getBootstrapData(rctx)
		if err != nil {
			klog.Errorf("%s Failed to get the bootstrap data for create the VM %s. %v", rctx.LogPrefix, vmName, err)
			return err
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
		vmNic := &nutanixClientV3.VMNic{
			SubnetReference: &nutanixClientV3.Reference{
				UUID: utils.StringPtr(subnetUUID),
				Kind: utils.StringPtr("subnet"),
			}}
		nicList := []*nutanixClientV3.VMNic{vmNic}
		// If this is controlplane node Machine, use the cluster's spec.controlPlaneEndpoint host IP to create VM
		if nctx.IsControlPlaneMachine(rctx.NutanixMachine) {
			vmNic.IPEndpointList = []*nutanixClientV3.IPAddress{&nutanixClientV3.IPAddress{
				//Type: utils.StringPtr("ASSIGNED"),
				IP: utils.StringPtr(rctx.NutanixCluster.Spec.ControlPlaneEndpoint.Host)}}
		}
		diskSize := rctx.NutanixMachine.Spec.SystemDiskSize
		diskSizeMib := GetMibValueOfQuantity(diskSize)
		systemDisk, err := createSystemDiskSpec(imageUUID, diskSizeMib)
		if err != nil {
			return fmt.Errorf("error occurred while creating system disk spec: %v", err)
		}
		diskList := []*nutanixClientV3.VMDisk{
			systemDisk,
		}
		vmMetadata := nutanixClientV3.Metadata{
			Kind:        utils.StringPtr("vm"),
			SpecVersion: utils.Int64Ptr(1),
		}
		vmSpec.Resources = &nutanixClientV3.VMResources{
			PowerState:            utils.StringPtr("ON"),
			HardwareClockTimezone: utils.StringPtr("UTC"),
			NumVcpusPerSocket:     utils.Int64Ptr(int64(rctx.NutanixMachine.Spec.VCPUsPerSocket)),
			NumSockets:            utils.Int64Ptr(int64(rctx.NutanixMachine.Spec.VCPUSockets)),
			MemorySizeMib:         utils.Int64Ptr(GetMibValueOfQuantity(rctx.NutanixMachine.Spec.MemorySize)),
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
		vmInput.Spec = &vmSpec
		vmInput.Metadata = &vmMetadata

		vm, err = client.V3.CreateVM(&vmInput)
		if err != nil {
			klog.Errorf("%s Failed to create VM %s. error: %v", rctx.LogPrefix, vmName, err)
			return err
		}
		vmUuid = *vm.Metadata.UUID
		klog.Infof("%s Sent the post request to create VM %s. Got the vm UUID: %s, status.state: %s", rctx.LogPrefix,
			rctx.NutanixMachine.Name, vmUuid, *vm.Status.State)
		// Wait for some time for the VM getting ready
		time.Sleep(10 * time.Second)
	}

	//Let's wait to vm's state to become "COMPLETE"
	err = nutanixClient.WaitForGetVMComplete(client, vmUuid)
	if err != nil {
		klog.Errorf("%s Failed to get the vm with UUID %s. error: %v", rctx.LogPrefix, vmUuid, err)
		return fmt.Errorf("Error retriving the created vm %s", rctx.NutanixMachine.Name)
	}

	vm, err = findVMByUUID(rctx, vmUuid)
	for err != nil {
		klog.Errorf("%s Failed to find the vm with UUID %s. %v", rctx.LogPrefix, vmUuid, err)
		return err
	}
	klog.Infof("%s The vm is ready. vmUUID: %s, state: %s", rctx.LogPrefix, vmUuid, *vm.Status.State)

	// Update the NutanixMachine status
	rctx.NutanixMachine.Status.VmUUID = vm.Metadata.UUID
	rctx.NutanixMachine.Status.Addresses = []capiv1.MachineAddress{}
	rctx.IP = *vm.Status.Resources.NicList[0].IPEndpointList[0].IP
	rctx.NutanixMachine.Status.Addresses = append(rctx.NutanixMachine.Status.Addresses, capiv1.MachineAddress{
		Type:    capiv1.MachineInternalIP,
		Address: rctx.IP,
	})
	rctx.NutanixMachine.Status.Addresses = append(rctx.NutanixMachine.Status.Addresses, capiv1.MachineAddress{
		Type:    capiv1.MachineHostName,
		Address: *vm.Spec.Name,
	})

	// Update the NutanixMachine Spec.ProviderID
	rctx.NutanixMachine.Spec.ProviderID = fmt.Sprintf(provideridFmt, *rctx.NutanixMachine.Status.VmUUID)
	rctx.NutanixMachine.Status.Ready = true
	klog.Infof("%s Created VM %s for cluster %s, update NutanixMachine spec.providerID to %s, and status %+v, vmUuid: %s",
		rctx.LogPrefix, rctx.NutanixMachine.Name, rctx.NutanixCluster.Name, rctx.NutanixMachine.Spec.ProviderID,
		rctx.NutanixMachine.Status, *rctx.NutanixMachine.Status.VmUUID)

	return nil
}

// findVMByUUID retrieves the VM with the given vm UUID
func findVMByUUID(rctx *nctx.MachineContext, uuid string) (*nutanixClientV3.VMIntentResponse, error) {

	klog.Infof("%s Checking if VM with UUID %s exists.", rctx.LogPrefix, uuid)

	response, err := rctx.NutanixClient.V3.GetVM(uuid)
	if err != nil {
		klog.Errorf("%s Failed to find VM by vmUUID %s. error: %v", rctx.LogPrefix, uuid, err)
		return nil, err
	}

	return response, nil
}

// findVMByName retrieves the VM with the given vm name
func findVMByName(rctx *nctx.MachineContext, vmName string) (*nutanixClientV3.VMIntentResource, error) {
	klog.Infof("%s Checking if VM with name %s exists.", rctx.LogPrefix, vmName)

	res, err := rctx.NutanixClient.V3.ListVM(&nutanixClientV3.DSMetadata{
		Filter: utils.StringPtr(fmt.Sprintf("vm_name==%s", vmName))})
	if err != nil || len(res.Entities) == 0 {
		klog.Errorf("%s Failed to find VM by name %s. error: %v", rctx.LogPrefix, vmName, err)
		return nil, fmt.Errorf("Failed to find VM by name %s. error: %v", vmName, err)
	}

	if len(res.Entities) > 1 {
		klog.Warningf("%s Found more than one (%v) vms with name %s.", rctx.LogPrefix, len(res.Entities), vmName)
	}

	return res.Entities[0], nil
}

// deleteVM deletes a VM and is invoked by the NutanixMachineReconciler
//func deleteVM(ctx context.Context, cluster *infrav1.NutanixCluster, machine *infrav1.NutanixMachine, logPrefix string) error {
func deleteVM(rctx *nctx.MachineContext) error {
	klog.Infof("Deleting VM %v for cluster %v.", rctx.NutanixMachine.Name, rctx.NutanixCluster.Name)
	var err error

	client, err := nutanixClient.Client(nutanixClient.ClientOptions{})
	if err != nil {
		return fmt.Errorf("Client Auth error: %v", err)
	}

	if rctx.NutanixMachine.Status.VmUUID == nil {
		klog.Warning(fmt.Sprintf("VmUUID not found in Status. Skipping delete"))
		return nil
	}
	uuid := utils.StringValue(rctx.NutanixMachine.Status.VmUUID)
	vmName := rctx.NutanixMachine.Name
	klog.Infof("Deleting VM %s with UUID: %s", vmName, uuid)
	_, err = client.V3.DeleteVM(uuid)
	if err != nil {
		klog.Infof("Error deleting machine %s", rctx.NutanixMachine.Name)
		return err
	}

	err = nutanixClient.WaitForGetVMDelete(client, uuid)
	if err != nil {
		klog.Errorf("VM %s failed to delete. %s", vmName, err.Error())
		// TODO find a better way to error check instead of string search comparison
		if strings.Contains(err.Error(), "does not exist") {
			klog.Infof("Successfully deleted vm %s with uuid %s", rctx.NutanixMachine.Name, uuid)
			return nil
		}

		return err
	}

	return nil
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

func getImageUUID(rctx *nctx.MachineContext) (string, error) {
	client, err := nutanixClient.Client(nutanixClient.ClientOptions{Debug: true})
	if err != nil {
		klog.Errorf("%s Failed to create the nutanix client. %v", rctx.LogPrefix, err)
		return "", fmt.Errorf("Client Auth error: %v", err)
	}
	rctx.NutanixClient = client
	machineSpec := rctx.NutanixMachine.Spec
	var foundImageUUID string
	imageUUID := machineSpec.Image.UUID
	imageName := machineSpec.Image.Name
	if imageUUID == nil && imageName == nil {
		return "", fmt.Errorf("image name or image uuid must be passed in order to retrieve the image")
	}
	if imageUUID != nil {
		imageIntentResponse, err := client.V3.GetImage(*imageUUID)
		if err != nil {
			if strings.Contains(fmt.Sprint(err), "ENTITY_NOT_FOUND") {
				return "", fmt.Errorf("failed to find image with UUID %s: %v", *imageUUID, err)
			}
		}
		foundImageUUID = *imageIntentResponse.Metadata.UUID
	} else if imageName != nil {
		responseImages, err := client.V3.ListAllImage()
		if err != nil {
			return "", err
		}
		foundImages := make([]*nutanixClientV3.ImageIntentResponse, 0)
		for _, s := range responseImages.Entities {
			imageSpec := s.Spec
			if *imageSpec.Name == *imageName {
				foundImages = append(foundImages, s)
			}
		}
		if len(foundImages) == 0 {
			return "", fmt.Errorf("failed to retrieve image by name %s", *imageName)
		} else if len(foundImages) > 1 {
			return "", fmt.Errorf("more than one image found with name %s", *imageName)
		} else {
			foundImageUUID = *foundImages[0].Metadata.UUID
		}
		if foundImageUUID == "" {
			return "", fmt.Errorf("failed to retrieve image by name or uuid. Verify input parameters.")
		}
	}
	return foundImageUUID, nil
}

func getSubnetUUID(rctx *nctx.MachineContext, peUUID string) (string, error) {
	client, err := nutanixClient.Client(nutanixClient.ClientOptions{Debug: true})
	if err != nil {
		klog.Errorf("%s Failed to create the nutanix client. %v", rctx.LogPrefix, err)
		return "", fmt.Errorf("Client Auth error: %v", err)
	}
	rctx.NutanixClient = client
	machineSpec := rctx.NutanixMachine.Spec
	var foundSubnetUUID string
	subnetUUID := machineSpec.Subnet.UUID
	subnetName := machineSpec.Subnet.Name
	if subnetUUID == nil && subnetName == nil {
		return "", fmt.Errorf("subnet name or subnet uuid must be passed in order to retrieve the subnet")
	}
	if subnetUUID != nil {
		subnetIntentResponse, err := client.V3.GetSubnet(*subnetUUID)
		if err != nil {
			if strings.Contains(fmt.Sprint(err), "ENTITY_NOT_FOUND") {
				return "", fmt.Errorf("failed to find subnet with UUID %s: %v", *subnetUUID, err)
			}
		}
		foundSubnetUUID = *subnetIntentResponse.Metadata.UUID
	} else if subnetName != nil {

		responseSubnets, err := client.V3.ListAllSubnet()
		if err != nil {
			return "", err
		}
		foundSubnets := make([]*nutanixClientV3.SubnetIntentResponse, 0)
		for _, s := range responseSubnets.Entities {
			subnetSpec := s.Spec
			if *subnetSpec.Name == *subnetName && *subnetSpec.ClusterReference.UUID == peUUID {
				foundSubnets = append(foundSubnets, s)
			}
		}
		if len(foundSubnets) == 0 {
			return "", fmt.Errorf("failed to retrieve subnet by name %s", *subnetName)
		} else if len(foundSubnets) > 1 {
			return "", fmt.Errorf("more than one subnet found with name %s", *subnetName)
		} else {
			foundSubnetUUID = *foundSubnets[0].Metadata.UUID
		}
		if foundSubnetUUID == "" {
			return "", fmt.Errorf("failed to retrieve subnet by name or uuid. Verify input parameters.")
		}
	}
	return foundSubnetUUID, nil
}

func getPEUUID(rctx *nctx.MachineContext) (string, error) {
	client, err := nutanixClient.Client(nutanixClient.ClientOptions{Debug: true})
	if err != nil {
		klog.Errorf("%s Failed to create the nutanix client. %v", rctx.LogPrefix, err)
		return "", fmt.Errorf("Client Auth error: %v", err)
	}
	rctx.NutanixClient = client
	machineSpec := rctx.NutanixMachine.Spec
	var foundPEUUID string
	peUUID := machineSpec.Cluster.UUID
	peName := machineSpec.Cluster.Name
	if peUUID == nil && peName == nil {
		return "", fmt.Errorf("cluster name or uuid must be passed in order to retrieve the pe")
	}
	if peUUID != nil {
		peIntentResponse, err := client.V3.GetCluster(*peUUID)
		if err != nil {
			if strings.Contains(fmt.Sprint(err), "ENTITY_NOT_FOUND") {
				return "", fmt.Errorf("failed to find Prism Element cluster with UUID %s: %v", *peUUID, err)
			}
		}
		foundPEUUID = *peIntentResponse.Metadata.UUID
	} else if peName != nil {

		responsePEs, err := client.V3.ListAllCluster()
		if err != nil {
			return "", err
		}
		foundPEs := make([]*nutanixClientV3.ClusterIntentResource, 0)
		for _, s := range responsePEs.Entities {
			peSpec := s.Spec
			if *peSpec.Name == *peName {
				foundPEs = append(foundPEs, s)
			}
		}
		if len(foundPEs) == 0 {
			return "", fmt.Errorf("failed to retrieve Prism Element cluster by name %s", *peName)
		} else if len(foundPEs) > 1 {
			return "", fmt.Errorf("more than one Prism Element cluster found with name %s", *peName)
		} else {
			foundPEUUID = *foundPEs[0].Metadata.UUID
		}
		if foundPEUUID == "" {
			return "", fmt.Errorf("failed to retrieve Prism Element cluster by name or uuid. Verify input parameters.")
		}
	}
	return foundPEUUID, nil
}

// GetMibValueOfQuantity returns the given quantity value in Mib
func GetMibValueOfQuantity(quantity resource.Quantity) int64 {
	return quantity.Value() / (1024 * 1024)
}

func createSystemDiskSpec(imageUUID string, systemDiskSize int64) (*nutanixClientV3.VMDisk, error) {
	if imageUUID == "" {
		return nil, fmt.Errorf("image UUID must be set when creating system disk")
	}
	if systemDiskSize <= 0 {
		return nil, fmt.Errorf("Invalid system disk size: %d. Provide in XXGi (for example 70Gi) format instead", systemDiskSize)
	}
	systemDisk := &nutanixClientV3.VMDisk{
		DataSourceReference: &nutanixClientV3.Reference{
			Kind: utils.StringPtr("image"),
			UUID: utils.StringPtr(imageUUID),
		},
		DiskSizeMib: utils.Int64Ptr(systemDiskSize)}
	return systemDisk, nil

}
