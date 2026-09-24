# 5. The Prism Central client layer: tasks, subtasks, polling, retries, request-id, errors

Stack, top to bottom:

```
CAPX controllers
  └── prism-go-client "converged" v4 client        (pgc:converged/v4/*.go)        ← CAPX calls this
        └── prism-go-client v4 SDK wrapper         (pgc:v4/v4.go)                 ← builds one ApiClient per namespace
              └── Nutanix generated Go SDKs         (ntnx-api-golang-clients: vmm, prism, clustermgmt, networking, ...)
                    └── hashicorp/go-retryablehttp  (HTTP retries)
                          └── HTTPS → Prism Central /api/<namespace>/v4.x/...
CAPX also uses the legacy v3 client (pgc:v3) for: v3 GetProject/ListAllProject (PC<7.6), recovery plans, recovery-plan jobs, Groups (DR config), v3 GetTask.
```

## 5.1 How PC long-running operations work (v4)

1. The client POSTs, e.g. `POST /api/vmm/v4.3/ahv/config/vms`.
2. PC returns **202 Accepted** with a `TaskReference` (task ext-id). `vmm-defs:.../api/vmEndpoints.yaml:560-567`.
   202 means "request accepted", **not** "VM created".
3. PC runs the work asynchronously. The parent task may spawn **subtasks** (for VM create, for example
   placement / disk / NIC work on the PE). A failure in a subtask fails the parent, but the parent's
   `errorMessages` is often generic.
4. The client polls `GET /api/prism/v4.x/config/tasks/{extId}` until a terminal status:
   `SUCCEEDED | FAILED | CANCELED` (other states: `QUEUED | RUNNING | SUSPENDED | CANCELLING | UNKNOWN | REDACTED`,
   `pgc:converged/converged.go:198-211`).
5. On success, `task.entitiesAffected` lists the created entity ext-ids, which the client fetches with `GET`.

## 5.2 `Operation.Wait` — the task-polling loop (verified)

`pgc:converged/v4/structs.go:206-304`

```go
for status != SUCCEEDED {
    if ctx.Done() → return "task wait canceled"                  // only exit besides the cases below
    sleep 1s                                                     // :221   fixed interval, no backoff
    task, err = GetTaskById(taskUUID)                            // :223-229
    if err → return "failed to get task ..."                     // :231-233  (err is a classified APIError)
    if task.Status == nil → return error                         // :236-238
    if FAILED {
        if task.ErrorMessages != nil → return "task <id> failed: <msgs>"   // :242-254  plain fmt.Errorf
        if CANCELED || CANCELING → return "canceled"                        // :256-258  UNREACHABLE: nested inside FAILED
    }
    collect entitiesAffected ext-ids (dedup)                     // :262-279
}
for each ext-id: entity, err = entityGetter(uuid)                // :282-298
    if entity != nil { if err → return err; append }             // entity == nil → skipped, err dropped
return result
```

Consequences:

| # | Behaviour | Impact |
|---|---|---|
| W1 | **No timeout.** The loop ends only on SUCCEEDED, a FAILED task with non-nil messages, an error, or context cancel. The reconcile context has no deadline (CAPX sets no reconcile timeout). | A stuck task blocks one of the 10 NutanixMachine workers indefinitely. 10 stuck creates stop all machine reconciles ([F-06](09-findings.md#f-06)). |
| W2 | **FAILED with nil `ErrorMessages` loops forever** (status ≠ SUCCEEDED, no return). | Worker hang ([F-06](09-findings.md#f-06)). |
| W3 | **CANCELED / CANCELLING / SUSPENDED / UNKNOWN loop forever** — the CANCELED check is nested inside the FAILED branch. | Worker hang. |
| W4 | Fixed 1 s poll. | One GET per second per in-flight task; many concurrent creates means a steady load on the PC tasks API. |
| W5 | A FAILED task returns a **plain `fmt.Errorf`**, not a classified `converged.APIError`. | CAPX's `isRetryableAPIError` treats it as **retryable** ([F-04](09-findings.md#f-04)). |
| W6 | A failed `GET task` returns a classified APIError (e.g. 404 → NotFound). | CAPX treats NotFound as **non-retryable → TERMINAL**, even though the VM may be fine. |
| W7 | Entity fetch after success: if `entityGetter` returns `(nil, err)`, e.g. 404 from read-after-write lag, the error is **dropped** and the entity skipped. | `CreateAsync+Wait` can return 0 VMs for a VM that exists → CAPX TERMINAL + orphan VM ([F-05](09-findings.md#f-05)). |
| W8 | Duplicate `entitiesAffected` are de-duplicated; comments note VM create "sometimes returns duplicates" / entities that cannot be found (`:272-291`). | Known upstream quirk, handled defensively. |

The `Operation` also exposes non-blocking `IsDone/IsSuccess/IsFailed/Status/UUID` (`:306-401`), but
`IsDone`/`Status` are only updated by `Wait`, so CAPX cannot resume a task after a restart: it keeps no task UUID.

## 5.3 Subtask error enrichment (CAPX side)

`controllers/task_errors.go` — used **only** by `createAndWaitForVM` (`controllers/nutanixmachine_controller.go:2079`).

```
waitForConvergedOperation(op):                                  task_errors.go:36-46
  result, err := op.Wait(ctx)
  if err → enrichTaskErrorWithFailedSubtasks(op.UUID(), err)
enrichTaskErrorWithFailedSubtasks:                              :48-64
  collect failed subtasks recursively (visited set)              :67-105
     Tasks.List(filter: parentTask/extId eq '<p>' and status eq FAILED)      :31, :108-110
     fallback if List fails: Tasks.Get(parent) → Tasks.Get(each subTask ref) :112-137
  each failed child → "[<operationDescription|operation>] <errorMessages...; legacyErrorMessage>"  :153-171
  return fmt.Errorf("%w; failed_subtasks: %s", parentErr, joined)           :64
```

- It wraps with `%w`, so classification stays whatever the parent error was (plain for task failure, see W5).
- Enrichment runs only when `Wait` **returns** an error. It does not help W1–W3 (no return).
- Enrichment is **not** used for: power-on (`:2465`), custom attributes (inside pgc), VM profile deploy (`:2133`),
  VM delete (not waited), VG detach (not waited), categories (sync API), and the VHA-domain v3 tasks.
- The enriched message goes to the log and into `failureMessage` **only** when CAPX decides the error is terminal.
  In the common (retryable) case it is only in the controller log. No condition carries it:
  `VMProvisionedTaskFailed = "FailedVMTask"` is defined (`api/v1beta1/conditions.go:116`) but never used.

## 5.4 Idempotency: the `NTNX-Request-Id` header (verified end to end)

| Hop | What happens | Reference |
|---|---|---|
| CAPX | `v4Converged.WithRequestID(ctx, requestID)` puts `{"NTNX-Request-Id": id}` into the context | `controllers/nutanixmachine_controller.go:2037`; `pgc:converged/v4/utils.go:294-314` |
| pgc | `CreateAsync` passes `headerArgs(ctx)` as SDK args | `pgc:converged/v4/vms.go:179-186`, `utils.go:322-334` |
| SDK | explicit headers are copied, except `authorization, cookie, host, user-agent` | `sdk-vmm:api/vm_api.go` `CreateVm` + `NewVmServiceApi` (`headers := []string{"authorization","cookie","host","user-agent"}`) |
| SDK | **if no request id is present, the SDK generates a random UUID** for every call | `sdk-vmm:client/api_client.go:245-250` |
| retryablehttp | HTTP-level retries resend the same request, so the same id | — |
| PC | `NTNX-Request-Id` is a **required** header on `createVm` and on the power actions | `vmm-defs:.../vmEndpoints.yaml:554-556`, `vmPowerEndpoints.yaml:88-90` |
| PC errors | `VM_MISSING_REQUEST_ID_HEADER` (30400), `VM_INVALID_REQUEST_ID_HEADER` (30401) | `vmm-defs:etc/resources/errorMessages/bundles/en_US/304xx-vmRequestIdErrors.yaml` |

So:
- **VM create (normal path)**: stable key across reconciles and restarts → a retried create returns the original task (at most once).
- **VM create via VM profile**: no key from CAPX → the SDK generates a new random one per reconcile → **no at-most-once protection** ([F-03](09-findings.md#f-03)).
- **Power-on, custom attributes, delete, VG detach**: random key per call. Acceptable, since these are keyed by VM UUID and mostly naturally idempotent.
- **Category create**: random key per call; duplicates are prevented only by list-then-create (racy, [F-08](09-findings.md#f-08)).
- **Open question for the VMM team:** how long does PC remember a request id, and what exactly does a replay
  return (the original task id even if FAILED? a new task?). Sid described the expected behaviour ("returns the same
  task"). The API definitions do not document it.

## 5.5 HTTP retries and timeouts in the SDK (verified for vmm v4.3.1)

| Setting | Default | Reference |
|---|---|---|
| Retried HTTP statuses | **408, 429, 503, 504** only | `sdk-vmm:client/api_client.go:43` |
| Transport errors (connection refused, TLS, timeout) | **not** retried by the SDK (`retryPolicy` returns false when err != nil) | `api_client.go:1059-1068` |
| Max retry attempts | 5 | `api_client.go:149`, applied `:668` |
| Retry wait | retryablehttp backoff, max 3 s (`RetryInterval`) | `:153`, `:669` |
| Connect timeout / read timeout | 30 s / 30 s | `:151-152` |
| Whole HTTP request timeout | connect + TLS handshake + read ≈ 60+ s | `:678` |
| 500 Internal Server Error | **not** retried by the SDK; surfaces as `ErrInternal` | — |

pgc sets `VerifySSL`, host/port, credentials, and an optional read timeout; it keeps the SDK retry defaults
(`pgc:v4/v4.go:210-240`).

## 5.6 Error classification in pgc (`converged/v4/errors.go`, verified)

`CategoriseFromOpenAPI` (`:181-190`): pulls HTTP `Status` and `Body` off the SDK error by reflection.
If neither exists (transport error) → returns `fmt.Errorf("api call failed: %w", err)` — **not** an APIError.

`Categorise` (`:85-121`), first match wins:

| HTTP status | `APIError.Kind` |
|---|---|
| 401 | `ErrUnauthenticated` |
| 403 | `ErrUnauthorized` |
| 404 | `ErrNotFound` |
| 429 | `ErrRateLimit` |
| 500–599 | `ErrInternal` |
| other (400, 409, 412, 422 ...) | from the body's first `errorGroup` via `errorGroupToKind` (`:33-60`), else **Kind = nil** |

`errorGroupToKind` covers only generic groups (`RESOURCE_NOT_FOUND`, `RATE_LIMIT_EXCEEDED`, `CLUSTERMGMT_SERVICE_ERROR`,
`FORBIDDEN`, ...). VMM-specific groups such as `VM_ETAG_MISMATCH`, `VM_MISSING_REQUEST_ID_HEADER`,
`OPERATION_TIME_OUT_ERROR` and `VM_SERVICE_UNAVAILABLE_ERROR` are not mapped. For a non-5xx status they end up
with Kind nil, which CAPX treats as **non-retryable**.

The VMM repo has ~30 error bundles (`vmm-defs:etc/resources/errorMessages/bundles/en_US/*.yaml`, ~6.8k lines).
Mapping each code to an HTTP status and a CAPX action is the per-API work of Phase 1.

## 5.7 Legacy v3 task waiting (VHA domain only)

`pkg/client/status.go:38-61` — `WaitForTaskToSucceed` polls v3 `GetTask` every 2 s with
`wait.PollUntilContextCancel`. `FAILED` / `INVALID_UUID` → error with `error_detail` and `progress_message`.
Like W1, there is **no timeout** besides the context. Used for recovery-plan create/delete
(`controllers/nutanixvirtualhadomain_controller.go:944`, `:1007`).

## 5.8 Client caching and credentials

- One converged client per NutanixCluster, cached, rebuilt when the endpoint/credential hash changes (`pgc:converged/v4/cache.go:31-60`).
- Session auth enabled (`pkg/client/cache.go:13-19`).
- During a password rotation window, PC can answer **401** → `ErrUnauthenticated` → CAPX: non-retryable →
  **TERMINAL** for any machine in the middle of creation ([F-09](09-findings.md#f-09)).
