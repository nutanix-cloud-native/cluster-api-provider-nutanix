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
	"errors"
	"testing"

	mocknutanixv4 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/mocks/nutanixv4"
	prismclientv3 "github.com/nutanix-cloud-native/prism-go-client/v3"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestNutanixUoWBatchExecuteNormal(t *testing.T) {
	step1 := capiv1.ConditionType("test-step-1")
	step2 := capiv1.ConditionType("test-step-2")

	stepResults := map[capiv1.ConditionType]bool{}
	nctx := NutanixExtendedContext{
		ExtendedContext: ExtendedContext{
			Context: context.Background(),
		},
		NutanixClients: &NutanixClients{
			V3Client: &prismclientv3.Client{},
			V4Facade: &mocknutanixv4.MockFacadeClientV4{},
		},
	}

	defaultOnSuccess := func(nctx NutanixExtendedContext, scope NutanixClusterScope, result ExtendedResult) error {
		stepResults[result.Step] = true
		return nil
	}

	defaultOnFailure := func(nctx NutanixExtendedContext, scope NutanixClusterScope, result ExtendedResult) error {
		stepResults[result.Step] = false
		return nil
	}

	batch := NutanixUoWBatch[NutanixClusterScope]{
		UoWs: []NutanixUnitOfWork[NutanixClusterScope]{
			{
				Step: step1,
				Actions: map[PrismCondition]func(nctx NutanixExtendedContext, scope NutanixClusterScope) (ExtendedResult, error){
					PrismConditionV3andV4FacadeClientReady: func(nctx NutanixExtendedContext, scope NutanixClusterScope) (ExtendedResult, error) {
						return ExtendedResult{}, nil
					},
				},
				OnSuccess: defaultOnSuccess,
				OnFailure: defaultOnFailure,
			},
			{
				Step: step2,
				Actions: map[PrismCondition]func(nctx NutanixExtendedContext, scope NutanixClusterScope) (ExtendedResult, error){
					PrismConditionV3andV4FacadeClientReady: func(nctx NutanixExtendedContext, scope NutanixClusterScope) (ExtendedResult, error) {
						return ExtendedResult{}, nil
					},
				},
				OnSuccess: defaultOnSuccess,
				OnFailure: defaultOnFailure,
			},
		},
	}

	_, err := batch.Execute(nctx, NutanixClusterScope{})
	if err != nil {
		t.Fatalf("error executing batch: %v", err)
	}

	for step, result := range stepResults {
		if !result {
			t.Fatalf("step %s failed", step)
		}
	}
}

func TestNutanixUoWBatchExecuteWithStopReconcile(t *testing.T) {
	step1 := capiv1.ConditionType("test-step-1")
	step2 := capiv1.ConditionType("test-step-2")

	stepResults := map[capiv1.ConditionType]bool{}

	defaultOnSuccess := func(nctx NutanixExtendedContext, scope NutanixClusterScope, result ExtendedResult) error {
		stepResults[result.Step] = true
		return nil
	}

	defaultOnFailure := func(nctx NutanixExtendedContext, scope NutanixClusterScope, result ExtendedResult) error {
		stepResults[result.Step] = false
		return nil
	}

	batch := NutanixUoWBatch[NutanixClusterScope]{
		UoWs: []NutanixUnitOfWork[NutanixClusterScope]{
			{
				Step: step1,
				Actions: map[PrismCondition]func(nctx NutanixExtendedContext, scope NutanixClusterScope) (ExtendedResult, error){
					PrismConditionV3andV4FacadeClientReady: func(nctx NutanixExtendedContext, scope NutanixClusterScope) (ExtendedResult, error) {
						return ExtendedResult{StopReconcile: true}, nil
					},
				},
				OnSuccess: defaultOnSuccess,
				OnFailure: defaultOnFailure,
			},
			{
				Step: step2,
				Actions: map[PrismCondition]func(nctx NutanixExtendedContext, scope NutanixClusterScope) (ExtendedResult, error){
					PrismConditionV3andV4FacadeClientReady: func(nctx NutanixExtendedContext, scope NutanixClusterScope) (ExtendedResult, error) {
						return ExtendedResult{}, nil
					},
				},
				OnSuccess: defaultOnSuccess,
				OnFailure: defaultOnFailure,
			},
		},
	}

	nctx := NutanixExtendedContext{
		ExtendedContext: ExtendedContext{
			Context: context.Background(),
		},
		NutanixClients: &NutanixClients{
			V3Client: &prismclientv3.Client{},
			V4Facade: &mocknutanixv4.MockFacadeClientV4{},
		},
	}
	_, err := batch.Execute(nctx, NutanixClusterScope{})
	if err != nil {
		t.Fatalf("error executing batch: %v", err)
	}

	// Only step1 should have executed and succeeded
	if len(stepResults) != 1 {
		t.Fatalf("expected only 1 step to execute, got %d", len(stepResults))
	}

	if !stepResults[step1] {
		t.Fatalf("step1 should have succeeded")
	}

	// step2 should not have executed
	if _, exists := stepResults[step2]; exists {
		t.Fatalf("step2 should not have executed")
	}
}

func TestNutanixUoWBatchExecuteWithError(t *testing.T) {
	step1 := capiv1.ConditionType("test-step-1")
	step2 := capiv1.ConditionType("test-step-2")

	stepResults := map[capiv1.ConditionType]bool{}

	defaultOnSuccess := func(nctx NutanixExtendedContext, scope NutanixClusterScope, result ExtendedResult) error {
		stepResults[result.Step] = true
		return nil
	}

	defaultOnFailure := func(nctx NutanixExtendedContext, scope NutanixClusterScope, result ExtendedResult) error {
		stepResults[result.Step] = false
		return nil
	}

	batch := NutanixUoWBatch[NutanixClusterScope]{
		UoWs: []NutanixUnitOfWork[NutanixClusterScope]{
			{
				Step: step1,
				Actions: map[PrismCondition]func(nctx NutanixExtendedContext, scope NutanixClusterScope) (ExtendedResult, error){
					PrismConditionV3andV4FacadeClientReady: func(nctx NutanixExtendedContext, scope NutanixClusterScope) (ExtendedResult, error) {
						return ExtendedResult{}, errors.New("test error")
					},
				},
				OnSuccess: defaultOnSuccess,
				OnFailure: defaultOnFailure,
			},
			{
				Step: step2,
				Actions: map[PrismCondition]func(nctx NutanixExtendedContext, scope NutanixClusterScope) (ExtendedResult, error){
					PrismConditionV3andV4FacadeClientReady: func(nctx NutanixExtendedContext, scope NutanixClusterScope) (ExtendedResult, error) {
						return ExtendedResult{}, nil
					},
				},
				OnSuccess: defaultOnSuccess,
				OnFailure: defaultOnFailure,
			},
		},
	}

	nctx := NutanixExtendedContext{
		ExtendedContext: ExtendedContext{
			Context: context.Background(),
		},
		NutanixClients: &NutanixClients{
			V3Client: &prismclientv3.Client{},
			V4Facade: &mocknutanixv4.MockFacadeClientV4{},
		},
	}
	_, err := batch.Execute(nctx, NutanixClusterScope{})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}

	if stepResults[step1] {
		t.Fatalf("step1 should have failed")
	}

	if stepResults[step2] {
		t.Fatalf("step2 should not have executed")
	}

	if err.Error() != "test error" {
		t.Fatalf("expected error message 'test error', got '%s'", err.Error())
	}
}

func TestNutanixUoWBatchExecuteWithRequeue(t *testing.T) {
	step1 := capiv1.ConditionType("test-step-1")
	step2 := capiv1.ConditionType("test-step-2")

	stepResults := map[capiv1.ConditionType]bool{}

	defaultOnSuccess := func(nctx NutanixExtendedContext, scope NutanixClusterScope, result ExtendedResult) error {
		stepResults[result.Step] = true
		return nil
	}

	defaultOnFailure := func(nctx NutanixExtendedContext, scope NutanixClusterScope, result ExtendedResult) error {
		stepResults[result.Step] = false
		return nil
	}

	batch := NutanixUoWBatch[NutanixClusterScope]{
		UoWs: []NutanixUnitOfWork[NutanixClusterScope]{
			{
				Step: step1,
				Actions: map[PrismCondition]func(nctx NutanixExtendedContext, scope NutanixClusterScope) (ExtendedResult, error){
					PrismConditionV3andV4FacadeClientReady: func(nctx NutanixExtendedContext, scope NutanixClusterScope) (ExtendedResult, error) {
						return ExtendedResult{Result: reconcile.Result{Requeue: true}}, nil
					},
				},
				OnSuccess: defaultOnSuccess,
				OnFailure: defaultOnFailure,
			},
			{
				Step: step2,
				Actions: map[PrismCondition]func(nctx NutanixExtendedContext, scope NutanixClusterScope) (ExtendedResult, error){
					PrismConditionV3andV4FacadeClientReady: func(nctx NutanixExtendedContext, scope NutanixClusterScope) (ExtendedResult, error) {
						return ExtendedResult{}, nil
					},
				},
				OnSuccess: defaultOnSuccess,
				OnFailure: defaultOnFailure,
			},
		},
	}

	nctx := NutanixExtendedContext{
		ExtendedContext: ExtendedContext{
			Context: context.Background(),
		},
		NutanixClients: &NutanixClients{
			V3Client: &prismclientv3.Client{},
			V4Facade: &mocknutanixv4.MockFacadeClientV4{},
		},
	}
	_, err := batch.Execute(nctx, NutanixClusterScope{})
	if err != nil {
		t.Fatalf("error executing batch: %v", err)
	}

	if !stepResults[step1] {
		t.Fatalf("step1 should have executed and succeeded")
	}

	if stepResults[step2] {
		t.Fatalf("step2 should not have executed")
	}

}
