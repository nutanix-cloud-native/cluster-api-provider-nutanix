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
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

var _ = Describe("When scaling up/down cluster with topology ", Label("clusterclass"), func() {
	const specName = "cluster-with-topology-scale-up-down-workflow"
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

	scaleUpDownWorkflow := func(
		targetKubeVer,
		targetImageName,
		fromMachineMemorySizeGibStr,
		toMachineMemorySizeGibStr,
		fromMachineSystemDiskSizeGibStr,
		toMachineSystemDiskSizeGibStr string,
		fromMachineVCPUSockets,
		toMachineVCPUSockets,
		fromMachineVCPUsPerSocket,
		toMachineVCPUsPerSocket int64,
	) {
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
		fmt.Sscan(toMachineMemorySizeGibStr, &toMachineMemorySizeGib)
		var toMachineSystemDiskSizeGib int64
		fmt.Sscan(toMachineSystemDiskSizeGibStr, &toMachineSystemDiskSizeGib)
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
	It("Scale down a cluster with CP and Worker node machine memory size from 4Gi to 3Gi with Kube127", Label("Kube127", "cluster-topology-scale-down"), func() {
		clusterName = testHelper.generateTestClusterName(specName)
		Expect(clusterName).NotTo(BeNil())

		kube127 := testHelper.getVariableFromE2eConfig("KUBERNETES_VERSION_v1_27")
		kube127Image := testHelper.getVariableFromE2eConfig("NUTANIX_MACHINE_TEMPLATE_IMAGE_NAME_v1_27")
		scaleUpDownWorkflow(kube127, kube127Image, "4Gi", "3Gi", "40Gi", "40Gi", 2, 2, 1, 1)
	})

	It("Scale down a cluster with CP and Worker node machine memory size from 4Gi to 3Gi with Kube128", Label("Kube128", "cluster-topology-scale-down"), func() {
		clusterName = testHelper.generateTestClusterName(specName)
		Expect(clusterName).NotTo(BeNil())

		kube128 := testHelper.getVariableFromE2eConfig("KUBERNETES_VERSION_v1_28")
		kube128Image := testHelper.getVariableFromE2eConfig("NUTANIX_MACHINE_TEMPLATE_IMAGE_NAME_v1_28")
		scaleUpDownWorkflow(kube128, kube128Image, "4Gi", "3Gi", "40Gi", "40Gi", 2, 2, 1, 1)
	})

	It("Scale down a cluster with CP and Worker node machine memory size from 4Gi to 3Gi with Kube129", Label("Kube129", "cluster-topology-scale-down"), func() {
		clusterName = testHelper.generateTestClusterName(specName)
		Expect(clusterName).NotTo(BeNil())

		Kube129 := testHelper.getVariableFromE2eConfig("KUBERNETES_VERSION_v1_29")
		Kube129Image := testHelper.getVariableFromE2eConfig("NUTANIX_MACHINE_TEMPLATE_IMAGE_NAME_v1_29")
		scaleUpDownWorkflow(Kube129, Kube129Image, "4Gi", "3Gi", "40Gi", "40Gi", 2, 2, 1, 1)
	})

	It("Scale down a cluster with CP and Worker node VCPUSockets from 3 to 2 with Kube127", Label("Kube127", "cluster-topology-scale-down"), func() {
		clusterName = testHelper.generateTestClusterName(specName)
		Expect(clusterName).NotTo(BeNil())

		kube127 := testHelper.getVariableFromE2eConfig("KUBERNETES_VERSION_v1_27")
		kube127Image := testHelper.getVariableFromE2eConfig("NUTANIX_MACHINE_TEMPLATE_IMAGE_NAME_v1_27")
		scaleUpDownWorkflow(kube127, kube127Image, "4Gi", "4Gi", "40Gi", "40Gi", 3, 2, 1, 1)
	})

	It("Scale down a cluster with CP and Worker node VCPUSockets from 3 to 2 with Kube128", Label("Kube128", "cluster-topology-scale-down"), func() {
		clusterName = testHelper.generateTestClusterName(specName)
		Expect(clusterName).NotTo(BeNil())

		kube128 := testHelper.getVariableFromE2eConfig("KUBERNETES_VERSION_v1_28")
		kube128Image := testHelper.getVariableFromE2eConfig("NUTANIX_MACHINE_TEMPLATE_IMAGE_NAME_v1_28")
		scaleUpDownWorkflow(kube128, kube128Image, "4Gi", "4Gi", "40Gi", "40Gi", 3, 2, 1, 1)
	})

	It("Scale down a cluster with CP and Worker node machine VCPUSockets from 3 to 2 with Kube129", Label("Kube129", "cluster-topology-scale-down"), func() {
		clusterName = testHelper.generateTestClusterName(specName)
		Expect(clusterName).NotTo(BeNil())

		Kube129 := testHelper.getVariableFromE2eConfig("KUBERNETES_VERSION_v1_29")
		Kube129Image := testHelper.getVariableFromE2eConfig("NUTANIX_MACHINE_TEMPLATE_IMAGE_NAME_v1_29")
		scaleUpDownWorkflow(Kube129, Kube129Image, "4Gi", "4Gi", "40Gi", "40Gi", 3, 2, 1, 1)
	})

	It("Scale down a cluster with CP and Worker node vcpu per socket from 2 to 1 with Kube127", Label("Kube127", "cluster-topology-scale-down"), func() {
		clusterName = testHelper.generateTestClusterName(specName)
		Expect(clusterName).NotTo(BeNil())

		kube127 := testHelper.getVariableFromE2eConfig("KUBERNETES_VERSION_v1_27")
		kube127Image := testHelper.getVariableFromE2eConfig("NUTANIX_MACHINE_TEMPLATE_IMAGE_NAME_v1_27")
		scaleUpDownWorkflow(kube127, kube127Image, "4Gi", "4Gi", "40Gi", "40Gi", 3, 3, 2, 1)
	})

	It("Scale down a cluster with CP and Worker node vcpu per socket from 2 to 1 with Kube128", Label("Kube128", "cluster-topology-scale-down"), func() {
		clusterName = testHelper.generateTestClusterName(specName)
		Expect(clusterName).NotTo(BeNil())

		kube128 := testHelper.getVariableFromE2eConfig("KUBERNETES_VERSION_v1_28")
		kube128Image := testHelper.getVariableFromE2eConfig("NUTANIX_MACHINE_TEMPLATE_IMAGE_NAME_v1_28")
		scaleUpDownWorkflow(kube128, kube128Image, "4Gi", "4Gi", "40Gi", "40Gi", 3, 3, 2, 1)
	})

	It("Scale down a cluster with CP and Worker node machine vcpu per socket from 2 to 1 with Kube129", Label("Kube129", "cluster-topology-scale-down"), func() {
		clusterName = testHelper.generateTestClusterName(specName)
		Expect(clusterName).NotTo(BeNil())

		Kube129 := testHelper.getVariableFromE2eConfig("KUBERNETES_VERSION_v1_29")
		Kube129Image := testHelper.getVariableFromE2eConfig("NUTANIX_MACHINE_TEMPLATE_IMAGE_NAME_v1_29")
		scaleUpDownWorkflow(Kube129, Kube129Image, "4Gi", "4Gi", "40Gi", "40Gi", 3, 3, 2, 1)
	})

	// TODO add system disk scale down tests
	// Scale Down Test End

	// Scale Up Test Start
	It("Scale up a cluster with CP and Worker node machine memory size from 3Gi to 4Gi with Kube127", Label("Kube127", "cluster-topology-scale-up"), func() {
		clusterName = testHelper.generateTestClusterName(specName)
		Expect(clusterName).NotTo(BeNil())

		kube127 := testHelper.getVariableFromE2eConfig("KUBERNETES_VERSION_v1_27")
		kube127Image := testHelper.getVariableFromE2eConfig("NUTANIX_MACHINE_TEMPLATE_IMAGE_NAME_v1_27")
		scaleUpDownWorkflow(kube127, kube127Image, "3Gi", "4Gi", "40Gi", "40Gi", 2, 2, 1, 1)
	})

	It("Scale up a cluster with CP and Worker node machine memory size from 3Gi to 4Gi with Kube128", Label("Kube128", "cluster-topology-scale-up"), func() {
		clusterName = testHelper.generateTestClusterName(specName)
		Expect(clusterName).NotTo(BeNil())

		kube128 := testHelper.getVariableFromE2eConfig("KUBERNETES_VERSION_v1_28")
		kube128Image := testHelper.getVariableFromE2eConfig("NUTANIX_MACHINE_TEMPLATE_IMAGE_NAME_v1_28")
		scaleUpDownWorkflow(kube128, kube128Image, "3Gi", "4Gi", "40Gi", "40Gi", 2, 2, 1, 1)
	})

	It("Scale up a cluster with CP and Worker node machine memory size from 3Gi to 4Gi with Kube129", Label("Kube129", "cluster-topology-scale-up"), func() {
		clusterName = testHelper.generateTestClusterName(specName)
		Expect(clusterName).NotTo(BeNil())

		Kube129 := testHelper.getVariableFromE2eConfig("KUBERNETES_VERSION_v1_29")
		Kube129Image := testHelper.getVariableFromE2eConfig("NUTANIX_MACHINE_TEMPLATE_IMAGE_NAME_v1_29")
		scaleUpDownWorkflow(Kube129, Kube129Image, "3Gi", "4Gi", "40Gi", "40Gi", 2, 2, 1, 1)
	})

	It("Scale up a cluster with CP and Worker node VCPUSockets from 2 to 3 with Kube127", Label("Kube127", "cluster-topology-scale-up"), func() {
		clusterName = testHelper.generateTestClusterName(specName)
		Expect(clusterName).NotTo(BeNil())

		kube127 := testHelper.getVariableFromE2eConfig("KUBERNETES_VERSION_v1_27")
		kube127Image := testHelper.getVariableFromE2eConfig("NUTANIX_MACHINE_TEMPLATE_IMAGE_NAME_v1_27")
		scaleUpDownWorkflow(kube127, kube127Image, "4Gi", "4Gi", "40Gi", "40Gi", 2, 3, 1, 1)
	})

	It("Scale up a cluster with CP and Worker node VCPUSockets from 2 to 3 with Kube128", Label("Kube128", "cluster-topology-scale-up"), func() {
		clusterName = testHelper.generateTestClusterName(specName)
		Expect(clusterName).NotTo(BeNil())

		kube128 := testHelper.getVariableFromE2eConfig("KUBERNETES_VERSION_v1_28")
		kube128Image := testHelper.getVariableFromE2eConfig("NUTANIX_MACHINE_TEMPLATE_IMAGE_NAME_v1_28")
		scaleUpDownWorkflow(kube128, kube128Image, "4Gi", "4Gi", "40Gi", "40Gi", 2, 3, 1, 1)
	})

	It("Scale up a cluster with CP and Worker node machine VCPUSockets from 2 to 3 with Kube129", Label("Kube129", "cluster-topology-scale-up"), func() {
		clusterName = testHelper.generateTestClusterName(specName)
		Expect(clusterName).NotTo(BeNil())

		Kube129 := testHelper.getVariableFromE2eConfig("KUBERNETES_VERSION_v1_29")
		Kube129Image := testHelper.getVariableFromE2eConfig("NUTANIX_MACHINE_TEMPLATE_IMAGE_NAME_v1_29")
		scaleUpDownWorkflow(Kube129, Kube129Image, "4Gi", "4Gi", "40Gi", "40Gi", 2, 3, 1, 1)
	})

	It("Scale up a cluster with CP and Worker node vcpu per socket from 1 to 2 with Kube127", Label("Kube127", "cluster-topology-scale-up"), func() {
		clusterName = testHelper.generateTestClusterName(specName)
		Expect(clusterName).NotTo(BeNil())

		kube127 := testHelper.getVariableFromE2eConfig("KUBERNETES_VERSION_v1_27")
		kube127Image := testHelper.getVariableFromE2eConfig("NUTANIX_MACHINE_TEMPLATE_IMAGE_NAME_v1_27")
		scaleUpDownWorkflow(kube127, kube127Image, "4Gi", "4Gi", "40Gi", "40Gi", 3, 3, 1, 2)
	})

	It("Scale up a cluster with CP and Worker node vcpu per socket from 1 to 2 with Kube128", Label("Kube128", "cluster-topology-scale-up"), func() {
		clusterName = testHelper.generateTestClusterName(specName)
		Expect(clusterName).NotTo(BeNil())

		kube128 := testHelper.getVariableFromE2eConfig("KUBERNETES_VERSION_v1_28")
		kube128Image := testHelper.getVariableFromE2eConfig("NUTANIX_MACHINE_TEMPLATE_IMAGE_NAME_v1_28")
		scaleUpDownWorkflow(kube128, kube128Image, "4Gi", "4Gi", "40Gi", "40Gi", 3, 3, 1, 2)
	})

	It("Scale up a cluster with CP and Worker node machine vcpu per socket from 1 to 2 with Kube129", Label("Kube129", "cluster-topology-scale-up"), func() {
		clusterName = testHelper.generateTestClusterName(specName)
		Expect(clusterName).NotTo(BeNil())

		Kube129 := testHelper.getVariableFromE2eConfig("KUBERNETES_VERSION_v1_29")
		Kube129Image := testHelper.getVariableFromE2eConfig("NUTANIX_MACHINE_TEMPLATE_IMAGE_NAME_v1_29")
		scaleUpDownWorkflow(Kube129, Kube129Image, "4Gi", "4Gi", "40Gi", "40Gi", 3, 3, 1, 2)
	})

	// TODO add system disk scale up tests
	// Scale Up Test End
})
