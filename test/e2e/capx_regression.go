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

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/test/framework"
	"sigs.k8s.io/cluster-api/test/framework/clusterctl"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Nutanix regression tests", Label("capx-regression-test", "regression", "slow", "network"), func() {
	const specName = "capx-regression"

	var (
		namespace        *corev1.Namespace
		clusterName      string
		clusterResources *clusterctl.ApplyClusterTemplateAndWaitResult
		cancelWatches    context.CancelFunc
		testHelper       testHelperInterface
	)

	BeforeEach(func() {
		Expect(e2eConfig).NotTo(BeNil(), "E2EConfig can't be nil")

		testHelper = newTestHelper(e2eConfig)
		clusterName = testHelper.generateTestClusterName(specName)
		clusterResources = new(clusterctl.ApplyClusterTemplateAndWaitResult)

		Expect(bootstrapClusterProxy).NotTo(BeNil(), "BootstrapClusterProxy can't be nil")
		Expect(artifactFolder).NotTo(BeEmpty(), "ArtifactFolder can't be empty")

		namespace, cancelWatches = setupSpecNamespace(ctx, specName, bootstrapClusterProxy, artifactFolder)

		Expect(clusterctlConfigPath).NotTo(BeEmpty(), "ClusterctlConfigPath can't be empty")
	})

	AfterEach(func() {
		bcpClient := bootstrapClusterProxy.GetClient()
		myCluster := &clusterv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterName,
				Namespace: namespace.Name,
			},
		}

		// Delete the cluster
		// Note: We are not using the framework.DeleteClusterAndWait function here because we want to
		// succeed even if the cluster is already deleted.
		// This is because the cluster may have been deleted by the test itself, and we don't want to
		// fail the test in that case.
		Byf("Deleting cluster %s/%s", namespace.Name, clusterName)
		Expect(bcpClient.Delete(ctx, myCluster)).To(SatisfyAny(Succeed(), MatchError(ContainSubstring("not found"))))

		Byf("Waiting for cluster %s/%s to be deleted", namespace.Name, clusterName)
		framework.WaitForClusterDeleted(ctx, framework.WaitForClusterDeletedInput{
			Getter:  bcpClient,
			Cluster: myCluster,
		}, e2eConfig.GetIntervals(specName, "wait-delete-cluster")...)

		Byf("Deleting namespace used for hosting the %q test spec", specName)
		framework.DeleteNamespace(ctx, framework.DeleteNamespaceInput{
			Deleter: bcpClient,
			Name:    myCluster.Namespace,
		})

		cancelWatches()
	})

	It("[Test regression: credentials-delete-race-regression] Create a cluster and delete the credentials secret before delete the cluster", func() {
		Expect(namespace).ToNot(BeNil(), "Namespace can't be nil")
		Expect(clusterName).ToNot(BeNil(), "ClusterName can't be nil")

		By("Create and deploy secret using e2e credentials", func() {
			up := getBaseAuthCredentials(*e2eConfig)
			testHelper.createSecret(createSecretParams{
				username:    up.username,
				password:    up.password,
				namespace:   namespace,
				clusterName: clusterName,
			})
		})

		By("Create and deploy Nutanix cluster", func() {
			flavor := clusterctl.DefaultFlavor

			deployParams := deployClusterParams{
				clusterName:           clusterName,
				namespace:             namespace,
				flavor:                flavor,
				clusterctlConfigPath:  clusterctlConfigPath,
				artifactFolder:        artifactFolder,
				bootstrapClusterProxy: bootstrapClusterProxy,
			}

			testHelper.deployClusterAndWait(deployParams, clusterResources)
		})

		By("Checking cluster credential condition is true", func() {
			testHelper.verifyConditionOnNutanixCluster(verifyConditionParams{
				clusterName:           clusterName,
				namespace:             namespace,
				bootstrapClusterProxy: bootstrapClusterProxy,
				expectedCondition: clusterv1.Condition{
					Type:   infrav1.CredentialRefSecretOwnerSetCondition,
					Status: corev1.ConditionTrue,
				},
			})
		})

		By("Checking cluster prism client init condition is true", func() {
			testHelper.verifyConditionOnNutanixCluster(verifyConditionParams{
				clusterName:           clusterName,
				namespace:             namespace,
				bootstrapClusterProxy: bootstrapClusterProxy,
				expectedCondition: clusterv1.Condition{
					Type:   infrav1.PrismCentralClientCondition,
					Status: corev1.ConditionTrue,
				},
			})
		})

		By("Dumping cluster resources", func() {
			dumpSpecResourcesAndCleanup(ctx, specName, bootstrapClusterProxy, artifactFolder, namespace, cancelWatches, clusterResources.Cluster, e2eConfig.GetIntervals, true)
		})

		By("Deleting the secret", func() {
			testHelper.deleteSecret(deleteSecretParams{
				namespace:   namespace,
				clusterName: clusterName,
			})
		})

		By("Deleting the cluster", func() {
			Byf("Deleting cluster %s/%s", namespace.Name, clusterName)
			framework.DeleteCluster(ctx, framework.DeleteClusterInput{
				Deleter: bootstrapClusterProxy.GetClient(),
				Cluster: &clusterv1.Cluster{
					ObjectMeta: metav1.ObjectMeta{
						Name:      clusterName,
						Namespace: namespace.Name,
					},
				},
			})

			Byf("Waiting for cluster %s/%s to be deleted", namespace.Name, clusterName)
			framework.WaitForClusterDeleted(ctx, framework.WaitForClusterDeletedInput{
				Getter: bootstrapClusterProxy.GetClient(),
				Cluster: &clusterv1.Cluster{
					ObjectMeta: metav1.ObjectMeta{
						Name:      clusterName,
						Namespace: namespace.Name,
					},
				},
			}, e2eConfig.GetIntervals(specName, "wait-delete-cluster")...)
		})

		// Check if secret is deleted
		By("Checking if secret is deleted", func() {
			err := bootstrapClusterProxy.GetClient().Get(ctx,
				client.ObjectKey{
					Name:      clusterName,
					Namespace: namespace.Name,
				}, &corev1.Secret{})
			Expect(err).To(HaveOccurred())
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})

		// Check if cluster is deleted
		By("Checking if cluster is deleted", func() {
			err := bootstrapClusterProxy.GetClient().Get(ctx,
				client.ObjectKey{
					Name:      clusterName,
					Namespace: namespace.Name,
				}, &clusterv1.Cluster{})
			Expect(err).To(HaveOccurred())
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})

		By("PASSED!")
	})
})
