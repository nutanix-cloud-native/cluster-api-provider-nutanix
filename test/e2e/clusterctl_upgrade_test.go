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
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/blang/semver/v4"
	"github.com/nutanix-cloud-native/cluster-api-provider-nutanix/test/e2e/log"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	bootstrapv1 "sigs.k8s.io/cluster-api/bootstrap/kubeadm/api/v1beta1"
	yaml "sigs.k8s.io/cluster-api/cmd/clusterctl/client/yamlprocessor"
	capi_e2e "sigs.k8s.io/cluster-api/test/e2e"
	"sigs.k8s.io/cluster-api/test/framework"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var kubernetesVersion = getKubernetesVersion()

func getKubernetesVersion() string {
	if e2eConfig != nil {
		if result, ok := e2eConfig.Variables["KUBERNETES_VERSION"]; ok {
			return result
		}
	} else {
		if result, ok := os.LookupEnv("KUBERNETES_VERSION"); ok {
			return result
		}
	}

	return "undefined"
}

var _ = Describe("[clusterctl-Upgrade] Upgrade CAPX (v1.2.4 => current) K8S "+kubernetesVersion, Label("clusterctl-upgrade"), func() {
	preWaitForCluster := createPreWaitForClusterFunc(func() capi_e2e.ClusterctlUpgradeSpecInput {
		return capi_e2e.ClusterctlUpgradeSpecInput{
			E2EConfig:             e2eConfig,
			ClusterctlConfigPath:  clusterctlConfigPath,
			BootstrapClusterProxy: bootstrapClusterProxy,
			ArtifactFolder:        artifactFolder,
		}
	})

	postUpgradeFunc := createPostUpgradeFunc(func() capi_e2e.ClusterctlUpgradeSpecInput {
		return capi_e2e.ClusterctlUpgradeSpecInput{
			E2EConfig:             e2eConfig,
			ClusterctlConfigPath:  clusterctlConfigPath,
			BootstrapClusterProxy: bootstrapClusterProxy,
			ArtifactFolder:        artifactFolder,
		}
	})

	capi_e2e.ClusterctlUpgradeSpec(ctx, func() capi_e2e.ClusterctlUpgradeSpecInput {
		return capi_e2e.ClusterctlUpgradeSpecInput{
			E2EConfig:                       e2eConfig,
			ClusterctlConfigPath:            clusterctlConfigPath,
			BootstrapClusterProxy:           bootstrapClusterProxy,
			ArtifactFolder:                  artifactFolder,
			SkipCleanup:                     skipCleanup,
			InitWithBinary:                  "https://github.com/kubernetes-sigs/cluster-api/releases/download/v1.3.10/clusterctl-{OS}-{ARCH}",
			InitWithKubernetesVersion:       e2eConfig.GetVariable("KUBERNETES_VERSION"),
			InitWithCoreProvider:            "cluster-api:v1.3.10",
			InitWithBootstrapProviders:      []string{"kubeadm:v1.3.10"},
			InitWithControlPlaneProviders:   []string{"kubeadm:v1.3.10"},
			InitWithInfrastructureProviders: []string{"nutanix:v1.2.4"},
			PreWaitForCluster:               preWaitForCluster,
			PostUpgrade:                     postUpgradeFunc,
		}
	})
})

func createPreWaitForClusterFunc(testInputFunc func() capi_e2e.ClusterctlUpgradeSpecInput) func(framework.ClusterProxy, string, string) {
	return func(managementClusterProxy framework.ClusterProxy, mgmtClusterNamespace, mgmtClusterName string) {
		testInput := testInputFunc()
		Expect(testInput.E2EConfig).NotTo(BeNil(), "Invalid argument. testInput.E2EConfig can't be nil when calling createPreWaitForClusterFunc")
		Expect(testInput.ArtifactFolder).NotTo(BeEmpty(), "Invalid argument. testInput.ArtifactFolder can't be empty when calling createPreWaitForClusterFunc")
		Expect(testInput.E2EConfig.Variables).NotTo(BeNil(), "Invalid argument. testInput.E2EConfig.Variables can't be nil when calling createPreWaitForClusterFunc")

		By("Get latest version of CAPX provider")

		latestVersionString := "v1.2.4"
		latestVersion, err := semver.ParseTolerant(latestVersionString)
		Expect(err).NotTo(HaveOccurred())

		nutanixProviderRepository := filepath.Join(testInput.ArtifactFolder, "repository", "infrastructure-nutanix")

		// Find the latest version of the CAPX provider defined for test
		_ = filepath.WalkDir(nutanixProviderRepository, func(path string, d os.DirEntry, err error) error {
			if d.IsDir() {
				version, err := semver.ParseTolerant(d.Name())
				if err == nil {
					if latestVersion.Compare(version) < 0 {
						latestVersion = version
						latestVersionString = d.Name()
					}
				}
			}
			return nil
		})

		log.Infof("Latest version of CAPX provider found: %s", latestVersionString)

		latestVersionComponentsYamlFile := filepath.Join(nutanixProviderRepository, latestVersionString, "components.yaml")

		Byf("Replacing image in %s", latestVersionComponentsYamlFile)

		// load the components.yaml file
		componentsYaml, err := os.ReadFile(latestVersionComponentsYamlFile)
		Expect(err).NotTo(HaveOccurred())

		gitCommitHash := os.Getenv("GIT_COMMIT")
		localImageRegistry := os.Getenv("LOCAL_IMAGE_REGISTRY")
		currentCommitImage := fmt.Sprintf("image: %s/controller:e2e-%s", localImageRegistry, gitCommitHash)

		// replace the image
		componentsYaml = bytes.ReplaceAll(componentsYaml,
			[]byte("image: ghcr.io/nutanix-cloud-native/cluster-api-provider-nutanix/controller:e2e"),
			[]byte(currentCommitImage),
		)

		// write the file back
		err = os.WriteFile(latestVersionComponentsYamlFile, componentsYaml, 0o644)
		Expect(err).NotTo(HaveOccurred())

		Byf("Successfully replaced image in components.yaml with the image from the current commit: %s", currentCommitImage)
	}
}

func createPostUpgradeFunc(testInputFunc func() capi_e2e.ClusterctlUpgradeSpecInput) func(framework.ClusterProxy) {
	return func(managementClusterProxy framework.ClusterProxy) {
		testInput := testInputFunc()
		Expect(testInput.E2EConfig).NotTo(BeNil(), "Invalid argument. testInput.E2EConfig can't be nil when calling createPostUpgradeFunc")
		Expect(testInput.ArtifactFolder).NotTo(BeEmpty(), "Invalid argument. testInput.ArtifactFolder can't be empty when calling createPostUpgradeFunc")
		Expect(testInput.E2EConfig.Variables).NotTo(BeNil(), "Invalid argument. testInput.E2EConfig.Variables can't be nil when calling createPostUpgradeFunc")

		By("Installing Nutanix CCM")

		yamlProc := yaml.NewSimpleProcessor()

		latestVersionString := "v1.2.4"
		latestVersion, err := semver.ParseTolerant(latestVersionString)
		Expect(err).NotTo(HaveOccurred())

		nutanixProviderRepository := filepath.Join(testInput.ArtifactFolder, "repository", "infrastructure-nutanix")

		// Find the latest version of the CAPX provider defined for test
		_ = filepath.WalkDir(nutanixProviderRepository, func(path string, d os.DirEntry, err error) error {
			if d.IsDir() {
				version, err := semver.ParseTolerant(d.Name())
				if err == nil {
					if latestVersion.Compare(version) < 0 {
						latestVersion = version
						latestVersionString = d.Name()
					}
				}
			}
			return nil
		})

		// Load the Nutanix CCM manifest
		manifestPath := filepath.Join(testInput.ArtifactFolder, "repository", "infrastructure-nutanix", latestVersionString, "ccm-update.yaml")
		log.Debugf("Loading Nutanix CCM manifest from %s", manifestPath)

		template, err := os.ReadFile(manifestPath)
		Expect(err).NotTo(HaveOccurred())

		// Process the Nutanix CCM manifest
		log.Debugf("Processing Nutanix CCM manifest")
		processedTemplate, err := yamlProc.Process(template, func(varName string) (string, error) {
			if !testInput.E2EConfig.HasVariable(varName) {
				log.Debugf("Nutanix CCM manifest variable %s not found", varName)
				return "", nil
			}

			log.Debugf("Nutanix CCM manifest variable %s found", varName)
			return testInput.E2EConfig.GetVariable(varName), nil
		})
		Expect(err).NotTo(HaveOccurred())

		// Apply the Nutanix CCM manifest
		log.Debugf("Applying Nutanix CCM manifest")
		err = managementClusterProxy.Apply(context.Background(), processedTemplate)
		Expect(err).NotTo(HaveOccurred())

		// Update Clusters with Nutanix CCM label
		log.Debugf("Updating Clusters with Nutanix CCM label")
		// List all clusters
		clusterList := &clusterv1.ClusterList{}
		err = managementClusterProxy.GetClient().List(context.Background(), clusterList)
		Expect(err).NotTo(HaveOccurred())

		clusterNames := []string{}

		// Update all clusters
		for _, cluster := range clusterList.Items {
			cluster.Labels["ccm"] = "nutanix"
			err = managementClusterProxy.GetClient().Update(context.Background(), &cluster)
			Expect(err).NotTo(HaveOccurred())
			clusterNames = append(clusterNames, cluster.Name)
			log.Debugf("Updated cluster %s with Nutanix CCM label", cluster.Name)
		}

		// Wait for Nutanix CCM to be ready
		log.Debugf("Waiting for Nutanix CCM to be ready")
		timeout := 5 * time.Minute
		interval := 10 * time.Second
		for _, clusterName := range clusterNames {
			Eventually(func() error {
				clusterProxy := managementClusterProxy.GetWorkloadCluster(context.Background(), "clusterctl-upgrade", clusterName)
				u := &unstructured.Unstructured{}
				u.SetGroupVersionKind(schema.GroupVersionKind{
					Group:   "apps",
					Kind:    "Deployment",
					Version: "v1",
				})
				err := clusterProxy.GetClient().Get(context.Background(), client.ObjectKey{
					Namespace: "kube-system",
					Name:      "nutanix-cloud-controller-manager",
				}, u)
				return err
			}, timeout, interval).ShouldNot(HaveOccurred())
		}

		By("Update KubeadmConfigTemplate with kubeletExtraArgs cloud-provider: external")
		// List all KubeadmConfigTemplates
		kubeadmConfigTemplateList := &bootstrapv1.KubeadmConfigTemplateList{}
		err = managementClusterProxy.GetClient().List(context.Background(), kubeadmConfigTemplateList)
		Expect(err).NotTo(HaveOccurred())

		// Update all KubeadmConfigTemplates
		for _, kubeadmConfigTemplate := range kubeadmConfigTemplateList.Items {
			if kubeadmConfigTemplate.Spec.Template.Spec.JoinConfiguration == nil {
				kubeadmConfigTemplate.Spec.Template.Spec.JoinConfiguration = &bootstrapv1.JoinConfiguration{
					NodeRegistration: bootstrapv1.NodeRegistrationOptions{},
				}
			}
			if kubeadmConfigTemplate.Spec.Template.Spec.JoinConfiguration.NodeRegistration.KubeletExtraArgs == nil {
				kubeadmConfigTemplate.Spec.Template.Spec.JoinConfiguration.NodeRegistration.KubeletExtraArgs = map[string]string{}
			}

			kubeadmConfigTemplate.Spec.Template.Spec.JoinConfiguration.NodeRegistration.KubeletExtraArgs["cloud-provider"] = "external"
			err = managementClusterProxy.GetClient().Update(context.Background(), &kubeadmConfigTemplate)
			Expect(err).NotTo(HaveOccurred())
			log.Debugf("Updated KubeadmConfigTemplate %s/%s with kubeletExtraArgs cloud-provider: external", kubeadmConfigTemplate.Namespace, kubeadmConfigTemplate.Name)
		}

		// TODO: KubeadmControlPlane extraArgs and kubeletExtraArgs changes test (maybe in a separate test)
	}
}
