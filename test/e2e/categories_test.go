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
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	clusterv1beta1 "sigs.k8s.io/cluster-api/api/core/v1beta1" //nolint:staticcheck // suppress complaining on Deprecated type
	"sigs.k8s.io/cluster-api/test/framework/clusterctl"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
)

const (
	defaultNonExistingAdditionalCategoryKey   = "nonExistingCategoryKeyCAPX"
	defaultNonExistingAdditionalCategoryValue = "nonExistingCategoryValueCAPX"
)

var _ = Describe("Nutanix categories", Label("nutanix-feature-test", "categories"), func() {
	const (
		specName = "cluster-categories"
	)

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

	It("Create a cluster with default cluster categories (no additional categories)", func() {
		Expect(namespace).NotTo(BeNil())
		flavor := clusterctl.DefaultFlavor
		expectedClusterNameCategoryKey := infrav1.DefaultCAPICategoryKeyForName
		By("Creating a workload cluster (no additional categories)", func() {
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

		By("Checking cluster category condition is true", func() {
			testHelper.verifyConditionOnNutanixCluster(verifyConditionParams{
				clusterName:           clusterName,
				namespace:             namespace,
				bootstrapClusterProxy: bootstrapClusterProxy,
				expectedCondition: clusterv1beta1.Condition{
					Type:   infrav1.ClusterCategoryCreatedCondition,
					Status: corev1.ConditionTrue,
				},
			})
		})

		By("Checking if a category was created", func() {
			testHelper.verifyCategoryExists(ctx, expectedClusterNameCategoryKey, clusterName)
		})

		By("Checking if there are VMs assigned to this category", func() {
			expectedCategories := map[string][]string{
				expectedClusterNameCategoryKey: {clusterName},
			}
			testHelper.verifyCategoriesNutanixMachines(ctx, clusterName, namespace.Name, expectedCategories)
		})

		By("PASSED!")
	})

	It("Create a cluster with additional categories", func() {
		Expect(namespace).NotTo(BeNil())
		flavor := "additional-categories"

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

		By("Verify if additional categories are assigned to the vms", func() {
			expectedClusterNameCategoryKey := infrav1.DefaultCAPICategoryKeyForName
			expectedCategories := map[string][]string{
				expectedClusterNameCategoryKey: {clusterName},
				"AppType":                      {"Kubernetes"},
				"Environment":                  {"Dev", "Testing"},
			}

			testHelper.verifyCategoriesNutanixMachines(ctx, clusterName, namespace.Name, expectedCategories)
		})

		By("PASSED!")
	})

	It("Create a cluster linked to non-existing categories (should fail)", func() {
		flavor = "no-nmt"
		Expect(namespace).NotTo(BeNil())

		By("Creating Nutanix Machine Template with invalid categories", func() {
			invalidProjectNMT := testHelper.createDefaultNMT(clusterName, namespace.Name)
			invalidProjectNMT.Spec.Template.Spec.AdditionalCategories = []infrav1.NutanixCategoryIdentifier{
				{
					Key:   defaultNonExistingAdditionalCategoryKey,
					Value: defaultNonExistingAdditionalCategoryValue,
				},
			}
			testHelper.createCapiObject(ctx, createCapiObjectParams{
				creator:    bootstrapClusterProxy.GetClient(),
				capiObject: invalidProjectNMT,
			})
		})

		By("Creating a workload cluster", func() {
			testHelper.deployCluster(
				deployClusterParams{
					clusterName:           clusterName,
					namespace:             namespace,
					flavor:                flavor,
					clusterctlConfigPath:  clusterctlConfigPath,
					artifactFolder:        artifactFolder,
					bootstrapClusterProxy: bootstrapClusterProxy,
				}, clusterResources)
		})

		By("Checking machine status is 'Failed' and failure message is set", func() {
			testHelper.verifyFailureMessageOnClusterMachines(ctx, verifyFailureMessageOnClusterMachinesParams{
				clusterName:            clusterName,
				namespace:              namespace,
				expectedPhase:          "Failed",
				expectedFailureMessage: "not found in category",
				bootstrapClusterProxy:  bootstrapClusterProxy,
			})
		})

		By("PASSED!")
	})

	It("Create and delete 2 clusters with same name with default cluster categories and should succeed", Label("same-name-clusters"), func() {
		Expect(namespace).NotTo(BeNil())
		flavor := clusterctl.DefaultFlavor
		expectedClusterNameCategoryKey := infrav1.DefaultCAPICategoryKeyForName
		By("Creating a workload cluster 1", func() {
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

		By("Checking cluster category condition is true", func() {
			testHelper.verifyConditionOnNutanixCluster(verifyConditionParams{
				clusterName:           clusterName,
				namespace:             namespace,
				bootstrapClusterProxy: bootstrapClusterProxy,
				expectedCondition: clusterv1beta1.Condition{
					Type:   infrav1.ClusterCategoryCreatedCondition,
					Status: corev1.ConditionTrue,
				},
			})
		})

		By("Checking if a category was created", func() {
			testHelper.verifyCategoryExists(ctx, expectedClusterNameCategoryKey, clusterName)
		})

		By("Checking if there are VMs assigned to this category", func() {
			expectedCategories := map[string][]string{
				expectedClusterNameCategoryKey: {clusterName},
			}
			testHelper.verifyCategoriesNutanixMachines(ctx, clusterName, namespace.Name, expectedCategories)
		})
		var cp1EndpointIP string
		By("Setting different Control plane endpoint IP for 2nd cluster", func() {
			cp1EndpointIP = testHelper.getVariableFromE2eConfig("CONTROL_PLANE_ENDPOINT_IP")
			cp2EndpointIP := testHelper.getVariableFromE2eConfig("CONTROL_PLANE_ENDPOINT_IP_WORKLOAD_CLUSTER")
			if cp2EndpointIP == "" {
				cp2EndpointIP = os.Getenv("CONTROL_PLANE_ENDPOINT_IP_WORKLOAD_CLUSTER")
				if cp2EndpointIP == "" {
					Fail("CONTROL_PLANE_ENDPOINT_IP_WORKLOAD_CLUSTER not set")
				}
			}
			testHelper.updateVariableInE2eConfig("CONTROL_PLANE_ENDPOINT_IP", cp2EndpointIP)
		})

		var (
			namespace2        *corev1.Namespace
			clusterResources2 *clusterctl.ApplyClusterTemplateAndWaitResult
			cancelWatches2    context.CancelFunc
		)
		By("configure env for 2nd cluster", func() {
			clusterResources2 = new(clusterctl.ApplyClusterTemplateAndWaitResult)
			namespace2, cancelWatches2 = setupSpecNamespace(ctx, specName+"-2", bootstrapClusterProxy, artifactFolder)
		})

		By("Creating a workload cluster 2 with same name", func() {
			testHelper.deployClusterAndWait(
				deployClusterParams{
					clusterName:           clusterName,
					namespace:             namespace2,
					flavor:                flavor,
					clusterctlConfigPath:  clusterctlConfigPath,
					artifactFolder:        artifactFolder,
					bootstrapClusterProxy: bootstrapClusterProxy,
				}, clusterResources2)
		})

		By("Checking cluster category condition is true", func() {
			testHelper.verifyConditionOnNutanixCluster(verifyConditionParams{
				clusterName:           clusterName,
				namespace:             namespace2,
				bootstrapClusterProxy: bootstrapClusterProxy,
				expectedCondition: clusterv1beta1.Condition{
					Type:   infrav1.ClusterCategoryCreatedCondition,
					Status: corev1.ConditionTrue,
				},
			})
		})

		By("Checking if a category was created", func() {
			testHelper.verifyCategoryExists(ctx, expectedClusterNameCategoryKey, clusterName)
		})

		By("Checking if there are VMs assigned to this category", func() {
			expectedCategories := map[string][]string{
				expectedClusterNameCategoryKey: {clusterName},
			}
			testHelper.verifyCategoriesNutanixMachines(ctx, clusterName, namespace2.Name, expectedCategories)
		})

		By("Delete 2nd cluster and its namespace", func() {
			dumpSpecResourcesAndCleanup(ctx, specName, bootstrapClusterProxy, artifactFolder, namespace2, cancelWatches2, clusterResources2.Cluster, e2eConfig.GetIntervals, skipCleanup)
		})

		By("Set original Control plane endpoint IP", func() {
			testHelper.updateVariableInE2eConfig("CONTROL_PLANE_ENDPOINT_IP", cp1EndpointIP)
		})

		// AfterEach section will take care of deleting the first cluster
	})
})
