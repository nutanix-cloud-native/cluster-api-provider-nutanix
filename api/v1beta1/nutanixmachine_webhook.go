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

package v1beta1

import (
	"errors"
	"fmt"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"strings"
)

// log is for logging in this package.
var nutanixmachinelog = logf.Log.WithName("nutanixmachine-resource")

func (r *NutanixMachine) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		Complete()
}

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!

//+kubebuilder:webhook:path=/mutate-infrastructure-cluster-x-k8s-io-v1beta1-nutanixmachine,mutating=true,failurePolicy=fail,groups=infrastructure.cluster.x-k8s.io,resources=nutanixmachines,verbs=create;update,versions=v1beta1,name=mnutanixmachine.kb.io,sideEffects=None,admissionReviewVersions=v1;v1beta1

var _ webhook.Defaulter = &NutanixMachine{}

// Nutanix Defaults
// Minimum Nutanix values taken from Nutanix reconciler
const (
	defaultNutanixCredentialsSecret = "nutanix-credentials"
	minNutanixCPUSockets            = 1
	minNutanixCPUPerSocket          = 1
	minNutanixMemoryMiB             = 2048
	minNutanixDiskGiB               = 20
)

// Default implements webhook.Defaulter so a webhook will be registered for the type
func (r *NutanixMachine) Default() {
	nutanixmachinelog.Info("default", "name", r.Name)
}

// TODO(user): change verbs to "verbs=create;update;delete" if you want to enable deletion validation.
//+kubebuilder:webhook:verbs=create;update;delete,path=/validate-infrastructure-cluster-x-k8s-io-v1beta1-nutanixmachine,mutating=false,failurePolicy=fail,groups=infrastructure.cluster.x-k8s.io,resources=nutanixmachines,versions=v1beta1,name=vnutanixmachine.kb.io,sideEffects=None,admissionReviewVersions=v1;v1beta1

var _ webhook.Validator = &NutanixMachine{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (r *NutanixMachine) ValidateCreate() error {
	if err := r.validateMachine("create"); err != nil {
		return err
	}

	return nil
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (r *NutanixMachine) ValidateUpdate(old runtime.Object) error {
	if err := r.validateMachine("update"); err != nil {
		return err
	}

	return nil
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (r *NutanixMachine) ValidateDelete() error {
	if err := r.validateMachine("delete"); err != nil {
		return err
	}

	return nil
}

func (r *NutanixMachine) validateMachine(operation string) error {
	nutanixmachinelog.Info("validate nutanix machine", "name", r.Name, "operation", operation)
	if r.Spec.VCPUSockets < minNutanixCPUSockets {
		return errors.New(fmt.Sprintf("VCPUSockets must be greater than or equal to %d", minNutanixCPUSockets))
	}

	if r.Spec.VCPUsPerSocket < minNutanixCPUPerSocket {
		return errors.New(fmt.Sprintf("VCPUPerSocket must be greater than or equal to %d", minNutanixCPUPerSocket))
	}

	minNutanixMemory, err := resource.ParseQuantity(fmt.Sprintf("%dMi", minNutanixMemoryMiB))
	if err != nil {
		return err
	}

	if r.Spec.MemorySize.Cmp(minNutanixMemory) < 0 {
		return errors.New(fmt.Sprintf("MemoryMiB must be greater than or equal to %d", minNutanixMemoryMiB))
	}

	minNutanixDisk, err := resource.ParseQuantity(fmt.Sprintf("%dGi", minNutanixDiskGiB))
	if err != nil {
		return err
	}

	if r.Spec.SystemDiskSize.Cmp(minNutanixDisk) < 0 {
		return errors.New(fmt.Sprintf("SystemDiskGiB must be greater than or equal to %d", minNutanixDiskGiB))
	}

	if strings.ToLower(r.Spec.BootType) != "uefi" && strings.ToLower(r.Spec.BootType) != "legacy" {
		return errors.New("BootType must be either uefi or legacy")
	}

	if err := validateNutanixResourceIdentifier("cluster", r.Spec.Cluster); err != nil {
		return err
	}

	if err := validateNutanixResourceIdentifier("image", r.Spec.Image); err != nil {
		return err
	}

	for _, subnet := range r.Spec.Subnets {
		if err := validateNutanixResourceIdentifier("subnet", subnet); err != nil {
			return err
		}
	}

	if r.Spec.Project != nil {
		if err := validateNutanixResourceIdentifier("project", *r.Spec.Project); err != nil {
			return err
		}
	}

	return nil
}

func validateNutanixResourceIdentifier(resource string, identifier NutanixResourceIdentifier) error {
	parentPath := field.NewPath("providerSpec")
	switch identifier.Type {
	case NutanixIdentifierName:
		if identifier.Name == nil || *identifier.Name == "" {
			return field.Required(parentPath.Child(resource).Child("name"), fmt.Sprintf("%s name must be provided", resource))
		}
	case NutanixIdentifierUUID:
		if identifier.UUID == nil || *identifier.UUID == "" {
			return field.Required(parentPath.Child(resource).Child("uuid"), fmt.Sprintf("%s UUID must be provided", resource))
		}
	default:
		return field.Invalid(parentPath.Child(resource).Child("type"), identifier.Type, fmt.Sprintf("%s type must be one of %s or %s", resource, NutanixIdentifierName, NutanixIdentifierUUID))
	}

	return nil
}
