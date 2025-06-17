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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/test/framework/clusterctl"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
)

const (
	nutanixFailureDomain1NameEnv = "NUTANIX_FAILURE_DOMAIN_1_NAME"
	nutanixFailureDomain2NameEnv = "NUTANIX_FAILURE_DOMAIN_2_NAME"
	nutanixFailureDomain3NameEnv = "NUTANIX_FAILURE_DOMAIN_3_NAME"
)

// Note: Still has "only-for-validation" label.
// Tests if:
//   - the control plane nodes are spread across the defined failure domains
//   - the VMs are deployed on the correct Prism Element cluster and subnet
//   - the correct failure domain conditions are applied to the nutanixCluster object
var _ = Describe("Nutanix failure domains", Label("capx-feature-test", "failure-domains", "only-for-validation"), func() {
	const specName = "failure-domains"

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
			testHelper.getVariableFromE2eConfig(nutanixFailureDomain2NameEnv),
			testHelper.getVariableFromE2eConfig(nutanixFailureDomain3NameEnv),
		}
		clusterName = testHelper.generateTestClusterName(specName)
		clusterResources = new(clusterctl.ApplyClusterTemplateAndWaitResult)
		Expect(bootstrapClusterProxy).NotTo(BeNil(), "BootstrapClusterProxy can't be nil")
		namespace, cancelWatches = setupSpecNamespace(ctx, specName, bootstrapClusterProxy, artifactFolder)
	})

	AfterEach(func() {
		dumpSpecResourcesAndCleanup(ctx, specName, bootstrapClusterProxy, artifactFolder, namespace, cancelWatches, clusterResources.Cluster, e2eConfig.GetIntervals, skipCleanup)
	})

	It("Create a cluster with multiple failure domains", func() {
		const flavor = "failure-domains"

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

		By("PASSED!")
	})
})
