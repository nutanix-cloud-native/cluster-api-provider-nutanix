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

package v1beta1

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func newTestNutanixMachineTemplate(name string, image *NutanixResourceIdentifier) *NutanixMachineTemplate {
	return &NutanixMachineTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: NutanixMachineTemplateSpec{
			Template: NutanixMachineTemplateResource{
				Spec: NutanixMachineSpec{
					VCPUsPerSocket: 2,
					VCPUSockets:    1,
					MemorySize:     resource.MustParse("4Gi"),
					SystemDiskSize: resource.MustParse("40Gi"),
					Image:          image,
					Cluster: NutanixResourceIdentifier{
						Type: NutanixIdentifierName,
						Name: ptr.To("test-cluster"),
					},
					Subnets: []NutanixResourceIdentifier{
						{
							Type: NutanixIdentifierName,
							Name: ptr.To("test-subnet"),
						},
					},
				},
			},
		},
	}
}

func TestNutanixMachineTemplateDefaulter_Default(t *testing.T) {
	defaulter := &NutanixMachineTemplateDefaulter{
		DefaultToPlaceholderImageName: true,
		DefaultToPlaceholderImageUUID: true,
	}

	tests := []struct {
		name         string
		image        *NutanixResourceIdentifier
		expectedName *string
		expectedUUID *string
		expectedType NutanixIdentifierType
		description  string
	}{
		{
			name: "type name with missing name gets placeholder",
			image: &NutanixResourceIdentifier{
				Type: NutanixIdentifierName,
			},
			expectedName: ptr.To(ImageNamePlaceholder),
			expectedUUID: nil,
			expectedType: NutanixIdentifierName,
			description:  "brownfield: type=name but name unset should get placeholder",
		},
		{
			name: "type uuid with missing uuid gets placeholder",
			image: &NutanixResourceIdentifier{
				Type: NutanixIdentifierUUID,
			},
			expectedName: nil,
			expectedUUID: ptr.To(ImageUUIDPlaceholder),
			expectedType: NutanixIdentifierUUID,
			description:  "brownfield: type=uuid but uuid unset should get placeholder",
		},
		{
			name: "type name with valid name is preserved",
			image: &NutanixResourceIdentifier{
				Type: NutanixIdentifierName,
				Name: ptr.To("my-image"),
			},
			expectedName: ptr.To("my-image"),
			expectedUUID: nil,
			expectedType: NutanixIdentifierName,
			description:  "valid name must not be overwritten",
		},
		{
			name: "type uuid with valid uuid is preserved",
			image: &NutanixResourceIdentifier{
				Type: NutanixIdentifierUUID,
				UUID: ptr.To("550e8400-e29b-41d4-a716-446655440000"),
			},
			expectedName: nil,
			expectedUUID: ptr.To("550e8400-e29b-41d4-a716-446655440000"),
			expectedType: NutanixIdentifierUUID,
			description:  "valid uuid must not be overwritten",
		},
		{
			name:         "nil image is not modified",
			image:        nil,
			expectedName: nil,
			expectedUUID: nil,
			description:  "nil image should remain nil",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nmt := newTestNutanixMachineTemplate(tt.name, tt.image)
			err := defaulter.Default(context.Background(), nmt)
			require.NoError(t, err)

			if tt.image == nil {
				assert.Nil(t, nmt.Spec.Template.Spec.Image, tt.description)
				return
			}

			require.NotNil(t, nmt.Spec.Template.Spec.Image, tt.description)
			assert.Equal(t, tt.expectedType, nmt.Spec.Template.Spec.Image.Type, tt.description)

			if tt.expectedName != nil {
				require.NotNil(t, nmt.Spec.Template.Spec.Image.Name, tt.description)
				assert.Equal(t, *tt.expectedName, *nmt.Spec.Template.Spec.Image.Name, tt.description)
			} else {
				assert.Nil(t, nmt.Spec.Template.Spec.Image.Name, tt.description)
			}

			if tt.expectedUUID != nil {
				require.NotNil(t, nmt.Spec.Template.Spec.Image.UUID, tt.description)
				assert.Equal(t, *tt.expectedUUID, *nmt.Spec.Template.Spec.Image.UUID, tt.description)
			} else {
				assert.Nil(t, nmt.Spec.Template.Spec.Image.UUID, tt.description)
			}
		})
	}
}

func TestNutanixMachineTemplateDefaulter_Idempotent(t *testing.T) {
	defaulter := &NutanixMachineTemplateDefaulter{
		DefaultToPlaceholderImageName: true,
		DefaultToPlaceholderImageUUID: true,
	}

	// Start with brownfield object (type=name, name=nil)
	nmt := newTestNutanixMachineTemplate("idempotent-test", &NutanixResourceIdentifier{
		Type: NutanixIdentifierName,
	})

	// First default
	err := defaulter.Default(context.Background(), nmt)
	require.NoError(t, err)
	require.NotNil(t, nmt.Spec.Template.Spec.Image.Name)
	assert.Equal(t, ImageNamePlaceholder, *nmt.Spec.Template.Spec.Image.Name)

	// Second default should produce the same result
	err = defaulter.Default(context.Background(), nmt)
	require.NoError(t, err)
	require.NotNil(t, nmt.Spec.Template.Spec.Image.Name)
	assert.Equal(t, ImageNamePlaceholder, *nmt.Spec.Template.Spec.Image.Name)
}

func TestNutanixMachineTemplateDefaulter_UnrelatedFieldsUntouched(t *testing.T) {
	defaulter := &NutanixMachineTemplateDefaulter{
		DefaultToPlaceholderImageName: true,
		DefaultToPlaceholderImageUUID: true,
	}

	nmt := newTestNutanixMachineTemplate("unrelated-fields", &NutanixResourceIdentifier{
		Type: NutanixIdentifierName,
	})

	// Set some specific values on unrelated fields
	originalCluster := nmt.Spec.Template.Spec.Cluster
	originalSubnets := nmt.Spec.Template.Spec.Subnets
	originalVCPUs := nmt.Spec.Template.Spec.VCPUsPerSocket
	originalMemory := nmt.Spec.Template.Spec.MemorySize

	err := defaulter.Default(context.Background(), nmt)
	require.NoError(t, err)

	// Verify unrelated fields were not modified
	assert.Equal(t, originalCluster, nmt.Spec.Template.Spec.Cluster,
		"cluster field should not be modified")
	assert.Equal(t, originalSubnets, nmt.Spec.Template.Spec.Subnets,
		"subnets field should not be modified")
	assert.Equal(t, originalVCPUs, nmt.Spec.Template.Spec.VCPUsPerSocket,
		"vcpusPerSocket should not be modified")
	assert.Equal(t, originalMemory, nmt.Spec.Template.Spec.MemorySize,
		"memorySize should not be modified")
}

func TestNutanixMachineTemplateDefaulter_WrongType(t *testing.T) {
	defaulter := &NutanixMachineTemplateDefaulter{
		DefaultToPlaceholderImageName: true,
		DefaultToPlaceholderImageUUID: true,
	}

	// Passing a non-NutanixMachineTemplate should return an error
	err := defaulter.Default(context.Background(), &NutanixMachine{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expected *NutanixMachineTemplate")
}

func TestNutanixMachineTemplateDefaulter_FeatureGateDisabled(t *testing.T) {
	tests := []struct {
		name        string
		defaulter   *NutanixMachineTemplateDefaulter
		image       *NutanixResourceIdentifier
		description string
	}{
		{
			name: "name defaulting disabled - name stays nil",
			defaulter: &NutanixMachineTemplateDefaulter{
				DefaultToPlaceholderImageName: false,
				DefaultToPlaceholderImageUUID: true,
			},
			image: &NutanixResourceIdentifier{
				Type: NutanixIdentifierName,
			},
			description: "with name feature gate disabled, name should remain nil",
		},
		{
			name: "uuid defaulting disabled - uuid stays nil",
			defaulter: &NutanixMachineTemplateDefaulter{
				DefaultToPlaceholderImageName: true,
				DefaultToPlaceholderImageUUID: false,
			},
			image: &NutanixResourceIdentifier{
				Type: NutanixIdentifierUUID,
			},
			description: "with uuid feature gate disabled, uuid should remain nil",
		},
		{
			name: "both disabled - nothing changes",
			defaulter: &NutanixMachineTemplateDefaulter{
				DefaultToPlaceholderImageName: false,
				DefaultToPlaceholderImageUUID: false,
			},
			image: &NutanixResourceIdentifier{
				Type: NutanixIdentifierName,
			},
			description: "with both feature gates disabled, no defaulting should occur",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nmt := newTestNutanixMachineTemplate(tt.name, tt.image)
			err := tt.defaulter.Default(context.Background(), nmt)
			require.NoError(t, err)
			require.NotNil(t, nmt.Spec.Template.Spec.Image, tt.description)

			assert.Nil(t, nmt.Spec.Template.Spec.Image.Name, tt.description)
			assert.Nil(t, nmt.Spec.Template.Spec.Image.UUID, tt.description)
		})
	}
}

func TestNutanixMachineTemplateDefaulter_FeatureGateIndependent(t *testing.T) {
	// Enable only name defaulting - uuid type should not get defaulted
	defaulter := &NutanixMachineTemplateDefaulter{
		DefaultToPlaceholderImageName: true,
		DefaultToPlaceholderImageUUID: false,
	}

	// Name type should get defaulted
	nmt := newTestNutanixMachineTemplate("name-enabled", &NutanixResourceIdentifier{
		Type: NutanixIdentifierName,
	})
	err := defaulter.Default(context.Background(), nmt)
	require.NoError(t, err)
	require.NotNil(t, nmt.Spec.Template.Spec.Image.Name)
	assert.Equal(t, ImageNamePlaceholder, *nmt.Spec.Template.Spec.Image.Name)

	// UUID type should NOT get defaulted
	nmt = newTestNutanixMachineTemplate("uuid-disabled", &NutanixResourceIdentifier{
		Type: NutanixIdentifierUUID,
	})
	err = defaulter.Default(context.Background(), nmt)
	require.NoError(t, err)
	assert.Nil(t, nmt.Spec.Template.Spec.Image.UUID)
}
