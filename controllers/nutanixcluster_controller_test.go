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

package controllers

import (
	"context"
	"testing"

	credentialTypes "github.com/nutanix-cloud-native/prism-go-client/environment/credentials"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/cluster-api/util"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	nctx "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/pkg/context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gstruct"
)

func TestNutanixClusterReconciler(t *testing.T) {
	g := NewWithT(t)

	_ = Describe("NutanixClusterReconciler", func() {
		const (
			fd1Name = "fd-1"
			fd2Name = "fd-2"
		)

		var (
			ntnxCluster *infrav1.NutanixCluster
			ctx         context.Context
			fd1         infrav1.NutanixFailureDomain
			reconciler  *NutanixClusterReconciler
			r           string
		)

		BeforeEach(func() {
			ctx = context.Background()
			r := util.RandomString(10)
			ntnxCluster = &infrav1.NutanixCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "default",
				},
				Spec: infrav1.NutanixClusterSpec{
					PrismCentral: &credentialTypes.NutanixPrismEndpoint{
						// Adding port info to override default value (0)
						Port: 9440,
					},
				},
			}
			fd1 = infrav1.NutanixFailureDomain{
				Name: fd1Name,
				Cluster: infrav1.NutanixResourceIdentifier{
					Type: infrav1.NutanixIdentifierName,
					Name: &r,
				},
				Subnets: []infrav1.NutanixResourceIdentifier{
					{
						Type: infrav1.NutanixIdentifierName,
						Name: &r,
					},
				},
			}
			reconciler = &NutanixClusterReconciler{
				Client: k8sClient,
				Scheme: runtime.NewScheme(),
			}
		})

		AfterEach(func() {
			err := k8sClient.Delete(ctx, ntnxCluster)
			Expect(err).NotTo(HaveOccurred())
		})

		Context("Reconcile an NutanixCluster", func() {
			It("should not error and not requeue the request", func() {
				// Create the NutanixCluster object and expect the Reconcile to be created
				g.Expect(k8sClient.Create(ctx, ntnxCluster)).To(Succeed())

				result, err := reconciler.Reconcile(ctx, ctrl.Request{
					NamespacedName: client.ObjectKey{
						Namespace: ntnxCluster.Namespace,
						Name:      ntnxCluster.Name,
					},
				})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(result.RequeueAfter).To(BeZero())
				g.Expect(result.Requeue).To(BeFalse())
			})
		})

		Context("ReconcileNormal for a NutanixCluster", func() {
			It("should not requeue if failure message is set on nutanixCluster", func() {
				g.Expect(k8sClient.Create(ctx, ntnxCluster)).To(Succeed())
				ntnxCluster.Status.FailureMessage = &r
				result, err := reconciler.reconcileNormal(&nctx.ClusterContext{
					Context:        ctx,
					NutanixCluster: ntnxCluster,
				})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(result.RequeueAfter).To(BeZero())
				g.Expect(result.Requeue).To(BeFalse())
			})
			It("should not error and not requeue if no failure domains are configured and cluster is Ready", func() {
				g.Expect(k8sClient.Create(ctx, ntnxCluster)).To(Succeed())
				ntnxCluster.Status.Ready = true
				result, err := reconciler.reconcileNormal(&nctx.ClusterContext{
					Context:        ctx,
					NutanixCluster: ntnxCluster,
				})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(result.RequeueAfter).To(BeZero())
				g.Expect(result.Requeue).To(BeFalse())
			})
			It("should not error and not requeue if failure domains are configured and cluster is Ready", func() {
				ntnxCluster.Spec.FailureDomains = []infrav1.NutanixFailureDomain{
					fd1,
				}
				g.Expect(k8sClient.Create(ctx, ntnxCluster)).To(Succeed())
				ntnxCluster.Status.Ready = true
				result, err := reconciler.reconcileNormal(&nctx.ClusterContext{
					Context:        ctx,
					NutanixCluster: ntnxCluster,
				})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(result.RequeueAfter).To(BeZero())
				g.Expect(result.Requeue).To(BeFalse())
			})
		})

		Context("Reconcile failure domains", func() {
			It("sets the failure domains in the nutanixcluster status and failure domain reconciled condition", func() {
				ntnxCluster.Spec.FailureDomains = []infrav1.NutanixFailureDomain{
					fd1,
				}

				// Create the NutanixCluster object and expect the Reconcile to be created
				g.Expect(k8sClient.Create(ctx, ntnxCluster)).To(Succeed())
				// Retrieve the applied nutanix cluster objects
				appliedNtnxCluster := &infrav1.NutanixCluster{}
				k8sClient.Get(ctx, client.ObjectKey{
					Namespace: ntnxCluster.Namespace,
					Name:      ntnxCluster.Name,
				}, appliedNtnxCluster)

				err := reconciler.reconcileFailureDomains(&nctx.ClusterContext{
					Context:        ctx,
					NutanixCluster: appliedNtnxCluster,
				})
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(appliedNtnxCluster.Status.Conditions).To(ContainElement(
					gstruct.MatchFields(
						gstruct.IgnoreExtras,
						gstruct.Fields{
							"Type":   Equal(infrav1.FailureDomainsReconciled),
							"Status": Equal(corev1.ConditionTrue),
						},
					),
				))
				g.Expect(appliedNtnxCluster.Status.FailureDomains).To(HaveKey(fd1Name))
				g.Expect(appliedNtnxCluster.Status.FailureDomains[fd1Name]).To(gstruct.MatchFields(
					gstruct.IgnoreExtras,
					gstruct.Fields{
						"ControlPlane": Equal(fd1.ControlPlane),
					},
				))
			})

			It("sets the NoFailureDomainsReconciled condition when no failure domains are set", func() {
				// Create the NutanixCluster object and expect the Reconcile to be created
				g.Expect(k8sClient.Create(ctx, ntnxCluster)).To(Succeed())
				// Retrieve the applied nutanix cluster objects
				appliedNtnxCluster := &infrav1.NutanixCluster{}
				k8sClient.Get(ctx, client.ObjectKey{
					Namespace: ntnxCluster.Namespace,
					Name:      ntnxCluster.Name,
				}, appliedNtnxCluster)

				err := reconciler.reconcileFailureDomains(&nctx.ClusterContext{
					Context:        ctx,
					NutanixCluster: appliedNtnxCluster,
				})
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(appliedNtnxCluster.Status.Conditions).To(ContainElement(
					gstruct.MatchFields(
						gstruct.IgnoreExtras,
						gstruct.Fields{
							"Type":   Equal(infrav1.NoFailureDomainsReconciled),
							"Status": Equal(corev1.ConditionTrue),
						},
					),
				))
				g.Expect(appliedNtnxCluster.Status.FailureDomains).To(BeEmpty())
			})
		})
	})
}
