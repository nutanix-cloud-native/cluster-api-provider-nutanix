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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
)

var _ = Describe("AHVMetroZone CEL Validation", func() {
	Context("Zones minimum items of 2 validation", func() {
		It("should accept 2 zones", func() {
			metroZone := defaultMetroZoneObject()

			err := k8sClient.Create(ctx, metroZone)
			Expect(err).NotTo(HaveOccurred())
			_ = k8sClient.Delete(ctx, metroZone)
		})

		It("should reject less than 2 zones", func() {
			metroZone := defaultMetroZoneObject()
			metroZone.Spec.Zones = metroZone.Spec.Zones[:1]

			err := k8sClient.Create(ctx, metroZone)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("spec.zones in body should have at least 2 items"))
		})
	})

	Context("Zone name uniqueness validation", func() {
		It("should accept zone with unique name ", func() {
			metroZone := defaultMetroZoneObject()
			zone3 := metroZone.Spec.Zones[0].DeepCopy()
			zone3.Name = "zone-3"
			metroZone.Spec.Zones = append(metroZone.Spec.Zones, *zone3)

			err := k8sClient.Create(ctx, metroZone)
			Expect(err).NotTo(HaveOccurred())
			_ = k8sClient.Delete(ctx, metroZone)
		})

		It("should reject zone with duplicate name", func() {
			metroZone := defaultMetroZoneObject()
			zone3 := metroZone.Spec.Zones[0].DeepCopy()
			metroZone.Spec.Zones = append(metroZone.Spec.Zones, *zone3)

			err := k8sClient.Create(ctx, metroZone)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("spec.zones[2]: Duplicate value"))
		})
	})

	Context("Placement strategy validation", func() {
		It("should accept Preferred strategy with preferredZone", func() {
			metroZone := defaultMetroZoneObject()
			metroZone.Spec.Placement.PreferredZone = ptr.To("zone-2")

			err := k8sClient.Create(ctx, metroZone)
			Expect(err).NotTo(HaveOccurred())
			_ = k8sClient.Delete(ctx, metroZone)
		})

		It("should accept Random strategy without preferredZone", func() {
			metroZone := defaultMetroZoneObject()
			metroZone.Spec.Placement.Strategy = "Random"
			metroZone.Spec.Placement.PreferredZone = nil

			err := k8sClient.Create(ctx, metroZone)
			Expect(err).NotTo(HaveOccurred())
			_ = k8sClient.Delete(ctx, metroZone)
		})

		It("should reject Preferred strategy without preferredZone", func() {
			metroZone := defaultMetroZoneObject()
			metroZone.Spec.Placement.PreferredZone = nil

			err := k8sClient.Create(ctx, metroZone)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("preferredZone is required for Preferred strategy"))
		})

		It("should reject unknown strategy", func() {
			metroZone := defaultMetroZoneObject()
			metroZone.Spec.Placement.Strategy = "unknown"

			err := k8sClient.Create(ctx, metroZone)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("Unsupported value: \"unknown\": supported values: \"Preferred\", \"Random\""))
		})
	})
})

func defaultMetroZoneObject() *infrav1.AHVMetroZone {
	return &infrav1.AHVMetroZone{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default-zone",
			Namespace: "default",
		},
		Spec: infrav1.AHVMetroZoneSpec{
			Zones: []infrav1.MetroZone{
				{
					Name: "zone-1",
					PrismElement: infrav1.NutanixResourceIdentifier{
						Type: infrav1.NutanixIdentifierName,
						Name: ptr.To("pe-1"),
					},
					Subnets: []infrav1.NutanixResourceIdentifier{{
						Type: infrav1.NutanixIdentifierName,
						Name: ptr.To("metro-subnet"),
					}},
				},
				{
					Name: "zone-2",
					PrismElement: infrav1.NutanixResourceIdentifier{
						Type: infrav1.NutanixIdentifierName,
						Name: ptr.To("pe-2"),
					},
					Subnets: []infrav1.NutanixResourceIdentifier{{
						Type: infrav1.NutanixIdentifierName,
						Name: ptr.To("metro-subnet"),
					}},
				},
			},
			Placement: infrav1.VMPlacement{
				Strategy:      infrav1.PreferredStrategy,
				PreferredZone: ptr.To("zone-1"),
			},
		},
	}
}
