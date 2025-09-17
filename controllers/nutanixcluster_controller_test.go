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
	"errors"
	"testing"

	credentialtypes "github.com/nutanix-cloud-native/prism-go-client/environment/credentials"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gstruct"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/utils/ptr"
	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/cluster-api/util"
	ctrl "sigs.k8s.io/controller-runtime"
	ctlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	ctrlutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	mockctlclient "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/mocks/ctlclient"
	mockmeta "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/mocks/k8sapimachinery"
	nctx "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/pkg/context"
)

func TestNutanixClusterReconciler(t *testing.T) {
	g := NewWithT(t)

	_ = Describe("NutanixClusterReconciler", func() {
		var (
			ntnxCluster *infrav1.NutanixCluster
			ctx         context.Context
			reconciler  *NutanixClusterReconciler
			fdRef       corev1.LocalObjectReference
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
					PrismCentral: &credentialtypes.NutanixPrismEndpoint{
						// Adding port info to override default value (0)
						Port: 9440,
					},
				},
			}
			fdRef = corev1.LocalObjectReference{Name: "test"}

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
					NamespacedName: ctlclient.ObjectKey{
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
			It("should not error and not requeue if failure domains (deprecated) are configured and cluster is Ready", func() {
				ntnxCluster.Spec.FailureDomains = []infrav1.NutanixFailureDomainConfig{{ //nolint:staticcheck // suppress complaining on Deprecated type
					Name: "fd-1",
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
					ControlPlane: true,
				}}
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
			It("should not error and not requeue if controlPlaneFailureDomains are configured and cluster is Ready", func() {
				ntnxCluster.Spec.ControlPlaneFailureDomains = []corev1.LocalObjectReference{fdRef}
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
			It("should not error if controlPlaneFailureDomains are configured with unique failure domain names", func() {
				fdRef1 := corev1.LocalObjectReference{Name: "test-1"}
				ntnxCluster.Spec.ControlPlaneFailureDomains = []corev1.LocalObjectReference{fdRef, fdRef1}
				err := k8sClient.Create(ctx, ntnxCluster)
				g.Expect(err).NotTo(HaveOccurred())
			})
			It("should error if controlPlaneFailureDomains are configured with duplicate failure domain names", func() {
				ntnxCluster.Spec.ControlPlaneFailureDomains = []corev1.LocalObjectReference{fdRef, fdRef}
				err := k8sClient.Create(ctx, ntnxCluster)
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("spec.controlPlaneFailureDomains[1]: Duplicate value"))
			})
		})

		Context("Reconcile failure domains", func() {
			It("not found failure domains not in the nutanixcluster status's failureDomains and the failure domain validated condition is false", func() {
				ntnxCluster.Spec.ControlPlaneFailureDomains = []corev1.LocalObjectReference{fdRef}

				// Create the NutanixCluster object and expect the Reconcile to be created
				g.Expect(k8sClient.Create(ctx, ntnxCluster)).To(Succeed())
				// Retrieve the applied nutanix cluster objects
				appliedNtnxCluster := &infrav1.NutanixCluster{}
				_ = k8sClient.Get(ctx, ctlclient.ObjectKey{
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
							"Type":   Equal(infrav1.FailureDomainsValidatedCondition),
							"Status": Equal(corev1.ConditionFalse),
							"Reason": Equal(infrav1.FailureDomainsMisconfiguredReason),
						},
					),
				))
			})

			It("sets the NoFailureDomainsConfigured condition when no failure domains are set", func() {
				// Create the NutanixCluster object and expect the Reconcile to be created
				g.Expect(k8sClient.Create(ctx, ntnxCluster)).To(Succeed())
				// Retrieve the applied nutanix cluster objects
				appliedNtnxCluster := &infrav1.NutanixCluster{}
				_ = k8sClient.Get(ctx, ctlclient.ObjectKey{
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
							"Type":   Equal(infrav1.NoFailureDomainsConfiguredCondition),
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
						PrismCentral: &credentialtypes.NutanixPrismEndpoint{
							// Adding port info to override default value (0)
							Port: 9440,
						},
					},
				}
				g.Expect(k8sClient.Create(ctx, additionalNtnxCluster)).To(Succeed())

				// Add credential ref to the ntnxCluster resource
				ntnxCluster.Spec.PrismCentral.CredentialRef = &credentialtypes.NutanixCredentialReference{
					Kind:      credentialtypes.SecretKind,
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
				ntnxCluster.Spec.PrismCentral.CredentialRef = &credentialtypes.NutanixCredentialReference{
					Kind:      credentialtypes.SecretKind,
					Name:      ntnxSecret.Name,
					Namespace: ntnxSecret.Namespace,
				}

				// Create secret
				g.Expect(k8sClient.Create(ctx, ntnxSecret)).To(Succeed())

				// Reconcile credentialRef
				err := reconciler.reconcileCredentialRef(ctx, ntnxCluster)
				g.Expect(err).ToNot(HaveOccurred())

				// Get latest secret status
				g.Expect(k8sClient.Get(ctx, ctlclient.ObjectKey{
					Namespace: ntnxSecret.Namespace,
					Name:      ntnxSecret.Name,
				}, ntnxSecret)).To(Succeed())

				// Check if secret is owned by the NutanixCluster
				g.Expect(util.IsOwnedByObject(ntnxSecret, ntnxCluster)).To(BeTrue())

				// check finalizer
				g.Expect(ctrlutil.ContainsFinalizer(ntnxSecret, infrav1.NutanixClusterCredentialFinalizer))
			})
			It("does not add another credentialRef if it is already set", func() {
				// Add credential ref to the ntnxCluster resource
				ntnxCluster.Spec.PrismCentral.CredentialRef = &credentialtypes.NutanixCredentialReference{
					Kind:      credentialtypes.SecretKind,
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
				g.Expect(k8sClient.Get(ctx, ctlclient.ObjectKey{
					Namespace: ntnxSecret.Namespace,
					Name:      ntnxSecret.Name,
				}, ntnxSecret)).To(Succeed())

				// Check if secret is owned by the NutanixCluster
				g.Expect(util.IsOwnedByObject(ntnxSecret, ntnxCluster)).To(BeTrue())

				// check if only one ownerReference has been added
				g.Expect(len(ntnxSecret.OwnerReferences)).To(Equal(1))
			})

			It("allows multiple ownerReferences with different kinds", func() {
				// Add credential ref to the ntnxCluster resource
				ntnxCluster.Spec.PrismCentral.CredentialRef = &credentialtypes.NutanixCredentialReference{
					Kind:      credentialtypes.SecretKind,
					Name:      ntnxSecret.Name,
					Namespace: ntnxSecret.Namespace,
				}

				// Add an ownerReference for a fake object
				ntnxSecret.OwnerReferences = []metav1.OwnerReference{
					{
						APIVersion: capiv1.GroupVersion.String(),
						Kind:       capiv1.ClusterKind,
						UID:        ntnxCluster.UID,
						Name:       r,
					},
				}

				g.Expect(k8sClient.Create(ctx, ntnxSecret)).To(Succeed())

				// Reconcile credentialRef
				err := reconciler.reconcileCredentialRef(ctx, ntnxCluster)
				g.Expect(err).ToNot(HaveOccurred())

				// Get latest secret status
				g.Expect(k8sClient.Get(ctx, ctlclient.ObjectKey{
					Namespace: ntnxSecret.Namespace,
					Name:      ntnxSecret.Name,
				}, ntnxSecret)).To(Succeed())

				// Check if secret is owned by the NutanixCluster
				g.Expect(util.IsOwnedByObject(ntnxSecret, ntnxCluster)).To(BeTrue())

				g.Expect(len(ntnxSecret.OwnerReferences)).To(Equal(2))
			})
			It("should error if secret does not exist", func() {
				// Add credential ref to the ntnxCluster resource
				ntnxCluster.Spec.PrismCentral.CredentialRef = &credentialtypes.NutanixCredentialReference{
					Kind:      credentialtypes.SecretKind,
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
						PrismCentral: &credentialtypes.NutanixPrismEndpoint{
							// Adding port info to override default value (0)
							Port: 9440,
							CredentialRef: &credentialtypes.NutanixCredentialReference{
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
				g.Expect(k8sClient.Get(ctx, ctlclient.ObjectKey{
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
						PrismCentral: &credentialtypes.NutanixPrismEndpoint{
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
						PrismCentral: &credentialtypes.NutanixPrismEndpoint{
							// Adding port info to override default value (0)
							Port: 9440,
							CredentialRef: &credentialtypes.NutanixCredentialReference{
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

var _ = Describe("NutanixCluster FailureDomain CEL validation", func() {
	var (
		ntnxCluster *infrav1.NutanixCluster
		ctx         context.Context
	)

	BeforeEach(func() {
		ctx = context.Background()
		r := util.RandomString(10)

		ntnxCluster = &infrav1.NutanixCluster{
			TypeMeta: metav1.TypeMeta{
				Kind:       infrav1.NutanixClusterKind,
				APIVersion: infrav1.GroupVersion.String(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster-" + r,
				Namespace: "default",
				UID:       utilruntime.NewUUID(),
			},
			Spec: infrav1.NutanixClusterSpec{
				PrismCentral: &credentialtypes.NutanixPrismEndpoint{
					Port: 9440,
				},
			},
		}
	})

	Context("Validate failure domain field constraints via envtest", func() {
		It("should reject creation when both failureDomains and controlPlaneFailureDomains are set", func() {
			// Create a cluster with both failure domain fields set
			invalidCluster := ntnxCluster.DeepCopy()
			invalidCluster.Spec.FailureDomains = []infrav1.NutanixFailureDomainConfig{ //nolint:staticcheck // deprecated field
				{
					Name: "fd1",
					Cluster: infrav1.NutanixResourceIdentifier{
						Type: infrav1.NutanixIdentifierName,
						Name: ptr.To("test-cluster"),
					},
					Subnets: []infrav1.NutanixResourceIdentifier{
						{
							Type: infrav1.NutanixIdentifierName,
							Name: ptr.To("test-subnet"),
						},
					},
					ControlPlane: true,
				},
			}
			invalidCluster.Spec.ControlPlaneFailureDomains = []corev1.LocalObjectReference{
				{Name: "fd1"},
			}

			// Attempt to create the cluster should fail with validation error
			err := k8sClient.Create(ctx, invalidCluster)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("Cannot set both 'failureDomains' and 'controlPlaneFailureDomains' fields simultaneously"))
		})

		It("should allow creation when only failureDomains is set", func() {
			// Create a cluster with only failureDomains set (deprecated field)
			validCluster := ntnxCluster.DeepCopy()
			validCluster.Spec.FailureDomains = []infrav1.NutanixFailureDomainConfig{ //nolint:staticcheck // deprecated field
				{
					Name: "fd1",
					Cluster: infrav1.NutanixResourceIdentifier{
						Type: infrav1.NutanixIdentifierName,
						Name: ptr.To("test-cluster"),
					},
					Subnets: []infrav1.NutanixResourceIdentifier{
						{
							Type: infrav1.NutanixIdentifierName,
							Name: ptr.To("test-subnet"),
						},
					},
					ControlPlane: true,
				},
			}

			// Creation should succeed
			Expect(k8sClient.Create(ctx, validCluster)).To(Succeed())

			// Clean up
			Expect(k8sClient.Delete(ctx, validCluster)).To(Succeed())
		})

		It("should allow creation when only controlPlaneFailureDomains is set", func() {
			// Create a cluster with only controlPlaneFailureDomains set (new field)
			validCluster := ntnxCluster.DeepCopy()
			validCluster.Spec.ControlPlaneFailureDomains = []corev1.LocalObjectReference{
				{Name: "fd1"},
			}

			// Creation should succeed
			Expect(k8sClient.Create(ctx, validCluster)).To(Succeed())

			// Clean up
			Expect(k8sClient.Delete(ctx, validCluster)).To(Succeed())
		})

		It("should allow creation when neither failure domain field is set", func() {
			// Create a cluster with neither failure domain field set
			validCluster := ntnxCluster.DeepCopy()

			// Creation should succeed (both fields are optional)
			Expect(k8sClient.Create(ctx, validCluster)).To(Succeed())

			// Clean up
			Expect(k8sClient.Delete(ctx, validCluster)).To(Succeed())
		})

		It("should reject update when both fields are set via update", func() {
			// First create a valid cluster
			validCluster := ntnxCluster.DeepCopy()
			Expect(k8sClient.Create(ctx, validCluster)).To(Succeed())

			// Now try to update it to set both fields
			updatedCluster := validCluster.DeepCopy()
			updatedCluster.Spec.FailureDomains = []infrav1.NutanixFailureDomainConfig{ //nolint:staticcheck // deprecated field
				{
					Name: "fd1",
					Cluster: infrav1.NutanixResourceIdentifier{
						Type: infrav1.NutanixIdentifierName,
						Name: ptr.To("test-cluster"),
					},
					Subnets: []infrav1.NutanixResourceIdentifier{
						{
							Type: infrav1.NutanixIdentifierName,
							Name: ptr.To("test-subnet"),
						},
					},
					ControlPlane: true,
				},
			}
			updatedCluster.Spec.ControlPlaneFailureDomains = []corev1.LocalObjectReference{
				{Name: "fd1"},
			}

			// Update should fail with validation error
			err := k8sClient.Update(ctx, updatedCluster)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("Cannot set both 'failureDomains' and 'controlPlaneFailureDomains' fields simultaneously"))

			// Clean up the created cluster
			Expect(k8sClient.Delete(ctx, validCluster)).To(Succeed())
		})

		It("should allow migration from legacy failureDomains to new controlPlaneFailureDomains", func() {
			// First create a cluster with the legacy failureDomains field
			clusterWithLegacy := ntnxCluster.DeepCopy()
			clusterWithLegacy.Spec.FailureDomains = []infrav1.NutanixFailureDomainConfig{ //nolint:staticcheck // deprecated field
				{
					Name: "fd1",
					Cluster: infrav1.NutanixResourceIdentifier{
						Type: infrav1.NutanixIdentifierName,
						Name: ptr.To("test-cluster"),
					},
					Subnets: []infrav1.NutanixResourceIdentifier{
						{
							Type: infrav1.NutanixIdentifierName,
							Name: ptr.To("test-subnet"),
						},
					},
					ControlPlane: true,
				},
			}

			// Create the cluster with legacy field
			Expect(k8sClient.Create(ctx, clusterWithLegacy)).To(Succeed())

			// Now migrate: remove legacy field and add new field
			migratedCluster := clusterWithLegacy.DeepCopy()
			migratedCluster.Spec.FailureDomains = nil //nolint:staticcheck // Deprecated field
			migratedCluster.Spec.ControlPlaneFailureDomains = []corev1.LocalObjectReference{
				{Name: "fd1"},
			}

			// Migration update should succeed
			Expect(k8sClient.Update(ctx, migratedCluster)).To(Succeed())

			// Clean up the migrated cluster
			Expect(k8sClient.Delete(ctx, migratedCluster)).To(Succeed())
		})

		It("should reject adding new controlPlaneFailureDomains to existing cluster with legacy failureDomains", func() {
			// First create a cluster with the legacy failureDomains field
			clusterWithLegacy := ntnxCluster.DeepCopy()
			clusterWithLegacy.Spec.FailureDomains = []infrav1.NutanixFailureDomainConfig{ //nolint:staticcheck // deprecated field
				{
					Name: "fd1",
					Cluster: infrav1.NutanixResourceIdentifier{
						Type: infrav1.NutanixIdentifierName,
						Name: ptr.To("test-cluster"),
					},
					Subnets: []infrav1.NutanixResourceIdentifier{
						{
							Type: infrav1.NutanixIdentifierName,
							Name: ptr.To("test-subnet"),
						},
					},
					ControlPlane: true,
				},
			}

			// Create the cluster with legacy field
			Expect(k8sClient.Create(ctx, clusterWithLegacy)).To(Succeed())

			// Now try to add the new field without removing the legacy one (bad migration)
			badMigrationCluster := clusterWithLegacy.DeepCopy()
			// Keep the legacy field AND add the new field (this should fail)
			badMigrationCluster.Spec.ControlPlaneFailureDomains = []corev1.LocalObjectReference{
				{Name: "fd1"},
			}

			// Bad migration update should fail with validation error
			err := k8sClient.Update(ctx, badMigrationCluster)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("Cannot set both 'failureDomains' and 'controlPlaneFailureDomains' fields simultaneously"))

			// Clean up the original cluster
			Expect(k8sClient.Delete(ctx, clusterWithLegacy)).To(Succeed())
		})
	})
})

func TestReconcileCredentialRefWithPrismCentralNotSetOnCluster(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	ctx := context.Background()
	fakeClient := mockctlclient.NewMockClient(mockCtrl)
	nutanixCluster := &infrav1.NutanixCluster{
		Spec: infrav1.NutanixClusterSpec{},
	}

	reconciler := &NutanixClusterReconciler{
		Client: fakeClient,
	}

	err := reconciler.reconcileCredentialRef(ctx, nutanixCluster)
	assert.NoError(t, err)
}

func TestReconcileCredentialRefWithValidCredentialRef(t *testing.T) {
	mockCtrl := gomock.NewController(t)

	nutanixCluster := &infrav1.NutanixCluster{
		Spec: infrav1.NutanixClusterSpec{
			PrismCentral: &credentialtypes.NutanixPrismEndpoint{
				CredentialRef: &credentialtypes.NutanixCredentialReference{
					Kind:      credentialtypes.SecretKind,
					Name:      "test-credential",
					Namespace: "test-ns",
				},
			},
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-ns",
		},
	}

	secret := ctlclient.ObjectKey{
		Name:      "test-credential",
		Namespace: "test-ns",
	}

	ctx := context.Background()
	fakeClient := mockctlclient.NewMockClient(mockCtrl)
	fakeClient.EXPECT().Get(ctx, secret, gomock.Any()).Return(nil)
	fakeClient.EXPECT().Update(ctx, gomock.Any()).Return(nil)

	reconciler := &NutanixClusterReconciler{
		Client: fakeClient,
	}

	err := reconciler.reconcileCredentialRef(ctx, nutanixCluster)
	assert.NoError(t, err)
}

func TestReconcileCredentialRefWithValidCredentialRefFailedUpdate(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	ctx := context.Background()
	fakeClient := mockctlclient.NewMockClient(mockCtrl)
	nutanixCluster := &infrav1.NutanixCluster{
		Spec: infrav1.NutanixClusterSpec{
			PrismCentral: &credentialtypes.NutanixPrismEndpoint{
				CredentialRef: &credentialtypes.NutanixCredentialReference{
					Kind:      credentialtypes.SecretKind,
					Name:      "test-credential",
					Namespace: "test-ns",
				},
			},
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-ns",
		},
	}
	secret := ctlclient.ObjectKey{
		Name:      "test-credential",
		Namespace: "test-ns",
	}

	fakeClient.EXPECT().Get(ctx, secret, gomock.Any()).Return(nil)
	fakeClient.EXPECT().Update(ctx, gomock.Any()).Return(errors.New("failed to update secret"))

	reconciler := &NutanixClusterReconciler{
		Client: fakeClient,
	}

	err := reconciler.reconcileCredentialRef(ctx, nutanixCluster)
	assert.Error(t, err)
}

func TestReconcileTrustBundleRefWithNilTrustBundleRef(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	ctx := context.Background()
	fakeClient := mockctlclient.NewMockClient(mockCtrl)
	nutanixCluster := &infrav1.NutanixCluster{
		Spec: infrav1.NutanixClusterSpec{},
	}

	reconciler := &NutanixClusterReconciler{
		Client: fakeClient,
	}

	err := reconciler.reconcileTrustBundleRef(ctx, nutanixCluster)
	assert.NoError(t, err)
}

func TestReconcileTrustBundleRefWithValidTrustBundleRef(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	ctx := context.Background()
	fakeClient := mockctlclient.NewMockClient(mockCtrl)
	nutanixCluster := &infrav1.NutanixCluster{
		Spec: infrav1.NutanixClusterSpec{
			PrismCentral: &credentialtypes.NutanixPrismEndpoint{
				AdditionalTrustBundle: &credentialtypes.NutanixTrustBundleReference{
					Name: "test-trustbundle",
				},
			},
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-ns",
		},
	}

	configMap := &corev1.ConfigMap{}
	configMapKey := ctlclient.ObjectKey{
		Namespace: nutanixCluster.Namespace,
		Name:      nutanixCluster.Spec.PrismCentral.AdditionalTrustBundle.Name,
	}

	fakeClient.EXPECT().Get(ctx, configMapKey, configMap).Return(nil)
	fakeClient.EXPECT().Update(ctx, gomock.Any()).Return(nil)

	reconciler := &NutanixClusterReconciler{
		Client: fakeClient,
	}

	err := reconciler.reconcileTrustBundleRef(ctx, nutanixCluster)
	assert.NoError(t, err)
}

func TestReconcileTrustBundleRefWithFailedGet(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	ctx := context.Background()
	fakeClient := mockctlclient.NewMockClient(mockCtrl)
	nutanixCluster := &infrav1.NutanixCluster{
		Spec: infrav1.NutanixClusterSpec{
			PrismCentral: &credentialtypes.NutanixPrismEndpoint{
				AdditionalTrustBundle: &credentialtypes.NutanixTrustBundleReference{
					Name: "test-trustbundle",
				},
			},
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-ns",
		},
	}

	configMap := &corev1.ConfigMap{}
	configMapKey := ctlclient.ObjectKey{
		Namespace: nutanixCluster.Namespace,
		Name:      nutanixCluster.Spec.PrismCentral.AdditionalTrustBundle.Name,
	}

	fakeClient.EXPECT().Get(ctx, configMapKey, configMap).Return(errors.New("failed to get configmap"))

	reconciler := &NutanixClusterReconciler{
		Client: fakeClient,
	}

	err := reconciler.reconcileTrustBundleRef(ctx, nutanixCluster)
	assert.Error(t, err)
}

func TestReconcileTrustBundleRefWithFailedUpdate(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	ctx := context.Background()
	fakeClient := mockctlclient.NewMockClient(mockCtrl)
	nutanixCluster := &infrav1.NutanixCluster{
		Spec: infrav1.NutanixClusterSpec{
			PrismCentral: &credentialtypes.NutanixPrismEndpoint{
				AdditionalTrustBundle: &credentialtypes.NutanixTrustBundleReference{
					Name: "test-trustbundle",
				},
			},
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-ns",
		},
	}

	configMap := &corev1.ConfigMap{}
	configMapKey := ctlclient.ObjectKey{
		Namespace: nutanixCluster.Namespace,
		Name:      nutanixCluster.Spec.PrismCentral.AdditionalTrustBundle.Name,
	}

	fakeClient.EXPECT().Get(ctx, configMapKey, configMap).Return(nil)
	fakeClient.EXPECT().Update(ctx, gomock.Any()).Return(errors.New("failed to update configmap"))

	reconciler := &NutanixClusterReconciler{
		Client: fakeClient,
	}

	err := reconciler.reconcileTrustBundleRef(ctx, nutanixCluster)
	assert.Error(t, err)
}

func TestReconcileTrustBundleRefWithExistingOwner(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	ctx := context.Background()
	fakeClient := mockctlclient.NewMockClient(mockCtrl)
	nutanixCluster := &infrav1.NutanixCluster{
		Spec: infrav1.NutanixClusterSpec{
			PrismCentral: &credentialtypes.NutanixPrismEndpoint{
				AdditionalTrustBundle: &credentialtypes.NutanixTrustBundleReference{
					Name: "test-trustbundle",
				},
			},
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-ns",
		},
		TypeMeta: metav1.TypeMeta{
			Kind: infrav1.NutanixClusterKind,
		},
	}

	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: infrav1.GroupVersion.String(),
					Kind:       infrav1.NutanixClusterKind,
					Name:       "another-cluster",
				},
			},
		},
	}
	configMapKey := ctlclient.ObjectKey{
		Namespace: nutanixCluster.Namespace,
		Name:      nutanixCluster.Spec.PrismCentral.AdditionalTrustBundle.Name,
	}

	fakeClient.EXPECT().Get(ctx, configMapKey, gomock.Any()).DoAndReturn(func(_ context.Context, _ ctlclient.ObjectKey, obj runtime.Object, _ ...ctlclient.GetOption) error {
		configMap.DeepCopyInto(obj.(*corev1.ConfigMap))
		return nil
	})

	reconciler := &NutanixClusterReconciler{
		Client: fakeClient,
	}

	err := reconciler.reconcileTrustBundleRef(ctx, nutanixCluster)
	assert.Error(t, err)
}

func TestNutanixClusterReconciler_SetupWithManager(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	ctx := context.Background()
	log := ctrl.Log.WithName("controller")
	scheme := runtime.NewScheme()
	err := infrav1.AddToScheme(scheme)
	require.NoError(t, err)
	err = capiv1.AddToScheme(scheme)
	require.NoError(t, err)

	restScope := mockmeta.NewMockRESTScope(mockCtrl)
	restScope.EXPECT().Name().Return(meta.RESTScopeNameNamespace).AnyTimes()

	restMapper := mockmeta.NewMockRESTMapper(mockCtrl)
	restMapper.EXPECT().RESTMapping(gomock.Any()).Return(&meta.RESTMapping{Scope: restScope}, nil).AnyTimes()

	mockClient := mockctlclient.NewMockClient(mockCtrl)
	mockClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	mockClient.EXPECT().RESTMapper().Return(restMapper).AnyTimes()

	cache := mockctlclient.NewMockCache(mockCtrl)

	mgr := mockctlclient.NewMockManager(mockCtrl)
	mgr.EXPECT().GetCache().Return(cache).AnyTimes()
	mgr.EXPECT().GetScheme().Return(scheme).AnyTimes()
	mgr.EXPECT().GetControllerOptions().Return(config.Controller{MaxConcurrentReconciles: 1}).AnyTimes()
	mgr.EXPECT().GetLogger().Return(log).AnyTimes()
	mgr.EXPECT().Add(gomock.Any()).Return(nil).AnyTimes()
	mgr.EXPECT().GetClient().Return(mockClient).AnyTimes()

	reconciler := &NutanixClusterReconciler{
		Client: mockClient,
		Scheme: scheme,
		controllerConfig: &ControllerConfig{
			MaxConcurrentReconciles: 1,
			SkipNameValidation:      true, // Enable for tests
		},
	}

	err = reconciler.SetupWithManager(ctx, mgr)
	assert.NoError(t, err)
}

func TestReconcileTrustBundleRefDelete(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	ctx := context.Background()
	fakeClient := mockctlclient.NewMockClient(mockCtrl)

	reconciler := &NutanixClusterReconciler{
		Client: fakeClient,
	}

	nutanixCluster := &infrav1.NutanixCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-ns",
		},
	}

	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-configmap",
			Namespace:  "test-ns",
			Finalizers: []string{infrav1.NutanixClusterCredentialFinalizer},
		},
	}

	configMapKey := ctlclient.ObjectKey{
		Namespace: configMap.Namespace,
		Name:      configMap.Name,
	}

	t.Run("should not return error if prism central or trust bundle is not set or trust bundle is of kind string", func(t *testing.T) {
		nutanixCluster.Spec.PrismCentral = nil
		err := reconciler.reconcileTrustBundleRefDelete(ctx, nutanixCluster)
		assert.NoError(t, err)

		nutanixCluster.Spec.PrismCentral = &credentialtypes.NutanixPrismEndpoint{}
		err = reconciler.reconcileTrustBundleRefDelete(ctx, nutanixCluster)
		assert.NoError(t, err)

		nutanixCluster.Spec.PrismCentral = &credentialtypes.NutanixPrismEndpoint{
			AdditionalTrustBundle: &credentialtypes.NutanixTrustBundleReference{
				Kind: credentialtypes.NutanixTrustBundleKindString,
			},
		}

		err = reconciler.reconcileTrustBundleRefDelete(ctx, nutanixCluster)
		assert.NoError(t, err)
	})

	t.Run("should return nil if GET error is not found", func(t *testing.T) {
		nutanixCluster.Spec.PrismCentral = &credentialtypes.NutanixPrismEndpoint{
			AdditionalTrustBundle: &credentialtypes.NutanixTrustBundleReference{
				Kind:      credentialtypes.NutanixTrustBundleKindConfigMap,
				Name:      configMap.Name,
				Namespace: configMap.Namespace,
			},
		}

		fakeClient.EXPECT().Get(ctx, configMapKey, gomock.Any()).DoAndReturn(func(_ context.Context, _ ctlclient.ObjectKey, obj runtime.Object, _ ...ctlclient.GetOption) error {
			return apierrors.NewNotFound(schema.GroupResource{}, "not found")
		})

		err := reconciler.reconcileTrustBundleRefDelete(ctx, nutanixCluster)
		assert.NoError(t, err)
	})

	t.Run("should return error if GET error is different that not found", func(t *testing.T) {
		nutanixCluster.Spec.PrismCentral = &credentialtypes.NutanixPrismEndpoint{
			AdditionalTrustBundle: &credentialtypes.NutanixTrustBundleReference{
				Kind:      credentialtypes.NutanixTrustBundleKindConfigMap,
				Name:      configMap.Name,
				Namespace: configMap.Namespace,
			},
		}

		fakeClient.EXPECT().Get(ctx, configMapKey, gomock.Any()).DoAndReturn(func(_ context.Context, _ ctlclient.ObjectKey, obj runtime.Object, _ ...ctlclient.GetOption) error {
			return apierrors.NewBadRequest("bad request")
		})

		err := reconciler.reconcileTrustBundleRefDelete(ctx, nutanixCluster)
		assert.Error(t, err)
	})

	t.Run("should return error if Update returns error after removing finalizers", func(t *testing.T) {
		nutanixCluster.Spec.PrismCentral = &credentialtypes.NutanixPrismEndpoint{
			AdditionalTrustBundle: &credentialtypes.NutanixTrustBundleReference{
				Kind:      credentialtypes.NutanixTrustBundleKindConfigMap,
				Name:      configMap.Name,
				Namespace: configMap.Namespace,
			},
		}

		fakeClient.EXPECT().Get(ctx, configMapKey, gomock.Any()).DoAndReturn(func(_ context.Context, _ ctlclient.ObjectKey, obj runtime.Object, _ ...ctlclient.GetOption) error {
			configMap.DeepCopyInto(obj.(*corev1.ConfigMap))
			return nil
		})

		fakeClient.EXPECT().Update(ctx, gomock.Any()).Return(errors.New("failed to update configmap"))

		err := reconciler.reconcileTrustBundleRefDelete(ctx, nutanixCluster)
		assert.Error(t, err)
	})

	t.Run("should return error if Delete returns error other than not found", func(t *testing.T) {
		nutanixCluster.Spec.PrismCentral = &credentialtypes.NutanixPrismEndpoint{
			AdditionalTrustBundle: &credentialtypes.NutanixTrustBundleReference{
				Kind:      credentialtypes.NutanixTrustBundleKindConfigMap,
				Name:      configMap.Name,
				Namespace: configMap.Namespace,
			},
		}

		fakeClient.EXPECT().Get(ctx, configMapKey, gomock.Any()).DoAndReturn(func(_ context.Context, _ ctlclient.ObjectKey, obj runtime.Object, _ ...ctlclient.GetOption) error {
			configMap.DeepCopyInto(obj.(*corev1.ConfigMap))
			return nil
		})

		fakeClient.EXPECT().Update(ctx, gomock.Any()).Return(nil)
		fakeClient.EXPECT().Delete(ctx, gomock.Any()).Return(apierrors.NewBadRequest("bad request"))

		err := reconciler.reconcileTrustBundleRefDelete(ctx, nutanixCluster)
		assert.Error(t, err)
	})

	t.Run("should return no errors if configmap already deleted", func(t *testing.T) {
		nutanixCluster.Spec.PrismCentral = &credentialtypes.NutanixPrismEndpoint{
			AdditionalTrustBundle: &credentialtypes.NutanixTrustBundleReference{
				Kind:      credentialtypes.NutanixTrustBundleKindConfigMap,
				Name:      configMap.Name,
				Namespace: configMap.Namespace,
			},
		}

		fakeClient.EXPECT().Get(ctx, configMapKey, gomock.Any()).DoAndReturn(func(_ context.Context, _ ctlclient.ObjectKey, obj runtime.Object, _ ...ctlclient.GetOption) error {
			configMap.DeepCopyInto(obj.(*corev1.ConfigMap))
			return nil
		})

		fakeClient.EXPECT().Update(ctx, gomock.Any()).Return(nil)
		fakeClient.EXPECT().Delete(ctx, gomock.Any()).Return(apierrors.NewNotFound(schema.GroupResource{}, "configmap not found"))

		err := reconciler.reconcileTrustBundleRefDelete(ctx, nutanixCluster)
		assert.NoError(t, err)
	})
}
