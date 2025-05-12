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
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	credentialTypes "github.com/nutanix-cloud-native/prism-go-client/environment/credentials"
	prismGoClientV3 "github.com/nutanix-cloud-native/prism-go-client/v3"
	. "github.com/onsi/gomega" //nolint:staticcheck // gomega is used with . imports conventionally
	"github.com/onsi/gomega/gstruct"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/test/framework"
	"sigs.k8s.io/cluster-api/test/framework/bootstrap"
	"sigs.k8s.io/cluster-api/test/framework/clusterctl"
	"sigs.k8s.io/cluster-api/util"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	"github.com/nutanix-cloud-native/cluster-api-provider-nutanix/controllers"
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

	categoryKeyVarKey   = "NUTANIX_ADDITIONAL_CATEGORY_KEY"
	categoryValueVarKey = "NUTANIX_ADDITIONAL_CATEGORY_VALUE"
	imageVarKey         = "NUTANIX_MACHINE_TEMPLATE_IMAGE_NAME"
	clusterVarKey       = "NUTANIX_PRISM_ELEMENT_CLUSTER_NAME"
	subnetVarKey        = "NUTANIX_SUBNET_NAME"

	nameType = "name"

	nutanixProjectNameEnv = "NUTANIX_PROJECT_NAME"

	flavorTopology = "topology"
)

// Test suite global vars.
var (
	ctx = ctrl.SetupSignalHandler()

	// e2eConfig to be used for this test, read from configPath.
	e2eConfig *clusterctl.E2EConfig

	// clusterctlConfigPath to be used for this test, created by generating a clusterctl local repository
	// with the providers specified in the configPath.
	clusterctlConfigPath string

	// bootstrapClusterProvider manages provisioning of the bootstrap cluster to be used for the e2e tests.
	// Please note that provisioning will be skipped if e2e.use-existing-cluster is provided.
	bootstrapClusterProvider bootstrap.ClusterProvider

	// bootstrapClusterProxy allows to interact with the bootstrap cluster to be used for the e2e tests.
	bootstrapClusterProxy framework.ClusterProxy
)

// Test suite flags.
var (
	// configPath is the path to the e2e config file.
	configPath string

	// useExistingCluster instructs the test to use the current cluster instead of creating a new one (default discovery rules apply).
	useExistingCluster bool

	// artifactFolder is the folder to store e2e test artifacts.
	artifactFolder string

	// clusterctlConfig is the file which tests will use as a clusterctl config.
	// If it is not set, a local clusterctl repository (including a clusterctl config) will be created automatically.
	clusterctlConfig string

	// alsoLogToFile enables additional logging to the 'ginkgo-log.txt' file in the artifact folder.
	// These logs also contain timestamps.
	alsoLogToFile bool

	// skipCleanup prevents cleanup of test resources e.g. for debug purposes.
	skipCleanup bool

	// flavor is used to add clusterResourceSet for CNI usage in e2e tests
	flavor string
)

type testHelperInterface interface {
	createClusterFromConfig(ctx context.Context, input clusterctl.ApplyClusterTemplateAndWaitInput, result *clusterctl.ApplyClusterTemplateAndWaitResult)
	createDefaultNMT(clusterName, namespace string) *infrav1.NutanixMachineTemplate
	createDefaultNutanixCluster(clusterName, namespace, controlPlaneEndpointIP string, controlPlanePort int32) *infrav1.NutanixCluster
	createNameGPUNMT(ctx context.Context, clusterName, namespace string, params createGPUNMTParams) *infrav1.NutanixMachineTemplate
	createCapiObject(ctx context.Context, params createCapiObjectParams)
	createDeviceIDGPUNMT(ctx context.Context, clusterName, namespace string, params createGPUNMTParams) *infrav1.NutanixMachineTemplate
	createSecret(params createSecretParams)
	createUUIDNMT(ctx context.Context, clusterName, namespace string) *infrav1.NutanixMachineTemplate
	createUUIDProjectNMT(ctx context.Context, clusterName, namespace string) *infrav1.NutanixMachineTemplate
	createDefaultNMTwithDataDisks(clusterName, namespace string, params withDataDisksParams) *infrav1.NutanixMachineTemplate
	deployCluster(params deployClusterParams, clusterResources *clusterctl.ApplyClusterTemplateAndWaitResult)
	deployClusterAndWait(params deployClusterParams, clusterResources *clusterctl.ApplyClusterTemplateAndWaitResult)
	deleteSecret(params deleteSecretParams)
	deleteAllClustersAndWait(ctx context.Context, specName string, bootstrapClusterProxy framework.ClusterProxy, namespace *corev1.Namespace, intervalsGetter func(spec, key string) []interface{})
	deleteClusterAndWait(ctx context.Context, specName string, bootstrapClusterProxy framework.ClusterProxy, cluster *capiv1.Cluster, intervalsGetter func(spec, key string) []interface{})
	findGPU(ctx context.Context, gpuName string) *prismGoClientV3.GPU
	generateNMTName(clusterName string) string
	generateNMTProviderID(clusterName string) string
	generateTestClusterName(specName string) string
	getMachinesForCluster(ctx context.Context, clusterName, namespace string, bootstrapClusterProxy framework.ClusterProxy) *capiv1.MachineList
	getNutanixMachineForCluster(ctx context.Context, clusterName, namespace, machineName string, bootstrapClusterProxy framework.ClusterProxy) *infrav1.NutanixMachine
	getNutanixMachinesForCluster(ctx context.Context, clusterName, namespace string, bootstrapClusterProxy framework.ClusterProxy) *infrav1.NutanixMachineList
	getNutanixClusterByName(ctx context.Context, input getNutanixClusterByNameInput) *infrav1.NutanixCluster
	getNutanixVMsForCluster(ctx context.Context, clusterName, namespace string) []*prismGoClientV3.VMIntentResponse
	getNutanixResourceIdentifierFromEnv(envVarKey string) infrav1.NutanixResourceIdentifier
	getNutanixResourceIdentifierFromE2eConfig(variableKey string) infrav1.NutanixResourceIdentifier
	getVariableFromE2eConfig(variableKey string) string
	getDefaultStorageContainerNameAndUuid(ctx context.Context) (string, string, error)
	updateVariableInE2eConfig(variableKey string, variableValue string)
	stripNutanixIDFromProviderID(providerID string) string
	verifyCategoryExists(ctx context.Context, categoryKey, categoyValue string)
	verifyCategoriesNutanixMachines(ctx context.Context, clusterName, namespace string, expectedCategories map[string]string)
	verifyConditionOnNutanixCluster(params verifyConditionParams)
	verifyConditionOnNutanixMachines(params verifyConditionParams)
	verifyDisksOnNutanixMachines(ctx context.Context, params verifyDisksOnNutanixMachinesParams)
	verifyFailureDomainsOnClusterMachines(ctx context.Context, params verifyFailureDomainsOnClusterMachinesParams)
	verifyFailureMessageOnClusterMachines(ctx context.Context, params verifyFailureMessageOnClusterMachinesParams)
	verifyGPUNutanixMachines(ctx context.Context, params verifyGPUNutanixMachinesParams)
	verifyProjectNutanixMachines(ctx context.Context, params verifyProjectNutanixMachinesParams)
	verifyResourceConfigOnNutanixMachines(ctx context.Context, params verifyResourceConfigOnNutanixMachinesParams)
}

type testHelper struct {
	nutanixClient *prismGoClientV3.Client
	e2eConfig     *clusterctl.E2EConfig
}

func newTestHelper(e2eConfig *clusterctl.E2EConfig) testHelperInterface {
	c, err := initNutanixClient(*e2eConfig)
	Expect(err).ShouldNot(HaveOccurred())

	return testHelper{
		nutanixClient: c,
		e2eConfig:     e2eConfig,
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

	Expect(input.ClusterProxy.CreateOrUpdate(ctx, clusterTemplate, input.CreateOrUpdateOpts...)).To(Succeed())

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
					Image:          ptr.To(t.getNutanixResourceIdentifierFromE2eConfig(imageVarKey)),
					Cluster:        t.getNutanixResourceIdentifierFromE2eConfig(clusterVarKey),
					Subnets: []infrav1.NutanixResourceIdentifier{
						t.getNutanixResourceIdentifierFromE2eConfig(subnetVarKey),
					},
					SystemDiskSize: resource.MustParse(defaultSystemDiskSize),
				},
			},
		},
	}
}

func (t testHelper) createUUIDNMT(ctx context.Context, clusterName, namespace string) *infrav1.NutanixMachineTemplate {
	imageVarValue := t.getVariableFromE2eConfig(imageVarKey)
	clusterVarValue := t.getVariableFromE2eConfig(clusterVarKey)
	subnetVarValue := t.getVariableFromE2eConfig(subnetVarKey)

	clusterUUID, err := controllers.GetPEUUID(ctx, t.nutanixClient, &clusterVarValue, nil)
	Expect(err).ToNot(HaveOccurred())

	image, err := controllers.GetImage(ctx, t.nutanixClient, infrav1.NutanixResourceIdentifier{
		Type: infrav1.NutanixIdentifierName,
		Name: ptr.To(imageVarValue),
	})
	Expect(err).ToNot(HaveOccurred())

	subnetUUID, err := controllers.GetSubnetUUID(ctx, t.nutanixClient, clusterUUID, &subnetVarValue, nil)
	Expect(err).ToNot(HaveOccurred())

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
					Image: &infrav1.NutanixResourceIdentifier{
						Type: infrav1.NutanixIdentifierUUID,
						UUID: image.Metadata.UUID,
					},
					Cluster: infrav1.NutanixResourceIdentifier{
						Type: infrav1.NutanixIdentifierUUID,
						UUID: ptr.To(clusterUUID),
					},
					Subnets: []infrav1.NutanixResourceIdentifier{
						{
							Type: infrav1.NutanixIdentifierUUID,
							UUID: ptr.To(subnetUUID),
						},
					},
					SystemDiskSize: resource.MustParse(defaultSystemDiskSize),
				},
			},
		},
	}
}

func (t testHelper) createUUIDProjectNMT(ctx context.Context, clusterName, namespace string) *infrav1.NutanixMachineTemplate {
	projectVarValue := t.getVariableFromE2eConfig(nutanixProjectNameEnv)
	projectUUID, err := controllers.GetProjectUUID(ctx, t.nutanixClient, &projectVarValue, nil)
	Expect(err).ToNot(HaveOccurred())

	nmt := t.createUUIDNMT(ctx, clusterName, namespace)
	nmt.Spec.Template.Spec.Project = &infrav1.NutanixResourceIdentifier{
		Type: infrav1.NutanixIdentifierUUID,
		UUID: &projectUUID,
	}
	return nmt
}

type createGPUNMTParams struct {
	gpuVendorEnvKey string
	gpuNameEnvKey   string
}

func (t testHelper) createNameGPUNMT(ctx context.Context, clusterName, namespace string, params createGPUNMTParams) *infrav1.NutanixMachineTemplate {
	gpuName := t.getVariableFromE2eConfig(params.gpuNameEnvKey)
	_ = t.getVariableFromE2eConfig(params.gpuVendorEnvKey)

	nmt := t.createDefaultNMT(clusterName, namespace)
	nmt.Spec.Template.Spec.GPUs = []infrav1.NutanixGPU{
		{
			Type: infrav1.NutanixGPUIdentifierName,
			Name: &gpuName,
		},
	}
	return nmt
}

func (t testHelper) findGPU(ctx context.Context, gpuName string) *prismGoClientV3.GPU {
	clusterVarValue := t.getVariableFromE2eConfig(clusterVarKey)

	clusterUUID, err := controllers.GetPEUUID(ctx, t.nutanixClient, &clusterVarValue, nil)
	Expect(err).ToNot(HaveOccurred())
	Expect(clusterUUID).ToNot(BeNil())
	allGpus, err := controllers.GetGPUsForPE(ctx, t.nutanixClient, clusterUUID)
	Expect(err).ToNot(HaveOccurred())
	Expect(allGpus).ToNot(HaveLen(0))

	for _, gpu := range allGpus {
		if gpu == nil {
			continue
		}
		if gpu.Name == gpuName {
			return gpu
		}
	}
	return nil
}

func (t testHelper) createDeviceIDGPUNMT(ctx context.Context, clusterName, namespace string, params createGPUNMTParams) *infrav1.NutanixMachineTemplate {
	gpuName := t.getVariableFromE2eConfig(params.gpuNameEnvKey)
	foundGpu := t.findGPU(ctx, gpuName)
	Expect(foundGpu).ToNot(BeNil())

	nmt := t.createUUIDNMT(ctx, clusterName, namespace)
	nmt.Spec.Template.Spec.GPUs = []infrav1.NutanixGPU{
		{
			Type:     infrav1.NutanixGPUIdentifierDeviceID,
			DeviceID: foundGpu.DeviceID,
		},
	}
	return nmt
}

type withDataDisksParams struct {
	DataDisks []infrav1.NutanixMachineVMDisk
}

func (t testHelper) createDefaultNMTwithDataDisks(clusterName, namespace string, params withDataDisksParams) *infrav1.NutanixMachineTemplate {
	defNmt := t.createDefaultNMT(clusterName, namespace)
	defNmt.Spec.Template.Spec.DataDisks = params.DataDisks

	return defNmt
}

func (t testHelper) createDefaultNutanixCluster(clusterName, namespace, controlPlaneEndpointIP string, controlPlanePort int32) *infrav1.NutanixCluster {
	return &infrav1.NutanixCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterName,
			Namespace: namespace,
		},
		Spec: infrav1.NutanixClusterSpec{
			ControlPlaneEndpoint: capiv1.APIEndpoint{
				Host: controlPlaneEndpointIP,
				Port: controlPlanePort,
			},
		},
	}
}

type createCapiObjectParams struct {
	creator    framework.Creator
	capiObject client.Object
}

func (t testHelper) createCapiObject(ctx context.Context, params createCapiObjectParams) {
	Eventually(func() error {
		return params.creator.Create(ctx, params.capiObject)
	}, defaultTimeout, defaultInterval).Should(Succeed())
}

type createSecretParams struct {
	namespace   *corev1.Namespace
	clusterName string
	username    string
	password    string
}

func (t testHelper) createSecret(params createSecretParams) {
	ba := credentialTypes.BasicAuthCredential{
		PrismCentral: credentialTypes.PrismCentralBasicAuth{
			BasicAuth: credentialTypes.BasicAuth{
				Username: params.username,
				Password: params.password,
			},
		},
	}
	baBytes, err := json.Marshal(ba)
	Expect(err).ToNot(HaveOccurred())
	creds := []credentialTypes.Credential{
		{
			Type: credentialTypes.BasicAuthCredentialType,
			Data: baBytes,
		},
	}
	credsBytes, err := json.Marshal(creds)
	Expect(err).ToNot(HaveOccurred())
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      params.clusterName,
			Namespace: params.namespace.Name,
		},
		Data: map[string][]byte{
			"credentials": credsBytes,
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
		KubernetesVersion:        t.e2eConfig.GetVariable(KubernetesVersion),
		ControlPlaneMachineCount: ptr.To(int64(1)),
		WorkerMachineCount:       ptr.To(int64(1)),
	}

	t.createClusterFromConfig(ctx, clusterctl.ApplyClusterTemplateAndWaitInput{
		ClusterProxy:                 params.bootstrapClusterProxy,
		ConfigCluster:                cc,
		WaitForClusterIntervals:      t.e2eConfig.GetIntervals("", "wait-cluster"),
		WaitForControlPlaneIntervals: t.e2eConfig.GetIntervals("", "wait-control-plane"),
		WaitForMachineDeployments:    t.e2eConfig.GetIntervals("", "wait-worker-nodes"),
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
		KubernetesVersion:        t.e2eConfig.GetVariable(KubernetesVersion),
		ControlPlaneMachineCount: ptr.To(int64(1)),
		WorkerMachineCount:       ptr.To(int64(1)),
	}

	clusterctl.ApplyClusterTemplateAndWait(ctx, clusterctl.ApplyClusterTemplateAndWaitInput{
		ClusterProxy:                 params.bootstrapClusterProxy,
		ConfigCluster:                cc,
		WaitForClusterIntervals:      t.e2eConfig.GetIntervals("", "wait-cluster"),
		WaitForControlPlaneIntervals: t.e2eConfig.GetIntervals("", "wait-control-plane"),
		WaitForMachineDeployments:    t.e2eConfig.GetIntervals("", "wait-worker-nodes"),
	}, clusterResources)
}

func (t testHelper) deleteAllClustersAndWait(ctx context.Context, specName string, bootstrapClusterProxy framework.ClusterProxy, namespace *corev1.Namespace, intervalsGetter func(spec, key string) []interface{}) {
	framework.DeleteAllClustersAndWait(ctx, framework.DeleteAllClustersAndWaitInput{
		Client:    bootstrapClusterProxy.GetClient(),
		Namespace: namespace.Name,
	}, intervalsGetter(specName, "wait-delete-cluster")...)
}

func (t testHelper) deleteClusterAndWait(ctx context.Context, specName string, bootstrapClusterProxy framework.ClusterProxy, cluster *capiv1.Cluster, intervalsGetter func(spec, key string) []interface{}) {
	framework.DeleteClusterAndWait(ctx, framework.DeleteClusterAndWaitInput{
		Client:  bootstrapClusterProxy.GetClient(),
		Cluster: cluster,
	}, intervalsGetter(specName, "wait-delete-cluster")...)
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

func (t testHelper) getMachinesForCluster(ctx context.Context, clusterName, namespace string, bootstrapClusterProxy framework.ClusterProxy) *capiv1.MachineList {
	machineList := &capiv1.MachineList{}
	labels := map[string]string{capiv1.ClusterNameLabel: clusterName}
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
	labels := map[string]string{capiv1.ClusterNameLabel: clusterName}
	err := bootstrapClusterProxy.GetClient().List(ctx, machineList, client.InNamespace(namespace), client.MatchingLabels(labels))
	Expect(err).ShouldNot(HaveOccurred())
	return machineList
}

func (t testHelper) getNutanixVMsForCluster(ctx context.Context, clusterName, namespace string) []*prismGoClientV3.VMIntentResponse {
	nutanixMachines := t.getMachinesForCluster(ctx, clusterName, namespace, bootstrapClusterProxy)
	vms := make([]*prismGoClientV3.VMIntentResponse, 0)
	for _, m := range nutanixMachines.Items {
		machineProviderID := m.Spec.ProviderID
		Expect(machineProviderID).NotTo(BeNil())
		machineVmUUID := t.stripNutanixIDFromProviderID(*machineProviderID)
		vm, err := t.nutanixClient.V3.GetVM(ctx, machineVmUUID)
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
		Name: ptr.To(envVarValue),
	}
}

func (t testHelper) getNutanixResourceIdentifierFromE2eConfig(variableKey string) infrav1.NutanixResourceIdentifier {
	variableValue := t.getVariableFromE2eConfig(variableKey)
	return infrav1.NutanixResourceIdentifier{
		Type: nameType,
		Name: ptr.To(variableValue),
	}
}

func (t testHelper) getVariableFromE2eConfig(variableKey string) string {
	Expect(t.e2eConfig.HasVariable(variableKey)).To(BeTrue(), "expected e2econfig variable %s to exist", variableKey)
	variableValue := t.e2eConfig.GetVariable(variableKey)
	Expect(variableValue).ToNot(BeEmpty(), "expected e2econfig variable %s to be set", variableKey)
	return variableValue
}

func (t testHelper) getDefaultStorageContainerNameAndUuid(ctx context.Context) (string, string, error) {
	scName := ""
	scUUID := ""

	scResponse, err := controllers.ListStorageContainers(ctx, t.nutanixClient)
	if err != nil {
		return "", "", err
	}

	if len(scResponse) == 0 {
		return "", "", fmt.Errorf("no storage containers found")
	}

	peName := t.getVariableFromE2eConfig(clusterVarKey)

	for _, sc := range scResponse {
		if strings.Contains(*sc.Name, "default") && strings.EqualFold(*sc.ClusterName, peName) {
			if sc.Name != nil {
				scName = *sc.Name
			}

			if sc.UUID != nil {
				scUUID = *sc.UUID
			}

			return scName, scUUID, nil
		}
	}

	return "", "", fmt.Errorf("no default storage container found")
}

func (t testHelper) updateVariableInE2eConfig(variableKey string, variableValue string) {
	t.e2eConfig.Variables[variableKey] = variableValue
	os.Setenv(variableKey, variableValue)
}

func (t testHelper) stripNutanixIDFromProviderID(providerID string) string {
	return strings.TrimPrefix(providerID, nutanixProviderIDPrefix)
}

func (t testHelper) verifyCategoryExists(ctx context.Context, categoryKey, categoryValue string) {
	_, err := t.nutanixClient.V3.GetCategoryValue(ctx, categoryKey, categoryValue)
	Expect(err).ShouldNot(HaveOccurred())
}

func (t testHelper) verifyCategoriesNutanixMachines(ctx context.Context, clusterName, namespace string, expectedCategories map[string]string) {
	nutanixMachines := t.getMachinesForCluster(ctx, clusterName, namespace, bootstrapClusterProxy)
	for _, m := range nutanixMachines.Items {
		machineProviderID := m.Spec.ProviderID
		Expect(machineProviderID).NotTo(BeNil())
		machineVmUUID := t.stripNutanixIDFromProviderID(*machineProviderID)
		vm, err := t.nutanixClient.V3.GetVM(ctx, machineVmUUID)
		Expect(err).ShouldNot(HaveOccurred())
		categoriesMeta := vm.Metadata.Categories
		for k, v := range expectedCategories {
			Expect(categoriesMeta).To(HaveKeyWithValue(k, v))
		}
	}
}

type verifyResourceConfigOnNutanixMachinesParams struct {
	clusterName                string
	namespace                  *corev1.Namespace
	toMachineMemorySizeGib     int64
	toMachineSystemDiskSizeGib int64
	toMachineVCPUSockets       int64
	toMachineVCPUsPerSocket    int64
	bootstrapClusterProxy      framework.ClusterProxy
}

func (t testHelper) verifyResourceConfigOnNutanixMachines(ctx context.Context, params verifyResourceConfigOnNutanixMachinesParams) {
	Eventually(
		func(g Gomega) {
			nutanixMachines := t.getMachinesForCluster(ctx,
				params.clusterName,
				params.namespace.Name,
				params.bootstrapClusterProxy)
			for _, m := range nutanixMachines.Items {
				machineProviderID := m.Spec.ProviderID
				g.Expect(machineProviderID).NotTo(BeNil())
				machineVmUUID := t.stripNutanixIDFromProviderID(*machineProviderID)
				vm, err := t.nutanixClient.V3.GetVM(ctx, machineVmUUID)
				g.Expect(err).ShouldNot(HaveOccurred())
				vmMemorySizeInMib := *vm.Status.Resources.MemorySizeMib
				g.Expect(vmMemorySizeInMib).To(Equal(params.toMachineMemorySizeGib*1024), "expected memory size of VMs to be equal to %d but was %d", params.toMachineMemorySizeGib*1024, vmMemorySizeInMib)
				vmNumSockets := *vm.Status.Resources.NumSockets
				g.Expect(vmNumSockets).To(Equal(params.toMachineVCPUSockets), "expected num sockets of VMs to be equal to %d but was %d", params.toMachineVCPUSockets, vmNumSockets)
				vmNumVcpusPerSocket := *vm.Status.Resources.NumVcpusPerSocket
				g.Expect(vmNumVcpusPerSocket).To(Equal(params.toMachineVCPUsPerSocket), "expected vcpu per socket of VMs to be equal to %d but was %d", params.toMachineVCPUsPerSocket, vmNumVcpusPerSocket)
				// TODO check system disk size as well
			}
		},
		defaultTimeout,
		defaultInterval,
	).Should(Succeed())
}

type verifyConditionParams struct {
	bootstrapClusterProxy framework.ClusterProxy
	clusterName           string
	namespace             *corev1.Namespace
	expectedCondition     capiv1.Condition
}

func (t testHelper) verifyConditionOnNutanixCluster(params verifyConditionParams) {
	Eventually(
		func() []capiv1.Condition {
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
	Eventually(
		func() []infrav1.NutanixMachine {
			nutanixMachines := t.getNutanixMachinesForCluster(ctx, params.clusterName, params.namespace.Name, params.bootstrapClusterProxy)
			return nutanixMachines.Items
		},
		defaultTimeout,
		defaultInterval,
	).Should(
		ContainElement(
			gstruct.MatchFields(
				gstruct.IgnoreExtras,
				gstruct.Fields{
					"Status": gstruct.MatchFields(
						gstruct.IgnoreExtras,
						gstruct.Fields{
							"Conditions": ContainElement(
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
						},
					),
				},
			),
		),
	)
}

type verifyFailureDomainsOnClusterMachinesParams struct {
	clusterName           string
	namespace             *corev1.Namespace
	failureDomainNames    []string
	bootstrapClusterProxy framework.ClusterProxy
}

func (t testHelper) verifyFailureDomainsOnClusterMachines(ctx context.Context, params verifyFailureDomainsOnClusterMachinesParams) {
	Eventually(func() bool {
		nutanixCluster := t.getNutanixClusterByName(ctx, getNutanixClusterByNameInput{
			Getter:    params.bootstrapClusterProxy.GetClient(),
			Name:      params.clusterName,
			Namespace: params.namespace.Name,
		})
		Expect(nutanixCluster).ToNot(BeNil())
		var match bool
		for _, fdName := range params.failureDomainNames {
			nutanixMachines := t.getMachinesForCluster(ctx, params.clusterName, params.namespace.Name, params.bootstrapClusterProxy)
			for _, m := range nutanixMachines.Items {
				machineSpec := m.Spec
				if *machineSpec.FailureDomain == fdName {
					// failure domain had a match
					match = true
					// Search for failure domain
					fd, err := controllers.GetFailureDomain(fdName, nutanixCluster)
					Expect(err).ShouldNot(HaveOccurred())
					Expect(fd).ToNot(BeNil())
					// Search for VM
					machineVmUUID := t.stripNutanixIDFromProviderID(*machineSpec.ProviderID)
					vm, err := t.nutanixClient.V3.GetVM(ctx, machineVmUUID)
					Expect(err).ShouldNot(HaveOccurred())
					Expect(vm).ToNot(BeNil())
					// Check if correct PE and subnet are used
					Expect(*vm.Spec.ClusterReference.Name).To(Equal(*fd.Cluster.Name))
					Expect(*vm.Spec.Resources.NicList[0].SubnetReference.Name).To(Equal(*fd.Subnets[0].Name))
					break
				}
			}
			if !match {
				return false
			}
		}
		return true
	}, defaultTimeout, defaultInterval).Should(BeTrue())
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
		vm, err := t.nutanixClient.V3.GetVM(ctx, machineVmUUID)
		Expect(err).ShouldNot(HaveOccurred())
		assignedProjectName := *vm.Metadata.ProjectReference.Name
		Expect(assignedProjectName).To(Equal(params.nutanixProjectName))
	}
}

type verifyGPUNutanixMachinesParams struct {
	clusterName           string
	namespace             string
	gpuName               string
	bootstrapClusterProxy framework.ClusterProxy
}

func (t testHelper) verifyGPUNutanixMachines(ctx context.Context, params verifyGPUNutanixMachinesParams) {
	nutanixMachines := t.getNutanixMachinesForCluster(ctx, params.clusterName, params.namespace, params.bootstrapClusterProxy)
	for _, m := range nutanixMachines.Items {
		machineProviderID := m.Spec.ProviderID
		Expect(machineProviderID).NotTo(BeEmpty())
		machineVmUUID := t.stripNutanixIDFromProviderID(machineProviderID)
		vm, err := t.nutanixClient.V3.GetVM(ctx, machineVmUUID)
		Expect(err).ShouldNot(HaveOccurred())
		foundGpu := t.findGPU(ctx, params.gpuName)
		Expect(foundGpu).ToNot(BeNil())
		gpuList := vm.Spec.Resources.GpuList
		Expect(gpuList).ToNot(HaveLen(0))
		Expect(gpuList).To(ContainElement(
			HaveValue(
				gstruct.MatchFields(
					gstruct.IgnoreExtras,
					gstruct.Fields{
						"DeviceID": HaveValue(Equal(*foundGpu.DeviceID)),
						"Vendor":   HaveValue(Equal(foundGpu.Vendor)),
					},
				),
			)))
	}
}

type verifyDisksOnNutanixMachinesParams struct {
	clusterName           string
	namespace             string
	bootstrapClusterProxy framework.ClusterProxy
	diskCount             int
}

func (t testHelper) verifyDisksOnNutanixMachines(ctx context.Context, params verifyDisksOnNutanixMachinesParams) {
	nutanixMachines := t.getNutanixMachinesForCluster(ctx, params.clusterName, params.namespace, params.bootstrapClusterProxy)
	for _, m := range nutanixMachines.Items {
		machineProviderID := m.Spec.ProviderID
		Expect(machineProviderID).NotTo(BeEmpty())
		machineVmUUID := t.stripNutanixIDFromProviderID(machineProviderID)
		vm, err := t.nutanixClient.V3.GetVM(ctx, machineVmUUID)
		Expect(err).ShouldNot(HaveOccurred())
		disks := vm.Status.Resources.DiskList
		Expect(disks).To(HaveLen(params.diskCount))
	}
}

type deleteSecretParams struct {
	namespace   *corev1.Namespace
	clusterName string
}

func (t testHelper) deleteSecret(params deleteSecretParams) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      params.clusterName,
			Namespace: params.namespace.Name,
		},
	}

	Eventually(func() error {
		return bootstrapClusterProxy.GetClient().Delete(ctx, secret)
	}, time.Second*5, defaultInterval).Should(Succeed())
}

func init() {
	flag.StringVar(&configPath, "e2e.config", "", "path to the e2e config file")
	flag.StringVar(&artifactFolder, "e2e.artifacts-folder", "", "folder where e2e test artifact should be stored")
	flag.BoolVar(&alsoLogToFile, "e2e.also-log-to-file", true, "if true, ginkgo logs are additionally written to the `ginkgo-log.txt` file in the artifacts folder (including timestamps)")
	flag.BoolVar(&skipCleanup, "e2e.skip-resource-cleanup", false, "if true, the resource cleanup after tests will be skipped")
	flag.StringVar(&clusterctlConfig, "e2e.clusterctl-config", "", "file which tests will use as a clusterctl config. If it is not set, a local clusterctl repository (including a clusterctl config) will be created automatically.")
	flag.BoolVar(&useExistingCluster, "e2e.use-existing-cluster", false, "if true, the test uses the current cluster instead of creating a new one (default discovery rules apply)")
}
