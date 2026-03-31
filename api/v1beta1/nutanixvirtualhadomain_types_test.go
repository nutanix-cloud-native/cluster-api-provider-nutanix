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

var _ = Describe("NutanixVirtualHADomain CEL Validation", func() {
	Context("metroRef validation", func() {
		It("should accept non-empty metroRef.name", func() {
			vhaDomain := defaultvhaDomainObject()

			err := k8sClient.Create(ctx, vhaDomain)
			Expect(err).NotTo(HaveOccurred())
			_ = k8sClient.Delete(ctx, vhaDomain)
		})

		It("should reject empty metroRef.name", func() {
			vhaDomain := defaultvhaDomainObject()
			vhaDomain.Spec.MetroRef.Name = ""

			err := k8sClient.Create(ctx, vhaDomain)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("metroRef.name must not be empty"))
		})
	})

	Context("protectionPolicy, categories and recoveryPlans validation", func() {
		It("should allow protectionPolicy, categories and recoveryPlans all not set", func() {
			vhaDomain := defaultvhaDomainObject()

			err := k8sClient.Create(ctx, vhaDomain)
			Expect(err).NotTo(HaveOccurred())
			_ = k8sClient.Delete(ctx, vhaDomain)
		})

		It("should accept protectionPolicy, categories and recoveryPlans all set", func() {
			vhaDomain := defaultvhaDomainObject()
			vhaDomain.Spec.ProtectionPolicy = &infrav1.NutanixResourceIdentifier{Type: infrav1.NutanixIdentifierName, Name: ptr.To("pp-metro0")}
			vhaDomain.Spec.Categories = []infrav1.NutanixCategoryIdentifier{
				{Key: "vha-mycluster-metro0", Value: "pe1-name"},
				{Key: "vha-mycluster-metro0", Value: "pe2-name"},
			}
			vhaDomain.Spec.RecoveryPlans = []infrav1.NutanixResourceIdentifier{
				{Type: infrav1.NutanixIdentifierName, Name: ptr.To("rp1-metro0")},
				{Type: infrav1.NutanixIdentifierName, Name: ptr.To("rp2-metro0")},
			}

			err := k8sClient.Create(ctx, vhaDomain)
			Expect(err).NotTo(HaveOccurred())
			_ = k8sClient.Delete(ctx, vhaDomain)
		})

		It("should reject protectionPolicy set but categories and recoveryPlans are not set", func() {
			vhaDomain := defaultvhaDomainObject()
			vhaDomain.Spec.ProtectionPolicy = &infrav1.NutanixResourceIdentifier{Type: infrav1.NutanixIdentifierName, Name: ptr.To("pp-metro0")}

			err := k8sClient.Create(ctx, vhaDomain)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("protectionPolicy, categories and recoveryPlans must be either all set or all unset."))
		})

		It("should reject categories set but protectionPolicy and recoveryPlans are not set", func() {
			vhaDomain := defaultvhaDomainObject()
			vhaDomain.Spec.Categories = []infrav1.NutanixCategoryIdentifier{
				{Key: "vha-mycluster-metro0", Value: "pe1-name"},
				{Key: "vha-mycluster-metro0", Value: "pe2-name"},
			}

			err := k8sClient.Create(ctx, vhaDomain)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("protectionPolicy, categories and recoveryPlans must be either all set or all unset."))
		})

		It("should reject recoveryPlans set but protectionPolicy and categories are not set", func() {
			vhaDomain := defaultvhaDomainObject()
			vhaDomain.Spec.RecoveryPlans = []infrav1.NutanixResourceIdentifier{
				{Type: infrav1.NutanixIdentifierName, Name: ptr.To("rp1-metro0")},
				{Type: infrav1.NutanixIdentifierName, Name: ptr.To("rp2-metro0")},
			}

			err := k8sClient.Create(ctx, vhaDomain)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("protectionPolicy, categories and recoveryPlans must be either all set or all unset."))
		})

		It("should reject categories set less than 2 items", func() {
			vhaDomain := defaultvhaDomainObject()
			vhaDomain.Spec.ProtectionPolicy = &infrav1.NutanixResourceIdentifier{Type: infrav1.NutanixIdentifierName, Name: ptr.To("pp-metro0")}
			vhaDomain.Spec.Categories = []infrav1.NutanixCategoryIdentifier{
				{Key: "vha-mycluster-metro0", Value: "pe1-name"},
			}
			vhaDomain.Spec.RecoveryPlans = []infrav1.NutanixResourceIdentifier{
				{Type: infrav1.NutanixIdentifierName, Name: ptr.To("rp1-metro0")},
				{Type: infrav1.NutanixIdentifierName, Name: ptr.To("rp2-metro0")},
			}

			err := k8sClient.Create(ctx, vhaDomain)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("When all set, categories and recoveryPlans must have two items."))
		})

		It("should reject categories set more than 2 items", func() {
			vhaDomain := defaultvhaDomainObject()
			vhaDomain.Spec.ProtectionPolicy = &infrav1.NutanixResourceIdentifier{Type: infrav1.NutanixIdentifierName, Name: ptr.To("pp-metro0")}
			vhaDomain.Spec.Categories = []infrav1.NutanixCategoryIdentifier{
				{Key: "vha-mycluster-metro0", Value: "pe1-name"},
				{Key: "vha-mycluster-metro0", Value: "pe2-name"},
				{Key: "vha-mycluster-metro0", Value: "pe3-name"},
			}
			vhaDomain.Spec.RecoveryPlans = []infrav1.NutanixResourceIdentifier{
				{Type: infrav1.NutanixIdentifierName, Name: ptr.To("rp1-metro0")},
				{Type: infrav1.NutanixIdentifierName, Name: ptr.To("rp2-metro0")},
			}

			err := k8sClient.Create(ctx, vhaDomain)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("spec.categories: Too many: 3: must have at most 2 items"))
		})

		It("should reject recoveryPlans set less than 2 items", func() {
			vhaDomain := defaultvhaDomainObject()
			vhaDomain.Spec.ProtectionPolicy = &infrav1.NutanixResourceIdentifier{Type: infrav1.NutanixIdentifierName, Name: ptr.To("pp-metro0")}
			vhaDomain.Spec.Categories = []infrav1.NutanixCategoryIdentifier{
				{Key: "vha-mycluster-metro0", Value: "pe1-name"},
				{Key: "vha-mycluster-metro0", Value: "pe2-name"},
			}
			vhaDomain.Spec.RecoveryPlans = []infrav1.NutanixResourceIdentifier{
				{Type: infrav1.NutanixIdentifierName, Name: ptr.To("rp1-metro0")},
			}

			err := k8sClient.Create(ctx, vhaDomain)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("When all set, categories and recoveryPlans must have two items."))
		})

		It("should reject recoveryPlans set more than 2 items", func() {
			vhaDomain := defaultvhaDomainObject()
			vhaDomain.Spec.ProtectionPolicy = &infrav1.NutanixResourceIdentifier{Type: infrav1.NutanixIdentifierName, Name: ptr.To("pp-metro0")}
			vhaDomain.Spec.Categories = []infrav1.NutanixCategoryIdentifier{
				{Key: "vha-mycluster-metro0", Value: "pe1-name"},
				{Key: "vha-mycluster-metro0", Value: "pe2-name"},
			}
			vhaDomain.Spec.RecoveryPlans = []infrav1.NutanixResourceIdentifier{
				{Type: infrav1.NutanixIdentifierName, Name: ptr.To("rp1-metro0")},
				{Type: infrav1.NutanixIdentifierName, Name: ptr.To("rp2-metro0")},
				{Type: infrav1.NutanixIdentifierName, Name: ptr.To("rp3-metro0")},
			}

			err := k8sClient.Create(ctx, vhaDomain)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("spec.recoveryPlans: Too many: 3: must have at most 2 items"))
		})
	})
})

func defaultvhaDomainObject() *infrav1.NutanixVirtualHADomain {
	return &infrav1.NutanixVirtualHADomain{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "metro0",
			Namespace: "default",
		},
		Spec: infrav1.NutanixVirtualHADomainSpec{
			MetroRef: corev1.LocalObjectReference{
				Name: "metro0",
			},
		},
	}
}
