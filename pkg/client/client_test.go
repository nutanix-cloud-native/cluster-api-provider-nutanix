package client

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestClient(t *testing.T) {
	clientHelper, err := NewNutanixClientHelper(nil, nil)
	assert.NoError(t, err)
	assert.NotNil(t, clientHelper)
}
