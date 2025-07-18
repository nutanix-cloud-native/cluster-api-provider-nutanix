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

package v1beta1

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/ptr"
)

func TestNutanixMachineSpec_JSONMarshalling(t *testing.T) {
	tests := []struct {
		name             string
		spec             NutanixMachineSpec
		expectedJSON     string
		shouldContain    []string
		shouldNotContain []string
	}{
		{
			name: "marshalling with zero-value cluster should omit cluster field",
			spec: NutanixMachineSpec{
				VCPUsPerSocket: 2,
				VCPUSockets:    1,
				MemorySize:     resource.MustParse("4Gi"),
				SystemDiskSize: resource.MustParse("20Gi"),
				Subnets: []NutanixResourceIdentifier{
					{
						Type: NutanixIdentifierUUID,
						UUID: ptr.To("subnet-uuid"),
					},
				},
				// Cluster field is intentionally left as zero value
			},
			shouldNotContain: []string{`"cluster"`},
			shouldContain:    []string{`"vcpusPerSocket"`, `"vcpuSockets"`, `"memorySize"`, `"systemDiskSize"`, `"subnet"`},
		},
		{
			name: "marshalling with populated cluster should include cluster field",
			spec: NutanixMachineSpec{
				VCPUsPerSocket: 2,
				VCPUSockets:    1,
				MemorySize:     resource.MustParse("4Gi"),
				SystemDiskSize: resource.MustParse("20Gi"),
				Cluster: NutanixResourceIdentifier{
					Type: NutanixIdentifierUUID,
					UUID: ptr.To("cluster-uuid"),
				},
				Subnets: []NutanixResourceIdentifier{
					{
						Type: NutanixIdentifierUUID,
						UUID: ptr.To("subnet-uuid"),
					},
				},
			},
			shouldContain: []string{`"cluster"`, `"vcpusPerSocket"`, `"vcpuSockets"`, `"memorySize"`, `"systemDiskSize"`, `"subnet"`},
		},
		{
			name: "marshalling with cluster by name should include cluster field",
			spec: NutanixMachineSpec{
				VCPUsPerSocket: 2,
				VCPUSockets:    1,
				MemorySize:     resource.MustParse("4Gi"),
				SystemDiskSize: resource.MustParse("20Gi"),
				Cluster: NutanixResourceIdentifier{
					Type: NutanixIdentifierName,
					Name: ptr.To("cluster-name"),
				},
				Subnets: []NutanixResourceIdentifier{
					{
						Type: NutanixIdentifierName,
						Name: ptr.To("subnet-name"),
					},
				},
			},
			shouldContain: []string{`"cluster"`, `"cluster-name"`, `"vcpusPerSocket"`, `"vcpuSockets"`, `"memorySize"`, `"systemDiskSize"`, `"subnet"`},
		},
		{
			name: "marshalling with explicitly empty cluster struct should omit cluster field",
			spec: NutanixMachineSpec{
				VCPUsPerSocket: 2,
				VCPUSockets:    1,
				MemorySize:     resource.MustParse("4Gi"),
				SystemDiskSize: resource.MustParse("20Gi"),
				Cluster: NutanixResourceIdentifier{
					// Explicitly created empty struct with zero values
					Type: "",
					UUID: nil,
					Name: nil,
				},
				Subnets: []NutanixResourceIdentifier{
					{
						Type: NutanixIdentifierUUID,
						UUID: ptr.To("subnet-uuid"),
					},
				},
			},
			shouldNotContain: []string{`"cluster"`},
			shouldContain:    []string{`"vcpusPerSocket"`, `"vcpuSockets"`, `"memorySize"`, `"systemDiskSize"`, `"subnet"`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jsonBytes, err := json.Marshal(tt.spec)
			require.NoError(t, err, "Failed to marshal NutanixMachineSpec")

			jsonString := string(jsonBytes)

			// Check for strings that should be present
			for _, shouldContain := range tt.shouldContain {
				assert.Contains(t, jsonString, shouldContain, "Expected JSON to contain %q", shouldContain)
			}

			// Check for strings that should not be present
			for _, shouldNotContain := range tt.shouldNotContain {
				assert.NotContains(t, jsonString, shouldNotContain, "Expected JSON to NOT contain %q", shouldNotContain)
			}

			// Test that we can unmarshal back to the same structure
			var unmarshaledSpec NutanixMachineSpec
			require.NoError(t, json.Unmarshal(jsonBytes, &unmarshaledSpec), "Failed to unmarshal JSON back to NutanixMachineSpec")

			// Verify critical fields are preserved
			assert.Equal(t, tt.spec.VCPUsPerSocket, unmarshaledSpec.VCPUsPerSocket, "VCPUsPerSocket mismatch")
			assert.Equal(t, tt.spec.VCPUSockets, unmarshaledSpec.VCPUSockets, "VCPUSockets mismatch")
			assert.True(t, unmarshaledSpec.MemorySize.Equal(tt.spec.MemorySize), "MemorySize mismatch: expected %v, got %v", tt.spec.MemorySize, unmarshaledSpec.MemorySize)
			assert.True(t, unmarshaledSpec.SystemDiskSize.Equal(tt.spec.SystemDiskSize), "SystemDiskSize mismatch: expected %v, got %v", tt.spec.SystemDiskSize, unmarshaledSpec.SystemDiskSize)

			// Verify cluster field behavior
			if tt.spec.Cluster.Type != "" {
				// If original had a cluster, unmarshaled should too
				assert.Equal(t, tt.spec.Cluster.Type, unmarshaledSpec.Cluster.Type, "Cluster.Type mismatch")
				if tt.spec.Cluster.UUID != nil {
					require.NotNil(t, unmarshaledSpec.Cluster.UUID, "Expected Cluster.UUID to not be nil")
					assert.Equal(t, *tt.spec.Cluster.UUID, *unmarshaledSpec.Cluster.UUID, "Cluster.UUID mismatch")
				}
				if tt.spec.Cluster.Name != nil {
					require.NotNil(t, unmarshaledSpec.Cluster.Name, "Expected Cluster.Name to not be nil")
					assert.Equal(t, *tt.spec.Cluster.Name, *unmarshaledSpec.Cluster.Name, "Cluster.Name mismatch")
				}
			} else {
				// If original had zero cluster, unmarshaled should be zero too
				assert.Empty(t, unmarshaledSpec.Cluster.Type, "Expected unmarshaled cluster type to be empty")
				assert.Nil(t, unmarshaledSpec.Cluster.UUID, "Expected unmarshaled cluster UUID to be nil")
				assert.Nil(t, unmarshaledSpec.Cluster.Name, "Expected unmarshaled cluster Name to be nil")
			}
		})
	}
}

func TestNutanixMachineSpec_JSONUnmarshalling(t *testing.T) {
	tests := []struct {
		name         string
		jsonInput    string
		expectError  bool
		expectedSpec NutanixMachineSpec
	}{
		{
			name: "unmarshalling JSON without cluster field should result in zero-value cluster",
			jsonInput: `{
				"vcpusPerSocket": 2,
				"vcpuSockets": 1,
				"memorySize": "4Gi",
				"systemDiskSize": "20Gi",
				"subnet": [{"type": "uuid", "uuid": "subnet-uuid"}]
			}`,
			expectError: false,
			expectedSpec: NutanixMachineSpec{
				VCPUsPerSocket: 2,
				VCPUSockets:    1,
				MemorySize:     resource.MustParse("4Gi"),
				SystemDiskSize: resource.MustParse("20Gi"),
				Subnets: []NutanixResourceIdentifier{
					{
						Type: NutanixIdentifierUUID,
						UUID: ptr.To("subnet-uuid"),
					},
				},
				// Cluster should be zero value
			},
		},
		{
			name: "unmarshalling JSON with cluster field should populate cluster",
			jsonInput: `{
				"vcpusPerSocket": 2,
				"vcpuSockets": 1,
				"memorySize": "4Gi",
				"systemDiskSize": "20Gi",
				"cluster": {"type": "uuid", "uuid": "cluster-uuid"},
				"subnet": [{"type": "uuid", "uuid": "subnet-uuid"}]
			}`,
			expectError: false,
			expectedSpec: NutanixMachineSpec{
				VCPUsPerSocket: 2,
				VCPUSockets:    1,
				MemorySize:     resource.MustParse("4Gi"),
				SystemDiskSize: resource.MustParse("20Gi"),
				Cluster: NutanixResourceIdentifier{
					Type: NutanixIdentifierUUID,
					UUID: ptr.To("cluster-uuid"),
				},
				Subnets: []NutanixResourceIdentifier{
					{
						Type: NutanixIdentifierUUID,
						UUID: ptr.To("subnet-uuid"),
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var spec NutanixMachineSpec
			err := json.Unmarshal([]byte(tt.jsonInput), &spec)

			if tt.expectError {
				assert.Error(t, err, "Expected an error but got none")
				return
			}

			require.NoError(t, err, "Expected no error but got: %v", err)

			// Verify the unmarshaled spec matches expectations
			assert.Equal(t, tt.expectedSpec.VCPUsPerSocket, spec.VCPUsPerSocket, "VCPUsPerSocket mismatch")
			assert.Equal(t, tt.expectedSpec.VCPUSockets, spec.VCPUSockets, "VCPUSockets mismatch")
			assert.True(t, spec.MemorySize.Equal(tt.expectedSpec.MemorySize), "MemorySize mismatch: expected %v, got %v", tt.expectedSpec.MemorySize, spec.MemorySize)
			assert.True(t, spec.SystemDiskSize.Equal(tt.expectedSpec.SystemDiskSize), "SystemDiskSize mismatch: expected %v, got %v", tt.expectedSpec.SystemDiskSize, spec.SystemDiskSize)

			// Verify cluster field
			assert.Equal(t, tt.expectedSpec.Cluster.Type, spec.Cluster.Type, "Cluster.Type mismatch")
			if tt.expectedSpec.Cluster.UUID != nil {
				require.NotNil(t, spec.Cluster.UUID, "Expected Cluster.UUID to not be nil")
				assert.Equal(t, *tt.expectedSpec.Cluster.UUID, *spec.Cluster.UUID, "Cluster.UUID mismatch")
			} else {
				assert.Nil(t, spec.Cluster.UUID, "Expected Cluster.UUID to be nil")
			}
		})
	}
}
