package client

import (
	"github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	"github.com/nutanix-cloud-native/prism-go-client/environment/types"
	"github.com/nutanix-cloud-native/prism-go-client/v3"
)

// NutanixClientCache is the cache of prism clients to be shared across the different controllers
var NutanixClientCache = v3.NewClientCache(v3.WithSessionAuth(true))

// CacheParams is the struct that implements ClientCacheParams interface from prism-go-client
type CacheParams struct {
	NutanixCluster          *v1beta1.NutanixCluster
	PrismManagementEndpoint *types.ManagementEndpoint
}

func (c *CacheParams) Key() string {
	return c.NutanixCluster.GetNamespacedName()
}

func (c *CacheParams) ManagementEndpoint() types.ManagementEndpoint {
	return *c.PrismManagementEndpoint
}
