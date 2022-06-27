//go:build e2e
// +build e2e

/*
Copyright 2021.

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

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/test/framework/clusterctl"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
)

var _ = Describe("Nutanix client [PR-Blocking]", func() {
	var (
		namespace        *corev1.Namespace
		specName         = "cluster-ntnx-client"
		clusterName      string
		clusterResources *clusterctl.ApplyClusterTemplateAndWaitResult
		cancelWatches    context.CancelFunc
	)
	BeforeEach(func() {
		clusterName = generateTestClusterName(specName)
		clusterResources = new(clusterctl.ApplyClusterTemplateAndWaitResult)
		Expect(bootstrapClusterProxy).NotTo(BeNil(), "BootstrapClusterProxy can't be nil")
		namespace, cancelWatches = setupSpecNamespace(ctx, specName, bootstrapClusterProxy, artifactFolder)
	})
	AfterEach(func() {
		dumpSpecResourcesAndCleanup(ctx, specName, bootstrapClusterProxy, artifactFolder, namespace, cancelWatches, clusterResources.Cluster, e2eConfig.GetIntervals, skipCleanup)
	})

	It("Create a cluster without credentialRef (use default credentials)", func() {
		flavor = "no-credential-ref"
		Expect(namespace).NotTo(BeNil())

		By("Creating a workload cluster")
		deployClusterAndWait(
			deployClusterParams{
				clusterName:           clusterName,
				namespace:             namespace,
				flavor:                flavor,
				clusterctlConfigPath:  clusterctlConfigPath,
				artifactFolder:        artifactFolder,
				bootstrapClusterProxy: bootstrapClusterProxy,
				e2eConfig:             *e2eConfig,
			}, clusterResources)

		By("checking cluster prism client init condition is true")
		verifyConditionOnNutanixCluster(verifyConditionOnNutanixClusterParams{
			clusterName:           clusterName,
			namespace:             namespace,
			bootstrapClusterProxy: bootstrapClusterProxy,
			expectedCondition: clusterv1.Condition{
				Type:   infrav1.PrismCentralClientCondition,
				Status: corev1.ConditionTrue,
			},
		})

		By("PASSED!")
	})

	It("Create a cluster without secret and add it later", func() {
		flavor = "no-secret"
		Expect(namespace).NotTo(BeNil())

		By("Creating a workload cluster")
		deployCluster(
			deployClusterParams{
				clusterName:           clusterName,
				namespace:             namespace,
				flavor:                flavor,
				clusterctlConfigPath:  clusterctlConfigPath,
				artifactFolder:        artifactFolder,
				bootstrapClusterProxy: bootstrapClusterProxy,
				e2eConfig:             *e2eConfig,
			}, clusterResources)

		By("Checking cluster condition for credentials is set to false")
		verifyConditionOnNutanixCluster(verifyConditionOnNutanixClusterParams{
			clusterName:           clusterName,
			namespace:             namespace,
			bootstrapClusterProxy: bootstrapClusterProxy,
			expectedCondition: clusterv1.Condition{
				Type:     infrav1.CredentialRefSecretOwnerSetCondition,
				Reason:   infrav1.CredentialRefSecretOwnerSetFailed,
				Severity: clusterv1.ConditionSeverityError,
				Status:   corev1.ConditionFalse,
			},
		})

		By("Creating secret using e2e credentials")
		nutanixCreds := getNutanixCredentialsFromEnvironment()
		createSecret(createSecretParams{
			username:    nutanixCreds.nutanixUsername,
			password:    nutanixCreds.nutanixPassword,
			namespace:   namespace,
			clusterName: clusterName,
		})

		By("checking cluster credential condition is true")
		verifyConditionOnNutanixCluster(verifyConditionOnNutanixClusterParams{
			clusterName:           clusterName,
			namespace:             namespace,
			bootstrapClusterProxy: bootstrapClusterProxy,
			expectedCondition: clusterv1.Condition{
				Type:   infrav1.CredentialRefSecretOwnerSetCondition,
				Status: corev1.ConditionTrue,
			},
		})

		By("checking cluster prism client init condition is true")
		verifyConditionOnNutanixCluster(verifyConditionOnNutanixClusterParams{
			clusterName:           clusterName,
			namespace:             namespace,
			bootstrapClusterProxy: bootstrapClusterProxy,
			expectedCondition: clusterv1.Condition{
				Type:   infrav1.PrismCentralClientCondition,
				Status: corev1.ConditionTrue,
			},
		})

		By("PASSED!")
	})
})
