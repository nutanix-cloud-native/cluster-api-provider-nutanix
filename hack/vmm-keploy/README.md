# VMM create record and replay

Go tests that call `POST /api/vmm/v4.3/ahv/config/vms`. Keploy records Prism's
response for one case and replays it later. The same cases are the Postman
collection `VMM Create`.

The walkthrough, the file map, and which runs create a VM are in
[docs/vmm-create-record-replay.md](../../docs/vmm-create-record-replay.md).

Record and replay one case from this directory:

```bash
export PATH="$HOME/.keploy/bin:$PATH"
export VMM_LIVE=1 PC_ENDPOINT="https://${PC_HOST}:9440"
# PC_USER, PC_PASSWORD, PE_UUID, SUBNET_UUID, IMAGE_UUID

keploy mock record --local --name B1 \
  -c "go test -count=1 -run TestLive/B1_missing_request_id"

keploy mock replay --local --name B1 --on-miss fail \
  -c "go test -count=1 -run TestLive/B1_missing_request_id"
```
