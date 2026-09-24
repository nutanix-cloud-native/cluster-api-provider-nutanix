# 4. NutanixMachine controller — the create path

File: `controllers/nutanixmachine_controller.go` (3127 lines) + helpers in `controllers/helpers.go` (2829 lines).

This chapter walks one NutanixMachine from "just created" to `status.ready=true`, listing for every step:
what it does, which PC API it calls, what is persisted, and what happens on error.

Error-handling legend used in the tables:
- **Wait**: return `(Result{}, nil)`; the next reconcile comes from a watch event.
- **Requeue(err)**: return an error; controller-runtime requeues with exponential backoff (1 ms → 1000 s per item, `main.go:504`).
- **RequeueAfter(n)**: timed requeue, no error.
- **TERMINAL**: `rctx.SetFailureStatus(reason, err)` writes `status.failureReason` / `failureMessage`
  (`pkg/context/context.go:147-152`) and returns an error. Every later reconcile stops at
  `:575-578`. Recovery needs a human (delete the Machine or clear the fields). What counts as retryable
  is decided by `isRetryableAPIError` (`controllers/helpers.go:137-154`), see [chapter 6](06-error-handling-model.md).

---

## 4.1 `Reconcile` preamble — `:217-323`

| # | Step | Error behaviour | Reference |
|---|---|---|---|
| 1 | Get NutanixMachine | NotFound → stop; else Requeue(err) | `:222-233` |
| 2 | `GetOwnerMachine` | err → Requeue(err); nil → **Wait** for CAPI to set the ownerRef | `:236-244` |
| 3 | `GetClusterFromMetadata` (Cluster via the Machine's cluster-name label) | err → **returns nil** (logs "Machine is missing cluster label") — no requeue | `:248-252` |
| 4 | `annotations.IsPaused(cluster, machine)` — checks the **CAPI Machine's** annotation, not the NutanixMachine's | Wait | `:253-256` |
| 5 | Get NutanixCluster by `cluster.spec.infrastructureRef.name` | err → **returns nil** ("Waiting for NutanixCluster"); only a later watch event reconciles again | `:259-268` |
| 6 | Patch helper from the object as fetched | failure → `Requeue: true` | `:271-275` |
| 7 | v3 client + converged v4 client (cached per NutanixCluster) | Requeue(err) | `:279-289` |
| 8 | Build `MachineContext` (Cluster, Machine, NutanixCluster, NutanixMachine, both clients, empty Datastore) | — | `:291-300`; struct `pkg/context/context.go:78-96` |
| 9 | `defer`: patch the NutanixMachine (spec + status + conditions) at the end of the reconcile | patch error aggregated into the return | `:302-314` |
| 10 | Deletion timestamp → `reconcileDelete` ([chapter 7](07-delete-path.md)); else `reconcileNormal` | — | `:317-322` |

Note on step 9: the guard is `if err == nil`, where `err` is the preamble variable last assigned at `:285`.
The outcome of `reconcileNormal` goes into `reterr`, not `err`. So the deferred patch **always** runs,
including after errors (this is what persists `failureReason`); the "not patching" branch is dead code.

---

## 4.2 `reconcileNormal` — `:573-764`

### Step N0 — terminal check and finalizer

| What | Behaviour | Reference |
|---|---|---|
| `status.failureReason` or `failureMessage` set | log, return nil — **no further reconciles ever** | `:575-578` |
| Add finalizer `infrastructure.cluster.x-k8s.io/nutanixmachine`, remove the deprecated one | persisted by the deferred patch | `:583-586` |

### Step N1 — "already Ready" branch — `:589-604`

If `status.ready == true`:
- If Cluster infra is not provisioned **or `Machine.spec.providerID` is empty** → log "The NutanixMachine is ready,
  wait for the owner Machine's update." → **RequeueAfter(5 s), forever** (`:590-594`).
- Else `syncVmUUID` (prefers `Machine.status.nodeInfo.systemUUID`, `:811-836`) and return.

After Ready, CAPX **does not look at the VM again**: no existence check, no power-state check, no drift correction.
A VM deleted or powered off in PC is only noticed through the Node (MachineHealthCheck).

The `Machine.spec.providerID == ""` condition is where [F-01](09-findings.md#f-01) deadlocks: CAPI copies
`Machine.spec.providerID` from `NutanixMachine.spec.providerID`, and waits while that is empty.

### Step N2 — wait for cluster infrastructure — `:607-618`

`Cluster.status.initialization.infrastructureProvisioned` not true → condition
`VMProvisioned=False/ClusterInfrastructureNotReady` → **Wait**.

### Step N3 — bootstrap data — `ensureBootstrapRef`, `:769-806`

- `spec.bootstrapRef` already set → continue.
- `Machine.spec.bootstrap.dataSecretName == nil`:
  - worker and control plane not initialized → `VMProvisioned=False/ControlplaneNotInitialized`
  - otherwise → `VMProvisioned=False/BootstrapDataNotReady`
  - **Wait**.
- Else set `spec.bootstrapRef = Secret/<dataSecretName>` (persisted by the deferred patch).

### Step N4 — PC version — `:626-633`

PC call: `DomainManager.GetPrismCentralVersion` (every reconcile). Error → **Requeue(err)** (never terminal).
Stored in `rctx.PCVersion`. Many later decisions branch on `isPCVersionHigherThan75` (`controllers/helpers.go:1946-1952`):
projects, resource groups, project-scoped categories, GPU profiles, VM profiles.

### Step N5 — project policy and effective project — `:636-664`

- Policy from Cluster annotation `capx.nutanix.com/project-policy`, default `unrestricted` (`:636-641`; constants `controllers/helpers.go:74-79`).
- `resolveEffectiveProject` (`:2918-2965`):
  - `spec.project` set → PC ≥ 7.6: `GetProjectV4` (`Projects.Get` / `Projects.GetByName`, `controllers/helpers.go:2061-2088`); PC < 7.6: `GetProjectV3` (v3 `GetProject` / `ListAllProject`, `:2091-2134`).
  - not set, PC < 7.6 → nil (no project).
  - not set, PC ≥ 7.6 → default project via `Projects.GetDefaultProject` (`controllers/helpers.go:1955-1967`).
  - error → condition `ProjectAssigned=False/ProjectAssignationFailed`.
- `validateProjectPolicy` (`:2967-3018`):
  - `default-only`: `GetDefaultProject` again; mismatch → terminalError.
  - `single-project`: needs annotation `capx.nutanix.com/project-uuid`; missing or mismatch → terminalError.
  - unknown policy → terminalError.
- Error handling: `!isRetryableAPIError(err)` → **TERMINAL** (`createErrorFailureReason = "CreateError"`), else Requeue(err) (`:646-658`).

### Step N6 — resource group (PC ≥ 7.6 with a project) — `:670-683`

`resolveResourceGroup` (`controllers/helpers.go:1994-2017`): `GetDefaultProject` (a third call); if the effective project
is the default one → nil. Otherwise `ResourceGroups.List(filter projectExtId)`; none found → terminalError.
Non-retryable → **TERMINAL**.

When non-nil, PE and storage-container lookups are resolved through the resource group's placement targets
instead of cluster-wide APIs (`GetPEUUID` `controllers/helpers.go:559-577`, `GetStorageContainerInCluster` `:2340-2377`).

### Step N7 — `getOrCreateVM` — `:1861-2062` (the core)

#### N7.1 Find an existing VM — `FindVM`, `controllers/helpers.go:269-331`

Identifier to search by, from `GetVMUUID` (`controllers/helpers.go:223-264`), first match wins:
1. `Machine.status.nodeInfo.systemUUID` (must parse as a UUID, else error)
2. `NutanixMachine.status.vmUUID`
3. `NutanixMachine.spec.providerID` minus `nutanix://`, if it parses as a UUID (a template placeholder is ignored)
4. none → search by name

| Case | PC calls | Result |
|---|---|---|
| No UUID, non-metro | `VMs.List(filter name eq '<Machine.name>' [and projectExtId])` (`FindVMByName`, `:335-361`) | 0 → nil (create). 1 → `VMs.Get` by UUID (project check). **>1 → error "found more than one"** → Requeue(err), loops forever |
| No UUID, metro | `findNonDecoupledVMByName` (`:471-522`): list by Machine name and NutanixMachine name; each hit → v3 `GroupsGetEntities(entity_dr_config)` to skip DR-decoupled VMs; `VMs.Get` each | at most one live VM |
| UUID known | `VMs.Get(uuid)` (`FindVMByUUID`, `:195-215`). 404 → nil. PC ≥ 7.6 and VM not in project → terminalError | found: the name must equal Machine or NutanixMachine name, else error (`:300-305`) |
| UUID known but VM gone (404), non-metro | — | **error "no vm ... found with UUID ... but was expected to be present"** (`:330`) → Requeue(err) forever. CAPX does not recreate a VM it recorded |
| UUID known, metro, decoupled or 404 | name lookup as above | recovered VM or error |

If a VM is found (`:1881-1885`): `markVMProvisioned` and return it.
**`spec.providerID` is NOT set on this path** ([F-01](09-findings.md#f-01)).

#### N7.2 Idempotency key — `getOrMintVMCreationRequestID`, `:1833-1856`

- Annotation `capx.nutanix.com/vm-creation-request-id` present → reuse it.
- Else mint `uuid.NewString()`, set the annotation, and **patch immediately** with `client.MergeFrom(before)` (`:1851`), so the key is durable before any Create call.
- Patch failure → Requeue(err).
- The key is minted **before** validation (N7.3). A machine that later fails validation still carries a key; harmless.
- It is an annotation, not status, so it survives `clusterctl move` (`controllers/helpers.go:81-87`).

#### N7.3 Validate the machine config — `validateMachineConfig`, `:1610-1676`

- If `Machine.spec.failureDomain` is set:
  - resolve the FD spec (`getFailureDomainSpec`, `:979-1015`): Metro → `getMetroFailureDomainSpec` (placement balancing, recovery-plan-job lookup, fallback to the other site, `:1037-1123`); MetroSite → `:1127-1212`; legacy embedded FD → `GetLegacyFailureDomainFromNutanixCluster`; otherwise the NutanixFailureDomain CR.
  - validate the FD against PC (`GetPEUUID`, `GetSubnetUUIDList`).
  - **write the FD's PE and subnets into `NutanixMachine.spec.cluster` / `spec.subnets`** and set `status.failureDomain` (`:1625-1627`). This is the "we populate PE and subnet into the machine spec" behaviour discussed with Sid.
- At least one subnet; PE name or UUID; `systemDiskSize ≥ 20Gi`; if no VM profile: `memorySize ≥ 2Gi`, `vcpusPerSocket ≥ 1`, `vcpuSockets ≥ 1`; data-disk rules (`:1678-1788`).
- Any error → **TERMINAL** (`:1897-1900`). Note this includes transient PC errors from FD validation: any error from `validateMachineConfig` is terminal, whatever its type.

#### N7.4 Resolve PE and subnets — `GetSubnetAndPEUUIDs`, `:3059-3075`

- `GetPEUUID` (`controllers/helpers.go:559-577`): with a resource group → `ResourceGroups.ListPrismElements`; else by UUID `Clusters.Get`, or by name `Clusters.List(name eq)` keeping only clusters with the AOS function (`hasPEClusterServiceEnabled`, `:2136-2148`). 0 matches → terminalError; >1 → plain error.
- `GetSubnetUUIDList` → `GetSubnet` per subnet (`:933-1031`): by UUID `Subnets.Get` (+ project access check); by name `Subnets.List(name eq)`, keep overlay subnets or VLAN subnets attached to the PE, prefer project-owned over shared. 0 → terminalError; >1 → plain error.
- Any error → **TERMINAL** (`:1902-1907`) — again regardless of retryability. A 500 from `Subnets.List` here terminally fails the machine.

#### N7.5 VM-profile path (only if `spec.vmProfile` is set) — `deployVMFromProfile`, `:2099-2166`

1. `GetVMProfile` (project required) → `VMProfiles.Get` / `VMProfiles.List`.
2. Build `DeployVmFromVmProfileParams`: name, PE, project, default categories (get-or-create), NICs mapped to profile NIC ext-ids, categories, the system disk from the image, cloud-init guest customization (`:2223-2324`).
3. `VMProfiles.DeployVmWithVmProfile(ctx, ...)` with **plain `rctx.Context`: no idempotency key** (`:2123`).
4. `vmOp.Wait(ctx)` directly: **no subtask error enrichment** (`:2133`).
5. Sets `spec.providerID` / `status.vmUUID` in memory only; persisted by the deferred patch at the end (`:2156-2157`). Contrast with the normal path, which patches immediately.

See [F-03](09-findings.md#f-03).

#### N7.6 Build the VM spec (normal path) — `:1914-2033`

| Sub-step | PC calls | Error | Reference |
|---|---|---|---|
| Name = `Machine.name`; memory, cores/socket, sockets; HW clock UTC | — | — | `:1915-1921` |
| Custom attributes `failure-domain:<fd>`, metro `metro-preferred-pe:` / `metro-node-group-name:` | — | — | `setFailureDomainCustomAttributes` `:1793-1820` |
| Cluster ref = PE UUID; one NIC per subnet UUID | — | — | `:1927-1939` |
| Default category `KubernetesClusterName=<cluster>` get-or-create | `Categories.List(key eq and value eq)`; if absent `Categories.Create` | non-retryable → TERMINAL; also sets NutanixCluster `ClusterCategoryCreated=False` | `:1949-1963`; impl `controllers/helpers.go:1727-1777` |
| Machine categories = default + `spec.additionalCategories` + (metro) VHA-domain category | metro: `Categories.List` to validate | non-retryable → TERMINAL; metro VHA domain not Ready → plain error → Requeue | `:1965-1972`, `:2845-2874`, `controllers/helpers.go:2694-2768` |
| Category references | `Categories.List` per category; missing → terminalError | TERMINAL | `:1974-1987`, `controllers/helpers.go:1805-1844` |
| Project reference on VM (if any); condition `ProjectAssigned=True` | — | — | `addVMToProject` `:3020-3047` |
| GPUs | device mode: `Clusters.ListClusterPhysicalGPUs` + `ListClusterVirtualGPUs`, skip in-use, **pick one at random**; profile mode (PC ≥ 7.6): `ListAHVPhysicalGPUProfiles` + `ListAHVVirtualGPUProfiles` | none available → terminalError → TERMINAL | `:1999-2008`; `controllers/helpers.go:2151-2309` |
| Disks: system disk from image (`spec.image` by name/UUID, or `spec.imageLookup` regex over **all** images, newest first); image delete-in-progress check; bootstrap CD-ROM (image-kind bootstrap); data disks (image data source, storage container) | `Images.Get` / `Images.List(name eq)` / `Images.List()` (unfiltered for lookup); `Tasks.List(kImageDelete running/queued on image)`; `StorageContainers.List` or `ResourceGroups.ListStorageContainers` | non-retryable → TERMINAL | `:2010-2019`, `:2592-2721`; helpers `:1158-1446`, `:2340-2423` |
| Guest customization: read the bootstrap Secret `data.value`, strip `## template: jinja\n`, replace `{{ ds.meta_data.hostname }}` with the Machine name (AOS 7.3 workaround), base64; metadata `{"hostname","uuid":<random>}`; CloudInit ConfigDriveV2 | — | any error → TERMINAL | `:2021-2025`, `:2486-2530`, `:2724-2745` |
| Boot type: legacy/UEFI with order CDROM, DISK, NETWORK | — | invalid → condition `VMProvisioned=False/VMBootTypeInvalid` + TERMINAL | `:2027-2033`, `:2876-2914` |

The PC round trips in this step happen on **every** reconcile that reaches creation, and again whenever a
create fails and is retried.

#### N7.7 Create the VM and wait — `createAndWaitForVM`, `:2073-2089`

```
ctx' = v4Converged.WithRequestID(ctx, requestID)              :2037   (adds NTNX-Request-Id header)
op, err = VMs.CreateAsync(ctx', vm)                           :2075   → POST /api/vmm/v4.3/ahv/config/vms  → 202 + TaskReference
    err → vmCreateFailure: non-retryable → TERMINAL, else Requeue(err)       :2091-2097
vms, err = waitForConvergedOperation(ctx', client, op)        :2079   → op.Wait (poll task every 1 s) ; on failure enrich with failed subtasks
    err → vmCreateFailure (same rule)
len(vms) != 1 → TERMINAL "operation completed but expected exactly 1 VM, got N"  :2083-2087
```

Details in [chapter 5](05-prism-client-layer.md):
- `op.Wait` blocks the reconcile worker until the task ends. There is no timeout besides the reconcile context,
  and controller-runtime sets none by default.
- A failed task becomes a plain `fmt.Errorf` error → `isRetryableAPIError` returns **true** → Requeue(err),
  **not** terminal ([F-04](09-findings.md#f-04)). No condition is set, so the reason only shows in logs.
- On retry, the same request id is sent again. PC is expected to return the original (failed) task, so the
  machine loops on the same failure with backoff until MachineHealthCheck deletes the Machine.
- If the VM was created but the follow-up `VMs.Get` in `Wait` fails (e.g. read-after-write lag), the SDK wrapper
  drops the entity and error silently. CAPX then sees 0 VMs → **TERMINAL**, and the VM exists in PC without CAPX
  having recorded its UUID ([F-05](09-findings.md#f-05)).

#### N7.8 Persist the identity immediately — `:2049-2060`

```
before := DeepCopy
spec.providerID = "nutanix://<vmUUID>"      :2051
status.vmUUID   = <vmUUID>                  :2052
patchMachine(before)                         :2054   (v1beta1 patch helper: metadata+spec patch, then status patch)
markVMProvisioned → VMProvisioned=True       :2060
```

This is the **only** place (besides the profile path) that sets `spec.providerID`. If CAPX dies, or this patch
fails, between the task completing and this patch landing, the next reconcile finds the VM by name
(N7.1) and never sets providerID ([F-01](09-findings.md#f-01)).

### Step N8 — custom attribute `providerid:<uuid>` — `addCustomAttributes`, `:2170-2197`

- If any existing attribute starts with `providerid:` → skip. Otherwise `VMs.AddVmCustomAttributes(uuid, ["providerid:<uuid>"])`.
- The converged call does `GetVmById` (to get the ETag), `POST .../$actions/add-custom-attributes` with `If-Match`, and waits for the task (`pgc:converged/v4/vms.go:370-435`).
- Non-retryable → **TERMINAL with reason CreateError, even though the VM already exists**.
  An ETag race (VM changed between GET and POST) is an HTTP error whose group `VM_ETAG_MISMATCH` is not classified
  (`vmm-defs:etc/resources/errorMessages/bundles/en_US/303xx-vmEtagErrors.yaml`) → Kind nil → **TERMINAL**
  (Hypothesis — needs the real HTTP status; see [F-07](09-findings.md#f-07)).

### Step N9 — power on — `:700-706`, `powerOnVM` `:2446-2484`

- Only if `vm.powerState != ON`.
- `VMs.PowerOnVM` (GET for the ETag + `POST $actions/power-on` + returns an op) → `Wait` → `FindVMByUUID` to refresh.
- Non-retryable at any stage → **TERMINAL** with `PowerOnError`. Same ETag risk as N8.
- No subtask enrichment on the power-on task (uses `powerOnTask.Wait` directly, `:2465`).

### Step N10 — `syncVmUUID` — `:709`, impl `:811-836`

Sets `status.vmUUID` to `Machine.status.nodeInfo.systemUUID` if present and a valid UUID, else to the VM ext-id,
and patches immediately if it changed. (After UPFO the VM ext-id changes; the Node's system UUID is the stable one.)

### Step N11 — failure domain status and Metro category check — `:716-729`

- `checkFailureDomainStatus` (`:839-909`): re-resolves the FD spec and checks that `spec.cluster` / `spec.subnets`
  are consistent with it (metro: compares network keys layer|VLAN|CIDR via `Subnets.Get/List`,
  `controllers/helpers.go:1047-1125`); inconsistent → Requeue(err) forever; else `status.failureDomain = fd`.
- `checkVHADomainCategory` (`:958-977`): a Metro VM must carry exactly one `k8s-vha-native-site` category from this
  cluster's VHA domains (`Categories.List` per VHA category); otherwise Requeue(err) forever.
- `patchMachine(beforeFailureDomainAndVHACheck)` (`:732-736`).

### Step N12 — addresses — `assignAddressesToMachine`, `:2825-2843`

- From each NIC: static `ipv4Config.ipAddress`, else `ipv4Info.learnedIpAddresses` (SR-IOV falls back to the
  deprecated `networkInfo`), `:2791-2823`. Adds `Hostname=<vm name>`.
- **No IPs yet → error "unable to determine network interfaces ... Retrying"** → condition
  `VMAddressesAssigned=False/VMAddressesFailed` (severity Error) → Requeue(err). The whole flow from N4 re-runs
  on every retry until DHCP / guest tools report an IP.
- The addresses come from the `vm` object returned by create/find/power-on. The NIC list endpoint that reliably
  returns learned IPs (`pgc:converged/v4/vms.go:676-701` `ListNicsByVmId`) is not used.

### Step N13 — Ready — `:752-763`

`VMAddressesAssigned=True`, `status.ready = true`. The deferred patch persists it. CAPI then picks it up
([chapter 2](02-e2e-flow.md), B5).

---

## 4.3 PC calls per reconcile (first successful create, PC 7.6, default project, image and subnet by name, no FD)

| Order | Call | Count |
|---|---|---|
| N4 | `DomainManager.GetPrismCentralVersion` | 1 |
| N5 | `Projects.GetDefaultProject` | 1 |
| N6 | `Projects.GetDefaultProject` | 1 |
| N7.1 | `VMs.List(name)` | 1 |
| N7.4 | `Clusters.List(name)`, `Subnets.List(name)` | 1 + 1 per subnet |
| N7.6 | `Categories.List` (+ `Categories.Create` once), `Categories.List` per category, `Images.List(name)`, `Tasks.List(kImageDelete)` | ~4–5 |
| N7.7 | `VMs.CreateAsync` + `GetTaskById` **once per second** until done + `VMs.Get` | 2 + task duration in seconds |
| N8 | `GetVmById` + `AddVmCustomAttributes` + task polls + `VMs.Get` | 3 + polls |
| N9 | `GetVmById` + `PowerOnVm` + task polls + `VMs.Get` + `VMs.Get` | 4 + polls |

About 20 calls plus one task poll per second per in-flight VM. Every requeue before Ready repeats N4–N7.1 at least.
PC-side rate limit for `createVm`: 5/s (xsmall) to 20/s (large) (`vmm-defs:versioned/v4/modules/ahv/released/api/vmEndpoints.yaml:481-493`).

## 4.4 What is persisted, and when (crash-safety view)

| Moment | Persisted on the API server | Survives CAPX crash? | Survives `clusterctl move`? |
|---|---|---|---|
| After N3 | finalizer, `spec.bootstrapRef` (deferred patch) | only if that reconcile ended | yes |
| N7.2 | annotation `vm-creation-request-id` (immediate patch) | yes | yes |
| N7.3 | `spec.cluster`, `spec.subnets`, `status.failureDomain` from the FD (deferred patch) | only if the reconcile ended | spec yes, status no |
| Between CreateAsync and N7.8 | **nothing** — the VM may exist in PC while CAPX holds its UUID only in memory | **no** | — |
| N7.8 | `spec.providerID`, `status.vmUUID` (immediate patch) | yes | providerID yes, vmUUID no |
| N10–N13 | `status.vmUUID`, addresses, conditions, `status.ready` | yes | status no |

The window between CreateAsync and N7.8 is the important one: the task wait can take minutes, and a crash or
leader change there leaves a VM that CAPX can find only by name.
