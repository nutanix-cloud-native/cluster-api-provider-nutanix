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

	"k8s.io/apimachinery/pkg/runtime"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
)

// NutanixMachineTemplateDefaulter implements admission.CustomDefaulter for NutanixMachineTemplate.
type NutanixMachineTemplateDefaulter struct{}

// Default sets placeholder for NutanixResourceIdentifier name fields when empty.
func (NutanixMachineTemplateDefaulter) Default(_ context.Context, obj runtime.Object) error {
	nmt, ok := obj.(*infrav1.NutanixMachineTemplate)
	if !ok {
		return nil
	}
	defaultNutanixMachineSpec(&nmt.Spec.Template.Spec)
	return nil
}
