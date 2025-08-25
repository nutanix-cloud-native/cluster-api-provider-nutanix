/*
Copyright 2025 Nutanix

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
	"fmt"

	//"errors"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/utils/ptr"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/controller-runtime/pkg/client"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
)

func TestNutanixFailureDomainReconciler(t *testing.T) {
	g := NewWithT(t)

	_ = Describe("NutanixFailureDomainReconciler", func() {
		var (
			ctx         context.Context
			fdObj       *infrav1.NutanixFailureDomain
			ntnxMachine *infrav1.NutanixMachine
			reconciler  *NutanixFailureDomainReconciler
			rstr, rstr2 string
		)

		BeforeEach(func() {
			ctx = context.Background()
			rstr = util.RandomString(10)
			rstr2 = util.RandomString(12)

			fdObj = &infrav1.NutanixFailureDomain{
				TypeMeta: metav1.TypeMeta{
					Kind:       infrav1.NutanixFailureDomainKind,
					APIVersion: infrav1.GroupVersion.String(),
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "fd-test",
					Namespace: corev1.NamespaceDefault,
				},
				Spec: infrav1.NutanixFailureDomainSpec{
					PrismElementCluster: infrav1.NutanixResourceIdentifier{
						Type: infrav1.NutanixIdentifierName,
						Name: &rstr,
					},
					Subnets: []infrav1.NutanixResourceIdentifier{
						{Type: infrav1.NutanixIdentifierName, Name: &rstr},
					},
				},
			}

			reconciler = &NutanixFailureDomainReconciler{
				Client: k8sClient,
				Scheme: runtime.NewScheme(),
			}

			ntnxMachine = &infrav1.NutanixMachine{
				TypeMeta: metav1.TypeMeta{
					Kind:       infrav1.NutanixMachineKind,
					APIVersion: infrav1.GroupVersion.String(),
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "m-test",
					Namespace: corev1.NamespaceDefault,
					UID:       utilruntime.NewUUID(),
				},
				Spec: infrav1.NutanixMachineSpec{
					VCPUsPerSocket: 1,
					VCPUSockets:    2,
					MemorySize:     resource.MustParse("2Gi"),
					SystemDiskSize: resource.MustParse("20Gi"),
					Image: &infrav1.NutanixResourceIdentifier{
						Type: infrav1.NutanixIdentifierName,
						Name: &rstr,
					},
					Cluster: infrav1.NutanixResourceIdentifier{
						Type: infrav1.NutanixIdentifierName,
						Name: &rstr,
					},
					Subnets: []infrav1.NutanixResourceIdentifier{
						{Type: infrav1.NutanixIdentifierUUID, UUID: &rstr},
					},
				},
				Status: infrav1.NutanixMachineStatus{
					FailureDomain: &fdObj.Name,
				},
			}
		})

		AfterEach(func() {
			// Delete the failure domain object if exists.
			_ = k8sClient.Delete(ctx, fdObj)

			// Delete the nutanix machine object if exists.
			_ = k8sClient.Delete(ctx, ntnxMachine)
		})

		Context("Update an NutanixFailureDomain Configuration", func() {
			It("should not allow to update failure domain prism element configuration", func() {
				// Create the NutanixFailureDomain object and expect creation success
				g.Expect(k8sClient.Create(ctx, fdObj)).To(Succeed())

				// change the PrismElement configuration
				fd2 := fdObj.DeepCopy()
				fd2.Spec.PrismElementCluster.Name = &rstr2
				err := k8sClient.Update(ctx, fd2)
				// Expect error ocurred and the error message contains the expected substr.
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("spec.prismElementCluster: Invalid value: \"object\": prismElementCluster is immutable once set"))
			})

			It("should not allow to update failure domain subnets configuration", func() {
				// Create the NutanixFailureDomain object and expect creation success
				g.Expect(k8sClient.Create(ctx, fdObj)).To(Succeed())

				// change the subnets configuration
				fd2 := fdObj.DeepCopy()
				fd2.Spec.Subnets[0].Name = &rstr2
				err := k8sClient.Update(ctx, fd2)
				// Expect error ocurred and the error message contains the expected substr.
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("spec.subnets: Invalid value: \"array\": subnets is immutable once set"))
			})
		})

		Context("Reconcile an NutanixFailureDomain", func() {
			It("should allow to delete failure domain if no nutanix machines use it", func() {
				// Create the NutanixFailureDomain object and expect creation success
				g.Expect(k8sClient.Create(ctx, fdObj)).To(Succeed())

				// Expect calling reconciler.reconcileDelete() without error, because no NutanixMachine uses the failure domain
				_, err := reconciler.reconcileDelete(ctx, fdObj)
				g.Expect(err).NotTo(HaveOccurred())
				cond := conditions.Get(fdObj, infrav1.FailureDomainSafeForDeletionCondition)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(corev1.ConditionTrue))

				// Delete the failure domain object and expect deletion success
				g.Expect(k8sClient.Delete(ctx, fdObj)).To(Succeed())
			})

			It("should not allow to delete failure domain if there are nutanix machines use it", func() {
				// Create the NutanixFailureDomain object and expect creation success
				g.Expect(k8sClient.Create(ctx, fdObj)).To(Succeed())

				// Create the NutanixMachine object and expect creation success
				g.Expect(k8sClient.Create(ctx, ntnxMachine)).To(Succeed())
				// Update ntnxMachine status to add failureDomain
				ntnxMachine.Status.FailureDomain = &fdObj.Name
				err := k8sClient.Status().Update(ctx, ntnxMachine)
				g.Expect(err).NotTo(HaveOccurred())

				// Expect calling reconciler.reconcileDelete() returns error, because there is NutanixMachine using the failure domain
				_, err = reconciler.reconcileDelete(ctx, fdObj)
				g.Expect(err).To(HaveOccurred())
				cond := conditions.Get(fdObj, infrav1.FailureDomainSafeForDeletionCondition)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(corev1.ConditionFalse))
				g.Expect(cond.Severity).To(Equal(capiv1.ConditionSeverityError))
				g.Expect(cond.Reason).To(Equal(infrav1.FailureDomainInUseReason))
			})
		})
	})

	_ = Describe("NutanixFailureDomain CEL Validation", func() {
		var ctx context.Context

		BeforeEach(func() {
			ctx = context.Background()
		})

		Context("Subnet uniqueness validation", func() {
			It("should accept NutanixFailureDomain with unique subnets", func() {
				subnet1 := infrav1.NutanixResourceIdentifier{
					Type: infrav1.NutanixIdentifierUUID,
					UUID: ptr.To("550e8400-e29b-41d4-a716-446655440000"),
				}
				subnet2 := infrav1.NutanixResourceIdentifier{
					Type: infrav1.NutanixIdentifierUUID,
					UUID: ptr.To("123e4567-e89b-12d3-a456-426614174000"),
				}
				nfd := &infrav1.NutanixFailureDomain{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "nfd-unique-subnets",
						Namespace: "default",
					},
					Spec: infrav1.NutanixFailureDomainSpec{
						PrismElementCluster: subnet1,
						Subnets:             []infrav1.NutanixResourceIdentifier{subnet1, subnet2},
					},
				}
				err := k8sClient.Create(ctx, nfd)
				Expect(err).NotTo(HaveOccurred())
				_ = k8sClient.Delete(ctx, nfd)
			})

			It("should reject NutanixFailureDomain with duplicate subnets", func() {
				subnet := infrav1.NutanixResourceIdentifier{
					Type: infrav1.NutanixIdentifierUUID,
					UUID: ptr.To("550e8400-e29b-41d4-a716-446655440000"),
				}
				nfd := &infrav1.NutanixFailureDomain{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "nfd-duplicate-subnets",
						Namespace: "default",
					},
					Spec: infrav1.NutanixFailureDomainSpec{
						PrismElementCluster: subnet,
						Subnets:             []infrav1.NutanixResourceIdentifier{subnet, subnet},
					},
				}
				err := k8sClient.Create(ctx, nfd)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("each subnet must be unique"))
			})
		})

		Context("Subnet min/max items validation", func() {
			It("should reject NutanixFailureDomain with zero subnets", func() {
				cluster := infrav1.NutanixResourceIdentifier{
					Type: infrav1.NutanixIdentifierUUID,
					UUID: ptr.To("550e8400-e29b-41d4-a716-446655440000"),
				}
				nfd := &infrav1.NutanixFailureDomain{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "nfd-no-subnets",
						Namespace: "default",
					},
					Spec: infrav1.NutanixFailureDomainSpec{
						PrismElementCluster: cluster,
						Subnets:             []infrav1.NutanixResourceIdentifier{},
					},
				}
				err := k8sClient.Create(ctx, nfd)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("MinItems"))
			})

			It("should reject NutanixFailureDomain with more than 32 subnets", func() {
				cluster := infrav1.NutanixResourceIdentifier{
					Type: infrav1.NutanixIdentifierUUID,
					UUID: ptr.To("550e8400-e29b-41d4-a716-446655440000"),
				}
				subnets := make([]infrav1.NutanixResourceIdentifier, 33)
				for i := range subnets {
					uuid := fmt.Sprintf("550e8400-e29b-41d4-a716-44665544%04d", i)
					subnets[i] = infrav1.NutanixResourceIdentifier{
						Type: infrav1.NutanixIdentifierUUID,
						UUID: &uuid,
					}
				}
				nfd := &infrav1.NutanixFailureDomain{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "nfd-too-many-subnets",
						Namespace: "default",
					},
					Spec: infrav1.NutanixFailureDomainSpec{
						PrismElementCluster: cluster,
						Subnets:             subnets,
					},
				}
				err := k8sClient.Create(ctx, nfd)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("MaxItems"))
			})
		})

		Context("Immutability validation", func() {
			It("should reject update to PrismElementCluster", func() {
				cluster := infrav1.NutanixResourceIdentifier{
					Type: infrav1.NutanixIdentifierUUID,
					UUID: ptr.To("550e8400-e29b-41d4-a716-446655440000"),
				}
				nfd := &infrav1.NutanixFailureDomain{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "nfd-immutable-cluster",
						Namespace: "default",
					},
					Spec: infrav1.NutanixFailureDomainSpec{
						PrismElementCluster: cluster,
						Subnets:             []infrav1.NutanixResourceIdentifier{cluster},
					},
				}
				Expect(k8sClient.Create(ctx, nfd)).To(Succeed())

				patch := client.MergeFrom(nfd.DeepCopy())
				nfd.Spec.PrismElementCluster.UUID = ptr.To("123e4567-e89b-12d3-a456-426614174000")
				err := k8sClient.Patch(ctx, nfd, patch)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("immutable"))

				_ = k8sClient.Delete(ctx, nfd)
			})

			It("should reject update to Subnets", func() {
				cluster := infrav1.NutanixResourceIdentifier{
					Type: infrav1.NutanixIdentifierUUID,
					UUID: ptr.To("550e8400-e29b-41d4-a716-446655440000"),
				}
				subnet2 := infrav1.NutanixResourceIdentifier{
					Type: infrav1.NutanixIdentifierUUID,
					UUID: ptr.To("123e4567-e89b-12d3-a456-426614174000"),
				}
				nfd := &infrav1.NutanixFailureDomain{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "nfd-immutable-subnets",
						Namespace: "default",
					},
					Spec: infrav1.NutanixFailureDomainSpec{
						PrismElementCluster: cluster,
						Subnets:             []infrav1.NutanixResourceIdentifier{cluster},
					},
				}
				Expect(k8sClient.Create(ctx, nfd)).To(Succeed())

				patch := client.MergeFrom(nfd.DeepCopy())
				nfd.Spec.Subnets = append(nfd.Spec.Subnets, subnet2)
				err := k8sClient.Patch(ctx, nfd, patch)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("immutable"))

				_ = k8sClient.Delete(ctx, nfd)
			})
		})

		Context("NutanixMachineSpec Subnets Uniqueness Validation", func() {
			It("should accept NutanixMachine with unique subnets", func() {
				subnet1 := infrav1.NutanixResourceIdentifier{
					Type: infrav1.NutanixIdentifierUUID,
					UUID: ptr.To("550e8400-e29b-41d4-a716-446655440000"),
				}
				subnet2 := infrav1.NutanixResourceIdentifier{
					Type: infrav1.NutanixIdentifierUUID,
					UUID: ptr.To("123e4567-e89b-12d3-a456-426614174000"),
				}
				nm := &infrav1.NutanixMachine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "machine-unique-subnets",
						Namespace: "default",
					},
					Spec: infrav1.NutanixMachineSpec{
						VCPUsPerSocket: 1,
						VCPUSockets:    1,
						MemorySize:     resource.MustParse("2Gi"),
						Cluster: infrav1.NutanixResourceIdentifier{
							Type: infrav1.NutanixIdentifierUUID,
							UUID: ptr.To("550e8400-e29b-41d4-a716-446655440000"),
						},
						Subnets: []infrav1.NutanixResourceIdentifier{subnet1, subnet2},
					},
				}
				err := k8sClient.Create(ctx, nm)
				Expect(err).NotTo(HaveOccurred())
				_ = k8sClient.Delete(ctx, nm)
			})

			It("should reject NutanixMachine with duplicate subnets", func() {
				subnet := infrav1.NutanixResourceIdentifier{
					Type: infrav1.NutanixIdentifierUUID,
					UUID: ptr.To("550e8400-e29b-41d4-a716-446655440000"),
				}
				nm := &infrav1.NutanixMachine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "machine-duplicate-subnets",
						Namespace: "default",
					},
					Spec: infrav1.NutanixMachineSpec{
						VCPUsPerSocket: 1,
						VCPUSockets:    1,
						MemorySize:     resource.MustParse("2Gi"),
						Cluster: infrav1.NutanixResourceIdentifier{
							Type: infrav1.NutanixIdentifierUUID,
							UUID: ptr.To("550e8400-e29b-41d4-a716-446655440000"),
						},
						Subnets: []infrav1.NutanixResourceIdentifier{subnet, subnet},
					},
				}
				err := k8sClient.Create(ctx, nm)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("each subnet must be unique"))
			})
		})
	})
}
