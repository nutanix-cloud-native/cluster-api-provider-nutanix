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
	utilruntime "k8s.io/apimachinery/pkg/util/uuid"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util"
	capiutil "sigs.k8s.io/cluster-api/util"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

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
			// To be replaced with capiv1.ClusterKind
			clusterKind = "Cluster"
		)

		var (
			ntnxCluster *infrav1.NutanixCluster
			ctx         context.Context
			fd1         infrav1.NutanixFailureDomain
			reconciler  *NutanixClusterReconciler
			ntnxSecret  *corev1.Secret
			r           string
		)

		BeforeEach(func() {
			ctx = context.Background()
			r = util.RandomString(10)
			ntnxSecret = &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      r,
					Namespace: corev1.NamespaceDefault,
				},
				StringData: map[string]string{
					r: r,
				},
			}
			ntnxCluster = &infrav1.NutanixCluster{
				TypeMeta: metav1.TypeMeta{
					Kind:       infrav1.NutanixClusterKind,
					APIVersion: infrav1.GroupVersion.String(),
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: corev1.NamespaceDefault,
					UID:       utilruntime.NewUUID(),
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
			// Delete ntnxCluster if exists.
			_ = k8sClient.Delete(ctx, ntnxCluster)
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
			It("should not requeue if failure message is set on NutanixCluster", func() {
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
				_ = k8sClient.Get(ctx, client.ObjectKey{
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
				_ = k8sClient.Get(ctx, client.ObjectKey{
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
		Context("Reconcile credentialRef for a NutanixCluster", func() {
			It("should return error if already owned by another NutanixCluster", func() {
				// Create an additional NutanixCluster object
				additionalNtnxCluster := &infrav1.NutanixCluster{
					ObjectMeta: metav1.ObjectMeta{
						Name:      r,
						Namespace: corev1.NamespaceDefault,
					},
					Spec: infrav1.NutanixClusterSpec{
						PrismCentral: &credentialTypes.NutanixPrismEndpoint{
							// Adding port info to override default value (0)
							Port: 9440,
						},
					},
				}
				g.Expect(k8sClient.Create(ctx, additionalNtnxCluster)).To(Succeed())

				// Add credential ref to the ntnxCluster resource
				ntnxCluster.Spec.PrismCentral.CredentialRef = &credentialTypes.NutanixCredentialReference{
					Kind:      credentialTypes.SecretKind,
					Name:      ntnxSecret.Name,
					Namespace: ntnxSecret.Namespace,
				}

				// Add an ownerReference for the additional NutanixCluster object
				ntnxSecret.OwnerReferences = []metav1.OwnerReference{
					{
						APIVersion: infrav1.GroupVersion.String(),
						Kind:       infrav1.NutanixClusterKind,
						UID:        additionalNtnxCluster.UID,
						Name:       additionalNtnxCluster.Name,
					},
				}
				g.Expect(k8sClient.Create(ctx, ntnxSecret)).To(Succeed())

				// Reconcile credentialRef
				err := reconciler.reconcileCredentialRef(ctx, ntnxCluster)
				g.Expect(err).To(HaveOccurred())
			})
			It("should add credentialRef and finalizer if not owned by other cluster", func() {
				// Add credential ref to the ntnxCluster resource
				ntnxCluster.Spec.PrismCentral.CredentialRef = &credentialTypes.NutanixCredentialReference{
					Kind:      credentialTypes.SecretKind,
					Name:      ntnxSecret.Name,
					Namespace: ntnxSecret.Namespace,
				}

				// Create secret
				g.Expect(k8sClient.Create(ctx, ntnxSecret)).To(Succeed())

				// Reconcile credentialRef
				err := reconciler.reconcileCredentialRef(ctx, ntnxCluster)
				g.Expect(err).ToNot(HaveOccurred())

				// Get latest secret status
				g.Expect(k8sClient.Get(ctx, client.ObjectKey{
					Namespace: ntnxSecret.Namespace,
					Name:      ntnxSecret.Name,
				}, ntnxSecret)).To(Succeed())

				// Check if secret is owned by the NutanixCluster
				g.Expect(capiutil.IsOwnedByObject(ntnxSecret, ntnxCluster)).To(BeTrue())

				// check finalizer
				g.Expect(ctrlutil.ContainsFinalizer(ntnxSecret, infrav1.NutanixClusterCredentialFinalizer))
			})
			It("does not add another credentialRef if it is already set", func() {
				// Add credential ref to the ntnxCluster resource
				ntnxCluster.Spec.PrismCentral.CredentialRef = &credentialTypes.NutanixCredentialReference{
					Kind:      credentialTypes.SecretKind,
					Name:      ntnxSecret.Name,
					Namespace: ntnxSecret.Namespace,
				}

				// Add an ownerReference for the  NutanixCluster object
				ntnxSecret.OwnerReferences = []metav1.OwnerReference{
					{
						APIVersion: infrav1.GroupVersion.String(),
						Kind:       infrav1.NutanixClusterKind,
						UID:        ntnxCluster.UID,
						Name:       ntnxCluster.Name,
					},
				}

				g.Expect(k8sClient.Create(ctx, ntnxSecret)).To(Succeed())

				// Reconcile credentialRef
				err := reconciler.reconcileCredentialRef(ctx, ntnxCluster)
				g.Expect(err).ToNot(HaveOccurred())

				// Get latest secret status
				g.Expect(k8sClient.Get(ctx, client.ObjectKey{
					Namespace: ntnxSecret.Namespace,
					Name:      ntnxSecret.Name,
				}, ntnxSecret)).To(Succeed())

				// Check if secret is owned by the NutanixCluster
				g.Expect(capiutil.IsOwnedByObject(ntnxSecret, ntnxCluster)).To(BeTrue())

				// check if only one ownerReference has been added
				g.Expect(len(ntnxSecret.OwnerReferences)).To(Equal(1))
			})

			It("allows multiple ownerReferences with different kinds", func() {
				// Add credential ref to the ntnxCluster resource
				ntnxCluster.Spec.PrismCentral.CredentialRef = &credentialTypes.NutanixCredentialReference{
					Kind:      credentialTypes.SecretKind,
					Name:      ntnxSecret.Name,
					Namespace: ntnxSecret.Namespace,
				}

				// Add an ownerReference for a fake object
				ntnxSecret.OwnerReferences = []metav1.OwnerReference{
					{
						APIVersion: capiv1.GroupVersion.String(),
						Kind:       clusterKind,
						UID:        ntnxCluster.UID,
						Name:       r,
					},
				}

				g.Expect(k8sClient.Create(ctx, ntnxSecret)).To(Succeed())

				// Reconcile credentialRef
				err := reconciler.reconcileCredentialRef(ctx, ntnxCluster)
				g.Expect(err).ToNot(HaveOccurred())

				// Get latest secret status
				g.Expect(k8sClient.Get(ctx, client.ObjectKey{
					Namespace: ntnxSecret.Namespace,
					Name:      ntnxSecret.Name,
				}, ntnxSecret)).To(Succeed())

				// Check if secret is owned by the NutanixCluster
				g.Expect(capiutil.IsOwnedByObject(ntnxSecret, ntnxCluster)).To(BeTrue())

				g.Expect(len(ntnxSecret.OwnerReferences)).To(Equal(2))
			})
			It("should error if secret does not exist", func() {
				// Add credential ref to the ntnxCluster resource
				ntnxCluster.Spec.PrismCentral.CredentialRef = &credentialTypes.NutanixCredentialReference{
					Kind:      credentialTypes.SecretKind,
					Name:      ntnxSecret.Name,
					Namespace: ntnxSecret.Namespace,
				}

				// Reconcile credentialRef
				err := reconciler.reconcileCredentialRef(ctx, ntnxCluster)
				g.Expect(err).To(HaveOccurred())
			})
			It("should error if NutanixCluster is nil", func() {
				// Reconcile credentialRef
				err := reconciler.reconcileCredentialRef(ctx, nil)
				g.Expect(err).To(HaveOccurred())
			})
		})
	})

	_ = Describe("NutanixCluster reconcileCredentialRefDelete", func() {
		Context("Delete credentials ref reconcile succeed", func() {
			It("Should not return error", func() {
				ctx := context.Background()
				reconciler := &NutanixClusterReconciler{
					Client: k8sClient,
					Scheme: runtime.NewScheme(),
				}

				ntnxCluster := &infrav1.NutanixCluster{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test",
						Namespace: "default",
					},
					Spec: infrav1.NutanixClusterSpec{
						PrismCentral: &credentialTypes.NutanixPrismEndpoint{
							// Adding port info to override default value (0)
							Port: 9440,
							CredentialRef: &credentialTypes.NutanixCredentialReference{
								Name:      "test",
								Namespace: "default",
								Kind:      "Secret",
							},
						},
					},
				}

				ntnxSecret := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test",
						Namespace: "default",
					},
					StringData: map[string]string{
						"credentials": "[{\"type\": \"basic_auth\", \"data\": { \"prismCentral\":{\"username\": \"nutanix_user\", \"password\": \"nutanix_pass\"}}}]",
					},
				}

				// Create the NutanixSecret object
				g.Expect(k8sClient.Create(ctx, ntnxSecret)).To(Succeed())

				// Create the NutanixCluster object
				g.Expect(k8sClient.Create(ctx, ntnxCluster)).To(Succeed())
				defer func() {
					err := k8sClient.Delete(ctx, ntnxCluster)
					Expect(err).NotTo(HaveOccurred())
				}()

				// Add finalizer to Nutanix Secret
				g.Expect(ctrlutil.AddFinalizer(ntnxSecret, infrav1.NutanixClusterCredentialFinalizer)).To(BeTrue())
				g.Expect(k8sClient.Update(ctx, ntnxSecret)).To(Succeed())

				// Reconile Delete credential ref
				err := reconciler.reconcileCredentialRefDelete(ctx, ntnxCluster)
				g.Expect(err).NotTo(HaveOccurred())

				// Check that Nutanix Secret is deleted
				g.Expect(k8sClient.Get(ctx, client.ObjectKey{
					Namespace: ntnxSecret.Namespace,
					Name:      ntnxSecret.Name,
				}, ntnxSecret)).ToNot(Succeed())
			})
		})

		Context("Delete credentials ref reconcile failed: no credential ref", func() {
			It("Should return error", func() {
				ctx := context.Background()
				reconciler := &NutanixClusterReconciler{
					Client: k8sClient,
					Scheme: runtime.NewScheme(),
				}

				ntnxCluster := &infrav1.NutanixCluster{
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

				// Create the NutanixCluster object
				g.Expect(k8sClient.Create(ctx, ntnxCluster)).To(Succeed())
				defer func() {
					err := k8sClient.Delete(ctx, ntnxCluster)
					Expect(err).NotTo(HaveOccurred())
				}()

				// Reconile Delete credential ref
				err := reconciler.reconcileCredentialRefDelete(ctx, ntnxCluster)
				g.Expect(err).To(HaveOccurred())
			})
		})

		Context("Delete credentials ref reconcile failed: there is no secret", func() {
			It("Should not return error", func() {
				ctx := context.Background()
				reconciler := &NutanixClusterReconciler{
					Client: k8sClient,
					Scheme: runtime.NewScheme(),
				}

				ntnxCluster := &infrav1.NutanixCluster{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test",
						Namespace: "default",
					},
					Spec: infrav1.NutanixClusterSpec{
						PrismCentral: &credentialTypes.NutanixPrismEndpoint{
							// Adding port info to override default value (0)
							Port: 9440,
							CredentialRef: &credentialTypes.NutanixCredentialReference{
								Name:      "test",
								Namespace: "default",
								Kind:      "Secret",
							},
						},
					},
				}

				// Create the NutanixCluster object
				g.Expect(k8sClient.Create(ctx, ntnxCluster)).To(Succeed())
				defer func() {
					err := k8sClient.Delete(ctx, ntnxCluster)
					Expect(err).NotTo(HaveOccurred())
				}()

				// Reconile Delete credential ref
				err := reconciler.reconcileCredentialRefDelete(ctx, ntnxCluster)
				g.Expect(err).NotTo(HaveOccurred())
			})
		})

		Context("Delete credentials ref reconcile failed: PrismCentral Info is null", func() {
			It("Should not return error", func() {
				ctx := context.Background()
				reconciler := &NutanixClusterReconciler{
					Client: k8sClient,
					Scheme: runtime.NewScheme(),
				}

				ntnxCluster := &infrav1.NutanixCluster{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test",
						Namespace: "default",
					},
					Spec: infrav1.NutanixClusterSpec{
						PrismCentral: nil,
					},
				}

				// Create the NutanixCluster object
				g.Expect(k8sClient.Create(ctx, ntnxCluster)).To(Succeed())
				defer func() {
					err := k8sClient.Delete(ctx, ntnxCluster)
					Expect(err).NotTo(HaveOccurred())
				}()

				// Reconile Delete credential ref
				err := reconciler.reconcileCredentialRefDelete(ctx, ntnxCluster)
				g.Expect(err).NotTo(HaveOccurred())
			})
		})
	})
}
