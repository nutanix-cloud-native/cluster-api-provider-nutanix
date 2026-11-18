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
	"reflect"
	"strings"
	"testing"

	clusterModels "github.com/nutanix/ntnx-api-golang-clients/clustermgmt-go-client/v4/models/clustermgmt/v4/config"
	vmmModels "github.com/nutanix/ntnx-api-golang-clients/vmm-go-client/v4/models/vmm/v4/ahv/config"
	imageModels "github.com/nutanix/ntnx-api-golang-clients/vmm-go-client/v4/models/vmm/v4/content"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	v4Converged "github.com/nutanix-cloud-native/prism-go-client/converged/v4"

	"go.uber.org/mock/gomock"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/ptr"
)

func TestCreateDataDiskList(t *testing.T) {
	expectedStorageContainers := []clusterModels.StorageContainer{
		{
			ContainerExtId: ptr.To("06b1ce03-f384-4488-9ba1-ae17ebcf1f91"),
			ClusterExtId:   ptr.To("00062e56-b9ac-7253-1946-7cc25586eeee"),
		},
	}

	tests := []struct {
		name             string
		convergedBuilder func() *v4Converged.Client
		dataDiskSpecs    []infrav1.NutanixMachineVMDisk
		peUUID           string
		wantDisks        func() []vmmModels.Disk
		wantCdRoms       func() []vmmModels.CdRom
		wantErr          bool
		errorMessage     string
	}{
		{
			name: "successful data disk creation without image reference",
			convergedBuilder: func() *v4Converged.Client {
				convergedClient := NewMockConvergedClient(gomock.NewController(t))
				convergedClient.MockStorageContainers.EXPECT().List(gomock.Any(), gomock.Any()).Return(expectedStorageContainers, nil)
				return convergedClient.Client
			},
			dataDiskSpecs: []infrav1.NutanixMachineVMDisk{
				{
					DiskSize: resource.MustParse("20Gi"),
					DeviceProperties: &infrav1.NutanixMachineVMDiskDeviceProperties{
						DeviceType:  infrav1.NutanixMachineDiskDeviceTypeDisk,
						AdapterType: infrav1.NutanixMachineDiskAdapterTypeSCSI,
					},
					StorageConfig: &infrav1.NutanixMachineVMStorageConfig{
						DiskMode: infrav1.NutanixMachineDiskModeStandard,
						StorageContainer: &infrav1.NutanixResourceIdentifier{
							UUID: ptr.To("06b1ce03-f384-4488-9ba1-ae17ebcf1f91"),
							Type: infrav1.NutanixIdentifierUUID,
						},
					},
				},
			},
			peUUID: "00062e56-b9ac-7253-1946-7cc25586eeee",
			wantDisks: func() []vmmModels.Disk {
				disk := vmmModels.NewDisk()
				disk.DiskAddress = vmmModels.NewDiskAddress()
				disk.DiskAddress.Index = ptr.To(1)
				disk.DiskAddress.BusType = vmmModels.DISKBUSTYPE_SCSI.Ref()

				vmDisk := vmmModels.NewVmDisk()
				vmDisk.DiskSizeBytes = ptr.To(int64(21474836480))
				vmDisk.StorageConfig = vmmModels.NewVmDiskStorageConfig()
				vmDisk.StorageConfig.IsFlashModeEnabled = ptr.To(false)
				vmDisk.StorageContainer = vmmModels.NewVmDiskContainerReference()
				vmDisk.StorageContainer.ExtId = ptr.To("06b1ce03-f384-4488-9ba1-ae17ebcf1f91")
				_ = disk.SetBackingInfo(*vmDisk)

				return []vmmModels.Disk{*disk}
			},
			wantCdRoms: func() []vmmModels.CdRom { return []vmmModels.CdRom{} },
			wantErr:    false,
		},
		{
			name: "successful data disk creation with image reference",
			convergedBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				expectedImage := &imageModels.Image{
					ExtId: ptr.To("f47ac10b-58cc-4372-a567-0e02b2c3d479"),
					Name:  ptr.To("data-image"),
				}
				convergedClient.MockImages.EXPECT().Get(gomock.Any(), "f47ac10b-58cc-4372-a567-0e02b2c3d479").Return(expectedImage, nil)
				convergedClient.MockStorageContainers.EXPECT().List(gomock.Any(), gomock.Any()).Return(expectedStorageContainers, nil)
				return convergedClient.Client
			},
			dataDiskSpecs: []infrav1.NutanixMachineVMDisk{
				{
					DiskSize: resource.MustParse("20Gi"),
					DeviceProperties: &infrav1.NutanixMachineVMDiskDeviceProperties{
						DeviceType:  infrav1.NutanixMachineDiskDeviceTypeDisk,
						AdapterType: infrav1.NutanixMachineDiskAdapterTypeSCSI,
					},
					DataSource: &infrav1.NutanixResourceIdentifier{
						UUID: ptr.To("f47ac10b-58cc-4372-a567-0e02b2c3d479"),
						Type: infrav1.NutanixIdentifierUUID,
					},
					StorageConfig: &infrav1.NutanixMachineVMStorageConfig{
						DiskMode: infrav1.NutanixMachineDiskModeStandard,
						StorageContainer: &infrav1.NutanixResourceIdentifier{
							UUID: ptr.To("06b1ce03-f384-4488-9ba1-ae17ebcf1f91"),
							Type: infrav1.NutanixIdentifierUUID,
						},
					},
				},
			},
			peUUID: "00062e56-b9ac-7253-1946-7cc25586eeee",
			wantDisks: func() []vmmModels.Disk {
				disk := vmmModels.NewDisk()
				disk.DiskAddress = vmmModels.NewDiskAddress()
				disk.DiskAddress.Index = ptr.To(1)
				disk.DiskAddress.BusType = vmmModels.DISKBUSTYPE_SCSI.Ref()

				vmDisk := vmmModels.NewVmDisk()
				vmDisk.DiskSizeBytes = ptr.To(int64(21474836480))

				imageRef := vmmModels.NewImageReference()
				imageRef.ImageExtId = ptr.To("f47ac10b-58cc-4372-a567-0e02b2c3d479")
				vmDisk.DataSource = vmmModels.NewDataSource()
				_ = vmDisk.DataSource.SetReference(*imageRef)
				vmDisk.DataSource.ReferenceItemDiscriminator_ = nil

				vmDisk.StorageConfig = vmmModels.NewVmDiskStorageConfig()
				vmDisk.StorageConfig.IsFlashModeEnabled = ptr.To(false)

				vmDisk.StorageContainer = vmmModels.NewVmDiskContainerReference()
				vmDisk.StorageContainer.ExtId = ptr.To("06b1ce03-f384-4488-9ba1-ae17ebcf1f91")
				_ = disk.SetBackingInfo(*vmDisk)

				return []vmmModels.Disk{*disk}
			},
			wantCdRoms: func() []vmmModels.CdRom { return []vmmModels.CdRom{} },
			wantErr:    false,
		},
		{
			name: "failed image lookup for data source",
			convergedBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				convergedClient.MockImages.EXPECT().Get(gomock.Any(), "f47ac10b-58cc-4372-a567-0e02b2c3d479").Return(nil, errors.New("image not found"))
				return convergedClient.Client
			},
			dataDiskSpecs: []infrav1.NutanixMachineVMDisk{
				{
					DiskSize: resource.MustParse("20Gi"),
					DeviceProperties: &infrav1.NutanixMachineVMDiskDeviceProperties{
						DeviceType:  infrav1.NutanixMachineDiskDeviceTypeDisk,
						AdapterType: infrav1.NutanixMachineDiskAdapterTypeSCSI,
					},
					DataSource: &infrav1.NutanixResourceIdentifier{
						UUID: ptr.To("f47ac10b-58cc-4372-a567-0e02b2c3d479"),
						Type: infrav1.NutanixIdentifierUUID,
					},
				},
			},
			peUUID:       "00062e56-b9ac-7253-1946-7cc25586eeee",
			wantDisks:    func() []vmmModels.Disk { return nil },
			wantCdRoms:   func() []vmmModels.CdRom { return nil },
			wantErr:      true,
			errorMessage: "image not found",
		},
		{
			name: "multiple data disks with different adapter types",
			convergedBuilder: func() *v4Converged.Client {
				convergedClient := NewMockConvergedClient(gomock.NewController(t))
				return convergedClient.Client
			},
			dataDiskSpecs: []infrav1.NutanixMachineVMDisk{
				{
					DiskSize: resource.MustParse("20Gi"),
					DeviceProperties: &infrav1.NutanixMachineVMDiskDeviceProperties{
						DeviceType:  infrav1.NutanixMachineDiskDeviceTypeDisk,
						AdapterType: infrav1.NutanixMachineDiskAdapterTypeSCSI,
					},
				},
				{
					DiskSize: resource.MustParse("30Gi"),
					DeviceProperties: &infrav1.NutanixMachineVMDiskDeviceProperties{
						DeviceType:  infrav1.NutanixMachineDiskDeviceTypeDisk,
						AdapterType: infrav1.NutanixMachineDiskAdapterTypeIDE,
					},
				},
			},
			peUUID: "00062e56-b9ac-7253-1946-7cc25586eeee",
			wantDisks: func() []vmmModels.Disk {
				disk1 := vmmModels.NewDisk()
				disk1.DiskAddress = vmmModels.NewDiskAddress()
				disk1.DiskAddress.Index = ptr.To(1)
				disk1.DiskAddress.BusType = vmmModels.DISKBUSTYPE_SCSI.Ref()

				vmDisk1 := vmmModels.NewVmDisk()
				vmDisk1.DiskSizeBytes = ptr.To(int64(21474836480))
				_ = disk1.SetBackingInfo(*vmDisk1)

				disk2 := vmmModels.NewDisk()
				disk2.DiskAddress = vmmModels.NewDiskAddress()
				disk2.DiskAddress.Index = ptr.To(1)
				disk2.DiskAddress.BusType = vmmModels.DISKBUSTYPE_IDE.Ref()

				vmDisk2 := vmmModels.NewVmDisk()
				vmDisk2.DiskSizeBytes = ptr.To(int64(32212254720))
				_ = disk2.SetBackingInfo(*vmDisk2)
				return []vmmModels.Disk{*disk1, *disk2}
			},
			wantCdRoms: func() []vmmModels.CdRom { return []vmmModels.CdRom{} },
			wantErr:    false,
		},
		{
			name: "data disk with flash mode enabled",
			convergedBuilder: func() *v4Converged.Client {
				convergedClient := NewMockConvergedClient(gomock.NewController(t))
				convergedClient.MockStorageContainers.EXPECT().List(gomock.Any(), gomock.Any()).Return(expectedStorageContainers, nil)
				return convergedClient.Client
			},
			dataDiskSpecs: []infrav1.NutanixMachineVMDisk{
				{
					DiskSize: resource.MustParse("20Gi"),
					DeviceProperties: &infrav1.NutanixMachineVMDiskDeviceProperties{
						DeviceType:  infrav1.NutanixMachineDiskDeviceTypeDisk,
						AdapterType: infrav1.NutanixMachineDiskAdapterTypeSCSI,
					},
					StorageConfig: &infrav1.NutanixMachineVMStorageConfig{
						DiskMode: infrav1.NutanixMachineDiskModeFlash,
						StorageContainer: &infrav1.NutanixResourceIdentifier{
							UUID: ptr.To("06b1ce03-f384-4488-9ba1-ae17ebcf1f91"),
							Type: infrav1.NutanixIdentifierUUID,
						},
					},
				},
			},
			peUUID: "00062e56-b9ac-7253-1946-7cc25586eeee",
			wantDisks: func() []vmmModels.Disk {
				disk := vmmModels.NewDisk()
				disk.DiskAddress = vmmModels.NewDiskAddress()
				disk.DiskAddress.Index = ptr.To(1)
				disk.DiskAddress.BusType = vmmModels.DISKBUSTYPE_SCSI.Ref()

				vmDisk := vmmModels.NewVmDisk()
				vmDisk.DiskSizeBytes = ptr.To(int64(21474836480))

				vmDisk.StorageConfig = vmmModels.NewVmDiskStorageConfig()
				vmDisk.StorageConfig.IsFlashModeEnabled = ptr.To(true)

				vmDisk.StorageContainer = vmmModels.NewVmDiskContainerReference()
				vmDisk.StorageContainer.ExtId = ptr.To("06b1ce03-f384-4488-9ba1-ae17ebcf1f91")
				_ = disk.SetBackingInfo(*vmDisk)

				return []vmmModels.Disk{*disk}
			},
			wantCdRoms: func() []vmmModels.CdRom { return []vmmModels.CdRom{} },
			wantErr:    false,
		},
		{
			name: "data disk with custom device index",
			convergedBuilder: func() *v4Converged.Client {
				convergedClient := NewMockConvergedClient(gomock.NewController(t))
				return convergedClient.Client
			},
			dataDiskSpecs: []infrav1.NutanixMachineVMDisk{
				{
					DiskSize: resource.MustParse("20Gi"),
					DeviceProperties: &infrav1.NutanixMachineVMDiskDeviceProperties{
						DeviceType:  infrav1.NutanixMachineDiskDeviceTypeDisk,
						AdapterType: infrav1.NutanixMachineDiskAdapterTypeSCSI,
						DeviceIndex: 5,
					},
				},
			},
			peUUID: "00062e56-b9ac-7253-1946-7cc25586eeee",
			wantDisks: func() []vmmModels.Disk {
				disk := vmmModels.NewDisk()
				disk.DiskAddress = vmmModels.NewDiskAddress()
				disk.DiskAddress.Index = ptr.To(5)
				disk.DiskAddress.BusType = vmmModels.DISKBUSTYPE_SCSI.Ref()

				vmDisk := vmmModels.NewVmDisk()
				vmDisk.DiskSizeBytes = ptr.To(int64(21474836480))
				_ = disk.SetBackingInfo(*vmDisk)

				return []vmmModels.Disk{*disk}
			},
			wantCdRoms: func() []vmmModels.CdRom { return []vmmModels.CdRom{} },
			wantErr:    false,
		},
		{
			name: "data disk with CDRom device type",
			convergedBuilder: func() *v4Converged.Client {
				convergedClient := NewMockConvergedClient(gomock.NewController(t))
				return convergedClient.Client
			},
			dataDiskSpecs: []infrav1.NutanixMachineVMDisk{
				{
					DiskSize: resource.MustParse("1Gi"),
					DeviceProperties: &infrav1.NutanixMachineVMDiskDeviceProperties{
						DeviceType:  infrav1.NutanixMachineDiskDeviceTypeCDRom,
						AdapterType: infrav1.NutanixMachineDiskAdapterTypeIDE,
					},
				},
			},
			peUUID:    "00062e56-b9ac-7253-1946-7cc25586eeee",
			wantDisks: func() []vmmModels.Disk { return []vmmModels.Disk{} },
			wantCdRoms: func() []vmmModels.CdRom {
				cdRom := vmmModels.NewCdRom()
				cdRom.DiskAddress = vmmModels.NewCdRomAddress()
				cdRom.DiskAddress.Index = ptr.To(1)
				cdRom.DiskAddress.BusType = vmmModels.CDROMBUSTYPE_IDE.Ref()

				vmDisk := vmmModels.NewVmDisk()
				vmDisk.DiskSizeBytes = ptr.To(int64(1073741824))
				cdRom.BackingInfo = vmDisk

				return []vmmModels.CdRom{*cdRom}
			},
			wantErr: false,
		},
		{
			name: "data disk with default values when no device properties provided",
			convergedBuilder: func() *v4Converged.Client {
				convergedClient := NewMockConvergedClient(gomock.NewController(t))
				return convergedClient.Client
			},
			dataDiskSpecs: []infrav1.NutanixMachineVMDisk{
				{
					DiskSize: resource.MustParse("20Gi"),
					// No DeviceProperties provided - should use defaults
				},
			},
			peUUID: "00062e56-b9ac-7253-1946-7cc25586eeee",
			wantDisks: func() []vmmModels.Disk {
				disk := vmmModels.NewDisk()
				disk.DiskAddress = vmmModels.NewDiskAddress()
				disk.DiskAddress.Index = ptr.To(1)
				disk.DiskAddress.BusType = vmmModels.DISKBUSTYPE_SCSI.Ref()

				vmDisk := vmmModels.NewVmDisk()
				vmDisk.DiskSizeBytes = ptr.To(int64(21474836480))
				_ = disk.SetBackingInfo(*vmDisk)

				return []vmmModels.Disk{*disk}
			},
			wantCdRoms: func() []vmmModels.CdRom { return []vmmModels.CdRom{} },
			wantErr:    false,
		},
		{
			name: "data disk with storage container lookup failure",
			convergedBuilder: func() *v4Converged.Client {
				convergedClient := NewMockConvergedClient(gomock.NewController(t))
				convergedClient.MockStorageContainers.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, errors.New("fake error"))
				return convergedClient.Client
			},
			dataDiskSpecs: []infrav1.NutanixMachineVMDisk{
				{
					DiskSize: resource.MustParse("20Gi"),
					DeviceProperties: &infrav1.NutanixMachineVMDiskDeviceProperties{
						DeviceType:  infrav1.NutanixMachineDiskDeviceTypeDisk,
						AdapterType: infrav1.NutanixMachineDiskAdapterTypeSCSI,
					},
					StorageConfig: &infrav1.NutanixMachineVMStorageConfig{
						DiskMode: infrav1.NutanixMachineDiskModeStandard,
						StorageContainer: &infrav1.NutanixResourceIdentifier{
							UUID: ptr.To("06b1ce03-f384-4488-9ba1-ae17ebcf1f91"),
							Type: infrav1.NutanixIdentifierUUID,
						},
					},
				},
			},
			peUUID:       "00062e56-b9ac-7253-1946-7cc25586eeee",
			wantDisks:    func() []vmmModels.Disk { return nil },
			wantCdRoms:   func() []vmmModels.CdRom { return nil },
			wantErr:      true,
			errorMessage: "fake error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Log("Running test case ", tt.name)
			ctx := context.Background()
			disks, cdRoms, err := CreateDataDiskList(
				ctx,
				tt.convergedBuilder(),
				tt.dataDiskSpecs,
				tt.peUUID,
				nil,
				"",
				nil,
			)
			if (err != nil) != tt.wantErr {
				t.Errorf("CreateDataDiskList() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(disks, tt.wantDisks()) {
				t.Errorf("CreateDataDiskList() = %v, want %v", disks, tt.wantDisks())
			}
			if !reflect.DeepEqual(cdRoms, tt.wantCdRoms()) {
				t.Errorf("CreateDataDiskList() = %v, want %v", cdRoms, tt.wantCdRoms())
			}
			if tt.errorMessage != "" && err != nil {
				if !strings.Contains(err.Error(), tt.errorMessage) {
					t.Errorf("CreateDataDiskList() error message = %v, want to contain %v", err.Error(), tt.errorMessage)
				}
			}
		})
	}
}
