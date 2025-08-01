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
	"fmt"
	"strconv"

	credentialTypes "github.com/nutanix-cloud-native/prism-go-client/environment/credentials"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/test/framework/clusterctl"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
)

var _ = Describe("Nutanix client", Label("capx-feature-test", "nutanix-client"), func() {
	const (
		specName = "cluster-ntnx-client"

		controlplaneEndpointIPKey       = "CONTROL_PLANE_ENDPOINT_IP"
		controlplaneEndpointPortKey     = "CONTROL_PLANE_ENDPOINT_PORT"
		defaultControlPlaneEndpointPort = 6443
	)

	var (
		namespace        *corev1.Namespace
		clusterName      string
		clusterResources *clusterctl.ApplyClusterTemplateAndWaitResult
		cancelWatches    context.CancelFunc
		testHelper       testHelperInterface

		controlplaneEndpointIP   string
		controlplaneEndpointPort int32
	)

	BeforeEach(func() {
		testHelper = newTestHelper(e2eConfig)
		clusterName = testHelper.generateTestClusterName(specName)
		clusterResources = new(clusterctl.ApplyClusterTemplateAndWaitResult)
		Expect(bootstrapClusterProxy).NotTo(BeNil(), "BootstrapClusterProxy can't be nil")
		namespace, cancelWatches = setupSpecNamespace(ctx, specName, bootstrapClusterProxy, artifactFolder)
		controlplaneEndpointIP = testHelper.getVariableFromE2eConfig(controlplaneEndpointIPKey)
		controlplaneEndpointPort = defaultControlPlaneEndpointPort
		if e2eConfig.HasVariable(controlplaneEndpointPortKey) {
			controlplaneEndpointPortInt, err := strconv.Atoi(e2eConfig.MustGetVariable(controlplaneEndpointPortKey))
			Expect(err).ToNot(HaveOccurred())
			controlplaneEndpointPort = int32(controlplaneEndpointPortInt)
		}
	})

	AfterEach(func() {
		dumpSpecResourcesAndCleanup(ctx, specName, bootstrapClusterProxy, artifactFolder, namespace, cancelWatches, clusterResources.Cluster, e2eConfig.GetIntervals, skipCleanup)
	})

	// credentialRef is a mandatory parameters for the prismCentral attribute
	It("Create a cluster without credentialRef (should fail)", func() {
		flavor = "no-nutanix-cluster"
		Expect(namespace).NotTo(BeNil())

		By("Creating NutanixCluster resource without credentialRef", func() {
			ntnxCluster := testHelper.createDefaultNutanixCluster(
				clusterName,
				namespace.Name,
				controlplaneEndpointIP,
				controlplaneEndpointPort,
			)

			ntnxCreds, err := getNutanixCredentials(*e2eConfig)
			Expect(err).ToNot(HaveOccurred())

			ntnxPort, err := strconv.Atoi(ntnxCreds.Port)
			Expect(err).ToNot(HaveOccurred())

			ntnxCluster.Spec.PrismCentral = &credentialTypes.NutanixPrismEndpoint{
				Address:  ntnxCreds.Endpoint,
				Port:     int32(ntnxPort),
				Insecure: ntnxCreds.Insecure,
			}
			testHelper.createCapiObject(ctx, createCapiObjectParams{
				creator:    bootstrapClusterProxy.GetClient(),
				capiObject: ntnxCluster,
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

		By("Checking CredentialRefSecretOwnerSet condition is false", func() {
			testHelper.verifyConditionOnNutanixCluster(verifyConditionParams{
				clusterName:           clusterName,
				namespace:             namespace,
				bootstrapClusterProxy: bootstrapClusterProxy,
				expectedCondition: clusterv1.Condition{
					Type:     infrav1.CredentialRefSecretOwnerSetCondition,
					Status:   corev1.ConditionFalse,
					Reason:   infrav1.CredentialRefSecretOwnerSetFailed,
					Severity: clusterv1.ConditionSeverityError,
				},
			})
		})

		By("PASSED!")
	})

	It("Create a cluster without prismCentral attribute (use default credentials)", func() {
		flavor = "no-nutanix-cluster"
		Expect(namespace).NotTo(BeNil())

		By("Creating NutanixCluster resource without credentialRef", func() {
			ntnxCluster := testHelper.createDefaultNutanixCluster(
				clusterName,
				namespace.Name,
				controlplaneEndpointIP,
				controlplaneEndpointPort,
			)

			testHelper.createCapiObject(ctx, createCapiObjectParams{
				creator:    bootstrapClusterProxy.GetClient(),
				capiObject: ntnxCluster,
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

		By("PASSED!")
	})

	It("Create a cluster without secret and add it later", func() {
		flavor = "no-secret"
		Expect(namespace).NotTo(BeNil())

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

		By("Checking cluster condition for credentials is set to false", func() {
			testHelper.verifyConditionOnNutanixCluster(verifyConditionParams{
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
		})

		By("Creating secret using e2e credentials", func() {
			up := getBaseAuthCredentials(*e2eConfig)
			testHelper.createSecret(createSecretParams{
				username:    up.username,
				password:    up.password,
				namespace:   namespace,
				clusterName: clusterName,
			})
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

		By("PASSED!")
	})

	It("Create a cluster with invalid credentials (should fail)", func() {
		const (
			flavor = "no-secret"
		)

		Expect(namespace).NotTo(BeNil())

		By("Creating secret with invalid credentials", func() {
			invalidCred := fmt.Sprintf("invalid-cred-e2e-%s", clusterName)
			testHelper.createSecret(createSecretParams{
				username:    invalidCred,
				password:    invalidCred,
				namespace:   namespace,
				clusterName: clusterName,
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

		By("Checking cluster prism client init condition is false", func() {
			testHelper.verifyConditionOnNutanixCluster(verifyConditionParams{
				clusterName:           clusterName,
				namespace:             namespace,
				bootstrapClusterProxy: bootstrapClusterProxy,
				expectedCondition: clusterv1.Condition{
					Type:     infrav1.PrismCentralClientCondition,
					Reason:   infrav1.PrismCentralClientInitializationFailed,
					Severity: clusterv1.ConditionSeverityError,
					Status:   corev1.ConditionFalse,
				},
			})
		})

		By("PASSED!")
	})
})
