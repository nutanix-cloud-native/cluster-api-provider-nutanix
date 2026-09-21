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
	"k8s.io/utils/ptr"
	"sigs.k8s.io/cluster-api/test/framework/clusterctl"
	"sigs.k8s.io/cluster-api/util"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
)

const (
	nutanixGPUPassthroughNameEnv = "NUTANIX_GPU_PASSTHROUGH_NAME"
	nutanixGPUVirtualNameEnv     = "NUTANIX_GPU_VIRTUAL_NAME"

	// AHV GPU profile names. These live in a different namespace from the device names
	// above: the physical profile is backed by devices named by nutanixGPUPassthroughNameEnv.
	nutanixGPUPhysicalProfileNameEnv = "NUTANIX_GPU_PHYSICAL_PROFILE_NAME"
	nutanixGPUVirtualProfileNameEnv  = "NUTANIX_GPU_VIRTUAL_PROFILE_NAME"
)

var _ = Describe("Nutanix Passthrough GPU", Label("passthrough", "gpu"), func() {
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
				expectedFailureMessage: "no available GPUs found",
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
					// The control plane and the workers share one NutanixMachineTemplate, so a
					// worker would claim a GPU of its own without being asserted on. Physical
					// GPUs are consumed whole and are scarce, so deploy without workers.
					workerMachineCount: ptr.To(int64(0)),
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
					// The control plane and the workers share one NutanixMachineTemplate, so a
					// worker would claim a GPU of its own without being asserted on. Physical
					// GPUs are consumed whole and are scarce, so deploy without workers.
					workerMachineCount: ptr.To(int64(0)),
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

var _ = Describe("Nutanix Virtual GPU", Label("virtual", "gpu"), func() {
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
				expectedFailureMessage: "no available GPUs found",
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
					// The control plane and the workers share one NutanixMachineTemplate, so a
					// worker would claim a GPU of its own without being asserted on. Physical
					// GPUs are consumed whole and are scarce, so deploy without workers.
					workerMachineCount: ptr.To(int64(0)),
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
					// The control plane and the workers share one NutanixMachineTemplate, so a
					// worker would claim a GPU of its own without being asserted on. Physical
					// GPUs are consumed whole and are scarce, so deploy without workers.
					workerMachineCount: ptr.To(int64(0)),
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

// Nutanix GPU profiles exercises GPU assignment by AHV GPU *profile* name (as opposed to
// device name / device ID), for both the broad-scope CI user and a project-scoped user.
//
// These live in the "gpu" suite rather than the "projects" suite because the e2e workflow
// only swaps in the GPU-capable Prism Element, subnet and control plane endpoint range when
// the label filter is "gpu"; running them under the "projects" label would land them on a
// Prism Element with no GPUs.
var _ = Describe("Nutanix GPU profiles", Label("gpu", "gpu-profile"), func() {
	const specName = "cluster-gpu-profile"

	var (
		namespace          *corev1.Namespace
		clusterName        string
		clusterResources   *clusterctl.ApplyClusterTemplateAndWaitResult
		cancelWatches      context.CancelFunc
		nutanixProjectName string
		testHelper         testHelperInterface
	)

	BeforeEach(func() {
		testHelper = newTestHelper(e2eConfig)
		nutanixProjectName = testHelper.getVariableFromE2eConfig(nutanixProjectNameEnv)
		clusterName = testHelper.generateTestClusterName(specName)
		clusterResources = new(clusterctl.ApplyClusterTemplateAndWaitResult)
		Expect(bootstrapClusterProxy).NotTo(BeNil(), "BootstrapClusterProxy can't be nil")
		namespace, cancelWatches = setupSpecNamespace(ctx, specName, bootstrapClusterProxy, artifactFolder)
	})

	AfterEach(func() {
		dumpSpecResourcesAndCleanup(ctx, specName, bootstrapClusterProxy, artifactFolder, namespace, cancelWatches, clusterResources.Cluster, e2eConfig.GetIntervals, skipCleanup)
	})

	// deployWithGPUProfile creates a NutanixMachineTemplate whose GPU is identified by the
	// profile name behind gpuProfileEnvKey, or by the UUID resolved from that name using the
	// authenticated E2E client. It optionally scopes the template to NUTANIX_PROJECT_NAME,
	// deploys a cluster with the given flavor, and verifies the resulting VMs reference the
	// expected profile.
	//
	// The cluster is deployed with zero workers: the KubeadmControlPlane and the
	// MachineDeployment share a single NutanixMachineTemplate, so every machine would
	// otherwise claim a GPU. Physical GPUs are consumed whole and are a scarce, shared
	// resource, so one control plane machine is all these specs need to assert on.
	deployWithGPUProfile := func(flavor, gpuProfileEnvKey string, profileIdentifierType infrav1.NutanixIdentifierType, scopedToProject bool) {
		Expect(namespace).NotTo(BeNil())
		profileName := testHelper.getVariableFromE2eConfig(gpuProfileEnvKey)
		profile := infrav1.NutanixResourceIdentifier{
			Type: infrav1.NutanixIdentifierName,
			Name: &profileName,
		}
		if profileIdentifierType == infrav1.NutanixIdentifierUUID {
			profileUUID := testHelper.findGPUProfileExtID(ctx, profileName)
			profile = infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: &profileUUID,
			}
		}

		By("Creating a GPU profile Nutanix Machine Template", func() {
			gpuNMT := testHelper.createProfileGPUNMT(clusterName, namespace.Name, profile)
			if scopedToProject {
				Expect(nutanixProjectName).ToNot(BeEmpty())
				gpuNMT.Spec.Template.Spec.Project = &infrav1.NutanixResourceIdentifier{
					Type: infrav1.NutanixIdentifierName,
					Name: &nutanixProjectName,
				}
			}

			testHelper.createCapiObject(ctx, createCapiObjectParams{
				creator:    bootstrapClusterProxy.GetClient(),
				capiObject: gpuNMT,
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
					workerMachineCount:    ptr.To(int64(0)),
				}, clusterResources)
		})

		By("Verifying the VMs reference the expected GPU profile")
		testHelper.verifyGPUProfileNutanixMachines(ctx, verifyGPUProfileNutanixMachinesParams{
			clusterName:           clusterName,
			namespace:             namespace.Name,
			gpuProfileName:        testHelper.getVariableFromE2eConfig(gpuProfileEnvKey),
			bootstrapClusterProxy: bootstrapClusterProxy,
		})

		By("PASSED!")
	}

	It("Create a cluster with a physical GPU profile", Label("physical", "broad-scope-user"), func() {
		deployWithGPUProfile("no-nmt", nutanixGPUPhysicalProfileNameEnv, infrav1.NutanixIdentifierName, false)
	})

	It("Create a cluster with a virtual GPU profile", Label("virtual", "broad-scope-user"), func() {
		deployWithGPUProfile("no-nmt", nutanixGPUVirtualProfileNameEnv, infrav1.NutanixIdentifierName, false)
	})

	It("Create a cluster with a physical GPU profile as a project-scoped user", Label("physical", "project-scope-user"), func() {
		deployWithGPUProfile("no-nmt-project-scoped-user", nutanixGPUPhysicalProfileNameEnv, infrav1.NutanixIdentifierName, true)
	})

	It("Create a cluster with a virtual GPU profile as a project-scoped user", Label("virtual", "project-scope-user"), func() {
		deployWithGPUProfile("no-nmt-project-scoped-user", nutanixGPUVirtualProfileNameEnv, infrav1.NutanixIdentifierName, true)
	})

	It("Create a cluster with a physical GPU profile UUID", Label("physical", "broad-scope-user"), func() {
		deployWithGPUProfile("no-nmt", nutanixGPUPhysicalProfileNameEnv, infrav1.NutanixIdentifierUUID, false)
	})

	It("Create a cluster with a virtual GPU profile UUID as a project-scoped user", Label("virtual", "project-scope-user"), func() {
		deployWithGPUProfile("no-nmt-project-scoped-user", nutanixGPUVirtualProfileNameEnv, infrav1.NutanixIdentifierUUID, true)
	})
})
