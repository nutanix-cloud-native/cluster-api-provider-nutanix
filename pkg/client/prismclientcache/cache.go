package prismclientcache

import (
	"errors"
	"sync"

	prismGoClientV3 "github.com/nutanix-cloud-native/prism-go-client/v3"
)

type clientCacheMap map[string]*prismGoClientV3.Client

var (
	// ErrorClientNotFound is returned when the client is not found in the cache
	ErrorClientNotFound = errors.New("client not found in client cache")

	// DefaultCache is the default cache of prism clients to be shared across the different controllers
	DefaultCache = newCache()
)

// ClientCache is a cache for prism clients
type ClientCache struct {
	cache clientCacheMap
	mtx   sync.RWMutex
}

// newCache returns a new ClientCache
func newCache() *ClientCache {
	return &ClientCache{
		cache: make(clientCacheMap),
		mtx:   sync.RWMutex{},
	}
}

// Get returns the client for the given cluster name
func (c *ClientCache) Get(clusterName string) (*prismGoClientV3.Client, error) {
	c.mtx.RLock()
	defer c.mtx.RUnlock()

	clnt, ok := c.cache[clusterName]
	if !ok {
		return nil, ErrorClientNotFound
	}

	return clnt, nil
}

// Set adds the client to the cache
func (c *ClientCache) Set(clusterName string, client *prismGoClientV3.Client) {
	c.mtx.Lock()
	defer c.mtx.Unlock()

	c.cache[clusterName] = client
}

// Delete removes the client from the cache
func (c *ClientCache) Delete(clusterName string) {
	c.mtx.Lock()
	defer c.mtx.Unlock()

	delete(c.cache, clusterName)
}
