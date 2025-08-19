/*
Copyright 2025 Nutanix

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

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	"github.com/nutanix-cloud-native/prism-go-client/facade"
	prismclientv3 "github.com/nutanix-cloud-native/prism-go-client/v3"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	corev1 "k8s.io/client-go/informers/core/v1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type PrismCondition string

const (
	PrismConditionV3andV4FacadeClientReady PrismCondition = "V3andV4ClientReady"
	PrismConditionV3OnlyClientReady        PrismCondition = "V3OnlyClientReady"
	PrismConditionNoClientsReady           PrismCondition = "NoClientsReady"
)

type NutanixClients struct {
	V3Client *prismclientv3.Client
	V4Facade facade.FacadeClientV4
}

type Informers struct {
	SecretInformer    corev1.SecretInformer
	ConfigMapInformer corev1.ConfigMapInformer
}

func CreateNutanixClients(ctx context.Context, ntnxCluster *infrav1.NutanixCluster, informers Informers) (*NutanixClients, error) {
	log := ctrl.LoggerFrom(ctx)

	var err error
	clients := &NutanixClients{}
	clients.V3Client, err = getPrismCentralClientForCluster(ctx, ntnxCluster, informers.SecretInformer, informers.ConfigMapInformer)
	if err != nil {
		log.Error(err, "error occurred while getting prism central v3 client")
		clients.V3Client = nil
	}

	clients.V4Facade, err = getPrismCentralV4FacadeClientForCluster(ctx, ntnxCluster, informers.SecretInformer, informers.ConfigMapInformer)
	if err != nil {
		log.Error(err, "error occurred while getting prism central v4 facade client")
		clients.V4Facade = nil
	}

	return clients, nil
}

func (clients *NutanixClients) GetPrismCondition() PrismCondition {
	if clients.V3Client != nil && clients.V4Facade != nil {
		return PrismConditionV3andV4FacadeClientReady
	}

	if clients.V3Client != nil {
		return PrismConditionV3OnlyClientReady
	}

	return PrismConditionNoClientsReady
}

type ExtendedContext struct {
	Context     context.Context
	Client      client.Client
	PatchHelper *patch.Helper
}

func (c *ExtendedContext) GetClient() client.Client {
	return c.Client
}

func (c *ExtendedContext) GetContext() context.Context {
	return c.Context
}

func (c *ExtendedContext) GetPatchHelper() *patch.Helper {
	return c.PatchHelper
}

type NutanixExtendedContext struct {
	ExtendedContext
	NutanixClients *NutanixClients
	PrismCondition PrismCondition
}

func (ntnxCtx *NutanixExtendedContext) GetV3Client() *prismclientv3.Client {
	return ntnxCtx.NutanixClients.V3Client
}

func (ntnxCtx *NutanixExtendedContext) GetV4FacadeClient() facade.FacadeClientV4 {
	return ntnxCtx.NutanixClients.V4Facade
}

type NutanixClusterScope struct {
	Cluster        *capiv1.Cluster
	NutanixCluster *infrav1.NutanixCluster
}

func (scope *NutanixClusterScope) GetNutanixCluster() *infrav1.NutanixCluster {
	return scope.NutanixCluster
}

type ExtendedResult struct {
	Step   capiv1.ConditionType
	Result reconcile.Result

	StopReconcile  bool
	ShouldFallback bool

	ActionError error
}

type NutanixUnitOfWork[T any] struct {
	Step      capiv1.ConditionType
	Actions   map[PrismCondition]func(nctx *NutanixExtendedContext, scope *T) (ExtendedResult, error)
	OnSuccess func(nctx *NutanixExtendedContext, scope *T, result ExtendedResult) error
	OnFailure func(nctx *NutanixExtendedContext, scope *T, result ExtendedResult) error
}

func (uow *NutanixUnitOfWork[T]) Execute(nctx *NutanixExtendedContext, scope *T) (ExtendedResult, error) {
	var result ExtendedResult
	var err error

	log := ctrl.LoggerFrom(nctx.Context)
	log.Info("Executing unit of work", "step", uow.Step)

	prismCondition := nctx.PrismCondition

	action, ok := uow.Actions[prismCondition]
	if !ok {
		return result, fmt.Errorf("no action found for prism condition %s", prismCondition)
	}

	result, err = action(nctx, scope)
	if err != nil {
		log.Error(err, "error occurred while executing action for step", "step", uow.Step)
		result.ActionError = err
	}

	result.Step = uow.Step

	postError := uow.PostExecute(nctx, scope, result)
	if postError != nil {
		log.Error(postError, "error occurred while executing post execute for step", "step", uow.Step)
		err = kerrors.NewAggregate([]error{err, postError})
	}

	return result, err
}

func (uow *NutanixUnitOfWork[T]) PostExecute(nctx *NutanixExtendedContext, scope *T, result ExtendedResult) error {
	log := ctrl.LoggerFrom(nctx.Context)
	if result.ActionError == nil {
		if uow.OnSuccess != nil {
			if err := uow.OnSuccess(nctx, scope, result); err != nil {
				log.Error(err, "error occurred while executing on success for step", "step", uow.Step)
				return err
			}
		}
	} else {
		if uow.OnFailure != nil {
			if err := uow.OnFailure(nctx, scope, result); err != nil {
				log.Error(err, "error occurred while executing on failure for step", "step", uow.Step)
				return err
			}
		}
	}
	return nil
}

type NutanixUoWBatch[T any] struct {
	UoWs []*NutanixUnitOfWork[T]
}

func (batch *NutanixUoWBatch[T]) Execute(nctx *NutanixExtendedContext, scope *T) (reconcile.Result, error) {
	var lastResult ExtendedResult
	var err error

	for _, uow := range batch.UoWs {
		lastResult, err := uow.Execute(nctx, scope)
		if lastResult.StopReconcile {
			return lastResult.Result, err
		}

		if err != nil {
			return lastResult.Result, err
		}

		if lastResult.Result.RequeueAfter > 0 {
			return lastResult.Result, err
		}

		if lastResult.Result.Requeue {
			return lastResult.Result, err
		}
	}

	return lastResult.Result, err
}
