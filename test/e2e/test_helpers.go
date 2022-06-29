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
	"time"

	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gstruct"
	corev1 "k8s.io/api/core/v1"
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

	defaultTimeout  = time.Second * 60
	defaultInterval = time.Second * 10

	nutanixUserKey     = "NUTANIX_USER"
	nutanixPasswordKey = "NUTANIX_PASSWORD"
)

type deployClusterParams struct {
	clusterName           string
	namespace             *corev1.Namespace
	flavor                string
	clusterctlConfigPath  string
	artifactFolder        string
	bootstrapClusterProxy framework.ClusterProxy
	e2eConfig             clusterctl.E2EConfig
}

func deployClusterAndWait(params deployClusterParams, clusterResources *clusterctl.ApplyClusterTemplateAndWaitResult) {
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

func deployCluster(params deployClusterParams, clusterResources *clusterctl.ApplyClusterTemplateAndWaitResult) {
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

	createClusterFromConfig(ctx, clusterctl.ApplyClusterTemplateAndWaitInput{
		ClusterProxy:                 params.bootstrapClusterProxy,
		ConfigCluster:                cc,
		WaitForClusterIntervals:      params.e2eConfig.GetIntervals("", "wait-cluster"),
		WaitForControlPlaneIntervals: params.e2eConfig.GetIntervals("", "wait-control-plane"),
		WaitForMachineDeployments:    params.e2eConfig.GetIntervals("", "wait-worker-nodes"),
	}, clusterResources)
}

func generateTestClusterName(specName string) string {
	return fmt.Sprintf("%s-%s", specName, util.RandomString(6))
}

func createClusterFromConfig(ctx context.Context, input clusterctl.ApplyClusterTemplateAndWaitInput, result *clusterctl.ApplyClusterTemplateAndWaitResult) {
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

type getNutanixClusterByNameInput struct {
	Getter    framework.Getter
	Name      string
	Namespace string
}

func getNutanixClusterByName(ctx context.Context, input getNutanixClusterByNameInput) *infrav1.NutanixCluster {
	cluster := &infrav1.NutanixCluster{}
	key := client.ObjectKey{
		Namespace: input.Namespace,
		Name:      input.Name,
	}
	Expect(input.Getter.Get(ctx, key, cluster)).To(Succeed(), "Failed to get Nutanix Cluster object %s/%s", input.Namespace, input.Name)
	return cluster
}

type verifyConditionOnNutanixClusterParams struct {
	bootstrapClusterProxy framework.ClusterProxy
	clusterName           string
	namespace             *corev1.Namespace
	expectedCondition     clusterv1.Condition
}

func verifyConditionOnNutanixCluster(params verifyConditionOnNutanixClusterParams) {
	Eventually(
		func() []clusterv1.Condition {
			cluster := getNutanixClusterByName(ctx, getNutanixClusterByNameInput{
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

type createSecretParams struct {
	namespace   *corev1.Namespace
	clusterName string
	username    string
	password    string
}

func createSecret(params createSecretParams) {
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

type nutanixCredentials struct {
	nutanixUsername string
	nutanixPassword string
}

func getNutanixCredentialsFromEnvironment() nutanixCredentials {
	nutanixUsername := os.Getenv(nutanixUserKey)
	Expect(nutanixUsername).ToNot(BeNil())
	nutanixPassword := os.Getenv(nutanixPasswordKey)
	Expect(nutanixPassword).ToNot(BeNil())
	return nutanixCredentials{
		nutanixUsername: nutanixUsername,
		nutanixPassword: nutanixPassword,
	}
}
