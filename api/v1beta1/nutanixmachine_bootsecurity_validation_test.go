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
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

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

	DescribeTable("validates boot security with VM profiles", func(secureBoot, vtpm bool) {
		machine := newMachine("test-profile-boot-security")
		profileName := "test-profile"
		machine.Spec.VMProfile = &infrav1.NutanixResourceIdentifier{
			Type: infrav1.NutanixIdentifierName,
			Name: &profileName,
		}
		machine.Spec.VCPUsPerSocket = 0
		machine.Spec.VCPUSockets = 0
		machine.Spec.MemorySize = resource.Quantity{}
		machine.Spec.SecureBootEnabled = secureBoot
		machine.Spec.VTPMEnabled = vtpm

		err := k8sClient.Create(ctx, machine)
		if secureBoot || vtpm {
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("When 'vmProfile' is set"))
		} else {
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Delete(ctx, machine)).To(Succeed())
		}
	},
		Entry("accepts disabled boot security", false, false),
		Entry("rejects Secure Boot", true, false),
		Entry("rejects vTPM", false, true),
	)

	DescribeTable("keeps boot security immutable", func(field string, enabled bool, value string, wantError bool) {
		machine := newMachine("test-immutable-boot-security")
		machine.Spec.BootType = infrav1.NutanixBootTypeUEFI
		if field == "secureBootEnabled" {
			machine.Spec.SecureBootEnabled = enabled
		} else {
			machine.Spec.SecureBootEnabled = true
			machine.Spec.VTPMEnabled = enabled
		}
		Expect(k8sClient.Create(ctx, machine)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, machine)).To(Succeed()) })

		patch := client.RawPatch(types.MergePatchType, []byte(fmt.Sprintf(`{"spec":{"%s":%s}}`, field, value)))
		err := k8sClient.Patch(ctx, machine, patch)
		if wantError {
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(field + " is immutable"))
		} else {
			Expect(err).NotTo(HaveOccurred())
		}
	},
		Entry("cannot enable omitted Secure Boot", "secureBootEnabled", false, "true", true),
		Entry("cannot disable Secure Boot", "secureBootEnabled", true, "false", true),
		Entry("cannot remove enabled Secure Boot", "secureBootEnabled", true, "null", true),
		Entry("accepts unchanged Secure Boot", "secureBootEnabled", true, "true", false),
		Entry("accepts explicit false for omitted Secure Boot", "secureBootEnabled", false, "false", false),
		Entry("cannot enable omitted vTPM", "vtpmEnabled", false, "true", true),
		Entry("cannot disable vTPM", "vtpmEnabled", true, "false", true),
		Entry("cannot remove enabled vTPM", "vtpmEnabled", true, "null", true),
		Entry("accepts unchanged vTPM", "vtpmEnabled", true, "true", false),
		Entry("accepts explicit false for omitted vTPM", "vtpmEnabled", false, "false", false),
	)
})
