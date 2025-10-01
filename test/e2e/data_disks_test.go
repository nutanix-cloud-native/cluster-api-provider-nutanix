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

package e2e

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/cluster-api/test/framework/clusterctl"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
)

var _ = Describe("Nutanix machine data disks", Label("nutanix-feature-test", "data-disks"), func() {
	const specName = "cluster-data-disks"

	var (
		namespace        *corev1.Namespace
		clusterName      string
		clusterResources *clusterctl.ApplyClusterTemplateAndWaitResult
		cancelWatches    context.CancelFunc
		testHelper       testHelperInterface
		dataDisks        []infrav1.NutanixMachineVMDisk
	)

	BeforeEach(func() {
		testHelper = newTestHelper(e2eConfig)
		clusterName = testHelper.generateTestClusterName(specName)
		clusterResources = new(clusterctl.ApplyClusterTemplateAndWaitResult)
		Expect(bootstrapClusterProxy).NotTo(BeNil(), "BootstrapCluterProxy can't be nil")
		namespace, cancelWatches = setupSpecNamespace(ctx, specName, bootstrapClusterProxy, artifactFolder)
	})

	AfterEach(func() {
		dumpSpecResourcesAndCleanup(ctx,
			specName, bootstrapClusterProxy,
			artifactFolder, namespace,
			cancelWatches, clusterResources.Cluster,
			e2eConfig.GetIntervals, skipCleanup)
	})

	It("Should create a cluster with data disks successfully", func() {
		const flavor = "no-nmt"

		Expect(namespace).NotTo(BeNil(), "Namespace can't be nil")

		dataDisks = []infrav1.NutanixMachineVMDisk{
			{
				DiskSize: resource.MustParse("10Gi"),
				DeviceProperties: &infrav1.NutanixMachineVMDiskDeviceProperties{
					DeviceType:  infrav1.NutanixMachineDiskDeviceTypeDisk,
					AdapterType: infrav1.NutanixMachineDiskAdapterTypeSCSI,
				},
			},
		}

		By("Creating a Nutanix Machine Config with data disks", func() {
			dataDiskNMT := testHelper.createDefaultNMTwithDataDisks(clusterName, namespace.Name, withDataDisksParams{
				DataDisks: dataDisks,
			})

			testHelper.createCapiObject(ctx, createCapiObjectParams{
				creator:    bootstrapClusterProxy.GetClient(),
				capiObject: dataDiskNMT,
			})
		})

		By("Creating a workload cluster", func() {
			testHelper.deployClusterAndWait(deployClusterParams{
				clusterName:           clusterName,
				namespace:             namespace,
				flavor:                flavor,
				clusterctlConfigPath:  clusterctlConfigPath,
				artifactFolder:        artifactFolder,
				bootstrapClusterProxy: bootstrapClusterProxy,
			}, clusterResources)
		})

		By("Checking the data disks are attached to the VMs", func() {
			testHelper.verifyDisksOnNutanixMachines(ctx, verifyDisksOnNutanixMachinesParams{
				clusterName:           clusterName,
				namespace:             namespace.Name,
				bootstrapClusterProxy: bootstrapClusterProxy,
				diskCount:             3,
			})
		})

		By("PASSED!")
	})

	It("Should failed to create a cluster with wrong data disk storage container", func() {
		const flavor = "no-nmt"

		Expect(namespace).NotTo(BeNil(), "Namespace can't be nil")

		dataDisks = []infrav1.NutanixMachineVMDisk{
			{
				DiskSize: resource.MustParse("10Gi"),
				DeviceProperties: &infrav1.NutanixMachineVMDiskDeviceProperties{
					DeviceType:  infrav1.NutanixMachineDiskDeviceTypeDisk,
					AdapterType: infrav1.NutanixMachineDiskAdapterTypeSCSI,
				},
				StorageConfig: &infrav1.NutanixMachineVMStorageConfig{
					DiskMode: infrav1.NutanixMachineDiskModeStandard,
					StorageContainer: &infrav1.NutanixResourceIdentifier{
						Type: infrav1.NutanixIdentifierUUID,
						UUID: ptr.To("01010101-0101-0101-0101-010101010101"),
					},
				},
			},
		}

		By("Creating a Nutanix Machine Config with data disks", func() {
			dataDiskNMT := testHelper.createDefaultNMTwithDataDisks(clusterName, namespace.Name, withDataDisksParams{
				DataDisks: dataDisks,
			})

			testHelper.createCapiObject(ctx, createCapiObjectParams{
				creator:    bootstrapClusterProxy.GetClient(),
				capiObject: dataDiskNMT,
			})
		})

		By("Creating a workload cluster", func() {
			testHelper.deployCluster(deployClusterParams{
				clusterName:           clusterName,
				namespace:             namespace,
				flavor:                flavor,
				clusterctlConfigPath:  clusterctlConfigPath,
				artifactFolder:        artifactFolder,
				bootstrapClusterProxy: bootstrapClusterProxy,
			}, clusterResources)
		})

		By("Checking machine status is 'Failed' and failure message is set", func() {
			testHelper.verifyFailureMessageOnClusterMachines(ctx, verifyFailureMessageOnClusterMachinesParams{
				clusterName:            clusterName,
				namespace:              namespace,
				expectedPhase:          "Failed",
				expectedFailureMessage: "failed to find storage container 01010101-0101-0101-0101-0101010101",
				bootstrapClusterProxy:  bootstrapClusterProxy,
			})
		})

		By("PASSED!")
	})

	It("Should failed to create a cluster with wrong data disk device index", func() {
		const flavor = "no-nmt"

		Expect(namespace).NotTo(BeNil(), "Namespace can't be nil")

		dataDisks = []infrav1.NutanixMachineVMDisk{
			{
				DiskSize: resource.MustParse("10Gi"),
				DeviceProperties: &infrav1.NutanixMachineVMDiskDeviceProperties{
					DeviceType:  infrav1.NutanixMachineDiskDeviceTypeDisk,
					AdapterType: infrav1.NutanixMachineDiskAdapterTypeSCSI,
					DeviceIndex: 10,
				},
			},
			{
				DiskSize: resource.MustParse("10Gi"),
				DeviceProperties: &infrav1.NutanixMachineVMDiskDeviceProperties{
					DeviceType:  infrav1.NutanixMachineDiskDeviceTypeDisk,
					AdapterType: infrav1.NutanixMachineDiskAdapterTypeSCSI,
					DeviceIndex: 10,
				},
			},
		}

		By("Creating a Nutanix Machine Config with data disks", func() {
			dataDiskNMT := testHelper.createDefaultNMTwithDataDisks(clusterName, namespace.Name, withDataDisksParams{
				DataDisks: dataDisks,
			})

			testHelper.createCapiObject(ctx, createCapiObjectParams{
				creator:    bootstrapClusterProxy.GetClient(),
				capiObject: dataDiskNMT,
			})
		})

		By("Creating a workload cluster", func() {
			testHelper.deployCluster(deployClusterParams{
				clusterName:           clusterName,
				namespace:             namespace,
				flavor:                flavor,
				clusterctlConfigPath:  clusterctlConfigPath,
				artifactFolder:        artifactFolder,
				bootstrapClusterProxy: bootstrapClusterProxy,
			}, clusterResources)
		})

		By("Checking machine status is 'Failed' and failure message is set", func() {
			testHelper.verifyFailureMessageOnClusterMachines(ctx, verifyFailureMessageOnClusterMachinesParams{
				clusterName:            clusterName,
				namespace:              namespace,
				expectedPhase:          "Failed",
				expectedFailureMessage: "Slot scsi.10 is occupied: 10",
				bootstrapClusterProxy:  bootstrapClusterProxy,
			})
		})

		By("PASSED!")
	})

	It("Should create a cluster with data disks successfully with default storage container by name", func() {
		const flavor = "no-nmt"

		Expect(namespace).NotTo(BeNil(), "Namespace can't be nil")

		scName, scUUID, err := testHelper.getDefaultStorageContainerNameAndUuid(ctx)
		Expect(err).To(BeNil(), "Failed to get default storage container")
		Expect(scName).NotTo(BeEmpty(), "Storage container name can't be empty")
		Expect(scUUID).NotTo(BeEmpty(), "Storage container UUID can't be empty")

		dataDisks = []infrav1.NutanixMachineVMDisk{
			{
				DiskSize: resource.MustParse("10Gi"),
				DeviceProperties: &infrav1.NutanixMachineVMDiskDeviceProperties{
					DeviceType:  infrav1.NutanixMachineDiskDeviceTypeDisk,
					AdapterType: infrav1.NutanixMachineDiskAdapterTypeSCSI,
				},
				StorageConfig: &infrav1.NutanixMachineVMStorageConfig{
					DiskMode: infrav1.NutanixMachineDiskModeStandard,
					StorageContainer: &infrav1.NutanixResourceIdentifier{
						Name: ptr.To(scName),
						Type: infrav1.NutanixIdentifierName,
					},
				},
			},
		}

		By("Creating a Nutanix Machine Config with data disks", func() {
			dataDiskNMT := testHelper.createDefaultNMTwithDataDisks(clusterName, namespace.Name, withDataDisksParams{
				DataDisks: dataDisks,
			})

			testHelper.createCapiObject(ctx, createCapiObjectParams{
				creator:    bootstrapClusterProxy.GetClient(),
				capiObject: dataDiskNMT,
			})
		})

		By("Creating a workload cluster", func() {
			testHelper.deployClusterAndWait(deployClusterParams{
				clusterName:           clusterName,
				namespace:             namespace,
				flavor:                flavor,
				clusterctlConfigPath:  clusterctlConfigPath,
				artifactFolder:        artifactFolder,
				bootstrapClusterProxy: bootstrapClusterProxy,
			}, clusterResources)
		})

		By("Checking the data disks are attached to the VMs", func() {
			testHelper.verifyDisksOnNutanixMachines(ctx, verifyDisksOnNutanixMachinesParams{
				clusterName:           clusterName,
				namespace:             namespace.Name,
				bootstrapClusterProxy: bootstrapClusterProxy,
				diskCount:             3,
			})
		})

		By("PASSED!")
	})

	It("Should create a cluster with data disks successfully with default storage container by uuid", func() {
		const flavor = "no-nmt"

		Expect(namespace).NotTo(BeNil(), "Namespace can't be nil")

		scName, scUUID, err := testHelper.getDefaultStorageContainerNameAndUuid(ctx)
		Expect(err).To(BeNil(), "Failed to get default storage container")
		Expect(scName).NotTo(BeEmpty(), "Storage container name can't be empty")
		Expect(scUUID).NotTo(BeEmpty(), "Storage container UUID can't be empty")

		dataDisks = []infrav1.NutanixMachineVMDisk{
			{
				DiskSize: resource.MustParse("10Gi"),
				DeviceProperties: &infrav1.NutanixMachineVMDiskDeviceProperties{
					DeviceType:  infrav1.NutanixMachineDiskDeviceTypeDisk,
					AdapterType: infrav1.NutanixMachineDiskAdapterTypeSCSI,
				},
				StorageConfig: &infrav1.NutanixMachineVMStorageConfig{
					DiskMode: infrav1.NutanixMachineDiskModeStandard,
					StorageContainer: &infrav1.NutanixResourceIdentifier{
						UUID: ptr.To(scUUID),
						Type: infrav1.NutanixIdentifierUUID,
					},
				},
			},
		}

		By("Creating a Nutanix Machine Config with data disks", func() {
			dataDiskNMT := testHelper.createDefaultNMTwithDataDisks(clusterName, namespace.Name, withDataDisksParams{
				DataDisks: dataDisks,
			})

			testHelper.createCapiObject(ctx, createCapiObjectParams{
				creator:    bootstrapClusterProxy.GetClient(),
				capiObject: dataDiskNMT,
			})
		})

		By("Creating a workload cluster", func() {
			testHelper.deployClusterAndWait(deployClusterParams{
				clusterName:           clusterName,
				namespace:             namespace,
				flavor:                flavor,
				clusterctlConfigPath:  clusterctlConfigPath,
				artifactFolder:        artifactFolder,
				bootstrapClusterProxy: bootstrapClusterProxy,
			}, clusterResources)
		})

		By("Checking the data disks are attached to the VMs", func() {
			testHelper.verifyDisksOnNutanixMachines(ctx, verifyDisksOnNutanixMachinesParams{
				clusterName:           clusterName,
				namespace:             namespace.Name,
				bootstrapClusterProxy: bootstrapClusterProxy,
				diskCount:             3,
			})
		})

		By("PASSED!")
	})
})
