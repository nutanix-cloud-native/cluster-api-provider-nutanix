/*
Copyright 2026 Nutanix

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestAppMessage(t *testing.T) {
	raw := []byte(`{
		"data": {"error": [{
			"$objectType": "vmm.v4.error.AppMessage",
			"errorGroup": "VM_MISSING_REQUEST_ID_HEADER",
			"code": "VMM-30400",
			"message": "NTNX-Request-Id header is required"
		}]}
	}`)
	code, group, message := appMessage(raw)
	if code != "VMM-30400" || group != "VM_MISSING_REQUEST_ID_HEADER" || message == "" {
		t.Fatalf("parsed (%s, %s, %s)", code, group, message)
	}
}

func TestLive(t *testing.T) {
	if os.Getenv("VMM_LIVE") != "1" {
		t.Skip("set VMM_LIVE=1 plus PC_ENDPOINT (or PC_HOST), PC_USER, and PC_PASSWORD to call Prism Central")
	}
	if os.Getenv("PC_ENDPOINT") == "" && os.Getenv("PC_HOST") == "" {
		t.Fatal("VMM_LIVE=1 requires PC_ENDPOINT or PC_HOST")
	}
	client, err := newClientFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	t.Cleanup(cancel)

	t.Run("A1_happy_replay_and_get", func(t *testing.T) {
		mustPlacement(t)
		id := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa1"
		body := baseVM("capx-vmm-" + id)
		first := mustCreate(t, ctx, client, id, body, true)
		if first.Status != http.StatusAccepted {
			t.Fatalf("A1 HTTP %d code=%s body=%s", first.Status, first.Code, snippet(first.Body))
		}
		taskID := taskExtID(first.Body)
		if taskID == "" {
			t.Fatal("A1 response has no task extId")
		}
		status, vm, _, err := client.pollTask(ctx, taskID)
		if err != nil {
			t.Fatal(err)
		}
		if status != "SUCCEEDED" || vm == "" {
			t.Fatalf("A1 task %s status %s vm %q", taskID, status, vm)
		}
		got, err := client.do(ctx, call{method: http.MethodGet, path: "/api/vmm/v4.3/ahv/config/vms/" + vm, auth: true})
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != http.StatusOK {
			t.Fatalf("A3 HTTP %d body=%s", got.Status, snippet(got.Body))
		}
		again, err := client.do(ctx, call{method: http.MethodGet, path: "/api/prism/v4.4/config/tasks/" + taskID, auth: true})
		if err != nil {
			t.Fatal(err)
		}
		if again.Status != http.StatusOK || taskExtID(again.Body) != taskID {
			t.Fatalf("A4 HTTP %d task %s", again.Status, taskExtID(again.Body))
		}
		replay := mustCreate(t, ctx, client, id, body, true)
		if replay.Status != http.StatusAccepted || taskExtID(replay.Body) != taskID {
			t.Fatalf("A2 HTTP %d task %s, want %s body=%s", replay.Status, taskExtID(replay.Body), taskID, snippet(replay.Body))
		}
		t.Logf("A1 vm %s task %s replay matched", vm, taskID)
	})

	t.Run("B1_missing_request_id", func(t *testing.T) {
		mustPlacement(t)
		res := mustCreate(t, ctx, client, "", baseVM("capx-vmm-b1"), true)
		wantCode(t, res, http.StatusBadRequest, "VMM-30400")
	})

	t.Run("B2_request_id_not_uuid", func(t *testing.T) {
		mustPlacement(t)
		res := mustCreate(t, ctx, client, "not-a-uuid", baseVM("capx-vmm-b2"), true)
		wantCode(t, res, http.StatusBadRequest, "VMM-30401")
	})

	t.Run("B3_bad_auth", func(t *testing.T) {
		mustPlacement(t)
		bad := *client
		bad.Password = "wrong-password"
		res := mustCreate(t, ctx, &bad, "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbb3", baseVM("capx-vmm-b3"), true)
		if res.Status != http.StatusUnauthorized {
			t.Fatalf("B3 HTTP %d, want 401 body=%s", res.Status, snippet(res.Body))
		}
	})

	t.Run("C1_empty_body", func(t *testing.T) {
		res := mustCreate(t, ctx, client, "cccccccc-cccc-4ccc-8ccc-ccccccccccc1", map[string]any{}, true)
		if res.Status == http.StatusBadRequest {
			t.Logf("C1 rejected synchronously body=%s", snippet(res.Body))
			return
		}
		if res.Status != http.StatusAccepted {
			t.Fatalf("C1 HTTP %d body=%s", res.Status, snippet(res.Body))
		}
		taskID := taskExtID(res.Body)
		status, vm, raw, err := client.pollTask(ctx, taskID)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("C1 task %s status %s vm %q body %s", taskID, status, vm, snippet(raw))
		if status != "SUCCEEDED" && status != "FAILED" {
			t.Fatalf("C1 task status %s", status)
		}
	})

	t.Run("C2_zero_sockets", func(t *testing.T) {
		mustPlacement(t)
		body := baseVM("capx-vmm-c2")
		body["numSockets"] = 0
		res := mustCreate(t, ctx, client, "cccccccc-cccc-4ccc-8ccc-ccccccccccc2", body, true)
		if res.Status != http.StatusBadRequest {
			t.Fatalf("C2 HTTP %d body=%s", res.Status, snippet(res.Body))
		}
		text := string(res.Body)
		if !strings.Contains(text, "VMM-30102") && !strings.Contains(text, "validationErrorMessages") && !strings.Contains(text, "numSockets") {
			t.Fatalf("C2 body did not describe the bad socket count: %s", snippet(res.Body))
		}
	})

	t.Run("C3_power_state_on", func(t *testing.T) {
		mustPlacement(t)
		body := baseVM("capx-vmm-c3")
		body["powerState"] = "ON"
		rejectOrFailedTask(t, ctx, client, mustCreate(t, ctx, client, "cccccccc-cccc-4ccc-8ccc-ccccccccccc3", body, true))
	})

	t.Run("C4_unknown_cluster", func(t *testing.T) {
		mustPlacement(t)
		body := baseVM("capx-vmm-c4")
		body["cluster"] = map[string]any{"extId": "00000000-0000-0000-0000-000000000001"}
		rejectOrFailedTask(t, ctx, client, mustCreate(t, ctx, client, "cccccccc-cccc-4ccc-8ccc-ccccccccccc4", body, true))
	})

	t.Run("C5_unknown_subnet_then_E1_replay", func(t *testing.T) {
		mustPlacement(t)
		body := baseVM("capx-vmm-c5")
		body["nics"] = []any{map[string]any{"nicNetworkInfo": map[string]any{
			"$objectType": "vmm.v4.ahv.config.VirtualEthernetNicNetworkInfo",
			"subnet":      map[string]any{"extId": "00000000-0000-0000-0000-000000000002"},
		}}}
		id := "cccccccc-cccc-4ccc-8ccc-ccccccccccc5"
		first := mustCreate(t, ctx, client, id, body, true)
		taskID := rejectOrFailedTask(t, ctx, client, first)
		replay := mustCreate(t, ctx, client, id, body, true)
		if replay.Status != first.Status {
			t.Fatalf("E1 HTTP %d, first was %d body=%s", replay.Status, first.Status, snippet(replay.Body))
		}
		if taskID != "" && taskExtID(replay.Body) != taskID {
			t.Fatalf("E1 task %s, want %s", taskExtID(replay.Body), taskID)
		}
	})

	t.Run("C7_unknown_image", func(t *testing.T) {
		mustPlacement(t)
		body := baseVM("capx-vmm-c7")
		body["disks"] = []any{map[string]any{"backingInfo": map[string]any{
			"$objectType":   "vmm.v4.ahv.config.VmDisk",
			"diskSizeBytes": 21474836480,
			"dataSource": map[string]any{"reference": map[string]any{
				"$objectType": "vmm.v4.ahv.config.ImageReference",
				"imageExtId":  "00000000-0000-0000-0000-000000000003",
			}},
		}}}
		rejectOrFailedTask(t, ctx, client, mustCreate(t, ctx, client, "cccccccc-cccc-4ccc-8ccc-ccccccccccc7", body, true))
	})

	t.Run("D2_oversized_then_E2_replay", func(t *testing.T) {
		mustPlacement(t)
		body := baseVM("capx-vmm-d2")
		body["memorySizeBytes"] = 2199023255552
		body["numSockets"] = 256
		id := "dddddddd-dddd-4ddd-8ddd-ddddddddddd2"
		first := mustCreate(t, ctx, client, id, body, true)
		if first.Status != http.StatusBadRequest && first.Status != http.StatusAccepted {
			t.Fatalf("D2 HTTP %d code=%s body=%s", first.Status, first.Code, snippet(first.Body))
		}
		var failedTask string
		if first.Status == http.StatusAccepted {
			failedTask = taskExtID(first.Body)
			status, _, raw, err := client.pollTask(ctx, failedTask)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("D2 task %s status %s body %s", failedTask, status, snippet(raw))
			if status != "FAILED" {
				t.Fatalf("D2 task status %s, want FAILED", status)
			}
		}
		replay := mustCreate(t, ctx, client, id, body, true)
		if replay.Status != first.Status {
			t.Fatalf("E2 HTTP %d, first was %d body=%s", replay.Status, first.Status, snippet(replay.Body))
		}
		if failedTask != "" && taskExtID(replay.Body) != failedTask {
			t.Fatalf("E2 task %s, want %s", taskExtID(replay.Body), failedTask)
		}
	})

	t.Run("D4_gpu", func(t *testing.T) {
		mustPlacement(t)
		body := baseVM("capx-vmm-d4")
		body["gpus"] = []any{map[string]any{
			"$objectType": "vmm.v4.ahv.config.Gpu",
			"mode":        "PASSTHROUGH_COMPUTE",
			"vendor":      "NVIDIA",
			"deviceId":    0,
		}}
		res := mustCreate(t, ctx, client, "dddddddd-dddd-4ddd-8ddd-ddddddddddd4", body, true)
		if res.Status == http.StatusBadRequest {
			t.Logf("D4 rejected synchronously code=%s body=%s", res.Code, snippet(res.Body))
			return
		}
		if res.Status != http.StatusAccepted {
			t.Fatalf("D4 HTTP %d body=%s", res.Status, snippet(res.Body))
		}
		taskID := taskExtID(res.Body)
		status, vm, raw, err := client.pollTask(ctx, taskID)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("D4 task %s status %s vm %q body %s", taskID, status, vm, snippet(raw))
		if status != "FAILED" && status != "SUCCEEDED" {
			t.Fatalf("D4 task status %s", status)
		}
	})

	t.Run("D7_burst", func(t *testing.T) {
		if os.Getenv("VMM_RUN_BURST") != "1" {
			t.Skip("set VMM_RUN_BURST=1 to send 30 creates; this can create VMs")
		}
		mustPlacement(t)
		counts := map[int]int{}
		for i := 0; i < 30; i++ {
			id := newRequestID()
			body := baseVM("capx-vmm-burst-" + id)
			body["numSockets"] = 1
			body["numCoresPerSocket"] = 1
			res, err := client.do(ctx, call{
				method:    http.MethodPost,
				path:      "/api/vmm/v4.3/ahv/config/vms",
				body:      body,
				requestID: id,
				auth:      true,
			})
			if err != nil {
				t.Fatal(err)
			}
			counts[res.Status]++
		}
		t.Logf("D7 status counts %v", counts)
		if counts[http.StatusTooManyRequests] == 0 {
			t.Log("D7 did not observe HTTP 429")
		}
	})
}

func mustPlacement(t *testing.T) {
	t.Helper()
	if err := requirePlacement(); err != nil {
		t.Fatal(err)
	}
}

func mustCreate(t *testing.T, ctx context.Context, client *Client, requestID string, body any, auth bool) Response {
	t.Helper()
	res, err := client.do(ctx, call{
		method:    http.MethodPost,
		path:      "/api/vmm/v4.3/ahv/config/vms",
		body:      body,
		requestID: requestID,
		auth:      auth,
	})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// rejectOrFailedTask accepts a synchronous 400, or a 202 whose task later fails.
// A task that succeeds means Prism created a VM the case meant to reject.
func rejectOrFailedTask(t *testing.T, ctx context.Context, client *Client, res Response) string {
	t.Helper()
	if res.Status == http.StatusBadRequest {
		t.Logf("rejected HTTP 400 code=%s body=%s", res.Code, snippet(res.Body))
		return ""
	}
	if res.Status != http.StatusAccepted {
		t.Fatalf("HTTP %d code=%s body=%s", res.Status, res.Code, snippet(res.Body))
	}
	taskID := taskExtID(res.Body)
	if taskID == "" {
		t.Fatal("202 response has no task extId")
	}
	status, vm, raw, err := client.pollTask(ctx, taskID)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("task %s status %s vm %q body %s", taskID, status, vm, snippet(raw))
	if status != "FAILED" {
		t.Fatalf("task status %s, want FAILED", status)
	}
	return taskID
}

func wantCode(t *testing.T, res Response, status int, code string) {
	t.Helper()
	if res.Status != status {
		t.Fatalf("HTTP %d, want %d code=%s group=%s body=%s", res.Status, status, res.Code, res.Group, snippet(res.Body))
	}
	if code != "" && res.Code != code && !strings.Contains(string(res.Body), code) {
		t.Fatalf("code %q group %q, want %s body=%s", res.Code, res.Group, code, snippet(res.Body))
	}
}
