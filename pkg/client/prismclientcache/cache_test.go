package prismclientcache

import (
	"testing"

	prismGoClientV3 "github.com/nutanix-cloud-native/prism-go-client/v3"
	"github.com/stretchr/testify/assert"
)

func TestGetReturnsClientIfPresentInCache(t *testing.T) {
	cache := newCache()
	client := &prismGoClientV3.Client{}
	cache.Set("cluster1", client)

	returnedClient, err := cache.Get("cluster1")

	assert.NoError(t, err)
	assert.Equal(t, client, returnedClient)
}

func TestGetReturnsErrorIfClientNotPresentInCache(t *testing.T) {
	cache := newCache()

	_, err := cache.Get("cluster1")

	assert.ErrorIs(t, err, ErrorClientNotFound)
}

func TestAddAddsClientToCache(t *testing.T) {
	cache := newCache()
	client := &prismGoClientV3.Client{}

	cache.Set("cluster1", client)

	returnedClient, err := cache.Get("cluster1")

	assert.NoError(t, err)
	assert.Equal(t, client, returnedClient)
}

func TestAddOverwritesExistingClientInCache(t *testing.T) {
	cache := newCache()
	client1 := &prismGoClientV3.Client{}
	client2 := &prismGoClientV3.Client{}

	cache.Set("cluster1", client1)
	cache.Set("cluster1", client2)

	returnedClient, err := cache.Get("cluster1")

	assert.NoError(t, err)
	assert.Equal(t, client2, returnedClient)
}

func TestDeleteRemovesClientFromCache(t *testing.T) {
	cache := newCache()
	client := &prismGoClientV3.Client{}
	cache.Set("cluster1", client)

	cache.Delete("cluster1")

	_, err := cache.Get("cluster1")

	assert.ErrorIs(t, err, ErrorClientNotFound)
}

func TestDeleteDoesNotErrorIfClientNotPresentInCache(t *testing.T) {
	cache := newCache()

	cache.Delete("cluster1") // No error expected
}
