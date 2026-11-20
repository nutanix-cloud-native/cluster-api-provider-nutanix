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

package services

import "errors"

// terminalError represents a deterministic, non-retryable error caused by
// invalid user configuration (e.g. referenced resource does not exist).
// It is distinct from converged.APIError which represents HTTP-level failures.
type terminalError struct {
	message string
}

func (e *terminalError) Error() string { return e.message }

// NewTerminalError returns a deterministic, non-retryable error.
func NewTerminalError(message string) error {
	return &terminalError{message: message}
}

// IsTerminalError reports whether err is (or wraps) a terminalError.
func IsTerminalError(err error) bool {
	var te *terminalError
	return errors.As(err, &te)
}
