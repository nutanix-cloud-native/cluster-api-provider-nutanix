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
	"k8s.io/apimachinery/pkg/types"
	capiv1beta2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/cluster-api/test/framework/clusterctl"
	"sigs.k8s.io/controller-runtime/pkg/client"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	"github.com/nutanix-cloud-native/cluster-api-provider-nutanix/controllers"
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

	It("Should publish status.capacity on NutanixMachineTemplate and propagate annotations to MachineDeployment", func() {
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

		By("Verifying MachineDeployment has capacity annotations propagated")
		mdList := &capiv1beta2.MachineDeploymentList{}
		Expect(bcpClient.List(ctx, mdList, client.InNamespace(namespace.Name))).To(Succeed())
		Expect(mdList.Items).NotTo(BeEmpty(), "expected at least one MachineDeployment")

		var matchingMD *capiv1beta2.MachineDeployment
		for i := range mdList.Items {
			md := &mdList.Items[i]
			ref := md.Spec.Template.Spec.InfrastructureRef
			if ref.Kind == "NutanixMachineTemplate" && ref.Name == workerNMT.Name {
				matchingMD = md
				break
			}
		}
		Expect(matchingMD).NotTo(BeNil(),
			fmt.Sprintf("expected a MachineDeployment referencing NutanixMachineTemplate %s", workerNMT.Name))

		Eventually(func(g Gomega) {
			updated := &capiv1beta2.MachineDeployment{}
			g.Expect(bcpClient.Get(ctx, types.NamespacedName{
				Name:      matchingMD.Name,
				Namespace: matchingMD.Namespace,
			}, updated)).To(Succeed())

			annotations := updated.Annotations
			g.Expect(annotations).To(HaveKey(controllers.CapacityAnnotationCPU),
				"MachineDeployment should have capacity cpu annotation")
			g.Expect(annotations).To(HaveKey(controllers.CapacityAnnotationMemory),
				"MachineDeployment should have capacity memory annotation")
			g.Expect(annotations[controllers.CapacityAnnotationCPU]).To(Equal(cpuQty.String()),
				"capacity cpu annotation should match status.capacity")
			g.Expect(annotations[controllers.CapacityAnnotationMemory]).To(Equal(memQty.String()),
				"capacity memory annotation should match status.capacity")
		}, defaultTimeout, defaultInterval).Should(Succeed())

		Byf("MachineDeployment %s has capacity annotations: cpu=%s, memory=%s",
			matchingMD.Name,
			matchingMD.Annotations[controllers.CapacityAnnotationCPU],
			matchingMD.Annotations[controllers.CapacityAnnotationMemory])

		By("Verifying autoscaling range annotations can coexist with capacity annotations")
		patch := client.MergeFrom(matchingMD.DeepCopy())
		if matchingMD.Annotations == nil {
			matchingMD.Annotations = make(map[string]string)
		}
		matchingMD.Annotations["cluster.x-k8s.io/cluster-api-autoscaler-node-group-min-size"] = "0"
		matchingMD.Annotations["cluster.x-k8s.io/cluster-api-autoscaler-node-group-max-size"] = "5"
		Expect(bcpClient.Patch(ctx, matchingMD, patch)).To(Succeed())

		Eventually(func(g Gomega) {
			updated := &capiv1beta2.MachineDeployment{}
			g.Expect(bcpClient.Get(ctx, types.NamespacedName{
				Name:      matchingMD.Name,
				Namespace: matchingMD.Namespace,
			}, updated)).To(Succeed())

			annotations := updated.Annotations
			g.Expect(annotations).To(HaveKeyWithValue(
				"cluster.x-k8s.io/cluster-api-autoscaler-node-group-min-size", "0"))
			g.Expect(annotations).To(HaveKeyWithValue(
				"cluster.x-k8s.io/cluster-api-autoscaler-node-group-max-size", "5"))
			g.Expect(annotations).To(HaveKey(controllers.CapacityAnnotationCPU))
			g.Expect(annotations).To(HaveKey(controllers.CapacityAnnotationMemory))
		}, defaultTimeout, defaultInterval).Should(Succeed())

		By("PASSED!")
	})
})
