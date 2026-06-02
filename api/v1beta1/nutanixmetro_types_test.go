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
)

var _ = Describe("NutanixMetro CEL Validation", func() {
	Context("failureDomains items of 2 validation", func() {
		It("should accept 2 failureDomains", func() {
			metroZone := defaultNutanixMetroObject()

			err := k8sClient.Create(ctx, metroZone)
			Expect(err).NotTo(HaveOccurred())
			_ = k8sClient.Delete(ctx, metroZone)
		})

		It("should reject less than 2 failureDomains", func() {
			metroZone := defaultNutanixMetroObject()
			metroZone.Spec.FailureDomains = metroZone.Spec.FailureDomains[:1]

			err := k8sClient.Create(ctx, metroZone)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("spec.failureDomains in body should have at least 2 items"))
		})

		It("should reject more than 2 failureDomains", func() {
			metroZone := defaultNutanixMetroObject()
			metroZone.Spec.FailureDomains = append(metroZone.Spec.FailureDomains, corev1.LocalObjectReference{Name: "fd-3"})

			err := k8sClient.Create(ctx, metroZone)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("spec.failureDomains: Too many: 3: must have at most 2 items"))
		})
	})

	Context("failureDomains name uniqueness validation", func() {
		It("should reject failureDomains with duplicate name", func() {
			metroZone := defaultNutanixMetroObject()
			metroZone.Spec.FailureDomains[1].Name = metroZone.Spec.FailureDomains[0].Name

			err := k8sClient.Create(ctx, metroZone)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("spec.failureDomains[1]: Duplicate value"))
		})
	})
})

func defaultNutanixMetroObject() *infrav1.NutanixMetro {
	return &infrav1.NutanixMetro{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default-zone",
			Namespace: "default",
		},
		Spec: infrav1.NutanixMetroSpec{
			FailureDomains: []corev1.LocalObjectReference{
				{Name: "fd-1"},
				{Name: "fd-2"},
			},
		},
	}
}
