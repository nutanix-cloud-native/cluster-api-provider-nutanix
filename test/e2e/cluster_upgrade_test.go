//go:build e2e

/*
Copyright 2022 Nutanix, Inc
Copyright 2021 The Kubernetes Authors.

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
	"k8s.io/utils/ptr"
	capi_e2e "sigs.k8s.io/cluster-api/test/e2e"
)

var _ = Describe("When upgrading a workload cluster and testing K8S conformance", Label("cluster-upgrade-conformance", "slow", "network"), func() {
	capi_e2e.ClusterUpgradeConformanceSpec(ctx, func() capi_e2e.ClusterUpgradeConformanceSpecInput {
		return capi_e2e.ClusterUpgradeConformanceSpecInput{
			E2EConfig:             e2eConfig,
			ClusterctlConfigPath:  clusterctlConfigPath,
			BootstrapClusterProxy: bootstrapClusterProxy,
			ArtifactFolder:        artifactFolder,
			SkipCleanup:           skipCleanup,
		}
	})
})

// var _ = Describe("When upgrading a workload cluster using ClusterClass", func() {
// 	ClusterUpgradeConformanceSpec(ctx, func() ClusterUpgradeConformanceSpecInput {
// 		return ClusterUpgradeConformanceSpecInput{
// 			E2EConfig:             e2eConfig,
// 			ClusterctlConfigPath:  clusterctlConfigPath,
// 			BootstrapClusterProxy: bootstrapClusterProxy,
// 			ArtifactFolder:        artifactFolder,
// 			SkipCleanup:           skipCleanup,
// 			Flavor:                pointer.String("topology"),
// 			// This test is run in CI in parallel with other tests. To keep the test duration reasonable
// 			// the conformance tests are skipped.
// 			SkipConformanceTests: true,
// 		}
// 	})
// })

var _ = Describe("When upgrading a workload cluster with a single control plane machine", Label("cluster-upgrade-conformance", "slow", "network"), func() {
	capi_e2e.ClusterUpgradeConformanceSpec(ctx, func() capi_e2e.ClusterUpgradeConformanceSpecInput {
		return capi_e2e.ClusterUpgradeConformanceSpecInput{
			E2EConfig:             e2eConfig,
			ClusterctlConfigPath:  clusterctlConfigPath,
			BootstrapClusterProxy: bootstrapClusterProxy,
			ArtifactFolder:        artifactFolder,
			SkipCleanup:           skipCleanup,
			// This test is run in CI in parallel with other tests. To keep the test duration reasonable
			// the conformance tests are skipped.
			SkipConformanceTests:     true,
			ControlPlaneMachineCount: ptr.To(int64(1)),
			WorkerMachineCount:       ptr.To(int64(1)),
		}
	})
})

var _ = Describe("When upgrading a workload cluster with a HA control plane", Label("cluster-upgrade-conformance", "slow", "network"), func() {
	capi_e2e.ClusterUpgradeConformanceSpec(ctx, func() capi_e2e.ClusterUpgradeConformanceSpecInput {
		return capi_e2e.ClusterUpgradeConformanceSpecInput{
			E2EConfig:             e2eConfig,
			ClusterctlConfigPath:  clusterctlConfigPath,
			BootstrapClusterProxy: bootstrapClusterProxy,
			ArtifactFolder:        artifactFolder,
			SkipCleanup:           skipCleanup,
			// This test is run in CI in parallel with other tests. To keep the test duration reasonable
			// the conformance tests are skipped.
			SkipConformanceTests:     true,
			ControlPlaneMachineCount: ptr.To(int64(3)),
			WorkerMachineCount:       ptr.To(int64(1)),
		}
	})
})

var _ = Describe("When upgrading a workload cluster with a HA control plane using scale-in rollout", Label("cluster-upgrade-conformance", "slow", "network"), func() {
	capi_e2e.ClusterUpgradeConformanceSpec(ctx, func() capi_e2e.ClusterUpgradeConformanceSpecInput {
		return capi_e2e.ClusterUpgradeConformanceSpecInput{
			E2EConfig:             e2eConfig,
			ClusterctlConfigPath:  clusterctlConfigPath,
			BootstrapClusterProxy: bootstrapClusterProxy,
			ArtifactFolder:        artifactFolder,
			SkipCleanup:           skipCleanup,
			// This test is run in CI in parallel with other tests. To keep the test duration reasonable
			// the conformance tests are skipped.
			SkipConformanceTests:     true,
			ControlPlaneMachineCount: ptr.To(int64(3)),
			WorkerMachineCount:       ptr.To(int64(1)),
			Flavor:                   ptr.To("kcp-scale-in"),
		}
	})
})
