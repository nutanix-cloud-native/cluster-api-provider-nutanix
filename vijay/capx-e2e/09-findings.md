# 9. Findings from the code read (gaps, bugs, risks)

Status key: **Verified** = follows directly from the code. **Hypothesis** = code path is clear but depends on a PC
behaviour (HTTP status, request-id semantics) that must be confirmed on a real PC or with the owning team.

Severity: **High** = can lose or leak a VM, deadlock provisioning, or stall the controller. **Medium** = wrong
classification or retry behaviour with a real user impact. **Low** = hygiene, clarity, or edge cases.

| ID | Title | Sev | Status |
|---|---|---|---|
| [F-01](#f-01) | providerID never set when the VM is found instead of created → Ready without providerID → deadlock | High | Verified (repro pending) |
| [F-02](#f-02) | CAPX uses the v1beta1 contract; its "terminal failure" has no effect in CAPI v1.13; compat removed ~Apr 2027 | High | Verified |
| [F-03](#f-03) | VM-profile create path has no idempotency key, no subtask errors, deferred providerID persist | High | Verified |
| [F-04](#f-04) | A FAILED create task is treated as retryable → endless retry of the same failed request | High | Verified (PC replay semantics: hypothesis) |
| [F-05](#f-05) | Post-create entity fetch errors are dropped → "0 VMs" → TERMINAL while the VM exists → orphan | High | Verified |
| [F-06](#f-06) | Task wait has no timeout and never exits on CANCELED/SUSPENDED/UNKNOWN or FAILED-without-messages | High | Verified |
| [F-07](#f-07) | ETag races on power-on / custom-attributes can go TERMINAL | Medium | Hypothesis |
| [F-08](#f-08) | Category get-or-create is list-then-create with no "already exists" handling | Low | Hypothesis |
| [F-09](#f-09) | 401/403 are terminal → a credential rotation fails in-flight machines | Medium | Verified |
| [F-10](#f-10) | FD/PE/subnet validation errors are always terminal, even for 5xx | Medium | Verified |
| [F-11](#f-11) | 404 is always terminal, including read-after-write lag | Medium | Verified |
| [F-12](#f-12) | No state checks after Ready (VM deleted/off in PC is not noticed) | Medium | Verified |
| [F-13](#f-13) | Delete: no name search when the UUID is unknown (leak); delete-task failures invisible; one VG per 30 s | Medium | Verified |
| [F-14](#f-14) | Scale/load: rate-limiter flags unused, 1 s task polling, full lookup re-run on every requeue | Medium | Verified |
| [F-15](#f-15) | Failure reasons are not surfaced on the object for retryable errors; `FailedVMTask` unused | Medium | Verified |
| [F-16](#f-16) | Duplicate VM names → "more than one VM" error loops forever | Low | Verified |
| [F-17](#f-17) | Recorded VM deleted out-of-band before Ready → "expected to be present" loops forever | Low | Verified |
| [F-18](#f-18) | NutanixCluster becomes Ready even with invalid failure domains; no PC reachability check | Low | Verified |
| [F-19](#f-19) | Pause check reads the CAPI Machine's annotation, not the NutanixMachine's | Low | Verified |
| [F-20](#f-20) | Code hygiene: dead "not patching" branch; cluster-name label from NutanixCluster name; two writers of NutanixCluster status | Low | Verified |

---

### F-01
**providerID is never set when the VM is found instead of created → NutanixMachine Ready without providerID → CAPI/CAPX deadlock.**

Evidence:
- `spec.providerID` is assigned only at `controllers/nutanixmachine_controller.go:2051` (normal create) and `:2156`
  (profile create), both right after a create **in the same reconcile**.
- When `FindVM` returns an existing VM (`:1874-1885`), `getOrCreateVM` returns it **without** setting providerID.
- The flow continues to `status.ready = true` (`:759`); `syncVmUUID` fills `status.vmUUID` (`:709`).
- CAPI waits for `spec.providerID` (`capi:internal/controllers/machine/machine_controller_phases.go:319-330`), so
  `Machine.spec.providerID` stays empty.
- CAPX's Ready branch waits for `Machine.spec.providerID` and requeues every 5 s forever (`:589-594`).

How we get there (any gap between the create task finishing and the patch at `:2054` landing):
1. CAPX pod restarts or loses leadership while `op.Wait` is polling (it can take minutes).
2. `patchMachine` at `:2054` fails (API server conflict or timeout) → `return nil, err` → the next reconcile finds the VM by name.
3. `Wait` returns an error after the VM was actually created (task GET transient failure, context cancel) → the next
   reconcile finds the VM by name.

This matches the dev33 QA ticket: Machine, NutanixMachine and Node exist, `status.vmUUID` is set, spec.providerID is
empty, and the two objects wait on each other. It also explains why it only shows up on a slow environment: it
needs a long task wait plus a restart or error inside it. **Reproduce:** start a cluster, and during the first worker's
VM-create task wait, `kubectl delete pod` the CAPX manager (or inject a patch failure in a unit test with the mocks).
Expected: Ready=true with empty providerID.

Direction: set providerID whenever a VM is identified (found or created), and ideally persist the task/VM identity
before waiting. Never derive it from status.

### F-02
**The contract version gap.**
- CAPX declares only `cluster.x-k8s.io/v1beta1: v1beta1` (`config/crd/kustomization.yaml:46`, `metadata.yaml`).
- In CAPI v1.13 (v1beta2 contract), InfraMachine `status.failureReason/failureMessage` are copied to
  `Machine.status.deprecated.v1beta1` and **do not fail the Machine**; MHC ignores them (`infra-machine.md`,
  "terminal failures"). CAPX stops reconciling the machine (`:575-578`) while CAPI keeps waiting. Only an MHC
  `nodeStartupTimeout` recovers it.
- No `Ready` condition on NutanixMachine / NutanixCluster, so `Machine.InfrastructureReady` uses CAPI's fallback.
- v1beta1 compatibility (status.ready, failure fields, map-form failureDomains) is tentatively **removed April 2027**.

Direction: model terminal failures as a documented condition (for example `VMProvisioned=False` with reason
`...Failed` and severity Error), and plan the v1beta2 contract migration (`status.initialization.provisioned`,
list-form failure domains, v1beta2 conditions with `Ready`).

### F-03
**The VM-profile create path lacks the protections of the normal path.**
- `DeployVmWithVmProfile(ctx, ...)` uses `rctx.Context`, not `WithRequestID(...)` (`controllers/nutanixmachine_controller.go:2123`).
  The SDK then generates a new random request id per call (`sdk-vmm:client/api_client.go:245-250`). HTTP-level retries
  resend the same request (same id), which is fine. But if the reconcile is interrupted while the first deploy is in
  flight (CAPX restart, wait error) and the VM is not yet visible to the name lookup, the next reconcile issues a
  second deploy with a new id → **a second VM**. This is the same duplicate-VM class the request-id annotation fixed for
  the normal path.
- Waits with `vmOp.Wait` directly (`:2133`): no subtask error enrichment.
- providerID / vmUUID are set in memory (`:2156-2157`) and only persisted by the deferred patch at the end of the
  reconcile. A crash before that → F-01.

### F-04
**A FAILED create task is classified as retryable.**
- `Operation.Wait` returns a plain `fmt.Errorf("task %s failed: ...")` (`pgc:converged/v4/structs.go:253`).
- `isRetryableAPIError`: not an APIError → **true** (`controllers/helpers.go:148-153`).
- `vmCreateFailure` therefore does **not** set failureReason (`controllers/nutanixmachine_controller.go:2091-2097`);
  the error is returned and requeued with backoff.
- The next reconcile finds no VM (none was created), reuses the same request id (`:1891`), and PC is expected to return
  the same failed task → fails again → backoff up to 1000 s → forever, until MHC removes the Machine.
- No condition is set (`VMProvisionedTaskFailed` is unused), so `kubectl describe` shows nothing.

This is the opposite of the intended design (a failed create should be terminal for that NutanixMachine; a new
NutanixMachine gets a new request id). **Hypothesis to confirm with the VMM team:** what a replay of a request id
whose task FAILED returns, and how long request ids are remembered.

### F-05
**A created VM can be reported as "0 VMs" → TERMINAL → orphaned VM.**
- After SUCCEEDED, `Wait` fetches each affected entity; if the getter returns `(nil, err)` the error is dropped
  (`pgc:converged/v4/structs.go:282-298`).
- A transient `VMs.Get` failure (404 read-after-write, 5xx after retries) gives an empty result.
- CAPX: `len(createdVMs) != 1` → **SetFailureStatus** (`controllers/nutanixmachine_controller.go:2083-2087`). The VM UUID
  was never persisted.
- The machine is terminal; MHC eventually deletes the Machine; delete finds no UUID and removes the finalizer
  (`:350-356`) → **the VM is leaked** in PC. (A replacement machine gets a new VM.)

### F-06
**Task wait can hang forever; ten hangs stop all machine reconciles.**
- `Wait` has no deadline (`pgc:converged/v4/structs.go:215-280`); CAPX sets no reconcile timeout.
- FAILED with `ErrorMessages == nil` → no return, loops (`:242-259`).
- CANCELED / CANCELLING / SUSPENDED / UNKNOWN → never handled (the CANCELED check is nested inside the FAILED branch).
- Each hang holds one of the 10 NutanixMachine workers (`main.go:76`, `:240`). After 10, no NutanixMachine in any
  cluster reconciles until the pod restarts, which then risks F-01 for each of them.
- Same pattern (no timeout) in the v3 `WaitForTaskToSucceed` used by the VHA domain controller (`pkg/client/status.go:38-61`).

### F-07
**ETag races can turn a transient condition into a terminal failure.** (Hypothesis)
- Power-on and add-custom-attributes do GET (ETag) then POST with `If-Match` (`pgc:converged/v4/vms.go:290-327`, `:392-435`).
- If the VM changes in between (DR, category update, guest tools), PC returns `VM_ETAG_MISMATCH` (code 30303,
  `vmm-defs:.../303xx-vmEtagErrors.yaml`). The group is not in pgc's `errorGroupToKind`; if the HTTP status is 412
  (to confirm) the error is `APIError{Kind:nil}` → non-retryable → **TERMINAL** (`controllers/nutanixmachine_controller.go:2190-2193`, `:2459-2462`).
- The right handling is to re-GET and retry.

### F-08
**Category get-or-create has no "already exists" handling.** (Hypothesis, low likelihood in the default flow)
- `getOrCreateCategoryForProject` does `Categories.List(key,value)` and, if empty, `Categories.Create`
  (`controllers/helpers.go:1744-1774`). If `Create` fails because the value already exists, the 4xx with an unmapped
  error group becomes `APIError{Kind:nil}` → non-retryable → **TERMINAL**.
- In the default flow the cluster category is created by the first control-plane machine alone (KCP creates CP machines
  one at a time, and workers wait for the control plane), so the race window is small. It opens with parallel creation
  (MachinePools, several MachineDeployments on a pre-initialized cluster) or with PC list lag right after a create.
- To confirm: PC's response to creating an existing key/value, and list consistency after create.
- Related: category delete errors are swallowed (`controllers/helpers.go:1654-1660`) → leftover categories.

### F-09
**Credential problems are terminal.** 401 → `ErrUnauthenticated`, 403 → `ErrUnauthorized` (`pgc:converged/v4/errors.go:99-105`);
neither is NotFound/RateLimit/Internal, but both are APIErrors → `isRetryableAPIError` returns false. A password
rotation, an expired API key, or an IAM hiccup during provisioning permanently fails every machine that hits it.
A 403 is usually a real configuration problem; a 401 during rotation is transient.

### F-10
**PE/subnet/FD lookup failures are always terminal.** `validateMachineConfig` (PC calls for FD validation) and
`GetSubnetAndPEUUIDs` call `SetFailureStatus` unconditionally (`controllers/nutanixmachine_controller.go:1896-1907`).
A 500 or 503 (after SDK retries) from `Clusters.List` / `Subnets.List` fails the machine permanently. Everywhere else,
the same errors would be retried.

### F-11
**404 is always non-retryable.** Correct for "your image does not exist", wrong for PC read-after-write lag right after
a create/update, and wrong for a task GET that returns 404. Sid noted that read-after-write inconsistency does happen.
The helpers mostly turn "not found by name" into a `terminalError` themselves, so a transient 404 is doubly terminal.

### F-12
**No state checks after Ready.** Once `status.ready=true`, reconcile only syncs `vmUUID` (`:589-604`). A VM deleted,
powered off, or moved in PC is not detected by CAPX. It surfaces only through the Node (NotReady → MHC after 5 min in
the default template). That may be acceptable by design, but it is the current behaviour and should be a stated decision.

### F-13
**Delete-path gaps.**
- UUID unknown → finalizer removed without a name search (`:350-356`) → any unrecorded VM leaks (F-05; F-01 after `clusterctl move`).
- The delete task is not waited on or inspected (`:451-466`); a failing delete is retried every 5 s with no reason shown.
- `detachVolumeGroupsFromVM` returns after the first VG (`controllers/helpers.go:2572`) → one VG per 30 s pass.
- The name-mismatch check (`:396-398`) blocks deletion forever if someone renamed the VM in PC.

### F-14
**Load and scale behaviour.**
- The `--rate-limiter-*` flags are parsed but unused; controllers use a hard-coded limiter (`main.go:242-246` vs `:490`, `:504`).
- Task polling at a fixed 1 s per in-flight task (`pgc:converged/v4/structs.go:221`).
- Every reconcile before Ready re-runs PC version, project and default-project lookups (2–3 identical calls), find-VM, and
  on create all lookups (≈20 calls, chapter 4 §4.3). Errors requeue with backoff but restart the whole sequence.
- `GetImageByLookup` lists **all** images on every create (`controllers/helpers.go:1368`).
- `createVm` PC rate limit is 5–20/s depending on PC size (`vmm-defs:.../vmEndpoints.yaml:481-493`); 429s are retried
  5× by the SDK, then requeued.
- Worth measuring at 100 clusters: PC API rate, task-API polling rate, reconcile latency.

### F-15
**Visibility.** Retryable failures (the majority) produce no condition, no Event, and no metric; only logs.
`VMProvisionedTaskFailed = "FailedVMTask"` exists (`api/v1beta1/conditions.go:116`) but is never set. The enriched
subtask error ends up in `failureMessage` only on the terminal path. `VMAddressesAssigned=False` uses severity
**Error** for the normal "waiting for DHCP" state (`:742`).

### F-16
**Duplicate VM names.** `FindVMByName` returns `"found more than one (%v) vms with name"` (`controllers/helpers.go:352-354`),
a plain error → retried forever. Seen when a previous run leaked a VM with the same Machine name (F-05) or across
projects on PC < 7.6.

### F-17
**Recorded VM vanished before Ready.** UUID known, `VMs.Get` 404, non-metro → `"no vm %s found with UUID %s but was
expected to be present"` (`controllers/helpers.go:330`) → retried forever. CAPX neither recreates nor fails the machine.

### F-18
**NutanixCluster Ready semantics.** `status.ready=true` is set even when failure-domain validation failed (errors only in
the `FailureDomainsValidated` condition, `controllers/nutanixcluster_controller.go:644-654`) and without any PC reachability
check. Machines then fail later, at VM creation.

### F-19
**Pause.** The machine controller calls `annotations.IsPaused(cluster, machine)` with the **CAPI Machine**
(`controllers/nutanixmachine_controller.go:253`). The contract says to honour the paused annotation on the InfraMachine
itself. There is also no `Paused` condition.

### F-20
**Hygiene.**
- The machine `Reconcile` deferred patch guard `if err == nil` checks the preamble `err`, which is always nil there, so the
  "not patching NutanixMachine since error occurred" branch is dead (`:302-314`). It always patches, which is the desired
  behaviour, but the code reads as if it does not.
- NutanixCluster delete lists NutanixMachines with `cluster-name = NutanixCluster.name` (`pkg/context/context.go:121-123`);
  correct only when NutanixCluster.name == Cluster.name.
- `ClusterCategoryCreated` on NutanixCluster is patched by the machine controller (`controllers/nutanixmachine_controller.go:3079-3127`),
  so NutanixCluster status has two writers.
- `NutanixCluster` "Waiting" returns nil when the NutanixCluster is not found (`:264-268`); it relies on the watch to fire again.
