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
	"encoding/json"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
)

func TestWebhookHandler_MutatesNutanixMachine(t *testing.T) {
	scheme := runtime.NewScheme()
	utilruntime.Must(infrav1.AddToScheme(scheme))

	wh := admission.WithCustomDefaulter(scheme, &infrav1.NutanixMachine{}, &NutanixMachineDefaulter{})
	ctx := context.Background()

	// NutanixMachine with image type=name but name nil (should be defaulted)
	nm := &infrav1.NutanixMachine{
		Spec: infrav1.NutanixMachineSpec{
			VCPUsPerSocket: 1,
			VCPUSockets:    1,
			MemorySize:     resource.MustParse("2Gi"),
			SystemDiskSize: resource.MustParse("20Gi"),
			Image: &infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierName,
				Name: nil, // empty - webhook should set placeholder
			},
			Cluster: infrav1.NutanixResourceIdentifier{Type: infrav1.NutanixIdentifierName},
		},
	}
	nm.APIVersion = "infrastructure.cluster.x-k8s.io/v1beta1"
	nm.Kind = "NutanixMachine"

	raw, err := json.Marshal(nm)
	if err != nil {
		t.Fatalf("marshal NutanixMachine: %v", err)
	}

	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Create,
			Object:    runtime.RawExtension{Raw: raw},
			Kind:      metav1.GroupVersionKind{Group: "infrastructure.cluster.x-k8s.io", Version: "v1beta1", Kind: "NutanixMachine"},
			Resource:  metav1.GroupVersionResource{Group: "infrastructure.cluster.x-k8s.io", Version: "v1beta1", Resource: "nutanixmachines"},
		},
	}

	resp := wh.Handle(ctx, req)

	if !resp.Allowed {
		t.Fatalf("expected webhook to allow request, got allowed=%v, result=%v", resp.Allowed, resp.Result)
	}
	if len(resp.Patches) == 0 {
		t.Fatal("expected webhook to return patches (mutate image name), got no patches")
	}

	// At least one patch should set spec.image.name to the placeholder
	var found bool
	for _, p := range resp.Patches {
		if p.Path == "/spec/image/name" {
			valBytes, _ := json.Marshal(p.Value)
			var val string
			if err := json.Unmarshal(valBytes, &val); err == nil && val == DefaultNamePlaceholder {
				found = true
				break
			}
		}
	}
	if !found {
		t.Errorf("expected a patch for /spec/image/name = %q; patches: %+v", DefaultNamePlaceholder, resp.Patches)
	}
}

func TestWebhookHandler_MutatesNutanixMachineTemplate(t *testing.T) {
	scheme := runtime.NewScheme()
	utilruntime.Must(infrav1.AddToScheme(scheme))

	wh := admission.WithCustomDefaulter(scheme, &infrav1.NutanixMachineTemplate{}, &NutanixMachineTemplateDefaulter{})
	ctx := context.Background()

	nmt := &infrav1.NutanixMachineTemplate{
		Spec: infrav1.NutanixMachineTemplateSpec{
			Template: infrav1.NutanixMachineTemplateResource{
				Spec: infrav1.NutanixMachineSpec{
					VCPUsPerSocket: 1,
					VCPUSockets:    1,
					MemorySize:     resource.MustParse("2Gi"),
					SystemDiskSize: resource.MustParse("20Gi"),
					Image: &infrav1.NutanixResourceIdentifier{
						Type: infrav1.NutanixIdentifierName,
						Name: nil,
					},
					Cluster: infrav1.NutanixResourceIdentifier{Type: infrav1.NutanixIdentifierName},
				},
			},
		},
	}
	nmt.APIVersion = "infrastructure.cluster.x-k8s.io/v1beta1"
	nmt.Kind = "NutanixMachineTemplate"

	raw, err := json.Marshal(nmt)
	if err != nil {
		t.Fatalf("marshal NutanixMachineTemplate: %v", err)
	}

	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Create,
			Object:    runtime.RawExtension{Raw: raw},
			Kind:      metav1.GroupVersionKind{Group: "infrastructure.cluster.x-k8s.io", Version: "v1beta1", Kind: "NutanixMachineTemplate"},
			Resource:  metav1.GroupVersionResource{Group: "infrastructure.cluster.x-k8s.io", Version: "v1beta1", Resource: "nutanixmachinetemplates"},
		},
	}

	resp := wh.Handle(ctx, req)

	if !resp.Allowed {
		t.Fatalf("expected webhook to allow request, got allowed=%v, result=%v", resp.Allowed, resp.Result)
	}
	if len(resp.Patches) == 0 {
		t.Fatal("expected webhook to return patches (mutate template spec image name), got no patches")
	}

	var found bool
	for _, p := range resp.Patches {
		if p.Path == "/spec/template/spec/image/name" {
			valBytes, _ := json.Marshal(p.Value)
			var val string
			if err := json.Unmarshal(valBytes, &val); err == nil && val == DefaultNamePlaceholder {
				found = true
				break
			}
		}
	}
	if !found {
		t.Errorf("expected a patch for /spec/template/spec/image/name = %q; patches: %+v", DefaultNamePlaceholder, resp.Patches)
	}
}
