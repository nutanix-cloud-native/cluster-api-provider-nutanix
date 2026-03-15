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
	credentialTypes "github.com/nutanix-cloud-native/prism-go-client/environment/credentials"
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

var _ = Describe("NutanixMetroReconciler", func() {
	var (
		ctx            context.Context
		metroObj       *infrav1.NutanixMetro
		machine        *capiv1beta2.Machine
		ntnxCluster    *infrav1.NutanixCluster
		fdObj1, fdObj2 *infrav1.NutanixFailureDomain
		metroSiteObj   *infrav1.NutanixMetroSite

		reconciler *NutanixMetroReconciler
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

		reconciler = &NutanixMetroReconciler{
			Client: k8sClient,
			Scheme: runtime.NewScheme(),
		}

		ntnxCluster = &infrav1.NutanixCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "cl-test",
				Namespace: "default",
			},
			Spec: infrav1.NutanixClusterSpec{
				PrismCentral: &credentialTypes.NutanixPrismEndpoint{
					Address: "metro.test",
					Port:    9440,
				},
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
	})

	AfterEach(func() {
		_ = k8sClient.Delete(ctx, fdObj1)
		_ = k8sClient.Delete(ctx, fdObj2)
		_ = k8sClient.Delete(ctx, metroObj)
	})

	Context("Test reconcileDelete", func() {
		It("should prevent deletion when referenced by NutanixCluster", func() {
			Expect(k8sClient.Create(ctx, metroObj)).To(Succeed())

			ntnxCluster.Spec.MetroRefs = []corev1.LocalObjectReference{{Name: metroObj.Name}}
			err := reconciler.reconcileDelete(ctx, metroObj, []capiv1beta2.Machine{}, []infrav1.NutanixCluster{*ntnxCluster}, []infrav1.NutanixMetroSite{})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("is not safe for deletion since it is in use"))
			cond := v1beta1conditions.Get(metroObj, infrav1.MetroSafeForDeletionCondition)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(corev1.ConditionFalse))
			Expect(cond.Message).To(ContainSubstring("nutanixCluster:" + ntnxCluster.Name))
		})

		It("should prevent deletion when referenced by Machine spec.failureDomain", func() {
			Expect(k8sClient.Create(ctx, metroObj)).To(Succeed())

			machine.Spec.FailureDomain = metroFailureDomainPrefix + metroObj.Name
			err := reconciler.reconcileDelete(ctx, metroObj, []capiv1beta2.Machine{*machine}, []infrav1.NutanixCluster{}, []infrav1.NutanixMetroSite{})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("is not safe for deletion since it is in use"))
			cond := v1beta1conditions.Get(metroObj, infrav1.MetroSafeForDeletionCondition)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(corev1.ConditionFalse))
			Expect(cond.Message).To(ContainSubstring(fmt.Sprintf("machine:%s,cluster:%s", machine.Name, machine.Spec.ClusterName)))
		})

		It("should prevent deletion when referenced by NutanixMetroSite", func() {
			Expect(k8sClient.Create(ctx, metroObj)).To(Succeed())

			metroSiteObj.Spec.MetroRef = corev1.LocalObjectReference{Name: metroObj.Name}
			err := reconciler.reconcileDelete(ctx, metroObj, []capiv1beta2.Machine{}, []infrav1.NutanixCluster{}, []infrav1.NutanixMetroSite{*metroSiteObj})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("is not safe for deletion since it is in use"))
			cond := v1beta1conditions.Get(metroObj, infrav1.MetroSafeForDeletionCondition)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(corev1.ConditionFalse))
			Expect(cond.Message).To(ContainSubstring("metroSite:" + metroSiteObj.Name))
		})

		It("should allow deletion when not referenced by other objects", func() {
			Expect(k8sClient.Create(ctx, metroObj)).To(Succeed())

			machine.Spec.FailureDomain = ""
			ntnxCluster.Spec.MetroRefs = []corev1.LocalObjectReference{}
			metroSiteObj.Spec.MetroRef.Name = "other"
			err := reconciler.reconcileDelete(ctx, metroObj, []capiv1beta2.Machine{*machine}, []infrav1.NutanixCluster{*ntnxCluster}, []infrav1.NutanixMetroSite{*metroSiteObj})
			Expect(err).NotTo(HaveOccurred())

			cond := v1beta1conditions.Get(metroObj, infrav1.MetroSafeForDeletionCondition)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(corev1.ConditionTrue))
			Expect(ctrlutil.ContainsFinalizer(metroObj, infrav1.NutanixMetroFinalizer)).To(BeFalse())
		})
	})

	Context("Test reconcileNormal", func() {
		It("reconciling should succeed when the referenced failureDomain objects exist", func() {
			Expect(k8sClient.Create(ctx, fdObj1)).To(Succeed())
			Expect(k8sClient.Create(ctx, fdObj2)).To(Succeed())
			Expect(k8sClient.Create(ctx, metroObj)).To(Succeed())

			err := reconciler.reconcileNormal(ctx, metroObj)
			Expect(err).NotTo(HaveOccurred())
		})

		It("reconciling should fail when the referenced failureDomain object(s) not exist", func() {
			Expect(k8sClient.Create(ctx, metroObj)).To(Succeed())

			err := reconciler.reconcileNormal(ctx, metroObj)
			Expect(err).To(HaveOccurred())
		})
	})
})
