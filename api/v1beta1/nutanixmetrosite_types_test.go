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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
)

var _ = Describe("NutanixMetroSite CEL Validation", func() {
	Context("Immutability validation", func() {
		It("should reject update to preferredFailureDomain", func() {
			site := defaultNutanixMetroSiteObject()
			Expect(k8sClient.Create(ctx, site)).To(Succeed())

			site.Spec.PreferredFailureDomain.Name = "fd-other"
			err := k8sClient.Update(ctx, site)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("preferredFailureDomain is immutable once set"))

			_ = k8sClient.Delete(ctx, site)
		})

		It("should reject update to metroRef", func() {
			site := defaultNutanixMetroSiteObject()
			Expect(k8sClient.Create(ctx, site)).To(Succeed())

			site.Spec.MetroRef.Name = "some-other-metro"
			err := k8sClient.Update(ctx, site)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("metroRef is immutable once set"))

			_ = k8sClient.Delete(ctx, site)
		})

		It("should allow update that does not change immutable fields", func() {
			site := defaultNutanixMetroSiteObject()
			Expect(k8sClient.Create(ctx, site)).To(Succeed())

			patch := client.MergeFrom(site.DeepCopy())
			if site.Labels == nil {
				site.Labels = map[string]string{}
			}
			site.Labels["test"] = "label"
			err := k8sClient.Patch(ctx, site, patch)
			Expect(err).NotTo(HaveOccurred())

			_ = k8sClient.Delete(ctx, site)
		})
	})

	Context("Reference name not empty validation", func() {
		It("should reject metroRef with empty name", func() {
			site := defaultNutanixMetroSiteObject()
			site.Spec.MetroRef.Name = ""

			err := k8sClient.Create(ctx, site)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("metroRef.name must not be empty"))
		})

		It("should reject preferredFailureDomain with empty name", func() {
			site := defaultNutanixMetroSiteObject()
			site.Spec.PreferredFailureDomain.Name = ""

			err := k8sClient.Create(ctx, site)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("preferredFailureDomain.name must not be empty"))
		})

		It("should accept when reference names are not empty", func() {
			site := defaultNutanixMetroSiteObject()

			err := k8sClient.Create(ctx, site)
			Expect(err).NotTo(HaveOccurred())
			_ = k8sClient.Delete(ctx, site)
		})
	})
})

func defaultNutanixMetroSiteObject() *infrav1.NutanixMetroSite {
	return &infrav1.NutanixMetroSite{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default-site",
			Namespace: "default",
		},
		Spec: infrav1.NutanixMetroSiteSpec{
			PreferredFailureDomain: corev1.LocalObjectReference{
				Name: "fd-1",
			},
			MetroRef: corev1.LocalObjectReference{
				Name: "metro-0",
			},
		},
	}
}
