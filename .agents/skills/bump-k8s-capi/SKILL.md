---
name: bump-k8s-capi
description: Bump Kubernetes and Cluster API minor versions across CAPX. Use when upgrading Kubernetes or CAPI dependencies, tooling, templates, and e2e configuration. Includes migration-guide-driven adaptations and Nutanix-specific image and upgrade-fixture checks.
argument-hint: <k8s-minor> <capi-minor> (e.g. 1.35 1.13)
---

# Bump Kubernetes and Cluster API versions

Target versions: **$ARGUMENTS**

Read and follow [the CAPX upgrade guide](../../../docs/bump-k8s-capi.md), resolved
from this skill directory. The first argument is the target Kubernetes minor;
the second is the target CAPI minor. Ask for missing target versions before
editing. Explicit patch versions supplied by the user take precedence over
the guide's latest-stable-patch discovery.

Follow the repository's `AGENTS.md`. Do not commit, push, open a PR, or provision
Nutanix infrastructure unless requested.
