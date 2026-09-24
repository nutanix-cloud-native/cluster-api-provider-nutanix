# 3. NutanixCluster controller

File: `controllers/nutanixcluster_controller.go` (977 lines).

## 3.1 Setup and watches

| What | Reference |
|---|---|
| Reconciler struct: k8s client, Secret/ConfigMap informers, scheme, config | `:58-64` |
| Watches NutanixCluster (`For`), Cluster (pause / infra-provisioned transitions only), NutanixFailureDomain (mapped to NutanixClusters that list it in `spec.controlPlaneFailureDomains`) | `:83-113`, map func `:115-145` |
| RBAC: may **create** NutanixVirtualHADomains | `:155` |

## 3.2 `Reconcile` — preamble (runs for both normal and delete)

| Step | Behaviour | On error | Reference |
|---|---|---|---|
| 1 | Get NutanixCluster | NotFound → stop; else return err (requeue with backoff) | `:173-187` |
| 2 | `GetOwnerCluster` | err → requeue; nil owner → stop, wait for CAPI to set ownerRef | `:190-198` |
| 3 | Paused (`Cluster.spec.paused` or paused annotation on NutanixCluster) | stop | `:199-202` |
| 4 | Create a v1beta1 patch helper; **defer Patch** of the whole object at the end | helper failure → `Requeue: true` | `:206-218` |
| 5 | `reconcileCredentialRef`: get the credential Secret, set ownerRef to this NutanixCluster (fails if another NutanixCluster owns it), add finalizer, `Update` | condition `CredentialRefSecretOwnerSet=False` + return err | `:220-236`, impl `:914-968` |
| 6 | `reconcileTrustBundleRef`: same for the CA ConfigMap | condition `TrustBundleSecretOwnerSet=False` + return err | `:238-241`, impl `:808-872` |
| 7 | Build **v3** PC client from cache | condition `PrismClientInit=False` + return err | `:243-247`, impl `controllers/helpers.go:2451-2495` |
| 8 | Build **converged v4** PC client from cache | condition `PrismClientConvergedV4Init=False` + return err | `:248-252`, impl `controllers/helpers.go:2497-2537` |
| 9 | Branch: deletion timestamp set → `reconcileDelete`, else `reconcileNormal` | — | `:262-267` |

### How the PC credentials are resolved (steps 7–8)

`pkg/client/client.go:69-106` (`BuildManagementEndpoint`):
1. If `NutanixCluster.spec.prismCentral` is set → provider from it (address and port are required, `:118-123`);
   credential Secret namespace defaults to the cluster namespace (`:130-132`).
2. Otherwise → fall back to the CAPX manager's own file `/etc/nutanix/config/prismCentral` (`:37`, `:145-167`),
   with namespace from `POD_NAMESPACE`.
3. `environment.GetManagementEndpoint` returns endpoint + credentials (read through the Secret informer).

Client caches (`pkg/client/cache.go`): `NutanixClientCache` (v3), `NutanixClientCacheV4`,
`NutanixConvergedClientV4Cache`, all created `WithSessionAuth(true)`. Keyed by NutanixCluster
`namespace/name`. The converged cache rebuilds the client when the management endpoint hash changes
(credential rotation), `pgc:converged/v4/cache.go:31-60`.

Note: steps 7–8 run on **every** reconcile of every NutanixCluster and NutanixMachine
(`controllers/nutanixmachine_controller.go:279-289`). They are cheap after the first time (cache hit),
but each builds the endpoint from informer data first.

## 3.3 `reconcileNormal`

`controllers/nutanixcluster_controller.go:348-382`

| Step | Behaviour | Reference |
|---|---|---|
| 1 | If `status.failureReason` or `failureMessage` is set → log and stop (nothing in CAPX sets these on NutanixCluster today) | `:350-353` |
| 2 | Add finalizer `infrastructure.cluster.x-k8s.io/nutanixcluster`, remove the deprecated one | `:357-360` |
| 3 | `reconcileFailureDomains` (below) | `:363-366` |
| 4 | `reconcileVHADomains` (Metro only, below) | `:370-373` |
| 5 | If already Ready → return | `:375-378` |
| 6 | `status.ready = true` | `:380` |

There is **no PC reachability check** before Ready. Once the clients are built, and FDs and VHA domains
reconciled without a hard error, the cluster is Ready.

### 3.3.1 `reconcileFailureDomains` — `:524-663`

- No FDs configured (neither `controlPlaneFailureDomains` nor the deprecated `failureDomains`) →
  condition `NoFailureDomainsConfigured=True`, clear `status.failureDomains`, return (`:529-543`).
- For each entry in `spec.controlPlaneFailureDomains` (`:561-634`):
  - **`NutanixMetro/<name>`**: get the NutanixMetro, then each referenced NutanixFailureDomain, and validate
    each against PC using the default project (`:563-582`).
  - **`NutanixMetroSite/<name>`**: get the MetroSite, then its preferred FD, and validate (`:583-600`).
  - **plain FD name**: get the NutanixFailureDomain; on first use, resolve the project scope once
    (`resolveProjectScopeForCluster`, `:665-689`: PC version, project policy annotation, resource group, project),
    then validate (`:601-629`).
  - Validation = `GetPEUUID` + `GetSubnetUUIDList` against PC (`validateFailureDomainSpec`, `:693-707`).
  - A valid FD is added to `status.failureDomains` as `{controlPlane: true}` (`:633`).
- The deprecated `spec.failureDomains` entries are copied without validation (`:637-639`).
- **Any** validation error → condition `FailureDomainsValidated=False` (severity Warning) with all errors
  joined, and **return nil** (`:644-654`). The cluster still becomes Ready.
- A hard error (project-scope resolution fails) returns an error and requeues (`:615-618`).

PC calls here: `DomainManager.GetPrismCentralVersion`, `Projects.GetDefaultProject`, `ResourceGroups.List`
(project-scoped only), `Clusters.Get` / `Clusters.List` or `ResourceGroups.ListPrismElements`, `Subnets.Get` /
`Subnets.List`. These run on every NutanixCluster reconcile that has FDs.

### 3.3.2 `reconcileVHADomains` — `:387-462` (Metro only)

1. Collect the unique metro names referenced by the control-plane FDs **and** by worker MachineDeployments'
   `spec.template.spec.failureDomain` (`collectClusterMetroNames`, `:476-522`).
2. For each metro: the NutanixMetro and its FDs must exist (`:410-420`).
3. If no owned `NutanixVirtualHADomain` for that metro exists, create one named `<cluster>-<metro>`
   (`vHADomainName`, `controllers/helpers.go:2649-2651`), controller-owned by the NutanixCluster, labelled
   with the cluster name (`:435-458`).

The VHA domain controller then creates the PC categories, protection policy and recovery plans (chapter 8).

## 3.4 `reconcileDelete` — `:270-346`

Order matters:

| # | Step | Blocking behaviour | Reference |
|---|---|---|---|
| 1 | List NutanixMachines with label `cluster.x-k8s.io/cluster-name=<NutanixCluster.name>` | if any remain → requeue after 5 s | `:274-283` |
| 2 | Delete owned NutanixVirtualHADomains, then wait for them to disappear | requeue after 5 s | `:293-310` |
| 3 | `reconcileCategoriesDelete`: delete the `KubernetesClusterName=<cluster>` category value and the obsolete `kubernetes-io-cluster-<cluster>=owned`, **only if** `ClusterCategoryCreated` is True or its reason is `DeletionFailed` | error → condition `ClusterCategoryCreated=False/DeletionFailed` + return err | `:312-316`, impl `:712-763` |
| 4 | Drop the three PC clients for this cluster from the caches | — | `:318-322` |
| 5 | Remove finalizers from the credential Secret and delete it if not already deleting | err → return | `:324-327`, `:765-806` |
| 6 | Same for the trust-bundle ConfigMap | err → return | `:329-332`, `:874-912` |
| 7 | Remove the NutanixCluster finalizers; drop the workload remote client from cache | — | `:335-343` |

Subtleties:
- Step 1 uses the **NutanixCluster name** as the cluster-name label value (`pkg/context/context.go:121-123`).
  That is correct only when NutanixCluster.name == Cluster.name, which the templates do.
- In step 3, a failure deleting a category value is logged and **swallowed** (`controllers/helpers.go:1654-1660`,
  comment references NCN-101935: the value may still be attached to VMs). The category can be left behind.
- Step 3 needs a working PC client (it calls `GetPrismCentralVersion`). If the credential Secret was already deleted
  by the user, the preamble fails at step 7/8 and deletion blocks.

## 3.5 Conditions written on NutanixCluster

| Condition | Written by | When |
|---|---|---|
| `CredentialRefSecretOwnerSet` | cluster controller | every reconcile |
| `TrustBundleSecretOwnerSet` | cluster controller | when a trust bundle is configured |
| `PrismClientInit` / `PrismClientConvergedV4Init` | `controllers/helpers.go:2461-2536` (called from **both** controllers) | every reconcile |
| `NoFailureDomainsConfigured` / `FailureDomainsValidated` | cluster controller | every reconcile |
| `ClusterCategoryCreated` | **machine controller** (`markClusterCategoryCreated`, `controllers/nutanixmachine_controller.go:3079-3127`) and cluster controller on delete | first VM create / delete |

The machine controller patches the NutanixCluster with its own patch helper when it sets
`ClusterCategoryCreated`, so two controllers write NutanixCluster status.
