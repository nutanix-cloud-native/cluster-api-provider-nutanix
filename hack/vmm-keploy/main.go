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
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"
)

func main() {
	discover := flag.Bool("discover", false, "print the first cluster, subnet, and image extId")
	flag.Parse()
	if !*discover {
		fmt.Fprintln(os.Stderr, "record and replay a case with go test, for example:")
		fmt.Fprintln(os.Stderr, `  keploy mock record --local --name B1 -c "go test -count=1 -run TestLive/B1_missing_request_id"`)
		fmt.Fprintln(os.Stderr, "  go run . -discover    # print PE_UUID, SUBNET_UUID, IMAGE_UUID")
		os.Exit(2)
	}
	client, err := newClientFromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	printFirst(ctx, client, "PE_UUID", "/api/clustermgmt/v4.0/config/clusters")
	printFirst(ctx, client, "SUBNET_UUID", "/api/networking/v4.0/config/subnets")
	printFirst(ctx, client, "IMAGE_UUID", "/api/vmm/v4.3/content/images")
}

func printFirst(ctx context.Context, client *Client, label, path string) {
	res, err := client.do(ctx, call{method: http.MethodGet, path: path, auth: true})
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", label, err)
		return
	}
	var wrap struct {
		Data []struct {
			ExtID string `json:"extId"`
			Name  string `json:"name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(res.Body, &wrap); err != nil || res.Status != http.StatusOK || len(wrap.Data) == 0 {
		fmt.Fprintf(os.Stderr, "%s: HTTP %d %s\n", label, res.Status, snippet(res.Body))
		return
	}
	fmt.Printf("%s=%s # %s\n", label, wrap.Data[0].ExtID, wrap.Data[0].Name)
}
