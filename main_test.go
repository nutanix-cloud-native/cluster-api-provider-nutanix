package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRateLimiter(t *testing.T) {
	tests := []struct {
		name        string
		baseDelay   time.Duration
		maxDelay    time.Duration
		maxBurst    int
		qps         int
		expectedErr string
	}{
		{
			name:      "valid rate limiter",
			baseDelay: 500 * time.Millisecond,
			maxDelay:  15 * time.Minute,
			maxBurst:  100,
			qps:       10,
		},
		{
			name:        "negative base delay",
			baseDelay:   -500 * time.Millisecond,
			maxDelay:    15 * time.Minute,
			maxBurst:    100,
			qps:         10,
			expectedErr: "baseDelay cannot be negative",
		},
		{
			name:        "negative max delay",
			baseDelay:   500 * time.Millisecond,
			maxDelay:    -15 * time.Minute,
			maxBurst:    100,
			qps:         10,
			expectedErr: "maxDelay cannot be negative",
		},
		{
			name:        "maxDelay should be greater than or equal to baseDelay",
			baseDelay:   500 * time.Millisecond,
			maxDelay:    400 * time.Millisecond,
			maxBurst:    100,
			qps:         10,
			expectedErr: "maxDelay should be greater than or equal to baseDelay",
		},
		{
			name:        "bucketSize must be positive",
			baseDelay:   500 * time.Millisecond,
			maxDelay:    15 * time.Minute,
			maxBurst:    0,
			qps:         10,
			expectedErr: "bucketSize must be positive",
		},
		{
			name:        "qps must be positive",
			baseDelay:   500 * time.Millisecond,
			maxDelay:    15 * time.Minute,
			maxBurst:    100,
			qps:         0,
			expectedErr: "minimum QPS must be positive",
		},
		{
			name:        "bucketSize must be greater than or equal to qps",
			baseDelay:   500 * time.Millisecond,
			maxDelay:    15 * time.Minute,
			maxBurst:    10,
			qps:         100,
			expectedErr: "bucketSize must be at least as large as the QPS to handle bursts effectively",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := compositeRateLimiter(tt.baseDelay, tt.maxDelay, tt.maxBurst, tt.qps)
			if tt.expectedErr != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
