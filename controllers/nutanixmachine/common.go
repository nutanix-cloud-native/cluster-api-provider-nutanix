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

	"k8s.io/apimachinery/pkg/runtime"
	coreinformers "k8s.io/client-go/informers/core/v1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"

	"github.com/nutanix-cloud-native/cluster-api-provider-nutanix/controllers"
)

type NutanixMachineScope struct {
	controllers.NutanixClusterScope
	NutanixMachine *infrav1.NutanixMachine
	Machine        *capiv1.Machine
}

func NewNutanixMachineScope(
	nutanixCluster *infrav1.NutanixCluster,
	nutanixMachine *infrav1.NutanixMachine,
	cluster *capiv1.Cluster,
	machine *capiv1.Machine,
) *NutanixMachineScope {
	return &NutanixMachineScope{
		NutanixClusterScope: controllers.NutanixClusterScope{
			Cluster:        cluster,
			NutanixCluster: nutanixCluster,
		},
		NutanixMachine: nutanixMachine,
		Machine:        machine,
	}
}

type NutanixMachineReconciler struct {
	client.Client
	SecretInformer    coreinformers.SecretInformer
	ConfigMapInformer coreinformers.ConfigMapInformer
	Scheme            *runtime.Scheme
	controllerConfig  *controllers.ControllerConfig
}

func NewNutanixMachineReconciler(
	client client.Client,
	secretInformer coreinformers.SecretInformer,
	configMapInformer coreinformers.ConfigMapInformer,
	scheme *runtime.Scheme,
	copts ...controllers.ControllerConfigOpts) (*NutanixMachineReconciler, error) {
	controllerConf := &controllers.ControllerConfig{}
	for _, opt := range copts {
		if err := opt(controllerConf); err != nil {
			return nil, err
		}
	}

	return &NutanixMachineReconciler{
		Client:            client,
		SecretInformer:    secretInformer,
		ConfigMapInformer: configMapInformer,
		Scheme:            scheme,
		controllerConfig:  controllerConf,
	}, nil
}

func (r *NutanixMachineReconciler) NewNutanixMachineUoWBatch(
	uoWs ...controllers.NutanixUnitOfWork[NutanixMachineScope],
) *controllers.NutanixUoWBatch[NutanixMachineScope] {
	return &controllers.NutanixUoWBatch[NutanixMachineScope]{
		UoWs: uoWs,
	}
}

func (r *NutanixMachineReconciler) NewNutanixMachineUoW(
	step capiv1.ConditionType,
	actions map[controllers.PrismCondition]func(nctx controllers.NutanixExtendedContext, scope NutanixMachineScope) (controllers.ExtendedResult, error),
) *controllers.NutanixUoWBatch[NutanixMachineScope] {
	return &controllers.NutanixUoWBatch[NutanixMachineScope]{
		UoWs: []controllers.NutanixUnitOfWork[NutanixMachineScope]{
			{
				Step:      step,
				Actions:   actions,
				OnSuccess: r.DefaultOnSuccess,
				OnFailure: r.DefaultOnFailure,
			},
		},
	}
}

func (r *NutanixMachineReconciler) DefaultOnSuccess(nctx controllers.NutanixExtendedContext, scope NutanixMachineScope, result controllers.ExtendedResult) error {
	log := log.FromContext(nctx.Context)

	conditions.MarkTrue(scope.NutanixMachine, result.Step)

	err := nctx.PatchHelper.Patch(nctx.Context, scope.NutanixMachine)
	if err != nil {
		log.Error(err, "failed to patch NutanixMachine")
		return err
	}
	log.V(1).Info(fmt.Sprintf("Patched NutanixMachine. Spec: %+v. Status: %+v.",
		scope.NutanixMachine.Spec, scope.NutanixMachine.Status))
	return nil
}

func (r *NutanixMachineReconciler) DefaultOnFailure(nctx controllers.NutanixExtendedContext, scope NutanixMachineScope, result controllers.ExtendedResult) error {
	log := log.FromContext(nctx.Context)

	conditionMessage := result.ActionError.Error()

	conditions.MarkFalse(
		scope.NutanixMachine,
		result.Step,
		conditionMessage,
		capiv1.ConditionSeverityError,
		"error occurred while executing step %s %v",
		result.Step,
		result.ActionError,
	)

	err := nctx.PatchHelper.Patch(nctx.Context, scope.NutanixMachine)
	if err != nil {
		log.Error(err, "failed to patch NutanixMachine")
		return err
	}
	log.V(1).Info(fmt.Sprintf("Patched NutanixMachine. Spec: %+v. Status: %+v.",
		scope.NutanixMachine.Spec, scope.NutanixMachine.Status))
	return nil
}
