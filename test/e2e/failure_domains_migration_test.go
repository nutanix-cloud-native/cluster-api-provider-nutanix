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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gstruct"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	controlplanev1 "sigs.k8s.io/cluster-api/controlplane/kubeadm/api/v1beta1"
	"sigs.k8s.io/cluster-api/test/framework/clusterctl"
	"sigs.k8s.io/controller-runtime/pkg/client"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
)

var _ = Describe("Migrating nutanix failure domains", Label("capx-feature-test", "failure-domains-migration"), func() {
	const specName = "failure-domains-migration"

	var (
		namespace             *corev1.Namespace
		clusterName           string
		clusterResources      *clusterctl.ApplyClusterTemplateAndWaitResult
		cancelWatches         context.CancelFunc
		failureDomainNames    []string
		newFailureDomainNames []string
		testHelper            testHelperInterface
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
				expectedCondition: capiv1.Condition{
					Type:   infrav1.FailureDomainsValidatedCondition,
					Status: corev1.ConditionTrue,
				},
			})
		})

		By("Checking if machines are spread across failure domains", func() {
			testHelper.verifyLegacyFailureDomainsOnClusterMachines(ctx, verifyFailureDomainsOnClusterMachinesParams{
				clusterName:           clusterName,
				namespace:             namespace,
				bootstrapClusterProxy: bootstrapClusterProxy,
				failureDomainNames:    failureDomainNames,
			})
		})

		By("Creating Nutanix failure domain CRD", func() {
			nutanixCluster := testHelper.getNutanixClusterByName(ctx, getNutanixClusterByNameInput{
				Getter:    bootstrapClusterProxy.GetClient(),
				Name:      clusterName,
				Namespace: namespace.Name,
			})
			Expect(nutanixCluster).ToNot(BeNil())
			bootstrapClient := bootstrapClusterProxy.GetClient()
			for _, fd := range nutanixCluster.Spec.FailureDomains { //nolint:staticcheck // suppress complaining on Deprecated field

				clustersForFD := fd.Cluster.DeepCopy()
				subnetsForFD := []infrav1.NutanixResourceIdentifier{}
				for _, subnet := range fd.Subnets {
					subnetCopy := subnet.DeepCopy()
					subnetsForFD = append(subnetsForFD, *subnetCopy)
				}
				newFDName := fmt.Sprintf("%s-%s", clusterName, fd.Name)
				newFailureDomainNames = append(newFailureDomainNames, newFDName)
				nutanixFailureDomain := &infrav1.NutanixFailureDomain{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: namespace.Name,
						Name:      newFDName,
					},
					Spec: infrav1.NutanixFailureDomainSpec{
						PrismElementCluster: *clustersForFD,
						Subnets:             subnetsForFD,
					},
				}
				err := bootstrapClient.Create(ctx, nutanixFailureDomain)
				Expect(err).ToNot(HaveOccurred(), "expected to create failure domain object on workload")
			}
		})

		By("Updating the nutanixCluster object", func() {
			nutanixCluster := testHelper.getNutanixClusterByName(ctx, getNutanixClusterByNameInput{
				Getter:    bootstrapClusterProxy.GetClient(),
				Name:      clusterName,
				Namespace: namespace.Name,
			})
			Expect(nutanixCluster).ToNot(BeNil())
			bootstrapClient := bootstrapClusterProxy.GetClient()
			localRefs := []corev1.LocalObjectReference{}
			for _, name := range newFailureDomainNames {
				localRefs = append(localRefs, corev1.LocalObjectReference{Name: name})
			}
			nutanixCluster.Spec.FailureDomains = nil //nolint:staticcheck // suppress complaining on Deprecated field
			nutanixCluster.Spec.ControlPlaneFailureDomains = localRefs
			err := bootstrapClient.Update(ctx, nutanixCluster)
			Expect(err).To(BeNil())
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

		By("Forcing a kcp rollout", func() {
			bootstrapClient := bootstrapClusterProxy.GetClient()
			kcp := clusterResources.ControlPlane
			err := bootstrapClient.Get(ctx, client.ObjectKeyFromObject(kcp), kcp)
			Expect(err).To(BeNil())
			now := metav1.Now()
			kcpCopy := kcp.DeepCopy()
			kcpCopy.Spec.RolloutAfter = &now
			err = bootstrapClient.Update(ctx, kcpCopy)
			Expect(err).To(BeNil())

			Eventually(
				func() []capiv1.Condition {
					err := bootstrapClient.Get(ctx, client.ObjectKeyFromObject(kcp), kcp)
					Expect(err).To(BeNil())
					return kcp.Status.Conditions
				},
				time.Minute*15,
				defaultInterval,
			).Should(
				ContainElement(
					gstruct.MatchFields(
						gstruct.IgnoreExtras,
						gstruct.Fields{
							"Type":   Equal(controlplanev1.MachinesSpecUpToDateCondition),
							"Status": Equal(corev1.ConditionTrue),
						},
					),
				),
			)
		})

		By("Checking if machines are spread across failure domains", func() {
			testHelper.verifyNewFailureDomainsOnClusterMachines(ctx, verifyFailureDomainsOnClusterMachinesParams{
				clusterName:           clusterName,
				namespace:             namespace,
				bootstrapClusterProxy: bootstrapClusterProxy,
				failureDomainNames:    newFailureDomainNames,
			})
		})

		By("PASSED!")
	})
})
