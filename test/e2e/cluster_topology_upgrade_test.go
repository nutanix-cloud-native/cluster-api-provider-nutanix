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

var _ = Describe("When upgrading the k8s version of cluster with topology", Label("clusterclass", "only-for-validation"), func() {
	const specName = "cluster-with-topology-upgrade-workflow"
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

	upgradeWorkflow := func(fromKube, toKube *E2eConfigK8SVersion) {
		clusterName = testHelper.generateTestClusterName(specName)
		Expect(clusterName).NotTo(BeNil())

		fromKubeVer := testHelper.getVariableFromE2eConfig(fromKube.E2eConfigK8sVersionEnvVar)
		fromImageName := testHelper.getVariableFromE2eConfig(fromKube.E2eConfigK8sVersionImageEnvVar)
		toKubeVer := testHelper.getVariableFromE2eConfig(toKube.E2eConfigK8sVersionEnvVar)
		toImageName := testHelper.getVariableFromE2eConfig(toKube.E2eConfigK8sVersionImageEnvVar)

		By("Creating a workload cluster with topology")
		clusterTopologyConfig := NewClusterTopologyConfig(
			WithName(clusterName),
			WithKubernetesVersion(fromKubeVer),
			WithControlPlaneCount(1),
			WithWorkerNodeCount(1),
			WithControlPlaneMachineTemplateImage(fromImageName),
			WithWorkerMachineTemplateImage(fromImageName),
		)

		clusterResources, err := nutanixE2ETest.CreateCluster(ctx, clusterTopologyConfig)
		Expect(err).ToNot(HaveOccurred())

		// Set test specific variable so that cluster can be cleaned up after each test
		cluster = clusterResources.Cluster

		By("Waiting until nodes are ready")
		nutanixE2ETest.WaitForNodesReady(ctx, fromKubeVer, clusterResources)

		clusterTopologyConfig = NewClusterTopologyConfig(
			WithName(clusterName),
			WithKubernetesVersion(toKubeVer),
			WithControlPlaneCount(1),
			WithWorkerNodeCount(1),
			WithControlPlaneMachineTemplateImage(toImageName),
			WithWorkerMachineTemplateImage(toImageName),
		)

		clusterResources, err = nutanixE2ETest.UpgradeCluster(ctx, clusterTopologyConfig)
		Expect(err).ToNot(HaveOccurred())

		By("Waiting for control-plane machines to have the upgraded Kubernetes version")
		nutanixE2ETest.WaitForControlPlaneMachinesToBeUpgraded(ctx, clusterTopologyConfig, clusterResources)

		By("Waiting for machine deployment machines to have the upgraded Kubernetes version")
		nutanixE2ETest.WaitForMachineDeploymentMachinesToBeUpgraded(ctx, clusterTopologyConfig, clusterResources)

		By("Waiting until nodes are ready")
		nutanixE2ETest.WaitForNodesReady(ctx, toKubeVer, clusterResources)

		By("PASSED!")
	}

	// It("Upgrade a cluster with topology from version Kube126 to Kube128 and expect failure", Label("Kube128", "cluster-topology-upgrade-test"), func() {
	// 	// NOTE: Following test will always fail and did not find a way to not fail as function we call has Expect call
	// 	// For more info on why refer https://cluster-api.sigs.k8s.io/tasks/experimental-features/cluster-class/operate-cluster
	// 	// we get following error
	// 	// The Cluster "cluster-with-topology-upgrade-workflow-u318rg" is invalid: spec.topology.version: Forbidden: version cannot be increased from "1.26.13" to "1.28.6"
	// 	clusterName = testHelper.generateTestClusterName(specName)
	// 	Expect(clusterName).NotTo(BeNil())
	//
	//  upgradeWorkflow(NewE2eConfigK8SVersion126(), NewE2eConfigK8SVersion128())
	// })

	totalKubeTestConfigs := len(SupportedK8STargetConfigs)
	for i := 0; i < totalKubeTestConfigs; i++ {
		fromKube := SupportedK8STargetConfigs[i]
		if i+1 >= totalKubeTestConfigs {
			break
		}
		toKube := SupportedK8STargetConfigs[i+1]

		labels := []string{"cluster-topology-upgrade"}
		labels = append(labels, fromKube.targetLabels...)
		labels = append(labels, toKube.targetLabels...)
		It(fmt.Sprintf("Upgrade a cluster with topology from version %s to %s", fromKube.targetLabels[0], toKube.targetLabels[0]),
			Label(labels...), func() {
				upgradeWorkflow(fromKube.targetKube, toKube.targetKube)
			})
	}
})
