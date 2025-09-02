/*
Copyright 2022 Nutanix

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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
)

var _ = Describe("NutanixMachineTemplate Controller", func() {
	Context("When reconciling a NutanixMachineTemplate", func() {
		const (
			nutanixMachineTemplateName = "test-machine-template"
			namespace                  = "default"
			timeout                    = time.Second * 10
			interval                   = time.Millisecond * 250
		)

		ctx := context.Background()

		machineTemplate := &infrav1.NutanixMachineTemplate{
			ObjectMeta: metav1.ObjectMeta{
				Name:      nutanixMachineTemplateName,
				Namespace: namespace,
			},
			Spec: infrav1.NutanixMachineTemplateSpec{
				Template: infrav1.NutanixMachineTemplateResource{
					Spec: infrav1.NutanixMachineSpec{
						VCPUSockets:    2,
						VCPUsPerSocket: 2,
						MemorySize:     resource.MustParse("4Gi"),
						SystemDiskSize: resource.MustParse("20Gi"),
						Cluster: infrav1.NutanixResourceIdentifier{
							Type: infrav1.NutanixIdentifierName,
							Name: ptr.To("test-cluster"),
						},
						Subnets: []infrav1.NutanixResourceIdentifier{
							{
								Type: infrav1.NutanixIdentifierName,
								Name: ptr.To("test-subnet"),
							},
						},
					},
				},
			},
		}

		var reconciler *NutanixMachineTemplateReconciler

		BeforeEach(func() {
			reconciler = &NutanixMachineTemplateReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}
		})

		AfterEach(func() {
			// Cleanup
			err := k8sClient.Delete(ctx, machineTemplate)
			if err != nil && client.IgnoreNotFound(err) != nil {
				Expect(err).ToNot(HaveOccurred())
			}
		})

		It("Should successfully reconcile the resource", func() {
			By("Creating the NutanixMachineTemplate")
			Expect(k8sClient.Create(ctx, machineTemplate)).Should(Succeed())

			By("Reconciling the NutanixMachineTemplate")
			_, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      nutanixMachineTemplateName,
					Namespace: namespace,
				},
			})
			Expect(err).ToNot(HaveOccurred())

			By("Checking the NutanixMachineTemplate status is updated")
			var updatedTemplate infrav1.NutanixMachineTemplate
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      nutanixMachineTemplateName,
					Namespace: namespace,
				}, &updatedTemplate)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(updatedTemplate.Status.Capacity).ToNot(BeNil())
			}, timeout, interval).Should(Succeed())

			By("Verifying the capacity values are correct")
			Expect(updatedTemplate.Status.Capacity).To(HaveKey(infrav1.AutoscalerResourceCPU))
			Expect(updatedTemplate.Status.Capacity).To(HaveKey(infrav1.AutoscalerResourceMemory))

			cpuQuantity := updatedTemplate.Status.Capacity[infrav1.AutoscalerResourceCPU]
			expectedCPU := int64(2 * 2) // VCPUSockets * VCPUsPerSocket
			Expect(cpuQuantity.Value()).To(Equal(expectedCPU))

			memoryQuantity := updatedTemplate.Status.Capacity[infrav1.AutoscalerResourceMemory]
			expectedMemory := resource.MustParse("4Gi")
			Expect(memoryQuantity.Equal(expectedMemory)).To(BeTrue())
		})

		It("Should handle NutanixMachineTemplate deletion", func() {
			By("Creating the NutanixMachineTemplate")
			Expect(k8sClient.Create(ctx, machineTemplate)).Should(Succeed())

			By("Reconciling to add finalizer")
			_, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      nutanixMachineTemplateName,
					Namespace: namespace,
				},
			})
			Expect(err).ToNot(HaveOccurred())

			By("Checking finalizer is added")
			var updatedTemplate infrav1.NutanixMachineTemplate
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      nutanixMachineTemplateName,
					Namespace: namespace,
				}, &updatedTemplate)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(updatedTemplate.Finalizers).To(ContainElement(infrav1.NutanixMachineTemplateFinalizer))
			}, timeout, interval).Should(Succeed())

			By("Deleting the NutanixMachineTemplate")
			Expect(k8sClient.Delete(ctx, &updatedTemplate)).Should(Succeed())

			By("Reconciling deletion")
			_, err = reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      nutanixMachineTemplateName,
					Namespace: namespace,
				},
			})
			Expect(err).ToNot(HaveOccurred())

			By("Verifying the NutanixMachineTemplate is deleted")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      nutanixMachineTemplateName,
					Namespace: namespace,
				}, &updatedTemplate)
				return client.IgnoreNotFound(err) == nil
			}, timeout, interval).Should(BeTrue())
		})

		Context("Validate capacity calculation", func() {
			It("Should correctly calculate CPU and memory capacity", func() {
				By("Creating a NutanixMachineTemplate with specific resources")
				customTemplate := &infrav1.NutanixMachineTemplate{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "custom-template",
						Namespace: namespace,
					},
					Spec: infrav1.NutanixMachineTemplateSpec{
						Template: infrav1.NutanixMachineTemplateResource{
							Spec: infrav1.NutanixMachineSpec{
								VCPUSockets:    4,
								VCPUsPerSocket: 2,
								MemorySize:     resource.MustParse("8Gi"),
								SystemDiskSize: resource.MustParse("40Gi"),
								Cluster: infrav1.NutanixResourceIdentifier{
									Type: infrav1.NutanixIdentifierName,
									Name: ptr.To("test-cluster"),
								},
								Subnets: []infrav1.NutanixResourceIdentifier{
									{
										Type: infrav1.NutanixIdentifierName,
										Name: ptr.To("test-subnet"),
									},
								},
							},
						},
					},
				}

				Expect(k8sClient.Create(ctx, customTemplate)).Should(Succeed())
				defer func() {
					_ = k8sClient.Delete(ctx, customTemplate)
				}()

				By("Reconciling the custom template")
				_, err := reconciler.Reconcile(ctx, ctrl.Request{
					NamespacedName: types.NamespacedName{
						Name:      "custom-template",
						Namespace: namespace,
					},
				})
				Expect(err).ToNot(HaveOccurred())

				By("Verifying the capacity calculation")
				var updatedTemplate infrav1.NutanixMachineTemplate
				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, types.NamespacedName{
						Name:      "custom-template",
						Namespace: namespace,
					}, &updatedTemplate)
					g.Expect(err).ToNot(HaveOccurred())
					g.Expect(updatedTemplate.Status.Capacity).ToNot(BeNil())
				}, timeout, interval).Should(Succeed())

				cpuQuantity := updatedTemplate.Status.Capacity[infrav1.AutoscalerResourceCPU]
				expectedCPU := int64(4 * 2) // 4 sockets * 2 CPUs per socket = 8 CPUs
				Expect(cpuQuantity.Value()).To(Equal(expectedCPU))

				memoryQuantity := updatedTemplate.Status.Capacity[infrav1.AutoscalerResourceMemory]
				expectedMemory := resource.MustParse("8Gi")
				Expect(memoryQuantity.Equal(expectedMemory)).To(BeTrue())
			})
		})
	})
})
