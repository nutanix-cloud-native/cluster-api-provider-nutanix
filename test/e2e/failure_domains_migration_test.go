//go:build e2e
// +build e2e

/*
Copyright 2023 Nutanix

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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/test/framework/clusterctl"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
)

var _ = Describe("Migrating nutanix failure domains", Label("capx-feature-test", "failure-domains-migration"), func() {
	const specName = "failure-domains-migration"

	var (
		namespace          *corev1.Namespace
		clusterName        string
		clusterResources   *clusterctl.ApplyClusterTemplateAndWaitResult
		cancelWatches      context.CancelFunc
		failureDomainNames []string
		testHelper         testHelperInterface
	)

	BeforeEach(func() {
		testHelper = newTestHelper(e2eConfig)
		failureDomainNames = []string{
			testHelper.getVariableFromE2eConfig(nutanixFailureDomain1NameEnv),
		}
		clusterName = testHelper.generateTestClusterName(specName)
		clusterResources = new(clusterctl.ApplyClusterTemplateAndWaitResult)
		Expect(bootstrapClusterProxy).NotTo(BeNil(), "BootstrapClusterProxy can't be nil")
		namespace, cancelWatches = setupSpecNamespace(ctx, specName, bootstrapClusterProxy, artifactFolder)
	})

	AfterEach(func() {
		dumpSpecResourcesAndCleanup(ctx, specName, bootstrapClusterProxy, artifactFolder, namespace, cancelWatches, clusterResources.Cluster, e2eConfig.GetIntervals, skipCleanup)
	})

	It("Create a cluster with old failure domains", func() {
		const flavor = "failure-domains-migration"

		Expect(namespace).NotTo(BeNil())

		By("Creating a workload cluster")
		testHelper.deployClusterAndWait(
			deployClusterParams{
				clusterName:           clusterName,
				namespace:             namespace,
				flavor:                flavor,
				clusterctlConfigPath:  clusterctlConfigPath,
				artifactFolder:        artifactFolder,
				bootstrapClusterProxy: bootstrapClusterProxy,
			}, clusterResources)

		By("Checking failure domain condition is true", func() {
			testHelper.verifyConditionOnNutanixCluster(verifyConditionParams{
				clusterName:           clusterName,
				namespace:             namespace,
				bootstrapClusterProxy: bootstrapClusterProxy,
				expectedCondition: clusterv1.Condition{
					Type:   infrav1.FailureDomainsValidatedCondition,
					Status: corev1.ConditionTrue,
				},
			})
		})

		By("Checking if machines are spread across failure domains", func() {
			testHelper.verifyFailureDomainsOnClusterMachines(ctx, verifyFailureDomainsOnClusterMachinesParams{
				clusterName:           clusterName,
				namespace:             namespace,
				bootstrapClusterProxy: bootstrapClusterProxy,
				failureDomainNames:    failureDomainNames,
			})
		})

		By("Creating Nutanix failure domain CRD and removing the old field", func() {
			nutanixCluster := testHelper.getNutanixClusterByName(ctx, getNutanixClusterByNameInput{
				Getter:    bootstrapClusterProxy.GetClient(),
				Name:      clusterName,
				Namespace: namespace.Name,
			})
			Expect(nutanixCluster).ToNot(BeNil())
			bootstrapClient := bootstrapClusterProxy.GetClient()
			for _, fd := range nutanixCluster.Spec.FailureDomains {
				clustersForFD := fd.Cluster.DeepCopy()
				subnetsForFD := []infrav1.NutanixResourceIdentifier{}
				for _, subnet := range fd.Subnets {
					subnetCopy := subnet.DeepCopy()
					subnetsForFD = append(subnetsForFD, *subnetCopy)
				}
				nutanixFailureDomain := &infrav1.NutanixFailureDomain{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: namespace.Name,
						Name:      fmt.Sprintf("%s-%s", clusterName, fd.Name),
					},
					Spec: infrav1.NutanixFailureDomainSpec{
						PrismElementCluster: *clustersForFD,
						Subnets:             subnetsForFD,
					},
				}
				err := bootstrapClient.Create(ctx, nutanixFailureDomain)
				Expect(err).ToNot(HaveOccurred(), "expected to create failure domain object on workload")
			}
			nutanixCluster.Spec.FailureDomains = nil
			bootstrapClient.Update(ctx, nutanixCluster)
		})

		By("Verifying if validated failure domains condition is set on cluster", func() {
			testHelper.verifyConditionOnNutanixCluster(verifyConditionParams{
				bootstrapClusterProxy,
				clusterName,
				namespace,
				capiv1.Condition{
					Type:   infrav1.FailureDomainsValidatedCondition,
					Status: corev1.ConditionTrue,
				},
			})
		})

		By("PASSED!")
	})
})
