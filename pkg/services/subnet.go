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

package services

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	subnetModels "github.com/nutanix/ntnx-api-golang-clients/networking-go-client/v4/models/networking/v4/config"
	"k8s.io/utils/ptr"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	nctx "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/pkg/context"
	"github.com/nutanix-cloud-native/prism-go-client/converged"
	v4Converged "github.com/nutanix-cloud-native/prism-go-client/converged/v4"
)

const subnetTypeOverlay = "OVERLAY"

// subnetBelongsToCluster checks if a subnet belongs to the specified PE cluster.
// It checks both ClusterReference (single UUID) and ClusterReferenceList (list of UUIDs).
// According to the networking team, non-overlay subnets may have:
// - Both ClusterReference and ClusterReferenceList present (most cases): clusterReference will be deprecated in the future
// - Only ClusterReference present: Old AOS clusters (i.e. <=7.0) that don't support ClusterReferenceList on basic vlan subnets
// - Only ClusterReferenceList present: subnets backed by PC based vSwitches (i.e. >=7.3)
func subnetBelongsToCluster(subnet *subnetModels.Subnet, peUUID string) bool {
	// Check ClusterReference field
	if subnet.ClusterReference != nil && *subnet.ClusterReference == peUUID {
		return true
	}

	// Check ClusterReferenceList field
	if subnet.ClusterReferenceList != nil && slices.Contains(subnet.ClusterReferenceList, peUUID) {
		return true
	}

	return false
}

// isSubnetUsableForProject returns true if the subnet is accessible from the
// given project, i.e. it is either owned by the project or explicitly shared
// with it. PC < 7.6 has no concept of project ownership / sharing for subnets,
// so callers must gate this check on the PC version before invoking it.
func isSubnetUsableForProject(subnet *subnetModels.Subnet, projectExtID string) bool {
	if subnet == nil {
		return false
	}
	// Directly owned by the project.
	if subnet.ProjectExtId != nil && *subnet.ProjectExtId == projectExtID {
		return true
	}
	// Explicitly shared with the project.
	return slices.Contains(subnet.SharedWithProjects, projectExtID)
}

// subnetReachableFromPE returns true if the subnet is usable from the given
// Prism Element: overlay subnets are reachable from any PE, while VLAN subnets
// must belong to the PE cluster.
func subnetReachableFromPE(subnet *subnetModels.Subnet, peUUID string) bool {
	if subnet.SubnetType != nil && subnet.SubnetType.GetName() == subnetTypeOverlay {
		return true
	}
	return subnetBelongsToCluster(subnet, peUUID)
}

// GetSubnet returns the subnet with the given name or UUID, scoped
// to the PE identified by peUUID.
//
// When project is non-nil and the PC version supports projects (>= 7.6), the result
// is additionally scoped to the given project: a UUID lookup must resolve to a subnet
// owned by or shared with the project, and a name lookup retains only subnets accessible
// from the project, preferring a project-owned subnet over a shared one of the same name
// (so the presence of both is not treated as ambiguous). On PC < 7.6 (or when project is
// nil) the legacy, unscoped behavior is preserved.
//
//nolint:gocognit // project-aware lookup requires branching on UUID/name and ownership/shared state
func GetSubnet(ctx context.Context, client *v4Converged.Client, peUUID string, subnetName, subnetUUID *string, project *nctx.ProjectInfo, pcVersion string) (*subnetModels.Subnet, error) {
	if subnetUUID == nil && subnetName == nil {
		return nil, fmt.Errorf("subnet name or subnet uuid must be passed in order to retrieve the subnet")
	}

	var projectExtID *string
	if project != nil {
		projectExtID = project.ExtID
	}
	// projectAware indicates project scoping should be enforced. On PC < 7.6 the
	// concept of project ownership / shared-with-projects does not exist, so we
	// fall back to the legacy (unscoped) lookup behavior.
	projectAware := IsPCVersionHigherThan75(pcVersion) && projectExtID != nil

	switch {
	case subnetUUID != nil:
		subnetResp, err := client.Subnets.Get(ctx, *subnetUUID)
		if err != nil {
			if converged.IsNotFound(err) {
				return nil, fmt.Errorf("failed to find subnet with UUID %s: %w", *subnetUUID, err)
			}
			return nil, fmt.Errorf("failed to get subnet with UUID %s: %w", *subnetUUID, err)
		}
		if projectAware && !isSubnetUsableForProject(subnetResp, *projectExtID) {
			return nil, &terminalError{message: fmt.Sprintf(
				"subnet with UUID %s is not accessible in project %s", *subnetUUID, *project.Name)}
		}
		return subnetResp, nil

	default: // search by name
		// Not using additional filtering since we want to list overlay and vlan subnets
		responseSubnets, err := client.Subnets.List(ctx, converged.WithFilter(fmt.Sprintf("name eq '%s'", *subnetName)))
		if err != nil {
			return nil, err
		}

		if !projectAware {
			// Legacy behavior: exact name match + PE/overlay scoping, must resolve to a single subnet.
			foundSubnets := make([]subnetModels.Subnet, 0)
			for i := range responseSubnets {
				subnet := &responseSubnets[i]
				if subnet.Name == nil || subnet.SubnetType == nil || !strings.EqualFold(*subnet.Name, *subnetName) {
					continue
				}
				if subnetReachableFromPE(subnet, peUUID) {
					foundSubnets = append(foundSubnets, *subnet)
				}
			}
			switch len(foundSubnets) {
			case 0:
				return nil, &terminalError{message: fmt.Sprintf("failed to retrieve subnet by name %s", *subnetName)}
			case 1:
				return &foundSubnets[0], nil
			default:
				return nil, fmt.Errorf("more than one subnet found with name %s", *subnetName)
			}
		}

		// Project-aware: a subnet must be PE-scoped (or overlay) and usable by the
		// project; partition the usable name matches into subnets owned by this
		// project versus subnets merely shared with it.
		var owned, shared []subnetModels.Subnet
		for i := range responseSubnets {
			subnet := &responseSubnets[i]
			if subnet.Name == nil || subnet.SubnetType == nil || !strings.EqualFold(*subnet.Name, *subnetName) {
				continue
			}
			if !subnetReachableFromPE(subnet, peUUID) {
				continue
			}
			if !isSubnetUsableForProject(subnet, *projectExtID) {
				continue
			}
			if subnet.ProjectExtId != nil && *subnet.ProjectExtId == *projectExtID {
				owned = append(owned, *subnet)
			} else {
				shared = append(shared, *subnet)
			}
		}

		// Prefer a project-owned subnet over a shared one of the same name.
		switch {
		case len(owned) == 1:
			return &owned[0], nil
		case len(owned) > 1:
			return nil, fmt.Errorf(
				"more than one subnet found with name %s owned by project %s", *subnetName, *project.Name)
		case len(shared) == 1:
			return &shared[0], nil
		case len(shared) > 1:
			return nil, fmt.Errorf(
				"more than one subnet found with name %s shared with project %s and none owned by it",
				*subnetName, *project.Name)
		default:
			return nil, &terminalError{message: fmt.Sprintf(
				"failed to retrieve subnet by name %s in project %s", *subnetName, *project.Name)}
		}
	}
}

// GetSubnetUUID returns the UUID of the subnet with the given name or UUID. See GetSubnet for
// the PE and project scoping rules.
func GetSubnetUUID(ctx context.Context, client *v4Converged.Client, peUUID string, subnetName, subnetUUID *string, project *nctx.ProjectInfo, pcVersion string) (string, error) {
	subnet, err := GetSubnet(ctx, client, peUUID, subnetName, subnetUUID, project, pcVersion)
	if err != nil {
		return "", err
	}
	foundSubnetUUID := ptr.Deref(subnet.ExtId, "")
	if foundSubnetUUID == "" {
		return "", fmt.Errorf("failed to retrieve subnet by name or uuid. Verify input parameters")
	}
	return foundSubnetUUID, nil
}

// subnetNetworkKey identifies a subnet by network layer, VLAN ID/VNI and CIDR.
// Metro sites use distinct Prism subnet objects (names/UUIDs) that must share this key.
func subnetNetworkKey(s *subnetModels.Subnet) string {
	if s == nil {
		return "UNKNOWN||"
	}
	layer := "UNKNOWN"
	if s.SubnetType != nil {
		layer = s.SubnetType.GetName()
	}
	vlanID := ""
	if s.NetworkId != nil {
		vlanID = strconv.Itoa(*s.NetworkId)
	}
	cidr := ptr.Deref(s.IpPrefix, "")
	return fmt.Sprintf("%s|%s|%s", layer, vlanID, cidr)
}

// SubnetNetworkKeys resolves each identifier against pe and returns the matching network keys.
func SubnetNetworkKeys(
	ctx context.Context,
	client *v4Converged.Client,
	ids []infrav1.NutanixResourceIdentifier,
	pe infrav1.NutanixResourceIdentifier,
) ([]string, error) {
	if client == nil {
		return nil, fmt.Errorf("cannot retrieve subnet network keys if nutanix client is nil")
	}
	peUUID, err := GetPEUUID(ctx, client, nil, pe.Name, pe.UUID)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(ids))
	for i := range ids {
		// Metro/MetroSite placement is not project scoped, so resolve without a project.
		subnet, err := GetSubnet(ctx, client, peUUID, ids[i].Name, ids[i].UUID, nil, "")
		if err != nil {
			return nil, fmt.Errorf("failed to resolve subnet %s: %w", ids[i].DisplayString(), err)
		}
		keys = append(keys, subnetNetworkKey(subnet))
	}
	return keys, nil
}

func stringSliceSetEquals(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	remaining := make(map[string]int, len(b))
	for _, s := range b {
		remaining[s]++
	}
	for _, s := range a {
		if remaining[s] == 0 {
			return false
		}
		remaining[s]--
	}
	return true
}

// MetroSubnetsMatch reports whether machine and failure-domain subnet identifiers
// describe the same network (layer, VLAN ID/VNI, CIDR), even when Prism names differ.
func MetroSubnetsMatch(
	ctx context.Context,
	client *v4Converged.Client,
	machineSubnets, fdSubnets []infrav1.NutanixResourceIdentifier,
	machinePE, fdPE infrav1.NutanixResourceIdentifier,
) (bool, []string, []string, error) {
	machineKeys, err := SubnetNetworkKeys(ctx, client, machineSubnets, machinePE)
	if err != nil {
		return false, nil, nil, fmt.Errorf("failed to resolve NutanixMachine subnet network keys: %w", err)
	}
	fdKeys, err := SubnetNetworkKeys(ctx, client, fdSubnets, fdPE)
	if err != nil {
		return false, nil, nil, fmt.Errorf("failed to resolve NutanixFailureDomain subnet network keys: %w", err)
	}
	return stringSliceSetEquals(machineKeys, fdKeys), machineKeys, fdKeys, nil
}

// GetSubnetUUIDList returns a list of subnet UUIDs for the given list of subnet names,
// scoped to the PE identified by peUUID and (on PC 7.6+ with a non-nil project) to the
// given project.
func GetSubnetUUIDList(ctx context.Context, client *v4Converged.Client, machineSubnets []infrav1.NutanixResourceIdentifier, peUUID string, project *nctx.ProjectInfo, pcVersion string) ([]string, error) {
	subnetUUIDs := make([]string, 0)
	for _, machineSubnet := range machineSubnets {
		subnetUUID, err := GetSubnetUUID(
			ctx,
			client,
			peUUID,
			machineSubnet.Name,
			machineSubnet.UUID,
			project,
			pcVersion,
		)
		if err != nil {
			return subnetUUIDs, err
		}
		subnetUUIDs = append(subnetUUIDs, subnetUUID)
	}
	return subnetUUIDs, nil
}
