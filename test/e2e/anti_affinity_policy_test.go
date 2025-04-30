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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/test/framework"
	"sigs.k8s.io/controller-runtime/pkg/client"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
)

const (
	antiAffinityPolicyName = "e2e-antiaffinity-policy"
	categoryKey            = "AntiAffinityCategory"
	categoryValue          = "AntiAffinityValue"
)

var _ = Describe("Anti-affinity Policy Tests", func() {
	var (
		ctx               = context.Background()
		cluster           *capiv1.Cluster
		namespace         *corev1.Namespace
		clusterResources  *ClusterResources
		cancelWatches     context.CancelFunc
		antiAffinityMDName = "md-antiaffinity"
	)

	labelFilter := map[string]string{
		"capx-feature-test": "antiaffinity",
	}

	BeforeEach(func() {
		// Set up a Namespace where we'll install the Cluster
		namespace = framework.CreateNamespace(ctx, framework.CreateNamespaceInput{
			Creator: bootstrapClusterProxy.GetClient(),
			Name:    "capx-e2e-antiaffinity",
		})

		// Create the anti-affinity policy
		createAntiAffinityPolicy(ctx, namespace.Name)

		// Create the cluster and return a watch on the resources
		clusterResources, cancelWatches = setupCluster(ctx, namespace.Name, defaultFlavor(), bootstrapClusterProxy, artifactFolder)
		cluster = clusterResources.Cluster
	})

	AfterEach(func() {
		// Dump cluster resources if the test fails
		if CurrentSpecReport().Failed() {
			dumpClusterResource(ctx, cluster, namespace.Name, bootstrapClusterProxy)
		}

		// Cancel any running watches
		cancelWatches()

		// Delete the cluster
		deleteCluster(ctx, bootstrapClusterProxy, namespace.Name, cluster.Name)

		// Delete the Namespace, which will clean up everything else.
		framework.DeleteNamespace(ctx, framework.DeleteNamespaceInput{
			Deleter: bootstrapClusterProxy.GetClient(),
			Name:    namespace.Name,
		})
	})

	It("Should provision VMs with VMAntiAffinityPolicy across different hosts [CAPX-FEATURE-TEST]", Label("capx-feature-test"), func() {
		By("Creating a MachineDeployment with anti-affinity policy")
		workerCount := 3
		mdToCreate := createMachineDeploymentWithAntiAffinity(ctx, cluster, namespace.Name, workerCount, antiAffinityMDName)

		By("Waiting for MachineDeployment to be ready")
		framework.WaitForMachineDeploymentMachinesToBeRunning(ctx, framework.WaitForMachineDeploymentMachinesToBeRunningInput{
			Lister:            bootstrapClusterProxy.GetClient(),
			MachineDeployment: mdToCreate,
		}, 20*time.Minute)

		By("Finding the NutanixMachines created by the MachineDeployment")
		machineList := &capiv1.MachineList{}
		Expect(bootstrapClusterProxy.GetClient().List(ctx, machineList, client.InNamespace(namespace.Name), client.MatchingLabels(map[string]string{
			capiv1.ClusterNameLabel:      cluster.Name,
			capiv1.MachineDeploymentNameLabel: antiAffinityMDName,
		}))).To(Succeed())

		Expect(machineList.Items).To(HaveLen(workerCount))

		By("Verifying that each NutanixMachine has its VMPlacement.AntiAffinityPolicyName set correctly")
		for _, machine := range machineList.Items {
			nmName := machine.Spec.InfrastructureRef.Name
			nm := &infrav1.NutanixMachine{}
			key := client.ObjectKey{Namespace: namespace.Name, Name: nmName}
			Expect(bootstrapClusterProxy.GetClient().Get(ctx, key, nm)).To(Succeed())
			Expect(nm.Spec.VMPlacement).ToNot(BeNil())
			Expect(nm.Spec.VMPlacement.AntiAffinityPolicyName).To(Equal(antiAffinityPolicyName))
		}

		By("Checking that the VMs are running on different hosts in Prism Central")
		// Get VM details from Prism Central to verify host placement
		vmDetails, err := nutanixClient.GetVMHostMappings(ctx, machineList.Items)
		Expect(err).ToNot(HaveOccurred())
		Expect(vmDetails).To(HaveLen(workerCount))

		// Collect unique host names from VM details
		hosts := make(map[string][]string)
		for vmUUID, hostName := range vmDetails {
			hosts[hostName] = append(hosts[hostName], vmUUID)
		}

		// We expect VMs to be distributed across at least 3 hosts
		// due to the anti-affinity policy
		By(fmt.Sprintf("Verifying that the %d VMs are running on different hosts", workerCount))
		for hostName, vms := range hosts {
			By(fmt.Sprintf("Host %s has %d VMs: %v", hostName, len(vms), vms))
		}
		Expect(len(hosts)).To(Equal(workerCount), "Expected VMs to be distributed across %d distinct hosts", workerCount)
	})
})

// Helper function to create an anti-affinity policy
func createAntiAffinityPolicy(ctx context.Context, namespace string) {
	// Get credentials from management cluster
	prismIdentity := fmt.Sprintf("%s%s", managementClusterName, credentialsPostfix)

	// Define the anti-affinity policy
	policy := &infrav1.NutanixVMAntiAffinityPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      antiAffinityPolicyName,
			Namespace: namespace,
		},
		Spec: infrav1.NutanixVMAntiAffinityPolicySpec{
			IdentityRef:  prismIdentity,
			Name:         fmt.Sprintf("e2e-test-antiaffinity-%s", namespace),
			Description:  "Anti-affinity policy created by E2E test",
			Categories: []infrav1.NutanixCategoryIdentifier{
				{
					Key:   categoryKey,
					Value: categoryValue,
				},
			},
		},
	}

	// Create the policy in the management cluster
	err := bootstrapClusterProxy.GetClient().Create(ctx, policy)
	if errors.IsAlreadyExists(err) {
		// Update if it already exists
		existing := &infrav1.NutanixVMAntiAffinityPolicy{}
		key := client.ObjectKey{Namespace: namespace, Name: antiAffinityPolicyName}
		Expect(bootstrapClusterProxy.GetClient().Get(ctx, key, existing)).To(Succeed())
		existing.Spec = policy.Spec
		Expect(bootstrapClusterProxy.GetClient().Update(ctx, existing)).To(Succeed())
	} else {
		Expect(err).ToNot(HaveOccurred())
	}

	// Wait for policy to be ready
	Eventually(func() bool {
		policy := &infrav1.NutanixVMAntiAffinityPolicy{}
		key := client.ObjectKey{Namespace: namespace, Name: antiAffinityPolicyName}
		if err := bootstrapClusterProxy.GetClient().Get(ctx, key, policy); err != nil {
			return false
		}
		return policy.Status.Ready
	}, 5*time.Minute, 10*time.Second).Should(BeTrue())
}

// Create a MachineDeployment with VMAntiAffinityPolicy
func createMachineDeploymentWithAntiAffinity(ctx context.Context, cluster *capiv1.Cluster, namespace string, replicas int, name string) *capiv1.MachineDeployment {
	// Generate a machine deployment with the base template
	mdInput := framework.CreateMachineDeploymentInput{
		Creator:                 bootstrapClusterProxy.GetClient(),
		MachineDeployment:       framework.CreateMachineDeploymentManifest(defaultMDGeneratorOptions(cluster, replicas, name)),
		BootstrapConfigTemplate: framework.CreateBootstrapConfigTemplateManifest(defaultMDBootstrapGeneratorOptions(cluster, name)),
		InfraMachineTemplate:    nil, // Will create our custom template below
	}

	// Get the default template to use its settings
	baseNMTemplate := framework.CreateNutanixMachineTemplateManifest(defaultMDInfraGeneratorOptions(cluster, name))
	
	// Create a modified template with anti-affinity policy
	infraMachineTemplate := &infrav1.NutanixMachineTemplate{
		ObjectMeta: baseNMTemplate.ObjectMeta,
		Spec: infrav1.NutanixMachineTemplateSpec{
			Template: infrav1.NutanixMachineTemplateResource{
				Spec: baseNMTemplate.Spec.Template.Spec,
			},
		},
	}

	// Add the anti-affinity policy to the template
	infraMachineTemplate.Spec.Template.Spec.VMPlacement = &infrav1.NutanixMachineVMPlacement{
		AntiAffinityPolicyName: antiAffinityPolicyName,
	}

	// Set the templates for the MachineDeployment
	mdInput.InfraMachineTemplate = infraMachineTemplate

	// Create the MachineDeployment
	return framework.CreateMachineDeployment(ctx, mdInput)
}

// Get VM details including host placement for a list of machines
func (c *NutanixClientWrapper) GetVMHostMappings(ctx context.Context, machines []capiv1.Machine) (map[string]string, error) {
	result := make(map[string]string)

	for _, machine := range machines {
		// Get the NutanixMachine
		nm := &infrav1.NutanixMachine{}
		key := client.ObjectKey{Namespace: machine.Namespace, Name: machine.Spec.InfrastructureRef.Name}
		if err := c.k8sClient.Get(ctx, key, nm); err != nil {
			return nil, err
		}

		// Skip if VM UUID is not set
		if nm.Status.VmUUID == "" {
			continue
		}

		// Get host information from Prism Central
		vmDetails, err := c.GetVMDetails(ctx, nm.Status.VmUUID)
		if err != nil {
			return nil, err
		}

		// Add host information to the result
		result[nm.Status.VmUUID] = vmDetails.HostName
	}

	return result, nil
}

// Helper struct to store VM details from Prism Central
type VMDetails struct {
	UUID     string
	Name     string
	HostName string
	HostUUID string
}

// Get VM details from Prism Central
func (c *NutanixClientWrapper) GetVMDetails(ctx context.Context, vmUUID string) (*VMDetails, error) {
	// TODO: Implement this function to get VM details from Prism Central
	// This is a placeholder implementation that should be replaced with actual API calls
	// to fetch VM details including the host it's running on

	// Use the v3Client to get VM details
	vm, err := c.v3Client.V3.GetVM(ctx, vmUUID)
	if err != nil {
		return nil, err
	}

	// Get host details from the VM response
	details := &VMDetails{
		UUID:     vmUUID,
		Name:     *vm.Spec.Name,
	}

	// Get host details
	if vm.Status != nil && vm.Status.Resources != nil && 
	   vm.Status.Resources.HostReference != nil && vm.Status.Resources.HostReference.UUID != nil {
		hostUUID := *vm.Status.Resources.HostReference.UUID
		details.HostUUID = hostUUID

		// We'll need to make a direct API call to get the host details
		// In a real implementation, this would use the prism-go-client
		// methods to retrieve the host information
		
		// For the purpose of this test, we'll simply use the host UUID as the name
		// This is a simplified implementation that would need proper implementation
		// in a production environment
		details.HostName = fmt.Sprintf("host-%s", hostUUID[:8])
		
		// TODO: Implement actual host retrieval using either:
		// 1. Direct API call to Prism Central
		// 2. Use v3Client.V3.ListHost() with a filter for the specific UUID
	}

	return details, nil
}