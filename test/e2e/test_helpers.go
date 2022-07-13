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
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	prismGoClientV3 "github.com/nutanix-cloud-native/prism-go-client/pkg/nutanix/v3"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gstruct"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/test/framework"
	"sigs.k8s.io/cluster-api/test/framework/clusterctl"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/controller-runtime/pkg/client"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
)

const (
	nutanixProviderIDPrefix = "nutanix://"

	defaultTimeout  = time.Second * 300
	defaultInterval = time.Second * 10

	defaultVCPUsPerSocket = int32(1)
	defaultVCPUSockets    = int32(2)
	defaultMemorySize     = "4Gi"
	defaultSystemDiskSize = "40Gi"
	defaultBootType       = "legacy"

	nutanixUserKey     = "NUTANIX_USER"
	nutanixPasswordKey = "NUTANIX_PASSWORD"

	categoryKeyEnvVarKey   = "NUTANIX_ADDITIONAL_CATEGORY_KEY"
	categoryValueEnvVarKey = "NUTANIX_ADDITIONAL_CATEGORY_VALUE"
	imageEnvVarKey         = "NUTANIX_MACHINE_TEMPLATE_IMAGE_NAME"
	clusterEnvVarKey       = "NUTANIX_PRISM_ELEMENT_CLUSTER_NAME"
	subnetEnvVarKey        = "NUTANIX_SUBNET_NAME"

	nameType = "name"
)

type testHelperInterface interface {
	createClusterFromConfig(ctx context.Context, input clusterctl.ApplyClusterTemplateAndWaitInput, result *clusterctl.ApplyClusterTemplateAndWaitResult)
	createDefaultNMT(clusterName, namespace string) *infrav1.NutanixMachineTemplate
	createNutanixMachineTemplate(ctx context.Context, params createNutanixMachineTemplateParams)
	createSecret(params createSecretParams)
	deployCluster(params deployClusterParams, clusterResources *clusterctl.ApplyClusterTemplateAndWaitResult)
	deployClusterAndWait(params deployClusterParams, clusterResources *clusterctl.ApplyClusterTemplateAndWaitResult)
	generateNMTName(clusterName string) string
	generateNMTProviderID(clusterName string) string
	generateTestClusterName(specName string) string
	getExpectedClusterCategoryKey(clusterName string) string
	getMachinesForCluster(ctx context.Context, clusterName, namespace string, bootstrapClusterProxy framework.ClusterProxy) *clusterv1.MachineList
	getNutanixMachineForCluster(ctx context.Context, clusterName, namespace, machineName string, bootstrapClusterProxy framework.ClusterProxy) *infrav1.NutanixMachine
	getNutanixMachinesForCluster(ctx context.Context, clusterName, namespace string, bootstrapClusterProxy framework.ClusterProxy) *infrav1.NutanixMachineList
	getNutanixClusterByName(ctx context.Context, input getNutanixClusterByNameInput) *infrav1.NutanixCluster
	getNutanixVMsForCluster(clusterName, namespace string) []*prismGoClientV3.VMIntentResponse
	getNutanixResourceIdentifierFromEnv(envVarKey string) infrav1.NutanixResourceIdentifier
	stripNutanixIDFromProviderID(providerID string) string
	verifyCategoryExists(categoryKey, categoyValue string)
	verifyCategoriesNutanixMachines(clusterName, namespace string, expectedCategories map[string]string)
	verifyConditionOnNutanixCluster(params verifyConditionParams)
	verifyConditionOnNutanixMachines(params verifyConditionParams)
	verifyFailureMessageOnClusterMachines(ctx context.Context, params verifyFailureMessageOnClusterMachinesParams)
	verifyProjectNutanixMachines(ctx context.Context, params verifyProjectNutanixMachinesParams)
}

type testHelper struct {
	nutanixClient *prismGoClientV3.Client
}

func newTestHelper() testHelperInterface {
	c, err := initNutanixClient()
	Expect(err).ShouldNot(HaveOccurred())

	return testHelper{
		nutanixClient: c,
	}
}

func (t testHelper) createClusterFromConfig(ctx context.Context, input clusterctl.ApplyClusterTemplateAndWaitInput, result *clusterctl.ApplyClusterTemplateAndWaitResult) {
	Expect(ctx).NotTo(BeNil(), "ctx is required for createClusterFromConfig")
	Expect(input.ClusterProxy).ToNot(BeNil(), "Invalid argument. input.ClusterProxy can't be nil when calling createClusterFromConfig")
	Expect(result).ToNot(BeNil(), "Invalid argument. result can't be nil when calling createClusterFromConfig")
	Expect(input.ConfigCluster.ControlPlaneMachineCount).ToNot(BeNil())
	Expect(input.ConfigCluster.WorkerMachineCount).ToNot(BeNil())

	clusterTemplate := clusterctl.ConfigCluster(ctx, clusterctl.ConfigClusterInput{
		KubeconfigPath:           input.ConfigCluster.KubeconfigPath,
		ClusterctlConfigPath:     input.ConfigCluster.ClusterctlConfigPath,
		Flavor:                   input.ConfigCluster.Flavor,
		Namespace:                input.ConfigCluster.Namespace,
		ClusterName:              input.ConfigCluster.ClusterName,
		KubernetesVersion:        input.ConfigCluster.KubernetesVersion,
		ControlPlaneMachineCount: input.ConfigCluster.ControlPlaneMachineCount,
		WorkerMachineCount:       input.ConfigCluster.WorkerMachineCount,
		InfrastructureProvider:   input.ConfigCluster.InfrastructureProvider,
		LogFolder:                input.ConfigCluster.LogFolder,
	})
	Expect(clusterTemplate).ToNot(BeNil())

	Expect(input.ClusterProxy.Apply(ctx, clusterTemplate, input.Args...)).To(Succeed())

	result.Cluster = framework.GetClusterByName(ctx, framework.GetClusterByNameInput{
		Getter:    input.ClusterProxy.GetClient(),
		Name:      input.ConfigCluster.ClusterName,
		Namespace: input.ConfigCluster.Namespace,
	})
	Expect(result.Cluster).ToNot(BeNil())
}

func (t testHelper) createDefaultNMT(clusterName, namespace string) *infrav1.NutanixMachineTemplate {
	return &infrav1.NutanixMachineTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      t.generateNMTName(clusterName),
			Namespace: namespace,
		},
		Spec: infrav1.NutanixMachineTemplateSpec{
			Template: infrav1.NutanixMachineTemplateResource{
				Spec: infrav1.NutanixMachineSpec{
					ProviderID:     t.generateNMTProviderID(clusterName),
					BootType:       defaultBootType,
					VCPUsPerSocket: defaultVCPUsPerSocket,
					VCPUSockets:    defaultVCPUSockets,
					MemorySize:     resource.MustParse(defaultMemorySize),
					Image:          t.getNutanixResourceIdentifierFromEnv(imageEnvVarKey),
					Cluster:        t.getNutanixResourceIdentifierFromEnv(clusterEnvVarKey),
					Subnets: []infrav1.NutanixResourceIdentifier{
						t.getNutanixResourceIdentifierFromEnv(subnetEnvVarKey),
					},
					SystemDiskSize: resource.MustParse(defaultSystemDiskSize),
				},
			},
		},
	}
}

type createNutanixMachineTemplateParams struct {
	creator                framework.Creator
	nutanixMachineTemplate client.Object
}

func (t testHelper) createNutanixMachineTemplate(ctx context.Context, params createNutanixMachineTemplateParams) {
	Eventually(func() error {
		return params.creator.Create(ctx, params.nutanixMachineTemplate)
	}, defaultTimeout, defaultInterval).Should(Succeed())
}

type createSecretParams struct {
	namespace   *corev1.Namespace
	clusterName string
	username    string
	password    string
}

func (t testHelper) createSecret(params createSecretParams) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      params.clusterName,
			Namespace: params.namespace.Name,
		},
		Data: map[string][]byte{
			"NUTANIX_USER":     []byte(params.username),
			"NUTANIX_PASSWORD": []byte(params.password),
		},
	}
	Eventually(func() error {
		return bootstrapClusterProxy.GetClient().Create(ctx, secret)
	}, defaultTimeout, defaultInterval).Should(Succeed())
}

type deployClusterParams struct {
	clusterName           string
	namespace             *corev1.Namespace
	flavor                string
	clusterctlConfigPath  string
	artifactFolder        string
	bootstrapClusterProxy framework.ClusterProxy
	e2eConfig             clusterctl.E2EConfig
}

func (t testHelper) deployCluster(params deployClusterParams, clusterResources *clusterctl.ApplyClusterTemplateAndWaitResult) {
	cc := clusterctl.ConfigClusterInput{
		LogFolder:                filepath.Join(params.artifactFolder, "clusters", params.bootstrapClusterProxy.GetName()),
		ClusterctlConfigPath:     params.clusterctlConfigPath,
		KubeconfigPath:           params.bootstrapClusterProxy.GetKubeconfigPath(),
		InfrastructureProvider:   clusterctl.DefaultInfrastructureProvider,
		Flavor:                   params.flavor,
		Namespace:                params.namespace.Name,
		ClusterName:              params.clusterName,
		KubernetesVersion:        params.e2eConfig.GetVariable(KubernetesVersion),
		ControlPlaneMachineCount: pointer.Int64Ptr(1),
		WorkerMachineCount:       pointer.Int64Ptr(1),
	}

	t.createClusterFromConfig(ctx, clusterctl.ApplyClusterTemplateAndWaitInput{
		ClusterProxy:                 params.bootstrapClusterProxy,
		ConfigCluster:                cc,
		WaitForClusterIntervals:      params.e2eConfig.GetIntervals("", "wait-cluster"),
		WaitForControlPlaneIntervals: params.e2eConfig.GetIntervals("", "wait-control-plane"),
		WaitForMachineDeployments:    params.e2eConfig.GetIntervals("", "wait-worker-nodes"),
	}, clusterResources)
}

func (t testHelper) deployClusterAndWait(params deployClusterParams, clusterResources *clusterctl.ApplyClusterTemplateAndWaitResult) {
	cc := clusterctl.ConfigClusterInput{
		LogFolder:                filepath.Join(params.artifactFolder, "clusters", params.bootstrapClusterProxy.GetName()),
		ClusterctlConfigPath:     params.clusterctlConfigPath,
		KubeconfigPath:           params.bootstrapClusterProxy.GetKubeconfigPath(),
		InfrastructureProvider:   clusterctl.DefaultInfrastructureProvider,
		Flavor:                   params.flavor,
		Namespace:                params.namespace.Name,
		ClusterName:              params.clusterName,
		KubernetesVersion:        params.e2eConfig.GetVariable(KubernetesVersion),
		ControlPlaneMachineCount: pointer.Int64Ptr(1),
		WorkerMachineCount:       pointer.Int64Ptr(1),
	}

	clusterctl.ApplyClusterTemplateAndWait(ctx, clusterctl.ApplyClusterTemplateAndWaitInput{
		ClusterProxy:                 params.bootstrapClusterProxy,
		ConfigCluster:                cc,
		WaitForClusterIntervals:      params.e2eConfig.GetIntervals("", "wait-cluster"),
		WaitForControlPlaneIntervals: params.e2eConfig.GetIntervals("", "wait-control-plane"),
		WaitForMachineDeployments:    params.e2eConfig.GetIntervals("", "wait-worker-nodes"),
	}, clusterResources)
}

func (t testHelper) generateNMTName(clusterName string) string {
	return fmt.Sprintf("%s-mt-0", clusterName)
}

func (t testHelper) generateNMTProviderID(clusterName string) string {
	return fmt.Sprintf("nutanix://%s-m1", clusterName)
}

func (t testHelper) generateTestClusterName(specName string) string {
	return fmt.Sprintf("%s-%s", specName, util.RandomString(6))
}

func (t testHelper) getExpectedClusterCategoryKey(clusterName string) string {
	return fmt.Sprintf("%s-%s", defaultClusterCategoryKeyPrefix, clusterName)
}

type getNutanixClusterByNameInput struct {
	Getter    framework.Getter
	Name      string
	Namespace string
}

func (t testHelper) getNutanixClusterByName(ctx context.Context, input getNutanixClusterByNameInput) *infrav1.NutanixCluster {
	cluster := &infrav1.NutanixCluster{}
	key := client.ObjectKey{
		Namespace: input.Namespace,
		Name:      input.Name,
	}
	Expect(input.Getter.Get(ctx, key, cluster)).To(Succeed(), "Failed to get Nutanix Cluster object %s/%s", input.Namespace, input.Name)
	return cluster
}

func (t testHelper) getMachinesForCluster(ctx context.Context, clusterName, namespace string, bootstrapClusterProxy framework.ClusterProxy) *clusterv1.MachineList {
	machineList := &clusterv1.MachineList{}
	labels := map[string]string{clusterv1.ClusterLabelName: clusterName}
	err := bootstrapClusterProxy.GetClient().List(ctx, machineList, client.InNamespace(namespace), client.MatchingLabels(labels))
	Expect(err).ShouldNot(HaveOccurred())
	return machineList
}

func (t testHelper) getNutanixMachineForCluster(ctx context.Context, clusterName, namespace, machineName string, bootstrapClusterProxy framework.ClusterProxy) *infrav1.NutanixMachine {
	machine := &infrav1.NutanixMachine{}

	machineKey := client.ObjectKey{
		Namespace: namespace,
		Name:      machineName,
	}
	err := bootstrapClusterProxy.GetClient().Get(ctx, machineKey, machine)
	Expect(err).ShouldNot(HaveOccurred())
	return machine
}

func (t testHelper) getNutanixMachinesForCluster(ctx context.Context, clusterName, namespace string, bootstrapClusterProxy framework.ClusterProxy) *infrav1.NutanixMachineList {
	machineList := &infrav1.NutanixMachineList{}
	labels := map[string]string{clusterv1.ClusterLabelName: clusterName}
	err := bootstrapClusterProxy.GetClient().List(ctx, machineList, client.InNamespace(namespace), client.MatchingLabels(labels))
	Expect(err).ShouldNot(HaveOccurred())
	return machineList
}

func (t testHelper) getNutanixVMsForCluster(clusterName, namespace string) []*prismGoClientV3.VMIntentResponse {
	nutanixMachines := t.getMachinesForCluster(ctx, clusterName, namespace, bootstrapClusterProxy)
	vms := make([]*prismGoClientV3.VMIntentResponse, 0)
	for _, m := range nutanixMachines.Items {
		machineProviderID := m.Spec.ProviderID
		Expect(machineProviderID).NotTo(BeNil())
		machineVmUUID := t.stripNutanixIDFromProviderID(*machineProviderID)
		vm, err := t.nutanixClient.V3.GetVM(machineVmUUID)
		Expect(err).ShouldNot(HaveOccurred())
		vms = append(vms, vm)
	}
	return vms
}

func (t testHelper) getNutanixResourceIdentifierFromEnv(envVarKey string) infrav1.NutanixResourceIdentifier {
	envVarValue := os.Getenv(envVarKey)
	Expect(envVarValue).ToNot(BeEmpty(), "expected environment variable %s to be set", envVarKey)
	return infrav1.NutanixResourceIdentifier{
		Type: nameType,
		Name: pointer.StringPtr(envVarValue),
	}
}

func (t testHelper) stripNutanixIDFromProviderID(providerID string) string {
	return strings.TrimPrefix(providerID, nutanixProviderIDPrefix)
}

func (t testHelper) verifyCategoryExists(categoryKey, categoryValue string) {
	_, err := t.nutanixClient.V3.GetCategoryValue(categoryKey, categoryValue)
	Expect(err).ShouldNot(HaveOccurred())
}

func (t testHelper) verifyCategoriesNutanixMachines(clusterName, namespace string, expectedCategories map[string]string) {
	nutanixMachines := t.getMachinesForCluster(ctx, clusterName, namespace, bootstrapClusterProxy)
	for _, m := range nutanixMachines.Items {
		machineProviderID := m.Spec.ProviderID
		Expect(machineProviderID).NotTo(BeNil())
		machineVmUUID := t.stripNutanixIDFromProviderID(*machineProviderID)
		vm, err := t.nutanixClient.V3.GetVM(machineVmUUID)
		Expect(err).ShouldNot(HaveOccurred())
		categoriesMeta := vm.Metadata.Categories
		for k, v := range expectedCategories {
			Expect(categoriesMeta).To(HaveKeyWithValue(k, v))
		}
	}
}

type verifyConditionParams struct {
	bootstrapClusterProxy framework.ClusterProxy
	clusterName           string
	namespace             *corev1.Namespace
	expectedCondition     clusterv1.Condition
}

func (t testHelper) verifyConditionOnNutanixCluster(params verifyConditionParams) {
	Eventually(
		func() []clusterv1.Condition {
			cluster := t.getNutanixClusterByName(ctx, getNutanixClusterByNameInput{
				Getter:    params.bootstrapClusterProxy.GetClient(),
				Name:      params.clusterName,
				Namespace: params.namespace.Name,
			})
			return cluster.Status.Conditions
		},
		defaultTimeout,
		defaultInterval,
	).Should(
		ContainElement(
			gstruct.MatchFields(
				gstruct.IgnoreExtras,
				gstruct.Fields{
					"Type":     Equal(params.expectedCondition.Type),
					"Reason":   Equal(params.expectedCondition.Reason),
					"Severity": Equal(params.expectedCondition.Severity),
					"Status":   Equal(params.expectedCondition.Status),
				},
			),
		),
	)
}

func (t testHelper) verifyConditionOnNutanixMachines(params verifyConditionParams) {
	nutanixMachines := t.getNutanixMachinesForCluster(ctx, params.clusterName, params.namespace.Name, params.bootstrapClusterProxy)
	for _, m := range nutanixMachines.Items {
		Eventually(
			func() []clusterv1.Condition {
				machine := t.getNutanixMachineForCluster(ctx, params.clusterName, params.namespace.Name, m.Name, params.bootstrapClusterProxy)
				return machine.Status.Conditions
			},
			defaultTimeout,
			defaultInterval,
		).Should(
			ContainElement(
				gstruct.MatchFields(
					gstruct.IgnoreExtras,
					gstruct.Fields{
						"Type":     Equal(params.expectedCondition.Type),
						"Reason":   Equal(params.expectedCondition.Reason),
						"Severity": Equal(params.expectedCondition.Severity),
						"Status":   Equal(params.expectedCondition.Status),
					},
				),
			),
		)
	}
}

type verifyFailureMessageOnClusterMachinesParams struct {
	clusterName            string
	namespace              *corev1.Namespace
	expectedPhase          string
	expectedFailureMessage string
	bootstrapClusterProxy  framework.ClusterProxy
}

func (t testHelper) verifyFailureMessageOnClusterMachines(ctx context.Context, params verifyFailureMessageOnClusterMachinesParams) {
	Eventually(func() bool {
		nutanixMachines := t.getMachinesForCluster(ctx, params.clusterName, params.namespace.Name, params.bootstrapClusterProxy)
		for _, m := range nutanixMachines.Items {
			machineStatus := m.Status
			if machineStatus.Phase == params.expectedPhase && strings.Contains(*machineStatus.FailureMessage, params.expectedFailureMessage) {
				return true
			}
		}
		return false
	}, defaultTimeout, defaultInterval).Should(BeTrue())
}

type verifyProjectNutanixMachinesParams struct {
	clusterName           string
	namespace             string
	nutanixProjectName    string
	bootstrapClusterProxy framework.ClusterProxy
}

func (t testHelper) verifyProjectNutanixMachines(ctx context.Context, params verifyProjectNutanixMachinesParams) {
	nutanixMachines := t.getNutanixMachinesForCluster(ctx, params.clusterName, params.namespace, params.bootstrapClusterProxy)
	for _, m := range nutanixMachines.Items {
		machineProviderID := m.Spec.ProviderID
		Expect(machineProviderID).NotTo(BeEmpty())
		machineVmUUID := t.stripNutanixIDFromProviderID(machineProviderID)
		vm, err := t.nutanixClient.V3.GetVM(machineVmUUID)
		Expect(err).ShouldNot(HaveOccurred())
		assignedProjectName := *vm.Metadata.ProjectReference.Name
		Expect(assignedProjectName).To(Equal(params.nutanixProjectName))
	}
}
