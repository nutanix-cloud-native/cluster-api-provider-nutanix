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
	"fmt"
	"io"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gstruct"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	capiv1beta1 "sigs.k8s.io/cluster-api/api/core/v1beta1" //nolint:staticcheck // suppress complaining on Deprecated package
	"sigs.k8s.io/cluster-api/test/framework"
	"sigs.k8s.io/cluster-api/test/framework/clusterctl"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
)

var _ = Describe("Nutanix projects", Label("projects"), func() {
	const (
		specName                = "cluster-projects"
		nonExistingProjectName  = "nonExistingProjectNameCAPX"
		inaccessibleProjectName = "pepsi"
	)

	var (
		namespace          *corev1.Namespace
		clusterName        string
		clusterResources   *clusterctl.ApplyClusterTemplateAndWaitResult
		cancelWatches      context.CancelFunc
		nutanixProjectName string
		testHelper         testHelperInterface
	)

	BeforeEach(func() {
		testHelper = newTestHelper(e2eConfig)
		nutanixProjectName = testHelper.getVariableFromE2eConfig(nutanixProjectNameEnv)
		clusterName = testHelper.generateTestClusterName(specName)
		clusterResources = new(clusterctl.ApplyClusterTemplateAndWaitResult)
		Expect(bootstrapClusterProxy).NotTo(BeNil(), "BootstrapClusterProxy can't be nil")
		namespace, cancelWatches = setupSpecNamespace(ctx, specName, bootstrapClusterProxy, artifactFolder)
	})

	AfterEach(func() {
		dumpSpecResourcesAndCleanup(ctx, specName, bootstrapClusterProxy, artifactFolder, namespace, cancelWatches, clusterResources.Cluster, e2eConfig.GetIntervals, skipCleanup)
	})

	It("Create a cluster linked to non-existing project (should fail)", Label("broad-scope-user"), func() {
		const flavor = "no-nmt"

		Expect(namespace).NotTo(BeNil())

		By("Creating invalid Project Nutanix Machine Template", func() {
			invalidProjectNMT := testHelper.createDefaultNMT(clusterName, namespace.Name)
			invalidProjectNMT.Spec.Template.Spec.Project = &infrav1.NutanixResourceIdentifier{
				Type: "name",
				Name: ptr.To(nonExistingProjectName),
			}
			testHelper.createCapiObject(ctx, createCapiObjectParams{
				creator:    bootstrapClusterProxy.GetClient(),
				capiObject: invalidProjectNMT,
			})
		})

		By("Creating a workload cluster", func() {
			testHelper.deployCluster(
				deployClusterParams{
					clusterName:           clusterName,
					namespace:             namespace,
					flavor:                flavor,
					clusterctlConfigPath:  clusterctlConfigPath,
					artifactFolder:        artifactFolder,
					bootstrapClusterProxy: bootstrapClusterProxy,
				},
				clusterResources,
			)
		})

		By("Checking project assigned condition is false with the expected failure message", func() {
			testHelper.verifyConditionOnNutanixMachines(verifyConditionParams{
				clusterName:           clusterName,
				namespace:             namespace,
				bootstrapClusterProxy: bootstrapClusterProxy,
				expectedCondition: capiv1beta1.Condition{
					Type:     infrav1.ProjectAssignedCondition,
					Reason:   infrav1.ProjectAssignationFailed,
					Severity: capiv1beta1.ConditionSeverityError,
					Status:   corev1.ConditionFalse,
				},
				expectedMessageSubstring: "not found",
			})
		})

		By("PASSED!")
	})

	It("Create a cluster linked to non-existing project as a project-scoped user (should fail)", Label("project-scope-user"), func() {
		flavor = "no-nmt-project-scoped-user"

		Expect(namespace).NotTo(BeNil())

		By("Creating invalid Project Nutanix Machine Template", func() {
			invalidProjectNMT := testHelper.createDefaultNMT(clusterName, namespace.Name)
			invalidProjectNMT.Spec.Template.Spec.Project = &infrav1.NutanixResourceIdentifier{
				Type: "name",
				Name: ptr.To(nonExistingProjectName),
			}
			testHelper.createCapiObject(ctx, createCapiObjectParams{
				creator:    bootstrapClusterProxy.GetClient(),
				capiObject: invalidProjectNMT,
			})
		})

		By("Creating a workload cluster using project-scoped credentials", func() {
			testHelper.deployCluster(
				deployClusterParams{
					clusterName:           clusterName,
					namespace:             namespace,
					flavor:                flavor,
					clusterctlConfigPath:  clusterctlConfigPath,
					artifactFolder:        artifactFolder,
					bootstrapClusterProxy: bootstrapClusterProxy,
				},
				clusterResources,
			)
		})

		By("Checking project assigned condition is false with the expected failure message", func() {
			testHelper.verifyConditionOnNutanixMachines(verifyConditionParams{
				clusterName:           clusterName,
				namespace:             namespace,
				bootstrapClusterProxy: bootstrapClusterProxy,
				expectedCondition: capiv1beta1.Condition{
					Type:     infrav1.ProjectAssignedCondition,
					Reason:   infrav1.ProjectAssignationFailed,
					Severity: capiv1beta1.ConditionSeverityError,
					Status:   corev1.ConditionFalse,
				},
				expectedMessageSubstring: "not found",
			})
		})

		By("PASSED!")
	})

	// Label "inaccessible-project" lets CI run only this case via LABEL_FILTERS.
	It("Create a cluster linked to a project it doesn't have access to as a project-scoped user (should fail)", Label("project-scope-user", "inaccessible-project"), func() {
		flavor = "no-nmt-project-scoped-user"

		Expect(namespace).NotTo(BeNil())

		By("Creating out-of-scope Project Nutanix Machine Template", func() {
			outOfScopeProjectNMT := testHelper.createDefaultNMT(clusterName, namespace.Name)
			outOfScopeProjectNMT.Spec.Template.Spec.Project = &infrav1.NutanixResourceIdentifier{
				Type: "name",
				Name: ptr.To(inaccessibleProjectName),
			}
			testHelper.createCapiObject(ctx, createCapiObjectParams{
				creator:    bootstrapClusterProxy.GetClient(),
				capiObject: outOfScopeProjectNMT,
			})
			By(fmt.Sprintf("NMT project set to inaccessible name %q", inaccessibleProjectName))
		})

		By("Creating a workload cluster using project-scoped credentials", func() {
			testHelper.deployCluster(
				deployClusterParams{
					clusterName:           clusterName,
					namespace:             namespace,
					flavor:                flavor,
					clusterctlConfigPath:  clusterctlConfigPath,
					artifactFolder:        artifactFolder,
					bootstrapClusterProxy: bootstrapClusterProxy,
				},
				clusterResources,
			)
		})

		By("Checking project assigned condition is false with the expected failure message", func() {
			waitStart := time.Now()
			lastDump := time.Time{}

			defer func() {
				machines := testHelper.getNutanixMachinesForCluster(ctx, clusterName, namespace.Name, bootstrapClusterProxy)
				dumpInaccessibleProjectMachineDiagnostics(clusterName, inaccessibleProjectName, time.Since(waitStart), machines.Items)
				dumpCAPXControllerLogsForProjectLookup(ctx, bootstrapClusterProxy, clusterName, inaccessibleProjectName)
			}()

			Eventually(func(g Gomega) {
				machines := testHelper.getNutanixMachinesForCluster(ctx, clusterName, namespace.Name, bootstrapClusterProxy)

				// Periodic dump so CI logs show progress while waiting (hypothesis:
				// CAPX blocks in Projects.GetByName("pepsi") and never sets ProjectAssigned).
				if lastDump.IsZero() || time.Since(lastDump) >= 60*time.Second {
					dumpInaccessibleProjectMachineDiagnostics(clusterName, inaccessibleProjectName, time.Since(waitStart), machines.Items)
					lastDump = time.Now()
				}

				g.Expect(machines.Items).To(ContainElement(
					gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{
						"Status": gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{
							"Conditions": ContainElement(
								gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{
									"Type":     Equal(infrav1.ProjectAssignedCondition),
									"Reason":   Equal(infrav1.ProjectAssignationFailed),
									"Severity": Equal(capiv1beta1.ConditionSeverityError),
									"Status":   Equal(corev1.ConditionFalse),
									"Message":  ContainSubstring("not found"),
								}),
							),
						}),
					}),
				))
			}, defaultTimeout, defaultInterval).Should(Succeed(),
				"expected ProjectAssigned=False/ProjectAssignationFailed with message containing %q for inaccessible project %q",
				"not found", inaccessibleProjectName)
		})

		By("PASSED!")
	})

	It("Create a cluster linked to an existing project as a broad-scope user", Label("broad-scope-user"), func() {
		flavor = "project"

		Expect(namespace).NotTo(BeNil())

		By("Creating a workload cluster")
		testHelper.deployClusterAndWait(
			deployClusterParams{
				clusterName:           clusterName,
				namespace:             namespace,
				flavor:                flavor,
				clusterctlConfigPath:  clusterctlConfigPath,
				artifactFolder:        artifactFolder,
				bootstrapClusterProxy: bootstrapClusterProxy,
			}, clusterResources)

		By("Checking project assigned condition is true", func() {
			testHelper.verifyConditionOnNutanixMachines(verifyConditionParams{
				clusterName:           clusterName,
				namespace:             namespace,
				bootstrapClusterProxy: bootstrapClusterProxy,
				expectedCondition: capiv1beta1.Condition{
					Type:   infrav1.ProjectAssignedCondition,
					Status: corev1.ConditionTrue,
				},
			})
		})

		By("Verifying if project is assigned to the VMs")
		Expect(nutanixProjectName).ToNot(BeEmpty())
		testHelper.verifyProjectNutanixMachines(ctx, verifyProjectNutanixMachinesParams{
			clusterName:           clusterName,
			namespace:             namespace.Name,
			nutanixProjectName:    nutanixProjectName,
			bootstrapClusterProxy: bootstrapClusterProxy,
		})

		By("PASSED!")
	})

	It("Create a cluster linked to an existing project as a project-scoped user", Label("project-scope-user"), func() {
		flavor = "project-scoped-user"

		Expect(namespace).NotTo(BeNil())

		By("Creating a workload cluster using project-scoped credentials")
		testHelper.deployClusterAndWait(
			deployClusterParams{
				clusterName:           clusterName,
				namespace:             namespace,
				flavor:                flavor,
				clusterctlConfigPath:  clusterctlConfigPath,
				artifactFolder:        artifactFolder,
				bootstrapClusterProxy: bootstrapClusterProxy,
			}, clusterResources)

		By("Checking project assigned condition is true", func() {
			testHelper.verifyConditionOnNutanixMachines(verifyConditionParams{
				clusterName:           clusterName,
				namespace:             namespace,
				bootstrapClusterProxy: bootstrapClusterProxy,
				expectedCondition: capiv1beta1.Condition{
					Type:   infrav1.ProjectAssignedCondition,
					Status: corev1.ConditionTrue,
				},
			})
		})

		By("Verifying if project is assigned to the VMs")
		Expect(nutanixProjectName).ToNot(BeEmpty())
		testHelper.verifyProjectNutanixMachines(ctx, verifyProjectNutanixMachinesParams{
			clusterName:           clusterName,
			namespace:             namespace.Name,
			nutanixProjectName:    nutanixProjectName,
			bootstrapClusterProxy: bootstrapClusterProxy,
		})

		By("PASSED!")
	})

	It("Create a cluster linked to an existing project by UUID", Label("uuid", "broad-scope-user"), func() {
		flavor = "no-nmt"
		Expect(namespace).NotTo(BeNil())

		By("Creating Nutanix Machine Template with UUIDs", func() {
			uuidNMT := testHelper.createUUIDProjectNMT(ctx, clusterName, namespace.Name)
			testHelper.createCapiObject(ctx, createCapiObjectParams{
				creator:    bootstrapClusterProxy.GetClient(),
				capiObject: uuidNMT,
			})
		})

		By("Creating a workload cluster linked to a project")
		testHelper.deployClusterAndWait(
			deployClusterParams{
				clusterName:           clusterName,
				namespace:             namespace,
				flavor:                flavor,
				clusterctlConfigPath:  clusterctlConfigPath,
				artifactFolder:        artifactFolder,
				bootstrapClusterProxy: bootstrapClusterProxy,
			}, clusterResources)

		By("Checking project assigned condition is true", func() {
			testHelper.verifyConditionOnNutanixMachines(verifyConditionParams{
				clusterName:           clusterName,
				namespace:             namespace,
				bootstrapClusterProxy: bootstrapClusterProxy,
				expectedCondition: capiv1beta1.Condition{
					Type:   infrav1.ProjectAssignedCondition,
					Status: corev1.ConditionTrue,
				},
			})
		})

		By("Verifying if project is assigned to the VMs")
		Expect(nutanixProjectName).ToNot(BeEmpty())
		testHelper.verifyProjectNutanixMachines(ctx, verifyProjectNutanixMachinesParams{
			clusterName:           clusterName,
			namespace:             namespace.Name,
			nutanixProjectName:    nutanixProjectName,
			bootstrapClusterProxy: bootstrapClusterProxy,
		})

		By("PASSED!")
	})
})

// dumpInaccessibleProjectMachineDiagnostics prints NutanixMachine state that explains
// why ProjectAssigned may be missing (e.g. stuck on BootstrapDataNotReady).
func dumpInaccessibleProjectMachineDiagnostics(clusterName, projectName string, waited time.Duration, machines []infrav1.NutanixMachine) {
	GinkgoWriter.Printf(
		"\n=== inaccessible-project diagnostics (cluster=%s project=%s waited=%s machines=%d) ===\n",
		clusterName, projectName, waited.Round(time.Second), len(machines),
	)
	if len(machines) == 0 {
		GinkgoWriter.Printf("no NutanixMachines found yet\n")
		return
	}
	for i := range machines {
		m := &machines[i]
		project := "<nil>"
		if m.Spec.Project != nil && m.Spec.Project.Name != nil {
			project = *m.Spec.Project.Name
		}
		bootstrapRef := "<nil>"
		if m.Spec.BootstrapRef != nil {
			bootstrapRef = fmt.Sprintf("%s/%s", m.Spec.BootstrapRef.Kind, m.Spec.BootstrapRef.Name)
		}
		GinkgoWriter.Printf(
			"- NutanixMachine %s/%s project=%s bootstrapRef=%s ready=%v failureReason=%v failureMessage=%v\n",
			m.Namespace, m.Name, project, bootstrapRef, m.Status.Ready, m.Status.FailureReason, m.Status.FailureMessage,
		)
		if len(m.Status.Conditions) == 0 {
			GinkgoWriter.Printf("  conditions: <none>\n")
			continue
		}
		for _, c := range m.Status.Conditions {
			GinkgoWriter.Printf(
				"  condition type=%s status=%s severity=%s reason=%s age=%s message=%q\n",
				c.Type, c.Status, c.Severity, c.Reason, time.Since(c.LastTransitionTime.Time).Round(time.Second), c.Message,
			)
		}
	}
}

// dumpCAPXControllerLogsForProjectLookup prints recent CAPX manager log lines related to
// project lookup / the cluster under test so CI can confirm whether GetByName hung.
func dumpCAPXControllerLogsForProjectLookup(ctx context.Context, proxy framework.ClusterProxy, clusterName, projectName string) {
	GinkgoWriter.Printf(
		"\n=== CAPX controller logs (filter cluster=%s project=%s) ===\n",
		clusterName, projectName,
	)
	clientset := proxy.GetClientSet()
	if clientset == nil {
		GinkgoWriter.Printf("bootstrap clientset is nil; skipping CAPX log dump\n")
		return
	}

	pods, err := clientset.CoreV1().Pods("capx-system").List(ctx, metav1.ListOptions{
		LabelSelector: "control-plane=controller-manager",
	})
	if err != nil {
		GinkgoWriter.Printf("failed to list capx-system pods: %v\n", err)
		return
	}
	if len(pods.Items) == 0 {
		GinkgoWriter.Printf("no capx-controller-manager pods found in capx-system\n")
		return
	}

	needles := []string{
		clusterName,
		projectName,
		"GetProjectV4",
		"resolveEffectiveProject",
		"searching for project",
		"Waiting for bootstrap data",
		"BootstrapDataNotReady",
		"ProjectAssign",
	}

	for _, pod := range pods.Items {
		req := clientset.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &corev1.PodLogOptions{
			Container: "manager",
			TailLines: ptr.To[int64](2000),
		})
		stream, err := req.Stream(ctx)
		if err != nil {
			GinkgoWriter.Printf("failed to stream logs for %s/%s: %v\n", pod.Namespace, pod.Name, err)
			continue
		}
		raw, err := io.ReadAll(stream)
		_ = stream.Close()
		if err != nil {
			GinkgoWriter.Printf("failed to read logs for %s/%s: %v\n", pod.Namespace, pod.Name, err)
			continue
		}

		matched := 0
		for _, line := range strings.Split(string(raw), "\n") {
			for _, needle := range needles {
				if strings.Contains(line, needle) {
					GinkgoWriter.Printf("%s\n", line)
					matched++
					break
				}
			}
		}
		GinkgoWriter.Printf("--- end logs for %s/%s (matched=%d lines) ---\n", pod.Namespace, pod.Name, matched)
	}
}
