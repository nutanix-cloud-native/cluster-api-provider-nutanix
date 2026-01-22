//go:build e2e

/*
Copyright 2024 Nutanix

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
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	capiv1beta2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

var _ = Describe("When scaling up/down cluster with topology ", Label("clusterclass", "only-for-validation"), func() {
	const specName = "cluster-with-topology-scale-up-down-workflow"
	const flavor = "topology"

	var (
		namespace      *corev1.Namespace
		clusterName    string
		cluster        *capiv1beta2.Cluster
		cancelWatches  context.CancelFunc
		testHelper     testHelperInterface
		nutanixE2ETest *NutanixE2ETest
	)

	BeforeEach(func() {
		testHelper = newTestHelper(e2eConfig)
		Expect(bootstrapClusterProxy).NotTo(BeNil(), "BootstrapClusterProxy can't be nil")
		namespace, cancelWatches = setupSpecNamespace(ctx, specName, bootstrapClusterProxy, artifactFolder)
		Expect(namespace).NotTo(BeNil())
		nutanixE2ETest = NewNutanixE2ETest(
			WithE2ETestSpecName(specName),
			WithE2ETestHelper(testHelper),
			WithE2ETestConfig(e2eConfig),
			WithE2ETestBootstrapClusterProxy(bootstrapClusterProxy),
			WithE2ETestArtifactFolder(artifactFolder),
			WithE2ETestClusterctlConfigPath(clusterctlConfigPath),
			WithE2ETestClusterTemplateFlavor(flavor),
			WithE2ETestNamespace(namespace),
		)
	})

	AfterEach(func() {
		dumpSpecResourcesAndCleanup(ctx, specName, bootstrapClusterProxy, artifactFolder, namespace, cancelWatches, cluster, e2eConfig.GetIntervals, skipCleanup)
	})

	scaleUpDownWorkflow := func(
		targetKube *E2eConfigK8SVersion,
		fromMachineMemorySizeGibStr,
		toMachineMemorySizeGibStr,
		fromMachineSystemDiskSizeGibStr,
		toMachineSystemDiskSizeGibStr string,
		fromMachineVCPUSockets,
		toMachineVCPUSockets,
		fromMachineVCPUsPerSocket,
		toMachineVCPUsPerSocket int64,
	) {
		clusterName = testHelper.generateTestClusterName(specName)
		Expect(clusterName).NotTo(BeNil())

		targetKubeVer := testHelper.getVariableFromE2eConfig(targetKube.E2eConfigK8sVersionEnvVar)
		targetImageName := testHelper.getVariableFromE2eConfig(targetKube.E2eConfigK8sVersionImageEnvVar)

		By("Creating a workload cluster with topology")
		clusterTopologyConfig := NewClusterTopologyConfig(
			WithName(clusterName),
			WithKubernetesVersion(targetKubeVer),
			WithMachineMemorySize(fromMachineMemorySizeGibStr),
			WithMachineSystemDiskSize(fromMachineSystemDiskSizeGibStr),
			WithMachineVCPUSockets(fromMachineVCPUSockets),
			WithMachineVCPUSPerSocket(fromMachineVCPUsPerSocket),
			WithControlPlaneCount(1),
			WithWorkerNodeCount(1),
			WithControlPlaneMachineTemplateImage(targetImageName),
			WithWorkerMachineTemplateImage(targetImageName),
		)

		clusterResources, err := nutanixE2ETest.CreateCluster(ctx, clusterTopologyConfig)
		Expect(err).ToNot(HaveOccurred())

		// Set test specific variable so that cluster can be cleaned up after each test
		cluster = clusterResources.Cluster

		By("Waiting until nodes are ready")
		nutanixE2ETest.WaitForNodesReady(ctx, targetKubeVer, clusterResources)

		clusterTopologyConfig = NewClusterTopologyConfig(
			WithName(clusterName),
			WithKubernetesVersion(targetKubeVer),
			WithMachineMemorySize(toMachineMemorySizeGibStr),
			WithMachineSystemDiskSize(toMachineSystemDiskSizeGibStr),
			WithMachineVCPUSockets(toMachineVCPUSockets),
			WithMachineVCPUSPerSocket(toMachineVCPUsPerSocket),
			WithControlPlaneCount(1),
			WithWorkerNodeCount(1),
			WithControlPlaneMachineTemplateImage(targetImageName),
			WithWorkerMachineTemplateImage(targetImageName),
		)

		clusterResources, err = nutanixE2ETest.UpgradeCluster(ctx, clusterTopologyConfig)
		Expect(err).ToNot(HaveOccurred())

		By("Waiting for control-plane machines to have the upgraded Kubernetes version")
		nutanixE2ETest.WaitForControlPlaneMachinesToBeUpgraded(ctx, clusterTopologyConfig, clusterResources)

		By("Waiting for machine deployment machines to have the upgraded Kubernetes version")
		nutanixE2ETest.WaitForMachineDeploymentMachinesToBeUpgraded(ctx, clusterTopologyConfig, clusterResources)

		By("Waiting until nodes are ready")
		nutanixE2ETest.WaitForNodesReady(ctx, targetKubeVer, clusterResources)

		var toMachineMemorySizeGib int64
		_, err = fmt.Sscan(toMachineMemorySizeGibStr, &toMachineMemorySizeGib)
		Expect(err).ToNot(HaveOccurred())

		var toMachineSystemDiskSizeGib int64
		_, err = fmt.Sscan(toMachineSystemDiskSizeGibStr, &toMachineSystemDiskSizeGib)
		Expect(err).ToNot(HaveOccurred())

		By("Check if all the machines have scaled down resource config (memory size, VCPUSockets, vcpusPerSocket)")
		testHelper.verifyResourceConfigOnNutanixMachines(ctx, verifyResourceConfigOnNutanixMachinesParams{
			clusterName:                clusterName,
			namespace:                  namespace,
			toMachineMemorySizeGib:     toMachineMemorySizeGib,
			toMachineSystemDiskSizeGib: toMachineSystemDiskSizeGib,
			toMachineVCPUSockets:       toMachineVCPUSockets,
			toMachineVCPUsPerSocket:    toMachineVCPUsPerSocket,
			bootstrapClusterProxy:      bootstrapClusterProxy,
		})

		By("PASSED!")
	}

	// Scale Down Test Start

	// Memory Size scale down tests
	for _, targetTestConfig := range SupportedK8STargetConfigs {
		labels := []string{"cluster-topology-scale-down", "cluster-topology-scale-down-memory-size"}
		labels = append(labels, targetTestConfig.targetLabels...)
		It(fmt.Sprintf("Scale down a cluster with CP and Worker node machine memory size from 4Gi to 3Gi with %s", targetTestConfig.targetLabels[0]),
			Label(labels...), func() {
				scaleUpDownWorkflow(targetTestConfig.targetKube, "4Gi", "3Gi", "40Gi", "40Gi", 2, 2, 1, 1)
			})
	}

	// VCPUSocket scale down tests
	for _, targetTestConfig := range SupportedK8STargetConfigs {
		labels := []string{"cluster-topology-scale-down", "cluster-topology-scale-down-vcpusockets"}
		labels = append(labels, targetTestConfig.targetLabels...)
		It(fmt.Sprintf("Scale down a cluster with CP and Worker node VCPUSockets from 3 to 2 with %s", targetTestConfig.targetLabels[0]),
			Label(labels...), func() {
				scaleUpDownWorkflow(targetTestConfig.targetKube, "4Gi", "4Gi", "40Gi", "40Gi", 3, 2, 1, 1)
			})
	}

	// VCPUPerSocket scale down tests
	for _, targetTestConfig := range SupportedK8STargetConfigs {
		labels := []string{"cluster-topology-scale-down", "cluster-topology-scale-down-vcpupersocket"}
		labels = append(labels, targetTestConfig.targetLabels...)
		It(fmt.Sprintf("Scale down a cluster with CP and Worker node vcpu per socket from 2 to 1 with %s", targetTestConfig.targetLabels[0]),
			Label(labels...), func() {
				scaleUpDownWorkflow(targetTestConfig.targetKube, "4Gi", "4Gi", "40Gi", "40Gi", 3, 3, 2, 1)
			})
	}

	// TODO:deepakm-ntnx system disk scale down tests

	// Scale Down Test End

	// Scale Up Test Start

	// Memory Size scale up tests
	for _, targetTestConfig := range SupportedK8STargetConfigs {
		labels := []string{"cluster-topology-scale-up", "cluster-topology-scale-up-memory-size"}
		labels = append(labels, targetTestConfig.targetLabels...)
		It(fmt.Sprintf("Scale up a cluster with CP and Worker node machine memory size from 3Gi to 4Gi with %s", targetTestConfig.targetLabels[0]),
			Label(labels...), func() {
				scaleUpDownWorkflow(targetTestConfig.targetKube, "3Gi", "4Gi", "40Gi", "40Gi", 2, 2, 1, 1)
			})
	}

	// VCPUSocket scale up tests
	for _, targetTestConfig := range SupportedK8STargetConfigs {
		labels := []string{"cluster-topology-scale-up", "cluster-topology-scale-up-vcpusocket"}
		labels = append(labels, targetTestConfig.targetLabels...)
		It(fmt.Sprintf("Scale up a cluster with CP and Worker node VCPUSockets from 2 to 3 with %s", targetTestConfig.targetLabels[0]),
			Label(labels...), func() {
				scaleUpDownWorkflow(targetTestConfig.targetKube, "4Gi", "4Gi", "40Gi", "40Gi", 2, 3, 1, 1)
			})
	}

	// VCPUPerSocket scale up tests
	for _, targetTestConfig := range SupportedK8STargetConfigs {
		labels := []string{"cluster-topology-scale-up", "cluster-topology-scale-up-vcpupersocket"}
		labels = append(labels, targetTestConfig.targetLabels...)
		It(fmt.Sprintf("Scale up a cluster with CP and Worker node vcpu per socket from 1 to 2 with %s", targetTestConfig.targetLabels[0]),
			Label(labels...), func() {
				scaleUpDownWorkflow(targetTestConfig.targetKube, "4Gi", "4Gi", "40Gi", "40Gi", 3, 3, 1, 2)
			})
	}

	// TODO:deepakm-ntnx system disk scale up tests
	// Scale Up Test End
})
