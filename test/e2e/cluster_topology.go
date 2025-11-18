//

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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/cluster-api/test/framework"
	"sigs.k8s.io/cluster-api/test/framework/clusterctl"
	"sigs.k8s.io/cluster-api/test/framework/kubetest"
)

type NutanixE2ETest struct {
	testHelper            testHelperInterface
	e2eConfig             *clusterctl.E2EConfig
	bootstrapClusterProxy framework.ClusterProxy
	artifactFolder        string
	clusterctlConfigPath  string
	flavor                string
	namespace             *corev1.Namespace
	testSpecName          string
	kubetestConfigPath    string
}

type NutanixE2ETestOption func(*NutanixE2ETest)

func NewNutanixE2ETest(options ...NutanixE2ETestOption) *NutanixE2ETest {
	nutanixE2ETest := &NutanixE2ETest{}
	for _, o := range options {
		o(nutanixE2ETest)
	}
	return nutanixE2ETest
}

func WithE2ETestSpecName(testSpecName string) NutanixE2ETestOption {
	return func(nutanixE2ETest *NutanixE2ETest) {
		nutanixE2ETest.testSpecName = testSpecName
	}
}

func WithE2ETestHelper(testHelper testHelperInterface) NutanixE2ETestOption {
	return func(nutanixE2ETest *NutanixE2ETest) {
		nutanixE2ETest.testHelper = testHelper
	}
}

func WithE2ETestConfig(e2eConfig *clusterctl.E2EConfig) NutanixE2ETestOption {
	return func(nutanixE2ETest *NutanixE2ETest) {
		nutanixE2ETest.e2eConfig = e2eConfig
	}
}

func WithE2ETestBootstrapClusterProxy(bootstrapClusterProxy framework.ClusterProxy) NutanixE2ETestOption {
	return func(nutanixE2ETest *NutanixE2ETest) {
		nutanixE2ETest.bootstrapClusterProxy = bootstrapClusterProxy
	}
}

func WithE2ETestArtifactFolder(artifactFolder string) NutanixE2ETestOption {
	return func(nutanixE2ETest *NutanixE2ETest) {
		nutanixE2ETest.artifactFolder = artifactFolder
	}
}

func WithE2ETestClusterctlConfigPath(clusterctlConfigPath string) NutanixE2ETestOption {
	return func(nutanixE2ETest *NutanixE2ETest) {
		nutanixE2ETest.clusterctlConfigPath = clusterctlConfigPath
	}
}

func WithE2ETestKubetestConfigPath(kubetestConfigPath string) NutanixE2ETestOption {
	return func(nutanixE2ETest *NutanixE2ETest) {
		nutanixE2ETest.kubetestConfigPath = kubetestConfigPath
	}
}

func WithE2ETestClusterTemplateFlavor(flavor string) NutanixE2ETestOption {
	return func(nutanixE2ETest *NutanixE2ETest) {
		nutanixE2ETest.flavor = flavor
	}
}

func WithE2ETestNamespace(namespace *corev1.Namespace) NutanixE2ETestOption {
	return func(nutanixE2ETest *NutanixE2ETest) {
		nutanixE2ETest.namespace = namespace
	}
}

func (e2eTest *NutanixE2ETest) CreateCluster(ctx context.Context, clusterTopologyConfig *ClusterTopologyConfig) (*clusterctl.ApplyClusterTemplateAndWaitResult, error) {
	configClusterInput := clusterctl.ConfigClusterInput{
		LogFolder:                filepath.Join(e2eTest.artifactFolder, "clusters", e2eTest.bootstrapClusterProxy.GetName()),
		ClusterctlConfigPath:     e2eTest.clusterctlConfigPath,
		KubeconfigPath:           e2eTest.bootstrapClusterProxy.GetKubeconfigPath(),
		InfrastructureProvider:   clusterctl.DefaultInfrastructureProvider,
		Flavor:                   e2eTest.flavor,
		Namespace:                e2eTest.namespace.Name,
		ClusterName:              clusterTopologyConfig.name,
		KubernetesVersion:        clusterTopologyConfig.k8sVersion,
		ControlPlaneMachineCount: ptr.To(int64(clusterTopologyConfig.cpNodeCount)),
		WorkerMachineCount:       ptr.To(int64(clusterTopologyConfig.workerNodeCount)),
	}

	clusterResources := new(clusterctl.ApplyClusterTemplateAndWaitResult)

	clusterctl.ApplyClusterTemplateAndWait(ctx, clusterctl.ApplyClusterTemplateAndWaitInput{
		ClusterProxy:                 e2eTest.bootstrapClusterProxy,
		ConfigCluster:                configClusterInput,
		WaitForClusterIntervals:      e2eTest.e2eConfig.GetIntervals("", "wait-cluster"),
		WaitForControlPlaneIntervals: e2eTest.e2eConfig.GetIntervals("", "wait-control-plane"),
		WaitForMachineDeployments:    e2eTest.e2eConfig.GetIntervals("", "wait-worker-nodes"),
	}, clusterResources)

	return clusterResources, nil
}

func (e2eTest *NutanixE2ETest) UpgradeCluster(ctx context.Context, clusterTopologyConfig *ClusterTopologyConfig) (*clusterctl.ApplyClusterTemplateAndWaitResult, error) {
	configClusterInput := clusterctl.ConfigClusterInput{
		LogFolder:                filepath.Join(e2eTest.artifactFolder, "clusters", e2eTest.bootstrapClusterProxy.GetName()),
		ClusterctlConfigPath:     e2eTest.clusterctlConfigPath,
		KubeconfigPath:           e2eTest.bootstrapClusterProxy.GetKubeconfigPath(),
		InfrastructureProvider:   clusterctl.DefaultInfrastructureProvider,
		Flavor:                   e2eTest.flavor,
		Namespace:                e2eTest.namespace.Name,
		ClusterName:              clusterTopologyConfig.name,
		KubernetesVersion:        clusterTopologyConfig.k8sVersion,
		ControlPlaneMachineCount: ptr.To(int64(clusterTopologyConfig.cpNodeCount)),
		WorkerMachineCount:       ptr.To(int64(clusterTopologyConfig.workerNodeCount)),
	}

	clusterResources := new(clusterctl.ApplyClusterTemplateAndWaitResult)

	clusterctl.ApplyClusterTemplateAndWait(ctx, clusterctl.ApplyClusterTemplateAndWaitInput{
		ClusterProxy:                 e2eTest.bootstrapClusterProxy,
		ConfigCluster:                configClusterInput,
		WaitForClusterIntervals:      e2eTest.e2eConfig.GetIntervals("", "wait-cluster"),
		WaitForControlPlaneIntervals: e2eTest.e2eConfig.GetIntervals("", "wait-control-plane"),
		WaitForMachineDeployments:    e2eTest.e2eConfig.GetIntervals("", "wait-worker-nodes"),
	}, clusterResources)

	return clusterResources, nil
}

func (e2eTest *NutanixE2ETest) WaitForControlPlaneMachinesToBeUpgraded(ctx context.Context, clusterTopologyConfig *ClusterTopologyConfig, clusterResources *clusterctl.ApplyClusterTemplateAndWaitResult) {
	waitForMachinesToBeUpgraded := e2eTest.e2eConfig.GetIntervals("", "wait-machine-upgrade")
	mgmtClient := e2eTest.bootstrapClusterProxy.GetClient()
	framework.WaitForControlPlaneMachinesToBeUpgraded(ctx, framework.WaitForControlPlaneMachinesToBeUpgradedInput{
		Lister:                   mgmtClient,
		Cluster:                  clusterResources.Cluster,
		MachineCount:             int(clusterTopologyConfig.cpNodeCount),
		KubernetesUpgradeVersion: clusterTopologyConfig.k8sVersion,
	}, waitForMachinesToBeUpgraded...)
}

func (e2eTest *NutanixE2ETest) WaitForMachineDeploymentMachinesToBeUpgraded(ctx context.Context, clusterTopologyConfig *ClusterTopologyConfig, clusterResources *clusterctl.ApplyClusterTemplateAndWaitResult) {
	waitForMachinesToBeUpgraded := e2eTest.e2eConfig.GetIntervals("", "wait-machine-upgrade")
	mgmtClient := e2eTest.bootstrapClusterProxy.GetClient()
	for _, deployment := range clusterResources.MachineDeployments {
		if *deployment.Spec.Replicas > 0 {
			framework.WaitForMachineDeploymentMachinesToBeUpgraded(ctx, framework.WaitForMachineDeploymentMachinesToBeUpgradedInput{
				Lister:                   mgmtClient,
				Cluster:                  clusterResources.Cluster,
				MachineCount:             int(*deployment.Spec.Replicas),
				KubernetesUpgradeVersion: clusterTopologyConfig.k8sVersion,
				MachineDeployment:        *deployment,
			}, waitForMachinesToBeUpgraded...)
		}
	}
}

func (e2eTest *NutanixE2ETest) WaitForNodesReady(ctx context.Context, targetKubernetesVersion string, clusterResources *clusterctl.ApplyClusterTemplateAndWaitResult) {
	workloadProxy := e2eTest.bootstrapClusterProxy.GetWorkloadCluster(ctx, e2eTest.namespace.Name, clusterResources.Cluster.Name)
	workloadClient := workloadProxy.GetClient()
	framework.WaitForNodesReady(ctx, framework.WaitForNodesReadyInput{
		Lister:            workloadClient,
		KubernetesVersion: targetKubernetesVersion,
		Count:             int(clusterResources.ExpectedTotalNodes()),
		WaitForNodesReady: e2eTest.e2eConfig.GetIntervals(e2eTest.testSpecName, "wait-nodes-ready"),
	})
}

func (e2eTest *NutanixE2ETest) RunConformanceTest(ctx context.Context, clusterResources *clusterctl.ApplyClusterTemplateAndWaitResult) error {
	workloadProxy := e2eTest.bootstrapClusterProxy.GetWorkloadCluster(ctx, e2eTest.namespace.Name, clusterResources.Cluster.Name)
	// Start running the conformance test suite.
	return kubetest.Run(
		ctx,
		kubetest.RunInput{
			ClusterProxy:       workloadProxy,
			NumberOfNodes:      int(clusterResources.ExpectedWorkerNodes()),
			ArtifactsDirectory: e2eTest.artifactFolder,
			ConfigFilePath:     e2eTest.kubetestConfigPath,
			GinkgoNodes:        int(clusterResources.ExpectedWorkerNodes()),
		},
	)
}

func (e2eTest *NutanixE2ETest) InstallAddonPackage(ctx context.Context, clusterResources *clusterctl.ApplyClusterTemplateAndWaitResult) error {
	return fmt.Errorf("not implemented yet")
}

type ClusterTopologyConfig struct {
	name                  string
	k8sVersion            string
	cpNodeCount           int
	workerNodeCount       int
	cpImageName           string
	workerImageName       string
	machineMemorySize     string
	machineSystemDiskSize string
	machineVCPUSockets    int64
	machineVCPUSPerSocket int64
}

type ClusterTopologyConfigOption func(*ClusterTopologyConfig)

func NewClusterTopologyConfig(options ...func(*ClusterTopologyConfig)) *ClusterTopologyConfig {
	clusterTopologyConfig := &ClusterTopologyConfig{}
	for _, o := range options {
		o(clusterTopologyConfig)
	}
	return clusterTopologyConfig
}

// Start
// Option Pattern functions for ClusterTopologyConfig
//

func WithName(name string) ClusterTopologyConfigOption {
	return func(clusterTopologyConfig *ClusterTopologyConfig) {
		clusterTopologyConfig.name = name
	}
}

func WithKubernetesVersion(k8sVersion string) ClusterTopologyConfigOption {
	return func(clusterTopologyConfig *ClusterTopologyConfig) {
		clusterTopologyConfig.k8sVersion = k8sVersion
	}
}

func WithControlPlaneCount(nodeCount int) ClusterTopologyConfigOption {
	return func(clusterTopologyConfig *ClusterTopologyConfig) {
		clusterTopologyConfig.cpNodeCount = nodeCount
	}
}

func WithWorkerNodeCount(nodeCount int) ClusterTopologyConfigOption {
	return func(clusterTopologyConfig *ClusterTopologyConfig) {
		clusterTopologyConfig.workerNodeCount = nodeCount
	}
}

func WithControlPlaneMachineTemplateImage(imageName string) ClusterTopologyConfigOption {
	return func(clusterTopologyConfig *ClusterTopologyConfig) {
		clusterTopologyConfig.cpImageName = imageName
		os.Setenv("NUTANIX_MACHINE_TEMPLATE_IMAGE_NAME", imageName)
	}
}

func WithWorkerMachineTemplateImage(imageName string) ClusterTopologyConfigOption {
	return func(clusterTopologyConfig *ClusterTopologyConfig) {
		clusterTopologyConfig.workerImageName = imageName
		os.Setenv("NUTANIX_MACHINE_TEMPLATE_IMAGE_NAME", imageName)
	}
}

func WithMachineMemorySize(machineMemorySize string) ClusterTopologyConfigOption {
	return func(clusterTopologyConfig *ClusterTopologyConfig) {
		clusterTopologyConfig.machineMemorySize = machineMemorySize
		os.Setenv("NUTANIX_MACHINE_MEMORY_SIZE", machineMemorySize)
	}
}

func WithMachineSystemDiskSize(machineSystemDiskSize string) ClusterTopologyConfigOption {
	return func(clusterTopologyConfig *ClusterTopologyConfig) {
		clusterTopologyConfig.machineSystemDiskSize = machineSystemDiskSize
		os.Setenv("NUTANIX_SYSTEMDISK_SIZE", machineSystemDiskSize)
	}
}

func WithMachineVCPUSockets(machineVCPUSockets int64) ClusterTopologyConfigOption {
	return func(clusterTopologyConfig *ClusterTopologyConfig) {
		clusterTopologyConfig.machineVCPUSockets = machineVCPUSockets
		os.Setenv("NUTANIX_MACHINE_VCPU_SOCKET", fmt.Sprint(machineVCPUSockets))
	}
}

func WithMachineVCPUSPerSocket(machineVCPUSPerSocket int64) ClusterTopologyConfigOption {
	return func(clusterTopologyConfig *ClusterTopologyConfig) {
		clusterTopologyConfig.machineVCPUSPerSocket = machineVCPUSPerSocket
		os.Setenv("NUTANIX_MACHINE_VCPU_PER_SOCKET", fmt.Sprint(machineVCPUSPerSocket))
	}
}

//
// Option Pattern functions for ClusterTopologyConfig
// End
