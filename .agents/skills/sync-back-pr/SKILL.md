---
name: sync-back-pr
description: Bring internal-only work from the private fork's `internal/main` into this public repo, replacing the internal SDK and the internal prism-go-client fork with their public equivalents and stripping internal-only wiring. Use when asked to "sync back to upstream", "upstream our internal changes", "open a sync-back PR", "pull internal changes into the public repo", or similar.
---

# sync-back-pr — upstream `internal/main` into this repo

This repo (`nutanix-cloud-native/cluster-api-provider-nutanix`) is the public one. A private fork,
`nutanix-cloud-native/internal-cluster-api-provider-nutanix`, carries internal development on its
`internal/main` branch. Two flows move code between them:

| | direction | branch | base | lives in |
|---|---|---|---|---|
| `sync-pr` | public → internal | `issue/sync-main` | `internal/main` | the internal fork |
| `sync-back-pr` | internal → public | `chore/sync-from-internal` | `main` | here |

This is the higher-risk direction: it moves code out of a private repo into a public one, and it
applies a diff produced against internal's CI, registry and dependency wiring. Everything below
exists to stop something leaking, breaking public CI, or weakening this repo's security posture.

CAPX raises the stakes on leaks specifically, because this repo's release artifacts are not just a
container image. It publishes `infrastructure-components.yaml` and a dozen `cluster-template*.yaml`
files as GitHub release assets, and those YAMLs carry image references. An internal registry
hostname that reaches them ships to every `clusterctl` user, not just to a CI log. See §4.

## Before doing anything: check for an existing sync-back PR

```bash
gh pr list --repo nutanix-cloud-native/cluster-api-provider-nutanix --state open \
  --json number,title,headRefName,baseRefName,url
```

If a PR from `chore/sync-from-internal` is already open, update that branch in place. Never open
a duplicate.

Then check what else is in flight. A sync-back is large and will conflict with any open PR
touching `go.mod`, `controllers/` or `templates/`. If one exists, **stack on it**
(`--base <that branch>`) rather than racing it — merging either one first would leave the other
with a `go.mod` conflict.

## Before doing anything else: check prism-go-client

Unlike most sync-backs, this one has a hard cross-repo prerequisite. `internal/main` carries

```
replace github.com/nutanix-cloud-native/prism-go-client => github.com/nutanix-cloud-native/internal-prism-go-client vX.Y.Z
```

and calls converged APIs that may exist **only** in the internal fork. The public repo cannot
carry that `replace` — a public module must not resolve to a private one, and a public consumer
would fail to build. So the feature has to reach public `prism-go-client` *first*.

Check that the public module actually has what the synced code calls before writing any Go:

```bash
gh api "repos/nutanix-cloud-native/prism-go-client/contents/converged?ref=main" --jq '.[].name'
```

Compare that against every converged interface the synced code names — the `mockgen` lines in the
`mocks` Makefile target are the fastest enumeration, since each new mock corresponds to an
interface that must exist upstream:

```bash
git diff main -- Makefile | grep 'mockgen.*converged'
grep -roh 'converged\.[A-Z][A-Za-z]*' --include='*.go' . | sort -u
```

If a needed file is missing there, the public prism-go-client sync-back has not landed yet. Do
not work around it — no `replace`, no vendoring, no copying the source in. Either wait for that
repo's stack to merge and a tag to be cut, or, if the branch is needed now, pin a pseudo-version
at the head of its top-of-stack PR branch and say so plainly in the PR body with a TODO to re-pin:

```bash
go get github.com/nutanix-cloud-native/prism-go-client@<sha-of-top-of-stack>
```

A pseudo-version pin is a **blocked** PR, not a mergeable one. Mark it as such.

### The version-regression trap, unique to this repo

Because `internal/main` resolves prism-go-client through the `replace`, the version on its
`require` line is dead text and drifts backwards — it has sat at `v0.7.3` while this repo's `main`
moved to `v0.8.0` and beyond. Applying internal's diff therefore *downgrades* prism-go-client
while appearing to leave it alone, and the build still succeeds because the newer version is
API-compatible. Nothing catches this.

**The resulting version must be greater than or equal to the one on `main`.** Check explicitly,
against `main` and not against the internal branch:

```bash
git diff main -- go.mod | grep -A1 -B1 prism-go-client
```

## Working safely

Use a scratch worktree so the diff can be shaped without disturbing a checkout:

```bash
git worktree add /tmp/sync-back main
cd /tmp/sync-back
git fetch <path-or-url-of-internal-fork> internal/main:internal-main
git checkout -b chore/sync-from-internal internal-main
```

Rewrite dependencies and strip internal-only files (below), then squash onto `main`.

## 1. Dependencies: public SDK only, on latest tags

Rewrite every internal SDK import to its public equivalent:

```
github.com/nutanix-core/ntnx-api-golang-sdk-internal/<mod>-go-client/v17
  ->  github.com/nutanix/ntnx-api-golang-clients/<mod>-go-client/v4
```

At the time of writing that covers `clustermgmt`, `monitoring`, `multidomain`, `networking`,
`prism` and `vmm`. The model sub-paths are identical between the two SDKs
(`models/clustermgmt/v4/ahv/config`, `models/vmm/v4/ahv/policies`, …), so the rewrite is purely the
module prefix and major version — import aliases and every `clusterModels.` / `vmmModels.`
reference stay as they are.

Two paths are worth a second look because they are not where you would guess:

- `networking-go-client/v17/models/prism/v4/config` — a *prism* model shipped inside the
  **networking** module. It is not a typo and it does not become `prism-go-client/v4`.
- `vmm-go-client/v17/models/common/v1/config` — `v1`, not `v4`.

Confirm both resolve under the public module rather than assuming, since a wrong guess here
compiles only after you have also "fixed" the call sites.

**This repo is a single module.** Unlike `cloud-provider-nutanix`, `test/e2e` has no `go.mod` of
its own — it is part of the root module, gated behind `//go:build e2e`. That removes the
two-modules hazard and replaces it with a quieter one: `go build ./...` and `go test ./...` never
compile `test/e2e` at all. A sync-back can be green on both and still ship an e2e package that does
not build. Every verification step must be run twice, once with `-tags=e2e`. See §7.

Drop the prism-go-client `replace` directive and require the public module directly. Then pin each
ntnx-api module to its newest tag, reading tags from the source of truth rather than the module
proxy, which can lag:

```bash
git ls-remote --tags https://github.com/nutanix/ntnx-api-golang-clients.git \
  | awk '{print $2}' | sed 's|refs/tags/||' | grep '^<mod>-go-client/' | sort -V | tail -5
```

**Gotcha that has bitten before:** if `go.mod` still holds `v17.x` versions under a `/v4` module
path, `go get` aborts with *"invalid: should be v4, not v17"* and applies **none** of the
requested pins — including ones for modules you did rewrite. It reads like a warning; it is a
total no-op, and the result is a sync silently carrying stale SDK versions. Fix the versions in
`go.mod` first, then `go get`, then verify every line:

```bash
grep ntnx-api go.mod   # confirm each module is on the tag you intended
```

Prefer stable tags. A prerelease is acceptable only where a needed package genuinely does not
exist in the stable tag — internal features reach public betas first. `multidomain` is the usual
offender, because the project and resource-group APIs the synced code depends on are newer than
its last stable tag. Verify rather than assume:

```bash
go mod tidy   # names the exact missing package, e.g.
              # multidomain-go-client/v4@v4.3.1 does not contain .../request/projects
```

If a beta is required, say so in the PR body and name the package that forces it. Where the
version is inherited from prism-go-client's own `go.mod`, keep the two in step — a lower pin here
than prism-go-client requires will be silently upgraded by MVS anyway, so pin it deliberately.

## 2. No internal wiring may remain

```bash
grep -rn "ntnx-api-golang-sdk-internal\|nutanix-core\|internal-prism-go-client" \
  --include="*.go" --include="*.mod" --include="*.sum" .
```

Must come back empty — `go.sum` included.

Internal infrastructure hostnames and private sibling repos leak just as badly as Go imports, and
they hide in YAML that no compiler checks. In this repo they hide in *generated* YAML, which makes
them survive an otherwise careful review:

```bash
grep -rn "harbor.eng.nutanix.com\|ncn-prerelease\|internal-cluster-api-provider-nutanix\|internal-cloud-provider-nutanix\|dkp-container-images" \
  --exclude-dir=.git .
```

Also drop internal-only tooling that has no meaning here: the internal fork's `sync-pr` skill
(`.agents/skills/sync-pr` and its `.claude/skills` symlink) and `.claude/settings.local.json`.

**Do not delete this skill.** `sync-back-pr` lives in this repo. Depending on when the internal
fork last ran `sync-pr`, `internal/main` may not contain it, in which case the sync-back diff
will show it as a deletion. Keep it — same hazard class as the Codecov removal below.

## 3. `.github/` — the security-critical part

**Read every hunk and decide on it.** Do not reach for `git checkout main -- .github/`. A blanket
restore looks safe because it cannot leak anything, but it is a decision not to think, and it
silently reverts changes that genuinely belong here — most reliably the CI wiring for tests
arriving in the same sync, which then run unconfigured or not at all. Wholesale-keeping is
obviously wrong; wholesale-reverting is wrong in a way that passes CI.

Work through the diff hunk by hunk:

```bash
git diff main -- .github/
```

Every hunk is one of four things, and you have to say which:

1. **Internal-only infrastructure** → drop. Credentials, private registries, private module
   access, internal branch names. Catalogued below.
2. **A deletion or disabling of something only the public repo needs** → drop the change. The fork
   has no external contributors, no public coverage reporting and no reason to burn its runners on
   nightly conformance, so its CI legitimately lacks things this repo depends on. Catalogued below.
3. **A runner or trigger change** → drop, and treat it as a finding rather than a hunk.
   Catalogued below.
4. **Genuinely portable CI** → keep. Wiring for tests this sync adds, an action-version bump, a
   real fix to a job both repos run. Keep it, and justify it in the PR body.

The catalogues below are the lens, not the whole answer — they list what has come up before, and
a hunk that fits none of them still needs a judgement. When a hunk mixes categories, split it: the
fork's rewrite of `codeql-analysis.yml` is a single contiguous block containing a legitimate action
bump, a legitimate build fix, a runner move and a `continue-on-error` that neuters the job (see
"CodeQL" below).

### Internal additions that must NOT land here

- `GOPRIVATE` / `GONOSUMDB` env blocks — meaningless once dependencies are public. The fork adds
  them at the top of `blackduck.yaml`, `build-dev.yaml`, `codeql-analysis.yml`, `e2e.yaml`,
  `release.yaml` and `trivy-scan.yaml`.
- `actions/create-github-app-token` steps using `GHA_CHECKOUT_APP_ID` /
  `GHA_CHECKOUT_APP_PRIVATE_KEY` (one pair per workflow, for `nutanix-cloud-native` and
  `nutanix-core`), and the paired
  `git config --global url."https://x-access-token:...@github.com/...".insteadOf` rewrites.
  These mint credentials for private orgs. This repo runs workflows for **fork PRs**;
  token-minting steps here are a credential-exposure risk.
- Harbor release wiring in `release.yaml`: the `docker/login-action` step for
  `harbor.eng.nutanix.com` with `HARBOR_USERNAME` / `HARBOR_PASSWORD`, the "Build and push
  container to Harbor" `ko build` step, `NEW_IMG` repointed at
  `harbor.eng.nutanix.com/ncn-prerelease/internal-cluster-api-provider-nutanix`, the
  `make release-public-manifests` call, the extra `out/infrastructure-components-public.yaml`
  release asset, and the two-row Harbor-primary/GHCR-mirror changelog table. This repo publishes
  to GHCR only, and `NEW_IMG` must stay `ghcr.io/${{ github.repository }}/controller`.
- `actions/setup-java` in `blackduck.yaml`. It exists only because self-hosted runners ship no
  JDK; GitHub-hosted `ubuntu-latest` does. It arrives as a consequence of the runner move and goes
  with it.
- `DeterminateSystems/nix-installer-action` plus `skip-nix-installation: "true"` on
  `devbox-install-action`, wherever the fork adds it to a job this repo runs on a GitHub-hosted
  runner. Same reasoning: it is ARC-runner plumbing. (`e2e.yaml` already runs self-hosted in both
  repos and already has it — that job is not part of this hazard.)
- `internal/main` and `internal/release-*` branch triggers.
- Issue and PR templates rewritten to name `internal-cluster-api-provider-nutanix`.
- `if: github.repository == 'nutanix-cloud-native/internal-cluster-api-provider-nutanix'` guards.
  These never match here, so the job silently never runs. A permanently-skipped required check is
  worse than a failing one — it looks green.

### Internal removals that must NOT propagate here

A sync-back applies internal's *diff*, so a step deleted in the fork gets deleted here too. This
repo's fork-PR defences and its public-facing signal live entirely in things the fork has no use
for, which makes this the most dangerous section on the page. Guard against each explicitly:

- **Codecov.** `codecov/codecov-action` with `CODECOV_TOKEN` in `build-dev.yaml` is deleted on
  `internal/main`, because Codecov rejects tokenless uploads for private repos. It must stay here,
  along with the `EXPORT_RESULT` / `make coverage` plumbing it consumes.
- **The nightly conformance crons.** `calico-conformance-periodic.yaml`,
  `cilium-conformance-periodic.yaml`, `cilium-without-kubeproxy-conformance-periodic.yaml` and
  `flannel-conformance-periodic.yaml` each have their `schedule:` block **commented out** and
  replaced with `workflow_dispatch:` on `internal/main` — the fork does not want four nightly
  E2E runs on its own hardware. This is the easiest hunk on the page to wave through: it deletes
  nothing, it is four lines of comment, and it leaves a workflow that still exists and still
  passes. It also silently ends this repo's only recurring CNI conformance signal. Restore all
  four crons.
- **`check_approvals` jobs** using `nutanix-cloud-native/action-check-approvals`, and the
  `pull_request_target` triggers they gate. The fork keeps these today, but any hunk that weakens
  them is category 2 — removing the approval gate here would let an unapproved fork PR run
  integration jobs with secrets.
- This repo's `branches: [main, release-*]` triggers.

### CodeQL — read this hunk line by line

`codeql-analysis.yml` is the one file where all four categories appear inside a single rewritten
block, so it cannot be taken or dropped wholesale.

Portable (category 4, keep if verified):

- `github/codeql-action/init` and `analyze` bumped `v3` → `v4`.
- `with: languages: go` made explicit on `init`.
- Replacing `autobuild` with an explicit build step. Autobuild genuinely fails on this repo
  because Go comes from devbox and is not on the default `PATH`. Keep the intent, but the fork's
  step is `devbox run -- make build` *after* a self-hosted nix install — on `ubuntu-latest` the
  job needs `jetify-com/devbox-install-action` **without** `skip-nix-installation`.

Never (categories 1 and 3):

- `runs-on: [self-hosted-nutanix-docker-medium]`. CodeQL here runs on `ubuntu-latest`
  deliberately: untrusted fork code must compile in an ephemeral GitHub-hosted sandbox, **not** on
  self-hosted infrastructure.
- The two `create-github-app-token` steps and the `insteadOf` git config.
- `DeterminateSystems/nix-installer-action` and `skip-nix-installation: "true"`.
- **`continue-on-error: true` on the analyze step.** This is the worst line in the diff and the
  least conspicuous. It converts the repo's only static-analysis gate into a check that reports
  success no matter what CodeQL finds — the same "permanently green" failure as a
  `github.repository ==` guard, arriving as a single trailing line on an otherwise reasonable
  modernisation.

If verifying the `v4` bump against a GitHub-hosted runner is not practical inside this PR, leave
`codeql-analysis.yml` untouched and say so. Deferring it is fine; carrying it in half is not.

### Runner changes — never carry these back

The fork moves nearly every job onto `self-hosted-nutanix-docker-small` / `-medium`:
`check_approvals` and the build job in `blackduck.yaml` and `build-dev.yaml`, `CodeQL-Build`,
`check` in `conventional-pr-title.yaml`, `build_release` in `release.yaml`, and `Scan` in
`trivy-scan.yaml`. That is fine for a private repo where every contributor is trusted. Carrying it
here would let a fork PR execute attacker code on Nutanix-controlled runners. Combined with
`pull_request_target`, that is a textbook pwn-request.

**Rule:** jobs in this repo stay on the runner `main` puts them on, and CodeQL stays
GitHub-hosted. If a sync-back diff moves a job onto a self-hosted runner, or introduces
`pull_request_target` where this repo used `pull_request`, stop — that is a security regression,
not a sync.

### Category 4 in practice: wiring for tests you just added

This is the category a reviewer is least likely to miss and an automated sweep is most likely to
throw away. If the sync-back brings **new tests that read CI-supplied credentials**, the `env:`
lines feeding them are part of the change, not internal-specific wiring.

Getting it wrong is worse than it sounds, because the E2E scenarios `Skip()` when their variables
are unset rather than failing. Omit the wiring and CI stays green while the new scenarios never
run once — the same "permanently-skipped check looks green" hazard as a `github.repository ==`
guard, arriving from the opposite direction.

What has come up so far, all in the `Test build` step's `env:` in `.github/workflows/e2e.yaml`:

```yaml
NUTANIX_PROJECT_SCOPE_USER: ${{ secrets.NUTANIX_PROJECT_SCOPE_USER }}
NUTANIX_PROJECT_SCOPE_PASSWORD: ${{ secrets.NUTANIX_PROJECT_SCOPE_PASSWORD }}
NUTANIX_GPU_PHYSICAL_PROFILE_NAME: '${{ vars.NUTANIX_GPU_PHYSICAL_PROFILE_NAME }}'
NUTANIX_GPU_VIRTUAL_PROFILE_NAME: '${{ vars.NUTANIX_GPU_VIRTUAL_PROFILE_NAME }}'
```

plus the new entry in the `e2e-test` matrix in `build-dev.yaml` (`"projects"`), which is what
actually causes a scenario to run at all. A new test target that is never added to that matrix is
dead code in CI.

`build-dev.yaml` calls `e2e.yaml` with `secrets: inherit`, so no `secrets:` declaration is needed
on the reusable workflow — a bare reference is enough.

Carry the `env:` lines in the diff, then check the names resolve. **Repo secrets and variables
live in repo settings, not in the diff**, so a sync-back cannot create them and the reviewer
cannot see they are missing:

```bash
gh variable list --repo nutanix-cloud-native/cluster-api-provider-nutanix
gh secret list   --repo nutanix-cloud-native/cluster-api-provider-nutanix
```

Diff that against the internal fork's. Anything referenced but absent must be created by someone
with admin on this repo before the tests mean anything — call it out in the PR body as a required
follow-up, with the names, since it is work the PR itself cannot do.

### Before opening the PR

Read the surviving diff once more, as a whole:

```bash
git diff main -- .github/
```

There is no expected size. What there is, is an expectation that you can name the reason for
every remaining hunk, and that each one is category 4. List them in the PR body with those
reasons. A `.github/` diff nobody can explain hunk-by-hunk is the failure this section exists to
prevent — whether it is fifty lines long or zero.

## 4. Release artifacts and cluster templates — the second leak surface

This is where CAPX differs most from the other repos, and where a leak is worst. `templates/` and
`test/e2e/data/` are **generated, committed, and published**: `make release-manifests` copies
`cluster-template*.yaml` straight into the release, so a private hostname committed here is
downloaded by every `clusterctl` user.

The fork repoints the bundled CCM image at Harbor, in `templates/ccm/nutanix-ccm.yaml`, in every
generated `templates/cluster-template*.yaml`, and in
`test/e2e/data/infrastructure-nutanix/ccm-update.yaml`:

```
${CCM_REPO=harbor.eng.nutanix.com/ncn-prerelease/internal-cloud-provider-nutanix/controller}:${CCM_TAG=vX.Y.Z}
```

**None of it comes back.** Do not hand-edit the generated files — set the default and regenerate,
so the source template and its outputs cannot drift:

```bash
make update-ccm CCM_REPO=ghcr.io/nutanix-cloud-native/cloud-provider-nutanix/controller CCM_VERSION=<public tag>
make cluster-templates
```

Note that the `CCM_REPO=` *mechanism* — making the registry overridable at all, rather than
hardcoding `ghcr.io` — is a portable improvement worth keeping. Only the default value is
internal. Keep the `Makefile`'s `CCM_REPO ?=` line pointing at GHCR.

**The CCM *version* usually cannot come back either**, and this is the part that gets missed. The
fork tracks internal CCM builds, so `CCM_VERSION` names a tag that exists only in
`internal-cloud-provider-nutanix`. Pointing `CCM_REPO` at GHCR while keeping internal's tag
produces an image reference that resolves to nothing — worse than a stale pin, because it is
syntactically public and fails only at cluster-creation time. Check before deciding:

```bash
gh release list --repo nutanix-cloud-native/cloud-provider-nutanix --limit 5
```

If the fork's tag is not there, keep `main`'s `CCM_VERSION` and say so in the PR body.

That is not a defect in the sync — it is where this repo sits in a release chain that has to run
in order:

1. public `prism-go-client` sync-back stack merges, and a tag is cut
2. `cloud-provider-nutanix` re-pins to that tag and releases
3. this repo re-pins to that tag and releases
4. the two E2E bumps cross-reference each other — CAPX picks up the new CCM image, CCM picks up
   the new CAPX `infrastructure-components.yaml`

A sync-back is step 3's *input*, not step 4. Carrying a CCM bump or an E2E provider bump inside it
inverts the order and produces a PR that cannot pass E2E no matter how correct its Go is. Leave
both to the follow-up, and name the chain in the PR body so nobody re-derives it.

Also internal-only, and easy to miss because it lives outside `.github/`:

- `hack/release-public/kustomization.yaml` and the `release-public-manifests` Makefile target.
  Together they exist so internal can build a Harbor-based manifest and then rewrite the image to
  `ghcr.io/nutanix-cloud-native/dkp-container-images/nkp-cluster-api-provider-nutanix` for an NKP
  artifact. This repo builds a GHCR manifest directly and has no second artifact. The file names
  both a private source registry and a downstream product image — drop both.

The rest of the `Makefile` diff gets the same hunk-by-hunk treatment as `.github/`. New
`cluster-e2e-templates-v1beta1` entries and new `mockgen` lines under `mocks/converged/` are
category 4 and must be kept, or the templates and mocks this sync adds are never generated.

The new cluster templates themselves are safe once the CCM default is fixed: they otherwise
reference public kube-vip and registry.k8s.io images. Check rather than assume, since they are
generated files that a future bump could re-point.

## 5. Regenerate rather than hand-edit

Several trees in this repo are generated, and a sync-back that edits them directly will pass
review and then fail the first CI run that regenerates:

```bash
make generate manifests   # deepcopy + CRDs under config/crd/bases
make mocks                # mocks/converged/*, mocks/nutanix/*
make cluster-templates    # templates/cluster-template*.yaml
make cluster-e2e-templates
git diff --exit-code      # must be empty
```

`make mocks` matters most here, because the mocks are generated **from prism-go-client's
interfaces**. Regenerating them is the only check that the public module's converged interfaces
actually match what the synced controller code calls — a stale committed mock will compile happily
against a signature that no longer exists upstream.

## 6. Commit shape

Squash into a single commit on top of `main`. The internal history carries internal ticket IDs
and internal-CI commits describing infrastructure that does not exist here, and a merge would
import commits whose content this skill then reverts — incoherent public history.

Squashing costs per-commit authorship, so preserve it explicitly:

```bash
git log --format='%an <%ae>' main..internal-main | sort -u \
  | grep -viE "noreply@github|dependabot|actions@github"
```

Add each as a `Co-authored-by:` trailer. This is a public repo; do not erase contributors.

Per global instructions, no Claude/Anthropic attribution in commit messages or PR descriptions.

## 7. Verify before opening the PR

```bash
go build ./... && go vet ./...
go test ./...
go vet -tags=e2e ./test/e2e/...
go test -tags=e2e -run '^$' ./test/e2e/...   # compile/smoke check, provisions nothing
gofmt -l . | grep -v '^templates/\|^test/e2e/data/'
make lint
```

The `-tags=e2e` runs are not optional. `test/e2e` is in this module but behind a build tag, so a
plain `./...` build never compiles it — the single most likely way for this sync to ship broken
code is an e2e package that nobody built.

If `go list ./...` fails in a partial checkout or CI sandbox, set `GOFLAGS=-buildvcs=false`; the
`Makefile` already does this for `GOTESTPKGS` and the same applies to ad-hoc invocations.

Finish with §5's `git diff --exit-code` after regenerating — CRDs, deepcopy, mocks and templates
must be in sync with their sources, not merely present.

The CAPX E2E suite provisions real VMs against a live Prism Central and needs `NUTANIX_ENDPOINT`,
`NUTANIX_USER`/`NUTANIX_PASSWORD`, `NUTANIX_PRISM_ELEMENT_CLUSTER_NAME`, `NUTANIX_SUBNET_NAME`,
`CONTROL_PLANE_ENDPOINT_IP` and a reachable image registry. The project-scoped, GPU-profile and
VMProfile scenarios need a PC on **7.6 or newer**; below that they either skip or take a path
where the assertions are meaningless. If you cannot reach such a cluster, say so plainly in the PR
body rather than implying the sync was verified end to end.

## 8. PR body

Title: `feat: sync internal fork changes using public API SDKs`

Include:

1. **What is being upstreamed** — the new public API surface, in a paragraph.
2. **SDK versions**, the public prism-go-client version, and any prerelease or pseudo-version pin
   with the reason and the package that forces it. State explicitly that prism-go-client did not
   go backwards.
3. **What was deliberately excluded** — internal CI, Harbor release wiring, `hack/release-public/`,
   the `sync-pr` skill — and why.
4. **Anything a reviewer would otherwise miss** — exported-constant renames, CRD or API field
   changes, template default changes (e.g. `NUTANIX_MACHINE_BOOT_TYPE`), call-convention changes,
   inverted test assertions.
5. **Honest test status**, including what was not run.

## Checklist

- [ ] No duplicate sync-back PR; stacked on any conflicting in-flight PR
- [ ] Public prism-go-client actually carries every converged interface the synced code calls
- [ ] No `replace` directive to `internal-prism-go-client`
- [ ] prism-go-client version is **not lower** than the one on `main`
- [ ] Zero `nutanix-core` / `ntnx-api-golang-sdk-internal` references, `go.sum` included
- [ ] Zero `harbor.eng.nutanix.com`, `ncn-prerelease`, `dkp-container-images` or
      private-sibling-repo references anywhere, generated YAML included
- [ ] Every public SDK module on its newest appropriate tag, re-checked in `go.mod` after `go get`
- [ ] Prereleases justified by a genuinely missing package, and named in the PR body
- [ ] Every surviving `.github/` hunk reviewed individually and its reason stated in the PR body
- [ ] New tests' `env:` wiring carried over **and** the new `e2e-test` matrix entries kept; every
      secret/var it names exists in this repo's settings — or the missing names are flagged in the
      PR body as a required follow-up
- [ ] Codecov, the four nightly conformance crons, and `check_approvals` + `pull_request_target`
      still present; no `continue-on-error` on CodeQL
- [ ] No job moved to a self-hosted runner; no new `pull_request_target`
- [ ] CCM image default regenerated to GHCR across `templates/` and `test/e2e/data/`, and
      `CCM_VERSION` left at a tag that actually exists in public `cloud-provider-nutanix`
- [ ] No CCM or E2E provider bump carried inside the sync; release chain named in the PR body
- [ ] `hack/release-public/` and `release-public-manifests` not carried over; `release.yaml`
      publishes to GHCR only
- [ ] Generated trees regenerated, not hand-edited; `git diff --exit-code` clean afterwards
- [ ] `sync-pr` and `.claude/settings.local.json` not carried over; this skill not deleted
- [ ] Single squashed commit with `Co-authored-by:` trailers for every human author
- [ ] Build, vet, unit tests and lint pass, **including the `-tags=e2e` runs**; unrun E2E
      disclosed in the PR body
