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
	"fmt"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	capiv1beta1 "sigs.k8s.io/cluster-api/api/core/v1beta1" //nolint:staticcheck // suppress complaining on Deprecated package
	capiv1beta2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	v1beta1conditions "sigs.k8s.io/cluster-api/util/deprecated/v1beta1/conditions"         //nolint:staticcheck // suppress complaining on Deprecated package
	v1beta2conditions "sigs.k8s.io/cluster-api/util/deprecated/v1beta1/conditions/v1beta2" //nolint:staticcheck // suppress complaining on Deprecated package
)

var _ = Describe("NutanixFailureDomainReconciler", func() {
	var (
		ctx          context.Context
		fdObj        *infrav1.NutanixFailureDomain
		machine      *capiv1beta2.Machine
		metroObj     *infrav1.NutanixMetro
		metroSiteObj *infrav1.NutanixMetroSite
		reconciler   *NutanixFailureDomainReconciler
	)

	BeforeEach(func() {
		ctx = context.Background()

		fdObj = &infrav1.NutanixFailureDomain{
			TypeMeta: metav1.TypeMeta{
				Kind:       infrav1.NutanixFailureDomainKind,
				APIVersion: infrav1.GroupVersion.String(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "fd-test",
				Namespace: "default",
			},
			Spec: infrav1.NutanixFailureDomainSpec{
				PrismElementCluster: infrav1.NutanixResourceIdentifier{
					Type: infrav1.NutanixIdentifierName,
					Name: ptr.To("pe-test"),
				},
				Subnets: []infrav1.NutanixResourceIdentifier{
					{Type: infrav1.NutanixIdentifierName, Name: ptr.To("subnet-test")},
				},
			},
		}

		metroObj = &infrav1.NutanixMetro{
			TypeMeta: metav1.TypeMeta{
				Kind:       infrav1.NutanixMetroKind,
				APIVersion: infrav1.GroupVersion.String(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "metro-test",
				Namespace: "default",
			},
			Spec: infrav1.NutanixMetroSpec{
				FailureDomains: []corev1.LocalObjectReference{
					{Name: "fd-1"},
					{Name: "fd-2"},
				},
			},
		}

		metroSiteObj = &infrav1.NutanixMetroSite{
			TypeMeta: metav1.TypeMeta{
				Kind:       infrav1.NutanixMetroKind,
				APIVersion: infrav1.GroupVersion.String(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "metrosite-test",
				Namespace: "default",
			},
			Spec: infrav1.NutanixMetroSiteSpec{
				FailureDomainRef: corev1.LocalObjectReference{Name: "fd-1"},
				MetroRef:         corev1.LocalObjectReference{Name: metroObj.Name},
			},
		}

		machine = &capiv1beta2.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "m-test",
				Namespace: "default",
			},
			Spec: capiv1beta2.MachineSpec{
				ClusterName: "cl-test",
			},
		}

		reconciler = &NutanixFailureDomainReconciler{
			Client: k8sClient,
			Scheme: runtime.NewScheme(),
		}
	})

	AfterEach(func() {
		_ = k8sClient.Delete(ctx, fdObj)
		_ = k8sClient.Delete(ctx, metroObj)
	})

	Context("Update an NutanixFailureDomain Configuration", func() {
		It("should not allow to update failure domain prism element configuration", func() {
			Expect(k8sClient.Create(ctx, fdObj)).To(Succeed())

			fd := fdObj.DeepCopy()
			fd.Spec.PrismElementCluster.Name = ptr.To("pe-3")
			err := k8sClient.Update(ctx, fd)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("prismElementCluster is immutable once set"))
		})

		It("should not allow to update failure domain subnets configuration", func() {
			Expect(k8sClient.Create(ctx, fdObj)).To(Succeed())

			fd := fdObj.DeepCopy()
			fd.Spec.Subnets[0].Name = ptr.To("subnet-3")
			err := k8sClient.Update(ctx, fd)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("subnets is immutable once set"))
		})
	})

	Context("Test reconcileDelete", func() {
		It("should allow to delete failure domain if not referenced by other resources", func() {
			Expect(k8sClient.Create(ctx, fdObj)).To(Succeed())

			err := reconciler.reconcileDelete(ctx, fdObj, []capiv1beta2.Machine{*machine}, []infrav1.NutanixMetro{*metroObj}, []infrav1.NutanixMetroSite{*metroSiteObj})
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

		It("should not allow to delete failure domain if there are machines reference it", func() {
			Expect(k8sClient.Create(ctx, fdObj)).To(Succeed())

			machine.Spec.FailureDomain = fdObj.Name
			err := reconciler.reconcileDelete(ctx, fdObj, []capiv1beta2.Machine{*machine}, []infrav1.NutanixMetro{*metroObj}, []infrav1.NutanixMetroSite{*metroSiteObj})
			Expect(err).To(HaveOccurred())
			cond := v1beta1conditions.Get(fdObj, infrav1.FailureDomainSafeForDeletionCondition)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(corev1.ConditionFalse))
			Expect(cond.Severity).To(Equal(capiv1beta1.ConditionSeverityError))
			Expect(cond.Reason).To(Equal(infrav1.FailureDomainInUseReason))
			Expect(cond.Message).To(ContainSubstring(fmt.Sprintf("machine:%s,cluster:%s", machine.Name, machine.Spec.ClusterName)))
			condv1beta2 := v1beta2conditions.Get(fdObj, string(infrav1.FailureDomainSafeForDeletionCondition))
			Expect(condv1beta2).NotTo(BeNil())
			Expect(condv1beta2.Status).To(Equal(metav1.ConditionFalse))
			Expect(condv1beta2.Reason).To(Equal(infrav1.FailureDomainInUseReason))
		})

		It("should not allow to delete failure domain if there are nutanixMetroes reference it", func() {
			Expect(k8sClient.Create(ctx, fdObj)).To(Succeed())

			metroObj.Spec.FailureDomains[0].Name = fdObj.Name
			err := reconciler.reconcileDelete(ctx, fdObj, []capiv1beta2.Machine{*machine}, []infrav1.NutanixMetro{*metroObj}, []infrav1.NutanixMetroSite{*metroSiteObj})
			Expect(err).To(HaveOccurred())
			cond := v1beta1conditions.Get(fdObj, infrav1.FailureDomainSafeForDeletionCondition)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(corev1.ConditionFalse))
			Expect(cond.Severity).To(Equal(capiv1beta1.ConditionSeverityError))
			Expect(cond.Reason).To(Equal(infrav1.FailureDomainInUseReason))
			Expect(cond.Message).To(ContainSubstring("nutanixMetro:" + metroObj.Name))
			condv1beta2 := v1beta2conditions.Get(fdObj, string(infrav1.FailureDomainSafeForDeletionCondition))
			Expect(condv1beta2).NotTo(BeNil())
			Expect(condv1beta2.Status).To(Equal(metav1.ConditionFalse))
			Expect(condv1beta2.Reason).To(Equal(infrav1.FailureDomainInUseReason))
		})

		It("should not allow to delete failure domain if there are nutanixMetroeSites reference it", func() {
			Expect(k8sClient.Create(ctx, fdObj)).To(Succeed())

			metroSiteObj.Spec.FailureDomainRef.Name = fdObj.Name
			err := reconciler.reconcileDelete(ctx, fdObj, []capiv1beta2.Machine{*machine}, []infrav1.NutanixMetro{*metroObj}, []infrav1.NutanixMetroSite{*metroSiteObj})
			Expect(err).To(HaveOccurred())
			cond := v1beta1conditions.Get(fdObj, infrav1.FailureDomainSafeForDeletionCondition)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(corev1.ConditionFalse))
			Expect(cond.Severity).To(Equal(capiv1beta1.ConditionSeverityError))
			Expect(cond.Reason).To(Equal(infrav1.FailureDomainInUseReason))
			Expect(cond.Message).To(ContainSubstring("nutanixMetroSite:" + metroSiteObj.Name))
			condv1beta2 := v1beta2conditions.Get(fdObj, string(infrav1.FailureDomainSafeForDeletionCondition))
			Expect(condv1beta2).NotTo(BeNil())
			Expect(condv1beta2.Status).To(Equal(metav1.ConditionFalse))
			Expect(condv1beta2.Reason).To(Equal(infrav1.FailureDomainInUseReason))
		})
	})
})
