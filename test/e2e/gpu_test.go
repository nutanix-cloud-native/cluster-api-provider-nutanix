//go:build e2e

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

package e2e

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/cluster-api/test/framework/clusterctl"
	"sigs.k8s.io/cluster-api/util"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
)

const (
	nutanixGPUPassthroughNameEnv = "NUTANIX_GPU_PASSTHROUGH_NAME"
	nutanixGPUVirtualNameEnv     = "NUTANIX_GPU_VIRTUAL_NAME"
)

var _ = Describe("Nutanix Passthrough GPU", Label("nutanix-feature-test", "only-for-validation", "passthrough", "gpu"), func() {
	const specName = "cluster-gpu-passthrough"

	var (
		namespace                 *corev1.Namespace
		clusterName               string
		clusterResources          *clusterctl.ApplyClusterTemplateAndWaitResult
		cancelWatches             context.CancelFunc
		nutanixGPUPassthroughName string
		testHelper                testHelperInterface
	)

	BeforeEach(func() {
		testHelper = newTestHelper(e2eConfig)
		nutanixGPUPassthroughName = testHelper.getVariableFromE2eConfig(nutanixGPUPassthroughNameEnv)
		clusterName = testHelper.generateTestClusterName(specName)
		clusterResources = new(clusterctl.ApplyClusterTemplateAndWaitResult)
		Expect(bootstrapClusterProxy).NotTo(BeNil(), "BootstrapClusterProxy can't be nil")
		namespace, cancelWatches = setupSpecNamespace(ctx, specName, bootstrapClusterProxy, artifactFolder)
	})

	AfterEach(func() {
		dumpSpecResourcesAndCleanup(ctx, specName, bootstrapClusterProxy, artifactFolder, namespace, cancelWatches, clusterResources.Cluster, e2eConfig.GetIntervals, skipCleanup)
	})

	It("Create a cluster with non-existing Passthrough GPUs (should fail)", func() {
		const flavor = "no-nmt"

		Expect(namespace).NotTo(BeNil())

		By("Creating invalid Passthrough GPU Nutanix Machine Template", func() {
			invalidGPUName := util.RandomString(10)
			invalidGPUNMT := testHelper.createDefaultNMT(clusterName, namespace.Name)
			invalidGPUNMT.Spec.Template.Spec.GPUs = []infrav1.NutanixGPU{
				{
					Type: infrav1.NutanixGPUIdentifierName,
					Name: &invalidGPUName,
				},
			}

			testHelper.createCapiObject(ctx, createCapiObjectParams{
				creator:    bootstrapClusterProxy.GetClient(),
				capiObject: invalidGPUNMT,
			})
		})

		By("Creating a workload cluster", func() {
			testHelper.deployCluster(
				deployClusterParams{
					clusterName:           clusterName,
					namespace:             namespace,
					flavor:                flavor,
					clusterctlConfigPath:  clusterctlConfigPath,
					artifactFolder:        artifactFolder,
					bootstrapClusterProxy: bootstrapClusterProxy,
				},
				clusterResources,
			)
		})

		By("Checking machine status is 'Failed' and failure message is set", func() {
			testHelper.verifyFailureMessageOnClusterMachines(ctx, verifyFailureMessageOnClusterMachinesParams{
				clusterName:            clusterName,
				namespace:              namespace,
				expectedPhase:          "Failed",
				expectedFailureMessage: "no available GPU found",
				bootstrapClusterProxy:  bootstrapClusterProxy,
			})
		})

		By("PASSED!")
	})

	It("Create a cluster with passthrough GPUs", func() {
		const flavor = "no-nmt"

		Expect(namespace).NotTo(BeNil())

		By("Creating passthrough GPU Nutanix Machine Template", func() {
			GPUNMT := testHelper.createNameGPUNMT(ctx, clusterName, namespace.Name, createGPUNMTParams{
				gpuNameEnvKey: nutanixGPUPassthroughNameEnv,
			})

			testHelper.createCapiObject(ctx, createCapiObjectParams{
				creator:    bootstrapClusterProxy.GetClient(),
				capiObject: GPUNMT,
			})
		})

		By("Creating a workload cluster", func() {
			testHelper.deployClusterAndWait(
				deployClusterParams{
					clusterName:           clusterName,
					namespace:             namespace,
					flavor:                flavor,
					clusterctlConfigPath:  clusterctlConfigPath,
					artifactFolder:        artifactFolder,
					bootstrapClusterProxy: bootstrapClusterProxy,
				}, clusterResources)
		})

		By("Verifying if Passthrough GPU is assigned to the VMs")
		testHelper.verifyGPUNutanixMachines(ctx, verifyGPUNutanixMachinesParams{
			clusterName:           clusterName,
			namespace:             namespace.Name,
			gpuName:               nutanixGPUPassthroughName,
			bootstrapClusterProxy: bootstrapClusterProxy,
		})

		By("PASSED!")
	})

	It("Create a cluster with passthrough GPUs using device ID", func() {
		const flavor = "no-nmt"

		Expect(namespace).NotTo(BeNil())

		By("Creating passthrough GPU Nutanix Machine Template using deviceID", func() {
			GPUNMT := testHelper.createDeviceIDGPUNMT(ctx, clusterName, namespace.Name, createGPUNMTParams{
				gpuNameEnvKey: nutanixGPUPassthroughNameEnv,
			})

			testHelper.createCapiObject(ctx, createCapiObjectParams{
				creator:    bootstrapClusterProxy.GetClient(),
				capiObject: GPUNMT,
			})
		})

		By("Creating a workload cluster", func() {
			testHelper.deployClusterAndWait(
				deployClusterParams{
					clusterName:           clusterName,
					namespace:             namespace,
					flavor:                flavor,
					clusterctlConfigPath:  clusterctlConfigPath,
					artifactFolder:        artifactFolder,
					bootstrapClusterProxy: bootstrapClusterProxy,
				}, clusterResources)
		})

		By("Verifying if GPU is assigned to the VMs")
		testHelper.verifyGPUNutanixMachines(ctx, verifyGPUNutanixMachinesParams{
			clusterName:           clusterName,
			namespace:             namespace.Name,
			gpuName:               nutanixGPUPassthroughName,
			bootstrapClusterProxy: bootstrapClusterProxy,
		})

		By("PASSED!")
	})
})

var _ = Describe("Nutanix Virtual GPU", Label("nutanix-feature-test", "only-for-validation", "virtual", "gpu"), func() {
	const specName = "cluster-gpu-virtual"

	var (
		namespace             *corev1.Namespace
		clusterName           string
		clusterResources      *clusterctl.ApplyClusterTemplateAndWaitResult
		cancelWatches         context.CancelFunc
		nutanixGPUVirtualName string
		testHelper            testHelperInterface
	)

	BeforeEach(func() {
		testHelper = newTestHelper(e2eConfig)
		nutanixGPUVirtualName = testHelper.getVariableFromE2eConfig(nutanixGPUVirtualNameEnv)
		clusterName = testHelper.generateTestClusterName(specName)
		clusterResources = new(clusterctl.ApplyClusterTemplateAndWaitResult)
		Expect(bootstrapClusterProxy).NotTo(BeNil(), "BootstrapClusterProxy can't be nil")
		namespace, cancelWatches = setupSpecNamespace(ctx, specName, bootstrapClusterProxy, artifactFolder)
	})

	AfterEach(func() {
		dumpSpecResourcesAndCleanup(ctx, specName, bootstrapClusterProxy, artifactFolder, namespace, cancelWatches, clusterResources.Cluster, e2eConfig.GetIntervals, skipCleanup)
	})

	It("Create a cluster with non-existing virtual GPUs (should fail)", func() {
		const flavor = "no-nmt"

		Expect(namespace).NotTo(BeNil())

		By("Creating invalid virtual GPU Nutanix Machine Template", func() {
			invalidGPUName := util.RandomString(10)
			invalidGPUNMT := testHelper.createDefaultNMT(clusterName, namespace.Name)
			invalidGPUNMT.Spec.Template.Spec.GPUs = []infrav1.NutanixGPU{
				{
					Type: infrav1.NutanixGPUIdentifierName,
					Name: &invalidGPUName,
				},
			}

			testHelper.createCapiObject(ctx, createCapiObjectParams{
				creator:    bootstrapClusterProxy.GetClient(),
				capiObject: invalidGPUNMT,
			})
		})

		By("Creating a workload cluster", func() {
			testHelper.deployCluster(
				deployClusterParams{
					clusterName:           clusterName,
					namespace:             namespace,
					flavor:                flavor,
					clusterctlConfigPath:  clusterctlConfigPath,
					artifactFolder:        artifactFolder,
					bootstrapClusterProxy: bootstrapClusterProxy,
				},
				clusterResources,
			)
		})

		By("Checking machine status is 'Failed' and failure message is set", func() {
			testHelper.verifyFailureMessageOnClusterMachines(ctx, verifyFailureMessageOnClusterMachinesParams{
				clusterName:            clusterName,
				namespace:              namespace,
				expectedPhase:          "Failed",
				expectedFailureMessage: "no available GPU found",
				bootstrapClusterProxy:  bootstrapClusterProxy,
			})
		})

		By("PASSED!")
	})

	It("Create a cluster with virtual GPUs", func() {
		const flavor = "no-nmt"

		Expect(namespace).NotTo(BeNil())

		By("Creating virtual GPU Nutanix Machine Template", func() {
			GPUNMT := testHelper.createNameGPUNMT(ctx, clusterName, namespace.Name, createGPUNMTParams{
				gpuNameEnvKey: nutanixGPUVirtualNameEnv,
			})

			testHelper.createCapiObject(ctx, createCapiObjectParams{
				creator:    bootstrapClusterProxy.GetClient(),
				capiObject: GPUNMT,
			})
		})

		By("Creating a workload cluster", func() {
			testHelper.deployClusterAndWait(
				deployClusterParams{
					clusterName:           clusterName,
					namespace:             namespace,
					flavor:                flavor,
					clusterctlConfigPath:  clusterctlConfigPath,
					artifactFolder:        artifactFolder,
					bootstrapClusterProxy: bootstrapClusterProxy,
				}, clusterResources)
		})

		By("Verifying if virtual GPU is assigned to the VMs")
		testHelper.verifyGPUNutanixMachines(ctx, verifyGPUNutanixMachinesParams{
			clusterName:           clusterName,
			namespace:             namespace.Name,
			gpuName:               nutanixGPUVirtualName,
			bootstrapClusterProxy: bootstrapClusterProxy,
		})

		By("PASSED!")
	})

	It("Create a cluster with virtual GPUs using device ID", func() {
		const flavor = "no-nmt"

		Expect(namespace).NotTo(BeNil())

		By("Creating virtual GPU Nutanix Machine Template using deviceID", func() {
			GPUNMT := testHelper.createDeviceIDGPUNMT(ctx, clusterName, namespace.Name, createGPUNMTParams{
				gpuNameEnvKey: nutanixGPUVirtualNameEnv,
			})

			testHelper.createCapiObject(ctx, createCapiObjectParams{
				creator:    bootstrapClusterProxy.GetClient(),
				capiObject: GPUNMT,
			})
		})

		By("Creating a workload cluster", func() {
			testHelper.deployClusterAndWait(
				deployClusterParams{
					clusterName:           clusterName,
					namespace:             namespace,
					flavor:                flavor,
					clusterctlConfigPath:  clusterctlConfigPath,
					artifactFolder:        artifactFolder,
					bootstrapClusterProxy: bootstrapClusterProxy,
				}, clusterResources)
		})

		By("Verifying if GPU is assigned to the VMs")
		testHelper.verifyGPUNutanixMachines(ctx, verifyGPUNutanixMachinesParams{
			clusterName:           clusterName,
			namespace:             namespace.Name,
			gpuName:               nutanixGPUVirtualName,
			bootstrapClusterProxy: bootstrapClusterProxy,
		})

		By("PASSED!")
	})
})
