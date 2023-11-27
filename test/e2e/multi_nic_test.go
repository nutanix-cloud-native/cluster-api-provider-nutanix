//go:build e2e

/*
Copyright 2022 Nutanix

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
	"github.com/onsi/gomega/types"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/cluster-api/test/framework/clusterctl"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/infrastructure/v1beta1"
)

const (
	additionalSubnetVarKey = "NUTANIX_ADDITIONAL_SUBNET_NAME"
)

var _ = Describe("Nutanix Subnets", Label("capx-feature-test", "multi-nic", "slow", "network"), func() {
	const specName = "cluster-multi-nic"

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

	It("Create a cluster where machines have multiple subnets attached to the machines", func() {
		const (
			flavor = "no-nmt"
		)

		var nmtSubnets []infrav1.NutanixResourceIdentifier

		Expect(namespace).NotTo(BeNil())

		By("Creating Nutanix Machine Template with multiple subnets", func() {
			multiNicNMT := testHelper.createDefaultNMT(clusterName, namespace.Name)
			multiNicNMT.Spec.Template.Spec.Subnets = append(multiNicNMT.Spec.Template.Spec.Subnets,
				testHelper.getNutanixResourceIdentifierFromE2eConfig(additionalSubnetVarKey),
			)
			nmtSubnets = multiNicNMT.Spec.Template.Spec.Subnets
			testHelper.createCapiObject(ctx, createCapiObjectParams{
				creator:    bootstrapClusterProxy.GetClient(),
				capiObject: multiNicNMT,
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
				}, clusterResources)
		})

		By("Checking subnets attached to cluster machines", func() {
			toMatchFields := make([]types.GomegaMatcher, 0)
			for _, s := range nmtSubnets {
				toMatchFields = append(toMatchFields, gstruct.PointTo(
					gstruct.MatchFields(
						gstruct.IgnoreExtras,
						gstruct.Fields{
							"SubnetReference": gstruct.PointTo(
								gstruct.MatchFields(
									gstruct.IgnoreExtras,
									gstruct.Fields{
										"Name": gstruct.PointTo(Equal(*s.Name)),
									},
								),
							),
						},
					),
				))
			}

			nutanixVMs := testHelper.getNutanixVMsForCluster(ctx, clusterName, namespace.Name)
			for _, vm := range nutanixVMs {
				vmSubnets := vm.Status.Resources.NicList
				Expect(len(vmSubnets)).To(Equal(len(nmtSubnets)), "expected amount subnets linked to VMs to be equal to %d but was %d", len(nmtSubnets), len(vmSubnets))
				Expect(vmSubnets).Should(ContainElements(toMatchFields))
			}
		})

		By("PASSED!")
	})
})
