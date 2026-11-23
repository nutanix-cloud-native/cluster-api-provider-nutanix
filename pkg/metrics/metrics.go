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

// Package metrics records CAPX infrastructure operations on the
// controller-runtime Prometheus registry, which is served at /metrics.
package metrics

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/nutanix-cloud-native/prism-go-client/converged"
)

const (
	ResourceNutanixMachine         = "nutanixmachine"
	ResourceNutanixCluster         = "nutanixcluster"
	ResourceNutanixFailureDomain   = "nutanixfailuredomain"
	ResourceNutanixMetro           = "nutanixmetro"
	ResourceNutanixMetroSite       = "nutanixmetrosite"
	ResourceNutanixVirtualHADomain = "nutanixvirtualhadomain"
	ResourceMetroScaleDownBalancer = "metroscaledownbalancer"
	ResourceNutanixMachineTemplate = "nutanixmachinetemplate"

	OutcomeTerminal  = "terminal"
	OutcomeRetryable = "retryable"

	// Operations are a closed set. Do not put object names, UUIDs, or
	// error text in a label. Those values are unbounded.
	OpProject            = "project"
	OpProjectPolicy      = "project_policy"
	OpResourceGroup      = "resource_group"
	OpMachineConfig      = "machine_config"
	OpSubnetPE           = "subnet_pe"
	OpNIC                = "nic"
	OpCategory           = "category"
	OpCategoryDelete     = "category_delete"
	OpProjectAssign      = "project_assign"
	OpDeviceBundle       = "device_bundle"
	OpGPU                = "gpu"
	OpDisk               = "disk"
	OpGuestCustomization = "guest_customization"
	OpBootType           = "boot_type"
	OpVMCreate           = "vm_create"
	OpVMLookup           = "vm_lookup"
	OpVMProfile          = "vm_profile"
	OpCustomAttributes   = "custom_attributes"
	OpProfileNIC         = "profile_nic"
	OpPowerOn            = "power_on"
	OpImage              = "image"
	OpRequestID          = "request_id"
	OpPCVersion          = "pc_version"
	OpMachinePatch       = "machine_patch"
	OpVolumeGroupDetach  = "volume_group_detach"
	OpReconcile          = "reconcile"

	ClassRateLimit       = "rate_limit"
	ClassInternal        = "internal"
	ClassNotFound        = "not_found"
	ClassUnauthenticated = "unauthenticated"
	ClassUnauthorized    = "unauthorized"
	ClassClient          = "client"
	ClassTransport       = "transport"
	ClassNone            = "none"

	HTTPStatusNone  = "none"
	HTTPStatusOther = "other"

	TaskStateSucceeded = "succeeded"
	TaskStateFailed    = "failed"
	TaskStateCanceled  = "canceled"
	TaskStateError     = "error"

	IdempotentExistingVM    = "existing_vm"
	IdempotentAlreadyAbsent = "already_absent"

	CreateResultSuccess = "success"
	CreateResultError   = "error"
)

var httpStatusFromCause = regexp.MustCompile(`(?:^|\D)([1-5][0-9]{2})(?:\D|$)`)

// knownHTTPStatuses is the closed set used as the http_status label.
var knownHTTPStatuses = map[string]struct{}{
	"400": {}, "401": {}, "403": {}, "404": {}, "408": {}, "409": {},
	"412": {}, "428": {}, "429": {},
	"500": {}, "502": {}, "503": {}, "504": {},
}

// InfraOperationErrors counts failures while CAPX creates or changes Nutanix
// infrastructure. outcome=terminal means the NutanixMachine was marked failed.
// outcome=retryable means the same object will be reconciled again.
var InfraOperationErrors = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "capx_infra_operation_errors_total",
		Help: "Errors while CAPX creates or changes Nutanix infrastructure. outcome=terminal means the object was marked failed. outcome=retryable means the same object will be reconciled again.",
	},
	[]string{"resource", "operation", "outcome", "class", "http_status"},
)

// MachinesFailed is the number of NutanixMachines this process currently
// tracks as failed, by the operation that failed them. The gauge is adjusted
// when CAPX sets failure status and when a failed machine is deleted. After a
// controller restart the gauge starts at zero until new failures are recorded.
var MachinesFailed = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Name: "capx_infra_machines_failed",
		Help: "NutanixMachines this CAPX process currently tracks as failed, by the operation that failed them.",
	},
	[]string{"resource", "operation"},
)

// MachinesCreateStuck is the number of NutanixMachines this process currently
// tracks as not Ready for longer than the create-stuck threshold since CAPX
// stamped vm-create-started-at. Machine names are not labels. After a controller
// restart the gauge starts at zero until reconciles re-observe stuck machines.
var MachinesCreateStuck = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Name: "capx_infra_machines_create_stuck",
		Help: "NutanixMachines this CAPX process currently tracks as not Ready past the VM create stuck threshold.",
	},
	[]string{"resource"},
)

// VMCreateDuration is the time from FindVM through VMProvisioned (or create error).
var VMCreateDuration = prometheus.NewHistogramVec(
	prometheus.HistogramOpts{
		Name:    "capx_infra_vm_create_duration_seconds",
		Help:    "Time from FindVM through VMProvisioned or create failure for a NutanixMachine.",
		Buckets: []float64{1, 2, 5, 10, 30, 60, 120, 300, 600, 900},
	},
	[]string{"resource", "result"},
)

// PrismTaskDuration is how long CAPX blocked on a Prism task Wait.
// The task UUID is not a label.
var PrismTaskDuration = prometheus.NewHistogramVec(
	prometheus.HistogramOpts{
		Name:    "capx_infra_prism_task_duration_seconds",
		Help:    "Time CAPX spent waiting on a Prism task. The task UUID is not a label.",
		Buckets: []float64{0.5, 1, 2, 5, 10, 30, 60, 120, 300, 600},
	},
	[]string{"resource", "operation", "state"},
)

// PrismTaskResults counts Prism task Wait outcomes without the task UUID.
var PrismTaskResults = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "capx_infra_prism_task_results_total",
		Help: "Prism task Wait outcomes observed by CAPX. state is succeeded, failed, canceled, or error.",
	},
	[]string{"resource", "operation", "state"},
)

// IdempotentAccepts counts paths where CAPX treated an already-present or
// already-absent Prism object as success (no new create, or delete no-op).
var IdempotentAccepts = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "capx_infra_idempotent_accepts_total",
		Help: "Times CAPX treated an already-present or already-absent Prism object as success.",
	},
	[]string{"resource", "operation", "reason"},
)

var failedMachineOps sync.Map    // namespaced name -> operation
var createStuckMachines sync.Map // namespaced name -> struct{}

func init() {
	metrics.Registry.MustRegister(
		InfraOperationErrors,
		MachinesFailed,
		MachinesCreateStuck,
		VMCreateDuration,
		PrismTaskDuration,
		PrismTaskResults,
		IdempotentAccepts,
	)
}

// RecordInfraError increments InfraOperationErrors. class and http_status are
// derived from err so callers cannot invent unbounded labels.
func RecordInfraError(resource, operation string, err error, terminal bool) {
	RecordClass(resource, operation, ErrorClass(err), HTTPStatus(err), terminal)
}

// RecordClass increments InfraOperationErrors with explicit class and http_status.
// class must be one of the Class* constants. http_status must be none, a known
// three-digit code, or other.
func RecordClass(resource, operation, class, httpStatus string, terminal bool) {
	outcome := OutcomeRetryable
	if terminal {
		outcome = OutcomeTerminal
	}
	if httpStatus == "" {
		httpStatus = HTTPStatusNone
	}
	InfraOperationErrors.WithLabelValues(resource, operation, outcome, class, httpStatus).Inc()
}

// RecordMachineFailed increments MachinesFailed once per machine key.
func RecordMachineFailed(resource, namespace, name, operation string) {
	key := namespace + "/" + name
	if _, loaded := failedMachineOps.LoadOrStore(key, operation); loaded {
		return
	}
	MachinesFailed.WithLabelValues(resource, operation).Inc()
}

// ClearMachineFailed decrements MachinesFailed when a tracked failed machine is deleted.
func ClearMachineFailed(resource, namespace, name string) {
	key := namespace + "/" + name
	op, ok := failedMachineOps.LoadAndDelete(key)
	if !ok {
		return
	}
	MachinesFailed.WithLabelValues(resource, op.(string)).Dec()
}

// RecordMachineCreateStuck increments MachinesCreateStuck once per machine key.
// Returns true when this call newly marked the machine as stuck.
func RecordMachineCreateStuck(resource, namespace, name string) bool {
	key := namespace + "/" + name
	if _, loaded := createStuckMachines.LoadOrStore(key, struct{}{}); loaded {
		return false
	}
	MachinesCreateStuck.WithLabelValues(resource).Inc()
	return true
}

// ClearMachineCreateStuck decrements MachinesCreateStuck when a tracked stuck
// machine becomes Ready or is deleted.
func ClearMachineCreateStuck(resource, namespace, name string) {
	key := namespace + "/" + name
	if _, ok := createStuckMachines.LoadAndDelete(key); !ok {
		return
	}
	MachinesCreateStuck.WithLabelValues(resource).Dec()
}

// ObserveVMCreateDuration records FindVM-through-provision (or create error) latency.
func ObserveVMCreateDuration(resource, result string, d time.Duration) {
	VMCreateDuration.WithLabelValues(resource, result).Observe(d.Seconds())
}

// ObservePrismTask records Wait duration and outcome. state must be one of the TaskState* constants.
func ObservePrismTask(resource, operation, state string, d time.Duration) {
	PrismTaskDuration.WithLabelValues(resource, operation, state).Observe(d.Seconds())
	PrismTaskResults.WithLabelValues(resource, operation, state).Inc()
}

// RecordIdempotentAccept increments IdempotentAccepts. reason must be a closed constant.
func RecordIdempotentAccept(resource, operation, reason string) {
	IdempotentAccepts.WithLabelValues(resource, operation, reason).Inc()
}

// ErrorClass maps an error onto a small set of Prometheus label values.
// HTTP 408, 412, and 428 land in ClassClient: the converged client does not
// give them their own kind. HTTP 429 is ClassRateLimit. HTTP 500-599 is ClassInternal.
func ErrorClass(err error) string {
	switch {
	case err == nil:
		return ClassNone
	case converged.IsRateLimit(err):
		return ClassRateLimit
	case converged.IsInternal(err):
		return ClassInternal
	case converged.IsNotFound(err):
		return ClassNotFound
	case converged.IsUnauthenticated(err):
		return ClassUnauthenticated
	case converged.IsUnauthorized(err):
		return ClassUnauthorized
	default:
		var apiErr *converged.APIError
		if errors.As(err, &apiErr) {
			return ClassClient
		}
		return ClassTransport
	}
}

// HTTPStatus extracts a closed-set HTTP status label from a Prism APIError cause.
// Unclassified or non-API errors use HTTPStatusNone.
func HTTPStatus(err error) string {
	if err == nil {
		return HTTPStatusNone
	}
	var apiErr *converged.APIError
	if !errors.As(err, &apiErr) || apiErr.Cause == nil {
		return HTTPStatusNone
	}
	m := httpStatusFromCause.FindStringSubmatch(apiErr.Cause.Error())
	if len(m) < 2 {
		return HTTPStatusNone
	}
	code := m[1]
	if _, ok := knownHTTPStatuses[code]; ok {
		return code
	}
	if n, convErr := strconv.Atoi(code); convErr == nil && n >= 100 && n <= 599 {
		return HTTPStatusOther
	}
	return HTTPStatusNone
}

// TaskStateFromError maps a Wait error onto a closed task state label.
func TaskStateFromError(err error) string {
	if err == nil {
		return TaskStateSucceeded
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return TaskStateCanceled
	}
	var apiErr *converged.APIError
	if errors.As(err, &apiErr) {
		return TaskStateFailed
	}
	return TaskStateError
}
