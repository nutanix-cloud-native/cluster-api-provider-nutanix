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
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

// Placeholder values for brownfield compatibility.
// These are injected by the defaulting webhook when a NutanixMachineTemplate has
// spec.template.spec.image with type set but the corresponding name/uuid value missing.
// This can happen with legacy objects created before the CEL validation rules were added.
// The placeholders satisfy CEL validation at admission time; the actual image resolution
// is expected to be handled later by ClusterClass variable patches or the controller.
const (
	// ImageNamePlaceholder is the placeholder value set for image.name when type is "name"
	// but name is not provided. It is clearly identifiable as a non-real value.
	ImageNamePlaceholder = "PLACEHOLDER"

	// ImageUUIDPlaceholder is the placeholder UUID set for image.uuid when type is "uuid"
	// but uuid is not provided. It uses the nil UUID format (all zeros) which is a valid
	// UUID format that satisfies the CEL rule requiring 36 characters and a dash.
	ImageUUIDPlaceholder = "00000000-0000-0000-0000-000000000000"
)

// NutanixMachineTemplateDefaulter implements a defaulting webhook for NutanixMachineTemplate.
//
// This is a brownfield compatibility workaround: existing NutanixMachineTemplate objects
// in production may have spec.template.spec.image with type set to "name" or "uuid" but
// the corresponding value field (name or uuid) unset. The CEL validation rules on
// NutanixResourceIdentifier require these fields to be present. This webhook fills in
// placeholder values at admission time so the CEL rules pass, without weakening the
// API contract globally.
//
// The defaulter is idempotent and will not overwrite valid user-provided values.
type NutanixMachineTemplateDefaulter struct{}

var _ webhook.CustomDefaulter = &NutanixMachineTemplateDefaulter{}

// SetupWebhookWithManager registers the defaulting webhook for NutanixMachineTemplate.
//
// +kubebuilder:webhook:path=/mutate-infrastructure-cluster-x-k8s-io-v1beta1-nutanixmachinetemplate,mutating=true,failurePolicy=fail,sideEffects=None,groups=infrastructure.cluster.x-k8s.io,resources=nutanixmachinetemplates,verbs=create;update,versions=v1beta1,name=default.nutanixmachinetemplate.infrastructure.cluster.x-k8s.io,admissionReviewVersions=v1
func (d *NutanixMachineTemplateDefaulter) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(&NutanixMachineTemplate{}).
		WithDefaulter(d).
		Complete()
}

// Default implements webhook.CustomDefaulter.
// It normalizes spec.template.spec.image for brownfield NutanixMachineTemplate objects
// that have the image type set but the corresponding identifier value missing.
func (d *NutanixMachineTemplateDefaulter) Default(_ context.Context, obj runtime.Object) error {
	nmt, ok := obj.(*NutanixMachineTemplate)
	if !ok {
		return fmt.Errorf("expected *NutanixMachineTemplate, got %T", obj)
	}

	defaultNutanixMachineTemplateImage(nmt)
	return nil
}

// defaultNutanixMachineTemplateImage applies placeholder defaults to
// spec.template.spec.image when the identifier value is missing.
// This function is scoped narrowly to the image field only.
func defaultNutanixMachineTemplateImage(nmt *NutanixMachineTemplate) {
	image := nmt.Spec.Template.Spec.Image
	if image == nil {
		return
	}

	switch image.Type {
	case NutanixIdentifierName:
		if image.Name == nil {
			placeholder := ImageNamePlaceholder
			image.Name = &placeholder
		}
	case NutanixIdentifierUUID:
		if image.UUID == nil {
			placeholder := ImageUUIDPlaceholder
			image.UUID = &placeholder
		}
	}
}
