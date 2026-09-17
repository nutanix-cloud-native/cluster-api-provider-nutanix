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

package controllers

import (
	"context"
	"fmt"
	"strings"

	prismModels "github.com/nutanix/ntnx-api-golang-clients/prism-go-client/v4/models/prism/v4/config"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/nutanix-cloud-native/prism-go-client/converged"
	v4Converged "github.com/nutanix-cloud-native/prism-go-client/converged/v4"
)

const failedSubtasksFilter = "parentTask/extId eq '%s' and status eq Prism.Config.TaskStatus'FAILED'"

// waitForConvergedOperation waits for a Prism task and, on failure, appends
// error messages from failed child subtasks so callers see the actionable
// infrastructure reason (for example DHCP pool exhaustion) rather than only
// the generic parent-task error.
func waitForConvergedOperation[T any](ctx context.Context, client *v4Converged.Client, op converged.Operation[T]) ([]*T, error) {
	if op == nil {
		return nil, fmt.Errorf("operation is nil")
	}
	result, err := op.Wait(ctx)
	if err != nil {
		return nil, enrichTaskErrorWithFailedSubtasks(ctx, client, op.UUID(), err)
	}
	return result, nil
}

func enrichTaskErrorWithFailedSubtasks(ctx context.Context, client *v4Converged.Client, parentTaskUUID string, parentErr error) error {
	if parentErr == nil || parentTaskUUID == "" || client == nil {
		return parentErr
	}

	subtaskErrs := collectFailedSubtaskErrors(ctx, client, parentTaskUUID, map[string]struct{}{})
	if len(subtaskErrs) == 0 {
		return parentErr
	}

	log := ctrl.LoggerFrom(ctx)
	log.Error(parentErr, "Prism parent task failed; including failed subtask errors",
		"taskUUID", parentTaskUUID,
		"subtaskErrors", subtaskErrs,
	)
	return fmt.Errorf("%w; failed_subtasks: %s", parentErr, strings.Join(subtaskErrs, "; "))
}

func collectFailedSubtaskErrors(ctx context.Context, client *v4Converged.Client, parentTaskUUID string, visited map[string]struct{}) []string {
	if parentTaskUUID == "" {
		return nil
	}
	if visited == nil {
		visited = map[string]struct{}{}
	}
	if _, seen := visited[parentTaskUUID]; seen {
		return nil
	}
	visited[parentTaskUUID] = struct{}{}

	log := ctrl.LoggerFrom(ctx)
	children, err := listFailedChildTasks(ctx, client, parentTaskUUID)
	if err != nil {
		log.Error(err, "failed to list Prism subtasks while collecting failure details", "parentTaskUUID", parentTaskUUID)
		children = failedSubtasksFromParentGet(ctx, client, parentTaskUUID)
	}

	var msgs []string
	for i := range children {
		child := children[i]
		if child.Status == nil || *child.Status != prismModels.TASKSTATUS_FAILED {
			continue
		}
		detail := formatTaskError(&child)
		childUUID := ptr.Deref(child.ExtId, "")
		log.Info("failed Prism subtask",
			"parentTaskUUID", parentTaskUUID,
			"subtaskUUID", childUUID,
			"operation", taskOperation(&child),
			"error", detail,
		)
		msgs = append(msgs, detail)
		if childUUID != "" {
			msgs = append(msgs, collectFailedSubtaskErrors(ctx, client, childUUID, visited)...)
		}
	}
	return msgs
}

func listFailedChildTasks(ctx context.Context, client *v4Converged.Client, parentTaskUUID string) ([]prismModels.Task, error) {
	return client.Tasks.List(ctx, converged.WithFilter(fmt.Sprintf(failedSubtasksFilter, parentTaskUUID)))
}

func failedSubtasksFromParentGet(ctx context.Context, client *v4Converged.Client, parentTaskUUID string) []prismModels.Task {
	log := ctrl.LoggerFrom(ctx)
	parent, err := client.Tasks.Get(ctx, parentTaskUUID)
	if err != nil {
		log.Error(err, "failed to get Prism parent task while collecting subtask failure details", "parentTaskUUID", parentTaskUUID)
		return nil
	}
	if parent == nil || len(parent.SubTasks) == 0 {
		return nil
	}

	children := make([]prismModels.Task, 0, len(parent.SubTasks))
	for _, ref := range parent.SubTasks {
		if ref.ExtId == nil || *ref.ExtId == "" {
			continue
		}
		child, err := client.Tasks.Get(ctx, *ref.ExtId)
		if err != nil {
			log.Error(err, "failed to get Prism subtask while collecting failure details", "subtaskUUID", *ref.ExtId)
			continue
		}
		if child != nil {
			children = append(children, *child)
		}
	}
	return children
}

func taskOperation(task *prismModels.Task) string {
	if task == nil {
		return "unknown"
	}
	if desc := ptr.Deref(task.OperationDescription, ""); desc != "" {
		return desc
	}
	if op := ptr.Deref(task.Operation, ""); op != "" {
		return op
	}
	return "unknown"
}

func formatTaskError(task *prismModels.Task) string {
	op := taskOperation(task)

	var parts []string
	if task != nil {
		for _, msg := range task.ErrorMessages {
			if msg.Message != nil && *msg.Message != "" {
				parts = appendUnique(parts, *msg.Message)
			}
		}
		if legacy := ptr.Deref(task.LegacyErrorMessage, ""); legacy != "" {
			parts = appendUnique(parts, legacy)
		}
	}
	if len(parts) == 0 {
		parts = append(parts, "no error message provided")
	}
	return fmt.Sprintf("[%s] %s", op, strings.Join(parts, "; "))
}

func appendUnique(parts []string, msg string) []string {
	for _, existing := range parts {
		if existing == msg {
			return parts
		}
	}
	return append(parts, msg)
}
