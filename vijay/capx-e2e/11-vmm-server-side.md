# 11. The server side of VM create: metropolis, Ergon, Narsil

Chapters 4–6 describe what CAPX and prism-go-client do. This chapter follows the same `POST /vmm/v4.x/ahv/config/vms`
into the Prism Central (PC) code, to answer the questions chapter 9 left as hypotheses: what a replayed request id
returns, how long request ids are remembered, what a FAILED task means, and which task states CAPX can actually see.

## 11.1 Where the code lives

All server code is in the `main` monorepo (`~/go/src/github.com/main`), which has one long-lived branch per component.
The local checkout is `gateway-master`, which contains none of this. Read it without checking out, e.g.
`git show origin/ctrl-plane-pc-master:<path>`.

| Piece | Runs on | Branch / path | Commit read |
|---|---|---|---|
| VMM v4 API server ("metropolis", Go) | PC | `origin/ctrl-plane-pc-master` `ctrl_plane_pc_server/metropolis/server/go/metropolis_server/` | `302b06fb` |
| VMM v4 generated server/client code | PC | same branch, `ctrl_plane_pc_client/ntnx_api_vmm/go/` | `302b06fb` |
| Ergon (task service) + v4 Tasks API | PC and PE | `origin/ergon-master` `ergon_server/go_ergon/server/go/go_ergon_server/` | `f91b4c83` |
| Ergon Go task library (`ergon/task`) | used by metropolis | `origin/ergon-master` `ergon_client/ergon/go/ergon/task/` | `f91b4c83` |
| Narsil (VM service on PE) | PE | **not in this repo** — `origin/ctrl-plane-master` `ctrl_plane_server/narsil/` only pins a prebuilt binary (`narsil_1.10.1039`) | — |
| Acropolis / Anduril / Uhura (PE VM plumbing) | PE | `origin/ctrl-plane-master` `ctrl_plane_server/{acropolis,anduril,uhura}/` | `fd5329b0` |
| ESXi VM API (different server) | PC | `origin/ctrl-plane-pc-master` `ctrl_plane_pc_server/uhura_pc/.../grpc/ntnx-api-esxi-vmm/` | `302b06fb` |

Notation used below: `mp:` = `ctrl_plane_pc_server/metropolis/server/go/metropolis_server/`,
`vmc:` = `mp:grpc/ntnx-api-vmm/vmm/v4/ahv/config/vm_create_task.go`,
`erg:` = `ergon_server/go_ergon/server/go/go_ergon_server/`, `erglib:` = `ergon_client/ergon/go/ergon/task/task_util.go`.

## 11.2 The path, end to end

```
CAPX  POST /vms  (header NTNX-Request-Id = annotation value)
  │
  ▼  API gateway (Adonis) → gRPC
metropolis  VmmServiceServer.CreateVm                                   mp:grpc/ntnx-api-vmm/server/vm_service.go:633
  ├─ requestHandlerWithGrpcStatus: reject if V4 not enabled             :168
  ├─ requestHandler                                                     :193
  │   ├─ GetTaskProto: NTNX-Request-Id → ergon Task.request_id          mp:grpc/ntnx-api-vmm/util/request_util.go:125, :255
  │   │    missing header → 30400, not a UUID / several → 30401
  │   └─ StartWithContext(task)                                         vm_service.go:283 → erglib:924
  │        ├─ StartHook()  ← runs on EVERY request, replay included     vmc:220
  │        │    new VM UUID, new Narsil request id, add VM to entity_list, validation, WAL
  │        └─ Ergon TaskCreate(request_id)                              erglib:944, :994
  │             task UUID = UUIDv5(request_id)                          erg:ops/task_create.go:255
  │             already exists → return existing task, write nothing    :160, :586
  │             any error → metropolis process exits (glog.Fatal)       erglib:996-999
  └─ 202 + task ext id   (CAPX starts polling /prism/v4/config/tasks/{id})

metropolis task executor (async, serialized per VM UUID)                vmc:2477-2488
  CreateVmTask.Run                                                      vmc:982
  ├─ re-validate, placement, RBAC, quota, image checkout (pre-checks)   vmc:982-1141
  ├─ if WAL has no Narsil task UUID:
  │    narsilCreateVm → PE Narsil gRPC CreateVm (120 s deadline)         vmc:1568, :1596; mp:common/consts.go:37
  │      sends X-New-Vm-Uuid + the WAL's Narsil request id              vmc:502-508
  │    save Narsil task UUID in WAL                                     vmc:1632
  ├─ PollTask(Narsil task) — no deadline                                vmc:1505 → mp:grpc/task/base_task.go:685
  └─ return error → Ergon task FAILED; return nil → SUCCEEDED           erglib:560-614, :777
```

Two task layers exist: the **PC metropolis task** (the one CAPX polls) and the **PE Narsil task** it waits on. CAPX
never sees the Narsil task directly; its failure is copied into the PC task's error details (`base_task.go:685-711`).

## 11.3 Answers to the open questions

### Q1. What does replaying the same `NTNX-Request-Id` return? — **Verified**

The original task, in whatever state it is in (RUNNING, SUCCEEDED or **FAILED**).

- Ergon derives the task UUID as UUIDv5(request_id) (`erg:ops/task_create.go:251-256`). A second create with the same
  request id hits the same UUID, the DB write fails with a CAS error, which Ergon treats as "already exists"
  (`:586-591`), and returns the existing task's ext id without modifying it (`:159-170`).
- A "collision" error is only returned if the stored task has a *different* request id (`:538-555`) — practically
  impossible, since the UUID is derived from the request id.
- The VM UUID and Narsil request id that the replayed `StartHook` generated are thrown away; the original task keeps
  its own. So a replay can never start a second VM create while the original task is still stored.

**Catch:** `StartHook` (validation) runs *before* `TaskCreate` (`erglib:935-940`). A replay is re-validated against
current state. If something changed since the first call (cluster, project, image, subnet removed; a new
`servicesRequiringVmType` rule; V4 disabled during upgrade), the replay returns a synchronous 4xx/5xx
**instead of** the original task — even if the original already succeeded. CAPX then classifies that error on its own
(chapter 6), and a 4xx will likely go terminal on a machine whose VM may exist.

### Q2. How long is a request id remembered? — **Verified (defaults; real PCs may override gflags)**

As long as the Ergon task exists. Ergon's scanner runs every 30 min (`erg:common/gflags/gflags.go:254`) and deletes
completed tasks:

- if there are more than **4,000** completed tasks in the DB, any completed more than **1 hour** ago
  (`gflags.go:204`, `:249`; `erg:scanner/scanner.go:156-167`);
- otherwise, those completed more than **2 weeks** ago (`gflags.go:245`; `scanner.go:172-181`).

A busy PC (many clusters, many CAPX machines, DR, Calm) easily exceeds 4,000 completed tasks, so the practical
dedup window is **1–1.5 hours after the task completes**. After that, the same request id creates a **new task and a
new VM**. Running tasks are never deleted, so an in-flight create is always protected.

### Q3. Which task states can CAPX see, and with what errors? — **Verified**

v4 Tasks API mapping (`erg:task_gateway/api_svc/v4/providers/ergon.go:57-65`, `:838-848`):

| Ergon | v4 `status` | How a VM-create task gets there |
|---|---|---|
| kQueued | QUEUED | Before the metropolis executor picks it up |
| kRunning | RUNNING | During pre-checks and while waiting on Narsil |
| kSucceeded | SUCCEEDED | `Run()` returned nil |
| kFailed | FAILED | `Run()` returned an error |
| kAborted | CANCELED | **Only by a manual/support abort.** Create-VM tasks do not register the cancel capability, so `isCancelable=false` and the API cannot cancel them (`ergon.go:741`) |
| canceled flag on a pending task | CANCELING | Same — only via a manual path |
| kSuspended | SUSPENDED | Not used by the create path |

For FAILED, `errorMessages` is **never empty**: if the task has no structured error details, the gateway adds a generic
"legacy" error and puts the raw `Response.ErrorDetail` in `legacyErrorMessage` (`ergon.go:1044-1066`). The create task
also attaches structured details on almost every error path (checked: `validateClusters`, `getClusterUuid`,
`initialiseTaskPolicyUtil`, `narsilCreateVm` all call `SetTaskErrorDetails`).

### Q4. Does FAILED mean "no VM was created"? — **No. Verified for the PC side; the PE side is a hypothesis**

Metropolis calls Narsil with a 120 s deadline (`vmc:1596`, `mp:common/consts.go:37`). If that call returns *any*
error — deadline exceeded, connection reset, PE restart — the PC task is marked FAILED with an internal error
(`vmc:1597-1602`). Nothing records that the outcome is unknown. Narsil may well have accepted the request and created
the VM (UUID = the one in the task's `entitiesAffected`). Whether Narsil rolls back on a caller timeout can't be read
here — Narsil's source is not in this repo.

So a FAILED create task has two meanings: "rejected before anything happened" (most errors, including pre-checks and
Narsil task failures) and "outcome unknown" (the gRPC error path).

### Q5. What happens if metropolis crashes mid-create? — **Verified**

It recovers cleanly, from the WAL stored in the Ergon task:

- Crash before the Narsil call → on restart, `Run()` re-runs the pre-checks and calls Narsil with the **same** Narsil
  request id and VM UUID from the WAL (`vmc:229`, `:262`, `:502-508`). Narsil de-dup on that id is assumed (hypothesis).
- Crash after the Narsil task UUID is saved → on restart, `Run()` skips the call and just polls it (`vmc:1142`, `:1496`).
- Metropolis crashes *on purpose* if Ergon `TaskCreate` fails (`erglib:996-999`) or if polling the Narsil task
  returns an error (`base_task.go:690-691`). CAPX sees a 5xx or a connection error during the create call. A retry with
  the same request id is safe.

### Q6. Can a PC create task stay RUNNING forever? — **Verified on the PC side**

Yes. `PollTask` has no deadline (`base_task.go:685-711`), unlike batch restores which have 60 s / 15 min bounds
(`:714-727`). If the Narsil task on PE never completes (PE disconnected, Narsil wedged, replication stalled), the PC
task stays RUNNING, and CAPX's `Wait` (no deadline, F-06) waits with it.

### Q7. When does the VM UUID become visible? — **Verified**

When the task is created. `StartHook` generates the VM UUID and adds it to the task's `entity_list` (`vmc:262-269`),
with auto entity-list updates enabled (`vmc:390`). The UUID is in `entitiesAffected` from the very first `GET` of the task
— while it is QUEUED or RUNNING, not only on success.

## 11.4 What this changes in chapter 9

| Finding | Change |
|---|---|
| **F-01** providerID not set on found-VM path | **Fix option confirmed.** CAPX can read the VM UUID from `entitiesAffected` in the first task `GET` and persist it (annotation or `spec.providerID`) **before** waiting. Then a restart during `Wait` loses nothing. Setting providerID on the found-VM path is still needed. |
| **F-04** FAILED create treated as retryable | **Confirmed.** A replay returns the same FAILED task, so CAPX loops until Ergon deletes the task (1 h – 2 weeks), after which the retry creates a *new* VM. The design direction "FAILED = terminal for this NutanixMachine" is right, but see S-02: before going terminal, CAPX should check whether the VM UUID from `entitiesAffected` exists and delete it (or adopt it). |
| **F-06** task wait hangs | Partly downgraded. "FAILED without error messages" can't happen through the v4 Tasks API. CANCELED only happens on manual abort, but support aborts stuck tasks, so CAPX must still handle it. SUSPENDED isn't used for create. **No deadline is still the main risk**, and the server has none either (Q6). |

## 11.5 New findings

| ID | Title | Sev | Status |
|---|---|---|---|
| S-01 | Request-id dedup lasts only ~1 h after completion on a busy PC | High | Verified (defaults) |
| S-02 | A FAILED create task can still leave a VM on PE | High | PC side verified; PE behaviour hypothesis |
| S-03 | A replay is re-validated and can return a new error instead of the original task | Medium | Verified |
| S-04 | Metropolis exits on Ergon errors → transient 5xx/connection errors on create | Low | Verified |

### S-01
Evidence: Q2. Impact on CAPX: a NutanixMachine that retries a create more than ~1 h after the task completed (controller
down for over an hour, long backoff, `clusterctl move`, pause/unpause) sends a request id Ergon no longer knows. PC
creates a second VM. CAPX's name lookup (`FindVM`) usually catches a succeeded create first, but not if the first VM has a
different name or the lookup itself errors. Direction: treat the request id as a short-lived guard only; persist the VM
UUID from the task (F-01 fix) and look the VM up by UUID before any create.

### S-02
Evidence: Q4. Impact on CAPX: once F-04 is fixed and FAILED becomes terminal, a VM can leak on the gRPC-timeout path —
most likely on slow or overloaded PEs, which is also where 120 s gets exceeded. Direction: on FAILED, read the VM UUID from
`entitiesAffected`, `GET` it, and delete it if found, before marking terminal. **Ask the VMM/Narsil team:** does Narsil
roll back a create whose caller timed out, and does Narsil de-dup on the request id metropolis sends?

### S-03
Evidence: Q1, `erglib:935-940`, `vmc:220-392`. Scenario: CAPX creates a VM, the task succeeds, CAPX restarts before
recording anything, and meanwhile the image named in the machine template is deleted (common with image rotation).
Replay → `StartHook` → image checkout fails → 4xx. CAPX classifies the 4xx as terminal and fails a machine whose VM
exists. Direction: before replaying a create, `GET` the task by its expected UUID. It is deterministic
(UUIDv5 of the request id), or CAPX can store the task ext id from the first response.

### S-04
Evidence: Q5. Low severity because CAPX retries 5xx with the same request id, which is safe. Noted so the error
catalogue lists "connection reset / 503 on create" as expected and retryable.

## 11.6 Questions for the VMM / Narsil team

1. Does Narsil de-dup `CreateVm` on the `NTNX-Request-Id` metropolis forwards, and for how long?
2. If metropolis's 120 s call to Narsil times out, can the VM still be created? Is it cleaned up?
3. Are `go_ergon_completed_task_retention_secs` / `go_ergon_max_completed_task_count_in_db` overridden on production PCs?
4. Is there any supported way to fetch a task by request id (other than computing UUIDv5 on the client)?
5. Under what conditions does support abort a create-VM task, and should clients expect CANCELED?
