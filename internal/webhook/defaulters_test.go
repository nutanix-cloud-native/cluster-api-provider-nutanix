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

package webhook

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/ptr"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
)

func TestDefaultNamePlaceholder(t *testing.T) {
	if DefaultNamePlaceholder != "placeholder-image" {
		t.Errorf("DefaultNamePlaceholder = %q, want %q", DefaultNamePlaceholder, "placeholder-image")
	}
}

// minimalMachineSpec returns a NutanixMachineSpec with required fields set and image (type name) for defaulting tests.
func minimalMachineSpec() infrav1.NutanixMachineSpec {
	return infrav1.NutanixMachineSpec{
		VCPUsPerSocket: 1,
		VCPUSockets:    1,
		MemorySize:     resource.MustParse("2Gi"),
		SystemDiskSize: resource.MustParse("20Gi"),
		Image:          &infrav1.NutanixResourceIdentifier{Type: infrav1.NutanixIdentifierName},
		Cluster:        infrav1.NutanixResourceIdentifier{Type: infrav1.NutanixIdentifierName},
	}
}

//nolint:gocognit // table-driven test with many cases
func TestNutanixMachineDefaulter_Default(t *testing.T) {
	ctx := context.Background()
	defaulter := NutanixMachineDefaulter{}

	t.Run("sets placeholder when image type is name and name is nil", func(t *testing.T) {
		nm := &infrav1.NutanixMachine{
			Spec: minimalMachineSpec(),
		}
		nm.Spec.Image.Name = nil

		err := defaulter.Default(ctx, nm)
		if err != nil {
			t.Fatalf("Default() error = %v", err)
		}
		if nm.Spec.Image == nil || nm.Spec.Image.Name == nil || *nm.Spec.Image.Name != DefaultNamePlaceholder {
			t.Errorf("expected image.name = %q, got %v", DefaultNamePlaceholder, nm.Spec.Image.Name)
		}
	})

	t.Run("sets placeholder when image type is name and name is empty string", func(t *testing.T) {
		nm := &infrav1.NutanixMachine{
			Spec: minimalMachineSpec(),
		}
		nm.Spec.Image.Name = ptr.To("")

		err := defaulter.Default(ctx, nm)
		if err != nil {
			t.Fatalf("Default() error = %v", err)
		}
		if nm.Spec.Image.Name == nil || *nm.Spec.Image.Name != DefaultNamePlaceholder {
			t.Errorf("expected image.name = %q, got %v", DefaultNamePlaceholder, nm.Spec.Image.Name)
		}
	})

	t.Run("leaves image name unchanged when already set", func(t *testing.T) {
		want := "my-image"
		nm := &infrav1.NutanixMachine{
			Spec: minimalMachineSpec(),
		}
		nm.Spec.Image.Name = ptr.To(want)

		err := defaulter.Default(ctx, nm)
		if err != nil {
			t.Fatalf("Default() error = %v", err)
		}
		if nm.Spec.Image.Name == nil || *nm.Spec.Image.Name != want {
			t.Errorf("expected image.name = %q, got %v", want, nm.Spec.Image.Name)
		}
	})

	t.Run("does not set name when image type is uuid", func(t *testing.T) {
		nm := &infrav1.NutanixMachine{
			Spec: minimalMachineSpec(),
		}
		nm.Spec.Image = &infrav1.NutanixResourceIdentifier{
			Type: infrav1.NutanixIdentifierUUID,
			UUID: ptr.To("550e8400-e29b-41d4-a716-446655440000"),
		}

		err := defaulter.Default(ctx, nm)
		if err != nil {
			t.Fatalf("Default() error = %v", err)
		}
		if nm.Spec.Image.Name != nil {
			t.Errorf("expected image.name nil for type uuid, got %v", *nm.Spec.Image.Name)
		}
	})

	t.Run("defaults cluster when type is name and name is empty", func(t *testing.T) {
		nm := &infrav1.NutanixMachine{
			Spec: minimalMachineSpec(),
		}
		nm.Spec.Cluster.Name = nil

		err := defaulter.Default(ctx, nm)
		if err != nil {
			t.Fatalf("Default() error = %v", err)
		}
		if nm.Spec.Cluster.Name == nil || *nm.Spec.Cluster.Name != DefaultNamePlaceholder {
			t.Errorf("expected cluster.name = %q, got %v", DefaultNamePlaceholder, nm.Spec.Cluster.Name)
		}
	})

	t.Run("defaults subnets and project", func(t *testing.T) {
		nm := &infrav1.NutanixMachine{
			Spec: minimalMachineSpec(),
		}
		nm.Spec.Subnets = []infrav1.NutanixResourceIdentifier{
			{Type: infrav1.NutanixIdentifierName},
			{Type: infrav1.NutanixIdentifierName, Name: ptr.To("")},
		}
		nm.Spec.Project = &infrav1.NutanixResourceIdentifier{Type: infrav1.NutanixIdentifierName}

		err := defaulter.Default(ctx, nm)
		if err != nil {
			t.Fatalf("Default() error = %v", err)
		}
		if nm.Spec.Subnets[0].Name == nil || *nm.Spec.Subnets[0].Name != DefaultNamePlaceholder {
			t.Errorf("expected subnets[0].name = %q, got %v", DefaultNamePlaceholder, nm.Spec.Subnets[0].Name)
		}
		if nm.Spec.Subnets[1].Name == nil || *nm.Spec.Subnets[1].Name != DefaultNamePlaceholder {
			t.Errorf("expected subnets[1].name = %q, got %v", DefaultNamePlaceholder, nm.Spec.Subnets[1].Name)
		}
		if nm.Spec.Project.Name == nil || *nm.Spec.Project.Name != DefaultNamePlaceholder {
			t.Errorf("expected project.name = %q, got %v", DefaultNamePlaceholder, nm.Spec.Project.Name)
		}
	})

	t.Run("defaults dataDisks dataSource and storageConfig.storageContainer", func(t *testing.T) {
		nm := &infrav1.NutanixMachine{
			Spec: minimalMachineSpec(),
		}
		nm.Spec.DataDisks = []infrav1.NutanixMachineVMDisk{
			{
				DiskSize:   resource.MustParse("10Gi"),
				DataSource: &infrav1.NutanixResourceIdentifier{Type: infrav1.NutanixIdentifierName},
				StorageConfig: &infrav1.NutanixMachineVMStorageConfig{
					DiskMode:         infrav1.NutanixMachineDiskModeStandard,
					StorageContainer: &infrav1.NutanixResourceIdentifier{Type: infrav1.NutanixIdentifierName},
				},
			},
		}

		err := defaulter.Default(ctx, nm)
		if err != nil {
			t.Fatalf("Default() error = %v", err)
		}
		if nm.Spec.DataDisks[0].DataSource.Name == nil || *nm.Spec.DataDisks[0].DataSource.Name != DefaultNamePlaceholder {
			t.Errorf("expected dataDisks[0].dataSource.name = %q, got %v", DefaultNamePlaceholder, nm.Spec.DataDisks[0].DataSource.Name)
		}
		sc := nm.Spec.DataDisks[0].StorageConfig.StorageContainer
		if sc == nil || sc.Name == nil || *sc.Name != DefaultNamePlaceholder {
			t.Errorf("expected dataDisks[0].storageConfig.storageContainer.name = %q, got %v", DefaultNamePlaceholder, sc.Name)
		}
	})

	t.Run("returns nil for non-NutanixMachine object without mutating", func(t *testing.T) {
		nmt := &infrav1.NutanixMachineTemplate{
			Spec: infrav1.NutanixMachineTemplateSpec{
				Template: infrav1.NutanixMachineTemplateResource{
					Spec: infrav1.NutanixMachineSpec{
						VCPUsPerSocket: 1,
						VCPUSockets:    1,
						MemorySize:     resource.MustParse("2Gi"),
						SystemDiskSize: resource.MustParse("20Gi"),
						Image:          &infrav1.NutanixResourceIdentifier{Type: infrav1.NutanixIdentifierName},
						Cluster:        infrav1.NutanixResourceIdentifier{Type: infrav1.NutanixIdentifierName},
					},
				},
			},
		}
		err := defaulter.Default(ctx, nmt)
		if err != nil {
			t.Fatalf("Default() error = %v", err)
		}
		// Defaulter ignores non-NutanixMachine; template spec should be unchanged (image name still nil)
		if nmt.Spec.Template.Spec.Image != nil && nmt.Spec.Template.Spec.Image.Name != nil {
			t.Errorf("NutanixMachineDefaulter should not mutate NutanixMachineTemplate, got image.name = %q", *nmt.Spec.Template.Spec.Image.Name)
		}
	})
}
