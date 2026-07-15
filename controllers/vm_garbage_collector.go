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
	"slices"
	"strings"
	"time"

	"github.com/nutanix-cloud-native/prism-go-client/converged"
	v4Converged "github.com/nutanix-cloud-native/prism-go-client/converged/v4"
	vmmconfig "github.com/nutanix/ntnx-api-golang-clients/vmm-go-client/v4/models/vmm/v4/ahv/config"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/utils/ptr"
	capiv1beta2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
)

const (
	// orphanedVMGCGracePeriod is the minimum age a candidate VM must have before the orphaned-VM
	// garbage collector will delete it. It guards against racing with in-flight VM provisioning
	// (a freshly created VM whose owning NutanixMachine has not yet recorded its UUID).
	orphanedVMGCGracePeriod = 15 * time.Minute

	// orphanedVMGCInterval is how often the NutanixCluster reconciler re-enqueues itself so the
	// orphaned-VM garbage collector runs. Orphans can appear from events that do not touch the
	// NutanixCluster object (e.g. a Prism Element rejoining Prism Central after a site failover),
	// so a periodic resync is what drives their cleanup.
	orphanedVMGCInterval = 10 * time.Minute
)

// garbageCollectOrphanedVMs deletes CAPX-provisioned VMs of the given cluster that are no longer
// tracked by any NutanixMachine.
//
// Background (ENG-945088): in a metro/vHA site-failover, a manually powered-off worker VM is replaced
// by Cluster API and its NutanixMachine is deleted while the peer Prism Element is disconnected. The
// delete only reaches the reachable PE; the stale copy frozen on the disconnected PE resurfaces when
// that PE rejoins Prism Central. Because the owning NutanixMachine (and its finalizer) is already
// gone, nothing reconciles the resurfaced VM and it leaks. This GC reaps such orphans. The same
// mechanism also cleans up VMs leaked by any other path that leaves a CAPX VM without an owning
// NutanixMachine, so it is cluster-scoped rather than metro-specific.
//
// It is deliberately conservative: a VM is only deleted when it (1) carries this cluster's ownership
// category, (2) carries the CAPX providerID custom attribute (i.e. CAPX created it), (3) is older
// than the grace period, and (4) matches no live NutanixMachine/Machine by UUID or name.
func garbageCollectOrphanedVMs(
	ctx context.Context,
	k8sClient client.Client,
	convergedClient *v4Converged.Client,
	clusterName, namespace string,
) error {
	log := ctrl.LoggerFrom(ctx)

	// Resolve the cluster-ownership category (KubernetesClusterName=<clusterName>) that CAPX stamps
	// on every VM it creates. If it is absent, CAPX has not created any VM for this cluster yet.
	category, err := getCategory(ctx, convergedClient, infrav1.DefaultCAPICategoryKeyForName, clusterName)
	if err != nil {
		return fmt.Errorf("failed to resolve cluster category %s=%s: %w", infrav1.DefaultCAPICategoryKeyForName, clusterName, err)
	}
	if category == nil || category.ExtId == nil {
		log.V(1).Info("cluster ownership category not found; skipping orphaned VM GC", "cluster", clusterName)
		return nil
	}
	categoryExtId := *category.ExtId

	// List all VMs requesting only the fields the GC needs and match the cluster ownership
	// category client-side in isOrphanedCAPXVM.
	vms, err := convergedClient.VMs.List(ctx,
		converged.WithSelect("extId,name,customAttributes,createTime,categories/extId"))
	if err != nil {
		return fmt.Errorf("failed to list VMs for cluster %s: %w", clusterName, err)
	}
	if len(vms) == 0 {
		return nil
	}

	// Build the set of VM UUIDs and names still owned by a live NutanixMachine/Machine in this cluster.
	expectedUUIDs, expectedNames, err := managedVMIdentifiers(ctx, k8sClient, clusterName, namespace)
	if err != nil {
		return err
	}

	var errs []error
	for i := range vms {
		vm := &vms[i]
		if !isOrphanedCAPXVM(vm, categoryExtId, expectedUUIDs, expectedNames) {
			continue
		}
		vmName := ptr.Deref(vm.Name, "")
		vmUUID := ptr.Deref(vm.ExtId, "")
		log.Info("Deleting orphaned CAPX VM not tracked by any NutanixMachine",
			"cluster", clusterName, "vmName", vmName, "vmUUID", vmUUID)
		if _, delErr := DeleteVM(ctx, convergedClient, vmName, vmUUID); delErr != nil {
			errs = append(errs, fmt.Errorf("failed to delete orphaned VM %s (%s): %w", vmName, vmUUID, delErr))
		}
	}
	return kerrors.NewAggregate(errs)
}

// managedVMIdentifiers returns the set of VM UUIDs and VM names that are still owned by a live
// NutanixMachine/Machine of the given cluster. VM names are collected from both the NutanixMachine
// (legacy naming) and its owning CAPI Machine (current naming), matching how VMs are located
// elsewhere.
func managedVMIdentifiers(
	ctx context.Context,
	k8sClient client.Client,
	clusterName, namespace string,
) (map[string]struct{}, map[string]struct{}, error) {
	uuids := map[string]struct{}{}
	names := map[string]struct{}{}

	listOpts := []client.ListOption{
		client.InNamespace(namespace),
		client.MatchingLabels{capiv1beta2.ClusterNameLabel: clusterName},
	}

	nmList := &infrav1.NutanixMachineList{}
	if err := k8sClient.List(ctx, nmList, listOpts...); err != nil {
		return nil, nil, fmt.Errorf("failed to list NutanixMachines for cluster %s: %w", clusterName, err)
	}
	for i := range nmList.Items {
		nm := &nmList.Items[i]
		if nm.Status.VmUUID != "" {
			uuids[nm.Status.VmUUID] = struct{}{}
		}
		if nm.Spec.ProviderID != "" {
			uuids[strings.TrimPrefix(nm.Spec.ProviderID, providerIdPrefix)] = struct{}{}
		}
		names[nm.Name] = struct{}{}
	}

	mList := &capiv1beta2.MachineList{}
	if err := k8sClient.List(ctx, mList, listOpts...); err != nil {
		return nil, nil, fmt.Errorf("failed to list Machines for cluster %s: %w", clusterName, err)
	}
	for i := range mList.Items {
		names[mList.Items[i].Name] = struct{}{}
	}

	return uuids, names, nil
}

// isOrphanedCAPXVM reports whether vm is a CAPX-provisioned VM of this cluster that is no longer
// tracked by any live NutanixMachine/Machine and is therefore safe to garbage-collect.
func isOrphanedCAPXVM(vm *vmmconfig.Vm, categoryExtId string, expectedUUIDs, expectedNames map[string]struct{}) bool {
	// The cluster ownership category is the primary ownership signal.
	if !slices.ContainsFunc(vm.Categories, func(c vmmconfig.CategoryReference) bool {
		return c.ExtId != nil && *c.ExtId == categoryExtId
	}) {
		return false
	}

	// Only reap VMs CAPX itself provisioned (they carry the providerID custom attribute).
	if !slices.ContainsFunc(vm.CustomAttributes, func(attr string) bool {
		return strings.HasPrefix(attr, vmCustomAttributePrefix4ProviderID)
	}) {
		return false
	}

	// Avoid racing with in-flight provisioning: skip VMs younger than the grace period.
	if vm.CreateTime != nil && time.Since(*vm.CreateTime) < orphanedVMGCGracePeriod {
		return false
	}

	// Still managed if its UUID or name maps to a live NutanixMachine/Machine.
	if vm.ExtId != nil {
		if _, ok := expectedUUIDs[*vm.ExtId]; ok {
			return false
		}
	}
	if vm.Name != nil {
		if _, ok := expectedNames[*vm.Name]; ok {
			return false
		}
	}

	return true
}
