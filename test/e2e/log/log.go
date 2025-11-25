//go:build e2e

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

package log

import (
	"fmt"

	ginkgov2 "github.com/onsi/ginkgo/v2"
)

const (
	// Debug log level
	LogDebug = "DEBUG"

	// Info log level
	LogInfo = "INFO"

	// Warn log level
	LogWarn = "WARN"

	// Error log level
	LogError = "ERROR"
)

// Debugf logs a debug message
func Debugf(format string, a ...interface{}) {
	Logf(LogDebug, format, a...)
}

// Infof logs an info message
func Infof(format string, a ...interface{}) {
	Logf(LogInfo, format, a...)
}

// Warnf logs a warning message
func Warnf(format string, a ...interface{}) {
	Logf(LogWarn, format, a...)
}

// Errorf logs an error message
func Errorf(format string, a ...interface{}) {
	Logf(LogError, format, a...)
}

// Logf logs a message with the given level
func Logf(level string, format string, a ...interface{}) {
	msg := level + ": " + format + "\n"
	fmt.Fprintf(ginkgov2.GinkgoWriter, msg, a...)
}
