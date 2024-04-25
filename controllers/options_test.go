package controllers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/client-go/util/workqueue"
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

func TestWithRateLimiter(t *testing.T) {
	tests := []struct {
		name         string
		rateLimiter  workqueue.RateLimiter
		expectError  bool
		expectedType interface{}
	}{
		{
			name:         "TestWithRateLimiterNil",
			rateLimiter:  nil,
			expectError:  true,
			expectedType: nil,
		},
		{
			name:         "TestWithRateLimiterSet",
			rateLimiter:  workqueue.DefaultControllerRateLimiter(),
			expectError:  false,
			expectedType: &workqueue.MaxOfRateLimiter{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opt := WithRateLimiter(tt.rateLimiter)
			config := &ControllerConfig{}
			err := opt(config)
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.IsType(t, tt.expectedType, config.RateLimiter)
			}
		})
	}
}
