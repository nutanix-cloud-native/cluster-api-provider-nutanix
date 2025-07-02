package controllers

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"golang.org/x/time/rate"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// WithSkipNameValidationForTesting returns ControllerConfigOpts that enables
// SkipNameValidation for unit tests
func WithSkipNameValidationForTesting() ControllerConfigOpts {
	return WithSkipNameValidation(true)
}

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
		rateLimiter  workqueue.TypedRateLimiter[reconcile.Request]
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
			rateLimiter:  workqueue.NewTypedMaxOfRateLimiter(workqueue.NewTypedItemExponentialFailureRateLimiter[reconcile.Request](1*time.Millisecond, 1000*time.Second), &workqueue.TypedBucketRateLimiter[reconcile.Request]{Limiter: rate.NewLimiter(rate.Limit(10), 100)}),
			expectError:  false,
			expectedType: &workqueue.TypedMaxOfRateLimiter[reconcile.Request]{},
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

func TestWithSkipNameValidation(t *testing.T) {
	tests := []struct {
		name               string
		skipNameValidation bool
		expectedValue      bool
	}{
		{
			name:               "TestWithSkipNameValidationTrue",
			skipNameValidation: true,
			expectedValue:      true,
		},
		{
			name:               "TestWithSkipNameValidationFalse",
			skipNameValidation: false,
			expectedValue:      false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opt := WithSkipNameValidation(tt.skipNameValidation)
			config := &ControllerConfig{}
			err := opt(config)
			assert.NoError(t, err)
			assert.Equal(t, tt.expectedValue, config.SkipNameValidation)
		})
	}
}
