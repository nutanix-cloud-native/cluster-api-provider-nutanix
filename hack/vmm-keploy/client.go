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
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Client talks to Prism Central the way the create exercise does.
// Keploy records these calls when the test process is started under
// `keploy mock record`, and serves them back under `keploy mock replay`.
type Client struct {
	BaseURL  string
	User     string
	Password string
	HTTP     *http.Client
}

type Response struct {
	Status  int
	Body    []byte
	Code    string
	Group   string
	Message string
}

func newClientFromEnv() (*Client, error) {
	base := os.Getenv("PC_ENDPOINT")
	if base == "" {
		host := os.Getenv("PC_HOST")
		if host == "" {
			return nil, fmt.Errorf("set PC_ENDPOINT or PC_HOST")
		}
		base = "https://" + host + ":9440"
	}
	user := os.Getenv("PC_USER")
	pass := os.Getenv("PC_PASSWORD")
	if user == "" || pass == "" {
		return nil, fmt.Errorf("set PC_USER and PC_PASSWORD")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // Prism Central uses a private CA.
	return &Client{
		BaseURL:  strings.TrimRight(base, "/"),
		User:     user,
		Password: pass,
		HTTP:     &http.Client{Timeout: 90 * time.Second, Transport: transport},
	}, nil
}

func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

type call struct {
	method    string
	path      string
	body      any
	requestID string // empty omits NTNX-Request-Id
	auth      bool
}

func (c *Client) do(ctx context.Context, in call) (Response, error) {
	var reader io.Reader
	if in.body != nil {
		raw, err := json.Marshal(in.body)
		if err != nil {
			return Response{}, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, in.method, c.BaseURL+in.path, reader)
	if err != nil {
		return Response{}, err
	}
	req.Header.Set("Accept", "application/json")
	if in.body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if in.requestID != "" {
		req.Header.Set("NTNX-Request-Id", in.requestID)
	}
	if in.auth {
		req.SetBasicAuth(c.User, c.Password)
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return Response{}, err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return Response{}, err
	}
	out := Response{Status: res.StatusCode, Body: raw}
	out.Code, out.Group, out.Message = appMessage(raw)
	return out, nil
}

func appMessage(raw []byte) (code, group, message string) {
	var wrap struct {
		Data struct {
			Error json.RawMessage `json:"error"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &wrap); err != nil || len(wrap.Data.Error) == 0 {
		return "", "", ""
	}
	var items []struct {
		Code       string `json:"code"`
		ErrorGroup string `json:"errorGroup"`
		Message    string `json:"message"`
	}
	if err := json.Unmarshal(wrap.Data.Error, &items); err == nil && len(items) > 0 {
		return items[0].Code, items[0].ErrorGroup, items[0].Message
	}
	var one struct {
		Code       string `json:"code"`
		ErrorGroup string `json:"errorGroup"`
		Message    string `json:"message"`
	}
	if err := json.Unmarshal(wrap.Data.Error, &one); err == nil {
		return one.Code, one.ErrorGroup, one.Message
	}
	return "", "", ""
}

func taskExtID(raw []byte) string {
	var wrap struct {
		Data struct {
			ExtID string `json:"extId"`
		} `json:"data"`
	}
	_ = json.Unmarshal(raw, &wrap)
	return wrap.Data.ExtID
}

func taskStatus(raw []byte) (status, vm string) {
	var wrap struct {
		Data struct {
			Status           string `json:"status"`
			EntitiesAffected []struct {
				ExtID string `json:"extId"`
				Rel   string `json:"rel"`
			} `json:"entitiesAffected"`
		} `json:"data"`
	}
	_ = json.Unmarshal(raw, &wrap)
	status = wrap.Data.Status
	for _, entity := range wrap.Data.EntitiesAffected {
		if entity.Rel == "vmm:ahv:config:vm" && entity.ExtID != "" {
			return status, entity.ExtID
		}
	}
	return status, ""
}

func snippet(raw []byte) string {
	text := strings.TrimSpace(string(raw))
	if len(text) > 500 {
		text = text[:500]
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, []byte(text)); err == nil {
		return buf.String()
	}
	return text
}

func (c *Client) pollTask(ctx context.Context, taskID string) (status, vm string, raw []byte, err error) {
	deadline := time.Now().Add(3 * time.Minute)
	for {
		res, err := c.do(ctx, call{
			method: http.MethodGet,
			path:   "/api/prism/v4.4/config/tasks/" + taskID,
			auth:   true,
		})
		if err != nil {
			return "", "", nil, err
		}
		status, vm = taskStatus(res.Body)
		switch status {
		case "SUCCEEDED", "FAILED", "CANCELED", "ABORTED":
			return status, vm, res.Body, nil
		}
		if time.Now().After(deadline) {
			return status, vm, res.Body, fmt.Errorf("task %s still %s", taskID, status)
		}
		select {
		case <-ctx.Done():
			return status, vm, res.Body, ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}
