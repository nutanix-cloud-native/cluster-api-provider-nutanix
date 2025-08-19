/*
Copyright 2025 Nutanix Inc.

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
	"github.com/nutanix-cloud-native/cluster-api-provider-nutanix/controllers"
)

func (r *NutanixMachineReconciler) GetUoWNormalBatch() *controllers.NutanixUoWBatch[NutanixMachineScope] {
	NutanixMachineUoWAddFinalizer := r.NewNutanixMachineUoW(
		NutanixMachineAddFinalizer,
		map[controllers.PrismCondition]func(nctx *controllers.NutanixExtendedContext, scope *NutanixMachineScope) (controllers.ExtendedResult, error){
			controllers.PrismConditionV3andV4FacadeClientReady: r.AddFinalizer,
			controllers.PrismConditionV3OnlyClientReady:        r.AddFinalizer,
			controllers.PrismConditionNoClientsReady:           r.FatalPrismCondtion,
		},
	)

	NutanixMachineProviderIdReady := r.NewNutanixMachineUoW(
		NutanixMachineProviderIdReady,
		map[controllers.PrismCondition]func(nctx *controllers.NutanixExtendedContext, scope *NutanixMachineScope) (controllers.ExtendedResult, error){},
	)

	NutanixMachineClusterInfrastructureReady := r.NewNutanixMachineUoW(
		NutanixMachineClusterInfrastructureReady,
		map[controllers.PrismCondition]func(nctx *controllers.NutanixExtendedContext, scope *NutanixMachineScope) (controllers.ExtendedResult, error){},
	)

	NutanixMachineBootstrapDataReady := r.NewNutanixMachineUoW(
		NutanixMachineBootstrapDataReady,
		map[controllers.PrismCondition]func(nctx *controllers.NutanixExtendedContext, scope *NutanixMachineScope) (controllers.ExtendedResult, error){},
	)

	NutanixMachineVMReady := r.NewNutanixMachineUoW(
		NutanixMachineVMReady,
		map[controllers.PrismCondition]func(nctx *controllers.NutanixExtendedContext, scope *NutanixMachineScope) (controllers.ExtendedResult, error){},
	)

	NutanixMachineFailureDomainReady := r.NewNutanixMachineUoW(
		NutanixMachineFailureDomainReady,
		map[controllers.PrismCondition]func(nctx *controllers.NutanixExtendedContext, scope *NutanixMachineScope) (controllers.ExtendedResult, error){},
	)

	NutanixMachineIPAddressesAssigned := r.NewNutanixMachineUoW(
		NutanixMachineIPAddressesAssigned,
		map[controllers.PrismCondition]func(nctx *controllers.NutanixExtendedContext, scope *NutanixMachineScope) (controllers.ExtendedResult, error){},
	)

	return r.NewNutanixMachineUoWBatch(
		NutanixMachineUoWAddFinalizer,
		NutanixMachineProviderIdReady,
		NutanixMachineClusterInfrastructureReady,
		NutanixMachineBootstrapDataReady,
		NutanixMachineVMReady,
		NutanixMachineFailureDomainReady,
		NutanixMachineIPAddressesAssigned,
	)
}

func (r *NutanixMachineReconciler) GetUoWDeleteBatch() *controllers.NutanixUoWBatch[NutanixMachineScope] {
	NutanixMachineVmNeedsDeletion := r.NewNutanixMachineUoW(
		NutanixMachineVmNeedsDeletion,
		map[controllers.PrismCondition]func(nctx *controllers.NutanixExtendedContext, scope *NutanixMachineScope) (controllers.ExtendedResult, error){},
	)

	NutanixMachineVmDelete := r.NewNutanixMachineUoW(
		NutanixMachineVmDelete,
		map[controllers.PrismCondition]func(nctx *controllers.NutanixExtendedContext, scope *NutanixMachineScope) (controllers.ExtendedResult, error){},
	)

	NutanixMachineFinalizerRemoved := r.NewNutanixMachineUoW(
		NutanixMachineFinalizerRemoved,
		map[controllers.PrismCondition]func(nctx *controllers.NutanixExtendedContext, scope *NutanixMachineScope) (controllers.ExtendedResult, error){},
	)

	NutanixMachineVolumeGroupDetachNeeded := r.NewNutanixMachineUoW(
		NutanixMachineVolumeGroupDetachNeeded,
		map[controllers.PrismCondition]func(nctx *controllers.NutanixExtendedContext, scope *NutanixMachineScope) (controllers.ExtendedResult, error){},
	)

	NutanixMachineVolumeGroupDetach := r.NewNutanixMachineUoW(
		NutanixMachineVolumeGroupDetach,
		map[controllers.PrismCondition]func(nctx *controllers.NutanixExtendedContext, scope *NutanixMachineScope) (controllers.ExtendedResult, error){},
	)

	return r.NewNutanixMachineUoWBatch(
		NutanixMachineVmNeedsDeletion,
		NutanixMachineVmDelete,
		NutanixMachineFinalizerRemoved,
		NutanixMachineVolumeGroupDetachNeeded,
		NutanixMachineVolumeGroupDetach,
	)
}
