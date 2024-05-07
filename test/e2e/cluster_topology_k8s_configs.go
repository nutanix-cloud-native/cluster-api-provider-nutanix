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

// NOTE:
// 1. To support a new k8s version,
// - add a new function NewE2eConfigK8SVersion<k8sversion> similar to
// NewE2eConfigK8SVersion127 with respective var names
// - add a new row in SupportedK8STargetConfigs and
// 2. To remove a supported version,
// - delete a row from SupportedK8STargetConfigs array.
// - delete respective version's NewE2eConfigK8SVersion<k8sversion> function
// All the tests iterating over this array will will use newer configs

// E2eConfigK8SVersion holds K8s version specific e2e config variables
type E2eConfigK8SVersion struct {
	E2eConfigK8sVersionEnvVar      string
	E2eConfigK8sVersionImageEnvVar string
}

// returns E2eConfigK8SVersion with Kube127 specific e2e config vars
func NewE2eConfigK8SVersion127() *E2eConfigK8SVersion {
	return &E2eConfigK8SVersion{
		E2eConfigK8sVersionEnvVar:      "KUBERNETES_VERSION_v1_27",
		E2eConfigK8sVersionImageEnvVar: "NUTANIX_MACHINE_TEMPLATE_IMAGE_NAME_v1_27",
	}
}

// returns E2eConfigK8SVersion with Kube128 specific e2e config vars
func NewE2eConfigK8SVersion128() *E2eConfigK8SVersion {
	return &E2eConfigK8SVersion{
		E2eConfigK8sVersionEnvVar:      "KUBERNETES_VERSION_v1_28",
		E2eConfigK8sVersionImageEnvVar: "NUTANIX_MACHINE_TEMPLATE_IMAGE_NAME_v1_28",
	}
}

// returns E2eConfigK8SVersion with Kube129 specific e2e config vars
func NewE2eConfigK8SVersion129() *E2eConfigK8SVersion {
	return &E2eConfigK8SVersion{
		E2eConfigK8sVersionEnvVar:      "KUBERNETES_VERSION_v1_29",
		E2eConfigK8sVersionImageEnvVar: "NUTANIX_MACHINE_TEMPLATE_IMAGE_NAME_v1_29",
	}
}

// returns E2eConfigK8SVersion with Kube130 specific e2e config vars
func NewE2eConfigK8SVersion130() *E2eConfigK8SVersion {
	return &E2eConfigK8SVersion{
		E2eConfigK8sVersionEnvVar:      "KUBERNETES_VERSION_v1_30",
		E2eConfigK8sVersionImageEnvVar: "NUTANIX_MACHINE_TEMPLATE_IMAGE_NAME_v1_30",
	}
}

// TestConfig holds required parameters for each supported k8s target config
type TestConfig struct {
	targetKube   *E2eConfigK8SVersion
	targetLabels []string
}

// SupportedK8STargetConfigs holds all the supported k8s versions and respective labels for
// each version speficic test
var SupportedK8STargetConfigs = []TestConfig{
	{targetKube: NewE2eConfigK8SVersion127(), targetLabels: []string{"Kube127"}},
	{targetKube: NewE2eConfigK8SVersion128(), targetLabels: []string{"Kube128"}},
	{targetKube: NewE2eConfigK8SVersion129(), targetLabels: []string{"Kube129"}},
	{targetKube: NewE2eConfigK8SVersion130(), targetLabels: []string{"Kube130"}},
}
