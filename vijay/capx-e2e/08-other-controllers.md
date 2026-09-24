# 8. The other controllers (summary)

All are registered in `main.go:498-538` with the machine-controller options (10 workers each, same rate limiter).

## 8.1 NutanixFailureDomain — `controllers/nutanixfailuredomain_controller.go`

- **No PC calls.** It is a Kubernetes-side guard.
- Watches FDs plus Machines, NutanixMetros and NutanixMetroSites that reference them (`:70-169`).
- Normal: add finalizer `infrastructure.cluster.x-k8s.io/nutanixfailuredomain` (`:318`).
- Delete: blocks while any Machine, Metro or MetroSite still references the FD; condition
  `FailureDomainSafeForDeletion=False/FailureDomainInUse` (`:252-316`).
- PC validation of an FD happens in the **NutanixCluster** controller (chapter 3) and again in the **NutanixMachine**
  controller at VM creation (chapter 4, N7.3), not here.

## 8.2 NutanixMachineTemplate — `controllers/nutanixmachinetemplate_controller.go`

- **No PC calls.** Computes `status.capacity` (CPU = sockets × cores, memory, GPU count) from the inline spec for
  cluster-autoscaler scale-from-zero (`:83-107`).
- A defaulting webhook runs at admission (`api/v1beta1/nutanixmachinetemplate_webhook.go`, registered `main.go:484-486`).

## 8.3 NutanixVirtualHADomain (Metro DR resources) — `controllers/nutanixvirtualhadomain_controller.go` (1112 lines)

Created by the NutanixCluster controller for each metro the cluster uses (chapter 3 §3.3.2). It owns the PC-side DR
objects that make Metro failover work.

| Aspect | Behaviour | Reference |
|---|---|---|
| Preamble | get object; patch helper with deferred patch; paused annotation; Cluster from labels; NutanixCluster; ensure controller ownerRef; build v3 + converged clients | `:117-224` |
| Resync | `RequeueAfter: vhaResyncInterval` = **5 min** after a successful normal reconcile (periodic drift check) | `:66`, `:227` |
| Normal | `ensureVHADomainPCResources`: per movement group, get-or-create **categories** (`k8s-vha-native-site=<value>` per site), a **protection policy** (v4 DataPolicies), and one **recovery plan** per site (**v3 API**, then v3 task wait) | `:287-357`, `:359-483`, `:647-956` |
| Validation | `validateVHADomainPCResources`: the categories, protection policy (`ProtectionPolicies.Get`) and recovery plans (v3 `GetRecoveryPlan`) must still exist; else `VHADomainPCResourcesValidated=False` and `status.ready=false` | `:485-621` |
| Ready | `status.ready` = protection policy set and every movement group has category↔recovery-plan mappings | `:332-343` |
| Delete | delete recovery plans (v3 + wait), protection policy, categories; condition `VHADomainSafeForDeletion` | `:230-285`, `:958-1068` |
| Lookups by name | `findProtectionPolicyByName` (v4 list), `findRecoveryPlanByName` (v3 `ListAllRecoveryPlans name==`) — how it re-attaches to objects created before a crash | `:1084-1110` |

Why v3: recovery plans are created with v3 because v4 recovery plans were not visible in the PC UI (Abhay, standup
2026-09-10). The metro groups lookup also has no v4 equivalent for projects.

Machine-side coupling: a Metro NutanixMachine will not create its VM until its VHA domain is `Ready` and the
preferred site's category exists in PC (`controllers/helpers.go:2694-2768`), because the category can only be applied
at create time.

## 8.4 NutanixMetro / NutanixMetroSite — `controllers/nutanixmetro_controller.go`, `nutanixmetrosite_controller.go`

- Kubernetes-side validation and finalizers for the metro topology objects (two FDs per metro; a MetroSite points to
  a metro and a preferred FD).
- Conditions `metroValidated` / `metroSiteValidated`, `metroSafeForDeletion` / `metroSiteSafeForDeletion`
  (`api/v1beta1/conditions.go:57-91`).
- Delete is blocked while Machines, MachineDeployments or NutanixClusters reference them (`nutanixmetro_controller.go:341`,
  `nutanixmetrosite_controller.go:273`).

## 8.5 MetroScaleDownBalancer — `controllers/nutanixmetro_scaledown_controller.go`

- For worker MachineSets on a stretched NutanixMetro FD: when scaling down, marks victim Machines with CAPI's
  delete-machine annotation so both sites stay balanced (`:45-64`, `selectVictims` `:245`, `applyDeleteAnnotations` `:333`).
- Site of each machine comes from the NutanixMachine label `metro.nutanix.com/native-failuredomain` (`:389`).
- No PC calls.

## 8.6 Metro placement inside the NutanixMachine controller (for reference)

`getMetroFailureDomainSpec` (`controllers/nutanixmachine_controller.go:1037-1123`):
1. If the machine already has the `metro.nutanix.com/native-failuredomain` label → reuse that FD (placement decided before).
2. Otherwise `computeMetroPlacementIndex` (`:1424-1501`): read siblings **uncached** (APIReader), group by MachineSet →
   MachineDeployment → MachinePool → control plane, count placed machines per FD, sort pending machines by name,
   greedy least-count. Deterministic, so concurrent reconciles agree without locking.
3. Check the latest recovery-plan job (v3 `ListRecoveryPlanJobs`, `:1296-1332`) to see whether the site is currently
   failed over; if so place on the active PE and set `MetroRecoveryPlacement=True` + annotation
   `metro.nutanix.com/active-placement-pe` (`:1214-1260`, `:1366-1401`).
4. Validate against PC; on failure try the other FD (`:1106-1117`).
