//

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
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

var _ = Describe("When scaling in cluster with topology ", Label("clusterclass", "only-for-validation"), func() {
	const specName = "cluster-with-topology-scale-in-workflow"
	const flavor = "topology"

	var (
		namespace      *corev1.Namespace
		clusterName    string
		cluster        *clusterv1.Cluster
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

	scaleInWorkflow := func(targetKube *E2eConfigK8SVersion,
		fromCPNodeCount, fromWorkerNodeCount, toCPNodeCount, toWorkerNodeCount int,
	) {
		clusterName = testHelper.generateTestClusterName(specName)
		Expect(clusterName).NotTo(BeNil())

		targetKubeVer := testHelper.getVariableFromE2eConfig(targetKube.E2eConfigK8sVersionEnvVar)
		targetImageName := testHelper.getVariableFromE2eConfig(targetKube.E2eConfigK8sVersionImageEnvVar)

		By("Creating a workload cluster with topology")
		clusterTopologyConfig := NewClusterTopologyConfig(
			WithName(clusterName),
			WithKubernetesVersion(targetKubeVer),
			WithControlPlaneCount(fromCPNodeCount),
			WithWorkerNodeCount(fromWorkerNodeCount),
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
			WithControlPlaneCount(toCPNodeCount),
			WithWorkerNodeCount(toWorkerNodeCount),
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

		By("PASSED!")
	}

	// CP scale in tests
	for _, targetTestConfig := range SupportedK8STargetConfigs {
		labels := []string{"cluster-topology-scale-in", "cluster-topology-scale-in-cp"}
		labels = append(labels, targetTestConfig.targetLabels...)
		It(fmt.Sprintf("Scale in a cluster with topology from 3 CP node to 1 CP nodes with %s", targetTestConfig.targetLabels[0]),
			Label(labels...), func() {
				scaleInWorkflow(targetTestConfig.targetKube, 3, 1, 1, 1)
			})
	}

	// Worker scale in tests
	for _, targetTestConfig := range SupportedK8STargetConfigs {
		labels := []string{"cluster-topology-scale-in", "cluster-topology-scale-in-worker"}
		labels = append(labels, targetTestConfig.targetLabels...)
		It(fmt.Sprintf("Scale in a cluster with topology from 3 Worker node to 1 Worker nodes with %s", targetTestConfig.targetLabels[0]),
			Label(labels...), func() {
				scaleInWorkflow(targetTestConfig.targetKube, 1, 3, 1, 1)
			})
	}

	// CP and Worker scale in tests
	for _, targetTestConfig := range SupportedK8STargetConfigs {
		labels := []string{"cluster-topology-scale-in", "cluster-topology-scale-in-cp-worker"}
		labels = append(labels, targetTestConfig.targetLabels...)
		It(fmt.Sprintf("Scale in a cluster with topology from 3 CP and Worker node to 1 CP and Worker nodes with %s", targetTestConfig.targetLabels[0]),
			Label(labels...), func() {
				scaleInWorkflow(targetTestConfig.targetKube, 3, 3, 1, 1)
			})
	}
})
