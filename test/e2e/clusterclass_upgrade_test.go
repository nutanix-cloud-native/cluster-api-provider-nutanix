//go:build e2e

/*
Copyright 2024 Nutanix

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
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/cluster-api/test/framework"
	"sigs.k8s.io/cluster-api/test/framework/clusterctl"
)

var _ = Describe("When upgrading a workload cluster using ClusterClass", Label("clusterclass"), func() {
	const specName = "clusterclass-upgrade"

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

	It("Create a cluster with topology", func() {
		const flavor = "topology"

		Expect(namespace).NotTo(BeNil())

		By("Creating a workload cluster with topology")
		// Set KubernetesVersion to KUBERNETES_VERSION_UPGRADE_FROM
		// Set control plane node machine template image NUTANIX_MACHINE_TEMPLATE_IMAGE_UPGRADE_FROM
		// Set workload node machine template image NUTANIX_MACHINE_TEMPLATE_IMAGE_UPGRADE_FROM
		os.Setenv("NUTANIX_MACHINE_TEMPLATE_IMAGE_NAME", e2eConfig.GetVariable("NUTANIX_MACHINE_TEMPLATE_IMAGE_UPGRADE_FROM"))

		cc := clusterctl.ConfigClusterInput{
			LogFolder:                filepath.Join(artifactFolder, "clusters", bootstrapClusterProxy.GetName()),
			ClusterctlConfigPath:     clusterctlConfigPath,
			KubeconfigPath:           bootstrapClusterProxy.GetKubeconfigPath(),
			InfrastructureProvider:   clusterctl.DefaultInfrastructureProvider,
			Flavor:                   flavor,
			Namespace:                namespace.Name,
			ClusterName:              clusterName,
			KubernetesVersion:        e2eConfig.GetVariable(KubernetesVersionUpgradeFrom),
			ControlPlaneMachineCount: pointer.Int64Ptr(1),
			WorkerMachineCount:       pointer.Int64Ptr(1),
		}

		clusterctl.ApplyClusterTemplateAndWait(ctx, clusterctl.ApplyClusterTemplateAndWaitInput{
			ClusterProxy:                 bootstrapClusterProxy,
			ConfigCluster:                cc,
			WaitForClusterIntervals:      e2eConfig.GetIntervals("", "wait-cluster"),
			WaitForControlPlaneIntervals: e2eConfig.GetIntervals("", "wait-control-plane"),
			WaitForMachineDeployments:    e2eConfig.GetIntervals("", "wait-worker-nodes"),
		}, clusterResources)

		By("Upgrading the Cluster topology")
		// Set KubernetesVersion to KUBERNETES_VERSION_UPGRADE_TO
		// Set control plane node machine template image NUTANIX_MACHINE_TEMPLATE_IMAGE_UPGRADE_TO
		// Set workload node machine template image NUTANIX_MACHINE_TEMPLATE_IMAGE_UPGRADE_TO
		os.Setenv("NUTANIX_MACHINE_TEMPLATE_IMAGE_NAME", e2eConfig.GetVariable("NUTANIX_MACHINE_TEMPLATE_IMAGE_UPGRADE_TO"))
		cc = clusterctl.ConfigClusterInput{
			LogFolder:                filepath.Join(artifactFolder, "clusters", bootstrapClusterProxy.GetName()),
			ClusterctlConfigPath:     clusterctlConfigPath,
			KubeconfigPath:           bootstrapClusterProxy.GetKubeconfigPath(),
			InfrastructureProvider:   clusterctl.DefaultInfrastructureProvider,
			Flavor:                   flavor,
			Namespace:                namespace.Name,
			ClusterName:              clusterName,
			KubernetesVersion:        e2eConfig.GetVariable(KubernetesVersionUpgradeTo),
			ControlPlaneMachineCount: pointer.Int64Ptr(1),
			WorkerMachineCount:       pointer.Int64Ptr(1),
		}

		fmt.Fprintf(GinkgoWriter, "Upgrade to: %#v\n", cc)

		clusterctl.ApplyClusterTemplateAndWait(ctx, clusterctl.ApplyClusterTemplateAndWaitInput{
			ClusterProxy:                 bootstrapClusterProxy,
			ConfigCluster:                cc,
			WaitForClusterIntervals:      e2eConfig.GetIntervals("", "wait-cluster"),
			WaitForControlPlaneIntervals: e2eConfig.GetIntervals("", "wait-control-plane"),
			WaitForMachineDeployments:    e2eConfig.GetIntervals("", "wait-worker-nodes"),
		}, clusterResources)

		waitForMachinesToBeUpgraded := e2eConfig.GetIntervals("", "wait-machine-upgrade")

		mgmtClient := bootstrapClusterProxy.GetClient()
		By("Waiting for control-plane machines to have the upgraded Kubernetes version")
		framework.WaitForControlPlaneMachinesToBeUpgraded(ctx, framework.WaitForControlPlaneMachinesToBeUpgradedInput{
			Lister:                   mgmtClient,
			Cluster:                  clusterResources.Cluster,
			MachineCount:             int(*cc.ControlPlaneMachineCount),
			KubernetesUpgradeVersion: e2eConfig.GetVariable(KubernetesVersionUpgradeTo),
		}, waitForMachinesToBeUpgraded...)
		fmt.Fprintf(GinkgoWriter, "Waiting for Kubernetes versions of Cluster nodes %s to be upgraded to %s\n",
			klog.KObj(clusterResources.Cluster), e2eConfig.GetVariable(KubernetesVersionUpgradeTo))

		for _, deployment := range clusterResources.MachineDeployments {
			if *deployment.Spec.Replicas > 0 {
				By("")
				fmt.Fprintf(GinkgoWriter, "Waiting for Kubernetes versions of machines in MachineDeployment %s to be upgraded to %s",
					klog.KObj(deployment), e2eConfig.GetVariable(KubernetesVersionUpgradeTo))

				framework.WaitForMachineDeploymentMachinesToBeUpgraded(ctx, framework.WaitForMachineDeploymentMachinesToBeUpgradedInput{
					Lister:                   mgmtClient,
					Cluster:                  clusterResources.Cluster,
					MachineCount:             int(*deployment.Spec.Replicas),
					KubernetesUpgradeVersion: e2eConfig.GetVariable(KubernetesVersionUpgradeTo),
					MachineDeployment:        *deployment,
				}, waitForMachinesToBeUpgraded...)
			}
		}

		By("Waiting until nodes are ready")
		workloadProxy := bootstrapClusterProxy.GetWorkloadCluster(ctx, namespace.Name, clusterResources.Cluster.Name)
		workloadClient := workloadProxy.GetClient()
		framework.WaitForNodesReady(ctx, framework.WaitForNodesReadyInput{
			Lister:            workloadClient,
			KubernetesVersion: e2eConfig.GetVariable(KubernetesVersionUpgradeTo),
			Count:             int(clusterResources.ExpectedTotalNodes()),
			WaitForNodesReady: e2eConfig.GetIntervals(specName, "wait-nodes-ready"),
		})

		By("PASSED!")
	})
})
