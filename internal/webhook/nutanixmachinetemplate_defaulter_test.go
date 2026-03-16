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

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
)

func TestNutanixMachineTemplateDefaulter_Default(t *testing.T) {
	ctx := context.Background()
	defaulter := NutanixMachineTemplateDefaulter{}

	minimalSpec := func() infrav1.NutanixMachineSpec {
		return infrav1.NutanixMachineSpec{
			VCPUsPerSocket: 1,
			VCPUSockets:    1,
			MemorySize:     resource.MustParse("2Gi"),
			SystemDiskSize: resource.MustParse("20Gi"),
			Image:          &infrav1.NutanixResourceIdentifier{Type: infrav1.NutanixIdentifierName},
			Cluster:        infrav1.NutanixResourceIdentifier{Type: infrav1.NutanixIdentifierName},
		}
	}

	t.Run("sets placeholder on template spec image when name is nil", func(t *testing.T) {
		nmt := &infrav1.NutanixMachineTemplate{
			Spec: infrav1.NutanixMachineTemplateSpec{
				Template: infrav1.NutanixMachineTemplateResource{
					Spec: minimalSpec(),
				},
			},
		}
		nmt.Spec.Template.Spec.Image.Name = nil

		err := defaulter.Default(ctx, nmt)
		if err != nil {
			t.Fatalf("Default() error = %v", err)
		}
		if nmt.Spec.Template.Spec.Image.Name == nil || *nmt.Spec.Template.Spec.Image.Name != DefaultNamePlaceholder {
			t.Errorf("expected template.spec.image.name = %q, got %v", DefaultNamePlaceholder, nmt.Spec.Template.Spec.Image.Name)
		}
	})

	t.Run("defaults cluster and project in template spec", func(t *testing.T) {
		nmt := &infrav1.NutanixMachineTemplate{
			Spec: infrav1.NutanixMachineTemplateSpec{
				Template: infrav1.NutanixMachineTemplateResource{
					Spec: minimalSpec(),
				},
			},
		}
		nmt.Spec.Template.Spec.Cluster.Name = nil
		nmt.Spec.Template.Spec.Project = &infrav1.NutanixResourceIdentifier{Type: infrav1.NutanixIdentifierName}

		err := defaulter.Default(ctx, nmt)
		if err != nil {
			t.Fatalf("Default() error = %v", err)
		}
		if nmt.Spec.Template.Spec.Cluster.Name == nil || *nmt.Spec.Template.Spec.Cluster.Name != DefaultNamePlaceholder {
			t.Errorf("expected template.spec.cluster.name = %q, got %v", DefaultNamePlaceholder, nmt.Spec.Template.Spec.Cluster.Name)
		}
		if nmt.Spec.Template.Spec.Project.Name == nil || *nmt.Spec.Template.Spec.Project.Name != DefaultNamePlaceholder {
			t.Errorf("expected template.spec.project.name = %q, got %v", DefaultNamePlaceholder, nmt.Spec.Template.Spec.Project.Name)
		}
	})

	t.Run("returns nil for non-NutanixMachineTemplate object", func(t *testing.T) {
		nm := &infrav1.NutanixMachine{
			Spec: minimalSpec(),
		}
		nm.Spec.Image.Name = nil
		err := defaulter.Default(ctx, nm)
		if err != nil {
			t.Fatalf("Default() should not error for wrong type, got %v", err)
		}
		// NutanixMachine should be unchanged (defaulter ignores it)
		if nm.Spec.Image.Name != nil {
			t.Errorf("expected NutanixMachine to be unchanged, got image.name = %v", *nm.Spec.Image.Name)
		}
	})
}
