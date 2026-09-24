# CAPX End-to-End Reference — current behaviour, with code references

Purpose: be able to answer "what does CAPX do today when X happens?" with a code reference.
Covers the full path from a `Cluster` CR down to a VM on Prism Central (PC), the Cluster API
(CAPI) contract CAPX implements, the PC client layer (tasks, subtasks, retries, idempotency),
the error-handling model, the delete path, and a list of findings (gaps/bugs) found while
reading the code.

## Chapters

| # | File | What it answers |
|---|------|-----------------|
| 1 | [01-capi-contract.md](01-capi-contract.md) | What CAPI expects from an infrastructure provider, and where CAPX meets or misses each rule |
| 2 | [02-e2e-flow.md](02-e2e-flow.md) | The object graph and the full timeline: `Cluster` → KCP/MD → `Machine` → `NutanixMachine` → VM → Node |
| 3 | [03-nutanixcluster-controller.md](03-nutanixcluster-controller.md) | NutanixCluster reconcile, step by step (normal + delete) |
| 4 | [04-nutanixmachine-create.md](04-nutanixmachine-create.md) | NutanixMachine reconcile — VM creation path, every step and every PC call |
| 5 | [05-prism-client-layer.md](05-prism-client-layer.md) | prism-go-client converged v4 + Nutanix SDK: tasks, subtask errors, polling, retries, timeouts, request-id, error classification |
| 6 | [06-error-handling-model.md](06-error-handling-model.md) | Retryable vs terminal, conditions, requeue/backoff, what the user sees |
| 7 | [07-delete-path.md](07-delete-path.md) | NutanixMachine / NutanixCluster deletion, VG detach, categories, Metro decoupled VMs |
| 8 | [08-other-controllers.md](08-other-controllers.md) | FailureDomain, MachineTemplate, VirtualHADomain, Metro controllers (summary) |
| 9 | [09-findings.md](09-findings.md) | Gaps and bugs found, with evidence, impact, and how to reproduce |
| 10 | [10-quick-answers.md](10-quick-answers.md) | One-paragraph answers to questions you will likely be asked |

## Code versions these references point at

| Component | Location | Version / commit |
|-----------|----------|------------------|
| CAPX | `cluster-api-provider-nutanix/` | branch `issue/tempvj`, commit `cfe8cdb` (on top of upstream `15a84d1`) |
| prism-go-client (converged client) | `prism-go-client/` | tag `v0.8.2` (commit `a96bca3`) — the exact version in CAPX `go.mod` |
| Nutanix VMM Go SDK | `~/go/pkg/mod/github.com/nutanix/ntnx-api-golang-clients/vmm-go-client/v4@v4.3.1` | v4.3.1 |
| Nutanix Prism Go SDK | `~/go/pkg/mod/github.com/nutanix/ntnx-api-golang-clients/prism-go-client/v4@v4.4.1` | v4.4.1 |
| Cluster API | `~/go/pkg/mod/sigs.k8s.io/cluster-api@v1.13.6` | v1.13.6 |
| VMM API definitions | `nutanix-core/ntnx-api-vmm/` | commit `82d220fc` |

Line numbers drift as code changes. Re-check them if you read this against a newer commit.

## Notation

- `controllers/x.go:123` means a path in the CAPX repo unless another repo is named.
- `pgc:` means prism-go-client, e.g. `pgc:converged/v4/structs.go:206`.
- `capi:` means Cluster API v1.13.6, e.g. `capi:internal/controllers/machine/machine_controller_phases.go:244`.
- `sdk-vmm:` means the VMM Go SDK v4.3.1.
- `vmm-defs:` means `ntnx-api-vmm/vmm-api-definitions/defs/namespaces/vmm/`.
- **Verified** means read directly from code. **Hypothesis** means it follows from the code but needs confirming on a real PC or with the owning team.
