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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
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

	Context("protectionPolicUUID, categoryUUIDs and recoveryPlanUUIDs validation", func() {
		It("should allow uuid fields all not set", func() {
			vhaDomain := defaultvhaDomainObject()

			err := k8sClient.Create(ctx, vhaDomain)
			Expect(err).NotTo(HaveOccurred())
			_ = k8sClient.Delete(ctx, vhaDomain)
		})

		It("should accept all uuid fields set", func() {
			vhaDomain := defaultvhaDomainObject()
			vhaDomain.Spec.ProtectionPolicyUUID = ptr.To("57ea2690-d46d-4cbe-79d9-b19a32249d7a")
			vhaDomain.Spec.CategoryUUIDs = []string{"57ea2690-d46d-4cbe-79d9-b19a32249d7a", "268bf6b0-52f8-4490-927f-a937bf5ae5f0"}
			vhaDomain.Spec.RecoveryPlanUUIDs = []string{"57ea2690-d46d-4cbe-79d9-b19a32249d7a", "268bf6b0-52f8-4490-927f-a937bf5ae5f0"}

			err := k8sClient.Create(ctx, vhaDomain)
			Expect(err).NotTo(HaveOccurred())
			_ = k8sClient.Delete(ctx, vhaDomain)
		})

		It("should reject protectionPolicUUID set but other uuid fields not set", func() {
			vhaDomain := defaultvhaDomainObject()
			vhaDomain.Spec.ProtectionPolicyUUID = ptr.To("57ea2690-d46d-4cbe-79d9-b19a32249d7a")

			err := k8sClient.Create(ctx, vhaDomain)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("protectionPolicUUID, categoryUUIDs and recoveryPlanUUIDs must be either all set or all unset."))
		})

		It("should reject categoryUUIDs set but other uuid fields not set", func() {
			vhaDomain := defaultvhaDomainObject()
			vhaDomain.Spec.CategoryUUIDs = []string{"57ea2690-d46d-4cbe-79d9-b19a32249d7a", "268bf6b0-52f8-4490-927f-a937bf5ae5f0"}

			err := k8sClient.Create(ctx, vhaDomain)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("protectionPolicUUID, categoryUUIDs and recoveryPlanUUIDs must be either all set or all unset."))
		})

		It("should reject recoveryPlanUUIDs set but other uuid fields not set", func() {
			vhaDomain := defaultvhaDomainObject()
			vhaDomain.Spec.RecoveryPlanUUIDs = []string{"57ea2690-d46d-4cbe-79d9-b19a32249d7a", "268bf6b0-52f8-4490-927f-a937bf5ae5f0"}

			err := k8sClient.Create(ctx, vhaDomain)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("protectionPolicUUID, categoryUUIDs and recoveryPlanUUIDs must be either all set or all unset."))
		})

		It("should reject categoryUUIDs set more than 2 items", func() {
			vhaDomain := defaultvhaDomainObject()
			vhaDomain.Spec.ProtectionPolicyUUID = ptr.To("57ea2690-d46d-4cbe-79d9-b19a32249d7a")
			vhaDomain.Spec.CategoryUUIDs = []string{"57ea2690-d46d-4cbe-79d9-b19a32249d7a", "268bf6b0-52f8-4490-927f-a937bf5ae5f0", "000621c4-63cb-ecfa-51d1-7cc25586d099"}
			vhaDomain.Spec.RecoveryPlanUUIDs = []string{"57ea2690-d46d-4cbe-79d9-b19a32249d7a", "268bf6b0-52f8-4490-927f-a937bf5ae5f0"}

			err := k8sClient.Create(ctx, vhaDomain)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("spec.categoryUUIDs: Too many: 3: must have at most 2 items"))
		})

		It("should reject recoveryPlanUUIDs set more than 2 items", func() {
			vhaDomain := defaultvhaDomainObject()
			vhaDomain.Spec.ProtectionPolicyUUID = ptr.To("57ea2690-d46d-4cbe-79d9-b19a32249d7a")
			vhaDomain.Spec.CategoryUUIDs = []string{"57ea2690-d46d-4cbe-79d9-b19a32249d7a", "268bf6b0-52f8-4490-927f-a937bf5ae5f0"}
			vhaDomain.Spec.RecoveryPlanUUIDs = []string{"57ea2690-d46d-4cbe-79d9-b19a32249d7a", "268bf6b0-52f8-4490-927f-a937bf5ae5f0", "000621c4-63cb-ecfa-51d1-7cc25586d099"}

			err := k8sClient.Create(ctx, vhaDomain)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("spec.recoveryPlanUUIDs: Too many: 3: must have at most 2 items"))
		})
	})
})

func defaultvhaDomainObject() *infrav1.NutanixVirtualHADomain {
	return &infrav1.NutanixVirtualHADomain{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "metro-0",
			Namespace: "default",
		},
		Spec: infrav1.NutanixVirtualHADomainSpec{
			MetroRef: corev1.LocalObjectReference{
				Name: "metro-0",
			},
		},
	}
}
