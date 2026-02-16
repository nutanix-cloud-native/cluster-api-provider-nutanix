# AGENTS.md

This file provides **AI coding agents** with project-specific instructions for the **Cluster API Provider Nutanix (CAPX)** repository.

The goals are:
- Keep changes aligned with existing controller + API patterns.
- Use the repo’s `make` targets as the source of truth for generation/lint/test steps.
- Avoid running expensive or environment-dependent workflows unless explicitly requested.

## Project Overview

This is a Kubernetes Cluster API infrastructure provider for Nutanix Cloud Infrastructure, written in Go. It enables declarative management of Kubernetes clusters on Nutanix infrastructure using the Cluster API pattern.

## Prerequisites

Only these tools need to be installed manually:
- [devbox](https://www.jetpack.io/devbox/docs/installing_devbox/) - Package management
- [direnv](https://direnv.net/docs/installation.html) - Environment management
- A container runtime: **Docker or Podman**

All other tools (Go, kubectl, Kind, clusterctl, controller-gen, etc.) are managed via devbox.

## Getting Started (Local Dev)

```bash
# Clone and enter repo
gh repo clone nutanix-cloud-native/cluster-api-provider-nutanix
cd cluster-api-provider-nutanix

# Dependencies will auto-install via devbox/direnv (may take time on first run)
# If direnv prompts, allow it:
direnv allow

# Build the project
make build

# Build container image
make docker-build

# Push container image to LOCAL_IMAGE_REGISTRY
make docker-push

# Generate manifests and code
make manifests
make generate
```

## Repo “Source of Truth”

- **Commands**: use `make help` to discover targets and rely on `Makefile` descriptions.
- **API types**: `api/v1beta1/`
- **Controllers**: `controllers/`
- **Shared packages**: `pkg/`
- **Manifests/CRDs/RBAC**: `config/`
- **Templates**: `templates/`
- **Unit tests**: co-located in packages (plus controller tests)
- **Template tests**: `templates/template_test.go` driven by `make template-test`
- **E2E tests**: `test/e2e/` (environment dependent; see below)

## Development Workflow

### Local Management Cluster (KIND)
```bash
# Create local KIND cluster
make kind-create

# Prepare local clusterctl
make prepare-local-clusterctl

# Deploy provider
make deploy
```

### Test Clusters

#### Without Topology (Traditional)
```bash
# Set required environment variables first (see developer_workflow.md)
make test-cluster-create
make generate-cluster-kubeconfig
make test-cluster-install-cni
make test-cluster-delete
```

#### With Topology (ClusterClass)
```bash
make test-cc-cluster-create
make generate-cc-cluster-kubeconfig
make test-cc-cluster-install-cni
make test-cc-cluster-delete
```

## Build / Generate / Test (Suggested Ladder)

Prefer this order; stop as soon as you have enough confidence for the change size.

### 1) Fast local correctness

- **Format**

```bash
make fmt
```

- **Static checks**

```bash
make vet
```

### 2) Code generation + manifests (when needed)

Run these when you change:
- API types / conversions / markers => `make generate`
- RBAC, CRDs, webhooks, markers => `make manifests`
- Cluster templates => `make cluster-templates`

```bash
make generate
make manifests
make cluster-templates
```

### 3) Unit tests (default)

`make unit-test` regenerates mocks as a dependency.

```bash
make unit-test
```

### 4) Template tests (heavier; still preferred over E2E)

`make template-test` builds an image and prepares local `clusterctl` overrides.

```bash
make template-test
```

### 5) E2E tests (avoid unless explicitly required)

See “E2E Testing Guidelines” below.

## Common `make` Targets

These are the ones agents should reach for first:

```bash
# Build
make build
make docker-build

# Generate
make generate
make manifests
make cluster-templates

# Test
make unit-test
make template-test

# Lint
make lint
make lint-yaml
```

## Code Style Guidelines

- Go modules with standard Go formatting (`gofmt`)
- Use `controller-gen` for CRD and RBAC generation
- Follow Kubernetes controller patterns
- Mock external dependencies for unit tests
- Use structured logging
- Add appropriate build tags for E2E tests (`//go:build e2e`)

## Testing Strategy

- **Unit Tests**: Use mocks for external dependencies (Nutanix API, Kubernetes client)
- **Template Tests**: Validate cluster template generation
- **E2E Tests**: Require actual Nutanix environment, use Ginkgo framework
- **Coverage**: Target reasonable coverage with `make coverage`

## Project Structure (High-Level)

- `/api/v1beta1/` - API types and validation
- `/controllers/` - Kubernetes controllers
- `/pkg/` - Shared packages (client, context)
- `/templates/` - Cluster templates and flavors
- `/test/e2e/` - End-to-end tests
- `/config/` - Kubernetes manifests (CRDs, RBAC, etc.)
- `/mocks/` - Generated mocks for testing

## Important Environment Variables

For development and testing, set these variables (see `docs/developer_workflow.md` for complete list):
- `NUTANIX_ENDPOINT` - Prism Central endpoint
- `NUTANIX_USER` - Nutanix username
- `NUTANIX_PASSWORD` - Nutanix password
- `NUTANIX_INSECURE` - Whether to skip TLS verification: usually if the Prism Central certificate is not from publicly trusted CA (`true`/`false`)
- `NUTANIX_PORT` - Prism Central API port (typically `9440`)
- `CONTROL_PLANE_ENDPOINT_IP` - Cluster endpoint IP
- `KUBERNETES_VERSION` - Target K8s version
- `NUTANIX_MACHINE_TEMPLATE_IMAGE_NAME` - VM image name

## Lint and Validation

```bash
make lint           # Run golangci-lint
make lint-fix       # Run linter with auto-fix
make lint-yaml      # Validate YAML files
make verify         # CI-style verification (see note below)
```

> Note on `make verify`: `verify-manifests` checks `git diff --quiet HEAD`.
> In CI (clean checkout), this is a good “generated files are up to date” guard.
> Locally, it may fail simply because you have uncommitted changes. Use it as a
> **pre-push/CI check**, not as your primary local loop.

## Release Process

```bash
make release-manifests  # Generate release artifacts in /out
```

## Container Registry

- Development images: `ko.local/cluster-api-provider-nutanix`
- Uses `ko` for building efficient container images

## Security Considerations

- Never commit credentials or secrets
- Use proper RBAC for controller permissions
- Validate all external inputs (Nutanix API responses)
- Follow Kubernetes security best practices
- Store sensitive data in Kubernetes secrets

## Debugging

```bash
# Check cluster resources
kubectl get cluster-api --namespace capx-test-ns

# Check provider logs  
kubectl logs -n capx-system -l cluster.x-k8s.io/provider=infrastructure-nutanix

# List resources
make list-bootstrap-resources
make list-workload-resources
```

## Common Issues

- First `cd` into directory downloads dependencies via devbox (may take time)
- E2E tests require valid Nutanix environment configuration
- Template validation requires proper environment variable setup
- Container builds use `ko` which requires access to a container engine (Docker/Podman)
- If using Podman, ensure the Podman socket service is available (the `Makefile` attempts to start it when needed)

## Notes for AI Agents

- Prefer `make help` + existing `make` targets over ad-hoc scripts.
- Use existing mocks in `/mocks/` rather than creating new ones
- Follow the existing controller patterns when adding new functionality
- Test templates with `make template-test` after modifications
- Reference `docs/developer_workflow.md` for detailed environment setup
- All development tools are managed by devbox - don't try to install them manually

### AI Agent Workflow Requirements

**Mandatory pre-push / PR confidence steps (default):**
```bash
# Generate artifacts affected by code changes
make generate
make manifests

# Format + validate
make fmt
make vet
make lint
make lint-yaml

# Unit tests
make unit-test

# Template tests (preferred over E2E for most changes): only if the changes in the code involved template changes
make template-test
```

**E2E Testing Guidelines:**
- AI agents should NOT trigger broad-scoped e2e tests that run all test suites
- If e2e testing is absolutely required, limit to cilium CNI with focused labels:
  - Use `make test-e2e-cilium` instead of `make test-e2e-calico`
  - Use focused `LABEL_FILTERS="quickstart && !clusterclass"` for basic validation
  - Avoid matrix strategies that run multiple e2e suites simultaneously
- For most agent tasks, unit tests and template tests provide sufficient coverage

**E2E quick examples (only when explicitly requested):**

```bash
# Dry-run: list which tests would run (safe)
LABEL_FILTERS="quickstart && !clusterclass" make list-e2e

# Focused run (environment required)
LABEL_FILTERS="quickstart && !clusterclass" make test-e2e-cilium
```