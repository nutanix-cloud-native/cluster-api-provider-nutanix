# ntnx-sim: a Prism Central simulator for CAPX

`ntnx-sim` is an in-memory Prism Central that serves the subset of the Nutanix
v4 API CAPX uses. It lets an **unmodified** CAPX manager run without Nutanix
hardware, for scale testing or hardware-free CI.

It is a library (`test/simulator`, an `http.Handler` over an in-memory store)
plus a thin binary (`test/simulator/cmd/ntnx-sim`).

## What it serves

| API | Endpoints |
| --- | --- |
| SDK version negotiation | `OPTIONS /api/{namespace}/unversioned/info` |
| v3 session login | `GET /api/nutanix/v3/users/me` (CAPX's v3 client logs in at construction; nothing else v3 is served) |
| vmm | VMs list/get/create/delete, `$actions/power-on`, `$actions/power-off`, `$actions/add-custom-attributes`; images list/get |
| clustermgmt | clusters list/get; storage containers list |
| networking | subnets list/get |
| prism | categories list/get/create/delete; tasks list/get |
| operational | `GET /-/healthz`, `GET /-/stats` (per-route request counters, task outcomes, injected faults, store sizes) |

Everything else under `/api` answers **501** so an unexpected call fails loudly
instead of silently succeeding. Projects, metro/vHA (recovery plans, DR
config), GPUs and volume groups are out of scope for now.

## How it stays wire-compatible

Every response is produced by marshalling the same `ntnx-api-golang-clients`
model structs that CAPX unmarshals through `prism-go-client`, so envelopes,
`$objectType` discriminators and enum spellings match by construction. The
test suite drives the simulator through the real `prism-go-client` converged
client with the exact call sequence and `$filter` strings CAPX issues.

Behaviours CAPX depends on:

- Mutations return `202` plus a task reference. Tasks go `RUNNING` then
  `SUCCEEDED` after the configured duration and list the affected entity, which
  is what `Operation.Wait` in prism-go-client resolves.
- `GET` responses carry an `ETag` (header and `$reserved`); mutations require
  `If-Match` and answer `412` on mismatch.
- `$filter` is evaluated by a small OData parser covering `eq/ne/gt/ge/lt/le`,
  `and/or/not`, parentheses, typed enum literals such as
  `Prism.Config.TaskStatus'RUNNING'` and the `entitiesAffected/any(a:a/extId eq '...')`
  lambda. `$page`/`$limit` paginate the way the converged iterator expects.
- Powering a VM on allocates an address from the subnet's pool and reports it as
  a learned IP on the NIC, which populates `NutanixMachine.status.addresses`.
- Empty lists omit `data`, as Prism Central does; the SDK cannot decode `[]`.

## Running it

```sh
go build -o bin/ntnx-sim ./test/simulator/cmd/ntnx-sim
bin/ntnx-sim --listen :9440 --username admin --password ntnx-sim \
  --tls-cert-out /tmp/ntnx-sim-ca.pem \
  --vm-create-duration 30s --vm-power-on-duration 45s
```

prism-go-client only speaks HTTPS, so a self-signed certificate is generated
(SANs from `--tls-hosts`) unless `--tls-cert/--tls-key` are given. Point CAPX
at it:

```yaml
apiVersion: infrastructure.cluster.x-k8s.io/v1beta1
kind: NutanixCluster
spec:
  prismCentral:
    address: ntnx-sim.ntnx-sim.svc      # must be in --tls-hosts
    port: 9440
    insecure: false
    additionalTrustBundle:
      kind: ConfigMap
      name: ntnx-sim-ca                  # data.ca.crt = contents of --tls-cert-out
    credentialRef:
      kind: Secret
      name: ntnx-sim-credentials         # the usual CAPX basic-auth credential secret
  controlPlaneEndpoint:
    host: 10.10.255.1                    # CAPX treats this as user input; the harness owns it
    port: 6443
```

Or set `insecure: true` and skip the trust bundle. The cluster, subnet and
image names in the cluster template must match the seed
(`pe-sim`, `subnet-sim`, `ubuntu-sim`, `default-sim` by default). See
`config.example.yaml` for a multi-cluster seed, timing and fault settings.

## Using it as a library

```go
sim, err := simulator.New(simulator.DefaultConfig(), simulator.WithHooks(simulator.Hooks{
    OnVMPoweredOn: func(ctx context.Context, ev simulator.VMEvent) {
        // ev.VM is the SDK model (extId, name, customAttributes carry the providerID).
        // ev.Addresses lists the IPs assigned to its NICs.
        // A scale harness creates its fake Node / etcd member here.
    },
    OnVMDeleted: func(ctx context.Context, ev simulator.VMEvent) { /* tear it down */ },
}))
srv := httptest.NewUnstartedServer(sim.Handler())
srv.StartTLS()
```

Hooks fire after the corresponding task completes, outside the store lock. The
simulator does not model the workload cluster: a real VM would run cloud-init
and join the cluster, a simulated one does nothing. Whatever fakes the nodes
(for example `cluster-api/test/infrastructure/inmemory`) belongs in the
harness, keyed on the VM's `providerid:` custom attribute.

## Scale-testing notes

- Every mutation in CAPX blocks the reconcile goroutine in `Operation.Wait`,
  polling the task once a second. With the default `--max-concurrent-reconciles=10`
  the number of in-flight VM operations is bounded by concurrency times task
  duration, so `timing.vmCreate` and `timing.vmPowerOn` are the primary
  throughput dials. Run both an "instant" and a realistic-timing profile; they
  expose different limits.
- `faults.rateLimitEvery` answers 429 with `RATE_LIMIT_EXCEEDED`. The v4 SDK
  retries 429 (and 408/503/504) up to five times at three second intervals
  before CAPX sees `converged.ErrRateLimit`, so a low N mostly adds latency and
  a high N mostly adds request volume. 500s are not retried and surface
  immediately as `converged.ErrInternal`.
- `/-/stats` reports request counts and mean latency per route pattern, which
  is a direct measure of CAPX's PC request volume per reconcile.
- `prismCentral.address` is the natural shard key: run several simulators and
  spread clusters across them.
