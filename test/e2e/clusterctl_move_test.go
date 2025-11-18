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
	. "github.com/onsi/ginkgo/v2"
	capie2e "sigs.k8s.io/cluster-api/test/e2e"
)

var _ = Describe("[clusterctl-move] Create a Self-Hosted CAPX Cluster", Label("capx-feature-test", "clusterctl-move"), func() {
	// Run the CAPI SelfHostedSpec test suite
	// This test suite will create a self-hosted CAPX cluster
	capie2e.SelfHostedSpec(ctx, func() capie2e.SelfHostedSpecInput {
		return capie2e.SelfHostedSpecInput{
			E2EConfig:             e2eConfig,
			ClusterctlConfigPath:  clusterctlConfigPath,
			BootstrapClusterProxy: bootstrapClusterProxy,
			ArtifactFolder:        artifactFolder,
			SkipCleanup:           skipCleanup,
			SkipUpgrade:           true,
		}
	})
})

var _ = Describe("[clusterctl-move] Create a Self-Hosted CAPX Cluster", Label("clusterclass", "clusterctl-move"), func() {
	// Run the CAPI SelfHostedSpec test suite
	// This test suite will create a self-hosted CAPX cluster
	capie2e.SelfHostedSpec(ctx, func() capie2e.SelfHostedSpecInput {
		return capie2e.SelfHostedSpecInput{
			E2EConfig:             e2eConfig,
			Flavor:                flavorTopology,
			ClusterctlConfigPath:  clusterctlConfigPath,
			BootstrapClusterProxy: bootstrapClusterProxy,
			ArtifactFolder:        artifactFolder,
			SkipCleanup:           skipCleanup,
			SkipUpgrade:           true,
		}
	})
})
