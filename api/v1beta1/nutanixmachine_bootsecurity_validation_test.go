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

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
)

var _ = Describe("NutanixMachine boot security CEL validation", func() {
	newMachine := func(name string) *infrav1.NutanixMachine {
		clusterName := "test-cluster"
		return createTestNutanixMachine(name, infrav1.NutanixResourceIdentifier{
			Type: infrav1.NutanixIdentifierName,
			Name: &clusterName,
		})
	}

	It("accepts UEFI Secure Boot with vTPM", func() {
		machine := newMachine("test-uefi-secure-boot-vtpm")
		machine.Spec.BootType = infrav1.NutanixBootTypeUEFI
		machine.Spec.SecureBootEnabled = true
		machine.Spec.VTPMEnabled = true

		Expect(k8sClient.Create(ctx, machine)).To(Succeed())
		Expect(k8sClient.Delete(ctx, machine)).To(Succeed())
	})

	It("accepts the existing default configuration", func() {
		machine := newMachine("test-default-boot-security")

		Expect(k8sClient.Create(ctx, machine)).To(Succeed())
		Expect(k8sClient.Delete(ctx, machine)).To(Succeed())
	})

	It("rejects Secure Boot with legacy boot", func() {
		machine := newMachine("test-legacy-secure-boot")
		machine.Spec.BootType = infrav1.NutanixBootTypeLegacy
		machine.Spec.SecureBootEnabled = true

		err := k8sClient.Create(ctx, machine)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("secureBootEnabled requires bootType to be uefi"))
	})

	It("rejects vTPM without Secure Boot", func() {
		machine := newMachine("test-vtpm-without-secure-boot")
		machine.Spec.BootType = infrav1.NutanixBootTypeUEFI
		machine.Spec.VTPMEnabled = true

		err := k8sClient.Create(ctx, machine)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("vtpmEnabled requires bootType to be uefi and secureBootEnabled to be true"))
	})
})
