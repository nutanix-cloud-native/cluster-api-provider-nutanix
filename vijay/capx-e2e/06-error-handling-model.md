# 6. Error-handling model: what CAPX does with each kind of error today

## 6.1 The three outcomes of a reconcile

| Outcome | Code shape | What happens next |
|---|---|---|
| **Wait** | `return reconcile.Result{}, nil` | Nothing, until a watched object changes (NutanixMachine, Machine, NutanixCluster, Cluster). |
| **Timed requeue** | `return reconcile.Result{RequeueAfter: d}, nil` | Reconcile again after `d` (5 s or 30 s in CAPX). Backoff state is reset. |
| **Error requeue** | `return reconcile.Result{}, err` | Controller-runtime requeues with the item's exponential backoff: 1 ms × 2ⁿ, capped at **1000 s (~16.7 min)** (`main.go:504`), plus a global 10 qps / 100 burst bucket. |
| **Terminal** (CAPX-specific) | `rctx.SetFailureStatus(reason, err)` + return err | `status.failureReason/failureMessage` are persisted by the deferred patch; every later reconcile exits at `controllers/nutanixmachine_controller.go:575-578`. |

## 6.2 The classifier: `isRetryableAPIError` — `controllers/helpers.go:137-154`

```go
switch {
case converged.IsNotFound(err), isTerminalError(err):  return false   // 404 or CAPX terminalError
case converged.IsRateLimit(err), converged.IsInternal(err): return true // 429 or 5xx
default:
    if errors.As(err, &*converged.APIError) { return false }   // any other classified HTTP error: 400/401/403/409/412/...
    return true                                                 // not an APIError: transport errors, task failures, CAPX's own fmt.Errorf
}
```

Applied to real error sources:

| Error source | Error value | Retryable? | Reference |
|---|---|---|---|
| HTTP 404 on any GET/POST | `APIError{Kind: ErrNotFound}` | **No → terminal** | pgc `errors.go:106` |
| HTTP 429 (after 5 SDK retries) | `ErrRateLimit` | Yes | `errors.go:108` |
| HTTP 5xx (500 not retried by SDK; 503/504 after 5 retries) | `ErrInternal` | Yes | `errors.go:110` |
| HTTP 401 (bad or rotated credentials) | `ErrUnauthenticated` | **No → terminal** | `errors.go:99-102` |
| HTTP 403 (missing permission) | `ErrUnauthorized` | **No → terminal** | `errors.go:103-105` |
| HTTP 400/409/412/422 with an unmapped `errorGroup` (e.g. `VM_ETAG_MISMATCH`, validation errors) | `APIError{Kind: nil}` | **No → terminal** | `errors.go:114-120` |
| Transport error (DNS, connection refused, TLS, client timeout) | `fmt.Errorf("api call failed: %w")` | Yes | `errors.go:186-188` |
| **PC task finished FAILED** (with messages) | plain `fmt.Errorf("task %s failed: ...")` | **Yes** | pgc `structs.go:253` |
| Task-wait ctx cancelled | plain error | Yes | `structs.go:218` |
| CAPX "not found by name" lookups (image, subnet, PE, GPU, category, project, storage container) | `*terminalError` | No → terminal | `controllers/helpers.go:618`, `:983`, `:1199`, `:2181`, ... |
| CAPX "more than one found" lookups | plain `fmt.Errorf` | Yes (loops forever until data changes) | `controllers/helpers.go:353`, `:620`, `:987`, `:1203` |

Main surprises:
1. **A failed PC task is retryable, while a 404 is terminal.** That is the opposite of what the design discussion
   wants: a failed create should be terminal for the machine (at-most-once), and a transient 404 (read-after-write lag)
   should be retried.
2. **Credentials (401/403) are terminal.** A password rotation or a temporary IAM glitch permanently fails every
   machine that was mid-create.
3. Some call sites **ignore the classifier** and are always terminal: `validateMachineConfig` (`:1896-1900`) and
   `GetSubnetAndPEUUIDs` (`:1902-1907`), both of which make PC calls; also guest customization and boot type
   (config-only, so that is fine).

## 6.3 Every terminal site in the NutanixMachine controller

38 `SetFailureStatus` calls (`grep -n SetFailureStatus controllers/nutanixmachine_controller.go`). Grouped:

| Phase | Lines | Gated by `isRetryableAPIError`? |
|---|---|---|
| Project resolve / policy / resource group | 649, 656, 676 | yes |
| `validateMachineConfig` (FD + PC validation, sizes) | 1898 | **no — always terminal** |
| `GetSubnetAndPEUUIDs` (PE + subnet lookups on PC) | 1905 | **no — always terminal** |
| Categories get-or-create, identifiers, references | 1953, 1969, 1983 | yes |
| Project on VM | 1994 | yes |
| GPUs | 2004 | yes |
| Disks (system, bootstrap, data) | 2014, 2647, 2660, 2668, 2684, 2697, 2715 | mostly yes; "image being deleted" and disk-spec errors always terminal |
| Guest customization / boot type | 2023, 2031 | no (config errors) |
| VM create: `CreateAsync` / `Wait` | 2094 | yes |
| VM create returned ≠ 1 VM | 2085 | **no — always terminal** (hits the orphan case, [F-05](09-findings.md#f-05)) |
| VM profile path | 2118, 2127, 2137, 2143, 2209, 2215, 2261, 2282, 2296, 2311, 2319, 2342, 2351 | mixed |
| Custom attributes (VM exists) | 2192 | yes |
| Power on (VM exists) | 2461, 2469, 2479 | yes |

Nothing terminal exists in the delete path, the Ready branch, address assignment, or failure-domain / VHA checks.
Those retry forever.

## 6.4 What "terminal" means end to end (v1beta2 CAPI)

```
CAPX: SetFailureStatus → NutanixMachine.status.failureReason = "CreateError"|"PowerOnError", failureMessage = <err>
CAPX: every later reconcile returns immediately (no conditions updated, VM not touched)
CAPI Machine controller (v1.13): copies them to Machine.status.deprecated.v1beta1.failure* — NO lifecycle effect
                                  (capi contract infra-machine.md, "terminal failures")
Machine stays Phase=Provisioning (no providerID) or Provisioned (if the VM existed)
MachineHealthCheck: nodeStartupTimeout 10m (default template) → marks the Machine unhealthy → remediation deletes it
MachineSet/KCP: creates a replacement Machine → new NutanixMachine → new request id → new VM
Old NutanixMachine delete: if status.vmUUID/providerID known → VM deleted; if unknown → finalizer dropped, any VM leaks
```

So in practice "terminal" is "wait for MHC", and only when an MHC with `nodeStartupTimeout` covers that machine.
KCP-managed control-plane machines are remediated only if the MHC selects them and KCP remediation allows it.
Without an MHC the machine sits forever.

## 6.5 What the user can see today

`kubectl describe nutanixmachine <name>` shows:

| Situation | Visible signal |
|---|---|
| Waiting for cluster infra / control plane / bootstrap | `VMProvisioned=False` with reason `ClusterInfrastructureNotReady` / `ControlplaneNotInitialized` / `BootstrapDataNotReady` |
| Project problems | `ProjectAssigned=False/ProjectAssignationFailed` + message |
| Terminal create/power-on error | `status.failureReason/failureMessage` (the message includes subtask details when the create task failed and the error was classified terminal) |
| **Retryable create failure (e.g. VM create task FAILED)** | **nothing on the object** — `VMProvisioned` is not updated; only controller logs have the enriched error |
| Waiting for IP | `VMAddressesAssigned=False/VMAddressesFailed` (severity Error, although it is usually just "not yet") |
| Delete problems | `VMProvisioned=False/DeletionFailed` or `VolumeGroupDetachFailed` + message |
| Metro recovery placement | `MetroRecoveryPlacement=True/SiteMaintenance` |

There is no `Ready` summary condition. There are no Kubernetes Events and no metrics beyond controller-runtime defaults
(reconcile counts, durations, workqueue depth). There is no per-PC-API latency or error metric.

## 6.6 Requeue intervals used in CAPX

| Where | Interval | Reference |
|---|---|---|
| Machine Ready but Machine not updated yet | 5 s, forever | `controllers/nutanixmachine_controller.go:593` |
| Delete: VM has running/queued tasks | 5 s | `:417` |
| Delete: VG detach issued | 30 s (`detachVGRequeueAfter`) | `:447`, `controllers/helpers.go:66` |
| Delete: VM delete task issued | 5 s | `:466` |
| Delete: waiting for a recovered (non-decoupled) Metro VM | 5 s | `:373` |
| Cluster delete: machines or VHA domains remain | 5 s | `controllers/nutanixcluster_controller.go:282`, `:309` |
| VHA domain resync | `vhaResyncInterval` | `controllers/nutanixvirtualhadomain_controller.go:227` |
| Everything else that returns an error | 1 ms → 1000 s exponential per item | `main.go:504` |
