package client

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClient(t *testing.T) {
	clientHelper, err := NewNutanixClientHelper(nil, nil)
	assert.NoError(t, err)
	assert.NotNil(t, clientHelper)
}
