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

func TestAHVMetroZoneReconciler(t *testing.T) {
	g := NewWithT(t)

	_ = Describe("AHVMetroZoneReconciler", func() {
		var (
			ctx          context.Context
			metroZoneObj *infrav1.AHVMetroZone
			machineObj   *capiv1.Machine
			reconciler   *AHVMetroZoneReconciler
		)

		BeforeEach(func() {
			ctx = context.Background()

			metroZoneObj = &infrav1.AHVMetroZone{
				TypeMeta: metav1.TypeMeta{
					Kind:       infrav1.AHVMetroZoneKind,
					APIVersion: infrav1.GroupVersion.String(),
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "metro-zone-test",
					Namespace: corev1.NamespaceDefault,
				},
				Spec: infrav1.AHVMetroZoneSpec{
					Zones: []infrav1.MetroZone{
						{
							Name: "zone-1",
							PrismElement: infrav1.NutanixResourceIdentifier{
								Type: infrav1.NutanixIdentifierName,
								Name: ptr.To("pe-1"),
							},
							Subnets: []infrav1.NutanixResourceIdentifier{
								{Type: infrav1.NutanixIdentifierName, Name: ptr.To("metro-subnet")},
							},
						},
						{
							Name: "zone-2",
							PrismElement: infrav1.NutanixResourceIdentifier{
								Type: infrav1.NutanixIdentifierName,
								Name: ptr.To("pe-2"),
							},
							Subnets: []infrav1.NutanixResourceIdentifier{
								{Type: infrav1.NutanixIdentifierName, Name: ptr.To("metro-subnet")},
							},
						},
					},
					Placement: infrav1.VMPlacement{
						Strategy: infrav1.RandomStrategy,
					},
				},
			}

			reconciler = &AHVMetroZoneReconciler{
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

		Context("Reconcile an AHVMetroZone", func() {
			It("should allow to delete the AHVMetroZone object if no machines use it", func() {
				// Expect calling reconciler.reconcileDelete() without error.
				machineObj.Spec.FailureDomain = nil
				g.Expect(reconciler.reconcileDelete(ctx, metroZoneObj, []capiv1.Machine{*machineObj})).NotTo(HaveOccurred())
				cond := v1beta1conditions.Get(metroZoneObj, infrav1.MetroZoneSafeForDeletionCondition)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(corev1.ConditionTrue))
			})

			It("should not allow to delete the AHVMetroZone object if there are machines use it", func() {
				// Set failureDomain reference to the AHVMetroZone to Machine spec.
				machineObj.Spec.FailureDomain = ptr.To(fmt.Sprintf("%s%s", metroZoneFailureDomainPrefix, metroZoneObj.Name))

				// Expect calling reconciler.reconcileDelete() returns error.
				g.Expect(reconciler.reconcileDelete(ctx, metroZoneObj, []capiv1.Machine{*machineObj})).To(HaveOccurred())
				cond := v1beta1conditions.Get(metroZoneObj, infrav1.MetroZoneSafeForDeletionCondition)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(corev1.ConditionFalse))
				g.Expect(cond.Severity).To(Equal(capiv1.ConditionSeverityError))
				g.Expect(cond.Reason).To(Equal(infrav1.MetroZoneInUseReason))
				g.Expect(cond.Message).To(ContainSubstring("The AHVMetroZone object is used by machines:"))
			})

			It("should allow preferredZone references to configured zone name for the Preferred placement strategy", func() {
				metroZoneObj.Spec.Placement.Strategy = infrav1.PreferredStrategy
				metroZoneObj.Spec.Placement.PreferredZone = ptr.To(metroZoneObj.Spec.Zones[0].Name)

				// Expect calling reconciler.reconcileNormal() without error.
				g.Expect(reconciler.reconcileNormal(ctx, metroZoneObj)).To(Succeed())
			})

			It("should not allow preferredZone blank for the Preferred placement strategy", func() {
				metroZoneObj.Spec.Placement.Strategy = infrav1.PreferredStrategy
				metroZoneObj.Spec.Placement.PreferredZone = nil

				// Expect calling reconciler.reconcileNormal() failed
				err := reconciler.reconcileNormal(ctx, metroZoneObj)
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("preferredZone is required for Preferred strategy"))
			})

			It("should not allow preferredZone references to unknown zone name for the Preferred placement strategy", func() {
				metroZoneObj.Spec.Placement.Strategy = infrav1.PreferredStrategy
				metroZoneObj.Spec.Placement.PreferredZone = ptr.To("unknown")

				// Expect creation object reconciling failed
				err := reconciler.reconcileNormal(ctx, metroZoneObj)
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("preferredZone \"unknown\" is invalid."))
			})
		})
	})
}
