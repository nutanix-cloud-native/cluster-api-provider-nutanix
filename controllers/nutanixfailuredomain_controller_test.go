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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/uuid"
	capiv1beta1 "sigs.k8s.io/cluster-api/api/core/v1beta1" //nolint:staticcheck // suppress complaining on Deprecated package
	"sigs.k8s.io/cluster-api/util"
	v1beta1conditions "sigs.k8s.io/cluster-api/util/deprecated/v1beta1/conditions"         //nolint:staticcheck // suppress complaining on Deprecated package
	v1beta2conditions "sigs.k8s.io/cluster-api/util/deprecated/v1beta1/conditions/v1beta2" //nolint:staticcheck // suppress complaining on Deprecated package

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
)

var _ = Describe("NutanixFailureDomainReconciler", func() {
	var (
		ctx         context.Context
		fdObj       *infrav1.NutanixFailureDomain
		ntnxMachine *infrav1.NutanixMachine
		reconciler  *NutanixFailureDomainReconciler
		rstr, rstr2 string
	)

	BeforeEach(func() {
		ctx = context.Background()
		rstr = util.RandomString(10)
		rstr2 = util.RandomString(12)

		fdObj = &infrav1.NutanixFailureDomain{
			TypeMeta: metav1.TypeMeta{
				Kind:       infrav1.NutanixFailureDomainKind,
				APIVersion: infrav1.GroupVersion.String(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "fd-test",
				Namespace: corev1.NamespaceDefault,
			},
			Spec: infrav1.NutanixFailureDomainSpec{
				PrismElementCluster: infrav1.NutanixResourceIdentifier{
					Type: infrav1.NutanixIdentifierName,
					Name: &rstr,
				},
				Subnets: []infrav1.NutanixResourceIdentifier{
					{Type: infrav1.NutanixIdentifierName, Name: &rstr},
				},
			},
		}

		reconciler = &NutanixFailureDomainReconciler{
			Client: k8sClient,
			Scheme: runtime.NewScheme(),
		}

		ntnxMachine = &infrav1.NutanixMachine{
			TypeMeta: metav1.TypeMeta{
				Kind:       infrav1.NutanixMachineKind,
				APIVersion: infrav1.GroupVersion.String(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "m-test",
				Namespace: corev1.NamespaceDefault,
				UID:       utilruntime.NewUUID(),
			},
			Spec: infrav1.NutanixMachineSpec{
				VCPUsPerSocket: 1,
				VCPUSockets:    2,
				MemorySize:     resource.MustParse("2Gi"),
				SystemDiskSize: resource.MustParse("20Gi"),
				Image: &infrav1.NutanixResourceIdentifier{
					Type: infrav1.NutanixIdentifierName,
					Name: &rstr,
				},
				Cluster: infrav1.NutanixResourceIdentifier{
					Type: infrav1.NutanixIdentifierName,
					Name: &rstr,
				},
				Subnets: []infrav1.NutanixResourceIdentifier{
					{Type: infrav1.NutanixIdentifierName, Name: &rstr},
				},
			},
			Status: infrav1.NutanixMachineStatus{
				FailureDomain: &fdObj.Name,
			},
		}
	})

	AfterEach(func() {
		_ = k8sClient.Delete(ctx, fdObj)
		_ = k8sClient.Delete(ctx, ntnxMachine)
	})

	Context("Update an NutanixFailureDomain Configuration", func() {
		It("should not allow to update failure domain prism element configuration", func() {
			Expect(k8sClient.Create(ctx, fdObj)).To(Succeed())

			fd2 := fdObj.DeepCopy()
			fd2.Spec.PrismElementCluster.Name = &rstr2
			err := k8sClient.Update(ctx, fd2)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("prismElementCluster is immutable once set"))
		})

		It("should not allow to update failure domain subnets configuration", func() {
			Expect(k8sClient.Create(ctx, fdObj)).To(Succeed())

			fd2 := fdObj.DeepCopy()
			fd2.Spec.Subnets[0].Name = &rstr2
			err := k8sClient.Update(ctx, fd2)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("subnets is immutable once set"))
		})
	})

	Context("Reconcile an NutanixFailureDomain", func() {
		It("should allow to delete failure domain if no nutanix machines use it", func() {
			Expect(k8sClient.Create(ctx, fdObj)).To(Succeed())

			_, err := reconciler.reconcileDelete(ctx, fdObj)
			Expect(err).NotTo(HaveOccurred())
			cond := v1beta1conditions.Get(fdObj, infrav1.FailureDomainSafeForDeletionCondition)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(corev1.ConditionTrue))
			condv1beta2 := v1beta2conditions.Get(fdObj, string(infrav1.FailureDomainSafeForDeletionCondition))
			Expect(condv1beta2).NotTo(BeNil())
			Expect(condv1beta2.Status).To(Equal(metav1.ConditionTrue))
			Expect(condv1beta2.Reason).To(Equal(capiv1beta1.ReadyV1Beta2Reason))

			Expect(k8sClient.Delete(ctx, fdObj)).To(Succeed())
		})

		It("should not allow to delete failure domain if there are nutanix machines use it", func() {
			Expect(k8sClient.Create(ctx, fdObj)).To(Succeed())

			Expect(k8sClient.Create(ctx, ntnxMachine)).To(Succeed())
			ntnxMachine.Status.FailureDomain = &fdObj.Name
			err := k8sClient.Status().Update(ctx, ntnxMachine)
			Expect(err).NotTo(HaveOccurred())

			_, err = reconciler.reconcileDelete(ctx, fdObj)
			Expect(err).To(HaveOccurred())
			cond := v1beta1conditions.Get(fdObj, infrav1.FailureDomainSafeForDeletionCondition)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(corev1.ConditionFalse))
			Expect(cond.Severity).To(Equal(capiv1beta1.ConditionSeverityError))
			Expect(cond.Reason).To(Equal(infrav1.FailureDomainInUseReason))
			condv1beta2 := v1beta2conditions.Get(fdObj, string(infrav1.FailureDomainSafeForDeletionCondition))
			Expect(condv1beta2).NotTo(BeNil())
			Expect(condv1beta2.Status).To(Equal(metav1.ConditionFalse))
			Expect(condv1beta2.Reason).To(Equal(infrav1.FailureDomainInUseReason))
		})
	})
})
