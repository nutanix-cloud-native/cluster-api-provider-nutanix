/*
Copyright 2022 Nutanix

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

package client

import (
	"context"
	"fmt"
	"time"

	"github.com/nutanix-cloud-native/prism-go-client/utils"
	nutanixClientV3 "github.com/nutanix-cloud-native/prism-go-client/v3"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrl "sigs.k8s.io/controller-runtime"
)

const (
	pollingInterval = time.Second * 2
	stateSucceeded  = "SUCCEEDED"
)

// WaitForTaskCompletion will poll indefinitely every 2 seconds for the task with uuid to have status of "SUCCEEDED".
// Returns an error from GetTaskState or a timeout error if the context is cancelled.
func WaitForTaskCompletion(ctx context.Context, conn *nutanixClientV3.Client, uuid string) error {
	return wait.PollImmediateInfiniteWithContext(ctx, pollingInterval, func(ctx context.Context) (done bool, err error) {
		state, getErr := GetTaskState(ctx, conn, uuid)
		return state == stateSucceeded, getErr
	})
}

func GetTaskState(ctx context.Context, client *nutanixClientV3.Client, taskUUID string) (string, error) {
	log := ctrl.LoggerFrom(ctx)
	log.V(1).Info(fmt.Sprintf("Getting task with UUID %s", taskUUID))
	v, err := client.V3.GetTask(ctx, taskUUID)
	if err != nil {
		log.Error(err, fmt.Sprintf("error occurred while waiting for task with UUID %s", taskUUID))
		return "", err
	}

	if *v.Status == "INVALID_UUID" || *v.Status == "FAILED" {
		return *v.Status,
			fmt.Errorf("error_detail: %s, progress_message: %s", utils.StringValue(v.ErrorDetail), utils.StringValue(v.ProgressMessage))
	}
	taskStatus := *v.Status
	log.V(1).Info(fmt.Sprintf("Status for task with UUID %s: %s", taskUUID, taskStatus))
	return taskStatus, nil
}
