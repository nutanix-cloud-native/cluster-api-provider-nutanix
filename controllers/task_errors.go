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
	"errors"
	"fmt"
	"strings"
	"time"

	prismModels "github.com/nutanix/ntnx-api-golang-clients/prism-go-client/v4/models/prism/v4/config"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/nutanix-cloud-native/cluster-api-provider-nutanix/pkg/services"
	"github.com/nutanix-cloud-native/prism-go-client/converged"
	v4Converged "github.com/nutanix-cloud-native/prism-go-client/converged/v4"
)

const failedSubtasksFilter = "parentTask/extId eq '%s' and status eq Prism.Config.TaskStatus'FAILED'"

const (
	// maxSubtaskDepth bounds how far enrichment walks a failed Prism task tree.
	maxSubtaskDepth = 5
	// maxSubtaskMessages caps diagnostic text written onto the returned error
	// so Kubernetes conditions stay readable.
	maxSubtaskMessages = 5
	// maxSubtasksFetched caps List/Get calls made while enriching one failure,
	// including the parent-Get fallback.
	maxSubtasksFetched = 32
	// subtaskEnrichmentTimeout bounds Prism Tasks calls on the failure path so
	// a slow Tasks API cannot stall the reconcile worker.
	subtaskEnrichmentTimeout = 10 * time.Second
)

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
	if parentErr == nil || parentTaskUUID == "" || client == nil || client.Tasks == nil {
		return parentErr
	}
	// op.Wait often fails because the reconcile context is already canceled.
	// Listing tasks with that context only produces more errors.
	if ctx.Err() != nil {
		return parentErr
	}

	enrichCtx, cancel := context.WithTimeout(ctx, subtaskEnrichmentTimeout)
	defer cancel()

	subtaskErrs := collectFailedSubtaskErrors(enrichCtx, client, parentTaskUUID)
	if len(subtaskErrs) == 0 {
		return parentErr
	}

	// The reconciler logs the returned error. Keep enrichment detail at V(1)
	// so a failed task is not logged twice at Error level.
	ctrl.LoggerFrom(ctx).V(1).Info("Prism parent task failed; including failed subtask errors",
		"error", parentErr,
		"taskUUID", parentTaskUUID,
		"subtaskErrors", subtaskErrs,
	)
	return fmt.Errorf("%w%s%s", parentErr, services.FailedSubtasksSuffix, strings.Join(subtaskErrs, "; "))
}

// subtaskWalk bounds one enrichment pass: cycle protection, depth, fetch
// count, and unique messages shared across parent and child tasks.
type subtaskWalk struct {
	seenMsgs map[string]struct{}
	msgs     []string
	visited  map[string]struct{}
	fetched  int
}

func collectFailedSubtaskErrors(ctx context.Context, client *v4Converged.Client, parentTaskUUID string) []string {
	w := &subtaskWalk{
		seenMsgs: map[string]struct{}{},
		visited:  map[string]struct{}{},
	}
	w.walk(ctx, client, parentTaskUUID, 0)
	return w.msgs
}

func (w *subtaskWalk) walk(ctx context.Context, client *v4Converged.Client, parentTaskUUID string, depth int) {
	if ctx.Err() != nil || parentTaskUUID == "" || depth >= maxSubtaskDepth || w.full() {
		return
	}
	if _, seen := w.visited[parentTaskUUID]; seen {
		return
	}
	w.visited[parentTaskUUID] = struct{}{}
	if !w.allowFetch() {
		return
	}

	w.fetched++
	log := ctrl.LoggerFrom(ctx)
	children, err := listFailedChildTasks(ctx, client, parentTaskUUID)
	if err != nil {
		log.V(1).Info("failed to list Prism subtasks while collecting failure details",
			"error", err,
			"parentTaskUUID", parentTaskUUID,
		)
		// A transport, auth, or rate-limit failure will also fail the follow-up
		// Gets. Fall back only when List rejected the filter itself.
		if ctx.Err() != nil || !listFailureAllowsParentFallback(err) {
			return
		}
		children = w.failedSubtasksFromParentGet(ctx, client, parentTaskUUID)
	}

	for i := range children {
		if ctx.Err() != nil || w.full() {
			return
		}
		child := children[i]
		if child.Status == nil || *child.Status != prismModels.TASKSTATUS_FAILED {
			continue
		}
		childUUID := ptr.Deref(child.ExtId, "")
		detail, added := w.addTask(&child)
		if added {
			log.V(1).Info("failed Prism subtask",
				"parentTaskUUID", parentTaskUUID,
				"subtaskUUID", childUUID,
				"operation", taskOperation(&child),
				"error", detail,
			)
		}
		if childUUID != "" {
			w.walk(ctx, client, childUUID, depth+1)
		}
	}
}

func (w *subtaskWalk) allowFetch() bool {
	return w.fetched < maxSubtasksFetched && !w.full()
}

func (w *subtaskWalk) full() bool {
	return len(w.msgs) >= maxSubtaskMessages || len(w.seenMsgs) >= maxSubtaskMessages
}

// addTask records a failed task's messages that have not already been seen on
// an ancestor or sibling. Prism copies a child message onto its parent; those
// repeats are dropped so the status text stays short.
func (w *subtaskWalk) addTask(task *prismModels.Task) (string, bool) {
	if w.full() {
		return "", false
	}

	var fresh []string
	var hadMessage bool
	if task != nil {
		for _, msg := range task.ErrorMessages {
			if w.full() {
				break
			}
			if msg.Message == nil || *msg.Message == "" {
				continue
			}
			hadMessage = true
			if _, ok := w.seenMsgs[*msg.Message]; ok {
				continue
			}
			w.seenMsgs[*msg.Message] = struct{}{}
			fresh = append(fresh, *msg.Message)
		}
		if legacy := ptr.Deref(task.LegacyErrorMessage, ""); legacy != "" && !w.full() {
			hadMessage = true
			if _, ok := w.seenMsgs[legacy]; !ok {
				w.seenMsgs[legacy] = struct{}{}
				fresh = append(fresh, legacy)
			}
		}
	}
	if len(fresh) == 0 {
		if hadMessage {
			return "", false
		}
		const placeholder = "no error message provided"
		if _, ok := w.seenMsgs[placeholder]; ok {
			return "", false
		}
		w.seenMsgs[placeholder] = struct{}{}
		fresh = append(fresh, placeholder)
	}

	detail := fmt.Sprintf("[%s] %s", taskOperation(task), strings.Join(fresh, "; "))
	w.msgs = append(w.msgs, detail)
	return detail, true
}

func listFailedChildTasks(ctx context.Context, client *v4Converged.Client, parentTaskUUID string) ([]prismModels.Task, error) {
	if client == nil || client.Tasks == nil {
		return nil, fmt.Errorf("tasks client is nil")
	}
	// OData string literals escape a single quote by doubling it.
	filterID := strings.ReplaceAll(parentTaskUUID, "'", "''")
	return client.Tasks.List(ctx, converged.WithFilter(fmt.Sprintf(failedSubtasksFilter, filterID)))
}

// listFailureAllowsParentFallback reports whether List failed because the
// failed-subtask filter is unsupported or rejected. Transient failures are
// not worth a 1+N Get fallback that would fail the same way.
func listFailureAllowsParentFallback(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if converged.IsRateLimit(err) || converged.IsInternal(err) || converged.IsNotFound(err) {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "filter") ||
		strings.Contains(msg, "unsupported") ||
		strings.Contains(msg, "not supported") ||
		strings.Contains(msg, "odata")
}

func (w *subtaskWalk) failedSubtasksFromParentGet(ctx context.Context, client *v4Converged.Client, parentTaskUUID string) []prismModels.Task {
	if ctx.Err() != nil || client == nil || client.Tasks == nil || !w.allowFetch() {
		return nil
	}

	log := ctrl.LoggerFrom(ctx)
	w.fetched++
	parent, err := client.Tasks.Get(ctx, parentTaskUUID)
	if err != nil {
		log.V(1).Info("failed to get Prism parent task while collecting subtask failure details",
			"error", err,
			"parentTaskUUID", parentTaskUUID,
		)
		return nil
	}
	if parent == nil || len(parent.SubTasks) == 0 {
		return nil
	}

	children := make([]prismModels.Task, 0, len(parent.SubTasks))
	for _, ref := range parent.SubTasks {
		if ctx.Err() != nil || !w.allowFetch() {
			break
		}
		if ref.ExtId == nil || *ref.ExtId == "" {
			continue
		}
		w.fetched++
		child, err := client.Tasks.Get(ctx, *ref.ExtId)
		if err != nil {
			log.V(1).Info("failed to get Prism subtask while collecting failure details",
				"error", err,
				"subtaskUUID", *ref.ExtId,
			)
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

// isRetryableAPIError reports whether err should be retried (requeued) rather
// than stamped as a terminal failure. It delegates to services.ClassifyError,
// which layers the charter VMM-xxxxx / CTGRS-xxxxx code taxonomy on top of the
// SDK's HTTP-status/errorGroup classification. Accepted and unknown errors are
// treated as retriable; only Terminal errors short-circuit retries. A nil
// error is not retried. Diagnostic text after FailedSubtasksSuffix is ignored
// by ClassifyError, so child-task messages cannot change this result.
func isRetryableAPIError(err error) bool {
	if err == nil {
		return false
	}
	return services.ClassifyError(err) != services.ClassTerminal
}
