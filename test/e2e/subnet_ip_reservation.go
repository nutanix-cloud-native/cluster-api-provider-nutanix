//go:build e2e

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

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	v4Converged "github.com/nutanix-cloud-native/prism-go-client/converged/v4"
	networkingcommonapi "github.com/nutanix/ntnx-api-golang-clients/networking-go-client/v4/models/common/v1/config"
	networkingapi "github.com/nutanix/ntnx-api-golang-clients/networking-go-client/v4/models/networking/v4/config"
	prismcommonapi "github.com/nutanix/ntnx-api-golang-clients/prism-go-client/v4/models/common/v1/config"
	prismapi "github.com/nutanix/ntnx-api-golang-clients/prism-go-client/v4/models/prism/v4/config"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/ptr"
)

const (
	ipReservationTaskPollInterval = 2 * time.Second
	ipReservationTaskTimeout      = 2 * time.Minute
)

// reservedIPsCompletionDetails mirrors the "reserved_ips" completion detail Prism Central
// attaches to a successful ReserveIpsBySubnetId task.
type reservedIPsCompletionDetails struct {
	ReservedIPs []string `json:"reserved_ips"`
}

// reserveSubnetIP asks Prism Central to atomically reserve a single free IP address from the
// given subnet's own IPAM pool, and returns a function that releases it again. Because Prism
// Central owns the reservation, concurrent callers (e.g. separate e2e CI jobs targeting the same
// subnet) can never be handed the same address.
func reserveSubnetIP(
	ctx context.Context,
	convergedClient *v4Converged.Client,
	subnetUUID string,
) (ip string, unreserve func() error, err error) {
	taskRef, err := convergedClient.Subnets.ReserveIpsBySubnetId(
		ctx,
		subnetUUID,
		&networkingapi.IpReserveSpec{
			Count:       ptr.To[int64](1),
			ReserveType: ptr.To(networkingapi.RESERVETYPE_IP_ADDRESS_COUNT),
		},
	)
	if err != nil {
		return "", nil, fmt.Errorf("failed to reserve IP in subnet %s: %w", subnetUUID, err)
	}
	if taskRef == nil || taskRef.ExtId == nil {
		return "", nil, fmt.Errorf("no task id found in reserve-IP response: %+v", taskRef)
	}

	completionDetails, err := waitForNetworkingTask(ctx, convergedClient, *taskRef.ExtId)
	if err != nil {
		return "", nil, fmt.Errorf("failed to wait for IP reservation task: %w", err)
	}
	if len(completionDetails) == 0 {
		return "", nil, fmt.Errorf("reserve-IP task for subnet %s completed with no details", subnetUUID)
	}

	// The reserved address comes back as a JSON-encoded string in the first completion detail
	// value, e.g. `"{\"reserved_ips\":[\"10.0.0.5\"]}"`.
	marshaledValue, err := json.Marshal(completionDetails[0].Value)
	if err != nil {
		return "", nil, fmt.Errorf("failed to marshal reserve-IP completion details: %w", err)
	}
	unquoted, err := strconv.Unquote(string(marshaledValue))
	if err != nil {
		return "", nil, fmt.Errorf("failed to unquote reserve-IP completion details %s: %w", marshaledValue, err)
	}

	var reserved reservedIPsCompletionDetails
	if err := json.Unmarshal([]byte(unquoted), &reserved); err != nil {
		return "", nil, fmt.Errorf("failed to unmarshal reserve-IP completion details %s: %w", unquoted, err)
	}
	if len(reserved.ReservedIPs) == 0 {
		return "", nil, fmt.Errorf("reserve-IP task for subnet %s reserved no addresses", subnetUUID)
	}

	reservedIP := reserved.ReservedIPs[0]
	return reservedIP, func() error {
		return unreserveSubnetIP(ctx, convergedClient, subnetUUID, reservedIP)
	}, nil
}

// unreserveSubnetIP releases a previously reserved IP address back to the subnet's IPAM pool.
func unreserveSubnetIP(ctx context.Context, convergedClient *v4Converged.Client, subnetUUID, ip string) error {
	ipAddress := networkingcommonapi.NewIPAddress()
	ipAddress.Ipv4 = networkingcommonapi.NewIPv4Address()
	ipAddress.Ipv4.Value = ptr.To(ip)

	taskRef, err := convergedClient.Subnets.UnreserveIpsBySubnetId(
		ctx,
		subnetUUID,
		&networkingapi.IpUnreserveSpec{
			UnreserveType: ptr.To(networkingapi.UNRESERVETYPE_IP_ADDRESS_LIST),
			IpAddresses:   []networkingcommonapi.IPAddress{*ipAddress},
		},
	)
	if err != nil {
		return fmt.Errorf("failed to unreserve IP %s in subnet %s: %w", ip, subnetUUID, err)
	}
	if taskRef == nil || taskRef.ExtId == nil {
		return fmt.Errorf("no task id found in unreserve-IP response: %+v", taskRef)
	}

	if _, err := waitForNetworkingTask(ctx, convergedClient, *taskRef.ExtId); err != nil {
		return fmt.Errorf("failed to wait for IP unreservation task: %w", err)
	}

	return nil
}

// waitForNetworkingTask polls a Prism Central task until it succeeds, and returns its completion
// details. It fails fast on task failure/cancellation or context cancellation.
func waitForNetworkingTask(
	ctx context.Context,
	convergedClient *v4Converged.Client,
	taskID string,
) ([]prismcommonapi.KVPair, error) {
	taskCtx, cancel := context.WithTimeout(ctx, ipReservationTaskTimeout)
	defer cancel()

	var completionDetails []prismcommonapi.KVPair
	if err := wait.PollUntilContextCancel(
		taskCtx,
		ipReservationTaskPollInterval,
		true,
		func(ctx context.Context) (bool, error) {
			task, err := convergedClient.Tasks.Get(ctx, taskID)
			if err != nil {
				return false, fmt.Errorf("failed to get task %s: %w", taskID, err)
			}

			switch ptr.Deref(task.Status, prismapi.TASKSTATUS_UNKNOWN) {
			case prismapi.TASKSTATUS_SUCCEEDED:
				completionDetails = task.CompletionDetails
				return true, nil
			case prismapi.TASKSTATUS_FAILED, prismapi.TASKSTATUS_CANCELED:
				return false, fmt.Errorf("task %s ended with status %v", taskID, task.Status)
			default:
				return false, nil
			}
		},
	); err != nil {
		return nil, fmt.Errorf("failed to wait for task %s to complete: %w", taskID, err)
	}

	return completionDetails, nil
}
