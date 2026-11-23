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

import (
	"context"
	"fmt"
	"math/rand"
	"strings"

	"github.com/go-logr/logr"
	clusterModels "github.com/nutanix/ntnx-api-golang-clients/clustermgmt-go-client/v4/models/clustermgmt/v4/config"
	vmmconfig "github.com/nutanix/ntnx-api-golang-clients/vmm-go-client/v4/models/vmm/v4/ahv/config"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	"github.com/nutanix-cloud-native/prism-go-client/converged"
	v4Converged "github.com/nutanix-cloud-native/prism-go-client/converged/v4"
)

// GetGPUList returns a list of GPU device IDs for the given list of GPUs
func GetGPUList(ctx context.Context, client *v4Converged.Client, gpus []infrav1.NutanixGPU, peUUID, pcVersion string) ([]vmmconfig.Gpu, error) {
	resultGPUs := make([]vmmconfig.Gpu, 0)
	for _, gpu := range gpus {
		foundGPU, err := GetGPU(ctx, client, peUUID, gpu, pcVersion)
		if err != nil {
			return nil, err
		}
		resultGPUs = append(resultGPUs, *foundGPU)
	}
	return resultGPUs, nil
}

// GetGPU resolves a single GPU for the given Prism Element.
//
// When the GPU is identified by profile (PC 7.6+ only), CAPX only attaches a
// reference to the matching AHV GPU profile (physical or virtual); AHV then
// picks the concrete GPU at power-on according to its own scheduling logic.
//
// Otherwise the legacy device-name / device-ID lookup is used: CAPX lists the matching
// GPUs on the Prism Element, skips any already in use, and selects one at random.
func GetGPU(ctx context.Context, client *v4Converged.Client, peUUID string, gpu infrav1.NutanixGPU, pcVersion string) (*vmmconfig.Gpu, error) {
	if gpu.Type == infrav1.NutanixGPUIdentifierProfile {
		return getGPUFromProfile(ctx, client, peUUID, gpu, pcVersion)
	}

	allUnusedGPUs, err := GetGPUsForPE(ctx, client, peUUID, gpu)
	if err != nil {
		return nil, err
	}
	if len(allUnusedGPUs) == 0 {
		return nil, &terminalError{message: fmt.Sprintf("no available GPUs found in Prism Element cluster with UUID %s", peUUID)}
	}

	randomIndex := rand.Intn(len(allUnusedGPUs))
	return allUnusedGPUs[randomIndex], nil
}

// getGPUFromProfile resolves a GPU identified by an AHV GPU profile name or UUID. Profile-based
// GPU assignment uses the named GPU profile APIs, which are only available on PC 7.6 or later.
// It searches both the physical and virtual GPU profile catalogs of the Prism Element and
// returns a GPU whose backing info references the matching profile. The profile identifier must
// resolve to exactly one profile across both catalogs.
func getGPUFromProfile(ctx context.Context, client *v4Converged.Client, peUUID string, gpu infrav1.NutanixGPU, pcVersion string) (*vmmconfig.Gpu, error) {
	if !IsPCVersionHigherThan75(pcVersion) {
		return nil, &terminalError{message: fmt.Sprintf(
			"GPU profile %s requires Prism Central 7.6 or later", gpu.Profile.DisplayString())}
	}
	profile := *gpu.Profile

	var profileFilter converged.ODataOption
	switch profile.Type {
	case infrav1.NutanixIdentifierName:
		// Match by profile name server-side via an OData filter. We compare on the exact
		// name (not tolower()) because tolower() silently returns zero results for names
		// containing special characters such as "NVIDIA Corporation GA107GL [A2 / A16]";
		// single quotes are doubled per OData escaping.
		profileFilter = converged.WithFilter(fmt.Sprintf(
			"configuration/name eq '%s'", strings.ReplaceAll(*profile.Name, "'", "''")))
	case infrav1.NutanixIdentifierUUID:
		profileFilter = converged.WithFilter(fmt.Sprintf("extId eq '%s'", *profile.UUID))
	default:
		return nil, &terminalError{message: fmt.Sprintf("unsupported GPU profile identifier %s", profile.DisplayString())}
	}

	physicalProfiles, err := client.Clusters.ListAHVPhysicalGPUProfiles(ctx, peUUID, profileFilter)
	if err != nil {
		return nil, fmt.Errorf("failed to list physical GPU profiles for Prism Element cluster with UUID %s: %w", peUUID, err)
	}

	virtualProfiles, err := client.Clusters.ListAHVVirtualGPUProfiles(ctx, peUUID, profileFilter)
	if err != nil {
		return nil, fmt.Errorf("failed to list virtual GPU profiles for Prism Element cluster with UUID %s: %w", peUUID, err)
	}

	switch total := len(physicalProfiles) + len(virtualProfiles); {
	case total == 0:
		return nil, &terminalError{message: fmt.Sprintf(
			"no GPU profile found with %s in Prism Element cluster with UUID %s", profile.DisplayString(), peUUID)}
	case total > 1:
		return nil, fmt.Errorf(
			"more than one GPU profile found with %s in Prism Element cluster with UUID %s", profile.DisplayString(), peUUID)
	}

	vmGpu := vmmconfig.NewGpu()
	if len(physicalProfiles) == 1 {
		ref := vmmconfig.NewPhysicalGpuProfileReference()
		ref.ExtId = physicalProfiles[0].ExtId
		backing := vmmconfig.NewPhysicalGpu()
		backing.PhysicalGpuProfileReference = ref
		if err := vmGpu.SetBackingInfo(*backing); err != nil {
			return nil, fmt.Errorf("failed to set physical GPU backing info for profile %s: %w", profile.DisplayString(), err)
		}
		return vmGpu, nil
	}

	ref := vmmconfig.NewVirtualGpuProfileReference()
	ref.ExtId = virtualProfiles[0].ExtId
	backing := vmmconfig.NewVirtualGpu()
	backing.VirtualGpuProfileReference = ref
	if err := vmGpu.SetBackingInfo(*backing); err != nil {
		return nil, fmt.Errorf("failed to set virtual GPU backing info for profile %s: %w", profile.DisplayString(), err)
	}
	return vmGpu, nil
}

func GetGPUsForPE(ctx context.Context, client *v4Converged.Client, peUUID string, gpu infrav1.NutanixGPU) ([]*vmmconfig.Gpu, error) {
	log := ctrl.LoggerFrom(ctx)
	var filter string
	var gpus []*vmmconfig.Gpu

	if gpu.DeviceID != nil {
		filter = fmt.Sprintf("physicalGpuConfig/deviceId eq %d", *gpu.DeviceID)
	} else if gpu.Name != nil {
		filter = fmt.Sprintf("physicalGpuConfig/deviceName eq '%s'", *gpu.Name)
	}

	physicalGPUs, err := client.Clusters.ListClusterPhysicalGPUs(ctx, peUUID, converged.WithFilter(filter))
	if err != nil {
		return nil, err
	}
	for _, physicalGPU := range physicalGPUs {
		cfg := physicalGPU.PhysicalGpuConfig
		if cfg == nil {
			log.V(1).Info("skipping physical GPU with nil PhysicalGpuConfig")
			continue
		}
		mode := vmmconfig.GPUMODE_PASSTHROUGH_COMPUTE.Ref()
		if cfg.Type != nil && *cfg.Type == clusterModels.GPUTYPE_PASSTHROUGH_GRAPHICS {
			mode = vmmconfig.GPUMODE_PASSTHROUGH_GRAPHICS.Ref()
		}
		if vmGpu := gpuConfigToVMGpu(log, "physical", cfg.DeviceName, cfg.DeviceId, cfg.VendorName, cfg.IsInUse, mode); vmGpu != nil {
			gpus = append(gpus, vmGpu)
		}
	}

	if gpu.Name != nil {
		filter = fmt.Sprintf("virtualGpuConfig/deviceName eq '%s'", *gpu.Name)
	} else if gpu.DeviceID != nil {
		filter = fmt.Sprintf("virtualGpuConfig/deviceId eq %d", *gpu.DeviceID)
	}

	virtualGPUs, err := client.Clusters.ListClusterVirtualGPUs(ctx, peUUID, converged.WithFilter(filter))
	if err != nil {
		return nil, err
	}
	for _, virtualGPU := range virtualGPUs {
		cfg := virtualGPU.VirtualGpuConfig
		if cfg == nil {
			log.V(1).Info("skipping virtual GPU with nil VirtualGpuConfig")
			continue
		}
		if vmGpu := gpuConfigToVMGpu(log, "virtual", cfg.DeviceName, cfg.DeviceId, cfg.VendorName, cfg.IsInUse, vmmconfig.GPUMODE_VIRTUAL.Ref()); vmGpu != nil {
			gpus = append(gpus, vmGpu)
		}
	}
	return gpus, nil
}

// gpuConfigToVMGpu builds a *vmmconfig.Gpu from the common fields of a physical
// or virtual GPU config returned by Prism Central. It returns nil (and logs the
// reason) when the GPU should be skipped: it is already in use, is missing a
// deviceId/vendorName, or has a deviceId that does not fit the platform int.
// kind is used only for log messages ("physical" / "virtual").
func gpuConfigToVMGpu(log logr.Logger, kind string, deviceName *string, deviceID *int64, vendorName *string, isInUse *bool, mode *vmmconfig.GpuMode) *vmmconfig.Gpu {
	if isInUse != nil && *isInUse {
		return nil
	}
	if deviceID == nil || vendorName == nil {
		log.V(1).Info(fmt.Sprintf("skipping %s GPU missing deviceId or vendorName", kind))
		return nil
	}
	if int64(int(*deviceID)) != *deviceID {
		log.V(1).Info(fmt.Sprintf("skipping %s GPU with deviceId %d outside platform int range", kind, *deviceID))
		return nil
	}

	vmGpu := vmmconfig.NewGpu()
	vmGpu.Name = deviceName
	vmGpu.DeviceId = ptr.To(int(*deviceID))
	vmGpu.Mode = mode
	vmGpu.Vendor = gpuVendorStringToGpuVendor(*vendorName)
	return vmGpu
}

func gpuVendorStringToGpuVendor(vendor string) *vmmconfig.GpuVendor {
	switch vendor {
	case "kNvidia":
		return vmmconfig.GPUVENDOR_NVIDIA.Ref()
	case "kIntel":
		return vmmconfig.GPUVENDOR_INTEL.Ref()
	case "kAmd":
		return vmmconfig.GPUVENDOR_AMD.Ref()
	default:
		return vmmconfig.GPUVENDOR_UNKNOWN.Ref()
	}
}
