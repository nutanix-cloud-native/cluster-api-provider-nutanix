# Webhooks in Cluster API Runtime Extensions (Nutanix)

This document describes how Kubernetes admission webhooks and the runtime hooks server are configured and used in this repository.

---

## Table of Contents

1. [Overview: Two Webhook Systems](#1-overview-two-webhook-systems)
2. [Admission Webhook Configuration](#2-admission-webhook-configuration)
3. [Admission Webhook Handlers](#3-admission-webhook-handlers)
4. [Request Flow](#4-request-flow)
5. [Testing](#5-testing)
6. [Reference Summary](#6-reference-summary)

---

## 1. Overview: Two Webhook Systems

This repo uses **two distinct webhook mechanisms**:

| System | Purpose | Location |
|--------|---------|----------|
| **Kubernetes admission webhooks** | Mutating/validating admission for Cluster API resources (e.g. `Cluster`) | `pkg/webhook/`, registered in `cmd/main.go`, configured via Helm `webhooks.yaml` |
| **Runtime hooks server** | Cluster API runtime extension hooks (e.g. BeforeClusterCreate, AfterControlPlaneInitialized) | `common/pkg/server/`, added to manager in `cmd/main.go` |

This document focuses on **Kubernetes admission webhooks**. The runtime hooks server is a separate HTTP server and protocol, not Kubernetes admission.

---

## 2. Admission Webhook Configuration

### 2.1 Controller Manager and Webhook Server

In **`cmd/main.go`**:

- A **webhook server** is created with `webhook.NewServer(webhookOptions)` and attached to the controller manager:
  - **Port**: `9444`
  - **Cert directory**: `/admission-certs` (configurable via `--admission-webhook-cert-dir`)

- Four admission endpoints are registered on that server:

| Path | Handler |
|------|---------|
| `/mutate-v1beta1-cluster` | Cluster defaulter |
| `/validate-v1beta1-cluster` | Cluster validator |
| `/mutate-v1beta1-addons` | Addons defaulter |
| `/preflight-v1beta1-cluster` | Preflight checks (validating) |

### 2.2 Kubernetes API Server Configuration (Helm)

The API server is told *when* to call these endpoints via:

**`charts/cluster-api-runtime-extensions-nutanix/templates/webhooks.yaml`**

This file defines:

- **MutatingWebhookConfiguration** with two webhooks:
  - `addons-defaulter.caren.nutanix.com` → path `/mutate-v1beta1-addons`
  - `cluster-defaulter.caren.nutanix.com` → path `/mutate-v1beta1-cluster`

- **ValidatingWebhookConfiguration** with two webhooks:
  - `cluster-validator.caren.nutanix.com` → path `/validate-v1beta1-cluster`
  - `preflight.cluster.caren.nutanix.com` → path `/preflight-v1beta1-cluster`

All webhooks:

- Target **`cluster.x-k8s.io`** / resource **`clusters`** (CREATE and/or UPDATE as specified).
- Use **matchConditions** so they only run when `has(object.spec.topology)` (ClusterClass-based clusters).
- Reference the admission **Service** created by the Helm chart and the paths above.
- Use **cert-manager** for CA injection via the annotation `cert-manager.io/inject-ca-from`.

### 2.3 Kubebuilder Markers

The `+kubebuilder:webhook` markers in the codebase document the same configuration and can be used for code generation:

- **`pkg/webhook/cluster/doc.go`** – mutate and validate cluster paths.
- **`pkg/webhook/addons/doc.go`** – addons defaulter path.
- **`pkg/webhook/preflight/doc.go`** – preflight path (including `timeoutSeconds=30`).

---

## 3. Admission Webhook Handlers

### 3.1 Cluster Mutating Webhook (`/mutate-v1beta1-cluster`)

- **Handler**: `cluster.NewDefaulter()` → `admission.MultiMutatingHandler` with one handler.
- **Implementation**: **Cluster UUID labeler** (`pkg/webhook/cluster/clusteruuidlabeler.go`).
  - Runs only for topology-based clusters.
  - Ensures a **cluster UUID annotation** exists (`api/v1alpha1.ClusterUUIDAnnotationKey`):
    - On **Create**: generates a new UUID (e.g. UUIDv7).
    - On **Update**: preserves or copies the UUID from the old object (important for cluster move operations where annotations may be stripped).
  - Returns a JSON patch to set the annotation.

### 3.2 Cluster Validating Webhook (`/validate-v1beta1-cluster`)

- **Handler**: `cluster.NewValidator()` → `admission.MultiValidatingHandler` with three validators.

| Validator | File | Purpose |
|-----------|------|---------|
| **ClusterUUIDLabeler** | `clusteruuidlabeler.go` | Ensures cluster has the UUID annotation and that it is **immutable** on update. |
| **NutanixValidator** | `nutanix_validator.go` | For provider `nutanix`: validates Prism Central vs control plane IP, credentials, failure domains, images, CIDRs, storage, etc. |
| **AdvancedCiliumConfigurationValidator** | `cilium_configuration_validator.go` | When kube-proxy is disabled, validates Cilium Helm values (e.g. template syntax and structure). |

All run only for topology clusters; Nutanix and Cilium validators no-op or skip when not applicable.

### 3.3 Addons Mutating Webhook (`/mutate-v1beta1-addons`)

- **Handler**: `addons.NewDefaulter()` → `admission.MultiMutatingHandler`.
- **Implementation**: Built from `allHandlers()` in `pkg/webhook/addons/defaulter.go`. When the **AutoEnableWorkloadClusterRegistry** feature gate is enabled, it registers **WorkloadClusterAutoEnabler** (`pkg/webhook/addons/registry/autoenabler.go`).
  - Operates on **Cluster** (the webhook rule is for `clusters`).
  - For topology clusters (without a skip annotation), if the registry addon and global image registry mirror are not already set, it **defaults** the cluster so the workload cluster registry addon is enabled (by patching cluster topology variables).

So “addons” here means defaulting the cluster so that addons (e.g. registry) are enabled, not a separate addons resource.

### 3.4 Preflight Webhook (`/preflight-v1beta1-cluster`)

- **Handler**: `preflight.New(client, decoder, checkers)` in `pkg/webhook/preflight/preflight.go`.
- **Behavior**:
  - **WebhookHandler** decodes the Cluster; skips on Delete, non-topology, or paused clusters. On Update, it may skip if the spec is unchanged (ignoring Paused).
  - **Skip list**: `skip.Evaluator` can skip individual checks or all checks based on cluster configuration.
  - **Checkers** are injected from `main.go`: **generic.Checker** and **preflightnutanix.Checker**. Each checker’s `Init(ctx, client, cluster)` returns a list of **Check**s; these run **concurrently** with a timeout (e.g. 30s minus buffer).
- **Generic checker** (`pkg/webhook/preflight/generic/checker.go`): Configuration and registry checks.
- **Nutanix checker** (`pkg/webhook/preflight/nutanix/checker.go`): Configuration, credentials, Prism Central version, failure domains, VM image and Kubernetes version checks, CIDR validation, storage container checks, etc.
- Results are aggregated: any **InternalError** or disallowed check yields a non-allowed response with causes; otherwise the request is allowed (optionally with warnings).

Preflight is a **validating** admission that runs heavier checks (including external Nutanix API calls) before admitting cluster create/update.

---

## 4. Request Flow

1. User creates or updates a **Cluster** with **spec.topology**.
2. The API server matches the webhook **rules** and **matchConditions** in `webhooks.yaml` and sends an **AdmissionReview** to the admission Service (port 9444) on the corresponding path.
3. The controller-runtime **webhook server** (same process as the manager) receives the request and dispatches by path:
   - **Mutating**: handler may return JSON patches (e.g. cluster UUID, addons defaulting).
   - **Validating**: handler allows or denies; preflight runs multiple checkers and then allows or denies with detailed causes.
4. The API server applies patches (mutating) or rejects/admits (validating) the request.

---

## 5. Testing

- **envtest** (`internal/test/envtest/environment.go`) starts a webhook server with the same options and can register the same admission handlers so integration tests hit the real webhook code.
- **Suite tests** (e.g. `pkg/webhook/cluster/webhook_suite_test.go`, `pkg/webhook/addons/registry/webhook_suite_test.go`) register the same paths against the test environment’s webhook server and drive admission tests.

---

## 6. Reference Summary

| Path | Type | Purpose |
|------|------|---------|
| `/mutate-v1beta1-cluster` | Mutating | Set or preserve cluster UUID annotation (topology clusters). |
| `/validate-v1beta1-cluster` | Validating | UUID immutability; Nutanix config validation; Cilium config validation. |
| `/mutate-v1beta1-addons` | Mutating | Default cluster so workload cluster registry addon is enabled (feature-gated). |
| `/preflight-v1beta1-cluster` | Validating | Generic and Nutanix preflight checks (credentials, Prism, images, CIDR, storage, etc.) with 30s timeout. |

Configuration is split between **Go** (handler registration and logic in `cmd/main.go` and `pkg/webhook/`) and **Kubernetes** (Helm template `webhooks.yaml` plus cert-manager for TLS). The **runtime hooks** in `common/pkg/server/` are a separate mechanism and are not Kubernetes admission webhooks.
