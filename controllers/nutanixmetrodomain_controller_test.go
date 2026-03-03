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
	"testing"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta1"                              //nolint:staticcheck // suppress complaining on Deprecated package
	v1beta1conditions "sigs.k8s.io/cluster-api/util/deprecated/v1beta1/conditions" //nolint:staticcheck // suppress complaining on Deprecated package
)

func TestNutanixMetroDomainReconciler(t *testing.T) {
	g := NewWithT(t)

	_ = Describe("NutanixMetroDomainReconciler", func() {
		var (
			ctx            context.Context
			metroDomainObj *infrav1.NutanixMetroDomain
			nclObj         *infrav1.NutanixCluster
			reconciler     *NutanixMetroDomainReconciler
		)

		BeforeEach(func() {
			ctx = context.Background()

			metroDomainObj = &infrav1.NutanixMetroDomain{
				TypeMeta: metav1.TypeMeta{
					Kind:       infrav1.NutanixMetroDomainKind,
					APIVersion: infrav1.GroupVersion.String(),
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "metro-domain-test",
					Namespace: corev1.NamespaceDefault,
				},
				Spec: infrav1.NutanixMetroDomainSpec{
					Sites: []infrav1.MetroSite{
						{
							Name:          "metro-site-1",
							FailureDomain: corev1.LocalObjectReference{Name: "fd-1"},
							Category:      infrav1.NutanixCategoryIdentifier{Key: "metro-category", Value: "pe-1"},
							RecoveryPlan:  &infrav1.NutanixResourceIdentifier{Type: infrav1.NutanixIdentifierName, Name: ptr.To("metro-rp-pe1")},
						},
						{
							Name:          "metro-site-2",
							FailureDomain: corev1.LocalObjectReference{Name: "fd-2"},
							Category:      infrav1.NutanixCategoryIdentifier{Key: "metro-category", Value: "pe-2"},
							RecoveryPlan:  &infrav1.NutanixResourceIdentifier{Type: infrav1.NutanixIdentifierName, Name: ptr.To("metro-rp-pe2")},
						},
					},
					ProtectionPolicy: &infrav1.NutanixResourceIdentifier{Type: infrav1.NutanixIdentifierName, Name: ptr.To("metro-pp")},
				},
			}

			reconciler = &NutanixMetroDomainReconciler{
				Client: k8sClient,
				Scheme: runtime.NewScheme(),
			}

			nclObj = &infrav1.NutanixCluster{
				TypeMeta: metav1.TypeMeta{
					Kind:       "NutanixCluster",
					APIVersion: infrav1.GroupVersion.String(),
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "ncl-test",
					Namespace: corev1.NamespaceDefault,
				},
				Spec: infrav1.NutanixClusterSpec{
					MetroDomains: []corev1.LocalObjectReference{{Name: "metro-domain-test"}},
				},
			}
		})

		Context("Reconcile a NutanixMetroDomain", func() {
			It("should allow to delete the NutanixMetroDomain object if no NutanixClusters use it", func() {
				// Expect calling reconciler.reconcileDelete() without error.
				nclObj.Spec.MetroDomains = []corev1.LocalObjectReference{}
				g.Expect(reconciler.reconcileDelete(ctx, metroDomainObj, []infrav1.NutanixCluster{*nclObj})).NotTo(HaveOccurred())
				cond := v1beta1conditions.Get(metroDomainObj, infrav1.MetroDomainSafeForDeletionCondition)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(corev1.ConditionTrue))
			})

			It("should not allow to delete the NutanixMetroDomain object if there are NutanixClusters use it", func() {
				// Set metroDomains reference to the NutanixMetroDomain to NutanixCluster spec.
				nclObj.Spec.MetroDomains = []corev1.LocalObjectReference{{Name: metroDomainObj.Name}}

				// Expect calling reconciler.reconcileDelete() returns error.
				g.Expect(reconciler.reconcileDelete(ctx, metroDomainObj, []infrav1.NutanixCluster{*nclObj})).To(HaveOccurred())
				cond := v1beta1conditions.Get(metroDomainObj, infrav1.MetroDomainSafeForDeletionCondition)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(corev1.ConditionFalse))
				g.Expect(cond.Severity).To(Equal(capiv1.ConditionSeverityError))
				g.Expect(cond.Reason).To(Equal(infrav1.MetroDomainInUseReason))
				g.Expect(cond.Message).To(ContainSubstring("The NutanixMetroDomain object is used by NutanixCusters"))
			})
		})
	})
}
