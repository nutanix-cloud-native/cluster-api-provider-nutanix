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
	"strings"

	clusterModels "github.com/nutanix/ntnx-api-golang-clients/clustermgmt-go-client/v4/models/clustermgmt/v4/config"
	multidomainModels "github.com/nutanix/ntnx-api-golang-clients/multidomain-go-client/v4/models/multidomain/v4/config"
	"k8s.io/utils/ptr"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	"github.com/nutanix-cloud-native/prism-go-client/converged"
	v4Converged "github.com/nutanix-cloud-native/prism-go-client/converged/v4"
)

// GetPEUUID returns the UUID of the Prism Element cluster with the given name or UUID.
//
// When rg is non-nil, the lookup is constrained to the project's resource group
// (its placement targets); a PE that is not part of placement targets of the resource group is
// treated as not authorized for the project. When rg is nil (default-project
// path), it falls back to cluster-wide client.Clusters.* lookups.
func GetPEUUID(ctx context.Context, client *v4Converged.Client, rg *multidomainModels.ResourceGroup, peName, peUUID *string) (string, error) {
	if client == nil {
		return "", fmt.Errorf("cannot retrieve Prism Element UUID if nutanix client is nil")
	}
	if peUUID == nil && peName == nil {
		return "", fmt.Errorf("cluster name or uuid must be passed in order to retrieve the Prism Element UUID")
	}

	// Project-scoped path: resolve against the resource group's placement targets.
	if rg != nil {
		return resolvePEFromResourceGroup(ctx, client, rg, peName, peUUID)
	}

	peCluster, err := GetPEClusterByIdentifier(ctx, client, peName, peUUID)
	if err != nil {
		return "", err
	}
	return ptr.Deref(peCluster.ExtId, ""), nil
}

// GetPECluster returns the Prism Element cluster with the given UUID.
func GetPECluster(ctx context.Context, client *v4Converged.Client, peUUID string) (*clusterModels.Cluster, error) {
	peCluster, err := client.Clusters.Get(ctx, peUUID)
	if err != nil {
		if converged.IsNotFound(err) {
			return nil, fmt.Errorf("failed to find Prism Element cluster with UUID %s: %w", peUUID, err)
		}
		return nil, fmt.Errorf("failed to get Prism Element cluster with UUID %s: %w", peUUID, err)
	}

	return peCluster, nil
}

// GetPEClusterByIdentifier resolves a Prism Element cluster by UUID or name and returns the full
// cluster object in a single API call. Callers that only need the UUID can use GetPEUUID; callers
// that need the cluster object (for example to read Config.IsAvailable) should prefer this over
// GetPEUUID followed by GetPECluster, which performs a redundant List+Get round trip.
func GetPEClusterByIdentifier(ctx context.Context, client *v4Converged.Client, peName, peUUID *string) (*clusterModels.Cluster, error) {
	if client == nil {
		return nil, fmt.Errorf("cannot retrieve Prism Element cluster if nutanix client is nil")
	}
	if peUUID != nil && *peUUID != "" {
		return GetPECluster(ctx, client, *peUUID)
	}
	if peName != nil && *peName != "" {
		responsePEs, err := client.Clusters.List(ctx, converged.WithFilter(fmt.Sprintf("name eq '%s'", *peName)))
		if err != nil {
			return nil, err
		}
		foundPEs := make([]clusterModels.Cluster, 0)
		for _, s := range responsePEs {
			if strings.EqualFold(*s.Name, *peName) && hasPEClusterServiceEnabled(&s) {
				foundPEs = append(foundPEs, s)
			}
		}
		switch len(foundPEs) {
		case 1:
			return &foundPEs[0], nil
		case 0:
			return nil, &terminalError{message: fmt.Sprintf("failed to retrieve Prism Element cluster by name %s", *peName)}
		default:
			return nil, fmt.Errorf("more than one Prism Element cluster found with name %s", *peName)
		}
	}
	return nil, fmt.Errorf("failed to retrieve Prism Element cluster by name or uuid. Verify input parameters")
}

// IsPEAvailable returns whether the Prism Element cluster with the given UUID is currently available.
func IsPEAvailable(ctx context.Context, client *v4Converged.Client, peUUID string) (bool, error) {
	pe, err := GetPECluster(ctx, client, peUUID)
	if err != nil {
		return false, err
	}
	if pe.Config == nil || pe.Config.IsAvailable == nil {
		return false, nil
	}
	return *pe.Config.IsAvailable, nil
}

// resolvePEFromResourceGroup resolves a PE identifier (name or UUID) against the
// Prism Elements reachable from the project's resource group. It relies on the
// prism-go-client ResourceGroups.ListPrismElements helper to extract the PEs
// from the resource group's placement targets.
func resolvePEFromResourceGroup(ctx context.Context, client *v4Converged.Client, rg *multidomainModels.ResourceGroup, peName, peUUID *string) (string, error) {
	if rg.ExtId == nil {
		return "", fmt.Errorf("resource group has no ExtId; cannot resolve Prism Element")
	}

	prismElements, err := client.ResourceGroups.ListPrismElements(ctx, *rg.ExtId)
	if err != nil {
		return "", fmt.Errorf("failed to list Prism Elements for resource group %s: %w", *rg.ExtId, err)
	}

	if peUUID != nil && *peUUID != "" {
		for _, pe := range prismElements {
			if pe.ExtId == *peUUID {
				return *peUUID, nil
			}
		}
		return "", &terminalError{message: fmt.Sprintf(
			"Prism Element cluster with UUID %s is not authorized for this project (not in resource group)", *peUUID)}
	}

	matches := make([]string, 0)
	for _, pe := range prismElements {
		if strings.EqualFold(pe.Name, *peName) {
			matches = append(matches, pe.ExtId)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return "", &terminalError{message: fmt.Sprintf(
			"failed to retrieve Prism Element cluster by name %s in this project's resource group", *peName)}
	default:
		return "", fmt.Errorf("more than one Prism Element cluster found with name %s in this project's resource group", *peName)
	}
}

func hasPEClusterServiceEnabled(peCluster *clusterModels.Cluster) bool {
	if peCluster.Config == nil ||
		peCluster.Config.ClusterFunction == nil {
		return false
	}
	serviceList := peCluster.Config.ClusterFunction
	for _, s := range serviceList {
		if strings.ToUpper(string(s.GetName())) == clusterModels.CLUSTERFUNCTIONREF_AOS.GetName() {
			return true
		}
	}
	return false
}

// GetStorageContainerInCluster resolves a storage container in the given Prism Element.
//
// When rg is non-nil, the lookup is constrained to the project's resource group
// (its placement targets); a storage container that is not reachable from the
// resource group is treated as not authorized for the project. When rg is nil
// (default-project path), it falls back to a cluster-wide client.StorageContainers.List.
func GetStorageContainerInCluster(ctx context.Context, client *v4Converged.Client, rg *multidomainModels.ResourceGroup, storageContainerIdentifier, clusterIdentifier infrav1.NutanixResourceIdentifier) (*clusterModels.StorageContainer, error) {
	// Project-scoped path: resolve against the resource group's placement targets.
	if rg != nil {
		return resolveStorageContainerFromResourceGroup(ctx, client, rg, storageContainerIdentifier, clusterIdentifier)
	}

	var filter, identifier string
	switch {
	case storageContainerIdentifier.IsUUID():
		identifier = *storageContainerIdentifier.UUID
		filter = fmt.Sprintf("containerExtId eq '%s'", identifier)
	case storageContainerIdentifier.IsName():
		identifier = *storageContainerIdentifier.Name
		filter = fmt.Sprintf("name eq '%s'", identifier)
	default:
		return nil, fmt.Errorf("storage container identifier is missing both name and uuid")
	}

	switch {
	case clusterIdentifier.IsUUID():
		filter = fmt.Sprintf("%s and clusterExtId eq '%s'", filter, *clusterIdentifier.UUID)
	case clusterIdentifier.IsName():
		filter = fmt.Sprintf("%s and clusterName eq '%s'", filter, *clusterIdentifier.Name)
	default:
		return nil, fmt.Errorf("cluster identifier is missing both name and uuid")
	}

	storageContainers, err := client.StorageContainers.List(ctx, converged.WithFilter(filter))
	if err != nil {
		return nil, err
	}

	if len(storageContainers) == 0 {
		return nil, &terminalError{message: fmt.Sprintf("found no storage container using filter: %s", filter)}
	}

	return &storageContainers[0], nil
}

// resolveStorageContainerFromResourceGroup resolves a storage container part of the project's resource group
// (its placement targets) constrained to the given Prism Element. It uses
// the prism-go-client ResourceGroups.ListStorageContainers helper to enumerate the
// storage containers (and their owning Prism Element) from the resource group's
// placement targets.
func resolveStorageContainerFromResourceGroup(ctx context.Context, client *v4Converged.Client, rg *multidomainModels.ResourceGroup, sc, peID infrav1.NutanixResourceIdentifier) (*clusterModels.StorageContainer, error) {
	if rg.ExtId == nil {
		return nil, fmt.Errorf("resource group has no ExtId; cannot resolve storage container")
	}

	storageContainers, err := client.ResourceGroups.ListStorageContainers(ctx, *rg.ExtId)
	if err != nil {
		return nil, fmt.Errorf("failed to list storage containers for resource group %s: %w", *rg.ExtId, err)
	}

	for i := range storageContainers {
		scInfo := &storageContainers[i]
		// Constrain to the requested Prism Element.
		if !prismElementInfoMatches(scInfo.PrismElement, peID) {
			continue
		}
		if storageContainerInfoMatches(scInfo, sc) {
			return &clusterModels.StorageContainer{
				ContainerExtId: ptr.To(scInfo.ExtId),
				Name:           ptr.To(scInfo.Name),
				ClusterExtId:   ptr.To(scInfo.PrismElement.ExtId),
				ClusterName:    ptr.To(scInfo.PrismElement.Name),
			}, nil
		}
	}

	// Resolve the Prism Element name from the resource group's storage container
	// listing for a clearer error; fall back to the identifier only defensively.
	peName := peID.String()
	for i := range storageContainers {
		pe := storageContainers[i].PrismElement
		if prismElementInfoMatches(pe, peID) && pe.Name != "" {
			peName = pe.Name
			break
		}
	}

	return nil, &terminalError{message: fmt.Sprintf(
		"no storage container %s found in resource group which is associated with prism element %s", sc.String(), peName)}
}

// prismElementInfoMatches reports whether the resource-group Prism Element matches
// the given PE identifier (by UUID or name).
func prismElementInfoMatches(pe converged.PrismElementInfo, peID infrav1.NutanixResourceIdentifier) bool {
	switch {
	case peID.IsUUID():
		return pe.ExtId == *peID.UUID
	case peID.IsName():
		return strings.EqualFold(pe.Name, *peID.Name)
	default:
		return false
	}
}

// storageContainerInfoMatches reports whether the resource-group storage container
// matches the given storage container identifier (by UUID or name).
func storageContainerInfoMatches(scInfo *converged.StorageContainerInfo, sc infrav1.NutanixResourceIdentifier) bool {
	switch {
	case sc.IsUUID():
		return scInfo.ExtId == *sc.UUID
	case sc.IsName():
		return strings.EqualFold(scInfo.Name, *sc.Name)
	default:
		return false
	}
}
