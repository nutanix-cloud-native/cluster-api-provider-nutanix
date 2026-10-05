# VMM create: Postman samples and Keploy record/replay

This is a lab for one Prism Central call: `POST /api/vmm/v4.3/ahv/config/vms`, plus reading the task and the VM. It exists so a single behaviour (duplicate request id, missing request id, unknown subnet, oversized VM) can be captured once and replayed later without standing up CAPX or Prism.

It does not change the CAPX controller.

## Read this first

There are two tools, and they do not call each other.

| Tool | What it is | When to use it |
| --- | --- | --- |
| Postman collection `VMM Create` | A catalog of HTTP requests, one per exercise row, with assertions | Inspect a call, send it by hand, or run the whole sheet |
| `hack/vmm-keploy` | A Go test that sends the same calls. Keploy records Prism's answers and plays them back | Freeze one behaviour and rerun that test with Prism offline |

Postman is the sample book. Keploy is the cassette player. Running a Postman request does not write a Keploy recording, and replaying a Keploy recording does not update Postman.

```text
You
 ├─ Postman  ──live HTTP──►  Prism Central
 └─ go test  ──live HTTP──►  Prism Central
       │
       └─ under `keploy mock record` the same go test writes keploy/<name>/
          under `keploy mock replay` those files answer instead of Prism
```

Most of the files you see are the sample book split into one file per request. You do not edit them to record or replay.

## What creates a real VM

Only these do, and only when they actually reach Prism:

| Run | Effect |
| --- | --- |
| Postman folder `exercise`, request `A1 happy create` | One VM, name `capx-vmm-<request-id>` |
| `go test -run TestLive/A1_happy_replay_and_get` with `VMM_LIVE=1` | One VM, same name pattern |
| Postman `manual` / `D7 burst creates`, or `VMM_RUN_BURST=1` | Up to 30 VMs |

Every other exercise request is expected to be rejected, or to replay a request id that Prism already accepted. Replay mode never reaches Prism, so it creates nothing.

`go test` inside `hack/vmm-keploy` does not call Prism unless `VMM_LIVE=1`.

## Day-to-day commands

Do this from a machine that can open TCP `9440` to Prism Central. A run from a network that cannot route to the Prism IPs fails before any case starts.

### 1. Fill in where Prism is, and which cluster, subnet, and image to use

Credentials stay out of git.

```bash
# ~/.config/capx-vmm/pc-dev.env
#   PC_HOST, PC_USER, PC_PASSWORD
set -a
source ~/.config/capx-vmm/pc-dev.env
set +a
export PC_ENDPOINT="https://${PC_HOST}:9440"
export PATH="$HOME/.keploy/bin:$PATH"
```

Use `~/.keploy/bin/keploy`. An older `keploy` 0.9 binary may already be on `PATH`; that one is a different program and is not this workflow.

Discover ids:

```bash
cd hack/vmm-keploy
export VMM_LIVE=1
go run . -discover
export PE_UUID=<printed> SUBNET_UUID=<printed> IMAGE_UUID=<printed>
```

Copy those three into `postman/environments/pc-dev.local.environment.yaml` as well if you want Postman to use them. That file is gitignored. The committed `postman/environments/pc-dev.environment.yaml` has an empty username and password on purpose.

The host you call is the `baseUrl` variable. An environment passed with `-e` overrides the value stored on the collection.

### 2. Send the sheet with Postman

From the repo root:

```bash
postman collection run "postman/collections/VMM Create" \
  -e postman/environments/pc-dev.local.environment.yaml \
  -i discover -k --no-report-events

postman collection run "postman/collections/VMM Create" \
  -e postman/environments/pc-dev.local.environment.yaml \
  -i exercise -k --no-report-events
```

`-i discover` and `-i exercise` are folder names. `-k` skips TLS verification because Prism uses a private CA. `--no-report-events` keeps the run off Postman's cloud.

Run one request by its filename stem:

```bash
postman collection run "postman/collections/VMM Create" \
  -e postman/environments/pc-dev.local.environment.yaml \
  -i "B1 omit request id" -k --no-report-events
```

`exercise` is ordered. A1 creates and saves `TASK_ID`. The next request polls that task. A2 resends the same `NTNX-Request-Id`. E1 resends the id from C5. E2 resends the id from D2. Running E1 or E2 alone, with those variables empty, is not a replay.

Do not run the `manual` folder as a batch. Those requests need a second user, a special subnet, or a Prism fault that this environment does not have. D7, inside `manual`, fires 30 creates.

### 3. Record one behaviour, then replay it

From `hack/vmm-keploy`, with `VMM_LIVE=1` and the three UUIDs exported.

Record "create with no request id":

```bash
keploy mock record --local --name B1 \
  -c "go test -count=1 -run TestLive/B1_missing_request_id"
```

That writes `hack/vmm-keploy/keploy/B1/`. Prism must be reachable for this step.

Replay it. The test process still thinks it is calling Prism. Keploy answers from the recording:

```bash
keploy mock replay --local --name B1 --on-miss fail \
  -c "go test -count=1 -run TestLive/B1_missing_request_id"
```

`--name` must match. `--on-miss fail` means a call that was not recorded fails the run instead of leaking through to Prism.

`--local` keeps the recording on disk. Without it, this Keploy build may try to use a cloud account.

The same pair of commands works for any row in the test table below. Change `--name` and the `-run` pattern together.

Recordings can contain the `Authorization` header. Delete that header from the YAML before committing a mock set.

## Why there are so many Postman files

`postman/collections/VMM Create/` is one collection. Postman's v3 format stores every request as its own YAML file, so the directory looks larger than the API.

| Folder | What the files are | Run it? |
| --- | --- | --- |
| `create/`, `task/`, `vm/` | Generated from the OpenAPI spec. One operation each: create VM, get task, list tasks, get VM. The `examples/` files under `.resources/` are the responses declared in the spec, not captures from Prism | Only if you want the bare operation |
| `discover/` | List clusters, subnets, images. Scripts save the first `extId` into the environment | Yes, once, before `exercise` |
| `exercise/` | One file per row that a normal Prism can answer, in run order | Yes |
| `manual/` | Rows that need a fixture this lab does not have | One at a time, when you have that fixture |

The spec those generated requests come from is `postman/specs/vmm-create.openapi.yaml`. Lint it with:

```bash
postman spec lint postman/specs/vmm-create.openapi.yaml --no-report-events
postman collection lint "postman/collections/VMM Create"
```

Regenerating the collection from the spec (`postman spec generate collection`) rewrites `create/`, `task/`, and `vm/`. It does not rebuild `exercise/`, `discover/`, or `manual/`. Do not regenerate over the collection if you have edited those generated requests by hand.

Collection auth is HTTP basic, username `{{basicAuthUsername}}` and password `{{basicAuthPassword}}`. Both variables are secret fields in the environment file.

## How a Postman exercise request is built

Every create in `exercise/` is the same JSON body, then a pre-request script changes one thing:

- A new `NTNX-Request-Id` (`{{$guid}}`) unless the row is a replay.
- VM name `capx-vmm-<that id>`.
- The mutation for that row (zero sockets, unknown subnet, and so on).

A1 stores the request id, the body, and the task id on the environment. A2 reads them back so the second POST is byte-for-byte the same call. C5 stores `REQ_ID_C5` for E1. D2 stores `REQ_ID_D2` and, if Prism returned 202, `TASK_ID_D2` for E2.

A1's poll script and D2's poll script call Prism again from `pm.sendRequest` in a loop. The saved URL on those requests is the same GET, so you can also send it once in the app.

`settings.strictSSL: false` is set on the exercise requests. `-k` on `postman collection run` does the same for the CLI.

## How the Go tests line up with the sheet

`hack/vmm-keploy` is its own module so `go test ./...` at the repo root does not enter it. Files:

| File | Role |
| --- | --- |
| `client.go` | HTTPS client, basic auth, `NTNX-Request-Id`, task polling, error-body parsing |
| `vm.go` | The shared create body. Reads `PE_UUID`, `SUBNET_UUID`, `IMAGE_UUID` |
| `cases_test.go` | `TestLive` subtests. This is what Keploy wraps |
| `main.go` | `go run . -discover` only |

TLS verification is disabled in the client for the same private CA.

| Sheet rows | Go subtest | What it asserts |
| --- | --- | --- |
| A1, A3, A4, A2 | `TestLive/A1_happy_replay_and_get` | 202, task `SUCCEEDED`, GET VM 200, GET task still that id, second POST returns the same task id |
| B1 | `TestLive/B1_missing_request_id` | 400 and `VMM-30400` |
| B2 | `TestLive/B2_request_id_not_uuid` | 400 and `VMM-30401` |
| B3 | `TestLive/B3_bad_auth` | 401 |
| C1 | `TestLive/C1_empty_body` | 400 |
| C2 | `TestLive/C2_zero_sockets` | 400 and `VMM-30102` |
| C3 | `TestLive/C3_power_state_on` | 400 and `VMM-30109` |
| C4 | `TestLive/C4_unknown_cluster` | 400 and `VMM-30106` |
| C5 and E1 | `TestLive/C5_unknown_subnet_then_E1_replay` | 400 `VMM-30604`, then the same id again, still 400, not 202 |
| C7 | `TestLive/C7_unknown_image` | 400 and `VMM-31201` |
| D2 and E2 | `TestLive/D2_oversized_then_E2_replay` | 400, or 202 then task `FAILED`. Replay returns the same status and, if there was a task, the same task id |
| D4 | `TestLive/D4_gpu` | 400 or 202. The code is logged; this row is a recording, not a single expected code |
| D7 | `TestLive/D7_burst` | Skipped unless `VMM_RUN_BURST=1`. Thirty creates, status counts logged |

A1 in Go includes A2, A3, and A4 because they share one request id and one task. Splitting them into four Keploy recordings would lose that id. The Postman folder keeps them as separate requests so you can look at each call.

These sheet rows are Postman `manual` requests only. The Go suite does not call them, because a healthy Prism will not produce the error:

| Row | What you need before the request means anything |
| --- | --- |
| B4 | A user who is not allowed to create VMs. Expect 403. The checked-in request still uses the normal account, so it is not B4 until you point auth at that user |
| C6 | A subnet this user cannot attach. Expect `VMM-30605` |
| C8 | PC 7.6+ and a cluster, subnet, or image outside the project. Expect `VMM-31701`, `VMM-31800`, or `VMM-31802` |
| D1 | A project migration in progress. Expect `VMM-31700` |
| D3 | A cluster with no host that can place the VM. Expect `VMM-34418` |
| D5 | A subnet whose IP pool is empty. Expect 202 and a failed subtask |
| D6 | VMM upgrading. Expect `VMM-10011` or `VMM-10012`. A successful create is not this case |
| D8 | Something in front of Prism that returns 500 or 503. Prism itself has no switch for this |

## What Keploy is doing

Keploy starts your test command and intercepts outgoing HTTP from that process. In record mode it forwards the call to Prism and saves the response. In replay mode it matches the outgoing call to the saved response and returns it.

The mock set name (`--name B1`) is the directory `keploy/B1/` under the current working directory. Run the commands from `hack/vmm-keploy` so recordings stay next to the tests. `--path` moves that directory if you need it elsewhere.

One recording per behaviour. A single recording of "all creates" is the wrong shape: Keploy would see several POSTs to the same path and could answer the unknown-subnet call with the happy-path body.

`--on-miss` choices:

| Value | Meaning |
| --- | --- |
| `fail` | Unknown call errors. Use this when you want the test to prove it stayed on the recording |
| `passthrough` | Unknown call goes to Prism and is not saved |
| `record` | Unknown call goes to Prism and is appended |

`go test -count=1` matters. The Go test cache would skip the process, and Keploy would record nothing.

After a recording exists, `VMM_LIVE=1` is still required because the test refuses to build an HTTP client without it. The client is constructed before Keploy serves the mock. The TCP connection itself does not have to succeed in replay.

## What this does not do

- It does not boot CAPX, a management cluster, or a `NutanixMachine`. The client is a standalone HTTP caller, so you can see Prism's behaviour before asking how CAPX classifies it. The classification notes for that are in `vijay/vmm-create-exercise.md`.
- The `examples/` YAML under `create/`, `task/`, and `vm/` are spec examples. They are not the bodies Prism returned. After a live Postman run you can save the real response with `postman collection example`.
- No Keploy mock set is checked in yet. The first record has to be made from a network that can reach Prism.
- Nothing here is committed automatically. `postman/environments/pc-dev.local.environment.yaml` is gitignored because it holds the password.

## Failure looks like this

| Symptom | Cause |
| --- | --- |
| `dial tcp ...:9440: i/o timeout` | This machine has no route to Prism. Fix the network, then rerun. The cases themselves have not failed |
| `set PE_UUID, SUBNET_UUID, and IMAGE_UUID` | Discover has not been exported into the shell |
| Postman `401` on every request | `basicAuthUsername` / `basicAuthPassword` are empty in the environment you passed |
| E1 or E2 does not match C5 or D2 | Those requests were run alone, so the saved request id was empty. Run `exercise` in order, or run the Go subtest that does both calls in one process |
| `keploy: unknown command` or a listener panic on port 6789 | The shell picked up Keploy 0.9. Put `~/.keploy/bin` first on `PATH` |
| Replay fails with a missed mock | The test issued a call that recording did not see, or `--name` does not match the directory you recorded |
