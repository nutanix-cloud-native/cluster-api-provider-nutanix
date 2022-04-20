package context

import (
	"context"
	"errors"
	"sync"

	"k8s.io/klog/v2"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/controllers/remote"
	ctlclient "sigs.k8s.io/controller-runtime/pkg/client"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	nutanixClientV3 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/pkg/nutanix/v3"
)

var (
	RemoteClientCache = map[ctlclient.ObjectKey]ctlclient.Client{}
	cacheLock         = &sync.Mutex{}
)

// ClusterContext is a context used with a NutanixCluster reconciler
type ClusterContext struct {
	Context       context.Context
	NutanixClient *nutanixClientV3.Client

	Cluster        *capiv1.Cluster
	NutanixCluster *infrav1.NutanixCluster

	// The prefix to prepend to logs
	LogPrefix string
}

// MachineContext is a context used with a NutanixMachine reconciler
type MachineContext struct {
	Context       context.Context
	NutanixClient *nutanixClientV3.Client

	Cluster        *capiv1.Cluster
	Machine        *capiv1.Machine
	NutanixCluster *infrav1.NutanixCluster
	NutanixMachine *infrav1.NutanixMachine

	// The VM ip address
	IP string

	// The prefix to prepend to logs
	LogPrefix string
}

// IsControlPlaneMachine returns true if the provided resource is
// a member of the control plane.
func IsControlPlaneMachine(nma *infrav1.NutanixMachine) bool {
	if nma == nil {
		return false
	}
	_, ok := nma.GetLabels()[capiv1.MachineControlPlaneLabelName]
	return ok
}

// ErrNoMachineIPAddr indicates that no valid IP addresses were found in a machine context
var ErrNoMachineIPAddr = errors.New("no IP addresses found for machine")

// GetMachinePreferredIPAddress returns the preferred IP address associated with the NutanixMachine
func GetMachinePreferredIPAddress(nma *infrav1.NutanixMachine) (string, error) {
	var internalIP, externalIP string
	for _, addr := range nma.Status.Addresses {
		if addr.Type == capiv1.MachineExternalIP {
			externalIP = addr.Address
		} else if addr.Type == capiv1.MachineInternalIP {
			internalIP = addr.Address
		}
	}

	if len(externalIP) > 0 {
		return externalIP, nil
	}
	if len(internalIP) > 0 {
		return internalIP, nil
	}

	return "", ErrNoMachineIPAddr
}

// GetNutanixMachinesInCluster gets a cluster's NutanixMachine resources.
func (clctx *ClusterContext) GetNutanixMachinesInCluster(client ctlclient.Client) ([]*infrav1.NutanixMachine, error) {

	clusterName := clctx.NutanixCluster.Name
	clusterNamespace := clctx.NutanixCluster.Namespace
	labels := map[string]string{capiv1.ClusterLabelName: clusterName}
	machineList := &infrav1.NutanixMachineList{}

	err := client.List(clctx.Context, machineList,
		ctlclient.InNamespace(clusterNamespace), ctlclient.MatchingLabels(labels))
	if err != nil {
		klog.Errorf("%s Failed to list NutanixMachines. %v", clctx.LogPrefix, err)
		return nil, err
	}

	ntxMachines := make([]*infrav1.NutanixMachine, len(machineList.Items))
	for i := range machineList.Items {
		ntxMachines[i] = &machineList.Items[i]
	}

	return ntxMachines, nil
}

func GetRemoteClient(ctx context.Context, client ctlclient.Client, clusterKey ctlclient.ObjectKey) (ctlclient.Client, error) {
	cacheLock.Lock()
	defer cacheLock.Unlock()

	remoteClient, ok := RemoteClientCache[clusterKey]
	if ok {
		return remoteClient, nil
	}

	remoteClient, err := remote.NewClusterClient(ctx, "remote-cluster-cache", client, clusterKey)
	if err != nil {
		klog.Errorf("Failed to create client for remote cluster %v", clusterKey)
		return nil, err
	}
	RemoteClientCache[clusterKey] = remoteClient

	return remoteClient, nil
}

func RemoveRemoteClient(clusterKey ctlclient.ObjectKey) {
	cacheLock.Lock()
	defer cacheLock.Unlock()

	delete(RemoteClientCache, clusterKey)
}
