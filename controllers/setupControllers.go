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

package controllers

import (
	"context"
	"fmt"

	coreinformers "k8s.io/client-go/informers/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

// SetupNutanixVMAntiAffinityPolicyController sets up the NutanixVMAntiAffinityPolicy controller with the Manager.
func SetupNutanixVMAntiAffinityPolicyController(ctx context.Context, mgr manager.Manager, secretInformer coreinformers.SecretInformer,
	configMapInformer coreinformers.ConfigMapInformer, opts ...ControllerConfigOpts,
) error {
	policyCtrl, err := NewNutanixVMAntiAffinityPolicyReconciler(
		mgr.GetClient(),
		secretInformer,
		configMapInformer,
		mgr.GetScheme(),
		opts...,
	)
	if err != nil {
		return fmt.Errorf("unable to create NutanixVMAntiAffinityPolicy controller: %w", err)
	}

	if err := policyCtrl.SetupWithManager(ctx, mgr); err != nil {
		return fmt.Errorf("unable to setup NutanixVMAntiAffinityPolicy controller with manager: %w", err)
	}

	return nil
}
