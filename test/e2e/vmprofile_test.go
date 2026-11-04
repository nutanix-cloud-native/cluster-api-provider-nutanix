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
)

var _ = Describe("Nutanix VMProfile", Label("nutanix-feature-test", "vmprofile", "only-for-validation"), func() {
	const (
		specName = "cluster-vmprofile"
	)

	var (
		namespace        *corev1.Namespace
		clusterName      string
		clusterResources *clusterctl.ApplyClusterTemplateAndWaitResult
		cancelWatches    context.CancelFunc
		vmProfileName    string
		testHelper       testHelperInterface
	)

	BeforeEach(func() {
		testHelper = newTestHelper(e2eConfig)
		vmProfileName = testHelper.getVariableFromE2eConfig("NUTANIX_VM_PROFILE_NAME")
		clusterName = testHelper.generateTestClusterName(specName)
		clusterResources = new(clusterctl.ApplyClusterTemplateAndWaitResult)
		Expect(bootstrapClusterProxy).NotTo(BeNil(), "BootstrapClusterProxy can't be nil")
		namespace, cancelWatches = setupSpecNamespace(ctx, specName, bootstrapClusterProxy, artifactFolder)
	})

	AfterEach(func() {
		dumpSpecResourcesAndCleanup(ctx, specName, bootstrapClusterProxy, artifactFolder, namespace, cancelWatches, clusterResources.Cluster, e2eConfig.GetIntervals, skipCleanup)
	})

	It("Create a cluster using VMProfile", func() {
		flavor = "vmprofile"
		Expect(namespace).NotTo(BeNil())
		Expect(vmProfileName).ToNot(BeEmpty(), "NUTANIX_VM_PROFILE_NAME must be set")

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

		By("Verifying cluster was created successfully with VMProfile", func() {
			Expect(clusterResources.Cluster).NotTo(BeNil())
			// deployClusterAndWait already ensures the cluster is provisioned
		})

		By("PASSED!")
	})
})
