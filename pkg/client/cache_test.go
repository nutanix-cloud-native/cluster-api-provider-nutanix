package client

import (
	"net/url"
	"testing"

	v4Converged "github.com/nutanix-cloud-native/prism-go-client/converged/v4"
	"github.com/nutanix-cloud-native/prism-go-client/environment/types"
	v3 "github.com/nutanix-cloud-native/prism-go-client/v3"
	v4 "github.com/nutanix-cloud-native/prism-go-client/v4"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
)

func TestCacheParamsKey(t *testing.T) {
	cluster := &v1beta1.NutanixCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-namespace",
		},
	}

	params := CacheParams{
		NutanixCluster: cluster,
	}

	expectedKey := "test-namespace/test-cluster"
	assert.Equal(t, expectedKey, params.Key())
}

func TestCacheParamsManagementEndpoint(t *testing.T) {
	endpoint := &types.ManagementEndpoint{
		Address: &url.URL{
			Scheme: "https",
			Host:   "prismcentral.nutanix.com:9440",
		},
	}

	params := &CacheParams{
		PrismManagementEndpoint: endpoint,
	}

	assert.Equal(t, *endpoint, params.ManagementEndpoint())
}

func TestNutanixClientCache(t *testing.T) {
	assert.NotNil(t, NutanixClientCache)
	assert.IsType(t, &v3.ClientCache{}, NutanixClientCache)
}

func TestNutanixClientCacheV4(t *testing.T) {
	assert.NotNil(t, NutanixClientCacheV4)
	assert.IsType(t, &v4.ClientCache{}, NutanixClientCacheV4)
}

func TestNutanixConvergedClientV4Cache(t *testing.T) {
	assert.NotNil(t, NutanixConvergedClientV4Cache)
	assert.IsType(t, &v4Converged.ClientCache{}, NutanixConvergedClientV4Cache)
}
