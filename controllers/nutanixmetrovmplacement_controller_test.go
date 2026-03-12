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
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta1"                              //nolint:staticcheck // suppress complaining on Deprecated package
	v1beta1conditions "sigs.k8s.io/cluster-api/util/deprecated/v1beta1/conditions" //nolint:staticcheck // suppress complaining on Deprecated package

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
)

func TestNutanixMetroVMPlacementReconciler(t *testing.T) {
	g := NewWithT(t)

	_ = Describe("NutanixMetroVMPlacementReconciler", func() {
		var (
			ctx          context.Context
			metroVMPlacementObj *infrav1.NutanixMetroVMPlacement
			machineObj   *capiv1.Machine
			reconciler   *NutanixMetroVMPlacementReconciler
		)

		BeforeEach(func() {
			ctx = context.Background()

			metroVMPlacementObj = &infrav1.NutanixMetroVMPlacement{
				TypeMeta: metav1.TypeMeta{
					Kind:       infrav1.NutanixMetroVMPlacementKind,
					APIVersion: infrav1.GroupVersion.String(),
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "metro-vmplacement-test",
					Namespace: corev1.NamespaceDefault,
				},
				Spec: infrav1.NutanixMetroVMPlacementSpec{
					FailureDomains: []corev1.LocalObjectReference{
						{Name: "fd-1"},
						{Name: "fd-2"},
					},
					PlacementStrategy: infrav1.RandomStrategy,
				},
			}

			reconciler = &NutanixMetroVMPlacementReconciler{
				Client: k8sClient,
				Scheme: runtime.NewScheme(),
			}

			machineObj = &capiv1.Machine{
				TypeMeta: metav1.TypeMeta{
					Kind:       "Machine",
					APIVersion: capiv1.GroupVersion.String(),
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "m-test",
					Namespace: corev1.NamespaceDefault,
				},
				Spec: capiv1.MachineSpec{
					ClusterName:       "metro-cl-test",
					Bootstrap:         capiv1.Bootstrap{},
					InfrastructureRef: corev1.ObjectReference{},
				},
			}
		})

		Context("Reconcile a NutanixMetroVMPlacement", func() {
			It("should allow to delete the NutanixMetroVMPlacement object if no machines use it", func() {
				// Expect calling reconciler.reconcileDelete() without error.
				machineObj.Spec.FailureDomain = nil
				g.Expect(reconciler.reconcileDelete(ctx, metroVMPlacementObj, []capiv1.Machine{*machineObj})).NotTo(HaveOccurred())
				cond := v1beta1conditions.Get(metroVMPlacementObj, infrav1.MetroVMPlacementSafeForDeletionCondition)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(corev1.ConditionTrue))
			})

			It("should not allow to delete the NutanixMetroVMPlacement object if there are machines use it", func() {
				// Set failureDomain reference to the NutanixMetroVMPlacement to Machine spec.
				machineObj.Spec.FailureDomain = ptr.To(fmt.Sprintf("%s%s", nutanixMetroFailureDomainPrefix, metroVMPlacementObj.Name))

				// Expect calling reconciler.reconcileDelete() returns error.
				g.Expect(reconciler.reconcileDelete(ctx, metroVMPlacementObj, []capiv1.Machine{*machineObj})).To(HaveOccurred())
				cond := v1beta1conditions.Get(metroVMPlacementObj, infrav1.MetroVMPlacementSafeForDeletionCondition)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(corev1.ConditionFalse))
				g.Expect(cond.Severity).To(Equal(capiv1.ConditionSeverityError))
				g.Expect(cond.Reason).To(Equal(infrav1.MetroVMPlacementInUseReason))
				g.Expect(cond.Message).To(ContainSubstring("The NutanixMetroVMPlacement object is used by machines"))
			})

			It("should allow preferredFailureDomain references to configured failureDomain name for the Preferred placement strategy", func() {
				metroVMPlacementObj.Spec.PlacementStrategy = infrav1.PreferredStrategy
				metroVMPlacementObj.Spec.PreferredFailureDomain = ptr.To(metroVMPlacementObj.Spec.FailureDomains[0].Name)

				// Expect calling reconciler.reconcileNormal() without error.
				g.Expect(reconciler.reconcileNormal(ctx, metroVMPlacementObj)).To(Succeed())
			})

			It("should not allow preferredFailureDomain blank for the Preferred placement strategy", func() {
				metroVMPlacementObj.Spec.PlacementStrategy = infrav1.PreferredStrategy
				metroVMPlacementObj.Spec.PreferredFailureDomain = nil

				// Expect calling reconciler.reconcileNormal() failed
				err := reconciler.reconcileNormal(ctx, metroVMPlacementObj)
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("preferredFailureDomain is required for Preferred strategy"))
			})

			It("should not allow preferredFailureDomain references to unknown failureDomain name for the Preferred placement strategy", func() {
				metroVMPlacementObj.Spec.PlacementStrategy = infrav1.PreferredStrategy
				metroVMPlacementObj.Spec.PreferredFailureDomain = ptr.To("unknown")

				// Expect creation object reconciling failed
				err := reconciler.reconcileNormal(ctx, metroVMPlacementObj)
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("preferredFailureDomain \"unknown\" is invalid."))
			})
		})
	})
}
