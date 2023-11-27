package infrastructure

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWithMaxConcurrentReconciles(t *testing.T) {
	tests := []struct {
		name                    string
		MaxConcurrentReconciles int
		expectError             bool
	}{
		{
			name:                    "TestWithMaxConcurrentReconcilesSetTo0",
			MaxConcurrentReconciles: 0,
			expectError:             true,
		},
		{
			name:                    "TestWithMaxConcurrentReconcilesSetTo10",
			MaxConcurrentReconciles: 10,
			expectError:             false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opt := WithMaxConcurrentReconciles(tt.MaxConcurrentReconciles)
			config := &ControllerConfig{}
			err := opt(config)
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.MaxConcurrentReconciles, config.MaxConcurrentReconciles)
			}
		})
	}
}
