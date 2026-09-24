# 1. The Cluster API contract and how CAPX implements it

## 1.1 What "the contract" is

Cluster API (CAPI) core controllers (Cluster, Machine, MachineSet, MachineDeployment, KubeadmControlPlane)
never talk to Nutanix. They create and watch *provider* objects (`NutanixCluster`, `NutanixMachine`)
through unstructured references and read a small, fixed set of fields from them. That fixed set of
fields and behaviours is the **contract**. Anything outside it is CAPX's own business.

Source of truth for the rules: `capi:docs/book/src/developer/providers/contracts/infra-cluster.md` and
`capi:.../contracts/infra-machine.md`.

There are two contract versions in play:

| Contract | Used by | Key fields |
|----------|---------|-----------|
| **v1beta1** (deprecated) | CAPX today | `status.ready`, `status.failureReason`, `status.failureMessage`, `status.failureDomains` as a map, v1beta1 conditions |
| **v1beta2** (current since CAPI v1.11) | CAPI v1.13 core | `status.initialization.provisioned`, conditions (`Ready` mirrored), **no terminal failures**, `status.failureDomains` as a list |

**CAPX declares the v1beta1 contract only:**
- CRD label `cluster.x-k8s.io/v1beta1: v1beta1` — `config/crd/kustomization.yaml:46`
- `metadata.yaml` maps every release (including `0.0`) to `contract: v1beta1`

CAPX *compiles* against CAPI v1.13.6, whose core types are v1beta2 (`go.mod`, `main.go:49`), but the
objects it exposes to CAPI still follow v1beta1. CAPI v1.13 keeps a compatibility layer for this:
it reads `status.ready` when `status.initialization.provisioned` is absent. **That compatibility is
tentatively removed in April 2027** (stated in both contract docs, e.g. `infra-machine.md` "InfraMachine:
initialization completed" section). That is one month after the NKP 2.21 target (March 2027).

## 1.2 Northbound: who reads what

```
 CAPI Cluster controller  ──reads──>  NutanixCluster.status.ready (v1beta1)       -> Cluster.status.initialization.infrastructureProvisioned
                                       NutanixCluster.spec.controlPlaneEndpoint    -> Cluster.spec.controlPlaneEndpoint
                                       NutanixCluster.status.failureDomains        -> Cluster.status.failureDomains
 CAPI Machine controller  ──reads──>  NutanixMachine.status.ready (v1beta1)       -> Machine.status.initialization.infrastructureProvisioned
                                       NutanixMachine.spec.providerID              -> Machine.spec.providerID
                                       NutanixMachine.status.addresses             -> Machine.status.addresses
                                       NutanixMachine.status.failureDomain         -> Machine.status.failureDomain
                                       NutanixMachine.status.conditions            -> Machine InfrastructureReady (mirrored)
                                       NutanixMachine.status.failureReason/Message -> Machine.status.deprecated.v1beta1 ONLY (no effect)
```

How CAPI Machine controller reads NutanixMachine (verified), `capi:internal/controllers/machine/machine_controller_phases.go:244-377`:
1. Get the infra object; if missing and the Machine was already provisioned, mark deprecated failure (`:260-274`).
2. Resolve the contract version from the CRD label (`:280`).
3. Read `provisioned` via the contract helper (`:286-293`). For v1beta1 that is `status.ready`.
4. Mirror the infra `Ready` condition into `Machine.InfrastructureReady` (`:302`).
5. If not provisioned → return and wait (`:309-317`).
6. **Read `spec.providerID`. If empty → log "Waiting for infrastructure provider to set spec.providerID" and wait** (`:319-330`).
7. Copy addresses, failure domain; set `Machine.spec.providerID` and `infrastructureProvisioned=true` (`:332-376`).

Step 6 matters: **Ready=true with an empty providerID is a state CAPI will wait on forever.** See [finding F-01](09-findings.md#f-01).

Then `capi:internal/controllers/machine/machine_controller_noderef.go:59` (`reconcileNode`) looks up the
workload-cluster Node whose `spec.providerID` equals `Machine.spec.providerID` (`getNode`, `:218-242`)
and sets `Machine.status.nodeRef`. The Node's providerID is set by the Nutanix cloud-controller-manager
(CCM) on the workload cluster, **from the VM's BIOS UUID**. That is why CAPX's providerID must be
`nutanix://<vm-uuid>` where the VM UUID equals the BIOS/system UUID at create time.

## 1.3 Rule-by-rule compliance table

### InfraCluster (`NutanixCluster`)

| Rule (v1beta2 doc) | Mandatory | CAPX today | Reference |
|---|---|---|---|
| Namespaced, TypeMeta/ObjectMeta, List type | Yes | Yes | `api/v1beta1/nutanixcluster_types.go:124-138,238` |
| CRD contract label | Yes | `v1beta1` only | `config/crd/kustomization.yaml:46` |
| Control plane endpoint in `spec.controlPlaneEndpoint` | If provider supplies it | User supplies it (kube-vip in templates). CAPX does not allocate an endpoint | `api/v1beta1/nutanixcluster_types.go:55`, `templates/cluster-template.yaml:446` |
| Failure domains in `status.failureDomains` | No | Yes, v1beta1 **map** form, only validated FDs included | `controllers/nutanixcluster_controller.go:524-663` |
| Initialization completed | Yes | `status.ready=true` (v1beta1). No `status.initialization.provisioned` | `controllers/nutanixcluster_controller.go:375-381` |
| Conditions | No | v1beta1 conditions + a parallel v1beta2 condition list. **No `Ready` summary condition** | grep: no `SetSummary` anywhere in controllers |
| Terminal failures | No | `SetFailureStatus` exists on ClusterContext but no caller sets it on NutanixCluster (only read at `:350`) | `pkg/context/context.go:140-145` |
| InfraClusterTemplate | For ClusterClass | Yes (`NutanixClusterTemplate`) | `api/v1beta1/nutanixclustertemplate_types.go` |
| Pausing | No | Checks `annotations.IsPaused(capiCluster, nutanixCluster)`; no `Paused` condition | `controllers/nutanixcluster_controller.go:199-202` |
| Multi-tenancy `--namespace` / `--watch-filter` | For clusterctl | Not wired (no watch-filter predicate) | `main.go` |

### InfraMachine (`NutanixMachine`)

| Rule | Mandatory | CAPX today | Reference |
|---|---|---|---|
| `spec.providerID` | Yes | Set to `nutanix://<vmUUID>` **only on the path where the VM is created in the current reconcile** | `controllers/nutanixmachine_controller.go:2051`, `:2156` |
| `status.failureDomain` | No | Set when `Machine.spec.failureDomain` is set | `controllers/nutanixmachine_controller.go:906`, `:1627` |
| `status.addresses` | No | NIC IPs + hostname | `controllers/nutanixmachine_controller.go:2825-2843` |
| Initialization completed | Yes | `status.ready=true` at the end of a successful reconcile | `controllers/nutanixmachine_controller.go:759` |
| Conditions (`Ready` mirrored to Machine) | No | Many specific conditions (`VMProvisioned`, `VMAddressesAssigned`, `ProjectAssigned`, ...). **No `Ready` summary**, so Machine `InfrastructureReady` uses CAPI's fallback | `api/v1beta1/conditions.go` |
| Terminal failures | No | Uses v1beta1 `status.failureReason/failureMessage`. **In v1beta2 these do not make the Machine fail and MHC ignores them** | `pkg/context/context.go:147-152`; `infra-machine.md` "terminal failures" |
| InfraMachineTemplate + SSA dry-run | For ClusterClass | Template exists; only a defaulting webhook, no immutability validation, so dry-run is not an issue | `api/v1beta1/nutanixmachinetemplate_webhook.go` |
| Autoscale from zero (`status.capacity`) | No | Yes, CPU/memory/GPU | `controllers/nutanixmachinetemplate_controller.go:83-107` |
| Pausing | No | Checks `annotations.IsPaused(cluster, machine)` where `machine` is the **CAPI Machine**. A paused annotation on the NutanixMachine itself is ignored | `controllers/nutanixmachine_controller.go:253` |

## 1.4 Contract points that affect the resiliency work directly

1. **"Terminal failure" no longer means anything to CAPI.** Under v1beta2, when CAPX sets
   `NutanixMachine.status.failureReason`, CAPX itself stops reconciling (`controllers/nutanixmachine_controller.go:575-578`),
   but the CAPI Machine does **not** fail. It stays `Provisioning` until a MachineHealthCheck times it out
   (default template: `nodeStartupTimeout: 10m`, `templates/cluster-template.yaml:391`). So "fail early,
   fail clearly" needs a **condition** with a documented reason, not failureReason.
2. **v1beta1 compatibility is removed ~April 2027.** Before then CAPX must move to `status.initialization.provisioned`,
   list-form `status.failureDomains`, and v1beta2 conditions (with a `Ready` condition). This is contract
   work, separate from resiliency, but it touches the same status code.
3. **providerID is the handshake.** CAPI waits for it, CCM sets the Node's copy from the BIOS UUID, and CAPX
   must never derive spec from status (status is lost on `clusterctl move`, see `controllers/helpers.go:239-263` comment).

## 1.5 CAPX's own implicit contracts (not CAPI's)

These are CAPX-internal rules other components depend on:

| Rule | Who depends on it | Reference |
|---|---|---|
| VM name = CAPI `Machine.name` (older VMs used NutanixMachine name) | delete path, name lookups | `controllers/nutanixmachine_controller.go:391-398`, `:1865` |
| Every VM gets category `KubernetesClusterName=<cluster>` (key from `DefaultCAPICategoryKeyForName`) | CSI, cleanup | `controllers/helpers.go:1522-1529` |
| Custom attribute `providerid:<vmUUID>` on the VM | external tooling | `controllers/nutanixmachine_controller.go:77`, `:2168-2197` |
| Metro VMs carry exactly one `k8s-vha-native-site` category | CSI, NKP k8s-HA | `controllers/nutanixmachine_controller.go:954-977` |
| Idempotency key annotation `capx.nutanix.com/vm-creation-request-id` | VM create at-most-once | `controllers/helpers.go:81-87` |
