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
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
)

var _ = Describe("NutanixFailureDomain CEL Validation", func() {
	Context("Subnet uniqueness validation", func() {
		It("should accept NutanixFailureDomain with unique subnets", func() {
			subnet1 := infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr.To("550e8400-e29b-41d4-a716-446655440000"),
			}
			subnet2 := infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr.To("123e4567-e89b-12d3-a456-426614174000"),
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
				UUID: ptr.To("550e8400-e29b-41d4-a716-446655440000"),
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
			Expect(err.Error()).To(ContainSubstring("each subnet must be unique"))
		})
	})

	Context("Subnet min/max items validation", func() {
		It("should reject NutanixFailureDomain with zero subnets", func() {
			cluster := infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr.To("550e8400-e29b-41d4-a716-446655440000"),
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
			Expect(err.Error()).To(ContainSubstring("spec.subnets: Invalid value: 0: spec.subnets in body should have at least 1 items"))
		})

		It("should reject NutanixFailureDomain with more than 32 subnets", func() {
			cluster := infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr.To("550e8400-e29b-41d4-a716-446655440000"),
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
			Expect(err.Error()).To(ContainSubstring("must have at most 32 items"))
		})
	})

	Context("Immutability validation", func() {
		It("should reject update to PrismElementCluster", func() {
			cluster := infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr.To("550e8400-e29b-41d4-a716-446655440000"),
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

			patch := client.MergeFrom(nfd.DeepCopy())
			nfd.Spec.PrismElementCluster.UUID = ptr.To("123e4567-e89b-12d3-a456-426614174000")
			err := k8sClient.Patch(ctx, nfd, patch)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("immutable"))

			_ = k8sClient.Delete(ctx, nfd)
		})

		It("should reject update to Subnets", func() {
			cluster := infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr.To("550e8400-e29b-41d4-a716-446655440000"),
			}
			subnet2 := infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr.To("123e4567-e89b-12d3-a456-426614174000"),
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

	Context("NutanixMachineSpec Subnets Uniqueness Validation", func() {
		It("should accept NutanixMachine with unique subnets", func() {
			subnet1 := infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr.To("550e8400-e29b-41d4-a716-446655440000"),
			}
			subnet2 := infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr.To("123e4567-e89b-12d3-a456-426614174000"),
			}
			nm := &infrav1.NutanixMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "machine-unique-subnets",
					Namespace: "default",
				},
				Spec: infrav1.NutanixMachineSpec{
					VCPUsPerSocket: 1,
					VCPUSockets:    1,
					MemorySize:     resource.MustParse("2Gi"),
					SystemDiskSize: resource.MustParse("20Gi"),
					Cluster: infrav1.NutanixResourceIdentifier{
						Type: infrav1.NutanixIdentifierUUID,
						UUID: ptr.To("550e8400-e29b-41d4-a716-446655440000"),
					},
					Image: &infrav1.NutanixResourceIdentifier{
						Type: infrav1.NutanixIdentifierUUID,
						UUID: ptr.To("550e8400-e29b-41d4-a716-446655440000"),
					},
					Subnets: []infrav1.NutanixResourceIdentifier{subnet1, subnet2},
				},
			}
			err := k8sClient.Create(ctx, nm)
			Expect(err).NotTo(HaveOccurred())
			_ = k8sClient.Delete(ctx, nm)
		})

		It("should reject NutanixMachine with duplicate subnets", func() {
			subnet := infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr.To("550e8400-e29b-41d4-a716-446655440000"),
			}
			nm := &infrav1.NutanixMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "machine-duplicate-subnets",
					Namespace: "default",
				},
				Spec: infrav1.NutanixMachineSpec{
					VCPUsPerSocket: 1,
					VCPUSockets:    1,
					MemorySize:     resource.MustParse("2Gi"),
					Cluster: infrav1.NutanixResourceIdentifier{
						Type: infrav1.NutanixIdentifierUUID,
						UUID: ptr.To("550e8400-e29b-41d4-a716-446655440000"),
					},
					Subnets: []infrav1.NutanixResourceIdentifier{subnet, subnet},
				},
			}
			err := k8sClient.Create(ctx, nm)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("each subnet must be unique"))
		})

		It("NutanixMachineSpec Subnet max items validation", func() {
			subnets := make([]infrav1.NutanixResourceIdentifier, 33)
			for i := range subnets {
				uuid := fmt.Sprintf("550e8400-e29b-41d4-a716-44665544%04d", i)
				subnets[i] = infrav1.NutanixResourceIdentifier{
					Type: infrav1.NutanixIdentifierUUID,
					UUID: &uuid,
				}
			}
			nm := &infrav1.NutanixMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "machine-duplicate-subnets",
					Namespace: "default",
				},
				Spec: infrav1.NutanixMachineSpec{
					VCPUsPerSocket: 1,
					VCPUSockets:    1,
					MemorySize:     resource.MustParse("2Gi"),
					Cluster: infrav1.NutanixResourceIdentifier{
						Type: infrav1.NutanixIdentifierUUID,
						UUID: ptr.To("550e8400-e29b-41d4-a716-446655440000"),
					},
					Subnets: subnets,
				},
			}
			err := k8sClient.Create(ctx, nm)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("must have at most 32 items"))
		})
	})
})
