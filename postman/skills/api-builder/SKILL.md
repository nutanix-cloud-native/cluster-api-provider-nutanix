---
name: api-builder
description: Guide for building high-quality APIs by leveraging postman cli. Use when documenting an existing API, designing or changing apis, or writing API tests in this repo.
---

# API Development Guide

## Overview

spec         (none yet - author one before writing code)
collections  vijay/CAPX.postman_collection.json
cli          postman 1.69.0

---

# Process

## 🚀 High-Level Workflow

Working on an API always involves a three phase process, in order. Idea is code never moves ahead of the contract.

1. **Design the api contract.** Write or update the OpenAPI spec, then lint it. Generate the postman collection for the spec. Write appropriate api tests scripts.
2. **Make code satisfy the contract.** Implement the contract and ensure all api tests are passing by running the collection.
3. **Publish.** Ask the user before pushing artifacts to the cloud workspace.

Before you begin, ensure postman skills are up to date: run `postman skills status`. If it reports the skills are behind, run `postman skills update` and re-read this file.

---

### Phase 1: Design the Contract

#### 1.1 Research good API design (new endpoints)

When the work is a new feature, decide the design before writing the spec. Resource naming, the HTTP method, the status codes the operation returns, idempotency, pagination, and the error shape. These decisions are the contract, so they come first. Skip this when you are documenting code that already exists.

#### 1.2 Write or update the spec

Write OpenAPI 3 to the spec path the repo declares. Specs are written in postman/specs

Do not guess. If a method, status code, parameter, or auth scheme is not determinable from the code, record it as a gap and leave the field out. A spec that looks complete but is not poisons every assertion built on it.

#### 1.3 Lint the spec

Lint before anything derives from it. A change to the API is not done until the spec changes and the lint passes.

#### 1.4 Generate the collection and environment

Build the collection from the spec. Add appropriate test scripts for the requests.

The collection is a directory of YAML in the Postman Collection v3 format. Read the schema reference before writing any collection file.


---

### Phase 2: Make Code Satisfy the Contract

#### 2.1 Implement

Write or change the implementation so the running service matches the spec. The spec leads; the code follows it.

#### 2.2 Run the collection and surface the result

Prove the endpoint by running the collection. Surface the collection run result to user exactly as it printed, including failures.


---

### Phase 3: Publish

Publishing is the only step that leaves the machine. By default all the artifacts stay in the repo. Only once you push to cloud other team members will be able to view/collaborate on the api. Push should require user consent.

Precondition: the repo must record a workspace id, which `init` command writes. If it is missing, say so and stop. Do not invent one. Use the default push strategy, which only creates and updates.

---

# Reference Files

## 📚 Documentation Library

Load these as needed during the work.

### Load during Phase 1

- [Collection Schema v3](reference/collection_schema_v3.md) - The Postman Collection v3 directory and YAML format. Read before writing or editing any collection/environment file so you match the schema instead of inventing structure.
