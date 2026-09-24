# 2. End-to-end flow: from `Cluster` CR to a running VM and Node

## 2.1 The object graph

A default CAPX cluster (`templates/cluster-template.yaml`) creates these objects in the management cluster:

```
Secret <cluster>                 (PC credentials)            template :233
ConfigMap <cluster>-pc-trusted-ca-bundle (optional CA)       template :4
Cluster <cluster>                                            template :329
 ├─ spec.infrastructureRef  -> NutanixCluster <cluster>      template :349, :585
 └─ spec.controlPlaneRef    -> KubeadmControlPlane <cluster>-kcp   template :345, :416
        └─ spec.machineTemplate.infrastructureRef -> NutanixMachineTemplate <cluster>-mt-0   template :566-570, :605
MachineDeployment <cluster>-wmd                              template :355
 ├─ template.spec.bootstrap.configRef -> KubeadmConfigTemplate <cluster>-kcfg-0   template :372-374, :299
 └─ template.spec.infrastructureRef   -> NutanixMachineTemplate <cluster>-mt-0    template :377-379
MachineHealthCheck <cluster>-mhc (nodeStartupTimeout 10m)    template :384-391
ClusterResourceSet nutanix-ccm-crs (installs CCM into the workload cluster)  template :281
```

At runtime the core controllers expand this into:

```
KubeadmControlPlane ──creates──> Machine (CP-n) ──ref──> NutanixMachine (cloned from NutanixMachineTemplate)
                                               └─ref──> KubeadmConfig (bootstrap) ──produces──> Secret (cloud-init user-data)
MachineDeployment ──> MachineSet ──> Machine (worker-n) ──ref──> NutanixMachine
                                                        └─ref──> KubeadmConfig ──> Secret
NutanixCluster ──(Metro only)──> NutanixVirtualHADomain  (created by CAPX, controllers/nutanixcluster_controller.go:435-457)
NutanixMachine ──(CAPX)──> VM on Prism Central (+ categories, custom attributes, power on)
VM boots ──cloud-init/kubeadm──> Node in workload cluster ──CCM sets──> Node.spec.providerID = nutanix://<bios-uuid>
```

Ownership and finalizers:

| Object | Owner | Finalizer set by CAPX | Reference |
|---|---|---|---|
| NutanixCluster | Cluster (set by CAPI) | `infrastructure.cluster.x-k8s.io/nutanixcluster` | `controllers/nutanixcluster_controller.go:357-360` |
| Credential Secret | NutanixCluster (ownerRef set by CAPX) | `infrastructure.cluster.x-k8s.io/nutanixclustercredential` | `controllers/nutanixcluster_controller.go:914-968` |
| Trust-bundle ConfigMap | NutanixCluster | same credential finalizer | `controllers/nutanixcluster_controller.go:808-872` |
| NutanixMachine | Machine (set by CAPI) | `infrastructure.cluster.x-k8s.io/nutanixmachine` | `controllers/nutanixmachine_controller.go:583-586` |
| NutanixVirtualHADomain | NutanixCluster (controller ref) | `infrastructure.cluster.x-k8s.io/nutanixvirtualhadomain` | `controllers/nutanixcluster_controller.go:443-445` |

## 2.2 Timeline of a fresh cluster create

Phases run concurrently. Each step shows who acts and which field hands off to the next step.

### Phase A — infrastructure cluster

| # | Actor | What happens | Hand-off field | Reference |
|---|---|---|---|---|
| A1 | User / NKP | Applies Cluster, NutanixCluster, KCP, MD, templates, Secret | — | template |
| A2 | CAPI Cluster controller | Sets ownerRef Cluster→NutanixCluster, then `reconcileInfrastructure` | NutanixCluster ownerRef | `capi:internal/controllers/cluster/cluster_controller_phases.go:141` |
| A3 | CAPX NutanixCluster controller | Waits for the ownerRef; checks pause; builds PC clients (v3 + converged v4); adds finalizer; owns the credential Secret and CA ConfigMap; validates failure domains against PC; creates VHA domains (Metro) | — | `controllers/nutanixcluster_controller.go:166-382` |
| A4 | CAPX | Sets `NutanixCluster.status.ready = true` | `status.ready` | `controllers/nutanixcluster_controller.go:380` |
| A5 | CAPI Cluster controller | Reads `status.ready` (v1beta1 contract) → `Cluster.status.initialization.infrastructureProvisioned = true`; copies `spec.controlPlaneEndpoint` and `status.failureDomains` | `Cluster...infrastructureProvisioned` | `cluster_controller_phases.go:141-250` |

Note: A4 happens even if failure-domain validation failed. The validation result only goes into the
`FailureDomainsValidated` condition (`controllers/nutanixcluster_controller.go:644-654`); invalid FDs are
just left out of `status.failureDomains`.

### Phase B — first control-plane machine

| # | Actor | What happens | Hand-off | Reference |
|---|---|---|---|---|
| B1 | KCP controller | `initializeControlPlane`: clones NutanixMachineTemplate → NutanixMachine, KubeadmConfig (init), creates Machine CP-0 | Machine, NutanixMachine, KubeadmConfig | `capi:controlplane/kubeadm/internal/controllers/scale.go:43`, `helpers.go:160` (`cloneConfigsAndGenerateMachine`), `helpers.go:298` |
| B2 | CAPI Machine controller | Sets ownerRef Machine→NutanixMachine, labels; `reconcileBootstrap` | NutanixMachine ownerRef | `capi:internal/controllers/machine/machine_controller_phases.go:148` |
| B3 | KubeadmConfig controller | `handleClusterNotInitialized`: generates certs + init cloud-config; `storeBootstrapData` writes Secret | `Machine.spec.bootstrap.dataSecretName` | `capi:bootstrap/kubeadm/internal/controllers/kubeadmconfig_controller.go:460`, `:1344` |
| B4 | CAPX NutanixMachine controller | Sees Cluster infra ready + dataSecretName → **creates the VM on PC** (whole of chapter 4) → sets `spec.providerID`, `status.addresses`, `status.ready=true` | `spec.providerID`, `status.ready` | `controllers/nutanixmachine_controller.go:573-764` |
| B5 | CAPI Machine controller | `reconcileInfrastructure`: reads ready + providerID → `Machine.spec.providerID`, `infrastructureProvisioned=true` | `Machine.spec.providerID` | `machine_controller_phases.go:244-377` |
| B6 | VM | Boots, cloud-init runs `kubeadm init`; kube-vip claims the control-plane endpoint IP | API server up at endpoint | template kube-vip `:446-528` |
| B7 | ClusterResourceSet + CCM | CCM installed; sets `Node.spec.providerID` from the VM BIOS UUID | Node.providerID | template `:281` |
| B8 | CAPI Machine controller | `reconcileNode`: finds the Node by providerID → `Machine.status.nodeRef` | nodeRef | `capi:internal/controllers/machine/machine_controller_noderef.go:59-242` |
| B9 | KCP / Cluster | Control plane initialized → `Cluster.status.initialization.controlPlaneInitialized = true` | CP initialized | KCP `controller.go:381` |

### Phase C — remaining control-plane and worker machines

| # | Actor | What happens | Reference |
|---|---|---|---|
| C1 | KCP | `scaleUpControlPlane` one at a time for CP-1, CP-2 (join config) | `scale.go:67` |
| C2 | MachineDeployment → MachineSet | `syncReplicas` → `createMachines` (workers created in parallel) | `capi:internal/controllers/machineset/machineset_controller.go:759`, `:819` |
| C3 | KubeadmConfig | `joinControlplane` / `joinWorker` — **only after the control plane is initialized** | `kubeadmconfig_controller.go:644`, `:810` |
| C4 | CAPX NutanixMachine | Worker NutanixMachines wait at `ensureBootstrapRef` with reason `ControlplaneNotInitialized` until the bootstrap Secret exists, then create VMs as in B4 | `controllers/nutanixmachine_controller.go:769-806` |

## 2.3 Where CAPX sits in the Machine lifecycle (one machine)

```
NutanixMachine created (by KCP/MS)       CAPX reconcile loop (repeats until Ready)
        │
        ▼
[wait] no owner Machine yet ...................... return, no requeue   nutanixmachine_controller.go:241-244
[wait] Cluster/Machine paused .................... return               :253-256
[wait] NutanixCluster not found .................. return (nil error!)  :264-268
        │
        ▼ build PC clients (v3 + converged)                               :279-289
        │
        ▼ reconcileNormal                                                  :573
[stop] failureReason already set ................. return forever        :575-578
        add finalizer                                                      :583-586
[Ready branch] status.ready already true ......... sync VmUUID only       :589-604
[wait] Cluster infra not provisioned ............. condition ClusterInfrastructureNotReady :608-618
[wait] no bootstrap data ......................... ControlplaneNotInitialized / BootstrapDataNotReady :621-623
        PC version → project → policy → resource group                    :626-683
        getOrCreateVM  (find VM, else build spec + CreateAsync + Wait)     :686, :1861-2062
        addCustomAttributes (providerid:<uuid>)                            :694
        power on if not ON (PowerOnVM + Wait + re-Get)                     :700-706
        syncVmUUID (prefers Node SystemUUID)                               :709
        checkFailureDomainStatus / checkVHADomainCategory                  :719-729
        patchMachine                                                       :732
        assignAddressesToMachine (needs NIC IPs, else error + requeue)     :739-750
        status.ready = true                                                :759
```

Every step before `status.ready = true` re-runs from the top on every requeue, including the PC calls
(version, project, find VM). See [chapter 4](04-nutanixmachine-create.md) for each call.

## 2.4 Watches — what triggers a NutanixMachine reconcile

From `controllers/nutanixmachine_controller.go:117-157`:
- the NutanixMachine itself (`For`)
- its CAPI Machine (`MachineToInfrastructureMapFunc`, `:136-143`)
- the NutanixCluster → all NutanixMachines of that cluster (`:144-149`, map func `:159-195`)
- the CAPI Cluster, only on pause transitions or when infrastructure becomes provisioned (`:150-154`)

NutanixCluster reconcile triggers (`controllers/nutanixcluster_controller.go:83-113`):
- the NutanixCluster itself
- the CAPI Cluster (pause transitions / infra provisioned)
- any NutanixFailureDomain referenced in `spec.controlPlaneFailureDomains`

## 2.5 Concurrency and rate limiting (as deployed)

| Setting | Value | Reference |
|---|---|---|
| Max concurrent reconciles, NutanixCluster | `--max-concurrent-reconciles` (default 10) | `main.go:76`, `:199-200`, `:239` |
| Max concurrent reconciles, NutanixMachine (and FD, template, metro, VHA controllers) | same flag, default 10, **each controller has its own 10 workers** | `main.go:240`, `:502-537` |
| Work-queue rate limiter actually used | hard-coded: exponential 1 ms → 1000 s per item, plus a 10 qps / 100 burst bucket | `main.go:490`, `:504` |
| `--rate-limiter-*` flags | parsed and validated into `config.rateLimiter`, **but never used** | `main.go:202-205`, `:242-246` |
| Reconcile timeout | none set; a reconcile can block as long as a PC task wait lasts | see [chapter 5](05-prism-client-layer.md) |

Controller-runtime guarantees only one reconcile per object at a time. Different NutanixMachines reconcile in parallel, up to 10.
