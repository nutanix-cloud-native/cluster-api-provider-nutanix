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

package services

import (
	"context"
	"errors"
	"fmt"
	"testing"

	clusterAhvModels "github.com/nutanix/ntnx-api-golang-clients/clustermgmt-go-client/v4/models/clustermgmt/v4/ahv/config"
	clusterModels "github.com/nutanix/ntnx-api-golang-clients/clustermgmt-go-client/v4/models/clustermgmt/v4/config"
	vmmModels "github.com/nutanix/ntnx-api-golang-clients/vmm-go-client/v4/models/vmm/v4/ahv/config"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	converged "github.com/nutanix-cloud-native/prism-go-client/converged"
	v4Converged "github.com/nutanix-cloud-native/prism-go-client/converged/v4"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"k8s.io/utils/ptr"
)

// TestGetGPUList tests the GetGPUList function
func TestGetGPUList(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ctx := context.Background()
	peUUID := "test-pe-uuid"

	t.Run("should return list of GPUs successfully", func(t *testing.T) {
		// Create mock client
		mockClientWrapper := NewMockConvergedClient(ctrl)

		// Create test GPU
		gpu := infrav1.NutanixGPU{
			Type: infrav1.NutanixGPUIdentifierName,
			Name: ptr.To("test-gpu-1"),
		}

		// Mock the GetGPUsForPE calls
		physicalGPU := clusterModels.PhysicalGpuProfile{
			PhysicalGpuConfig: &clusterModels.PhysicalGpuConfig{
				DeviceName: ptr.To("test-gpu-1"),
				DeviceId:   ptr.To(int64(123)),
				IsInUse:    ptr.To(false),
				Type:       clusterModels.GPUTYPE_PASSTHROUGH_COMPUTE.Ref(),
				VendorName: ptr.To("kNvidia"),
			},
		}

		mockClientWrapper.MockClusters.EXPECT().
			ListClusterPhysicalGPUs(ctx, peUUID, gomock.Any()).
			Return([]clusterModels.PhysicalGpuProfile{physicalGPU}, nil).
			AnyTimes()

		mockClientWrapper.MockClusters.EXPECT().
			ListClusterVirtualGPUs(ctx, peUUID, gomock.Any()).
			Return([]clusterModels.VirtualGpuProfile{}, nil).
			AnyTimes()

		gpus := []infrav1.NutanixGPU{gpu}

		result, err := GetGPUList(ctx, mockClientWrapper.Client, gpus, peUUID, "")
		assert.NoError(t, err)
		assert.Len(t, result, 1)
		assert.Equal(t, "test-gpu-1", *result[0].Name)
	})

	t.Run("should resolve a GPU by profile name via AHV profile APIs", func(t *testing.T) {
		mockClientWrapper := NewMockConvergedClient(ctrl)

		gpu := infrav1.NutanixGPU{
			Type: infrav1.NutanixGPUIdentifierProfile,
			Profile: &infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierName,
				Name: ptr.To("A100-4Q"),
			},
		}

		mockClientWrapper.MockClusters.EXPECT().
			ListAHVPhysicalGPUProfiles(ctx, peUUID, gomock.Any()).
			Return([]clusterAhvModels.PhysicalGpuProfile{}, nil)
		mockClientWrapper.MockClusters.EXPECT().
			ListAHVVirtualGPUProfiles(ctx, peUUID, gomock.Any()).
			Return([]clusterAhvModels.VirtualGpuProfile{
				{
					ExtId:         ptr.To("vgpu-profile-uuid"),
					Configuration: &clusterAhvModels.VirtualGpuProfileConfiguration{Name: ptr.To("A100-4Q")},
				},
			}, nil)

		result, err := GetGPUList(ctx, mockClientWrapper.Client, []infrav1.NutanixGPU{gpu}, peUUID, "7.6")
		assert.NoError(t, err)
		assert.Len(t, result, 1)
		require.NotNil(t, result[0].BackingInfo)
		backing, ok := result[0].BackingInfo.GetValue().(vmmModels.VirtualGpu)
		require.True(t, ok)
		require.NotNil(t, backing.VirtualGpuProfileReference)
		assert.Equal(t, "vgpu-profile-uuid", *backing.VirtualGpuProfileReference.ExtId)
	})
}

// TestGetGPU tests the GetGPU function
func TestGetGPU(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ctx := context.Background()
	peUUID := "test-pe-uuid"

	t.Run("should return error when no available GPUs found", func(t *testing.T) {
		// Create mock client
		mockClientWrapper := NewMockConvergedClient(ctrl)

		gpu := infrav1.NutanixGPU{
			Type: infrav1.NutanixGPUIdentifierName,
			Name: ptr.To("non-existent-gpu"),
		}

		// Mock GetGPUsForPE to return empty list
		mockClientWrapper.MockClusters.EXPECT().
			ListClusterPhysicalGPUs(ctx, peUUID, gomock.Any()).
			Return([]clusterModels.PhysicalGpuProfile{}, nil).
			AnyTimes()
		mockClientWrapper.MockClusters.EXPECT().
			ListClusterVirtualGPUs(ctx, peUUID, gomock.Any()).
			Return([]clusterModels.VirtualGpuProfile{}, nil).
			AnyTimes()

		_, err := GetGPU(ctx, mockClientWrapper.Client, peUUID, gpu, "")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no available GPUs found")
		assert.True(t, IsTerminalError(err))
	})

	t.Run("should error when profile used on PC below 7.6", func(t *testing.T) {
		mockClientWrapper := NewMockConvergedClient(ctrl)

		gpu := infrav1.NutanixGPU{
			Type: infrav1.NutanixGPUIdentifierProfile,
			Profile: &infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierName,
				Name: ptr.To("A100-4Q"),
			},
		}

		_, err := GetGPU(ctx, mockClientWrapper.Client, peUUID, gpu, "7.5")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "requires Prism Central 7.6 or later")
		assert.True(t, IsTerminalError(err))
	})

	t.Run("should resolve a physical GPU profile by name", func(t *testing.T) {
		mockClientWrapper := NewMockConvergedClient(ctrl)

		gpu := infrav1.NutanixGPU{
			Type: infrav1.NutanixGPUIdentifierProfile,
			Profile: &infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierName,
				Name: ptr.To("TeslaT4"),
			},
		}

		mockClientWrapper.MockClusters.EXPECT().
			ListAHVPhysicalGPUProfiles(ctx, peUUID, gomock.Any()).
			Return([]clusterAhvModels.PhysicalGpuProfile{
				{
					ExtId:         ptr.To("pgpu-profile-uuid"),
					Configuration: &clusterAhvModels.PhysicalGpuProfileConfiguration{Name: ptr.To("TeslaT4")},
				},
			}, nil)
		mockClientWrapper.MockClusters.EXPECT().
			ListAHVVirtualGPUProfiles(ctx, peUUID, gomock.Any()).
			Return([]clusterAhvModels.VirtualGpuProfile{}, nil)

		result, err := GetGPU(ctx, mockClientWrapper.Client, peUUID, gpu, "7.6")
		assert.NoError(t, err)
		require.NotNil(t, result.BackingInfo)
		backing, ok := result.BackingInfo.GetValue().(vmmModels.PhysicalGpu)
		require.True(t, ok)
		require.NotNil(t, backing.PhysicalGpuProfileReference)
		assert.Equal(t, "pgpu-profile-uuid", *backing.PhysicalGpuProfileReference.ExtId)
	})

	t.Run("should error when profile name matches no profiles", func(t *testing.T) {
		mockClientWrapper := NewMockConvergedClient(ctrl)

		gpu := infrav1.NutanixGPU{
			Type: infrav1.NutanixGPUIdentifierProfile,
			Profile: &infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierName,
				Name: ptr.To("missing"),
			},
		}

		mockClientWrapper.MockClusters.EXPECT().
			ListAHVPhysicalGPUProfiles(ctx, peUUID, gomock.Any()).
			Return([]clusterAhvModels.PhysicalGpuProfile{}, nil)
		mockClientWrapper.MockClusters.EXPECT().
			ListAHVVirtualGPUProfiles(ctx, peUUID, gomock.Any()).
			Return([]clusterAhvModels.VirtualGpuProfile{}, nil)

		_, err := GetGPU(ctx, mockClientWrapper.Client, peUUID, gpu, "7.6")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), `no GPU profile found with name="missing"`)
		assert.True(t, IsTerminalError(err))
	})

	t.Run("should error when profile name matches multiple profiles", func(t *testing.T) {
		mockClientWrapper := NewMockConvergedClient(ctrl)

		gpu := infrav1.NutanixGPU{
			Type: infrav1.NutanixGPUIdentifierProfile,
			Profile: &infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierName,
				Name: ptr.To("dup"),
			},
		}

		mockClientWrapper.MockClusters.EXPECT().
			ListAHVPhysicalGPUProfiles(ctx, peUUID, gomock.Any()).
			Return([]clusterAhvModels.PhysicalGpuProfile{
				{ExtId: ptr.To("pgpu-1"), Configuration: &clusterAhvModels.PhysicalGpuProfileConfiguration{Name: ptr.To("dup")}},
			}, nil)
		mockClientWrapper.MockClusters.EXPECT().
			ListAHVVirtualGPUProfiles(ctx, peUUID, gomock.Any()).
			Return([]clusterAhvModels.VirtualGpuProfile{
				{ExtId: ptr.To("vgpu-1"), Configuration: &clusterAhvModels.VirtualGpuProfileConfiguration{Name: ptr.To("dup")}},
			}, nil)

		_, err := GetGPU(ctx, mockClientWrapper.Client, peUUID, gpu, "7.6")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), `more than one GPU profile found with name="dup"`)
	})

	t.Run("should resolve a virtual GPU profile by UUID", func(t *testing.T) {
		mockClientWrapper := NewMockConvergedClient(ctrl)
		profileUUID := "30f46867-4eb4-48c6-b3ab-c1ca2a2e2d85"
		profileFilter := gomock.Cond(func(option converged.ODataOption) bool {
			params, err := v4Converged.OptsToV4ODataParams(option)
			return err == nil && params.Filter != nil && *params.Filter == fmt.Sprintf("extId eq '%s'", profileUUID)
		})
		gpu := infrav1.NutanixGPU{
			Type: infrav1.NutanixGPUIdentifierProfile,
			Profile: &infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: &profileUUID,
			},
		}

		mockClientWrapper.MockClusters.EXPECT().
			ListAHVPhysicalGPUProfiles(ctx, peUUID, profileFilter).
			Return([]clusterAhvModels.PhysicalGpuProfile{}, nil)
		mockClientWrapper.MockClusters.EXPECT().
			ListAHVVirtualGPUProfiles(ctx, peUUID, profileFilter).
			Return([]clusterAhvModels.VirtualGpuProfile{
				{ExtId: &profileUUID},
			}, nil)

		result, err := GetGPU(ctx, mockClientWrapper.Client, peUUID, gpu, "7.6")
		require.NoError(t, err)
		backing, ok := result.BackingInfo.GetValue().(vmmModels.VirtualGpu)
		require.True(t, ok)
		assert.Equal(t, profileUUID, *backing.VirtualGpuProfileReference.ExtId)
	})
}

// TestGetGPUsForPE tests the GetGPUsForPE function
func TestGetGPUsForPE(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ctx := context.Background()
	peUUID := "test-pe-uuid"

	t.Run("should return physical GPUs filtered by device ID", func(t *testing.T) {
		// Create mock client
		mockClientWrapper := NewMockConvergedClient(ctrl)

		deviceID := int64(123)
		gpu := infrav1.NutanixGPU{
			Type:     infrav1.NutanixGPUIdentifierDeviceID,
			DeviceID: &deviceID,
		}

		// Mock physical GPU response
		physicalGPU := clusterModels.PhysicalGpuProfile{
			PhysicalGpuConfig: &clusterModels.PhysicalGpuConfig{
				DeviceName: ptr.To("test-gpu"),
				DeviceId:   ptr.To(int64(123)),
				IsInUse:    ptr.To(false),
				Mode:       clusterModels.GPUMODE_UNUSED.Ref(),
				VendorName: ptr.To("kNvidia"),
			},
		}

		mockClientWrapper.MockClusters.EXPECT().
			ListClusterPhysicalGPUs(ctx, peUUID, gomock.Any()).
			Return([]clusterModels.PhysicalGpuProfile{physicalGPU}, nil)

		mockClientWrapper.MockClusters.EXPECT().
			ListClusterVirtualGPUs(ctx, peUUID, gomock.Any()).
			Return([]clusterModels.VirtualGpuProfile{}, nil)

		gpus, err := GetGPUsForPE(ctx, mockClientWrapper.Client, peUUID, gpu)

		assert.NoError(t, err)
		assert.Len(t, gpus, 1)
		assert.Equal(t, "test-gpu", *gpus[0].Name)
		assert.Equal(t, 123, *gpus[0].DeviceId)
		assert.Equal(t, vmmModels.GPUMODE_PASSTHROUGH_COMPUTE, *gpus[0].Mode)
		assert.Equal(t, vmmModels.GPUVENDOR_NVIDIA, *gpus[0].Vendor)
	})

	t.Run("should return physical GPUs filtered by name", func(t *testing.T) {
		// Create mock client
		mockClientWrapper := NewMockConvergedClient(ctrl)

		gpuName := "test-gpu"
		gpu := infrav1.NutanixGPU{
			Type: infrav1.NutanixGPUIdentifierName,
			Name: &gpuName,
		}

		// Mock physical GPU response
		physicalGPU := clusterModels.PhysicalGpuProfile{
			PhysicalGpuConfig: &clusterModels.PhysicalGpuConfig{
				DeviceName: ptr.To("test-gpu"),
				DeviceId:   ptr.To(int64(456)),
				IsInUse:    ptr.To(false),
				Type:       clusterModels.GPUTYPE_PASSTHROUGH_GRAPHICS.Ref(),
				VendorName: ptr.To("kAmd"),
			},
		}

		mockClientWrapper.MockClusters.EXPECT().
			ListClusterPhysicalGPUs(ctx, peUUID, gomock.Any()).
			Return([]clusterModels.PhysicalGpuProfile{physicalGPU}, nil)

		mockClientWrapper.MockClusters.EXPECT().
			ListClusterVirtualGPUs(ctx, peUUID, gomock.Any()).
			Return([]clusterModels.VirtualGpuProfile{}, nil)

		gpus, err := GetGPUsForPE(ctx, mockClientWrapper.Client, peUUID, gpu)

		assert.NoError(t, err)
		assert.Len(t, gpus, 1)
		assert.Equal(t, "test-gpu", *gpus[0].Name)
		assert.Equal(t, 456, *gpus[0].DeviceId)
		assert.Equal(t, vmmModels.GPUMODE_PASSTHROUGH_GRAPHICS, *gpus[0].Mode)
		assert.Equal(t, vmmModels.GPUVENDOR_AMD, *gpus[0].Vendor)
	})

	t.Run("should return virtual GPUs", func(t *testing.T) {
		// Create mock client
		mockClientWrapper := NewMockConvergedClient(ctrl)

		deviceID := int64(789)
		gpu := infrav1.NutanixGPU{
			Type:     infrav1.NutanixGPUIdentifierDeviceID,
			DeviceID: &deviceID,
		}

		// Mock virtual GPU response
		virtualGPU := clusterModels.VirtualGpuProfile{
			VirtualGpuConfig: &clusterModels.VirtualGpuConfig{
				DeviceName: ptr.To("virtual-gpu"),
				DeviceId:   ptr.To(int64(789)),
				IsInUse:    ptr.To(false),
				VendorName: ptr.To("kIntel"),
			},
		}

		mockClientWrapper.MockClusters.EXPECT().
			ListClusterPhysicalGPUs(ctx, peUUID, gomock.Any()).
			Return([]clusterModels.PhysicalGpuProfile{}, nil)

		mockClientWrapper.MockClusters.EXPECT().
			ListClusterVirtualGPUs(ctx, peUUID, gomock.Any()).
			Return([]clusterModels.VirtualGpuProfile{virtualGPU}, nil)

		gpus, err := GetGPUsForPE(ctx, mockClientWrapper.Client, peUUID, gpu)

		assert.NoError(t, err)
		assert.Len(t, gpus, 1)
		assert.Equal(t, "virtual-gpu", *gpus[0].Name)
		assert.Equal(t, 789, *gpus[0].DeviceId)
		assert.Equal(t, vmmModels.GPUMODE_VIRTUAL, *gpus[0].Mode)
		assert.Equal(t, vmmModels.GPUVENDOR_INTEL, *gpus[0].Vendor)
	})

	t.Run("should return error when physical GPU API call fails", func(t *testing.T) {
		// Create mock client
		mockClientWrapper := NewMockConvergedClient(ctrl)

		deviceID := int64(123)
		gpu := infrav1.NutanixGPU{
			Type:     infrav1.NutanixGPUIdentifierDeviceID,
			DeviceID: &deviceID,
		}

		expectedError := errors.New("API call failed")
		mockClientWrapper.MockClusters.EXPECT().
			ListClusterPhysicalGPUs(ctx, peUUID, gomock.Any()).
			Return(nil, expectedError)

		_, err := GetGPUsForPE(ctx, mockClientWrapper.Client, peUUID, gpu)
		assert.Error(t, err)
		assert.Equal(t, expectedError, err)
	})

	t.Run("should return error when virtual GPU API call fails", func(t *testing.T) {
		// Create mock client
		mockClientWrapper := NewMockConvergedClient(ctrl)

		deviceID := int64(123)
		gpu := infrav1.NutanixGPU{
			Type:     infrav1.NutanixGPUIdentifierDeviceID,
			DeviceID: &deviceID,
		}

		// Mock successful physical GPU call
		mockClientWrapper.MockClusters.EXPECT().
			ListClusterPhysicalGPUs(ctx, peUUID, gomock.Any()).
			Return([]clusterModels.PhysicalGpuProfile{}, nil)

		expectedError := errors.New("virtual GPU API call failed")
		mockClientWrapper.MockClusters.EXPECT().
			ListClusterVirtualGPUs(ctx, peUUID, gomock.Any()).
			Return(nil, expectedError)

		_, err := GetGPUsForPE(ctx, mockClientWrapper.Client, peUUID, gpu)
		assert.Error(t, err)
		assert.Equal(t, expectedError, err)
	})

	t.Run("should skip GPUs that are in use", func(t *testing.T) {
		// Create mock client
		mockClientWrapper := NewMockConvergedClient(ctrl)

		deviceID := int64(123)
		gpu := infrav1.NutanixGPU{
			Type:     infrav1.NutanixGPUIdentifierDeviceID,
			DeviceID: &deviceID,
		}

		// Mock physical GPU that is in use
		physicalGPUInUse := clusterModels.PhysicalGpuProfile{
			PhysicalGpuConfig: &clusterModels.PhysicalGpuConfig{
				DeviceName: ptr.To("in-use-gpu"),
				DeviceId:   ptr.To(int64(123)),
				IsInUse:    ptr.To(true), // This GPU is in use
				Mode:       clusterModels.GPUMODE_USED_FOR_PASSTHROUGH.Ref(),
				VendorName: ptr.To("kNvidia"),
			},
		}

		// Mock physical GPU that is not in use
		physicalGPUAvailable := clusterModels.PhysicalGpuProfile{
			PhysicalGpuConfig: &clusterModels.PhysicalGpuConfig{
				DeviceName: ptr.To("available-gpu"),
				DeviceId:   ptr.To(int64(456)),
				IsInUse:    ptr.To(false), // This GPU is available
				Mode:       clusterModels.GPUMODE_UNUSED.Ref(),
				VendorName: ptr.To("kNvidia"),
			},
		}

		mockClientWrapper.MockClusters.EXPECT().
			ListClusterPhysicalGPUs(ctx, peUUID, gomock.Any()).
			Return([]clusterModels.PhysicalGpuProfile{physicalGPUInUse, physicalGPUAvailable}, nil)

		mockClientWrapper.MockClusters.EXPECT().
			ListClusterVirtualGPUs(ctx, peUUID, gomock.Any()).
			Return([]clusterModels.VirtualGpuProfile{}, nil)

		gpus, err := GetGPUsForPE(ctx, mockClientWrapper.Client, peUUID, gpu)

		assert.NoError(t, err)
		assert.Len(t, gpus, 1) // Only the available GPU should be returned
		assert.Equal(t, "available-gpu", *gpus[0].Name)
		assert.Equal(t, 456, *gpus[0].DeviceId)
	})
}

// TestGpuVendorStringToGpuVendor tests the gpuVendorStringToGpuVendor function
func TestGpuVendorStringToGpuVendor(t *testing.T) {
	tests := []struct {
		name     string
		vendor   string
		expected vmmModels.GpuVendor
	}{
		{
			name:     "NVIDIA vendor",
			vendor:   "kNvidia",
			expected: vmmModels.GPUVENDOR_NVIDIA,
		},
		{
			name:     "Intel vendor",
			vendor:   "kIntel",
			expected: vmmModels.GPUVENDOR_INTEL,
		},
		{
			name:     "AMD vendor",
			vendor:   "kAmd",
			expected: vmmModels.GPUVENDOR_AMD,
		},
		{
			name:     "Unknown vendor",
			vendor:   "kUnknown",
			expected: vmmModels.GPUVENDOR_UNKNOWN,
		},
		{
			name:     "Empty vendor",
			vendor:   "",
			expected: vmmModels.GPUVENDOR_UNKNOWN,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := gpuVendorStringToGpuVendor(tt.vendor)
			assert.Equal(t, tt.expected, *result)
		})
	}
}
