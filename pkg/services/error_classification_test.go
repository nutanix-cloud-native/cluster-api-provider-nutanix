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

package services

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClassifyErrorIgnoresFailedSubtaskSuffix(t *testing.T) {
	retriable := fmt.Errorf("task failed: VMM-10011 RETRYABLE_ERROR")
	withTerminalChild := fmt.Errorf("%w%s[Place VM] VMM-34418 NO_SCHEDULABLE_HOST", retriable, FailedSubtasksSuffix)
	assert.Equal(t, ClassRetriable, ClassifyError(retriable))
	assert.Equal(t, ClassRetriable, ClassifyError(withTerminalChild))

	terminal := fmt.Errorf("task failed: VMM-30102 invalid argument")
	withRetriableChild := fmt.Errorf("%w%s[Upgrade] VMM-10012 please retry", terminal, FailedSubtasksSuffix)
	assert.Equal(t, ClassTerminal, ClassifyError(terminal))
	assert.Equal(t, ClassTerminal, ClassifyError(withRetriableChild))

	accepted := fmt.Errorf("create failed: VMM-30101 VM already exists")
	withTerminalChildOnAccepted := fmt.Errorf("%w%sCTGRS-50008 category in use", accepted, FailedSubtasksSuffix)
	assert.Equal(t, ClassAccepted, ClassifyError(withTerminalChildOnAccepted))
}
