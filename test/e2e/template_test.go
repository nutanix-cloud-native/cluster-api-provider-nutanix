//go:build e2e

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
	"github.com/onsi/gomega/gstruct"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/cluster-api/test/framework/clusterctl"
)

var _ = Describe("Nutanix Basic Creation with templating", Label("capx-feature-test", "image-templating"), func() {
	const (
		specName           = "cluster-template"
		ccmInstanceTypeKey = "node.kubernetes.io/instance-type"
		ccmInstanceType    = "ahv-vm"
		ccmZoneKey         = "topology.kubernetes.io/zone"
		ccmRegionKey       = "topology.kubernetes.io/region"
	)

	var (
		namespace         *corev1.Namespace
		clusterName       string
		clusterResources  *clusterctl.ApplyClusterTemplateAndWaitResult
		cancelWatches     context.CancelFunc
		testHelper        testHelperInterface
		expectedCCMLabels []string
	)

	BeforeEach(func() {
		testHelper = newTestHelper(e2eConfig)
		clusterName = testHelper.generateTestClusterName(specName)
		clusterResources = new(clusterctl.ApplyClusterTemplateAndWaitResult)
		Expect(bootstrapClusterProxy).NotTo(BeNil(), "BootstrapClusterProxy can't be nil")
		namespace, cancelWatches = setupSpecNamespace(ctx, specName, bootstrapClusterProxy, artifactFolder)
		expectedCCMLabels = []string{
			ccmZoneKey,
			ccmRegionKey,
			ccmInstanceTypeKey,
		}
	})

	AfterEach(func() {
		dumpSpecResourcesAndCleanup(ctx, specName, bootstrapClusterProxy, artifactFolder, namespace, cancelWatches, clusterResources.Cluster, e2eConfig.GetIntervals, skipCleanup)
	})

	It("Create a cluster with templating", func() {
		flavor = "image-lookup"
		Expect(namespace).NotTo(BeNil())

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

		By("Fetching workload proxy")
		workloadProxy := bootstrapClusterProxy.GetWorkloadCluster(ctx, namespace.Name, clusterResources.Cluster.Name)

		By("Checking if nodes have correct CCM labels")
		nodes, err := workloadProxy.GetClientSet().CoreV1().Nodes().List(ctx, metav1.ListOptions{})
		Expect(err).ToNot(HaveOccurred())
		for _, n := range nodes.Items {
			nodeLabels := n.Labels
			Expect(nodeLabels).To(gstruct.MatchKeys(gstruct.IgnoreExtras,
				gstruct.Keys{
					ccmInstanceTypeKey: Equal(ccmInstanceType),
				},
			))
			for _, k := range expectedCCMLabels {
				Expect(nodeLabels).To(HaveKey(k))
			}
		}

		By("PASSED!")
	})
})
