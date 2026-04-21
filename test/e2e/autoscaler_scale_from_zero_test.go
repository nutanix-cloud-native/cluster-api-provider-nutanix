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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/cluster-api/test/framework/clusterctl"
	"sigs.k8s.io/controller-runtime/pkg/client"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
)

var _ = Describe("Autoscaler scale-from-zero", Label("capx-feature-test", "autoscaler"), func() {
	const specName = "autoscaler-scale-from-zero"

	var (
		namespace        *corev1.Namespace
		clusterName      string
		clusterResources *clusterctl.ApplyClusterTemplateAndWaitResult
		cancelWatches    context.CancelFunc
		testHelper       testHelperInterface
	)

	BeforeEach(func() {
		testHelper = newTestHelper(e2eConfig)
		clusterName = testHelper.generateTestClusterName(specName)
		clusterResources = new(clusterctl.ApplyClusterTemplateAndWaitResult)
		Expect(bootstrapClusterProxy).NotTo(BeNil(), "BootstrapClusterProxy can't be nil")
		namespace, cancelWatches = setupSpecNamespace(ctx, specName, bootstrapClusterProxy, artifactFolder)
	})

	AfterEach(func() {
		dumpSpecResourcesAndCleanup(ctx, specName, bootstrapClusterProxy, artifactFolder, namespace, cancelWatches, clusterResources.Cluster, e2eConfig.GetIntervals, skipCleanup)
	})

	It("Should publish status.capacity on NutanixMachineTemplate", func() {
		Expect(namespace).NotTo(BeNil())

		By("Creating a workload cluster with 1 control plane and 1 worker node")
		testHelper.deployClusterAndWait(
			deployClusterParams{
				clusterName:           clusterName,
				namespace:             namespace,
				flavor:                "",
				clusterctlConfigPath:  clusterctlConfigPath,
				artifactFolder:        artifactFolder,
				bootstrapClusterProxy: bootstrapClusterProxy,
			}, clusterResources)

		Expect(clusterResources.Cluster).NotTo(BeNil())

		bcpClient := bootstrapClusterProxy.GetClient()

		By("Verifying NutanixMachineTemplate has status.capacity set")
		var workerNMT *infrav1.NutanixMachineTemplate
		Eventually(func(g Gomega) {
			nmtList := &infrav1.NutanixMachineTemplateList{}
			g.Expect(bcpClient.List(ctx, nmtList, client.InNamespace(namespace.Name))).To(Succeed())
			g.Expect(nmtList.Items).NotTo(BeEmpty(), "expected at least one NutanixMachineTemplate")

			for i := range nmtList.Items {
				nmt := &nmtList.Items[i]
				if len(nmt.Status.Capacity) > 0 {
					workerNMT = nmt
					break
				}
			}
			g.Expect(workerNMT).NotTo(BeNil(), "expected at least one NutanixMachineTemplate with status.capacity set")
		}, defaultTimeout, defaultInterval).Should(Succeed())

		By("Verifying status.capacity contains cpu and memory")
		cpuQty, hasCPU := workerNMT.Status.Capacity[corev1.ResourceCPU]
		memQty, hasMemory := workerNMT.Status.Capacity[corev1.ResourceMemory]
		Expect(hasCPU).To(BeTrue(), "status.capacity should have cpu")
		Expect(hasMemory).To(BeTrue(), "status.capacity should have memory")
		Expect(cpuQty.Value()).To(BeNumerically(">", 0), "cpu capacity should be > 0")
		Expect(memQty.Value()).To(BeNumerically(">", 0), "memory capacity should be > 0")
		Byf("NutanixMachineTemplate %s has capacity: cpu=%s, memory=%s",
			workerNMT.Name, cpuQty.String(), memQty.String())

		By("PASSED!")
	})
})
