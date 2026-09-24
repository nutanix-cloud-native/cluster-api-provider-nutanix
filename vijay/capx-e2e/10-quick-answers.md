# 10. Quick answers

Short answers to questions you are likely to get, with the reference to point at.

**Q1. What is the CAPI contract, and what does CAPX have to provide?**
CAPI core never calls Nutanix; it reads a fixed set of fields from CAPX objects. For machines: `spec.providerID`,
"provisioned" (`status.ready` in the v1beta1 contract CAPX uses), plus optional `status.addresses`,
`status.failureDomain` and conditions. For clusters: "provisioned", `spec.controlPlaneEndpoint` and
`status.failureDomains`. CAPX declares contract **v1beta1**; CAPI v1.13 supports that until ~April 2027. → [01](01-capi-contract.md)

**Q2. Walk me through cluster creation.**
Cluster → CAPI sets owner on NutanixCluster → CAPX validates FDs and marks it ready → KCP makes CP-0 (Machine +
NutanixMachine + KubeadmConfig) → the bootstrap Secret is written → CAPX creates the VM on PC, powers it on, sets
providerID and ready → CAPI copies providerID to the Machine → the VM boots, kubeadm init → CCM sets the Node
providerID → CAPI links Node ↔ Machine → CP initialized → the other CPs (one by one) and workers (in parallel) follow
the same machine path. → [02 §2.2](02-e2e-flow.md#22-timeline-of-a-fresh-cluster-create)

**Q3. What exactly triggers VM creation?**
A NutanixMachine reconcile where the Cluster infra is provisioned, `Machine.spec.bootstrap.dataSecretName` is set, and
no existing VM is found by UUID or name. `controllers/nutanixmachine_controller.go:607-623`, `:1874-1887`.

**Q4. How do we avoid duplicate VMs?**
Each NutanixMachine gets a UUID in the annotation `capx.nutanix.com/vm-creation-request-id`, persisted **before** the
first create call (`:1833-1856`). It is sent as `NTNX-Request-Id` (`:2037`), so PC returns the original task on a
retried create. Also, every reconcile first looks for an existing VM by UUID, then by name. **Gap:** the VM-profile path
does not use the key ([F-03](09-findings.md#f-03)).

**Q5. What happens if the VM-create task fails on PC?**
`Wait` returns "task failed: <msgs>". CAPX appends the failed subtasks' messages (`controllers/task_errors.go`), logs it,
and, because a task failure is classified as **retryable**, requeues with backoff. The same request id is reused, so it
keeps getting the same failed task until MHC replaces the Machine. Nothing appears on the object.
→ [F-04](09-findings.md#f-04), [05 §5.3](05-prism-client-layer.md#53-subtask-error-enrichment-capx-side)

**Q6. Do users see the real reason (e.g. no IPs left in the subnet)?**
Only if the error is classified terminal: then `status.failureMessage` contains the enriched text. For retryable errors
it is only in the controller logs. `FailedVMTask` is defined but never used. → [F-15](09-findings.md#f-15)

**Q7. What is a "terminal failure" in CAPX, and what does CAPI do with it?**
CAPX sets `status.failureReason/failureMessage` and stops reconciling that NutanixMachine (`:575-578`). Under CAPI
v1.13 (v1beta2 contract) the Machine **does not fail**; the values only show under `status.deprecated.v1beta1`. Recovery
happens only when an MHC times the Machine out (default `nodeStartupTimeout: 10m`). → [06 §6.4](06-error-handling-model.md#64-what-terminal-means-end-to-end-v1beta2-capi), [F-02](09-findings.md#f-02)

**Q8. What happens if CAPX crashes during VM creation?**
- Before the create call: the request id is persisted, so a retry reuses it. Safe.
- During the task wait: the VM may be created, but its UUID is only in memory. After restart CAPX finds it **by name**
  and continues, but **never sets `spec.providerID`** → the Machine never gets a providerID → deadlock.
  → [F-01](09-findings.md#f-01) (likely the dev33 ticket).
- After the UUID patch: safe.

**Q9. What if PC is slow, down, or rate-limiting?**
The SDK retries 408/429/503/504 five times (≤3 s apart); 500 is not retried by the SDK. After that: 429/5xx/transport
errors → CAPX requeues with exponential backoff up to ~16 min. A task that never finishes blocks a worker **forever**
(no timeout); 10 such tasks stop all machine reconciles. → [05 §5.2, §5.5](05-prism-client-layer.md), [F-06](09-findings.md#f-06)

**Q10. What if the PC password is rotated mid-provisioning?**
The client cache rebuilds on credential change, but any 401/403 seen by an in-flight create is classified
**non-retryable → terminal**. → [F-09](09-findings.md#f-09)

**Q11. Why is providerID the BIOS UUID, and why must it be in spec?**
CCM sets the Node's providerID from the VM's BIOS/system UUID; CAPI matches Machine ↔ Node on it. At create time the
VM ext-id equals the BIOS UUID, so CAPX sets `nutanix://<vm ext-id>`. It is in **spec** because status is dropped on
`clusterctl move`. Status must never be the source for spec (the regression fixed earlier). After Metro UPFO the ext-id
changes, which is why `status.vmUUID` follows `Machine.status.nodeInfo.systemUUID` (`:811-836`) while providerID stays.

**Q12. What happens on Machine delete? Can we leak VMs?**
CAPX finds the VM by recorded UUID (never by name for non-metro), waits for its running tasks, detaches volume groups,
issues the delete, and removes the finalizer once PC returns 404. If no UUID was ever recorded, the finalizer is removed
immediately and **any VM that exists leaks**. Delete-task failures are silent. → [07](07-delete-path.md), [F-13](09-findings.md#f-13)

**Q13. Is CAPX stateless?**
Yes in the controller sense: no database, all state is in the CRs (annotations, spec, status) and PC. The state it
relies on: the request-id annotation, `spec.providerID`, `status.vmUUID`, Metro labels and annotations. The risky part is
the window where the VM exists in PC but its UUID is not yet written to the CR. → [04 §4.4](04-nutanixmachine-create.md#44-what-is-persisted-and-when-crash-safety-view)

**Q14. How many VMs can CAPX create at once?**
Up to 10 NutanixMachines reconcile in parallel (`--max-concurrent-reconciles`, default 10), each blocking on its task.
PC limits `createVm` to 5–20 req/s depending on PC size. The `--rate-limiter-*` flags are ignored. → [02 §2.5](02-e2e-flow.md#25-concurrency-and-rate-limiting-as-deployed), [F-14](09-findings.md#f-14)

**Q15. Does CAPX notice a VM deleted or powered off in Prism after the machine is Ready?**
No. After Ready it only syncs the VM UUID. Detection is through the Node going NotReady and the MHC. → [F-12](09-findings.md#f-12)

**Q16. Which PC APIs does CAPX call?**
48 distinct calls: VM (create/get/list/delete/power-on/custom attributes), VM profiles, images, tasks, categories,
clusters and GPU profiles, subnets, storage containers, projects and resource groups, domain manager, volume groups,
protection policies, and v3 recovery plans / recovery-plan jobs / groups / projects / tasks.
→ [04 §4.3](04-nutanixmachine-create.md#43-pc-calls-per-reconcile-first-successful-create-pc-76-default-project-image-and-subnet-by-name-no-fd) for the per-reconcile view; the per-API analysis is Phase 1.

**Q17. Why is v3 still used?**
v4 recovery plans were not visible in the PC UI; the DR-config "decoupled" check needs v3 Groups (no v4 equivalent,
not project-scoped); v3 projects on PC < 7.6. → [08 §8.3](08-other-controllers.md#83-nutanixvirtualhadomain-metro-dr-resources--controllersnutanixvirtualhadomain_controllergo-1112-lines)

**Q18. What does "Ready" mean for NutanixCluster?**
PC clients could be built and FD/VHA reconciliation did not hard-fail. It does **not** check PC reachability, and invalid
FDs are only reported in a condition. → [F-18](09-findings.md#f-18)

**Q19. Where do we populate PE/subnet into the machine spec, and why?**
`validateMachineConfig` copies the failure domain's PE and subnets into `NutanixMachine.spec.cluster/subnets` and sets
`status.failureDomain` (`:1625-1627`). CAPX then builds the VM from the machine spec. CAPI does not care (it only reads
providerID, ready, addresses, failureDomain), but it means a spec field is written by the controller.

**Q20. Is there any timeout on VM creation?**
No CAPX-level timeout. HTTP calls time out at ~60 s each; the task wait has none. The practical upper bound is the MHC
`nodeStartupTimeout`, and even then the stuck reconcile only ends when the task ends or the pod restarts.
