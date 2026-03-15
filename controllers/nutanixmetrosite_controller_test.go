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
	capiv1beta2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	v1beta1conditions "sigs.k8s.io/cluster-api/util/deprecated/v1beta1/conditions" //nolint:staticcheck // suppress complaining on Deprecated package
	ctrlutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

var _ = Describe("NutanixMetroSiteReconciler", func() {
	var (
		ctx            context.Context
		metroObj       *infrav1.NutanixMetro
		machine        *capiv1beta2.Machine
		fdObj1, fdObj2 *infrav1.NutanixFailureDomain
		metroSiteObj   *infrav1.NutanixMetroSite

		reconciler *NutanixMetroSiteReconciler
	)

	BeforeEach(func() {
		ctx = context.Background()

		fdObj1 = &infrav1.NutanixFailureDomain{
			TypeMeta: metav1.TypeMeta{
				Kind:       infrav1.NutanixFailureDomainKind,
				APIVersion: infrav1.GroupVersion.String(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "fd-1",
				Namespace: "default",
			},
			Spec: infrav1.NutanixFailureDomainSpec{
				PrismElementCluster: infrav1.NutanixResourceIdentifier{
					Type: infrav1.NutanixIdentifierName,
					Name: ptr.To("pe-1"),
				},
				Subnets: []infrav1.NutanixResourceIdentifier{
					{Type: infrav1.NutanixIdentifierName, Name: ptr.To("subnet-metro")},
				},
			},
		}

		fdObj2 = &infrav1.NutanixFailureDomain{
			TypeMeta: metav1.TypeMeta{
				Kind:       infrav1.NutanixFailureDomainKind,
				APIVersion: infrav1.GroupVersion.String(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "fd-2",
				Namespace: "default",
			},
			Spec: infrav1.NutanixFailureDomainSpec{
				PrismElementCluster: infrav1.NutanixResourceIdentifier{
					Type: infrav1.NutanixIdentifierName,
					Name: ptr.To("pe-2"),
				},
				Subnets: []infrav1.NutanixResourceIdentifier{
					{Type: infrav1.NutanixIdentifierName, Name: ptr.To("subnet-metro")},
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
					{Name: fdObj1.Name},
					{Name: fdObj2.Name},
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
				FailureDomainRef: corev1.LocalObjectReference{Name: fdObj1.Name},
				MetroRef:         corev1.LocalObjectReference{Name: metroObj.Name},
			},
		}

		reconciler = &NutanixMetroSiteReconciler{
			Client: k8sClient,
			Scheme: runtime.NewScheme(),
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
	})

	AfterEach(func() {
		_ = k8sClient.Delete(ctx, fdObj1)
		_ = k8sClient.Delete(ctx, fdObj2)
		_ = k8sClient.Delete(ctx, metroSiteObj)
		_ = k8sClient.Delete(ctx, metroObj)
	})

	Context("Test reconcileDelete", func() {
		It("should prevent deletion when referenced by Machine spec.failureDomain", func() {
			Expect(k8sClient.Create(ctx, metroSiteObj)).To(Succeed())

			machine.Spec.FailureDomain = metroSiteFailureDomainPrefix + metroSiteObj.Name
			err := reconciler.reconcileDelete(ctx, metroSiteObj, []capiv1beta2.Machine{*machine})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("is not safe for deletion since it is in use"))
			cond := v1beta1conditions.Get(metroSiteObj, infrav1.MetroSiteSafeForDeletionCondition)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(corev1.ConditionFalse))
			Expect(cond.Message).To(ContainSubstring(fmt.Sprintf("machine:%s,cluster:%s", machine.Name, machine.Spec.ClusterName)))
		})

		It("should allow deletion when not referenced by other resources", func() {
			Expect(k8sClient.Create(ctx, metroSiteObj)).To(Succeed())

			machine.Spec.FailureDomain = "other"
			err := reconciler.reconcileDelete(ctx, metroSiteObj, []capiv1beta2.Machine{*machine})
			Expect(err).NotTo(HaveOccurred())

			cond := v1beta1conditions.Get(metroSiteObj, infrav1.MetroSiteSafeForDeletionCondition)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(corev1.ConditionTrue))
			Expect(ctrlutil.ContainsFinalizer(metroSiteObj, infrav1.NutanixMetroSiteFinalizer)).To(BeFalse())
		})
	})

	Context("Test reconcileNormal", func() {
		It("reconciling should succeed when its referenced objects exist", func() {
			Expect(k8sClient.Create(ctx, fdObj1)).To(Succeed())
			Expect(k8sClient.Create(ctx, fdObj2)).To(Succeed())
			Expect(k8sClient.Create(ctx, metroObj)).To(Succeed())
			Expect(k8sClient.Create(ctx, metroSiteObj)).To(Succeed())

			err := reconciler.reconcileNormal(ctx, metroSiteObj)
			Expect(err).NotTo(HaveOccurred())
		})

		It("reconciling should fail when the referenced failureDomain object not exist", func() {
			Expect(k8sClient.Create(ctx, fdObj2)).To(Succeed())
			Expect(k8sClient.Create(ctx, metroObj)).To(Succeed())
			Expect(k8sClient.Create(ctx, metroSiteObj)).To(Succeed())

			err := reconciler.reconcileNormal(ctx, metroSiteObj)
			Expect(err).To(HaveOccurred())
		})

		It("reconciling should fail when the referenced NutanixMetro object not exist", func() {
			Expect(k8sClient.Create(ctx, fdObj1)).To(Succeed())
			Expect(k8sClient.Create(ctx, fdObj2)).To(Succeed())
			Expect(k8sClient.Create(ctx, metroSiteObj)).To(Succeed())

			err := reconciler.reconcileNormal(ctx, metroSiteObj)
			Expect(err).To(HaveOccurred())
		})

		It("reconciling should fail when the referenced failureDomain is not from the referenced NutanixMetro object", func() {
			Expect(k8sClient.Create(ctx, fdObj1)).To(Succeed())
			Expect(k8sClient.Create(ctx, fdObj2)).To(Succeed())
			metroObj.Spec.FailureDomains = []corev1.LocalObjectReference{{Name: "fd-1"}, {Name: "fd-3"}}
			Expect(k8sClient.Create(ctx, metroObj)).To(Succeed())
			Expect(k8sClient.Create(ctx, metroSiteObj)).To(Succeed())

			metroSiteObj.Spec.FailureDomainRef.Name = "fd-2"
			err := reconciler.reconcileNormal(ctx, metroSiteObj)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(fmt.Sprintf("metroSite spec.failureDomain %s is not in the referred NutanixMetro's failureDomains", metroSiteObj.Spec.FailureDomainRef.Name)))
		})
	})
})
