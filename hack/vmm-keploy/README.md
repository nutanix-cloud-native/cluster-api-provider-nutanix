# VMM create record and replay

Go tests that call `POST /api/vmm/v4.3/ahv/config/vms`. Keploy records Prism's
response for one case and replays it later. The same cases are the Postman
collection `VMM Create`.

The walkthrough, the file map, and which runs create a VM are in
[docs/vmm-create-record-replay.md](../../docs/vmm-create-record-replay.md).

The open-source Keploy binary cannot record a native process on macOS. Run the
test in Docker so Keploy can intercept it. Request ids in `cases_test.go` are
fixed so a replay sends the same bytes as the recording. Recorded sets are in
`keploy/<name>/`.

```bash
export PATH="$HOME/.keploy/bin:$PATH"
# VMM_LIVE=1, PC_ENDPOINT, PC_USER, PC_PASSWORD, PE_UUID, SUBNET_UUID, IMAGE_UUID

keploy mock record --local --name B1 --cmd-type docker-run \
  -c "docker run --rm --name vmmkeploy --env-file \"$PC_ENV_FILE\" -v \"$PWD\":/src -w /src golang:1.25 go test -count=1 -run TestLive/B1_missing_request_id"

keploy mock replay --local --name B1 --on-miss fail --cmd-type docker-run \
  -c "docker run --rm --name vmmkeploy --env-file \"$PC_ENV_FILE\" -v \"$PWD\":/src -w /src golang:1.25 go test -count=1 -run TestLive/B1_missing_request_id"
```
