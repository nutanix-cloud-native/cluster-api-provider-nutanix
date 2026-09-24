# 7. Delete path

## 7.1 Who triggers a NutanixMachine delete

- Scale down (MachineSet / KCP), rolling upgrade, MHC remediation, or cluster delete → CAPI deletes the **Machine**.
- CAPI Machine controller `reconcileDelete` (`capi:internal/controllers/machine/machine_controller.go:465`) drains
  the node, waits for volume detach, deletes the bootstrap config, then deletes the infra object
  (`reconcileDeleteInfrastructure`, `:1051`) and waits until it is gone.
- The NutanixMachine gets a deletion timestamp. The CAPX finalizer holds it until CAPX removes the VM.

## 7.2 `reconcileDelete` step by step — `controllers/nutanixmachine_controller.go:325-467`

The terminal-failure check lives only in `reconcileNormal`, so **delete always runs**, even for machines with `failureReason`.

| # | Step | PC calls | Outcome | Reference |
|---|---|---|---|---|
| 1 | Condition `VMProvisioned=False/Deleting` | — | — | `:331-336` |
| 2 | No project resolution on purpose (a project lookup failure must not block deletion). `rctx.PCVersion` is empty on this path, so later lookups are unscoped | — | — | `:338-341` |
| 3 | `GetVMUUID` (systemUUID → status.vmUUID → spec.providerID) | — | invalid UUID → error requeue | `:342-347` |
| 4 | **UUID empty → remove finalizer, done.** No search by name | — | any VM created but never recorded **leaks** | `:350-356` |
| 5 | `vmToDelete`: non-metro → `FindVMByUUID`; metro → DR-aware (below) | `VMs.Get`; metro: v3 Groups + `VMs.List` by name | error → `VMProvisioned=False/DeletionFailed` + requeue | `:358-370`, `:481-523` |
| 6 | Metro: recorded VM decoupled and no recovered VM yet → RequeueAfter 5 s | — | — | `:371-374` |
| 7 | VM not found (404) → remove finalizer, done | — | — | `:376-381` |
| 8 | Name check: VM name must be the Machine name or NutanixMachine name (older CAPX named VMs after the NutanixMachine) | — | mismatch → error requeue, forever | `:383-398` |
| 9 | Any RUNNING/QUEUED task on the VM? | `Tasks.List(entitiesAffected has vm and status RUNNING|QUEUED)` (`controllers/helpers.go:1448-1496`) | yes → RequeueAfter 5 s | `:402-420` |
| 10 | VM has volume-group-backed disks → detach them, RequeueAfter 30 s | `VolumeGroups.DetachFromVM` | error → `VolumeGroupDetachFailed` + requeue | `:422-448`, `controllers/helpers.go:2552-2576` |
| 11 | `DeleteVM` → `VMs.DeleteAsync` (GET for ETag + `DELETE`). **The task is not waited on** | `GetVmById`, `DeleteVmById` | error → `DeletionFailed` + requeue | `:451-464`, `controllers/helpers.go:157-179` |
| 12 | RequeueAfter 5 s; the next pass reaches step 7 (404) or step 9 (delete task running) | — | — | `:465-466` |

Notes:
- **Delete-task failures are invisible.** CAPX never inspects the delete task. If it fails, the next pass finds the VM
  again with no running task and issues another delete, every 5 s, with no condition explaining why.
- **VG detach does one VG per pass.** `detachVolumeGroupsFromVM` returns after the first VG-backed disk
  (`controllers/helpers.go:2572` `return nil` inside the loop). With N volume groups, deletion takes at least N × 30 s. The
  detach task is not checked either; the loop just re-reads the VM.
- **Categories are not touched per VM**; deleting the VM removes its category associations on PC.
- The delete `DELETE` call uses a random request id (SDK default) and the VM's ETag. An ETag race returns
  `VM_ETAG_MISMATCH`: an unclassified APIError, but the delete path never goes terminal, so it just retries.

## 7.3 Metro / DR-aware delete (`vmToDelete`, `resolveRecoveredVMForDelete`)

Background: during a Metro unplanned failover (UPFO) DR recovers the VM on the other site with a **new ext-id**, and
the original VM becomes **decoupled** (owned by DR). CAPX must delete the recovered VM, never the decoupled one.

| Step | Behaviour | Reference |
|---|---|---|
| Metro path gate | `useMetroDRDeletePath`: Metro/MetroSite FD, active-placement annotation, or the skipped/recovered annotations (`controllers/helpers.go:385-405`) | non-metro stays UUID-only so a v3 Groups denial cannot block deletes |
| Is the recorded UUID decoupled? | v3 `GroupsGetEntities(entity_type=entity_dr_config, filter entity_uuid=in=<uuid>, attr role)`; `kDecoupled` → decoupled (`controllers/helpers.go:417-447`) | |
| Decoupled | annotate `capx.nutanix.com/skipped-decoupled-vm-uuid`; look for a recovered VM (`:497-501`) | |
| Recovered VM | annotation `capx.nutanix.com/recovered-vm-uuid` if set (re-check it is not decoupled too); otherwise list by name and pick the single non-decoupled VM (`:527-563`) | |
| Recorded UUID gone (404) | DR probably deleted the decoupled leftover → find the migrated VM by name (`:519-522`) | |
| Recorded UUID lookup errors | try by name; if nothing found, return the original error (`:503-514`) | |

## 7.4 NutanixCluster delete

See [chapter 3 §3.4](03-nutanixcluster-controller.md#34-reconciledelete--270-346). Order: wait for all NutanixMachines →
delete VHA domains and wait → delete cluster categories → drop PC clients from cache → release credential Secret and
CA ConfigMap → remove finalizer.

## 7.5 Delete-path failure scenarios (current behaviour)

| Scenario | What happens | Visible? |
|---|---|---|
| PC unreachable during delete | Step 5 errors → requeue with backoff up to ~16 min; the Machine delete hangs | `DeletionFailed` condition |
| Credential Secret deleted before the cluster | The preamble cannot build clients → every machine and cluster delete errors forever | `PrismClientInit=False` on the NutanixCluster |
| VM renamed in PC | Name mismatch (step 8) → error forever | only in logs (returned error, no condition) |
| VM stuck with a never-ending task | Step 9 requeues every 5 s forever | no condition |
| VM delete task fails (e.g. VM protected, host issue) | Re-issued every 5 s, reason never surfaced | no |
| VM created but UUID never recorded ([F-05](09-findings.md#f-05); also [F-01](09-findings.md#f-01) after a `clusterctl move`, which drops status.vmUUID) | Step 4 removes the finalizer → **orphan VM** in PC | no |
| Category value still attached to VMs at cluster delete | Delete error swallowed, cluster delete continues → category left behind | log only |
