# CAPX infrastructure monitoring

This document is the catalog of what CAPX observes while it creates and changes Nutanix infrastructure. Metrics are registered on the controller-runtime registry and served on the existing `/metrics` endpoint. This change also adds a PrometheusRule next to the ServiceMonitor.

## Why error text is not a Prometheus label

Prometheus creates one time series for every unique combination of label values. Labels must be a **closed, small set** (operation names, HTTP status codes, outcome).

These values must **not** be labels:

| Value | Why |
| --- | --- |
| Prism / CAPX error sentence (`status.failureMessage`) | Unbounded. "IP pool exhausted on subnet A" and "… subnet B" are different series forever. |
| Machine name, namespace, cluster name | One series per object. |
| VM UUID, Prism task UUID | One series per UUID. |

The admin still gets the sentence from `NutanixMachine.status.failureMessage` (and logs). Alerts name `operation` and `class` / `http_status`, then point the admin at that status field.

## Metrics in this change

### `capx_infra_operation_errors_total`

```promql
sum by (operation, outcome, class, http_status) (capx_infra_operation_errors_total)
```

| Label | Values | Meaning |
| --- | --- | --- |
| `resource` | `nutanixmachine`, `nutanixcluster`, `nutanixfailuredomain`, `nutanixmetro`, `nutanixmetrosite`, `nutanixvirtualhadomain`, `metroscaledownbalancer` | Object the controller was reconciling. |
| `operation` | See tables below. | Infrastructure step (or `reconcile` for non-machine controllers). |
| `outcome` | `terminal`, `retryable` | `terminal` means CAPX set `status.failureReason` on a NutanixMachine. `retryable` means the same object is reconciled again. |
| `class` | `rate_limit`, `internal`, `not_found`, `unauthenticated`, `unauthorized`, `client`, `transport` | How the error was classified. |
| `http_status` | `none`, `400`, `401`, `403`, `404`, `408`, `409`, `412`, `428`, `429`, `500`, `502`, `503`, `504`, `other` | Parsed from the Prism `APIError` cause when present. Splits 408 from 400 inside `class=client`. |

`outcome` follows the controller: `recordAndCheckTerminal` uses the same `isRetryableAPIError` check that decides `SetFailureStatus`. Sites that always call `SetFailureStatus` are recorded as `terminal`.

| Class | When |
| --- | --- |
| `rate_limit` | HTTP 429, or a rate-limit error group. |
| `internal` | HTTP 500-599. |
| `not_found` | HTTP 404. |
| `unauthenticated` | HTTP 401. |
| `unauthorized` | HTTP 403. |
| `client` | Other Prism API errors (including 400, 408, 412, 428) and local `terminalError`. |
| `transport` | Non-API errors (network, Kubernetes patch). |

### `capx_infra_machines_failed`

Gauge of NutanixMachines this CAPX process currently tracks as failed, by `operation`. Incremented when a terminal failure is recorded; decremented when a failed machine is deleted. After a controller restart the gauge starts at zero until new failures are recorded.

### `capx_infra_machines_create_stuck`

Gauge of NutanixMachines this CAPX process currently tracks as **not Ready for more than 10 minutes** since `capx.nutanix.com/vm-create-started-at` was stamped (first Prism create path entry). This covers create/retry loops that never finish, not a single reconcile's Prism Wait time.

| Field | Meaning |
| --- | --- |
| Annotation `capx.nutanix.com/vm-create-started-at` | RFC3339 wall time when CAPX first started creating the VM. |
| Threshold | 10 minutes (`vmCreateStuckAfter`). |
| Clear | Machine becomes `Status.Ready=true`, or is deleted. |

After a controller restart the gauge starts at zero until reconciles re-observe stuck machines.

### `capx_infra_vm_create_duration_seconds`

Histogram from FindVM through `VMProvisioned` (or create failure). Labels: `resource`, `result` (`success` / `error`).

### `capx_infra_prism_task_duration_seconds` / `capx_infra_prism_task_results_total`

How long CAPX blocked on Prism `Wait`, and the outcome. Labels: `resource`, `operation`, `state` (`succeeded` / `failed` / `canceled` / `error`). The task UUID is never a label.

### `capx_infra_idempotent_accepts_total`

Times CAPX treated an already-present or already-absent object as success. Labels: `resource`, `operation`, `reason` (`existing_vm` when FindVM finds the VM; `already_absent` when category delete finds nothing).

## Machine create / power-on operations

| `operation` | What failed | Typical outcome |
| --- | --- | --- |
| `pc_version` | Prism Central version lookup. | retryable |
| `project` | Resolving the project. | follows the API error |
| `project_policy` | Project policy check. | follows the API error |
| `resource_group` | Project resource group on PC 7.6+. | follows the API error |
| `vm_lookup` | Find VM by name before create. | retryable |
| `request_id` | Persisting the creation request id. | retryable |
| `machine_config` | Spec validation. | terminal |
| `subnet_pe` | Subnet / PE UUID. | terminal |
| `nic` | NIC list. | terminal |
| `category` | Create / resolve categories. | follows the API error |
| `category_delete` | Category delete (including swallowed NCN-101935 errors). | retryable (behavior still returns nil) |
| `project_assign` | Put VM in project. | follows the API error |
| `device_bundle` | Implicit GPU device bundle. | follows the API error |
| `gpu` | GPU list. | follows the API error |
| `disk` | Disks. | follows the API error, or terminal for local spec errors |
| `image` | Image lookup / deletion check. | follows the API error; image being deleted is terminal |
| `guest_customization` | Cloud-init. | terminal |
| `boot_type` | Boot type. | terminal |
| `vm_create` | CreateAsync / create task / wrong VM count. | follows the API error |
| `vm_profile` | Profile deploy. | follows the API error |
| `profile_nic` | Profile NIC mapping. | terminal |
| `machine_patch` | Patch after VmUUID. | retryable |
| `custom_attributes` | Provider-id attribute. | follows the API error |
| `power_on` | PowerOnVM / wait / fetch. | follows the API error |
| `volume_group_detach` | Detach VG during delete. | retryable |

`status.failureReason` remains only `CreateError` or `PowerOnError`.

## Non-machine controllers

Cluster, failure domain, metro, metro site, virtual HA domain, and metro scale-down balancer record `operation=reconcile` with `outcome=retryable` when `Reconcile` returns an error. They do not set NutanixMachine `failureReason`. They are still also counted by `controller_runtime_reconcile_errors_total`.

## Webhook rejections

CAPX’s NutanixMachineTemplate webhook is a **mutating defaulter** only; it does not reject. Bad templates are rejected by API-server CEL before the machine controller runs, so `capx_infra_operation_errors_total` stays at zero for that object. Those admission rejects are not CAPX Prometheus series.

## Alerts

| Alert | Fires when |
| --- | --- |
| `CAPXInfraOperationTerminal` | `increase(...{outcome="terminal"}[15m]) > 0` |
| `CAPXInfraOperationRetrying` | Retryable error rate stays above zero for 15m |
| `CAPXInfraVMCreateStuck` | `capx_infra_machines_create_stuck > 0` for 1m (not Ready >10m since create start) |
| `CAPXInfraVMCreateSlow` | p95 successful create duration >300s |
| `CAPXInfraPrismTaskSlow` | p95 Prism Wait >120s |

Inspect the Prism sentence with:

```bash
kubectl get nutanixmachine -A -o custom-columns=NS:.metadata.namespace,NAME:.metadata.name,REASON:.status.failureReason,MESSAGE:.status.failureMessage
```

The default install leaves `- ../prometheus` commented in `config/default/kustomization.yaml` because ServiceMonitor / PrometheusRule need the Prometheus Operator CRDs.

## Already available without CAPX-authored metrics

| Series / field | Source |
| --- | --- |
| `controller_runtime_reconcile_*` | controller-runtime, when `Reconcile` returns |
| `workqueue_*` | controller-runtime workqueue adapter |
| `rest_client_requests_total` | client-go via controller-runtime (Kubernetes API only; not Prism) |
| `status.failureReason` / `failureMessage` | Object status, not a metric |
| `VMProvisioned` condition | Object status, not a metric |

## Code

| Piece | Path |
| --- | --- |
| Metric definitions | `pkg/metrics/metrics.go` |
| Record helpers | `controllers/helpers.go` |
| Machine create / power-on / delete sites | `controllers/nutanixmachine_controller.go` |
| Create-stuck annotation + gauge refresh | `capx.nutanix.com/vm-create-started-at` in `getOrCreateVM` / `reconcileNormal` |
| Prism Wait timing | `controllers/task_errors.go` |
| Non-machine reconcile errors | cluster / failuredomain / metro / metrosite / virtualhadomain / metro_scaledown controllers |
| Alerts | `config/prometheus/alerts.yaml` |
