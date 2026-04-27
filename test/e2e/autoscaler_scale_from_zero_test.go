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
	"path/filepath"
	"strconv"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"

	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/cluster-api/test/framework"
	"sigs.k8s.io/cluster-api/test/framework/clusterctl"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/patch"
	"sigs.k8s.io/controller-runtime/pkg/client"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
)

const (
	autoscalerWorkloadYAMLPathVar = "AUTOSCALER_WORKLOAD"
	autoscalerVersionVar          = "AUTOSCALER_VERSION"
)

var _ = Describe("Autoscaler scale to and from zero", Label("scaling", "autoscaler"), func() {
	const specName = "autoscaler"
	const flavorAutoscaler = "topology-autoscaler"

	var (
		namespace        *corev1.Namespace
		clusterResources *clusterctl.ApplyClusterTemplateAndWaitResult
		cancelWatches    context.CancelFunc
	)

	BeforeEach(func() {
		Expect(e2eConfig).NotTo(BeNil(), "e2eConfig is required")
		Expect(e2eConfig.Variables).To(HaveKey(autoscalerWorkloadYAMLPathVar),
			"%s must be defined in the e2e config", autoscalerWorkloadYAMLPathVar)
		Expect(e2eConfig.Variables).To(HaveKey(autoscalerVersionVar),
			"%s must be defined in the e2e config", autoscalerVersionVar)
		Expect(clusterctlConfigPath).To(BeAnExistingFile(), "clusterctlConfigPath must be an existing file")
		Expect(bootstrapClusterProxy).NotTo(BeNil(), "BootstrapClusterProxy can't be nil")

		namespace, cancelWatches = setupSpecNamespace(ctx, specName, bootstrapClusterProxy, artifactFolder)
		clusterResources = new(clusterctl.ApplyClusterTemplateAndWaitResult)
	})

	AfterEach(func() {
		dumpSpecResourcesAndCleanup(ctx, specName, bootstrapClusterProxy, artifactFolder,
			namespace, cancelWatches, clusterResources.Cluster, e2eConfig.GetIntervals, skipCleanup)
	})

	It("Should deploy autoscaler and exercise scale to and from zero", func() {
		Expect(namespace).NotTo(BeNil())

		By("Creating a workload cluster with topology-autoscaler flavor")
		clusterName := fmt.Sprintf("%s-%s", specName, util.RandomString(6))
		clusterctl.ApplyClusterTemplateAndWait(ctx, clusterctl.ApplyClusterTemplateAndWaitInput{
			ClusterProxy: bootstrapClusterProxy,
			ConfigCluster: clusterctl.ConfigClusterInput{
				LogFolder:                filepath.Join(artifactFolder, "clusters", bootstrapClusterProxy.GetName()),
				ClusterctlConfigPath:     clusterctlConfigPath,
				KubeconfigPath:           bootstrapClusterProxy.GetKubeconfigPath(),
				InfrastructureProvider:   clusterctl.DefaultInfrastructureProvider,
				Flavor:                   flavorAutoscaler,
				Namespace:                namespace.Name,
				ClusterName:              clusterName,
				KubernetesVersion:        e2eConfig.MustGetVariable(KubernetesVersion),
				ControlPlaneMachineCount: ptr.To[int64](1),
				WorkerMachineCount:       nil,
			},
			WaitForClusterIntervals:      e2eConfig.GetIntervals(specName, "wait-cluster"),
			WaitForControlPlaneIntervals: e2eConfig.GetIntervals(specName, "wait-control-plane"),
			WaitForMachineDeployments:    e2eConfig.GetIntervals(specName, "wait-worker-nodes"),
		}, clusterResources)

		Expect(clusterResources.Cluster).NotTo(BeNil())
		Expect(clusterResources.Cluster.Spec.Topology).NotTo(BeNil(),
			"Autoscaler test requires a Cluster with topology")

		mdTopology := clusterResources.Cluster.Spec.Topology.Workers.MachineDeployments[0]
		Expect(mdTopology.Metadata.Annotations).NotTo(BeNil(),
			"MachineDeploymentTopology should have autoscaler annotations")
		mdNodeGroupMinSize, ok := mdTopology.Metadata.Annotations[clusterv1.AutoscalerMinSizeAnnotation]
		Expect(ok).To(BeTrue(), "MachineDeploymentTopology should have %s annotation",
			clusterv1.AutoscalerMinSizeAnnotation)
		mdNodeGroupMaxSize, ok := mdTopology.Metadata.Annotations[clusterv1.AutoscalerMaxSizeAnnotation]
		Expect(ok).To(BeTrue(), "MachineDeploymentTopology should have %s annotation",
			clusterv1.AutoscalerMaxSizeAnnotation)

		Expect(clusterResources.MachineDeployments).NotTo(BeEmpty())
		mdOriginalReplicas := *clusterResources.MachineDeployments[0].Spec.Replicas
		Expect(strconv.Itoa(int(mdOriginalReplicas))).To(Equal(mdNodeGroupMinSize),
			"MachineDeployment replicas should equal %s", clusterv1.AutoscalerMinSizeAnnotation)

		bcpClient := bootstrapClusterProxy.GetClient()

		By("Verifying NutanixMachineTemplate has status.capacity for scale-from-zero support")
		var workerNMT *infrav1.NutanixMachineTemplate
		Eventually(func(g Gomega) {
			nmtList := &infrav1.NutanixMachineTemplateList{}
			g.Expect(bcpClient.List(ctx, nmtList, client.InNamespace(namespace.Name))).To(Succeed())
			g.Expect(nmtList.Items).NotTo(BeEmpty())

			for i := range nmtList.Items {
				nmt := &nmtList.Items[i]
				if len(nmt.Status.Capacity) > 0 {
					workerNMT = nmt
					break
				}
			}
			g.Expect(workerNMT).NotTo(BeNil(),
				"at least one NutanixMachineTemplate must have status.capacity set")
		}, defaultTimeout, defaultInterval).Should(Succeed())

		cpuQty := workerNMT.Status.Capacity[corev1.ResourceCPU]
		memQty := workerNMT.Status.Capacity[corev1.ResourceMemory]
		Expect(cpuQty.Value()).To(BeNumerically(">", 0), "cpu capacity should be > 0")
		Expect(memQty.Value()).To(BeNumerically(">", 0), "memory capacity should be > 0")
		Byf("NutanixMachineTemplate %s reports capacity: cpu=%s, memory=%s",
			workerNMT.Name, cpuQty.String(), memQty.String())

		By("Adding capacity annotations to MachineDeploymentTopology for scale-from-zero")
		patchHelper, err := patch.NewHelper(clusterResources.Cluster, bcpClient)
		Expect(err).ToNot(HaveOccurred())
		for i := range clusterResources.Cluster.Spec.Topology.Workers.MachineDeployments {
			md := &clusterResources.Cluster.Spec.Topology.Workers.MachineDeployments[i]
			if md.Metadata.Annotations == nil {
				md.Metadata.Annotations = map[string]string{}
			}
			md.Metadata.Annotations["capacity.cluster-autoscaler.kubernetes.io/cpu"] = cpuQty.String()
			md.Metadata.Annotations["capacity.cluster-autoscaler.kubernetes.io/memory"] = memQty.String()
		}
		Eventually(func(g Gomega) {
			g.Expect(patchHelper.Patch(ctx, clusterResources.Cluster)).Should(Succeed())
		}, defaultTimeout, defaultInterval).Should(Succeed())

		workloadClusterProxy := bootstrapClusterProxy.GetWorkloadCluster(ctx,
			clusterResources.Cluster.Namespace, clusterResources.Cluster.Name)

		By("Installing the autoscaler on the management cluster")
		autoscalerWorkloadYAMLPath := e2eConfig.MustGetVariable(autoscalerWorkloadYAMLPathVar)
		framework.ApplyAutoscalerToWorkloadCluster(ctx, framework.ApplyAutoscalerToWorkloadClusterInput{
			ArtifactFolder:                    artifactFolder,
			InfrastructureMachineTemplateKind: "nutanixmachinetemplates",
			InfrastructureAPIGroup:            "infrastructure.cluster.x-k8s.io",
			WorkloadYamlPath:                  autoscalerWorkloadYAMLPath,
			ManagementClusterProxy:            bootstrapClusterProxy,
			WorkloadClusterProxy:              workloadClusterProxy,
			Cluster:                           clusterResources.Cluster,
			AutoscalerVersion:                 e2eConfig.MustGetVariable(autoscalerVersionVar),
			AutoscalerOnManagementCluster:     true,
		}, e2eConfig.GetIntervals(specName, "wait-controllers")...)

		By("Creating workload that forces the autoscaler to scale up")
		framework.AddScaleUpDeploymentAndWait(ctx, framework.AddScaleUpDeploymentAndWaitInput{
			ClusterProxy: workloadClusterProxy,
		}, e2eConfig.GetIntervals(specName, "wait-autoscaler")...)

		By("Checking the autoscaler scaled up the MachineDeployment")
		mdScaledUpReplicas := mdOriginalReplicas + 1
		framework.AssertMachineDeploymentReplicas(ctx, framework.AssertMachineDeploymentReplicasInput{
			GetLister:                bcpClient,
			MachineDeployment:        clusterResources.MachineDeployments[0],
			Replicas:                 mdScaledUpReplicas,
			WaitForMachineDeployment: e2eConfig.GetIntervals(specName, "wait-autoscaler"),
		})

		By("Enabling autoscaler with min-size=0 for scale-to-zero")
		framework.EnableAutoscalerForMachineDeploymentTopologyAndWait(ctx,
			framework.EnableAutoscalerForMachineDeploymentTopologyAndWaitInput{
				ClusterProxy:                bootstrapClusterProxy,
				Cluster:                     clusterResources.Cluster,
				NodeGroupMinSize:            "0",
				NodeGroupMaxSize:            mdNodeGroupMaxSize,
				WaitForAnnotationsToBeAdded: e2eConfig.GetIntervals(specName, "wait-autoscaler"),
			})

		By("Scaling the scale-up deployment to 0 replicas to trigger scale-to-zero")
		framework.ScaleScaleUpDeploymentAndWait(ctx, framework.ScaleScaleUpDeploymentAndWaitInput{
			ClusterProxy: workloadClusterProxy,
			Replicas:     0,
		}, e2eConfig.GetIntervals(specName, "wait-autoscaler")...)

		By("Checking the autoscaler scaled the MachineDeployment to zero")
		framework.AssertMachineDeploymentReplicas(ctx, framework.AssertMachineDeploymentReplicasInput{
			GetLister:                bcpClient,
			MachineDeployment:        clusterResources.MachineDeployments[0],
			Replicas:                 0,
			WaitForMachineDeployment: e2eConfig.GetIntervals(specName, "wait-autoscaler"),
		})

		By("Verifying NutanixMachineTemplate status.capacity persists at zero replicas")
		Eventually(func(g Gomega) {
			nmt := &infrav1.NutanixMachineTemplate{}
			g.Expect(bcpClient.Get(ctx, client.ObjectKeyFromObject(workerNMT), nmt)).To(Succeed())
			g.Expect(nmt.Status.Capacity).NotTo(BeEmpty(),
				"status.capacity must persist when scaled to zero for autoscaler scale-from-zero")
			g.Expect(nmt.Status.Capacity).To(HaveKey(corev1.ResourceCPU))
			g.Expect(nmt.Status.Capacity).To(HaveKey(corev1.ResourceMemory))
		}, defaultTimeout, defaultInterval).Should(Succeed())

		By("Scaling the scale-up deployment back to trigger scale-from-zero")
		framework.ScaleScaleUpDeploymentAndWait(ctx, framework.ScaleScaleUpDeploymentAndWaitInput{
			ClusterProxy: workloadClusterProxy,
			Replicas:     1,
		}, e2eConfig.GetIntervals(specName, "wait-autoscaler")...)

		By("Checking the autoscaler scaled the MachineDeployment from zero")
		framework.AssertMachineDeploymentReplicas(ctx, framework.AssertMachineDeploymentReplicasInput{
			GetLister:                bcpClient,
			MachineDeployment:        clusterResources.MachineDeployments[0],
			Replicas:                 1,
			WaitForMachineDeployment: e2eConfig.GetIntervals(specName, "wait-autoscaler"),
		})

		By("PASSED!")
	})
})
