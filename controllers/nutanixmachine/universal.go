/*
Copyright 2025 Nutanix, Inc.

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
package nutanixmachine

import (
	"fmt"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"
	ctrlutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	"github.com/nutanix-cloud-native/cluster-api-provider-nutanix/controllers"
)

func (r *NutanixMachineReconciler) FatalPrismCondtion(nctx *controllers.NutanixExtendedContext, scope *NutanixMachineScope) (controllers.ExtendedResult, error) {
	log := ctrl.LoggerFrom(nctx.Context)

	err := fmt.Errorf("no supported API found for NutanixMachine reconciliation")
	log.Error(err, "no supported API found for NutanixMachine reconciliation")

	return controllers.ExtendedResult{
		Result:        reconcile.Result{RequeueAfter: 5 * time.Minute},
		StopReconcile: true,
		ActionError:   err,
	}, err
}

func (r *NutanixMachineReconciler) AddFinalizer(nctx *controllers.NutanixExtendedContext, scope *NutanixMachineScope) (controllers.ExtendedResult, error) {
	// Add finalizer first if not exist to avoid the race condition
	if !ctrlutil.ContainsFinalizer(scope.NutanixMachine, infrav1.NutanixMachineFinalizer) {
		updated := ctrlutil.AddFinalizer(scope.NutanixMachine, infrav1.NutanixMachineFinalizer)
		if !updated {
			err := fmt.Errorf("failed to add finalizer")
			return controllers.ExtendedResult{
				Result:      reconcile.Result{},
				ActionError: err,
			}, err
		}
	}

	// Remove deprecated finalizer
	ctrlutil.RemoveFinalizer(scope.NutanixMachine, infrav1.DeprecatedNutanixMachineFinalizer)
	return controllers.ExtendedResult{
		Result: reconcile.Result{},
	}, nil
}
