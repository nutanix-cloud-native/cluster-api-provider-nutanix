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

var _ = Describe("NutanixMetroVMPlacement CEL Validation", func() {
	Context("failureDomains minimum items of 2 validation", func() {
		It("should accept 2 zones", func() {
			vmPlacement := defaultMetroVMPlacementObject()

			err := k8sClient.Create(ctx, vmPlacement)
			Expect(err).NotTo(HaveOccurred())
			_ = k8sClient.Delete(ctx, vmPlacement)
		})

		It("should reject less than 2 zones", func() {
			vmPlacement := defaultMetroVMPlacementObject()
			vmPlacement.Spec.FailureDomains = vmPlacement.Spec.FailureDomains[:1]

			err := k8sClient.Create(ctx, vmPlacement)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("spec.failureDomains in body should have at least 2 items"))
		})

		It("should reject more than 2 zones", func() {
			vmPlacement := defaultMetroVMPlacementObject()
			fd3 := corev1.LocalObjectReference{Name: "fd-3"}
			vmPlacement.Spec.FailureDomains = append(vmPlacement.Spec.FailureDomains, fd3)

			err := k8sClient.Create(ctx, vmPlacement)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("spec.failureDomains: Too many: 3: must have at most 2 items"))
		})
	})

	Context("FailureDomain name uniqueness validation", func() {
		It("should reject failureDomain with duplicate name", func() {
			vmPlacement := defaultMetroVMPlacementObject()
			vmPlacement.Spec.FailureDomains[1].Name = vmPlacement.Spec.FailureDomains[0].Name

			err := k8sClient.Create(ctx, vmPlacement)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("spec.failureDomains[1]: Duplicate value"))
		})
	})

	Context("Placement strategy validation", func() {
		It("should accept Preferred strategy with preferredFailureDomain", func() {
			vmPlacement := defaultMetroVMPlacementObject()
			vmPlacement.Spec.PreferredFailureDomain = &vmPlacement.Spec.FailureDomains[0].Name

			err := k8sClient.Create(ctx, vmPlacement)
			Expect(err).NotTo(HaveOccurred())
			_ = k8sClient.Delete(ctx, vmPlacement)
		})

		It("should accept Random strategy without preferredFailureDomain", func() {
			vmPlacement := defaultMetroVMPlacementObject()
			vmPlacement.Spec.PlacementStrategy = "Random"
			vmPlacement.Spec.PreferredFailureDomain = nil

			err := k8sClient.Create(ctx, vmPlacement)
			Expect(err).NotTo(HaveOccurred())
			_ = k8sClient.Delete(ctx, vmPlacement)
		})

		It("should reject Preferred strategy without preferredFailureDomain", func() {
			vmPlacement := defaultMetroVMPlacementObject()
			vmPlacement.Spec.PreferredFailureDomain = nil

			err := k8sClient.Create(ctx, vmPlacement)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("preferredFailureDomain is required for Preferred placementStrategy, and it must be referenced in spec.failureDomains."))
		})

		It("should reject Preferred strategy with preferredFailureDomain not in spec.failureDomains", func() {
			vmPlacement := defaultMetroVMPlacementObject()
			vmPlacement.Spec.PreferredFailureDomain = ptr.To("unknown")

			err := k8sClient.Create(ctx, vmPlacement)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("preferredFailureDomain is required for Preferred placementStrategy, and it must be referenced in spec.failureDomains."))
		})

		It("should reject unknown strategy", func() {
			vmPlacement := defaultMetroVMPlacementObject()
			vmPlacement.Spec.PlacementStrategy = "unknown"

			err := k8sClient.Create(ctx, vmPlacement)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("Unsupported value: \"unknown\": supported values: \"Preferred\", \"Random\""))
		})
	})
})

func defaultMetroVMPlacementObject() *infrav1.NutanixMetroVMPlacement {
	return &infrav1.NutanixMetroVMPlacement{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default-placement",
			Namespace: "default",
		},
		Spec: infrav1.NutanixMetroVMPlacementSpec{
			FailureDomains: []corev1.LocalObjectReference{
				{Name: "fd-1"},
				{Name: "fd-2"},
			},
			PlacementStrategy:      infrav1.PreferredStrategy,
			PreferredFailureDomain: ptr.To("fd-1"),
		},
	}
}
