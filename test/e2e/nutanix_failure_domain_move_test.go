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
	"fmt"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/cluster-api/test/framework"
	"sigs.k8s.io/cluster-api/test/framework/bootstrap"
	"sigs.k8s.io/cluster-api/test/framework/clusterctl"
	"sigs.k8s.io/controller-runtime/pkg/client"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
)

var _ = Describe("NutanixFailureDomain clusterctl move", Label("capx-feature-test", "clusterctl-move", "failure-domain"), func() {
	const specName = "nutanix-failure-domain-move"

	var (
		ctx               = context.TODO()
		targetCluster     framework.ClusterProxy
		targetProvider    bootstrap.ClusterProvider
		sourceNamespace   *corev1.Namespace
		targetNamespace   *corev1.Namespace
		failureDomainName string
		testHelper        testHelperInterface
	)

	BeforeEach(func() {
		testHelper = newTestHelper(e2eConfig)
		failureDomainName = fmt.Sprintf("test-fd-%s", testHelper.generateTestClusterName(specName))

		By("Creating target kind cluster")
		targetProvider = bootstrap.CreateKindBootstrapClusterAndLoadImages(ctx, bootstrap.CreateKindBootstrapClusterAndLoadImagesInput{
			Name:               "target-cluster",
			KubernetesVersion:  e2eConfig.MustGetVariable(KubernetesVersionManagement),
			RequiresDockerSock: e2eConfig.HasDockerProvider(),
			Images:             e2eConfig.Images,
			IPFamily:           e2eConfig.MustGetVariable(IPFamily),
			LogFolder:          filepath.Join(artifactFolder, "kind", "target"),
		})
		Expect(targetProvider).ToNot(BeNil())

		targetCluster = framework.NewClusterProxy("target", targetProvider.GetKubeconfigPath(), initScheme())
		Expect(targetCluster).ToNot(BeNil())

		By("Initializing target cluster with clusterctl")
		clusterctl.InitManagementClusterAndWatchControllerLogs(ctx, clusterctl.InitManagementClusterAndWatchControllerLogsInput{
			ClusterProxy:            targetCluster,
			ClusterctlConfigPath:    clusterctlConfigPath,
			InfrastructureProviders: e2eConfig.InfrastructureProviders(),
			LogFolder:               filepath.Join(artifactFolder, "clusters", "target"),
		}, e2eConfig.GetIntervals("target", "wait-controllers")...)

		By("Creating namespaces")
		// Use default namespace for simplicity
		sourceNamespace = &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: "default",
			},
		}

		targetNamespace = &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: "default",
			},
		}
	})

	AfterEach(func() {
		if !skipCleanup {
			By("Cleaning up target cluster")
			if targetProvider != nil {
				targetProvider.Dispose(ctx)
			}
		}
	})

	It("should successfully move NutanixFailureDomain between clusters", func() {
		By("Creating a NutanixFailureDomain in source cluster (bootstrap)")
		failureDomain := &infrav1.NutanixFailureDomain{
			ObjectMeta: metav1.ObjectMeta{
				Name:      failureDomainName,
				Namespace: sourceNamespace.Name,
			},
			Spec: infrav1.NutanixFailureDomainSpec{
				PrismElementCluster: infrav1.NutanixResourceIdentifier{
					Type: infrav1.NutanixIdentifierName,
					Name: stringPtr("test-cluster"),
				},
				Subnets: []infrav1.NutanixResourceIdentifier{
					{
						Type: infrav1.NutanixIdentifierName,
						Name: stringPtr("test-subnet"),
					},
				},
			},
		}

		Expect(bootstrapClusterProxy.GetClient().Create(ctx, failureDomain)).To(Succeed())

		By("Verifying NutanixFailureDomain exists in source cluster (bootstrap)")
		Eventually(func() error {
			fd := &infrav1.NutanixFailureDomain{}
			return bootstrapClusterProxy.GetClient().Get(ctx,
				client.ObjectKey{Name: failureDomainName, Namespace: sourceNamespace.Name}, fd)
		}, "30s", "1s").Should(Succeed())

		By("Verifying NutanixFailureDomain does not exist in target cluster")
		fd := &infrav1.NutanixFailureDomain{}
		err := targetCluster.GetClient().Get(ctx,
			client.ObjectKey{Name: failureDomainName, Namespace: targetNamespace.Name}, fd)
		Expect(err).To(HaveOccurred())

		By("Moving NutanixFailureDomain from source (bootstrap) to target cluster")
		clusterctl.Move(ctx, clusterctl.MoveInput{
			LogFolder:            filepath.Join(artifactFolder, "clusters"),
			ClusterctlConfigPath: clusterctlConfigPath,
			FromKubeconfigPath:   bootstrapClusterProxy.GetKubeconfigPath(),
			ToKubeconfigPath:     targetCluster.GetKubeconfigPath(),
			Namespace:            sourceNamespace.Name,
		})

		By("Verifying NutanixFailureDomain no longer exists in source cluster (bootstrap)")
		Eventually(func() bool {
			fd := &infrav1.NutanixFailureDomain{}
			err := bootstrapClusterProxy.GetClient().Get(ctx,
				client.ObjectKey{Name: failureDomainName, Namespace: sourceNamespace.Name}, fd)
			return err != nil
		}, "30s", "1s").Should(BeTrue())

		By("Verifying NutanixFailureDomain now exists in target cluster")
		Eventually(func() error {
			fd := &infrav1.NutanixFailureDomain{}
			return targetCluster.GetClient().Get(ctx,
				client.ObjectKey{Name: failureDomainName, Namespace: targetNamespace.Name}, fd)
		}, "30s", "1s").Should(Succeed())

		By("Verifying moved NutanixFailureDomain has correct spec")
		fd = &infrav1.NutanixFailureDomain{}
		Expect(targetCluster.GetClient().Get(ctx,
			client.ObjectKey{Name: failureDomainName, Namespace: targetNamespace.Name}, fd)).To(Succeed())

		Expect(fd.Spec.PrismElementCluster.Name).To(Equal(stringPtr("test-cluster")))
		Expect(fd.Spec.Subnets).To(HaveLen(1))
		Expect(fd.Spec.Subnets[0].Name).To(Equal(stringPtr("test-subnet")))

		By("PASSED!")
	})
})

func stringPtr(s string) *string {
	return &s
}
