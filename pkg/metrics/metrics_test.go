/*
Copyright 2026 Nutanix

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package metrics

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nutanix-cloud-native/prism-go-client/converged"
)

func TestRecordInfraError(t *testing.T) {
	InfraOperationErrors.Reset()

	RecordInfraError(ResourceNutanixMachine, OpVMCreate, fmt.Errorf("patch failed: %w", errors.New("timeout")), true)
	RecordInfraError(ResourceNutanixMachine, OpCategory, &converged.APIError{Kind: converged.ErrRateLimit, Cause: errors.New("429 Too Many Requests")}, false)

	require.Equal(t, 1.0, testutil.ToFloat64(InfraOperationErrors.WithLabelValues(ResourceNutanixMachine, OpVMCreate, OutcomeTerminal, ClassTransport, HTTPStatusNone)))
	assert.Equal(t, 1.0, testutil.ToFloat64(InfraOperationErrors.WithLabelValues(ResourceNutanixMachine, OpCategory, OutcomeRetryable, ClassRateLimit, "429")))
}

func TestErrorClass(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{name: "nil", err: nil, want: ClassNone},
		{name: "rate limit", err: &converged.APIError{Kind: converged.ErrRateLimit}, want: ClassRateLimit},
		{name: "internal", err: &converged.APIError{Kind: converged.ErrInternal}, want: ClassInternal},
		{name: "not found", err: &converged.APIError{Kind: converged.ErrNotFound}, want: ClassNotFound},
		{name: "unauthenticated", err: &converged.APIError{Kind: converged.ErrUnauthenticated}, want: ClassUnauthenticated},
		{name: "unauthorized", err: &converged.APIError{Kind: converged.ErrUnauthorized}, want: ClassUnauthorized},
		{name: "unclassified api error such as 408", err: &converged.APIError{Cause: errors.New("408 Request Timeout")}, want: ClassClient},
		{name: "plain error", err: errors.New("kube patch failed"), want: ClassTransport},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, ErrorClass(tc.err))
		})
	}
}

func TestHTTPStatus(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{name: "nil", err: nil, want: HTTPStatusNone},
		{name: "plain", err: errors.New("timeout"), want: HTTPStatusNone},
		{name: "408", err: &converged.APIError{Cause: errors.New("408 Request Timeout")}, want: "408"},
		{name: "429", err: &converged.APIError{Cause: errors.New("429 Too Many Requests")}, want: "429"},
		{name: "400", err: &converged.APIError{Cause: errors.New("400 Bad Request")}, want: "400"},
		{name: "other 418", err: &converged.APIError{Cause: errors.New("418 I'm a teapot")}, want: HTTPStatusOther},
		{name: "api without cause", err: &converged.APIError{Kind: converged.ErrNotFound}, want: HTTPStatusNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, HTTPStatus(tc.err))
		})
	}
}

func TestMachinesFailedGauge(t *testing.T) {
	MachinesFailed.Reset()
	// Unique names avoid cross-test pollution of the process-wide sync.Map.
	ns, name := "ns-gauge-test", "m-gauge-test"
	failedMachineOps.Delete(ns + "/" + name)

	RecordMachineFailed(ResourceNutanixMachine, ns, name, OpVMCreate)
	RecordMachineFailed(ResourceNutanixMachine, ns, name, OpVMCreate) // idempotent
	assert.Equal(t, 1.0, testutil.ToFloat64(MachinesFailed.WithLabelValues(ResourceNutanixMachine, OpVMCreate)))

	ClearMachineFailed(ResourceNutanixMachine, ns, name)
	assert.Equal(t, 0.0, testutil.ToFloat64(MachinesFailed.WithLabelValues(ResourceNutanixMachine, OpVMCreate)))
}

func TestMachinesCreateStuckGauge(t *testing.T) {
	MachinesCreateStuck.Reset()
	ns, name := "ns-stuck-test", "m-stuck-test"
	createStuckMachines.Delete(ns + "/" + name)

	assert.True(t, RecordMachineCreateStuck(ResourceNutanixMachine, ns, name))
	assert.False(t, RecordMachineCreateStuck(ResourceNutanixMachine, ns, name)) // idempotent
	assert.Equal(t, 1.0, testutil.ToFloat64(MachinesCreateStuck.WithLabelValues(ResourceNutanixMachine)))

	ClearMachineCreateStuck(ResourceNutanixMachine, ns, name)
	assert.Equal(t, 0.0, testutil.ToFloat64(MachinesCreateStuck.WithLabelValues(ResourceNutanixMachine)))
}

func TestObservePrismTaskAndCreateDuration(t *testing.T) {
	PrismTaskDuration.Reset()
	PrismTaskResults.Reset()
	VMCreateDuration.Reset()
	IdempotentAccepts.Reset()

	ObservePrismTask(ResourceNutanixMachine, OpVMCreate, TaskStateSucceeded, 2*time.Second)
	assert.Equal(t, 1.0, testutil.ToFloat64(PrismTaskResults.WithLabelValues(ResourceNutanixMachine, OpVMCreate, TaskStateSucceeded)))

	ObserveVMCreateDuration(ResourceNutanixMachine, CreateResultSuccess, 3*time.Second)
	RecordIdempotentAccept(ResourceNutanixMachine, OpVMCreate, IdempotentExistingVM)
	assert.Equal(t, 1.0, testutil.ToFloat64(IdempotentAccepts.WithLabelValues(ResourceNutanixMachine, OpVMCreate, IdempotentExistingVM)))
}

func TestTaskStateFromError(t *testing.T) {
	assert.Equal(t, TaskStateSucceeded, TaskStateFromError(nil))
	assert.Equal(t, TaskStateCanceled, TaskStateFromError(context.Canceled))
	assert.Equal(t, TaskStateCanceled, TaskStateFromError(context.DeadlineExceeded))
	assert.Equal(t, TaskStateFailed, TaskStateFromError(&converged.APIError{Kind: converged.ErrInternal}))
	assert.Equal(t, TaskStateError, TaskStateFromError(errors.New("boom")))
}
