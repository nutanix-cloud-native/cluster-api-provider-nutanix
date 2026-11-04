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
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFilterMatcher(t *testing.T) {
	task := map[string]any{
		"extId":     "t-1",
		"status":    "RUNNING",
		"operation": "kVmCreate",
		"entitiesAffected": []any{
			map[string]any{"extId": "vm-1", "rel": "vmm:ahv:config:vm"},
		},
		"progressPercentage": 42,
	}
	vm := map[string]any{"name": "it's", "powerState": "ON", "cluster": map[string]any{"extId": "pe-1"}}

	tests := []struct {
		name   string
		filter string
		entity any
		want   bool
	}{
		{"empty matches", "", vm, true},
		{"eq string", "name eq 'it''s'", vm, true},
		{"eq string mismatch", "name eq 'other'", vm, false},
		{"nested path", "cluster/extId eq 'pe-1'", vm, true},
		{"and", "name eq 'it''s' and powerState eq 'ON'", vm, true},
		{"or", "name eq 'nope' or powerState eq 'ON'", vm, true},
		{"not", "not (powerState eq 'ON')", vm, false},
		{"missing property", "missing eq 'x'", vm, false},
		{"missing property ne", "missing ne 'x'", vm, true},
		{"enum literal", "status eq Prism.Config.TaskStatus'RUNNING'", task, true},
		{"enum literal mismatch", "status eq Prism.Config.TaskStatus'QUEUED'", task, false},
		{"number gt", "progressPercentage gt 40", task, true},
		{"number le", "progressPercentage le 40", task, false},
		{"any lambda", "entitiesAffected/any(a:a/extId eq 'vm-1')", task, true},
		{"any lambda miss", "entitiesAffected/any(a:a/extId eq 'vm-2')", task, false},
		{"all lambda", "entitiesAffected/all(a:a/rel eq 'vmm:ahv:config:vm')", task, true},
		{
			"capx running task filter",
			"entitiesAffected/any(a:a/extId eq 'vm-1') and (status eq Prism.Config.TaskStatus'RUNNING' or status eq Prism.Config.TaskStatus'QUEUED')",
			task, true,
		},
		{
			"capx image delete filter",
			"entitiesAffected/any(a:a/extId eq 'vm-1') and (status eq Prism.Config.TaskStatus'RUNNING' or status eq Prism.Config.TaskStatus'QUEUED') and (operation eq 'kImageDelete')",
			task, false,
		},
		{"capx category filter", "key eq 'k' and value eq 'v'", map[string]any{"key": "k", "value": "v"}, true},
		{"capx storage container filter", "name eq 'sc' and clusterExtId eq 'pe-1'", map[string]any{"name": "sc", "clusterExtId": "pe-1"}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, err := newFilterMatcher(tc.filter)
			require.NoError(t, err)
			got, err := m.matches(tc.entity)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestFilterParseErrors(t *testing.T) {
	for _, filter := range []string{
		"name eq",
		"name eq 'unterminated",
		"(name eq 'x'",
		"name eq 'x' extra",
		"entitiesAffected/any(a a/extId eq 'x')",
		"name ! 'x'",
	} {
		_, err := newFilterMatcher(filter)
		require.Error(t, err, filter)
	}
}
