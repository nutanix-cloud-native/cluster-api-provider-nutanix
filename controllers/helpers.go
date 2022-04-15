/*
Copyright 2021.

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
	"fmt"
	"reflect"
	"strings"

	infrav1 "github.com/nutanix-core/cluster-api-provider-nutanix/api/v1beta1"
	nutanixClient "github.com/nutanix-core/cluster-api-provider-nutanix/pkg/client"
	nutanixClientV3 "github.com/nutanix-core/cluster-api-provider-nutanix/pkg/nutanix/v3"
	"github.com/nutanix-core/cluster-api-provider-nutanix/pkg/utils"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/klog/v2"
)

const (
	taskSucceededMessage = "SUCCEEDED"
)

// deleteVM deletes a VM and is invoked by the NutanixMachineReconciler
func deleteVM(client *nutanixClientV3.Client, vmName, vmUUID string) (string, error) {
	var err error

	if vmUUID == "" {
		klog.Warning(fmt.Sprintf("VmUUID was empty. Skipping delete"))
		return "", nil
	}

	klog.Infof("Deleting VM %s with UUID: %s", vmName, vmUUID)
	vmDeleteResponse, err := client.V3.DeleteVM(vmUUID)
	if err != nil {
		klog.Infof("Error deleting machine %s", vmName)
		return "", err
	}
	deleteTaskUUID := vmDeleteResponse.Status.ExecutionContext.TaskUUID.(string)

	return deleteTaskUUID, nil
}

// findVMByUUID retrieves the VM with the given vm UUID. Returns nil if not found
func findVMByUUID(client *nutanixClientV3.Client, uuid string) (*nutanixClientV3.VMIntentResponse, error) {

	klog.Infof("Checking if VM with UUID %s exists.", uuid)

	response, err := client.V3.GetVM(uuid)
	if err != nil {
		if strings.Contains(fmt.Sprint(err), "ENTITY_NOT_FOUND") {
			klog.Infof("vm with uuid %s does not exist.", uuid)
			return nil, nil
		} else {
			klog.Errorf("Failed to find VM by vmUUID %s. error: %v", uuid, err)
			return nil, err
		}
	}

	return response, nil
}

func findVM(client *nutanixClientV3.Client, nutanixMachine *infrav1.NutanixMachine) (*nutanixClientV3.VMIntentResponse, error) {
	vmName := nutanixMachine.Name
	vmUUID := nutanixMachine.Status.VmUUID
	// Search via uuid if it is present
	if vmUUID != "" {
		klog.Info("Searching for VM %s using UUID %s", vmName, vmUUID)
		vm, err := findVMByUUID(client, nutanixMachine.Status.VmUUID)
		if err != nil {
			klog.Errorf("error occurred finding VM with uuid %s: %v", nutanixMachine.Status.VmUUID, err)
			return nil, err
		}
		if vm == nil {
			errorMsg := fmt.Sprintf("no vm %s found with UUID %s but was expected to be present", vmName, vmUUID)
			klog.Error(errorMsg)
			return nil, fmt.Errorf(errorMsg)
		}
		return vm, nil
		// otherwise search via name
	} else {
		klog.Infof("Searching for VM %s using name", vmName)
		vm, err := findVMByName(client, vmName)
		if err != nil {
			klog.Errorf("error occurred finding VM %s by name: %v", vmName, err)
			return nil, err
		}
		return vm, nil
	}
}

// findVMByName retrieves the VM with the given vm name
func findVMByName(client *nutanixClientV3.Client, vmName string) (*nutanixClientV3.VMIntentResponse, error) {
	klog.Infof("Checking if VM with name %s exists.", vmName)

	res, err := client.V3.ListVM(&nutanixClientV3.DSMetadata{
		Filter: utils.StringPtr(fmt.Sprintf("vm_name==%s", vmName))})
	if err != nil {
		errorMsg := fmt.Errorf("error occurred when searching for VM by name %s. error: %v", vmName, err)
		klog.Error(errorMsg)
		return nil, errorMsg
	}

	if len(res.Entities) > 1 {
		errorMsg := fmt.Sprintf("Found more than one (%v) vms with name %s.", len(res.Entities), vmName)
		klog.Errorf(errorMsg)
		return nil, fmt.Errorf(errorMsg)
	}

	if len(res.Entities) == 0 {
		return nil, nil
	}

	return findVMByUUID(client, *res.Entities[0].Metadata.UUID)
}

func getPEUUID(client *nutanixClientV3.Client, peName, peUUID *string) (string, error) {
	var foundPEUUID string
	if peUUID == nil && peName == nil {
		return "", fmt.Errorf("cluster name or uuid must be passed in order to retrieve the pe")
	}
	if peUUID != nil {
		peIntentResponse, err := client.V3.GetCluster(*peUUID)
		if err != nil {
			if strings.Contains(fmt.Sprint(err), "ENTITY_NOT_FOUND") {
				return "", fmt.Errorf("failed to find Prism Element cluster with UUID %s: %v", *peUUID, err)
			}
		}
		foundPEUUID = *peIntentResponse.Metadata.UUID
	} else if peName != nil {

		responsePEs, err := client.V3.ListAllCluster()
		if err != nil {
			return "", err
		}
		foundPEs := make([]*nutanixClientV3.ClusterIntentResource, 0)
		for _, s := range responsePEs.Entities {
			peSpec := s.Spec
			if *peSpec.Name == *peName {
				foundPEs = append(foundPEs, s)
			}
		}
		if len(foundPEs) == 0 {
			return "", fmt.Errorf("failed to retrieve Prism Element cluster by name %s", *peName)
		} else if len(foundPEs) > 1 {
			return "", fmt.Errorf("more than one Prism Element cluster found with name %s", *peName)
		} else {
			foundPEUUID = *foundPEs[0].Metadata.UUID
		}
		if foundPEUUID == "" {
			return "", fmt.Errorf("failed to retrieve Prism Element cluster by name or uuid. Verify input parameters.")
		}
	}
	return foundPEUUID, nil
}

// getMibValueOfQuantity returns the given quantity value in Mib
func getMibValueOfQuantity(quantity resource.Quantity) int64 {
	return quantity.Value() / (1024 * 1024)
}

func createSystemDiskSpec(imageUUID string, systemDiskSize int64) (*nutanixClientV3.VMDisk, error) {
	if imageUUID == "" {
		return nil, fmt.Errorf("image UUID must be set when creating system disk")
	}
	if systemDiskSize <= 0 {
		return nil, fmt.Errorf("Invalid system disk size: %d. Provide in XXGi (for example 70Gi) format instead", systemDiskSize)
	}
	systemDisk := &nutanixClientV3.VMDisk{
		DataSourceReference: &nutanixClientV3.Reference{
			Kind: utils.StringPtr("image"),
			UUID: utils.StringPtr(imageUUID),
		},
		DiskSizeMib: utils.Int64Ptr(systemDiskSize)}
	return systemDisk, nil

}

func getSubnetUUID(client *nutanixClientV3.Client, peUUID string, subnetName, subnetUUID *string) (string, error) {
	var foundSubnetUUID string
	if subnetUUID == nil && subnetName == nil {
		return "", fmt.Errorf("subnet name or subnet uuid must be passed in order to retrieve the subnet")
	}
	if subnetUUID != nil {
		subnetIntentResponse, err := client.V3.GetSubnet(*subnetUUID)
		if err != nil {
			if strings.Contains(fmt.Sprint(err), "ENTITY_NOT_FOUND") {
				return "", fmt.Errorf("failed to find subnet with UUID %s: %v", *subnetUUID, err)
			}
		}
		foundSubnetUUID = *subnetIntentResponse.Metadata.UUID
	} else if subnetName != nil {

		responseSubnets, err := client.V3.ListAllSubnet()
		if err != nil {
			return "", err
		}
		foundSubnets := make([]*nutanixClientV3.SubnetIntentResponse, 0)
		for _, s := range responseSubnets.Entities {
			subnetSpec := s.Spec
			if *subnetSpec.Name == *subnetName && *subnetSpec.ClusterReference.UUID == peUUID {
				foundSubnets = append(foundSubnets, s)
			}
		}
		if len(foundSubnets) == 0 {
			return "", fmt.Errorf("failed to retrieve subnet by name %s", *subnetName)
		} else if len(foundSubnets) > 1 {
			return "", fmt.Errorf("more than one subnet found with name %s", *subnetName)
		} else {
			foundSubnetUUID = *foundSubnets[0].Metadata.UUID
		}
		if foundSubnetUUID == "" {
			return "", fmt.Errorf("failed to retrieve subnet by name or uuid. Verify input parameters.")
		}
	}
	return foundSubnetUUID, nil
}

func getImageUUID(client *nutanixClientV3.Client, imageName, imageUUID *string) (string, error) {
	var foundImageUUID string

	if imageUUID == nil && imageName == nil {
		return "", fmt.Errorf("image name or image uuid must be passed in order to retrieve the image")
	}
	if imageUUID != nil {
		imageIntentResponse, err := client.V3.GetImage(*imageUUID)
		if err != nil {
			if strings.Contains(fmt.Sprint(err), "ENTITY_NOT_FOUND") {
				return "", fmt.Errorf("failed to find image with UUID %s: %v", *imageUUID, err)
			}
		}
		foundImageUUID = *imageIntentResponse.Metadata.UUID
	} else if imageName != nil {
		responseImages, err := client.V3.ListAllImage()
		if err != nil {
			return "", err
		}
		foundImages := make([]*nutanixClientV3.ImageIntentResponse, 0)
		for _, s := range responseImages.Entities {
			imageSpec := s.Spec
			if *imageSpec.Name == *imageName {
				foundImages = append(foundImages, s)
			}
		}
		if len(foundImages) == 0 {
			return "", fmt.Errorf("failed to retrieve image by name %s", *imageName)
		} else if len(foundImages) > 1 {
			return "", fmt.Errorf("more than one image found with name %s", *imageName)
		} else {
			foundImageUUID = *foundImages[0].Metadata.UUID
		}
		if foundImageUUID == "" {
			return "", fmt.Errorf("failed to retrieve image by name or uuid. Verify input parameters.")
		}
	}
	return foundImageUUID, nil
}

func isExistingVM(client *nutanixClientV3.Client, vmUUID string) (bool, error) {
	vm, err := findVMByUUID(client, vmUUID)
	if err != nil {
		errorMsg := fmt.Errorf("error finding vm with uuid %s: %v", vmUUID, err)
		klog.Error(errorMsg)
		return false, errorMsg
	}

	return vm == nil, nil

}

func hasTaskInProgress(client *nutanixClientV3.Client, taskUUID string) (bool, error) {
	taskStatus, err := nutanixClient.GetTaskState(client, taskUUID)
	if err != nil {
		return false, err
	}
	if taskStatus != taskSucceededMessage {
		klog.Infof("VM task with UUID %s still in progress: %s. Requeuing", taskUUID, taskStatus)
		return true, nil
	}
	return false, nil
}

func getTaskUUIDFromVM(vm *nutanixClientV3.VMIntentResponse) (string, error) {
	if vm == nil {
		return "", fmt.Errorf("cannot extract task uuid from empty vm object")
	}
	taskInterface := vm.Status.ExecutionContext.TaskUUID
	vmName := *vm.Spec.Name

	switch t := reflect.TypeOf(taskInterface).Kind(); t {
	case reflect.Slice:
		l := taskInterface.([]interface{})
		if len(l) != 1 {
			return "", fmt.Errorf("Did not find expected amount of task UUIDs for VM %s", vmName)
		}
		return l[0].(string), nil
	case reflect.String:
		return taskInterface.(string), nil
	default:
		return "", fmt.Errorf("Invalid type found for task uuid extracted from vm %s: %v", vmName, t)
	}
}

func getSubnetUUIDList(client *nutanixClientV3.Client, machineSubnets []infrav1.NutanixResourceIdentifier, peUUID string) ([]string, error) {
	subnetUUIDs := make([]string, 0)
	for _, machineSubnet := range machineSubnets {
		subnetUUID, err := getSubnetUUID(
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

func getDefaultCAPICategoryIdentifiers(clusterName string) []*infrav1.NutanixCategoryIdentifier {
	return []*infrav1.NutanixCategoryIdentifier{
		&infrav1.NutanixCategoryIdentifier{
			Key:   fmt.Sprintf("%s%s", infrav1.DefaultCAPICategoryPrefix, clusterName),
			Value: infrav1.DefaultCAPICategoryOwnedValue,
		},
	}
}

func getOrCreateCategories(client *nutanixClientV3.Client, categoryIdentifiers []*infrav1.NutanixCategoryIdentifier) ([]*nutanixClientV3.CategoryValueStatus, error) {
	categories := make([]*nutanixClientV3.CategoryValueStatus, 0)
	for _, ci := range categoryIdentifiers {
		if ci == nil {
			return categories, fmt.Errorf("cannot get or create nil category")
		}
		category, err := getOrCreateCategory(client, ci)
		if err != nil {
			return categories, err
		}
		categories = append(categories, category)
	}
	return categories, nil
}

func getCategoryKey(client *nutanixClientV3.Client, key string) (*nutanixClientV3.CategoryKeyStatus, error) {
	categoryKey, err := client.V3.GetCategoryKey(key)
	if err != nil {
		if !strings.Contains(fmt.Sprint(err), "ENTITY_NOT_FOUND") {
			errorMsg := fmt.Errorf("Failed to retrieve category with key %s. error: %v", key, err)
			klog.Error(errorMsg)
			return nil, errorMsg
		} else {
			return nil, nil
		}
	}
	return categoryKey, nil
}

func getCategoryValue(client *nutanixClientV3.Client, key, value string) (*nutanixClientV3.CategoryValueStatus, error) {
	categoryValue, err := client.V3.GetCategoryValue(key, value)
	if err != nil {
		if !strings.Contains(fmt.Sprint(err), "CATEGORY_NAME_VALUE_MISMATCH") {
			errorMsg := fmt.Errorf("Failed to retrieve category value %s in category %s. error: %v", value, key, err)
			klog.Error(errorMsg)
			return nil, errorMsg
		} else {
			return nil, nil
		}
	}
	return categoryValue, nil
}

func deleteCategories(client *nutanixClientV3.Client, categoryIdentifiers []*infrav1.NutanixCategoryIdentifier) error {
	groupCategoriesByKey := make(map[string][]string, 0)
	for _, ci := range categoryIdentifiers {
		ciKey := ci.Key
		ciValue := ci.Value
		if gck, ok := groupCategoriesByKey[ciKey]; ok {
			groupCategoriesByKey[ciKey] = append(gck, ciValue)
		} else {
			groupCategoriesByKey[ciKey] = []string{ciValue}
		}
	}

	for key, values := range groupCategoriesByKey {
		klog.Info("Retrieving category with key %s", key)
		categoryKey, err := getCategoryKey(client, key)
		if err != nil {
			errorMsg := fmt.Errorf("Failed to retrieve category with key %s. error: %v", key, err)
			klog.Error(errorMsg)
			return errorMsg
		}
		klog.Info("Category with key %s found. Starting deletion of values")
		if categoryKey == nil {
			klog.Info("Category with key %s not found. Already deleted?", key)
			continue
		}
		for _, value := range values {
			categoryValue, err := getCategoryValue(client, key, value)
			if err != nil {
				errorMsg := fmt.Errorf("Failed to retrieve category value %s in category %s. error: %v", value, key, err)
				klog.Error(errorMsg)
				return errorMsg
			}
			if categoryValue == nil {
				klog.Info("Category with value %s in category not found. Already deleted?", value, key)
				continue
			}
			client.V3.DeleteCategoryValue(key, value)
		}
		// check if there are remaining category values
		categoryKeyValues, err := client.V3.ListCategoryValues(key, &nutanixClientV3.CategoryListMetadata{})
		if err != nil {
			errorMsg := fmt.Errorf("failed to get values of category with key %s: %v", key, err)
			klog.Error(errorMsg)
			return errorMsg
		}
		if len(categoryKeyValues.Entities) > 0 {
			errorMsg := fmt.Errorf("cannot remove category with key %s because it still has category values assigned", key)
			klog.Error(errorMsg)
			return errorMsg
		}
		klog.Infof("No values assigned to category. Removing category with key %s", key)
		err = client.V3.DeleteCategoryKey(key)
		if err != nil {
			errorMsg := fmt.Errorf("failed to delete category with key %s: %v", key, err)
			klog.Error(errorMsg)
			return errorMsg
		}
	}
	return nil
}

func getOrCreateCategory(client *nutanixClientV3.Client, categoryIdentifier *infrav1.NutanixCategoryIdentifier) (*nutanixClientV3.CategoryValueStatus, error) {
	if categoryIdentifier == nil {
		return nil, fmt.Errorf("category identifier cannot be nil when getting or creating categories")
	}
	if categoryIdentifier.Key == "" {
		return nil, fmt.Errorf("category identifier key must be set when when getting or creating categories")
	}
	if categoryIdentifier.Value == "" {
		return nil, fmt.Errorf("category identifier key must be set when when getting or creating categories")
	}
	klog.Infof("Checking existence of category with key %s", categoryIdentifier.Key)
	categoryKey, err := getCategoryKey(client, categoryIdentifier.Key)
	if err != nil {
		errorMsg := fmt.Errorf("Failed to retrieve category with key %s. error: %v", categoryIdentifier.Key, err)
		klog.Error(errorMsg)
		return nil, errorMsg
	}
	if categoryKey == nil {
		klog.Infof("Category with key %s did not exist.", categoryIdentifier.Key)
		categoryKey, err = client.V3.CreateOrUpdateCategoryKey(&nutanixClientV3.CategoryKey{
			Description: utils.StringPtr(infrav1.DefaultCAPICategoryDescription),
			Name:        utils.StringPtr(categoryIdentifier.Key),
		})
		if err != nil {
			errorMsg := fmt.Errorf("Failed to create category with key %s. error: %v", categoryIdentifier.Key, err)
			klog.Error(errorMsg)
			return nil, errorMsg
		}
	}
	categoryValue, err := getCategoryValue(client, *categoryKey.Name, categoryIdentifier.Value)
	if err != nil {
		errorMsg := fmt.Errorf("Failed to retrieve category value %s in category %s. error: %v", categoryIdentifier.Value, categoryIdentifier.Key, err)
		klog.Error(errorMsg)
		return nil, errorMsg
	}
	if categoryValue == nil {
		categoryValue, err = client.V3.CreateOrUpdateCategoryValue(*categoryKey.Name, &nutanixClientV3.CategoryValue{
			Description: utils.StringPtr(infrav1.DefaultCAPICategoryDescription),
			Value:       utils.StringPtr(categoryIdentifier.Value),
		})
		if err != nil {
			klog.Errorf("Failed to create category value %s in category key %s. error: %v", categoryIdentifier.Value, categoryIdentifier.Key, err)
		}
	}
	return categoryValue, nil
}

func getCategoryVMSpec(client *nutanixClientV3.Client, categoryIdentifiers []*infrav1.NutanixCategoryIdentifier) (map[string]string, error) {
	categorySpec := map[string]string{}
	for _, ci := range categoryIdentifiers {
		categoryValue, err := getCategoryValue(client, ci.Key, ci.Value)
		if err != nil {
			errorMsg := fmt.Errorf("Error occurred while to retrieving category value %s in category %s. error: %v", ci.Value, ci.Key, err)
			klog.Error(errorMsg)
			return nil, errorMsg
		}
		if categoryValue == nil {
			errorMsg := fmt.Errorf("Category value %s not found in category %s. error", ci.Value, ci.Key)
			klog.Error(errorMsg)
			return nil, errorMsg
		}
		categorySpec[ci.Key] = ci.Value
	}
	return categorySpec, nil
}
