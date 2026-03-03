/*
Copyright 2026 Nutanix

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

package v1beta1_test

import (
	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

var _ = Describe("NutanixMetroDomain CEL Validation", func() {
	Context("sites minimum items of 2 validation", func() {
		It("should accept 2 sites", func() {
			metroDomain := defaultMetroDomainObject()

			err := k8sClient.Create(ctx, metroDomain)
			Expect(err).NotTo(HaveOccurred())
			_ = k8sClient.Delete(ctx, metroDomain)
		})

		It("should reject less than 2 sites", func() {
			metroDomain := defaultMetroDomainObject()
			metroDomain.Spec.Sites = metroDomain.Spec.Sites[:1]

			err := k8sClient.Create(ctx, metroDomain)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("spec.sites in body should have at least 2 items"))
		})

		It("should reject more than 2 sites", func() {
			metroDomain := defaultMetroDomainObject()
			site3 := infrav1.MetroSite{
				FailureDomain: corev1.LocalObjectReference{Name: "fd-3"},
				Category:      infrav1.NutanixCategoryIdentifier{Key: "metro-category", Value: "pe-3"},
				RecoveryPlan:  &infrav1.NutanixResourceIdentifier{Type: infrav1.NutanixIdentifierName, Name: ptr.To("rp-pe-3")},
			}
			metroDomain.Spec.Sites = append(metroDomain.Spec.Sites, site3)

			err := k8sClient.Create(ctx, metroDomain)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("spec.sites: Too many: 3: must have at most 2 items"))
		})
	})

	Context("sites uniqueness validation", func() {
		It("should reject sites with duplicate name", func() {
			metroDomain := defaultMetroDomainObject()
			metroDomain.Spec.Sites[1].Name = metroDomain.Spec.Sites[0].Name

			err := k8sClient.Create(ctx, metroDomain)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("spec.sites[1]: Duplicate value"))
		})

		It("should reject sites with duplicate failureDomain name", func() {
			metroDomain := defaultMetroDomainObject()
			metroDomain.Spec.Sites[1].FailureDomain.Name = metroDomain.Spec.Sites[0].FailureDomain.Name

			err := k8sClient.Create(ctx, metroDomain)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("each site's failureDomain.name must be unique"))
		})
	})

	Context("sites category validation", func() {
		It("should reject site with category key not set", func() {
			metroDomain := defaultMetroDomainObject()
			metroDomain.Spec.Sites[0].Category.Key = ""

			err := k8sClient.Create(ctx, metroDomain)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("Metro site's category key and value must set"))
		})

		It("should reject site with category value not set", func() {
			metroDomain := defaultMetroDomainObject()
			metroDomain.Spec.Sites[0].Category.Value = ""

			err := k8sClient.Create(ctx, metroDomain)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("Metro site's category key and value must set"))
		})
	})

	Context("protectionPolicy and recoveryPlans set or non-set simultaniously validation", func() {
		It("should reject protectionPolicy set but one of recoveryPlans not set", func() {
			metroDomain := defaultMetroDomainObject()
			metroDomain.Spec.Sites[0].RecoveryPlan = nil

			err := k8sClient.Create(ctx, metroDomain)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("protectionPolicy and each site's recoveryPlan must set or non-set simultaniously"))
		})

		It("should reject protectionPolicy non-set but one of recoveryPlans set", func() {
			metroDomain := defaultMetroDomainObject()
			metroDomain.Spec.ProtectionPolicy = nil

			err := k8sClient.Create(ctx, metroDomain)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("protectionPolicy and each site's recoveryPlan must set or non-set simultaniously"))
		})

		It("should accept protectionPolicy and all sites' recoveryPlan are set", func() {
			metroDomain := defaultMetroDomainObject()

			err := k8sClient.Create(ctx, metroDomain)
			Expect(err).NotTo(HaveOccurred())
			_ = k8sClient.Delete(ctx, metroDomain)
		})

		It("should accept protectionPolicy and all sites' recoveryPlan are not set", func() {
			metroDomain := defaultMetroDomainObject()
			metroDomain.Spec.ProtectionPolicy = nil
			metroDomain.Spec.Sites[0].RecoveryPlan = nil
			metroDomain.Spec.Sites[1].RecoveryPlan = nil

			err := k8sClient.Create(ctx, metroDomain)
			Expect(err).NotTo(HaveOccurred())
			_ = k8sClient.Delete(ctx, metroDomain)
		})
	})

})

func defaultMetroDomainObject() *infrav1.NutanixMetroDomain {
	return &infrav1.NutanixMetroDomain{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default-metrodomain",
			Namespace: "default",
		},
		Spec: infrav1.NutanixMetroDomainSpec{
			Sites: []infrav1.MetroSite{
				{
					Name:          "site-1",
					FailureDomain: corev1.LocalObjectReference{Name: "fd-1"},
					Category:      infrav1.NutanixCategoryIdentifier{Key: "metro-category", Value: "pe-1"},
					RecoveryPlan:  &infrav1.NutanixResourceIdentifier{Type: infrav1.NutanixIdentifierName, Name: ptr.To("rp-pe-1")},
				},
				{
					Name:          "site-2",
					FailureDomain: corev1.LocalObjectReference{Name: "fd-2"},
					Category:      infrav1.NutanixCategoryIdentifier{Key: "metro-category", Value: "pe-2"},
					RecoveryPlan:  &infrav1.NutanixResourceIdentifier{Type: infrav1.NutanixIdentifierName, Name: ptr.To("rp-pe-2")},
				},
			},
			ProtectionPolicy: &infrav1.NutanixResourceIdentifier{Type: infrav1.NutanixIdentifierName, Name: ptr.To("pp")},
		},
	}
}
