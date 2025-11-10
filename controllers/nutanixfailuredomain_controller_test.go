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

	//"errors"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/uuid"
	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta1" //nolint:staticcheck // suppress complaining on Deprecated package
	"sigs.k8s.io/cluster-api/util"
	v1beta1conditions "sigs.k8s.io/cluster-api/util/deprecated/v1beta1/conditions" //nolint:staticcheck // suppress complaining on Deprecated package

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
						{Type: infrav1.NutanixIdentifierName, Name: &rstr},
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
				cond := v1beta1conditions.Get(fdObj, infrav1.FailureDomainSafeForDeletionCondition)
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
				cond := v1beta1conditions.Get(fdObj, infrav1.FailureDomainSafeForDeletionCondition)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(corev1.ConditionFalse))
				g.Expect(cond.Severity).To(Equal(capiv1.ConditionSeverityError))
				g.Expect(cond.Reason).To(Equal(infrav1.FailureDomainInUseReason))
			})
		})
	})
}
