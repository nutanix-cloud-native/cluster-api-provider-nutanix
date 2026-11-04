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

package simulator

import (
	"time"

	"github.com/google/uuid"
	prismconfig "github.com/nutanix/ntnx-api-golang-clients/prism-go-client/v4/models/prism/v4/config"
	prismerror "github.com/nutanix/ntnx-api-golang-clients/prism-go-client/v4/models/prism/v4/error"
	"k8s.io/utils/ptr"
)

// Entity relationship strings used in task entitiesAffected, as Prism
// Central reports them.
const (
	relVM    = "vmm:ahv:config:vm"
	relImage = "vmm:content:image"
)

// Task operation names, as Prism Central reports them.
const (
	opVMCreate   = "kVmCreate"
	opVMPowerOn  = "kVmPowerOn"
	opVMPowerOff = "kVmPowerOff"
	opVMDelete   = "kVmDelete"
	opVMUpdate   = "kVmUpdate"
)

// taskRetention bounds how long completed tasks stay listable.
const taskRetention = 30 * time.Minute

type entityRef struct {
	extID string
	name  string
	rel   string
}

// newTask registers a RUNNING task. The store lock must be held.
func (s *Simulator) newTask(operation string, refs ...entityRef) *taskRecord {
	now := time.Now().UTC()
	task := prismconfig.NewTask()
	task.ExtId = ptr.To(taskExtID())
	task.Operation = ptr.To(operation)
	task.OperationDescription = ptr.To(operation)
	task.Status = ptr.To(prismconfig.TASKSTATUS_RUNNING)
	task.CreatedTime = ptr.To(now)
	task.StartedTime = ptr.To(now)
	task.LastUpdatedTime = ptr.To(now)
	task.ProgressPercentage = ptr.To(0)
	task.IsCancelable = ptr.To(false)
	task.IsBackgroundTask = ptr.To(false)
	task.NumberOfSubtasks = ptr.To(0)
	task.NumberOfEntitiesAffected = ptr.To(len(refs))
	for _, ref := range refs {
		er := prismconfig.NewEntityReference()
		er.ExtId = ptr.To(ref.extID)
		er.Rel = ptr.To(ref.rel)
		if ref.name != "" {
			er.Name = ptr.To(ref.name)
		}
		task.EntitiesAffected = append(task.EntitiesAffected, *er)
	}
	rec := &taskRecord{task: task}
	s.store.tasks[*task.ExtId] = rec
	s.pruneTasks(now)
	return rec
}

// taskExtID produces a Prism-style task identifier. Real task ids are
// "ZXJnb24=:<uuid>"; the SDK treats them as opaque strings.
func taskExtID() string { return "ZXJnb24=:" + uuid.NewString() }

func (s *Simulator) pruneTasks(now time.Time) {
	for id, rec := range s.store.tasks {
		if rec.task.CompletedTime != nil && now.Sub(*rec.task.CompletedTime) > taskRetention {
			delete(s.store.tasks, id)
		}
	}
}

// completeFn mutates the store when a task succeeds. It runs with the store
// lock held and may return a callback to run after the lock is released
// (used for lifecycle hooks).
type completeFn func() func()

// scheduleTask completes rec after d, either by running complete or, when
// fault injection selects this task, by failing it.
func (s *Simulator) scheduleTask(rec *taskRecord, d time.Duration, complete completeFn) {
	fail := s.cfg.Faults.TaskFailureEvery > 0 && s.taskSeq.Add(1)%uint64(s.cfg.Faults.TaskFailureEvery) == 0
	time.AfterFunc(d, func() {
		var after func()
		s.store.mu.Lock()
		now := time.Now().UTC()
		if fail {
			s.failTask(rec, now, "injected task failure")
		} else {
			after = complete()
			rec.task.Status = ptr.To(prismconfig.TASKSTATUS_SUCCEEDED)
			rec.task.ProgressPercentage = ptr.To(100)
		}
		rec.task.CompletedTime = ptr.To(now)
		rec.task.LastUpdatedTime = ptr.To(now)
		rec.doc.invalidate()
		s.store.mu.Unlock()
		s.stats.taskCompleted(*rec.task.Operation, fail)
		if after != nil {
			after()
		}
	})
}

func (s *Simulator) failTask(rec *taskRecord, now time.Time, message string) {
	rec.task.Status = ptr.To(prismconfig.TASKSTATUS_FAILED)
	msg := prismerror.NewAppMessage()
	msg.Message = ptr.To(message)
	msg.ErrorGroup = ptr.To("INTERNAL_ERROR")
	msg.Code = ptr.To("SIM-50000")
	rec.task.ErrorMessages = []prismerror.AppMessage{*msg}
	rec.task.LegacyErrorMessage = ptr.To(message)
	rec.task.CompletedTime = ptr.To(now)
}
