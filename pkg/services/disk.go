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

	multidomainModels "github.com/nutanix/ntnx-api-golang-clients/multidomain-go-client/v4/models/multidomain/v4/config"
	vmmconfig "github.com/nutanix/ntnx-api-golang-clients/vmm-go-client/v4/models/vmm/v4/ahv/config"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/ptr"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	nctx "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/pkg/context"
	v4Converged "github.com/nutanix-cloud-native/prism-go-client/converged/v4"
)

// GetMibValueOfQuantity returns the given quantity value in Mib
func GetMibValueOfQuantity(quantity resource.Quantity) int64 {
	return quantity.Value() / (1024 * 1024)
}

func CreateSystemDiskSpec(imageUUID string, systemDiskSizeInBytes int64) (*vmmconfig.Disk, error) {
	if imageUUID == "" {
		return nil, fmt.Errorf("image UUID must be set when creating system disk")
	}
	if systemDiskSizeInBytes <= 0 {
		return nil, fmt.Errorf("invalid system disk size in bytes: %d. Provide in XXGi (for example 70Gi) format instead", systemDiskSizeInBytes)
	}

	disk := vmmconfig.NewDisk()
	err := disk.SetBackingInfo(*NewVMDiskWithImageRef(&imageUUID, systemDiskSizeInBytes))
	if err != nil {
		return nil, err
	}

	return disk, nil
}

// CreateDataDiskList creates a list of data disks and cdRoms with the given data disk specs
func CreateDataDiskList(ctx context.Context, convergedClient *v4Converged.Client, dataDiskSpecs []infrav1.NutanixMachineVMDisk, peUUID string, project *nctx.ProjectInfo, pcVersion string, resourceGroup *multidomainModels.ResourceGroup) ([]vmmconfig.Disk, []vmmconfig.CdRom, error) {
	dataDisks := []vmmconfig.Disk{}
	dataCdRoms := []vmmconfig.CdRom{}

	latestDeviceIndexByAdapterType := make(map[string]int)
	getDeviceIndex := func(adapterType string) int {
		if latestDeviceIndex, ok := latestDeviceIndexByAdapterType[adapterType]; ok {
			latestDeviceIndexByAdapterType[adapterType] = latestDeviceIndex + 1
			return latestDeviceIndex
		}

		if adapterType == string(infrav1.NutanixMachineDiskAdapterTypeSCSI) || adapterType == string(infrav1.NutanixMachineDiskAdapterTypeIDE) {
			latestDeviceIndexByAdapterType[adapterType] = 1
			return 1
		} else {
			latestDeviceIndexByAdapterType[adapterType] = 0
			return 0
		}
	}

	for _, dataDiskSpec := range dataDiskSpecs {
		vmDisk := vmmconfig.NewVmDisk()
		vmDisk.DiskSizeBytes = ptr.To(int64(dataDiskSpec.DiskSize.Value()))

		err := addDataSourceImageRefToVmDisk(ctx, convergedClient, vmDisk, dataDiskSpec.DataSource, project, pcVersion)
		if err != nil {
			return nil, nil, err
		}

		err = addStorageConfigAndContainerToVmDisk(ctx, convergedClient, vmDisk, dataDiskSpec.StorageConfig, peUUID, resourceGroup)
		if err != nil {
			return nil, nil, err
		}

		// Set default values for device type and adapter type
		deviceType := infrav1.NutanixMachineDiskDeviceTypeDisk
		adapterType := infrav1.NutanixMachineDiskAdapterTypeSCSI

		// If device properties are provided, use them
		if dataDiskSpec.DeviceProperties != nil {
			deviceType = dataDiskSpec.DeviceProperties.DeviceType
			adapterType = dataDiskSpec.DeviceProperties.AdapterType
		}

		deviceIndex := getDeviceIndex(string(adapterType))
		if dataDiskSpec.DeviceProperties != nil && dataDiskSpec.DeviceProperties.DeviceIndex != 0 {
			deviceIndex = int(dataDiskSpec.DeviceProperties.DeviceIndex)
		}

		// Set device properties
		switch deviceType {
		case infrav1.NutanixMachineDiskDeviceTypeDisk:
			disk := vmmconfig.NewDisk()
			disk.DiskAddress = vmmconfig.NewDiskAddress()
			disk.DiskAddress.Index = ptr.To(deviceIndex)
			disk.DiskAddress.BusType = adapterTypeToDiskBusType(adapterType)
			err = disk.SetBackingInfo(*vmDisk)
			if err != nil {
				return nil, nil, err
			}

			dataDisks = append(dataDisks, *disk)
		case infrav1.NutanixMachineDiskDeviceTypeCDRom:
			cdRom := vmmconfig.NewCdRom()
			cdRom.DiskAddress = vmmconfig.NewCdRomAddress()
			cdRom.DiskAddress.Index = ptr.To(deviceIndex)
			cdRom.DiskAddress.BusType = adapterTypeToCdRomBusType(adapterType)
			cdRom.BackingInfo = vmDisk

			dataCdRoms = append(dataCdRoms, *cdRom)
		default:
			return nil, nil, fmt.Errorf("invalid NutanixMachineDiskDeviceType to create data disks")
		}
	}

	return dataDisks, dataCdRoms, nil
}

func addDataSourceImageRefToVmDisk(ctx context.Context, convergedClient *v4Converged.Client, vmDisk *vmmconfig.VmDisk, dataSource *infrav1.NutanixResourceIdentifier, project *nctx.ProjectInfo, pcVersion string) error {
	if dataSource == nil {
		return nil
	}

	image, err := GetImage(ctx, convergedClient, infrav1.NutanixResourceIdentifier{
		UUID: dataSource.UUID,
		Type: infrav1.NutanixIdentifierUUID,
	}, project, pcVersion)
	if err != nil {
		return err
	}

	vmDisk.DataSource = vmmconfig.NewDataSource()
	imageRef := vmmconfig.NewImageReference()
	imageRef.ImageExtId = image.ExtId
	err = vmDisk.DataSource.SetReference(*imageRef)
	if err != nil {
		return err
	}
	vmDisk.DataSource.ReferenceItemDiscriminator_ = nil

	return nil
}

func addStorageConfigAndContainerToVmDisk(ctx context.Context, convergedClient *v4Converged.Client, vmDisk *vmmconfig.VmDisk, storageConfig *infrav1.NutanixMachineVMStorageConfig, peUUID string, resourceGroup *multidomainModels.ResourceGroup) error {
	if storageConfig == nil {
		return nil
	}

	vmDisk.StorageConfig = vmmconfig.NewVmDiskStorageConfig()

	flashModeEnabled := storageConfig.DiskMode == infrav1.NutanixMachineDiskModeFlash
	vmDisk.StorageConfig.IsFlashModeEnabled = ptr.To(flashModeEnabled)

	if storageConfig.StorageContainer != nil {
		peID := infrav1.NutanixResourceIdentifier{
			UUID: &peUUID,
			Type: infrav1.NutanixIdentifierUUID,
		}
		sc, err := GetStorageContainerInCluster(ctx, convergedClient, resourceGroup, *storageConfig.StorageContainer, peID)
		if err != nil {
			return err
		}

		vmDisk.StorageContainer = vmmconfig.NewVmDiskContainerReference()
		vmDisk.StorageContainer.ExtId = sc.ContainerExtId
	}

	return nil
}

func NewVMDiskWithImageRef(dataSourceImageExtId *string, diskSizeInBytes int64) *vmmconfig.VmDisk {
	vmDisk := vmmconfig.NewVmDisk()

	if diskSizeInBytes > 0 {
		vmDisk.DiskSizeBytes = ptr.To(diskSizeInBytes)
	}

	if dataSourceImageExtId != nil {
		vmDisk.DataSource = vmmconfig.NewDataSource()
		imageRef := vmmconfig.NewImageReference()
		imageRef.ImageExtId = dataSourceImageExtId
		_ = vmDisk.DataSource.SetReference(*imageRef)
		vmDisk.DataSource.ReferenceItemDiscriminator_ = nil
	}

	return vmDisk
}

func adapterTypeToDiskBusType(adapterType infrav1.NutanixMachineDiskAdapterType) *vmmconfig.DiskBusType {
	switch adapterType {
	case infrav1.NutanixMachineDiskAdapterTypeSCSI:
		return vmmconfig.DISKBUSTYPE_SCSI.Ref()
	case infrav1.NutanixMachineDiskAdapterTypeIDE:
		return vmmconfig.DISKBUSTYPE_IDE.Ref()
	case infrav1.NutanixMachineDiskAdapterTypePCI:
		return vmmconfig.DISKBUSTYPE_PCI.Ref()
	case infrav1.NutanixMachineDiskAdapterTypeSATA:
		return vmmconfig.DISKBUSTYPE_SATA.Ref()
	default:
		return vmmconfig.DISKBUSTYPE_UNKNOWN.Ref()
	}
}

func adapterTypeToCdRomBusType(adapterType infrav1.NutanixMachineDiskAdapterType) *vmmconfig.CdRomBusType {
	switch adapterType {
	case infrav1.NutanixMachineDiskAdapterTypeIDE:
		return vmmconfig.CDROMBUSTYPE_IDE.Ref()
	case infrav1.NutanixMachineDiskAdapterTypeSATA:
		return vmmconfig.CDROMBUSTYPE_SATA.Ref()
	default:
		return vmmconfig.CDROMBUSTYPE_UNKNOWN.Ref()
	}
}
