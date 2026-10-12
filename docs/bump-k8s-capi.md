# Bumping Kubernetes and Cluster API

Use `/bump-k8s-capi <k8s-minor> <capi-minor>` (for example,
`/bump-k8s-capi 1.35 1.13`), or follow this guide manually. This updates CAPX's
dependencies and development/test configuration; it is not an instruction to
release CAPX or migrate its infrastructure API contract.

Includes CAPX-specific lessons from [PR #718][capx-718]. That PR is a reference,
not a patch to replay: discover the current files and target release requirements
on every invocation.

## 1. Research and agree on the version set

Check `git status --short` first; preserve unrelated work. Read `AGENTS.md`,
`go.mod`, `Makefile`, and `devbox.json`, and use `make help` to discover current
targets. Use the devbox/direnv environment rather than installing tools manually.

Call the requested minors `K8S_MINOR` and `CAPI_MINOR`. If either is missing, ask
before editing. Honor explicitly requested patches; otherwise find the latest
stable patch **within each requested minor**, excluding drafts and prereleases:

```bash
gh api repos/kubernetes-sigs/cluster-api/releases --paginate \
  --jq '.[] | select(.draft == false and .prerelease == false) | .tag_name'
gh api repos/kubernetes/kubernetes/releases --paginate \
  --jq '.[] | select(.draft == false and .prerelease == false) | .tag_name'
```

Filter to the exact minor and compare versions numerically, not lexically or by
release-list position. Record the selected `CAPI_VERSION` and `K8S_VERSION`.

Before editing, record these additional choices and their evidence:

| Choice | Source / constraint |
| --- | --- |
| Kubernetes Go module patch | Target CAPI tag's `go.mod`; keep versioned Kubernetes staging modules on a compatible, consistent `v0.<minor>.<patch>` set. |
| controller-runtime | Target CAPI's supported minor; choose a stable patch in that minor. |
| Go version | Target CAPI/controller-runtime requirements, root `go.mod`, and available devbox packages. |
| KIND and management Kubernetes | KIND release notes must list a published `kindest/node` image for the chosen version. The management patch can differ from the workload patch. |
| Envtest | A published envtest asset version compatible with the code/tests; do not assume every workload patch is available. |
| cert-manager | Verify compatibility with clusterctl's default; change an explicit override only if one exists and the upgrade requires it. |
| Workload and upgrade-from Kubernetes | Supported CAPI skew and Nutanix VM images available for both versions. |
| etcd and CoreDNS | Target workload tag's `cmd/kubeadm/app/constants/constants.go` and release notes; do not copy another PR's values. |
| Cluster autoscaler | A published release matching the workload Kubernetes minor. |
| CAPX upgrade baseline | An actually published CAPX release compatible with the initial CAPI/Kubernetes versions. |

Fetch CAPI's dependency manifest and list its release documentation at the
selected tag. Quote API URLs containing `?` for zsh:

```bash
gh api "repos/kubernetes-sigs/cluster-api/contents/go.mod?ref=${CAPI_VERSION}" \
  --jq '.content' | base64 -d
gh api "repos/kubernetes-sigs/cluster-api/contents/docs/release/releases?ref=${CAPI_VERSION}" \
  --jq '.[].name'
```

Read the release and migration documents for **every crossed minor** and follow
their links to controller-runtime and provider-contract changes. File names vary
(for example, `release-1.13.md`); discover them rather than assuming a
`release-1.12-to-1.13.md` path exists. Check upstream's Kubernetes support matrix.
If the requested pair is unsupported, stop and ask rather than silently changing
the requested versions.

### Nutanix VM images

CAPX workload nodes boot Nutanix images. Check `NUTANIX_MACHINE_TEMPLATE_IMAGE_NAME`,
`NUTANIX_MACHINE_TEMPLATE_IMAGE_UPGRADE_FROM`,
`NUTANIX_MACHINE_TEMPLATE_IMAGE_UPGRADE_TO`, and image-lookup settings in the e2e
config. A plausible name such as `rockylinux-9-kube-v<version>.qcow2` is not proof
that the image exists in Prism Central or has the required Kubernetes binaries.
Verify availability using authorized environment access or ask the user to
confirm it. If unavailable, explicitly report live e2e as blocked; do not claim
the bump is validated on Nutanix. Never expose credentials in output or files.

## 2. Update dependencies and tooling

CAPX currently has one root Go module, including build-tagged `test/e2e`.
Rediscover module files if the layout has changed; update the existing dependency
and tooling files rather than introducing new ones.

Update CAPI and its test module to the **same exact tag**, and controller-runtime
to the researched compatible version:

```bash
go get sigs.k8s.io/cluster-api@${CAPI_VERSION} \
  sigs.k8s.io/cluster-api/test@${CAPI_VERSION} \
  sigs.k8s.io/controller-runtime@${CONTROLLER_RUNTIME_VERSION}
```

Inspect the target's nested modules before running this command. Starting with
CAPI 1.14, API types live in the separate `sigs.k8s.io/cluster-api/api` module;
include `sigs.k8s.io/cluster-api/api@${CAPI_VERSION}` in the same `go get` command.
Upstream's local replacements and zero-version placeholders do not resolve for
consumers. Pin the published API module tag explicitly, not a local replacement.

Align the Kubernetes staging modules present in `go.mod`, including indirect
ones such as `k8s.io/apiserver` and `k8s.io/cluster-bootstrap`, with the researched
version set. Do **not** set every `k8s.io/*` module to `v0.<minor>.<patch>`:
`k8s.io/utils`, `k8s.io/klog/v2`, and `k8s.io/kube-openapi` have independent
versioning. Review MVS's resulting versions with `go list -m all`.

Update `sigs.k8s.io/kind` if required by the selected KIND release. Let
`go mod tidy` resolve required transitive changes; avoid a blanket `go get -u`
and unrelated Nutanix SDK/CCM bumps. Preserve security fixes and inspect any
dependency downgrades. Review existing replacements; do not copy all upstream
`replace` directives, since dependency replacements are not inherited and may
be upstream development-only wiring.

Inspect `devbox.json` and regenerate `devbox.lock` through devbox when changing
packages. Many entries use `@latest`, but the lockfile still pins resolved versions;
editing package declarations alone does not prove tools were upgraded. Inspect
`hack/flakes/flake.nix` and its lockfile too: `setup-envtest` is a local flake
package with an explicit controller-runtime source revision and source/vendor
hashes. Update through Nix/devbox
when required; never guess the hashes. Coordinate Go, `kind`, `kubectl`, `clusterctl`, `setup-envtest`,
`controller-gen`, mockgen, Ginkgo, and golangci-lint pins only where the upgrade requires
it. A Go/tooling change must still work in the repo's CI devbox environment.
Run `go mod tidy` after source migrations as needed and include `go.sum`.

Check tools' build versions as well as package versions. Source-processing tools
such as mockgen must understand the target Go language version; an older build
can fail even when the `go` executable is new enough. Match Ginkgo's CLI to the
library version selected by the module graph. If required builds are absent from
the devbox catalog, ask whether to use the existing local-flake pattern or defer
the tooling change, and report the affected checks as blocked rather than passed.

## 3. Update test defaults, metadata, and upgrade fixtures

Search the current tree, including hidden CI files, for the **old exact versions**
and the variable names below. Classify matches before replacing them: historical
upgrade baselines and supported-version examples may intentionally remain old.

In `Makefile`, inspect `ENVTEST_K8S_VERSION` and other version defaults. Review
`.github/workflows/` for explicit Kubernetes/CAPI/tool pins. Review
`docs/developer_workflow.md` and `docs/getting_started.md` for affected examples;
do not claim support beyond what the researched compatibility matrix establishes.

In `test/e2e/config/nutanix.yaml`, coordinate:

- CAPI core, kubeadm bootstrap, and kubeadm control-plane versions: both provider
  manifest URLs and preloaded controller image tags.
- cert-manager compatibility with clusterctl's default, and explicit version/image
  overrides only where configured and required.
- `KUBERNETES_VERSION_MANAGEMENT`: the published KIND node version.
- `KUBERNETES_VERSION`, `KUBERNETES_VERSION_UPGRADE_TO`, and
  `KUBERNETES_VERSION_UPGRADE_FROM`: image-backed workload versions and supported
  upgrade skew, not necessarily the management version.
- Nutanix image names and image-lookup defaults for those workload versions.
- `ETCD_VERSION_UPGRADE_TO` and `COREDNS_VERSION_UPGRADE_TO`: versions verified
  against the target kubeadm release, unless a deliberate override is justified.
- `AUTOSCALER_VERSION`: the matching published Kubernetes-minor release.
- `INIT_WITH_BINARY` and `INIT_WITH_KUBERNETES_VERSION`: the initial clusterctl
  binary and Kubernetes version for the selected provider upgrade scenario.

`test/e2e/data/shared/metadata.yaml` describes **CAPI release series**. Add the
target CAPI minor if missing, preserving historical entries and mapping it to
the contract actually used by the test scenario.

Root `metadata.yaml` describes **CAPX release series**, not CAPI. Do not add a
CAPX minor just because CAPI gained one. If this work includes a new CAPX release
series, confirm that series and supported contract first. API object versions,
provider contract labels/metadata, and dependency versions are distinct: moving
kubeadm objects to `v1beta2` does not automatically migrate CAPX's `api/v1beta1`,
CRD storage version, or infrastructure contract.

### Keep the CAPX upgrade-from scenario coherent

Inspect `test/e2e/clusterctl_upgrade_test.go` alongside the YAML config. The test
can hardcode values that override configuration, including `InitWithBinary`,
core/bootstrap/control-plane provider versions, infrastructure provider version,
the scenario title, and `latestVersionString` in both pre/post-upgrade helpers.
Keep these consistent with the intended **starting** stack. Do not blindly set
all upgrade-from values to the target stack and erase upgrade coverage. If an
older clusterctl binary is intentional, document the skew and verify support.

When advancing the released CAPX baseline:

1. Verify its release assets exist with `gh release view <tag>`.
2. Update the matching `test/e2e/data/infrastructure-nutanix/<CAPX-tag>/` sources
   and remote release URL in `cluster-template/kustomization.yaml`.
3. Update the version-specific `cluster-e2e-templates-*` target and its dependency
   in `Makefile`, plus the config's template `sourcePath`.
4. Regenerate with `make cluster-e2e-templates`. Remove superseded fixtures only
   after confirming no remaining references; preserve intentional older scenarios.

## 4. Apply migration-driven code and template changes

Use the target migration guides, compilation errors, and upstream types as the
source of truth. Check controllers, webhook registrations, predicates, condition
utilities, import aliases, and e2e helper signatures. Regenerate mocks through
`make mocks`; do not patch generated mock methods manually.

PR #718 illustrates checks that a Go-only bump would miss. They are conditional
on the transition, not mandatory edits on every future bump:

- **Typed webhook defaulting:** controller-runtime introduced
  `admission.Defaulter[*NutanixMachineTemplate]` and a typed
  `ctrl.NewWebhookManagedBy` registration in
  `api/v1beta1/nutanixmachinetemplate_webhook.go`. Adapt tests to the new signature;
  preserve behavior and feature-gate coverage rather than removing failing tests.
- **E2E helper migrations:** check `test/e2e/md_rollout_test.go` for the transition
  from `MachineDeploymentRolloutSpec` to `KCPAndMachineDeploymentRolloutSpec`,
  and `test/e2e/test_helpers.go` for removed `CreateOrUpdateOpts`. Updated upstream
  tests can require KCP taints and propagation policies in the selected flavor.
  Keep test-only taints in test sources/flavors unless production-template behavior
  is explicitly intended and reviewed.
- **KCP/bootstrap schemas:** moving to `v1beta2` changes more than `apiVersion`.
  Check KCP `machineTemplate.spec.infrastructureRef` with `apiGroup`,
  `rollout.strategy.rollingUpdate`, removed `useExperimentalRetryJoin`, and
  map-to-`{name, value}`-list migrations for `extraArgs` and `kubeletExtraArgs`.
  Validate reference fields against each object's actual schema; do not globally
  replace `apiVersion` with `apiGroup`.
- **ClusterClass patches:** update selectors/references as well as objects.
  A map insertion such as `extraArgs/cloud-provider` becomes a list append at
  `extraArgs/-` with a `{name: cloud-provider, value: external}` value. Ensure the
  parent list exists and preserve version-gated cloud-provider behavior.
- **Nutanix-specific bootstrap settings:** preserve kube-vip, hostname setup,
  CCM injection, TLS cipher suites, and kubelet eviction thresholds. List-based
  kustomize patches can replace entire lists rather than merge by name: inspect
  rendered output to ensure CCM patches retain TLS arguments and eviction settings,
  and do not put kubelet-only flags into controller-manager arguments.
- **Ancillary networking:** inspect `test/e2e/data/cni/` for target-Kubernetes
  compatibility. PR #718 refreshed flannel/kindnet; those exact tags, registries,
  and RBAC changes are not a universal recipe. Update only with upstream evidence
  and coordinate any preload image references.

Edit template **sources** in `templates/base/`, `templates/clusterclass/`, flavor
directories (including `templates/overlays/` and `templates/failure-domains/`), and
`test/e2e/data/infrastructure-nutanix/v1beta1/`. Despite that directory name,
some contained kubeadm objects may use newer APIs. Include CSI/CSI3, scale-in,
failure-domain migration, and no-kubeproxy patches in the schema review.

Do not hand-edit generated `templates/cluster-template*.yaml` or e2e rendered
templates. Run `make cluster-templates` and `make cluster-e2e-templates`, then
review all affected flavors, with and without topology. Validate the rendered
structure against the selected CAPI CRDs as well as testing YAML syntax.
Inspect both basic-auth and API-key overlays where affected. Generation selects
an overlay through `NUTANIX_CREDENTIALS_TYPE`/`NUTANIX_CREDENTIALS_OVERLAY`, and an
exported `NUTANIX_API_KEY` changes the default; use explicit non-secret settings
to avoid accidentally changing the committed template defaults. Render alternate
overlays to scratch files for comparison rather than overwriting the intended
outputs. Generated e2e templates are gitignored; regenerate and test them locally,
but do not force-add them to the commit.

## 5. Regenerate and validate

Use the repo's make targets:

```bash
make generate
make manifests
make fmt
make vet
make lint
make lint-yaml
make unit-test
```

When templates change, also run:

```bash
make cluster-templates
make cluster-e2e-templates
make template-test
```

`make unit-test` regenerates mocks. `make template-test` builds an image and
prepares local clusterctl overrides, so it requires a working container runtime.
Report missing tools/environment requirements honestly instead of claiming a pass.

Plain Go builds and unit tests do not compile the `//go:build e2e` package. Always
include a non-provisioning e2e compile check for a dependency bump:

```bash
go test -tags=e2e -run '^$' ./test/e2e/...
go vet -tags=e2e ./test/e2e/...
```

The empty test selection avoids running `TestE2E` and its cluster setup. It does
not validate live infrastructure behavior. If VCS stamping fails in a partial
checkout, use `GOFLAGS=-buildvcs=false`, as the Makefile does for package discovery.

Rerun affected generators and confirm they introduce **no additional changes**
beyond the already reviewed diff. Do not expect `git diff --exit-code` against
HEAD to be empty while an uncommitted bump exists. Likewise, `make verify` can
fail its clean-HEAD guard merely because the working tree contains intended edits.
Finish with `git diff --check`, a full diff review, and a search for stale versions;
explain every intentionally retained baseline.

### Live e2e is opt-in

Do not launch broad Nutanix e2e suites or a CNI matrix by default. Only when
explicitly requested and credentials, images, and infrastructure are available,
start with the focused Cilium scenario allowed by `AGENTS.md`:

```bash
LABEL_FILTERS="quickstart && !clusterclass" make test-e2e-cilium
```

If ClusterClass or upgrade-specific coverage is needed, obtain approval for the
additional focused scenario. Do not change CI triggers, approval gates, runner
trust boundaries, or secrets as part of a version bump.

## 6. Report and hand off

Summarize the selected versions and evidence, migration changes, generated
artifacts, retained historical pins, and any follow-ups for published VM images
or external documentation. List each check as passed, failed, or not run, with
the reason. Distinguish compile/template validation from live Nutanix validation.

Do not commit, push, or open a PR unless requested. If asked, use conventional
commits (for example, `chore(deps): bump Kubernetes to 1.35 and CAPI to 1.13`),
following repository contribution requirements and its PR template. Keep source
migrations and generated results reviewable; avoid unrelated dependency churn.

[capx-718]: https://github.com/nutanix-cloud-native/cluster-api-provider-nutanix/pull/718
