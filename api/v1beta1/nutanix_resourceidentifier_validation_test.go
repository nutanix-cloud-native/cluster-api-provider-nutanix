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
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
)

var (
	k8sClient client.Client
	testEnv   *envtest.Environment
	ctx       = context.Background()
)

func TestNutanixResourceIdentifierValidation(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "NutanixResourceIdentifier CEL Validation Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}

	cfg, err := testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	err = infrav1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = clusterv1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())
})

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})

// Helper function to create a NutanixMachine with specific resource identifier for testing
func createTestNutanixMachine(name string, identifier infrav1.NutanixResourceIdentifier) *infrav1.NutanixMachine {
	memorySize := resource.MustParse("4Gi")
	systemDiskSize := resource.MustParse("40Gi")

	return &infrav1.NutanixMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: infrav1.NutanixMachineSpec{
			VCPUsPerSocket: 2,
			VCPUSockets:    1,
			MemorySize:     memorySize,
			SystemDiskSize: systemDiskSize,
			Cluster:        identifier, // This will test our CEL validation
			ImageLookup: &infrav1.NutanixImageLookup{
				BaseOS: "ubuntu",
			},
		},
	}
}

var _ = Describe("NutanixResourceIdentifier CEL Validation", func() {
	Context("UUID validation", func() {
		It("should accept valid UUID with correct format", func() {
			validUUID := "550e8400-e29b-41d4-a716-446655440000"
			machine := createTestNutanixMachine("test-valid-uuid", infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: &validUUID,
			})

			err := k8sClient.Create(ctx, machine)
			Expect(err).NotTo(HaveOccurred())

			// Cleanup
			_ = k8sClient.Delete(ctx, machine)
		})

		It("should accept another valid UUID format", func() {
			validUUID := "123e4567-e89b-12d3-a456-426614174000"
			machine := createTestNutanixMachine("test-valid-uuid-2", infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: &validUUID,
			})

			err := k8sClient.Create(ctx, machine)
			Expect(err).NotTo(HaveOccurred())

			// Cleanup
			_ = k8sClient.Delete(ctx, machine)
		})

		It("should reject UUID with incorrect length (too short)", func() {
			invalidUUID := "550e8400-e29b-41d4-a716-44665544000" // 35 chars
			machine := createTestNutanixMachine("test-invalid-uuid-short", infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: &invalidUUID,
			})

			err := k8sClient.Create(ctx, machine)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("uuid"))
		})

		It("should reject UUID with incorrect length (too long)", func() {
			invalidUUID := "550e8400-e29b-41d4-a716-4466554400001" // 37 chars
			machine := createTestNutanixMachine("test-invalid-uuid-long", infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: &invalidUUID,
			})

			err := k8sClient.Create(ctx, machine)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("uuid"))
		})

		It("should reject UUID without dashes", func() {
			invalidUUID := "550e8400e29b41d4a716446655440000" // 32 chars, no dashes
			machine := createTestNutanixMachine("test-invalid-uuid-no-dashes", infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: &invalidUUID,
			})

			err := k8sClient.Create(ctx, machine)
			Expect(err).To(HaveOccurred())
		})

		It("should accept UUID with wrong dash positions (limited validation)", func() {
			imperfectUUID := "550e84-00e29b-41d4a716-4466554400001" // 36 chars with dashes but wrong positions
			machine := createTestNutanixMachine("test-imperfect-uuid", infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: &imperfectUUID,
			})

			err := k8sClient.Create(ctx, machine)
			Expect(err).To(HaveOccurred())

			// Cleanup
			_ = k8sClient.Delete(ctx, machine)
		})

		It("should reject when UUID field is missing for uuid type", func() {
			machine := createTestNutanixMachine("test-missing-uuid", infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				// UUID field intentionally omitted
			})

			err := k8sClient.Create(ctx, machine)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("uuid"))
		})

		It("should reject when both UUID and name are provided for uuid type", func() {
			validUUID := "550e8400-e29b-41d4-a716-446655440000"
			name := "test-name"
			machine := createTestNutanixMachine("test-uuid-with-name", infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: &validUUID,
				Name: &name, // This should not be allowed
			})

			err := k8sClient.Create(ctx, machine)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("name"))
		})
	})

	Context("Name validation", func() {
		It("should accept valid name", func() {
			validName := "test-cluster-name"
			machine := createTestNutanixMachine("test-valid-name", infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierName,
				Name: &validName,
			})

			err := k8sClient.Create(ctx, machine)
			Expect(err).NotTo(HaveOccurred())

			// Cleanup
			_ = k8sClient.Delete(ctx, machine)
		})

		It("should accept name with special characters", func() {
			validName := "test_cluster-name.example"
			machine := createTestNutanixMachine("test-valid-name-special", infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierName,
				Name: &validName,
			})

			err := k8sClient.Create(ctx, machine)
			Expect(err).NotTo(HaveOccurred())

			// Cleanup
			_ = k8sClient.Delete(ctx, machine)
		})

		It("should reject empty name", func() {
			emptyName := ""
			machine := createTestNutanixMachine("test-empty-name", infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierName,
				Name: &emptyName,
			})

			err := k8sClient.Create(ctx, machine)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("name"))
		})

		It("should reject when name field is missing for name type", func() {
			machine := createTestNutanixMachine("test-missing-name", infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierName,
				// Name field intentionally omitted
			})

			err := k8sClient.Create(ctx, machine)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("name"))
		})

		It("should reject when both name and UUID are provided for name type", func() {
			validName := "test-cluster-name"
			validUUID := "550e8400-e29b-41d4-a716-446655440000"
			machine := createTestNutanixMachine("test-name-with-uuid", infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierName,
				Name: &validName,
				UUID: &validUUID, // This should not be allowed
			})

			err := k8sClient.Create(ctx, machine)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("uuid"))
		})
	})

	Context("Edge cases and comprehensive validation", func() {
		It("should reject string that contains dashes but is not valid UUID length", func() {
			invalidUUID := "abc-def-ghi" // Contains dashes but not 36 chars
			machine := createTestNutanixMachine("test-short-with-dashes", infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: &invalidUUID,
			})

			err := k8sClient.Create(ctx, machine)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("uuid"))
		})

		It("should reject 36-char string without dashes", func() {
			invalidUUID := "abcdefghijklmnopqrstuvwxyz1234567890" // 36 chars, no dashes
			machine := createTestNutanixMachine("test-36chars-no-dashes", infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: &invalidUUID,
			})

			err := k8sClient.Create(ctx, machine)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("uuid"))
		})
	})
})
