//go:build e2e

/*
Copyright 2020 The Kubernetes Authors.

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
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/test/framework"
	"sigs.k8s.io/cluster-api/test/framework/bootstrap"
	"sigs.k8s.io/cluster-api/test/framework/clusterctl"
	"sigs.k8s.io/cluster-api/test/framework/ginkgoextensions"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
)

func TestE2E(t *testing.T) {
	// If running in prow, make sure to use the artifacts folder that will be reported in test grid (ignoring the value provided by flag).
	if prowArtifactFolder, exists := os.LookupEnv("ARTIFACTS"); exists {
		artifactFolder = prowArtifactFolder
	}

	RegisterFailHandler(Fail)

	if alsoLogToFile {
		w, err := ginkgoextensions.EnableFileLogging(filepath.Join(artifactFolder, "ginkgo-log.txt"))
		Expect(err).ToNot(HaveOccurred())
		defer w.Close()
	}

	RunSpecs(t, "capx-e2e")
}

// Using a SynchronizedBeforeSuite for controlling how to create resources shared across ParallelNodes (~ginkgo threads).
// The local clusterctl repository & the bootstrap cluster are created once and shared across all the tests.
var _ = SynchronizedBeforeSuite(func() []byte {
	// Before all ParallelNodes.

	// This line prevents controller-runtime from complaining about log.SetLogger not being called.
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	Expect(configPath).To(BeAnExistingFile(), "Invalid test suite argument. e2e.config should be an existing file.")
	Expect(os.MkdirAll(artifactFolder, 0o755)).To(Succeed(), "Invalid test suite argument. Can't create e2e.artifacts-folder %q", artifactFolder)

	By("Initializing a runtime.Scheme with all the GVK relevant for this test")
	scheme := initScheme()

	Byf("Loading the e2e test configuration from %q", configPath)
	e2eConfig = loadE2EConfig(configPath)

	if clusterctlConfig == "" {
		Byf("Creating a clusterctl local repository into %q", artifactFolder)
		clusterctlConfigPath = createClusterctlLocalRepository(e2eConfig, filepath.Join(artifactFolder, "repository"))
	} else {
		Byf("Using existing clusterctl config %q", clusterctlConfig)
		clusterctlConfigPath = clusterctlConfig
	}

	By("Setting up the bootstrap cluster")
	bootstrapClusterProvider, bootstrapClusterProxy = setupBootstrapCluster(e2eConfig, scheme, useExistingCluster)

	By("Initializing the bootstrap cluster")
	initBootstrapCluster(bootstrapClusterProxy, e2eConfig, clusterctlConfigPath, artifactFolder)

	return []byte(
		strings.Join([]string{
			artifactFolder,
			configPath,
			clusterctlConfigPath,
			bootstrapClusterProxy.GetKubeconfigPath(),
		}, ","),
	)
}, func(data []byte) {
	// Before each ParallelNode.

	parts := strings.Split(string(data), ",")
	Expect(parts).To(HaveLen(4))

	artifactFolder = parts[0]
	configPath = parts[1]
	clusterctlConfigPath = parts[2]
	kubeconfigPath := parts[3]

	e2eConfig = loadE2EConfig(configPath)
	bootstrapClusterProxy = framework.NewClusterProxy("bootstrap", kubeconfigPath, initScheme(), framework.WithMachineLogCollector(framework.DockerLogCollector{}))
})

// Using a SynchronizedAfterSuite for controlling how to delete resources shared across ParallelNodes (~ginkgo threads).
// The bootstrap cluster is shared across all the tests, so it should be deleted only after all ParallelNodes completes.
// The local clusterctl repository is preserved like everything else created into the artifact folder.
var _ = SynchronizedAfterSuite(func() {
	// After each ParallelNode.
}, func() {
	// After all ParallelNodes.

	By("Dumping logs from the bootstrap cluster")
	dumpBootstrapClusterLogs(bootstrapClusterProxy)

	By("Tearing down the management cluster")
	if !skipCleanup {
		tearDown(bootstrapClusterProvider, bootstrapClusterProxy)
	}
})

func initScheme() *runtime.Scheme {
	sc := runtime.NewScheme()
	framework.TryAddDefaultSchemes(sc)
	err := infrav1.AddToScheme(sc)
	Expect(err).NotTo(HaveOccurred())
	err = storagev1.AddToScheme(sc)
	Expect(err).NotTo(HaveOccurred())
	return sc
}

func loadE2EConfig(configPath string) *clusterctl.E2EConfig {
	config := clusterctl.LoadE2EConfig(ctx, clusterctl.LoadE2EConfigInput{ConfigPath: configPath})
	Expect(config).ToNot(BeNil(), "Failed to load E2E config from %s", configPath)

	return config
}

func createClusterctlLocalRepository(config *clusterctl.E2EConfig, repositoryFolder string) string {
	createRepositoryInput := clusterctl.CreateRepositoryInput{
		E2EConfig:        config,
		RepositoryFolder: repositoryFolder,
	}

	// Ensuring a CNI file is defined in the config and register a FileTransformation to inject the referenced file in place of the CNI_RESOURCES envSubst variable.
	Expect(config.Variables).To(HaveKey(CNIPath), "Missing %s variable in the config", CNIPath)
	cniPath := config.MustGetVariable(CNIPath)
	Expect(cniPath).To(BeAnExistingFile(), "The %s variable should resolve to an existing file", CNIPath)

	createRepositoryInput.RegisterClusterResourceSetConfigMapTransformation(cniPath, CNIResources)

	clusterctlConfig := clusterctl.CreateRepository(ctx, createRepositoryInput)
	Expect(clusterctlConfig).To(BeAnExistingFile(), "The clusterctl config file does not exists in the local repository %s", repositoryFolder)
	return clusterctlConfig
}

func setupBootstrapCluster(config *clusterctl.E2EConfig, scheme *runtime.Scheme, useExistingCluster bool) (bootstrap.ClusterProvider, framework.ClusterProxy) {
	var clusterProvider bootstrap.ClusterProvider
	kubeconfigPath := ""
	if !useExistingCluster {
		By("Creating the bootstrap cluster")
		clusterProvider = bootstrap.CreateKindBootstrapClusterAndLoadImages(ctx, bootstrap.CreateKindBootstrapClusterAndLoadImagesInput{
			Name:               config.ManagementClusterName,
			KubernetesVersion:  config.MustGetVariable(KubernetesVersionManagement),
			RequiresDockerSock: config.HasDockerProvider(),
			Images:             config.Images,
			IPFamily:           config.MustGetVariable(IPFamily),
			LogFolder:          filepath.Join(artifactFolder, "kind"),
		})
		Expect(clusterProvider).ToNot(BeNil(), "Failed to create a bootstrap cluster")

		kubeconfigPath = clusterProvider.GetKubeconfigPath()
		Expect(kubeconfigPath).To(BeAnExistingFile(), "Failed to get the kubeconfig file for the bootstrap cluster")
	} else {
		By("Using an existing bootstrap cluster")
	}

	clusterProxy := framework.NewClusterProxy("bootstrap", kubeconfigPath, scheme)
	Expect(clusterProxy).ToNot(BeNil(), "Failed to get a bootstrap cluster proxy")

	return clusterProvider, clusterProxy
}

func initBootstrapCluster(bootstrapClusterProxy framework.ClusterProxy, config *clusterctl.E2EConfig, clusterctlConfig, artifactFolder string) {
	clusterctl.InitManagementClusterAndWatchControllerLogs(ctx, clusterctl.InitManagementClusterAndWatchControllerLogsInput{
		ClusterProxy:            bootstrapClusterProxy,
		ClusterctlConfigPath:    clusterctlConfig,
		InfrastructureProviders: config.InfrastructureProviders(),
		LogFolder:               filepath.Join(artifactFolder, "clusters", bootstrapClusterProxy.GetName()),
	}, config.GetIntervals(bootstrapClusterProxy.GetName(), "wait-controllers")...)
}

func dumpBootstrapClusterLogs(bootstrapClusterProxy framework.ClusterProxy) {
	if bootstrapClusterProxy == nil {
		return
	}

	clusterLogCollector := bootstrapClusterProxy.GetLogCollector()
	if clusterLogCollector == nil {
		return
	}

	nodes, err := bootstrapClusterProxy.GetClientSet().CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		fmt.Printf("Failed to get nodes for the bootstrap cluster: %v\n", err)
		return
	}

	for i := range nodes.Items {
		nodeName := nodes.Items[i].GetName()
		err = clusterLogCollector.CollectMachineLog(
			ctx,
			bootstrapClusterProxy.GetClient(),
			// The bootstrap cluster is not expected to be a CAPI cluster, so in order to re-use the logCollector,
			// we create a fake machine that wraps the node.
			// NOTE: This assumes a naming convention between machines and nodes, which e.g. applies to the bootstrap clusters generated with kind.
			//       This might not work if you are using an existing bootstrap cluster provided by other means.
			&clusterv1.Machine{
				Spec:       clusterv1.MachineSpec{ClusterName: nodeName},
				ObjectMeta: metav1.ObjectMeta{Name: nodeName},
			},
			filepath.Join(artifactFolder, "clusters", bootstrapClusterProxy.GetName(), "machines", nodeName),
		)
		if err != nil {
			fmt.Printf("Failed to get logs for the bootstrap cluster node %s: %v\n", nodeName, err)
		}
	}
}

func tearDown(bootstrapClusterProvider bootstrap.ClusterProvider, bootstrapClusterProxy framework.ClusterProxy) {
	if bootstrapClusterProxy != nil {
		bootstrapClusterProxy.Dispose(ctx)
	}
	if bootstrapClusterProvider != nil {
		bootstrapClusterProvider.Dispose(ctx)
	}
}
