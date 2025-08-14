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

package controllers

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"text/template"
	"time"

	"github.com/google/uuid"
	"github.com/nutanix-cloud-native/prism-go-client/facade"
	"github.com/nutanix-cloud-native/prism-go-client/utils"
	prismclientv3 "github.com/nutanix-cloud-native/prism-go-client/v3"
	prismclientv4 "github.com/nutanix-cloud-native/prism-go-client/v4"
	prismclientfacadev4 "github.com/nutanix-cloud-native/prism-go-client/facade/v4"
	clustermgmtconfig "github.com/nutanix/ntnx-api-golang-clients/clustermgmt-go-client/v4/models/clustermgmt/v4/config"
	networkingconfig "github.com/nutanix/ntnx-api-golang-clients/networking-go-client/v4/models/networking/v4/config"
	vmmconfig "github.com/nutanix/ntnx-api-golang-clients/vmm-go-client/v4/models/vmm/v4/ahv/config"
	vmmcontent "github.com/nutanix/ntnx-api-golang-clients/vmm-go-client/v4/models/vmm/v4/content"
	prismconfig "github.com/nutanix/ntnx-api-golang-clients/volumes-go-client/v4/models/prism/v4/config"
	volumesconfig "github.com/nutanix/ntnx-api-golang-clients/volumes-go-client/v4/models/volumes/v4/config"
	"k8s.io/apimachinery/pkg/api/resource"
	v1 "k8s.io/client-go/informers/core/v1"
	"k8s.io/utils/ptr"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/conditions"
	ctrl "sigs.k8s.io/controller-runtime"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	nutanixclient "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/pkg/client"
)

const (
	providerIdPrefix = "nutanix://"

	taskSucceededMessage = "SUCCEEDED"
	serviceNamePECluster = "AOS"

	gpuUnused = "UNUSED"

	detachVGRequeueAfter = 30 * time.Second

	ImageStateDeletePending    = "DELETE_PENDING"
	ImageStateDeleteInProgress = "DELETE_IN_PROGRESS"

	createErrorFailureReason = "CreateError"
)

type StorageContainerIntentResponse struct {
	Name        *string
	UUID        *string
	ClusterName *string
	ClusterUUID *string
}

// DeleteVM deletes a VM and is invoked by the NutanixMachineReconciler
func DeleteVM(ctx context.Context, client *prismclientv3.Client, vmName, vmUUID string) (string, error) {
	log := ctrl.LoggerFrom(ctx)
	var err error

	if vmUUID == "" {
		log.V(1).Info("VmUUID was empty. Skipping delete")
		return "", nil
	}

	log.Info(fmt.Sprintf("Deleting VM %s with UUID: %s", vmName, vmUUID))
	vmDeleteResponse, err := client.V3.DeleteVM(ctx, vmUUID)
	if err != nil {
		log.Error(err, fmt.Sprintf("error deleting vm %s", vmName))
		return "", err
	}
	deleteTaskUUID := vmDeleteResponse.Status.ExecutionContext.TaskUUID.(string)

	return deleteTaskUUID, nil
}

// FindVMByUUID retrieves the VM with the given vm UUID. Returns nil if not found
func FindVMByUUID(ctx context.Context, client *prismclientfacadev4.FacadeV4Client, uuid string) (*vmmconfig.Vm, error) {
	log := ctrl.LoggerFrom(ctx)
	log.V(1).Info(fmt.Sprintf("Checking if VM with UUID %s exists.", uuid))

	response, err := client.GetVM(uuid)
	if err != nil {
		if strings.Contains(fmt.Sprint(err), "ENTITY_NOT_FOUND") {
			log.V(1).Info(fmt.Sprintf("vm with uuid %s does not exist.", uuid))
			return nil, nil
		} else {
			log.Error(err, fmt.Sprintf("Failed to find VM by vmUUID %s", uuid))
			return nil, err
		}
	}

	return response, nil
}

// GenerateProviderID generates a provider ID for the given resource UUID
func GenerateProviderID(uuid string) string {
	return fmt.Sprintf("%s%s", providerIdPrefix, uuid)
}

// GetVMUUID returns the UUID of the VM with the given name
func GetVMUUID(nutanixMachine *infrav1.NutanixMachine) (string, error) {
	vmUUID := nutanixMachine.Status.VmUUID
	if vmUUID != "" {
		if _, err := uuid.Parse(vmUUID); err != nil {
			return "", fmt.Errorf("VMUUID was set but was not a valid UUID: %s err: %v", vmUUID, err)
		}
		return vmUUID, nil
	}
	providerID := nutanixMachine.Spec.ProviderID
	if providerID == "" {
		return "", nil
	}
	id := strings.TrimPrefix(providerID, providerIdPrefix)
	// Not returning error since the ProviderID initially is not a UUID. CAPX only sets the UUID after VM provisioning.
	// If it is not a UUID, continue.
	if _, err := uuid.Parse(id); err != nil {
		return "", nil
	}
	return id, nil
}

// FindVM retrieves the VM with the given uuid or name
func FindVM(ctx context.Context, client *prismclientfacadev4.FacadeV4Client, nutanixMachine *infrav1.NutanixMachine, vmName string) (*vmmconfig.Vm, error) {
	log := ctrl.LoggerFrom(ctx)
	vmUUID, err := GetVMUUID(nutanixMachine)
	if err != nil {
		return nil, err
	}
	// Search via uuid if it is present
	if vmUUID != "" {
		log.V(1).Info(fmt.Sprintf("Searching for VM %s using UUID %s", vmName, vmUUID))
		vm, err := FindVMByUUID(ctx, client, vmUUID)
		if err != nil {
			return nil, err
		}
		if vm == nil {
			return nil, fmt.Errorf("no vm %s found with UUID %s but was expected to be present", vmName, vmUUID)
		}
		// Check if the VM name matches the Machine name or the NutanixMachine name.
		// Earlier, we were creating VMs with the same name as the NutanixMachine name.
		// Now, we create VMs with the same name as the Machine name in line with other CAPI providers.
		// This check is to ensure that we are deleting the correct VM for both cases as older CAPX VMs
		// will have the NutanixMachine name as the VM name.
		if *vm.Name != vmName && *vm.Name != nutanixMachine.Name {
			return nil, fmt.Errorf("found VM with UUID %s but name %s did not match %s", vmUUID, *vm.Name, vmName)
		}
		return vm, nil
		// otherwise search via name
	} else {
		log.Info(fmt.Sprintf("Searching for VM %s using name", vmName))
		vm, err := FindVMByName(ctx, client, vmName)
		if err != nil {
			log.Error(err, fmt.Sprintf("error occurred finding VM %s by name", vmName))
			return nil, err
		}
		return vm, nil
	}
}

// FindVMByName retrieves the VM with the given vm name
func FindVMByName(ctx context.Context, client *prismclientfacadev4.FacadeV4Client, vmName string) (*vmmconfig.Vm, error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info(fmt.Sprintf("Checking if VM with name %s exists.", vmName))

	opts := facade.WithFilter(fmt.Sprintf("name eq '%s'", vmName))
	res, err := client.ListVMs(opts)
	if err != nil {
		return nil, err
	}

	if len(res) > 1 {
		return nil, fmt.Errorf("error: found more than one (%v) vms with name %s", len(res), vmName)
	}

	if len(res) == 0 {
		return nil, nil
	}

	return FindVMByUUID(ctx, client, *res[0].ExtId)
}

// GetPEUUID returns the UUID of the Prism Element cluster with the given name
func GetPEUUID(ctx context.Context, client *prismclientfacadev4.FacadeV4Client, peName, peUUID *string) (string, error) {
	if client == nil {
		return "", fmt.Errorf("cannot retrieve Prism Element UUID if nutanix client is nil")
	}
	if peUUID == nil && peName == nil {
		return "", fmt.Errorf("cluster name or uuid must be passed in order to retrieve the Prism Element UUID")
	}
	if peUUID != nil && *peUUID != "" {
		peIntentResponse, err := client.GetCluster(*peUUID)
		if err != nil {
			if strings.Contains(fmt.Sprint(err), "ENTITY_NOT_FOUND") {
				return "", fmt.Errorf("failed to find Prism Element cluster with UUID %s: %v", *peUUID, err)
			}
			return "", fmt.Errorf("failed to get Prism Element cluster with UUID %s: %v", *peUUID, err)
		}
		return *peIntentResponse.ExtId, nil
	} else if peName != nil && *peName != "" {
		responsePEs, err := client.ListAllClusters(nil, nil, nil, nil)
		if err != nil {
			return "", err
		}
		// Validate filtered PEs
		foundPEs := make([]*clustermgmtconfig.Cluster, 0)
		for i := range responsePEs {
			pe := &responsePEs[i]
			// DOUBT av - correct service???
			if strings.EqualFold(*pe.Name, *peName) && hasPEClusterServiceEnabled(pe, clustermgmtconfig.CLUSTERFUNCTIONREF_AOS) {
				foundPEs = append(foundPEs, pe)
			}
		}
		if len(foundPEs) == 1 {
			return *foundPEs[0].ExtId, nil
		}
		if len(foundPEs) == 0 {
			return "", fmt.Errorf("failed to retrieve Prism Element cluster by name %s", *peName)
		} else {
			return "", fmt.Errorf("more than one Prism Element cluster found with name %s", *peName)
		}
	}
	return "", fmt.Errorf("failed to retrieve Prism Element cluster by name or uuid. Verify input parameters")
}

// GetBytesValueOfQuantity returns the given quantity value in Bytes
func GetBytesValueOfQuantity(quantity resource.Quantity) int64 {
	return quantity.Value()
}

// GetMibValueOfQuantity returns the given quantity value in Mib
func GetMibValueOfQuantity(quantity resource.Quantity) int64 {
	return quantity.Value() / (1024 * 1024)
}

func CreateSystemDiskSpec(osImageUUID string, systemDiskSize int64) (*vmmconfig.Disk, error) {
	if osImageUUID == "" {
		return nil, fmt.Errorf("image UUID must be set when creating system disk")
	}
	if systemDiskSize <= 0 {
		return nil, fmt.Errorf("invalid system disk size: %d. Provide in XXGi (for example 70Gi) format instead", systemDiskSize)
	}

	vmDisk := vmmconfig.NewVmDisk()
	vmDisk.DiskSizeBytes = &systemDiskSize

	imageRef := vmmconfig.NewImageReference()
	imageRef.ImageExtId = ptr.To(osImageUUID)
	vmDisk.DataSource = vmmconfig.NewDataSource()
	vmDisk.DataSource.SetReference(*imageRef)
	vmDisk.DataSource.ReferenceItemDiscriminator_ = nil // TODO av - if not set apicall was failing

	systemDisk := vmmconfig.NewDisk()
	systemDisk.SetBackingInfo(*vmDisk)

	return systemDisk, nil
}

// CreateDataDiskList creates a list of data disks with the given data disk specs
func CreateDataDiskList(ctx context.Context, client *prismclientfacadev4.FacadeV4Client, dataDiskSpecs []infrav1.NutanixMachineVMDisk, peUUID string) ([]vmmconfig.Disk, []vmmconfig.CdRom, error) {
	dataDisks := make([]vmmconfig.Disk, 0)
	cdRoms := make([]vmmconfig.CdRom, 0)

	latestDeviceIndexByAdapterType := make(map[string]int64)
	getDeviceIndex := func(adapterType string) int64 {
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

	adapterTypeToDiskBusTypeRef := func(adapterType infrav1.NutanixMachineDiskAdapterType) *vmmconfig.DiskBusType {
		switch adapterType {
		case infrav1.NutanixMachineDiskAdapterTypeSCSI:
			return vmmconfig.DISKBUSTYPE_SCSI.Ref()
		case infrav1.NutanixMachineDiskAdapterTypeIDE:
			return vmmconfig.DISKBUSTYPE_IDE.Ref()
		case infrav1.NutanixMachineDiskAdapterTypePCI:
			return vmmconfig.DISKBUSTYPE_PCI.Ref()
		case infrav1.NutanixMachineDiskAdapterTypeSATA:
			return vmmconfig.DISKBUSTYPE_SATA.Ref()
		case infrav1.NutanixMachineDiskAdapterTypeSPAPR:
			return vmmconfig.DISKBUSTYPE_SPAPR.Ref()
		default:
			return vmmconfig.DISKBUSTYPE_UNKNOWN.Ref()
		}
	}

	adapterTypeToCdRomBusTypeRef := func(adapterType infrav1.NutanixMachineDiskAdapterType) *vmmconfig.CdRomBusType {
		switch adapterType {
		case infrav1.NutanixMachineDiskAdapterTypeIDE:
			return vmmconfig.CDROMBUSTYPE_IDE.Ref()
		case infrav1.NutanixMachineDiskAdapterTypeSATA:
			return vmmconfig.CDROMBUSTYPE_SATA.Ref()
		default:
			return vmmconfig.CDROMBUSTYPE_UNKNOWN.Ref()
		}
	}

	for _, dataDiskSpec := range dataDiskSpecs {
		vmDisk := vmmconfig.NewVmDisk()
		vmDisk.DiskSizeBytes = ptr.To(GetBytesValueOfQuantity(dataDiskSpec.DiskSize))

		// If data source is provided, get the image UUID
		if dataDiskSpec.DataSource != nil {
			image, err := GetImage(ctx, client, infrav1.NutanixResourceIdentifier{
				UUID: dataDiskSpec.DataSource.UUID,
				Type: infrav1.NutanixIdentifierUUID,
			})
			if err != nil {
				return nil, nil, err
			}

			imageRef := vmmconfig.NewImageReference()
			imageRef.ImageExtId = image.ExtId
			vmDisk.DataSource = vmmconfig.NewDataSource()
			vmDisk.DataSource.SetReference(*imageRef)
			vmDisk.DataSource.ReferenceItemDiscriminator_ = nil // TODO av
		}

		if dataDiskSpec.StorageConfig != nil {
			flashModeEnabled := false
			if dataDiskSpec.StorageConfig.DiskMode == infrav1.NutanixMachineDiskModeFlash {
				flashModeEnabled = true
			}
			vmDisk.StorageConfig = vmmconfig.NewVmDiskStorageConfig()
			vmDisk.StorageConfig.IsFlashModeEnabled = &flashModeEnabled

			if dataDiskSpec.StorageConfig.StorageContainer != nil {
				peID := infrav1.NutanixResourceIdentifier{
					UUID: &peUUID,
					Type: infrav1.NutanixIdentifierUUID,
				}
				sc, err := GetStorageContainerInCluster(ctx, client, *dataDiskSpec.StorageConfig.StorageContainer, peID)
				if err != nil {
					return nil, nil, err
				}

				vmDisk.StorageContainer = vmmconfig.NewVmDiskContainerReference()
				vmDisk.StorageContainer.ExtId = sc.ExtId
			}
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
			deviceIndex = int64(dataDiskSpec.DeviceProperties.DeviceIndex)
		}

		if deviceType == infrav1.NutanixMachineDiskDeviceTypeDisk {
			dataDisk := vmmconfig.NewDisk()
			dataDisk.ExtId = vmDisk.DiskExtId
			dataDisk.DiskAddress = vmmconfig.NewDiskAddress()
			dataDisk.DiskAddress.BusType = adapterTypeToDiskBusTypeRef(adapterType)
			dataDisk.DiskAddress.Index = ptr.To(int(deviceIndex))

			dataDisk.SetBackingInfo(*vmDisk)
			dataDisks = append(dataDisks, *dataDisk)
		}
		if deviceType == infrav1.NutanixMachineDiskDeviceTypeCDRom {
			cdRom := vmmconfig.NewCdRom()
			cdRom.ExtId = vmDisk.DiskExtId
			cdRom.DiskAddress = vmmconfig.NewCdRomAddress()
			cdRom.DiskAddress.BusType = adapterTypeToCdRomBusTypeRef(adapterType)
			cdRom.DiskAddress.Index = ptr.To(int(deviceIndex))

			cdRom.BackingInfo = vmDisk
			cdRoms = append(cdRoms, *cdRom)
		}

	}
	return dataDisks, cdRoms, nil
}

// GetSubnetUUID returns the UUID of the subnet with the given name
func GetSubnetUUID(ctx context.Context, client *prismclientfacadev4.FacadeV4Client, peUUID string, subnetName, subnetUUID *string) (string, error) {
	var foundSubnetUUID string
	if subnetUUID == nil && subnetName == nil {
		return "", fmt.Errorf("subnet name or subnet uuid must be passed in order to retrieve the subnet")
	}
	if subnetUUID != nil {
		subnetIntentResponse, err := client.GetSubnet(*subnetUUID)
		if err != nil {
			if strings.Contains(fmt.Sprint(err), "ENTITY_NOT_FOUND") {
				return "", fmt.Errorf("failed to find subnet with UUID %s: %v", *subnetUUID, err)
			}
			return "", fmt.Errorf("failed to get subnet with UUID %s: %v", *subnetUUID, err)
		}
		foundSubnetUUID = *subnetIntentResponse.ExtId
	} else { // else search by name
		// Not using additional filtering since we want to list overlay and vlan subnets
		responseSubnets, err := client.ListAllSubnets(nil, nil, nil, nil)
		if err != nil {
			return "", err
		}
		// Validate filtered Subnets
		foundSubnets := make([]*networkingconfig.Subnet, 0)
		for _, subnet := range responseSubnets {
			if subnet.Name == nil || subnet.SubnetType == nil {
				continue
			}
			if strings.EqualFold(*subnet.Name, *subnetName) {
				if *subnet.SubnetType == networkingconfig.SUBNETTYPE_OVERLAY {
					// Overlay subnets are present on all PEs managed by PC.
					foundSubnets = append(foundSubnets, &subnet)
				} else {
					// By default check if the PE UUID matches if it is not an overlay subnet.
					if *subnet.ClusterReference == peUUID {
						foundSubnets = append(foundSubnets, &subnet)
					}
				}
			}
		}
		if len(foundSubnets) == 0 {
			return "", fmt.Errorf("failed to retrieve subnet by name %s", *subnetName)
		} else if len(foundSubnets) > 1 {
			return "", fmt.Errorf("more than one subnet found with name %s", *subnetName)
		} else {
			foundSubnetUUID = *foundSubnets[0].ExtId
		}
		if foundSubnetUUID == "" {
			return "", fmt.Errorf("failed to retrieve subnet by name or uuid. Verify input parameters")
		}
	}
	return foundSubnetUUID, nil
}

// GetImage returns an image. If no UUID is provided, returns the unique image with the name.
// Returns an error if no image has the UUID, if no image has the name, or more than one image has the name.
func GetImage(ctx context.Context, client *prismclientfacadev4.FacadeV4Client, id infrav1.NutanixResourceIdentifier) (*vmmcontent.Image, error) {
	switch {
	case id.IsUUID():
		resp, err := client.GetImage(*id.UUID)
		if err != nil {
			if strings.Contains(fmt.Sprint(err), "ENTITY_NOT_FOUND") {
				return nil, fmt.Errorf("failed to find image with UUID %s: %v", *id.UUID, err)
			}
			return nil, fmt.Errorf("failed to get image with UUID %s: %v", *id.UUID, err)
		}
		return resp, nil
	case id.IsName():
		allImages, err := client.ListAllImages(nil, nil, nil)
		if err != nil {
			return nil, err
		}
		// Validate filtered Images
		foundImages := make([]*vmmcontent.Image, 0)
		for i := range allImages {
			image := &allImages[i]
			if strings.EqualFold(*image.Name, *id.Name) {
				foundImages = append(foundImages, image)
			}
		}
		if len(foundImages) == 0 {
			return nil, fmt.Errorf("found no image with name %s", *id.Name)
		} else if len(foundImages) > 1 {
			return nil, fmt.Errorf("more than one image found with name %s", *id.Name)
		} else {
			return foundImages[0], nil
		}
	default:
		return nil, fmt.Errorf("image identifier is missing both name and uuid")
	}
}

type ImageLookup struct {
	BaseOS     string
	K8sVersion string
}

func GetImageByLookup(
	ctx context.Context,
	client *prismclientfacadev4.FacadeV4Client,
	imageTemplate,
	imageLookupBaseOS,
	k8sVersion *string,
) (*vmmcontent.Image, error) {
	if strings.Contains(*k8sVersion, "v") {
		k8sVersion = ptr.To(strings.Replace(*k8sVersion, "v", "", 1))
	}
	params := ImageLookup{*imageLookupBaseOS, *k8sVersion}
	t, err := template.New("k8sTemplate").Parse(*imageTemplate)
	if err != nil {
		return nil, fmt.Errorf("failed to parse template given %s %v", *imageTemplate, err)
	}
	var templateBytes bytes.Buffer
	err = t.Execute(&templateBytes, params)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to substitute string %s with params %v error: %w",
			*imageTemplate,
			params,
			err,
		)
	}
	allImages, err := client.ListAllImages(nil, nil, nil)
	if err != nil {
		return nil, err
	}
	re := regexp.MustCompile(templateBytes.String())
	foundImages := make([]*vmmcontent.Image, 0)
	for i := range allImages {
		image := &allImages[i]
		if re.Match([]byte(*image.Name)) {
			foundImages = append(foundImages, image)
		}
	}
	sorted := sortImagesByLatestCreationTime(foundImages)
	if len(sorted) == 0 {
		return nil, fmt.Errorf("failed to find image with filter %s", templateBytes.String())
	}
	return sorted[0], nil
}

// returns the images with the latest creation time first.
func sortImagesByLatestCreationTime(
	images []*vmmcontent.Image,
) []*vmmcontent.Image {
	sort.Slice(images, func(i, j int) bool {
		if images[i].CreateTime == nil || images[j].CreateTime == nil {
			return images[i].CreateTime != nil
		}
		timeI := *images[i].CreateTime
		timeJ := *images[j].CreateTime
		return timeI.After(timeJ)
	})
	return images
}

func ImageMarkedForDeletion(image *vmmcontent.Image) bool {
	state := *image.Type
	// DOUBT av - correct imagetypes checked??
	return state == vmmcontent.IMAGETYPE_REDACTED || state == vmmcontent.IMAGETYPE_UNKNOWN
}

// HasTaskInProgress returns true if the given task is in progress
func HasTaskInProgress(ctx context.Context, client *prismclientv3.Client, taskUUID string) (bool, error) {
	log := ctrl.LoggerFrom(ctx)
	taskStatus, err := nutanixclient.GetTaskStatus(ctx, client, taskUUID)
	if err != nil {
		return false, err
	}
	if taskStatus != taskSucceededMessage {
		log.V(1).Info(fmt.Sprintf("VM task with UUID %s still in progress: %s", taskUUID, taskStatus))
		return true, nil
	}
	return false, nil
}

// GetTaskUUIDFromVM returns the UUID of the task that created the VM with the given UUID
func GetTaskUUIDFromVM(vm *prismclientv3.VMIntentResponse) (string, error) {
	if vm == nil {
		return "", fmt.Errorf("cannot extract task uuid from empty vm object")
	}
	if vm.Status.ExecutionContext == nil {
		return "", nil
	}
	taskInterface := vm.Status.ExecutionContext.TaskUUID
	vmName := *vm.Spec.Name

	switch t := reflect.TypeOf(taskInterface).Kind(); t {
	case reflect.Slice:
		l := taskInterface.([]interface{})
		if len(l) != 1 {
			return "", fmt.Errorf("did not find expected amount of task UUIDs for VM %s", vmName)
		}
		return l[0].(string), nil
	case reflect.String:
		return taskInterface.(string), nil
	default:
		return "", fmt.Errorf("invalid type found for task uuid extracted from vm %s: %v", vmName, t)
	}
}

// GetSubnetUUIDList returns a list of subnet UUIDs for the given list of subnet names
func GetSubnetUUIDList(ctx context.Context, client *prismclientfacadev4.FacadeV4Client, machineSubnets []infrav1.NutanixResourceIdentifier, peUUID string) ([]string, error) {
	subnetUUIDs := make([]string, 0)
	for _, machineSubnet := range machineSubnets {
		subnetUUID, err := GetSubnetUUID(
			ctx,
			client,
			peUUID,
			machineSubnet.Name,
			machineSubnet.UUID,
		)
		if err != nil {
			return subnetUUIDs, err
		}
		subnetUUIDs = append(subnetUUIDs, subnetUUID)
	}
	return subnetUUIDs, nil
}

// GetDefaultCAPICategoryIdentifiers returns the default CAPI category identifiers
func GetDefaultCAPICategoryIdentifiers(clusterName string) []*infrav1.NutanixCategoryIdentifier {
	return []*infrav1.NutanixCategoryIdentifier{
		{
			Key:   infrav1.DefaultCAPICategoryKeyForName,
			Value: clusterName,
		},
	}
}

// GetObsoleteDefaultCAPICategoryIdentifiers returns the default CAPI category identifiers
func GetObsoleteDefaultCAPICategoryIdentifiers(clusterName string) []*infrav1.NutanixCategoryIdentifier {
	return []*infrav1.NutanixCategoryIdentifier{
		{
			Key:   fmt.Sprintf("%s%s", infrav1.ObsoleteDefaultCAPICategoryPrefix, clusterName),
			Value: infrav1.ObsoleteDefaultCAPICategoryOwnedValue,
		},
	}
}

// GetOrCreateCategories returns the list of category UUIDs for the given list of category names
func GetOrCreateCategories(ctx context.Context, client *prismclientv3.Client, categoryIdentifiers []*infrav1.NutanixCategoryIdentifier) ([]*prismclientv3.CategoryValueStatus, error) {
	categories := make([]*prismclientv3.CategoryValueStatus, 0)
	for _, ci := range categoryIdentifiers {
		if ci == nil {
			return categories, fmt.Errorf("cannot get or create nil category")
		}
		category, err := getOrCreateCategory(ctx, client, ci)
		if err != nil {
			return categories, err
		}
		categories = append(categories, category)
	}
	return categories, nil
}

func getCategoryKey(ctx context.Context, client *prismclientv3.Client, key string) (*prismclientv3.CategoryKeyStatus, error) {
	categoryKey, err := client.V3.GetCategoryKey(ctx, key)
	if err != nil {
		if !strings.Contains(fmt.Sprint(err), "ENTITY_NOT_FOUND") {
			return nil, fmt.Errorf("failed to retrieve category with key %s. error: %v", key, err)
		} else {
			return nil, nil
		}
	}
	return categoryKey, nil
}

func getCategoryValue(ctx context.Context, client *prismclientv3.Client, key, value string) (*prismclientv3.CategoryValueStatus, error) {
	categoryValue, err := client.V3.GetCategoryValue(ctx, key, value)
	if err != nil {
		if !strings.Contains(fmt.Sprint(err), "CATEGORY_NAME_VALUE_MISMATCH") {
			return nil, fmt.Errorf("failed to retrieve category value %s in category %s. error: %v", value, key, err)
		} else {
			return nil, nil
		}
	}
	return categoryValue, nil
}

func deleteCategoryKeyValues(ctx context.Context, client *prismclientv3.Client, categoryIdentifiers []*infrav1.NutanixCategoryIdentifier, ignoreKeyDeletion bool) error {
	log := ctrl.LoggerFrom(ctx)
	groupCategoriesByKey := make(map[string][]string, 0)
	for _, ci := range categoryIdentifiers {
		ciKey := ci.Key
		ciValue := ci.Value
		if gck, ok := groupCategoriesByKey[ciKey]; ok {
			groupCategoriesByKey[ciKey] = append(gck, ciValue)
			continue
		}

		groupCategoriesByKey[ciKey] = []string{ciValue}
	}

	for key, values := range groupCategoriesByKey {
		log.V(1).Info(fmt.Sprintf("Retrieving category with key %s", key))
		categoryKey, err := getCategoryKey(ctx, client, key)
		if err != nil {
			errorMsg := fmt.Errorf("failed to retrieve category with key %s. error: %v", key, err)
			log.Error(errorMsg, "failed to retrieve category")
			return errorMsg
		}
		log.V(1).Info(fmt.Sprintf("Category with key %s found. Starting deletion of values", key))
		if categoryKey == nil {
			log.V(1).Info(fmt.Sprintf("Category with key %s not found. Already deleted?", key))
			continue
		}
		for _, value := range values {
			categoryValue, err := getCategoryValue(ctx, client, key, value)
			if err != nil {
				errorMsg := fmt.Errorf("failed to retrieve category value %s in category %s. error: %v", value, key, err)
				log.Error(errorMsg, "failed to retrieve category value")
				return errorMsg
			}
			if categoryValue == nil {
				log.V(1).Info(fmt.Sprintf("Category with value %s in category %s not found. Already deleted?", value, key))
				continue
			}

			err = client.V3.DeleteCategoryValue(ctx, key, value)
			if err != nil {
				errorMsg := fmt.Errorf("failed to delete category value with key:value %s:%s. error: %v", key, value, err)
				log.Error(errorMsg, "failed to delete category value")
				// NCN-101935: If the category value still has VMs assigned, do not delete the category key:value
				// TODO:deepakmntnx Add a check for specific error mentioned in NCN-101935
				return nil
			}
		}

		if !ignoreKeyDeletion {
			// check if there are remaining category values
			categoryKeyValues, err := client.V3.ListCategoryValues(ctx, key, &prismclientv3.CategoryListMetadata{})
			if err != nil {
				errorMsg := fmt.Errorf("failed to get values of category with key %s: %v", key, err)
				log.Error(errorMsg, "failed to get values of category")
				return errorMsg
			}
			if len(categoryKeyValues.Entities) > 0 {
				errorMsg := fmt.Errorf("cannot remove category with key %s because it still has category values assigned", key)
				log.Error(errorMsg, "cannot remove category")
				return errorMsg
			}
			log.V(1).Info(fmt.Sprintf("No values assigned to category. Removing category with key %s", key))
			err = client.V3.DeleteCategoryKey(ctx, key)
			if err != nil {
				errorMsg := fmt.Errorf("failed to delete category with key %s: %v", key, err)
				log.Error(errorMsg, "failed to delete category")
				return errorMsg
			}
		}
	}
	return nil
}

// DeleteCategories deletes the given list of categories
func DeleteCategories(ctx context.Context, client *prismclientv3.Client, categoryIdentifiers, obsoleteCategoryIdentifiers []*infrav1.NutanixCategoryIdentifier) error {
	// Dont delete keys with newer format as key is constant string
	err := deleteCategoryKeyValues(ctx, client, categoryIdentifiers, true)
	if err != nil {
		return err
	}
	// Delete obsolete keys with older format to cleanup brownfield setups
	err = deleteCategoryKeyValues(ctx, client, obsoleteCategoryIdentifiers, false)
	if err != nil {
		return err
	}

	return nil
}

func getOrCreateCategory(ctx context.Context, client *prismclientv3.Client, categoryIdentifier *infrav1.NutanixCategoryIdentifier) (*prismclientv3.CategoryValueStatus, error) {
	log := ctrl.LoggerFrom(ctx)
	if categoryIdentifier == nil {
		return nil, fmt.Errorf("category identifier cannot be nil when getting or creating categories")
	}
	if categoryIdentifier.Key == "" {
		return nil, fmt.Errorf("category identifier key must be set when when getting or creating categories")
	}
	if categoryIdentifier.Value == "" {
		return nil, fmt.Errorf("category identifier key must be set when when getting or creating categories")
	}
	log.V(1).Info(fmt.Sprintf("Checking existence of category with key %s", categoryIdentifier.Key))
	categoryKey, err := getCategoryKey(ctx, client, categoryIdentifier.Key)
	if err != nil {
		errorMsg := fmt.Errorf("failed to retrieve category with key %s. error: %v", categoryIdentifier.Key, err)
		log.Error(errorMsg, "failed to retrieve category")
		return nil, errorMsg
	}
	if categoryKey == nil {
		log.V(1).Info(fmt.Sprintf("Category with key %s did not exist.", categoryIdentifier.Key))
		categoryKey, err = client.V3.CreateOrUpdateCategoryKey(ctx, &prismclientv3.CategoryKey{
			Description: utils.StringPtr(infrav1.DefaultCAPICategoryDescription),
			Name:        utils.StringPtr(categoryIdentifier.Key),
		})
		if err != nil {
			errorMsg := fmt.Errorf("failed to create category with key %s. error: %v", categoryIdentifier.Key, err)
			log.Error(errorMsg, "failed to create category")
			return nil, errorMsg
		}
	}
	categoryValue, err := getCategoryValue(ctx, client, *categoryKey.Name, categoryIdentifier.Value)
	if err != nil {
		errorMsg := fmt.Errorf("failed to retrieve category value %s in category %s. error: %v", categoryIdentifier.Value, categoryIdentifier.Key, err)
		log.Error(errorMsg, "failed to retrieve category")
		return nil, errorMsg
	}
	if categoryValue == nil {
		categoryValue, err = client.V3.CreateOrUpdateCategoryValue(ctx, *categoryKey.Name, &prismclientv3.CategoryValue{
			Description: utils.StringPtr(infrav1.DefaultCAPICategoryDescription),
			Value:       utils.StringPtr(categoryIdentifier.Value),
		})
		if err != nil {
			errorMsg := fmt.Errorf("failed to create category value %s in category key %s: %v", categoryIdentifier.Value, categoryIdentifier.Key, err)
			log.Error(errorMsg, "failed to create category value")
			return nil, errorMsg
		}
	}
	return categoryValue, nil
}

// GetCategoryVMSpec returns a flatmap of categories and their values
func GetCategoryVMSpec(ctx context.Context, client *prismclientv3.Client, categoryIdentifiers []*infrav1.NutanixCategoryIdentifier) (map[string]string, error) {
	log := ctrl.LoggerFrom(ctx)
	categorySpec := map[string]string{}
	for _, ci := range categoryIdentifiers {
		categoryValue, err := getCategoryValue(ctx, client, ci.Key, ci.Value)
		if err != nil {
			errorMsg := fmt.Errorf("error occurred while to retrieving category value %s in category %s. error: %v", ci.Value, ci.Key, err)
			log.Error(errorMsg, "failed to retrieve category")
			return nil, errorMsg
		}
		if categoryValue == nil {
			errorMsg := fmt.Errorf("category value %s not found in category %s. error", ci.Value, ci.Key)
			log.Error(errorMsg, "category value not found")
			return nil, errorMsg
		}
		categorySpec[ci.Key] = ci.Value
	}
	return categorySpec, nil
}

// GetProjectUUID returns the UUID of the project with the given name
func GetProjectUUID(ctx context.Context, client *prismclientv3.Client, projectName, projectUUID *string) (string, error) {
	var foundProjectUUID string
	if projectUUID == nil && projectName == nil {
		return "", fmt.Errorf("name or uuid must be passed in order to retrieve the project")
	}
	if projectUUID != nil {
		projectIntentResponse, err := client.V3.GetProject(ctx, *projectUUID)
		if err != nil {
			if strings.Contains(fmt.Sprint(err), "ENTITY_NOT_FOUND") {
				return "", fmt.Errorf("failed to find project with UUID %s: %v", *projectUUID, err)
			}
			return "", fmt.Errorf("failed to get project with UUID %s: %v", *projectUUID, err)
		}
		foundProjectUUID = *projectIntentResponse.Metadata.UUID
	} else { // else search by name
		responseProjects, err := client.V3.ListAllProject(ctx, "")
		if err != nil {
			return "", err
		}
		foundProjects := make([]*prismclientv3.Project, 0)
		for _, s := range responseProjects.Entities {
			projectSpec := s.Spec
			if strings.EqualFold(projectSpec.Name, *projectName) {
				foundProjects = append(foundProjects, s)
			}
		}
		if len(foundProjects) == 0 {
			return "", fmt.Errorf("failed to retrieve project by name %s", *projectName)
		} else if len(foundProjects) > 1 {
			return "", fmt.Errorf("more than one project found with name %s", *projectName)
		} else {
			foundProjectUUID = *foundProjects[0].Metadata.UUID
		}
		if foundProjectUUID == "" {
			return "", fmt.Errorf("failed to retrieve project by name or uuid. Verify input parameters")
		}
	}
	return foundProjectUUID, nil
}

func hasPEClusterServiceEnabled(peCluster *clustermgmtconfig.Cluster, serviceName clustermgmtconfig.ClusterFunctionRef) bool {
	if peCluster.Config == nil {
		return false
	}
	serviceList := peCluster.Config.ClusterFunction
	for _, s := range serviceList {
		if s == serviceName {
			return true
		}
	}
	return false
}

// GetGPUList returns a list of GPU device IDs for the given list of GPUs
func GetGPUList(ctx context.Context, client *prismclientv3.Client, gpus []infrav1.NutanixGPU, peUUID string) ([]vmmconfig.Gpu, error) {
	resultGPUs := make([]vmmconfig.Gpu, 0)
	for _, gpu := range gpus {
		foundGPU, err := GetGPU(ctx, client, peUUID, gpu)
		if err != nil {
			return nil, err
		}
		resultGPUs = append(resultGPUs, *foundGPU)
	}
	return resultGPUs, nil
}

// GetGPUDeviceID returns the device ID of a GPU with the given name
func GetGPU(ctx context.Context, client *prismclientv3.Client, peUUID string, gpu infrav1.NutanixGPU) (*vmmconfig.Gpu, error) {
	gpuDeviceID := gpu.DeviceID
	gpuDeviceName := gpu.Name
	if gpuDeviceID == nil && gpuDeviceName == nil {
		return nil, fmt.Errorf("gpu name or gpu device ID must be passed in order to retrieve the GPU")
	}
	allGPUs, err := GetGPUsForPE(ctx, client, peUUID)
	if err != nil {
		return nil, err
	}
	if len(allGPUs) == 0 {
		return nil, fmt.Errorf("no available GPUs found in Prism Element cluster with UUID %s", peUUID)
	}
	for _, peGPU := range allGPUs {
		if peGPU.Status != gpuUnused {
			continue
		}
		if (gpuDeviceID != nil && *peGPU.DeviceID == *gpuDeviceID) || (gpuDeviceName != nil && *gpuDeviceName == peGPU.Name) {
			gpu := vmmconfig.NewGpu()
			gpu.DeviceId = ptr.To(int(*peGPU.DeviceID))
			// TODO av
			gpu.Mode = vmmconfig.GPUMODE_VIRTUAL.Ref()
			gpu.Vendor = vmmconfig.GPUVENDOR_NVIDIA.Ref()
			return gpu, nil
		}
	}
	return nil, fmt.Errorf("no available GPU found in Prism Element that matches required GPU inputs")
}

func GetGPUsForPE(ctx context.Context, client *prismclientv3.Client, peUUID string) ([]*prismclientv3.GPU, error) {
	gpus := make([]*prismclientv3.GPU, 0)
	// TODO av
	hosts, err := client.V3.ListAllHost(ctx)
	if err != nil {
		return gpus, err
	}

	for _, host := range hosts.Entities {
		if host == nil ||
			host.Status == nil ||
			host.Status.ClusterReference == nil ||
			host.Status.Resources == nil ||
			len(host.Status.Resources.GPUList) == 0 ||
			host.Status.ClusterReference.UUID != peUUID {
			continue
		}

		for _, peGpu := range host.Status.Resources.GPUList {
			if peGpu == nil {
				continue
			}
			gpus = append(gpus, peGpu)
		}
	}
	return gpus, nil
}

// GetLegacyFailureDomainFromNutanixCluster gets the failure domain with a given name from a NutanixCluster object.
func GetLegacyFailureDomainFromNutanixCluster(failureDomainName string, nutanixCluster *infrav1.NutanixCluster) *infrav1.NutanixFailureDomainConfig { //nolint:staticcheck // suppress complaining on Deprecated type
	for _, fd := range nutanixCluster.Spec.FailureDomains { //nolint:staticcheck // suppress complaining on Deprecated field
		if fd.Name == failureDomainName {
			return &fd
		}
	}
	return nil
}

func GetStorageContainerByNtnxResourceIdentifier(ctx context.Context, client *prismclientfacadev4.FacadeV4Client, storageContainerIdentifier infrav1.NutanixResourceIdentifier) (*clustermgmtconfig.StorageContainer, error) {
	storageContainers, err := client.ListAllStorageContainers(nil, nil, nil)
	if err != nil {
		return nil, err
	}

	switch {
	case storageContainerIdentifier.IsUUID():
		for i := range storageContainers {
			sc := &storageContainers[i]
			if *sc.ExtId == *storageContainerIdentifier.UUID {
				return sc, nil
			}
		}

		return nil, fmt.Errorf("failed to find storage container %s", *storageContainerIdentifier.UUID)

	case storageContainerIdentifier.IsName():
		for i := range storageContainers {
			sc := &storageContainers[i]
			if *sc.Name == *storageContainerIdentifier.Name {
				return sc, nil
			}
		}

		return nil, fmt.Errorf("failed to find storage container %s", *storageContainerIdentifier.Name)

	default:
		return nil, fmt.Errorf("storage container identifier is missing both name and uuid")
	}
}

func GetStorageContainerInCluster(ctx context.Context, client *prismclientfacadev4.FacadeV4Client, storageContainerIdentifier, clusterIdentifier infrav1.NutanixResourceIdentifier) (*clustermgmtconfig.StorageContainer, error) {
	storageContainers, err := client.ListAllStorageContainers(nil, nil, nil)
	if err != nil {
		return nil, err
	}

	switch {
	case storageContainerIdentifier.IsUUID():
		return getSCinClusterByUUID(storageContainers, storageContainerIdentifier, clusterIdentifier)

	case storageContainerIdentifier.IsName():
		return getSCinClusterByName(storageContainers, storageContainerIdentifier, clusterIdentifier)

	default:
		return nil, fmt.Errorf("storage container identifier is missing both name and uuid")
	}
}

func getSCinClusterByName(storageContainers []clustermgmtconfig.StorageContainer, storageContainerIdentifier infrav1.NutanixResourceIdentifier, clusterIdentifier infrav1.NutanixResourceIdentifier) (*clustermgmtconfig.StorageContainer, error) {
	for i := range storageContainers {
		sc := &storageContainers[i]
		if strings.EqualFold(*sc.Name, *storageContainerIdentifier.Name) {
			if clusterIdentifier.IsUUID() {
				if *sc.ClusterExtId == *clusterIdentifier.UUID {
					return sc, nil
				}
			} else if clusterIdentifier.IsName() {
				if *sc.ClusterName == *clusterIdentifier.Name {
					return sc, nil
				}
			} else {
				return nil, fmt.Errorf("cluster identifier is missing both name and uuid")
			}
		}
	}

	return nil, fmt.Errorf("failed to find storage container %s for cluster %v", *storageContainerIdentifier.Name, clusterIdentifier)
}

func getSCinClusterByUUID(storageContainers []clustermgmtconfig.StorageContainer, storageContainerIdentifier infrav1.NutanixResourceIdentifier, clusterIdentifier infrav1.NutanixResourceIdentifier) (*clustermgmtconfig.StorageContainer, error) {
	for i := range storageContainers {
		sc := &storageContainers[i]
		if *sc.ExtId == *storageContainerIdentifier.UUID {
			if clusterIdentifier.IsUUID() {
				if *sc.ClusterExtId == *clusterIdentifier.UUID {
					return sc, nil
				}
			} else if clusterIdentifier.IsName() {
				if *sc.ClusterName == *clusterIdentifier.Name {
					return sc, nil
				}
			} else {
				return nil, fmt.Errorf("cluster identifier is missing both name and uuid")
			}
		}
	}

	return nil, fmt.Errorf("failed to find storage container %s for cluster %v", *storageContainerIdentifier.UUID, clusterIdentifier)
}

func getPrismCentralClientForCluster(ctx context.Context, cluster *infrav1.NutanixCluster, secretInformer v1.SecretInformer, mapInformer v1.ConfigMapInformer) (*prismclientv3.Client, error) {
	log := ctrl.LoggerFrom(ctx)

	log.V(1).Info("Get client helper")
	clientHelper := nutanixclient.NewHelper(secretInformer, mapInformer)

	log.V(1).Info("Build management endpoint")
	managementEndpoint, err := clientHelper.BuildManagementEndpoint(ctx, cluster)
	if err != nil {
		log.Error(err, fmt.Sprintf("error occurred while getting management endpoint for cluster %q", cluster.GetNamespacedName()))
		conditions.MarkFalse(cluster, infrav1.PrismCentralClientCondition, infrav1.PrismCentralClientInitializationFailed, capiv1.ConditionSeverityError, "%s", err.Error())
		return nil, err
	}

	log.V(1).Info("Get or create prism central client v3")
	v3Client, err := nutanixclient.NutanixClientCache.GetOrCreate(&nutanixclient.CacheParams{
		NutanixCluster:          cluster,
		PrismManagementEndpoint: managementEndpoint,
	})
	if err != nil {
		log.Error(err, "error occurred while getting nutanix prism v3 Client from cache")
		conditions.MarkFalse(cluster, infrav1.PrismCentralClientCondition, infrav1.PrismCentralClientInitializationFailed, capiv1.ConditionSeverityError, "%s", err.Error())
		return nil, fmt.Errorf("nutanix prism v3 Client error: %w", err)
	}

	conditions.MarkTrue(cluster, infrav1.PrismCentralClientCondition)
	return v3Client, nil
}

func getPrismCentralV4ClientForCluster(ctx context.Context, cluster *infrav1.NutanixCluster, secretInformer v1.SecretInformer, mapInformer v1.ConfigMapInformer) (*prismclientv4.Client, error) {
	log := ctrl.LoggerFrom(ctx)

	clientHelper := nutanixclient.NewHelper(secretInformer, mapInformer)
	managementEndpoint, err := clientHelper.BuildManagementEndpoint(ctx, cluster)
	if err != nil {
		log.Error(err, fmt.Sprintf("error occurred while getting management endpoint for cluster %q", cluster.GetNamespacedName()))
		conditions.MarkFalse(cluster, infrav1.PrismCentralV4ClientCondition, infrav1.PrismCentralV4ClientInitializationFailed, capiv1.ConditionSeverityError, "%s", err.Error())
		return nil, err
	}

	client, err := nutanixclient.NutanixClientCacheV4.GetOrCreate(&nutanixclient.CacheParams{
		NutanixCluster:          cluster,
		PrismManagementEndpoint: managementEndpoint,
	})
	if err != nil {
		log.Error(err, "error occurred while getting nutanix prism v4 client from cache")
		conditions.MarkFalse(cluster, infrav1.PrismCentralV4ClientCondition, infrav1.PrismCentralV4ClientInitializationFailed, capiv1.ConditionSeverityError, "%s", err.Error())
		return nil, fmt.Errorf("nutanix prism v4 client error: %w", err)
	}

	conditions.MarkTrue(cluster, infrav1.PrismCentralV4ClientCondition)
	return client, nil
}

func detachVolumeGroupsFromVM(ctx context.Context, v4Client *prismclientv4.Client, vmName string, vmUUID string, vmDiskList []*prismclientv3.VMDisk) error {
	log := ctrl.LoggerFrom(ctx)
	volumeGroupsToDetach := make([]string, 0)
	for _, disk := range vmDiskList {
		if disk.VolumeGroupReference == nil {
			continue
		}

		volumeGroupsToDetach = append(volumeGroupsToDetach, *disk.VolumeGroupReference.UUID)
	}

	// Detach the volume groups from the virtual machine
	for _, volumeGroup := range volumeGroupsToDetach {
		log.Info(fmt.Sprintf("detaching volume group %s from virtual machine %s", volumeGroup, vmName))
		body := &volumesconfig.VmAttachment{
			ExtId: ptr.To(vmUUID),
		}

		resp, err := v4Client.VolumeGroupsApiInstance.DetachVm(&volumeGroup, body)
		if err != nil {
			return fmt.Errorf("failed to detach volume group %s from virtual machine %s: %w", volumeGroup, vmUUID, err)
		}

		data := resp.GetData()
		if _, ok := data.(prismconfig.TaskReference); !ok {
			return fmt.Errorf("failed to cast response to TaskReference")
		}
	}

	return nil
}

func resourceIdsEquals(nris1, nris2 []infrav1.NutanixResourceIdentifier) bool {
	if nris1 == nil && nris2 == nil {
		return true
	}
	if (nris1 == nil && nris2 != nil) ||
		(nris1 != nil && nris2 == nil) ||
		len(nris1) != len(nris2) {
		return false
	}

	for i := range nris1 {
		found := false
		for j := range nris2 {
			if nris1[i].EqualTo(&nris2[j]) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	return true
}
