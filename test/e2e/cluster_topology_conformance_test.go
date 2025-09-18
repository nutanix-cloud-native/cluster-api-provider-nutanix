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
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta1"
)

var _ = Describe("When creating a cluster with topology and running kubetest/conformance", Label("clusterclass", "only-for-validation"), func() {
	const specName = "cluster-with-topology-conformance"
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

		kubetestConfigFilePath := testHelper.getVariableFromE2eConfig("KUBETEST_CONFIGURATION")
		Expect(kubetestConfigFilePath).To(BeAnExistingFile(), "%s should be a valid kubetest config file")

		nutanixE2ETest = NewNutanixE2ETest(
			WithE2ETestSpecName(specName),
			WithE2ETestHelper(testHelper),
			WithE2ETestConfig(e2eConfig),
			WithE2ETestBootstrapClusterProxy(bootstrapClusterProxy),
			WithE2ETestArtifactFolder(artifactFolder),
			WithE2ETestClusterctlConfigPath(clusterctlConfigPath),
			WithE2ETestKubetestConfigPath(kubetestConfigFilePath),
			WithE2ETestClusterTemplateFlavor(flavor),
			WithE2ETestNamespace(namespace),
		)
	})

	conformanceWorkflow := func(targetKube *E2eConfigK8SVersion) {
		clusterName = testHelper.generateTestClusterName(specName)
		Expect(clusterName).NotTo(BeNil())

		targetKubeVer := testHelper.getVariableFromE2eConfig(targetKube.E2eConfigK8sVersionEnvVar)
		targetImageName := testHelper.getVariableFromE2eConfig(targetKube.E2eConfigK8sVersionImageEnvVar)

		By("Creating a workload cluster with topology")
		clusterTopologyConfig := NewClusterTopologyConfig(
			WithName(clusterName),
			WithKubernetesVersion(targetKubeVer),
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

		By("Running conformance tests")
		err = nutanixE2ETest.RunConformanceTest(ctx, clusterResources)
		Expect(err).ToNot(HaveOccurred(), "Failed to run Kubernetes conformance")

		By("PASSED!")
	}

	AfterEach(func() {
		dumpSpecResourcesAndCleanup(ctx, specName, bootstrapClusterProxy, artifactFolder, namespace, cancelWatches, cluster, e2eConfig.GetIntervals, skipCleanup)
	})

	// Conformance tests
	for _, targetTestConfig := range SupportedK8STargetConfigs {
		labels := []string{"cluster-topology-conformance"}
		labels = append(labels, targetTestConfig.targetLabels...)
		It(fmt.Sprintf("Create a cluster with topology with %s", targetTestConfig.targetLabels[0]),
			Label(labels...), func() {
				conformanceWorkflow(targetTestConfig.targetKube)
			})
	}
})
