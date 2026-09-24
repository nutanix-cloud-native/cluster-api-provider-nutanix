/*
Copyright 2025 Nutanix

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

package version

import "fmt"

// GitCommit is the git commit hash of the build, set via ldflags
// Example: go build -ldflags "-X github.com/nutanix-cloud-native/cluster-api-provider-nutanix/pkg/version.GitCommit=<hash>"
var GitCommit = "dev"

// UserAgent returns the User-Agent string for CAPX API requests
// Format: capx/<version> or cluster-api-provider-nutanix/<version>
// Uses GitCommit if set, otherwise "dev"
func UserAgent() string {
	version := GitCommit
	if version == "" {
		version = "dev"
	}
	// Use short component name "capx" as used in repo (CAPX)
	// Full name would be "cluster-api-provider-nutanix" but short is more common in logs
	return fmt.Sprintf("capx/%s", version)
}

// UserAgentWithComponent returns User-Agent with custom component name
func UserAgentWithComponent(component string) string {
	version := GitCommit
	if version == "" {
		version = "dev"
	}
	return fmt.Sprintf("%s/%s", component, version)
}
