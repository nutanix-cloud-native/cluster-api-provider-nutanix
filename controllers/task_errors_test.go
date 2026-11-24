/*
Copyright 2023 Nutanix

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
	"testing"

	prismModels "github.com/nutanix/ntnx-api-golang-clients/prism-go-client/v4/models/prism/v4/config"
	prismErrors "github.com/nutanix/ntnx-api-golang-clients/prism-go-client/v4/models/prism/v4/error"
	vmmModels "github.com/nutanix/ntnx-api-golang-clients/vmm-go-client/v4/models/vmm/v4/ahv/config"

	mockconverged "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/mocks/converged"
	"github.com/nutanix-cloud-native/cluster-api-provider-nutanix/pkg/services"
	converged "github.com/nutanix-cloud-native/prism-go-client/converged"
	v4Converged "github.com/nutanix-cloud-native/prism-go-client/converged/v4"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"k8s.io/utils/ptr"
)

func Test_isRetryableAPIError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "not found is not retryable",
			err:      &converged.APIError{Kind: converged.ErrNotFound, Message: "not found"},
			expected: false,
		},
		{
			name:     "rate limit is retryable",
			err:      &converged.APIError{Kind: converged.ErrRateLimit, Message: "rate limited"},
			expected: true,
		},
		{
			name:     "internal is retryable",
			err:      &converged.APIError{Kind: converged.ErrInternal, Message: "internal error"},
			expected: true,
		},
		{
			name:     "unknown errors default to retryable",
			err:      errors.New("timeout awaiting headers"),
			expected: true,
		},
		{
			name:     "terminal error is not retryable",
			err:      services.NewTerminalError("resource not found"),
			expected: false,
		},
		{
			name:     "unclassified APIError (Kind nil) is not retryable",
			err:      &converged.APIError{Kind: nil, Cause: errors.New("400 Bad Request")},
			expected: false,
		},
		{
			name:     "wrapped unclassified APIError is not retryable",
			err:      fmt.Errorf("failed to create VM: %w", &converged.APIError{Kind: nil, Cause: errors.New("validation error")}),
			expected: false,
		},
		{
			name:     "nil is not retryable",
			err:      nil,
			expected: false,
		},
		{
			name:     "subtask text does not flip a retriable parent to terminal",
			err:      fmt.Errorf("task failed: VMM-10011%sno host VMM-34418", services.FailedSubtasksSuffix),
			expected: true,
		},
		{
			name:     "subtask text does not flip a terminal parent to retriable",
			err:      fmt.Errorf("task failed: VMM-30102%splease retry VMM-10012", services.FailedSubtasksSuffix),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, isRetryableAPIError(tt.err))
		})
	}
}

func TestEnrichTaskErrorWithFailedSubtasks(t *testing.T) {
	parentUUID := "parent-create-vm"
	childUUID := "child-vnic"
	parentErr := errors.New("task parent-create-vm failed: Failed to perform the operation on the VM with UUID 'EMPTY' as '' is not defined")
	dhcpMsg := "Cannot allocate address! No DHCP pool is defined or the DHCP pool is exhausted"

	t.Run("appends failed child subtask errors from list", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockClient := NewMockConvergedClient(ctrl)
		failedStatus := prismModels.TASKSTATUS_FAILED
		mockClient.MockTasks.EXPECT().List(gomock.Any(), gomock.Any()).Return([]prismModels.Task{
			{
				ExtId:                ptr.To(childUUID),
				Status:               &failedStatus,
				OperationDescription: ptr.To("Create VM vNIC port"),
				ErrorMessages: []prismErrors.AppMessage{
					{Message: ptr.To(dhcpMsg)},
				},
			},
		}, nil)
		// Nested collect for the failed child lists its own children.
		mockClient.MockTasks.EXPECT().List(gomock.Any(), gomock.Any()).Return([]prismModels.Task{}, nil)

		err := enrichTaskErrorWithFailedSubtasks(context.Background(), mockClient.Client, parentUUID, parentErr)
		require.Error(t, err)
		assert.ErrorContains(t, err, parentErr.Error())
		assert.ErrorContains(t, err, "failed_subtasks:")
		assert.ErrorContains(t, err, dhcpMsg)
		assert.ErrorContains(t, err, "Create VM vNIC port")
	})

	t.Run("does not fall back to Get when list returns empty", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockClient := NewMockConvergedClient(ctrl)
		mockClient.MockTasks.EXPECT().List(gomock.Any(), gomock.Any()).Return([]prismModels.Task{}, nil)

		err := enrichTaskErrorWithFailedSubtasks(context.Background(), mockClient.Client, parentUUID, parentErr)
		require.Error(t, err)
		assert.Equal(t, parentErr, err)
	})

	t.Run("falls back to parent subtask references when list rejects the filter", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockClient := NewMockConvergedClient(ctrl)
		failedStatus := prismModels.TASKSTATUS_FAILED
		mockClient.MockTasks.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, errors.New("unsupported odata filter"))
		mockClient.MockTasks.EXPECT().Get(gomock.Any(), parentUUID).Return(&prismModels.Task{
			ExtId: ptr.To(parentUUID),
			SubTasks: []prismModels.TaskReferenceInternal{
				{ExtId: ptr.To(childUUID)},
			},
		}, nil)
		mockClient.MockTasks.EXPECT().Get(gomock.Any(), childUUID).Return(&prismModels.Task{
			ExtId:                ptr.To(childUUID),
			Status:               &failedStatus,
			OperationDescription: ptr.To("Create VM vNIC port"),
			LegacyErrorMessage:   ptr.To(dhcpMsg),
		}, nil)
		mockClient.MockTasks.EXPECT().List(gomock.Any(), gomock.Any()).Return([]prismModels.Task{}, nil)

		err := enrichTaskErrorWithFailedSubtasks(context.Background(), mockClient.Client, parentUUID, parentErr)
		require.Error(t, err)
		assert.ErrorContains(t, err, dhcpMsg)
	})

	t.Run("deduplicates identical ErrorMessages and LegacyErrorMessage", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockClient := NewMockConvergedClient(ctrl)
		failedStatus := prismModels.TASKSTATUS_FAILED
		mockClient.MockTasks.EXPECT().List(gomock.Any(), gomock.Any()).Return([]prismModels.Task{
			{
				ExtId:                ptr.To(childUUID),
				Status:               &failedStatus,
				OperationDescription: ptr.To("Create VM vNIC port"),
				ErrorMessages: []prismErrors.AppMessage{
					{Message: ptr.To(dhcpMsg)},
				},
				LegacyErrorMessage: ptr.To(dhcpMsg),
			},
		}, nil)
		mockClient.MockTasks.EXPECT().List(gomock.Any(), gomock.Any()).Return([]prismModels.Task{}, nil)

		err := enrichTaskErrorWithFailedSubtasks(context.Background(), mockClient.Client, parentUUID, parentErr)
		require.Error(t, err)
		assert.Equal(t, 1, strings.Count(err.Error(), dhcpMsg))
	})

	t.Run("returns original error when parent error is nil", func(t *testing.T) {
		err := enrichTaskErrorWithFailedSubtasks(context.Background(), nil, parentUUID, nil)
		assert.NoError(t, err)
	})

	t.Run("returns original error when the tasks client is nil", func(t *testing.T) {
		err := enrichTaskErrorWithFailedSubtasks(context.Background(), &v4Converged.Client{}, parentUUID, parentErr)
		assert.Equal(t, parentErr, err)
	})

	t.Run("does not call Prism when the context is already canceled", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		mockClient := NewMockConvergedClient(ctrl)
		err := enrichTaskErrorWithFailedSubtasks(ctx, mockClient.Client, parentUUID, parentErr)
		assert.Equal(t, parentErr, err)
	})

	t.Run("does not fall back to Get when list fails transiently", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockClient := NewMockConvergedClient(ctrl)
		mockClient.MockTasks.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, &converged.APIError{
			Kind:    converged.ErrInternal,
			Message: "service unavailable",
		})

		err := enrichTaskErrorWithFailedSubtasks(context.Background(), mockClient.Client, parentUUID, parentErr)
		assert.Equal(t, parentErr, err)
	})

	t.Run("deduplicates a message repeated on a child task", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockClient := NewMockConvergedClient(ctrl)
		failedStatus := prismModels.TASKSTATUS_FAILED
		grandchildUUID := "grandchild-vnic"
		gomock.InOrder(
			mockClient.MockTasks.EXPECT().List(gomock.Any(), gomock.Any()).Return([]prismModels.Task{
				{
					ExtId:                ptr.To(childUUID),
					Status:               &failedStatus,
					OperationDescription: ptr.To("Create VM NIC"),
					ErrorMessages:        []prismErrors.AppMessage{{Message: ptr.To(dhcpMsg)}},
				},
			}, nil),
			mockClient.MockTasks.EXPECT().List(gomock.Any(), gomock.Any()).Return([]prismModels.Task{
				{
					ExtId:                ptr.To(grandchildUUID),
					Status:               &failedStatus,
					OperationDescription: ptr.To("Assign IP"),
					ErrorMessages:        []prismErrors.AppMessage{{Message: ptr.To(dhcpMsg)}},
				},
			}, nil),
			mockClient.MockTasks.EXPECT().List(gomock.Any(), gomock.Any()).Return([]prismModels.Task{}, nil),
		)

		err := enrichTaskErrorWithFailedSubtasks(context.Background(), mockClient.Client, parentUUID, parentErr)
		require.Error(t, err)
		assert.Equal(t, 1, strings.Count(err.Error(), dhcpMsg))
		assert.NotContains(t, err.Error(), "Assign IP")
	})

	t.Run("caps unique subtask messages", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockClient := NewMockConvergedClient(ctrl)
		failedStatus := prismModels.TASKSTATUS_FAILED
		children := make([]prismModels.Task, 8)
		for i := range children {
			children[i] = prismModels.Task{
				ExtId:                ptr.To(fmt.Sprintf("child-%d", i)),
				Status:               &failedStatus,
				OperationDescription: ptr.To("Place VM"),
				ErrorMessages:        []prismErrors.AppMessage{{Message: ptr.To(fmt.Sprintf("reason-%d", i))}},
			}
		}
		mockClient.MockTasks.EXPECT().List(gomock.Any(), gomock.Any()).Return(children, nil)
		mockClient.MockTasks.EXPECT().List(gomock.Any(), gomock.Any()).Return([]prismModels.Task{}, nil).AnyTimes()

		err := enrichTaskErrorWithFailedSubtasks(context.Background(), mockClient.Client, parentUUID, parentErr)
		require.Error(t, err)
		for i := 0; i < maxSubtaskMessages; i++ {
			assert.ErrorContains(t, err, fmt.Sprintf("reason-%d", i))
		}
		assert.NotContains(t, err.Error(), "reason-5")
	})

	t.Run("stops walking at max depth", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockClient := NewMockConvergedClient(ctrl)
		failedStatus := prismModels.TASKSTATUS_FAILED
		var lists int
		mockClient.MockTasks.EXPECT().List(gomock.Any(), gomock.Any()).DoAndReturn(
			func(context.Context, ...converged.ODataOption) ([]prismModels.Task, error) {
				lists++
				return []prismModels.Task{
					{
						ExtId:                ptr.To(fmt.Sprintf("level-%d", lists)),
						Status:               &failedStatus,
						OperationDescription: ptr.To("Nested task"),
						ErrorMessages:        []prismErrors.AppMessage{{Message: ptr.To(dhcpMsg)}},
					},
				}, nil
			},
		).AnyTimes()

		err := enrichTaskErrorWithFailedSubtasks(context.Background(), mockClient.Client, parentUUID, parentErr)
		require.Error(t, err)
		assert.Equal(t, maxSubtaskDepth, lists)
		assert.Equal(t, 1, strings.Count(err.Error(), dhcpMsg))
	})

	t.Run("escapes single quotes in the task filter", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockClient := NewMockConvergedClient(ctrl)
		var gotFilter string
		mockClient.MockTasks.EXPECT().List(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, opts ...converged.ODataOption) ([]prismModels.Task, error) {
				sink := &odataFilterSink{}
				for _, opt := range opts {
					require.NoError(t, opt(sink))
				}
				gotFilter = sink.filter
				return []prismModels.Task{}, nil
			},
		)

		quotedID := "task-'id"
		err := enrichTaskErrorWithFailedSubtasks(context.Background(), mockClient.Client, quotedID, parentErr)
		assert.Equal(t, parentErr, err)
		assert.Equal(t, fmt.Sprintf(failedSubtasksFilter, "task-''id"), gotFilter)
	})

	t.Run("keeps the parent class when subtask text names another code", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockClient := NewMockConvergedClient(ctrl)
		failedStatus := prismModels.TASKSTATUS_FAILED
		retriableParent := errors.New("task parent-create-vm failed: VMM-10011 RETRYABLE_ERROR")
		mockClient.MockTasks.EXPECT().List(gomock.Any(), gomock.Any()).Return([]prismModels.Task{
			{
				ExtId:                ptr.To(childUUID),
				Status:               &failedStatus,
				OperationDescription: ptr.To("Place VM"),
				ErrorMessages:        []prismErrors.AppMessage{{Message: ptr.To("VMM-34418 NO_SCHEDULABLE_HOST")}},
			},
		}, nil)
		mockClient.MockTasks.EXPECT().List(gomock.Any(), gomock.Any()).Return([]prismModels.Task{}, nil)

		err := enrichTaskErrorWithFailedSubtasks(context.Background(), mockClient.Client, parentUUID, retriableParent)
		require.Error(t, err)
		assert.ErrorContains(t, err, "VMM-34418")
		assert.True(t, isRetryableAPIError(err))
		assert.ErrorIs(t, err, retriableParent)
	})
}

// odataFilterSink records the OData filter passed to Tasks.List.
type odataFilterSink struct {
	filter string
}

func (s *odataFilterSink) SetPageOption(int) error  { return nil }
func (s *odataFilterSink) SetLimitOption(int) error { return nil }
func (s *odataFilterSink) SetFilterOption(filter string) error {
	s.filter = filter
	return nil
}
func (s *odataFilterSink) SetOrderByOption(string) error { return nil }
func (s *odataFilterSink) SetExpandOption(string) error  { return nil }
func (s *odataFilterSink) SetSelectOption(string) error  { return nil }
func (s *odataFilterSink) SetApplyOption(string) error   { return nil }

func TestWaitForConvergedOperation_EnrichesFailedWait(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ctx := context.Background()
	parentUUID := "parent-task"
	childUUID := "child-task"
	waitErr := errors.New("task parent-task failed: generic parent error")
	childMsg := "Cannot allocate address! No DHCP pool is defined or the DHCP pool is exhausted"

	mockClient := NewMockConvergedClient(ctrl)
	mockOp := mockconverged.NewMockOperation[vmmModels.Vm](ctrl)
	mockOp.EXPECT().Wait(ctx).Return(nil, waitErr)
	mockOp.EXPECT().UUID().Return(parentUUID)

	failedStatus := prismModels.TASKSTATUS_FAILED
	mockClient.MockTasks.EXPECT().List(gomock.Any(), gomock.Any()).Return([]prismModels.Task{
		{
			ExtId:                ptr.To(childUUID),
			Status:               &failedStatus,
			OperationDescription: ptr.To("Create VM vNIC port"),
			ErrorMessages: []prismErrors.AppMessage{
				{Message: ptr.To(childMsg)},
			},
		},
	}, nil)
	mockClient.MockTasks.EXPECT().List(gomock.Any(), gomock.Any()).Return([]prismModels.Task{}, nil)

	_, err := waitForConvergedOperation(ctx, mockClient.Client, mockOp)
	require.Error(t, err)
	assert.ErrorContains(t, err, "generic parent error")
	assert.ErrorContains(t, err, childMsg)
}
