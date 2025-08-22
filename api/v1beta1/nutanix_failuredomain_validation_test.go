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

package v1beta1_test

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
)

var (
	k8sClient client.Client
	ctx       = context.Background()
)

var _ = Describe("NutanixFailureDomain CEL Validation", func() {
	Context("Subnet uniqueness validation", func() {
		It("should accept NutanixFailureDomain with unique subnets", func() {
			subnet1 := infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr("550e8400-e29b-41d4-a716-446655440000"),
			}
			subnet2 := infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr("123e4567-e89b-12d3-a456-426614174000"),
			}
			nfd := &infrav1.NutanixFailureDomain{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "nfd-unique-subnets",
					Namespace: "default",
				},
				Spec: infrav1.NutanixFailureDomainSpec{
					PrismElementCluster: subnet1,
					Subnets:             []infrav1.NutanixResourceIdentifier{subnet1, subnet2},
				},
			}
			err := k8sClient.Create(ctx, nfd)
			Expect(err).NotTo(HaveOccurred())
			_ = k8sClient.Delete(ctx, nfd)
		})

		It("should reject NutanixFailureDomain with duplicate subnets", func() {
			subnet := infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr("550e8400-e29b-41d4-a716-446655440000"),
			}
			nfd := &infrav1.NutanixFailureDomain{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "nfd-duplicate-subnets",
					Namespace: "default",
				},
				Spec: infrav1.NutanixFailureDomainSpec{
					PrismElementCluster: subnet,
					Subnets:             []infrav1.NutanixResourceIdentifier{subnet, subnet},
				},
			}
			err := k8sClient.Create(ctx, nfd)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("unique"))
		})
	})

	Context("Subnet min/max items validation", func() {
		It("should reject NutanixFailureDomain with zero subnets", func() {
			cluster := infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr("550e8400-e29b-41d4-a716-446655440000"),
			}
			nfd := &infrav1.NutanixFailureDomain{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "nfd-no-subnets",
					Namespace: "default",
				},
				Spec: infrav1.NutanixFailureDomainSpec{
					PrismElementCluster: cluster,
					Subnets:             []infrav1.NutanixResourceIdentifier{},
				},
			}
			err := k8sClient.Create(ctx, nfd)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("MinItems"))
		})

		It("should reject NutanixFailureDomain with more than 32 subnets", func() {
			cluster := infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr("550e8400-e29b-41d4-a716-446655440000"),
			}
			subnets := make([]infrav1.NutanixResourceIdentifier, 33)
			for i := range subnets {
				uuid := fmt.Sprintf("550e8400-e29b-41d4-a716-44665544%04d", i)
				subnets[i] = infrav1.NutanixResourceIdentifier{
					Type: infrav1.NutanixIdentifierUUID,
					UUID: &uuid,
				}
			}
			nfd := &infrav1.NutanixFailureDomain{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "nfd-too-many-subnets",
					Namespace: "default",
				},
				Spec: infrav1.NutanixFailureDomainSpec{
					PrismElementCluster: cluster,
					Subnets:             subnets,
				},
			}
			err := k8sClient.Create(ctx, nfd)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("MaxItems"))
		})
	})

	Context("Immutability validation", func() {
		It("should reject update to PrismElementCluster", func() {
			cluster := infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr("550e8400-e29b-41d4-a716-446655440000"),
			}
			nfd := &infrav1.NutanixFailureDomain{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "nfd-immutable-cluster",
					Namespace: "default",
				},
				Spec: infrav1.NutanixFailureDomainSpec{
					PrismElementCluster: cluster,
					Subnets:             []infrav1.NutanixResourceIdentifier{cluster},
				},
			}
			Expect(k8sClient.Create(ctx, nfd)).To(Succeed())

			// Try to update PrismElementCluster
			patch := client.MergeFrom(nfd.DeepCopy())
			nfd.Spec.PrismElementCluster.UUID = ptr("123e4567-e89b-12d3-a456-426614174000")
			err := k8sClient.Patch(ctx, nfd, patch)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("immutable"))

			_ = k8sClient.Delete(ctx, nfd)
		})

		It("should reject update to Subnets", func() {
			cluster := infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr("550e8400-e29b-41d4-a716-446655440000"),
			}
			subnet2 := infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr("123e4567-e89b-12d3-a456-426614174000"),
			}
			nfd := &infrav1.NutanixFailureDomain{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "nfd-immutable-subnets",
					Namespace: "default",
				},
				Spec: infrav1.NutanixFailureDomainSpec{
					PrismElementCluster: cluster,
					Subnets:             []infrav1.NutanixResourceIdentifier{cluster},
				},
			}
			Expect(k8sClient.Create(ctx, nfd)).To(Succeed())

			patch := client.MergeFrom(nfd.DeepCopy())
			nfd.Spec.Subnets = append(nfd.Spec.Subnets, subnet2)
			err := k8sClient.Patch(ctx, nfd, patch)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("immutable"))

			_ = k8sClient.Delete(ctx, nfd)
		})
	})
})

// ptr is a helper to get a pointer to a string
func ptr(s string) *string {
	return &s
}
