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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/pointer"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/test/framework/clusterctl"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
)

var _ = Describe("Nutanix projects", Label("capx-feature-test", "projects"), func() {
	const (
		specName               = "cluster-projects"
		nonExistingProjectName = "nonExistingProjectNameCAPX"
	)

	var (
		namespace          *corev1.Namespace
		clusterName        string
		clusterResources   *clusterctl.ApplyClusterTemplateAndWaitResult
		cancelWatches      context.CancelFunc
		nutanixProjectName string
		testHelper         testHelperInterface
	)

	BeforeEach(func() {
		testHelper = newTestHelper(e2eConfig)
		nutanixProjectName = testHelper.getVariableFromE2eConfig(nutanixProjectNameEnv)
		clusterName = testHelper.generateTestClusterName(specName)
		clusterResources = new(clusterctl.ApplyClusterTemplateAndWaitResult)
		Expect(bootstrapClusterProxy).NotTo(BeNil(), "BootstrapClusterProxy can't be nil")
		namespace, cancelWatches = setupSpecNamespace(ctx, specName, bootstrapClusterProxy, artifactFolder)
	})

	AfterEach(func() {
		dumpSpecResourcesAndCleanup(ctx, specName, bootstrapClusterProxy, artifactFolder, namespace, cancelWatches, clusterResources.Cluster, e2eConfig.GetIntervals, skipCleanup)
	})

	It("Create a cluster linked to non-existing project (should fail)", func() {
		const flavor = "no-nmt"

		Expect(namespace).NotTo(BeNil())

		By("Creating invalid Project Nutanix Machine Template", func() {
			invalidProjectNMT := testHelper.createDefaultNMT(clusterName, namespace.Name)
			invalidProjectNMT.Spec.Template.Spec.Project = &infrav1.NutanixResourceIdentifier{
				Type: "name",
				Name: pointer.String(nonExistingProjectName),
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
				},
				clusterResources,
			)
		})

		By("Checking project assigned condition is false", func() {
			testHelper.verifyConditionOnNutanixMachines(verifyConditionParams{
				clusterName:           clusterName,
				namespace:             namespace,
				bootstrapClusterProxy: bootstrapClusterProxy,
				expectedCondition: clusterv1.Condition{
					Type:     infrav1.ProjectAssignedCondition,
					Reason:   infrav1.ProjectAssignationFailed,
					Severity: clusterv1.ConditionSeverityError,
					Status:   corev1.ConditionFalse,
				},
			})
		})

		By("Checking machine status is 'Failed' and failure message is set", func() {
			testHelper.verifyFailureMessageOnClusterMachines(ctx, verifyFailureMessageOnClusterMachinesParams{
				clusterName:            clusterName,
				namespace:              namespace,
				expectedPhase:          "Failed",
				expectedFailureMessage: "failed to retrieve project",
				bootstrapClusterProxy:  bootstrapClusterProxy,
			})
		})

		By("PASSED!")
	})

	It("Create a cluster linked to an existing project", func() {
		flavor = "project"

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

		By("Checking project assigned condition is true", func() {
			testHelper.verifyConditionOnNutanixMachines(verifyConditionParams{
				clusterName:           clusterName,
				namespace:             namespace,
				bootstrapClusterProxy: bootstrapClusterProxy,
				expectedCondition: clusterv1.Condition{
					Type:   infrav1.ProjectAssignedCondition,
					Status: corev1.ConditionTrue,
				},
			})
		})

		By("Verifying if project is assigned to the VMs")
		Expect(nutanixProjectName).ToNot(BeEmpty())
		testHelper.verifyProjectNutanixMachines(ctx, verifyProjectNutanixMachinesParams{
			clusterName:           clusterName,
			namespace:             namespace.Name,
			nutanixProjectName:    nutanixProjectName,
			bootstrapClusterProxy: bootstrapClusterProxy,
		})

		By("PASSED!")
	})

	It("Create a cluster linked to an existing project by UUID", Label("uuid"), func() {
		flavor = "no-nmt"
		Expect(namespace).NotTo(BeNil())

		By("Creating Nutanix Machine Template with UUIDs", func() {
			uuidNMT := testHelper.createUUIDProjectNMT(ctx, clusterName, namespace.Name)
			testHelper.createCapiObject(ctx, createCapiObjectParams{
				creator:    bootstrapClusterProxy.GetClient(),
				capiObject: uuidNMT,
			})
		})

		By("Creating a workload cluster linked to a project")
		testHelper.deployClusterAndWait(
			deployClusterParams{
				clusterName:           clusterName,
				namespace:             namespace,
				flavor:                flavor,
				clusterctlConfigPath:  clusterctlConfigPath,
				artifactFolder:        artifactFolder,
				bootstrapClusterProxy: bootstrapClusterProxy,
			}, clusterResources)

		By("Checking project assigned condition is true", func() {
			testHelper.verifyConditionOnNutanixMachines(verifyConditionParams{
				clusterName:           clusterName,
				namespace:             namespace,
				bootstrapClusterProxy: bootstrapClusterProxy,
				expectedCondition: clusterv1.Condition{
					Type:   infrav1.ProjectAssignedCondition,
					Status: corev1.ConditionTrue,
				},
			})
		})

		By("Verifying if project is assigned to the VMs")
		Expect(nutanixProjectName).ToNot(BeEmpty())
		testHelper.verifyProjectNutanixMachines(ctx, verifyProjectNutanixMachinesParams{
			clusterName:           clusterName,
			namespace:             namespace.Name,
			nutanixProjectName:    nutanixProjectName,
			bootstrapClusterProxy: bootstrapClusterProxy,
		})

		By("PASSED!")
	})
})
