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
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/config"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	mockctlclient "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/mocks/ctlclient"
	mockmeta "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/mocks/k8sapimachinery"
)

func newTestScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = infrav1.AddToScheme(scheme)
	return scheme
}

func newNutanixMachineTemplate(name, namespace string, spec infrav1.NutanixMachineSpec) *infrav1.NutanixMachineTemplate {
	return &infrav1.NutanixMachineTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: infrav1.NutanixMachineTemplateSpec{
			Template: infrav1.NutanixMachineTemplateResource{
				Spec: spec,
			},
		},
	}
}

func assertQuantityEqual(t *testing.T, expected, actual resource.Quantity) {
	t.Helper()
	assert.True(t, expected.Equal(actual), "expected %s, got %s", expected.String(), actual.String())
}

func TestComputeDirectCapacity(t *testing.T) {
	t.Run("computes CPU and memory from inline spec", func(t *testing.T) {
		spec := infrav1.NutanixMachineSpec{
			VCPUsPerSocket: 4,
			VCPUSockets:    2,
			MemorySize:     resource.MustParse("8Gi"),
		}

		capacity := make(corev1.ResourceList)
		computeDirectCapacity(spec, capacity)

		assertQuantityEqual(t, *resource.NewQuantity(8, resource.DecimalSI), capacity[corev1.ResourceCPU])
		assertQuantityEqual(t, resource.MustParse("8Gi"), capacity[corev1.ResourceMemory])
		_, hasGPU := capacity[corev1.ResourceName("nvidia.com/gpu")]
		assert.False(t, hasGPU)
	})

	t.Run("includes GPU count when GPUs are specified", func(t *testing.T) {
		spec := infrav1.NutanixMachineSpec{
			VCPUsPerSocket: 8,
			VCPUSockets:    2,
			MemorySize:     resource.MustParse("64Gi"),
			GPUs: []infrav1.NutanixGPU{
				{Type: infrav1.NutanixGPUIdentifierName, Name: strPtr("NVIDIA Tesla V100")},
				{Type: infrav1.NutanixGPUIdentifierName, Name: strPtr("NVIDIA Tesla V100")},
			},
		}

		capacity := make(corev1.ResourceList)
		computeDirectCapacity(spec, capacity)

		assertQuantityEqual(t, *resource.NewQuantity(16, resource.DecimalSI), capacity[corev1.ResourceCPU])
		assertQuantityEqual(t, resource.MustParse("64Gi"), capacity[corev1.ResourceMemory])
		assertQuantityEqual(t, *resource.NewQuantity(2, resource.DecimalSI), capacity[corev1.ResourceName("nvidia.com/gpu")])
	})

	t.Run("handles single socket single core", func(t *testing.T) {
		spec := infrav1.NutanixMachineSpec{
			VCPUsPerSocket: 1,
			VCPUSockets:    1,
			MemorySize:     resource.MustParse("2Gi"),
		}

		capacity := make(corev1.ResourceList)
		computeDirectCapacity(spec, capacity)

		assertQuantityEqual(t, *resource.NewQuantity(1, resource.DecimalSI), capacity[corev1.ResourceCPU])
		assertQuantityEqual(t, resource.MustParse("2Gi"), capacity[corev1.ResourceMemory])
	})
}

func TestNutanixMachineTemplateReconciler_Reconcile(t *testing.T) {
	t.Run("sets status.capacity for standard template", func(t *testing.T) {
		scheme := newTestScheme()
		nmt := newNutanixMachineTemplate("worker-template", "default", infrav1.NutanixMachineSpec{
			VCPUsPerSocket: 4,
			VCPUSockets:    2,
			MemorySize:     resource.MustParse("8Gi"),
		})

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(nmt).
			WithStatusSubresource(nmt).
			Build()

		reconciler := &NutanixMachineTemplateReconciler{
			Client:           fakeClient,
			Scheme:           scheme,
			controllerConfig: &ControllerConfig{},
		}

		result, err := reconciler.Reconcile(context.Background(), ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name:      "worker-template",
				Namespace: "default",
			},
		})

		require.NoError(t, err)
		assert.Equal(t, ctrl.Result{}, result)

		updated := &infrav1.NutanixMachineTemplate{}
		err = fakeClient.Get(context.Background(), types.NamespacedName{
			Name:      "worker-template",
			Namespace: "default",
		}, updated)
		require.NoError(t, err)

		assertQuantityEqual(t, *resource.NewQuantity(8, resource.DecimalSI), updated.Status.Capacity[corev1.ResourceCPU])
		assertQuantityEqual(t, resource.MustParse("8Gi"), updated.Status.Capacity[corev1.ResourceMemory])
	})

	t.Run("sets status.capacity with GPUs", func(t *testing.T) {
		scheme := newTestScheme()
		nmt := newNutanixMachineTemplate("gpu-template", "default", infrav1.NutanixMachineSpec{
			VCPUsPerSocket: 8,
			VCPUSockets:    2,
			MemorySize:     resource.MustParse("64Gi"),
			GPUs: []infrav1.NutanixGPU{
				{Type: infrav1.NutanixGPUIdentifierName, Name: strPtr("NVIDIA Tesla V100")},
				{Type: infrav1.NutanixGPUIdentifierName, Name: strPtr("NVIDIA Tesla V100")},
			},
		})

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(nmt).
			WithStatusSubresource(nmt).
			Build()

		reconciler := &NutanixMachineTemplateReconciler{
			Client:           fakeClient,
			Scheme:           scheme,
			controllerConfig: &ControllerConfig{},
		}

		result, err := reconciler.Reconcile(context.Background(), ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name:      "gpu-template",
				Namespace: "default",
			},
		})

		require.NoError(t, err)
		assert.Equal(t, ctrl.Result{}, result)

		updated := &infrav1.NutanixMachineTemplate{}
		err = fakeClient.Get(context.Background(), types.NamespacedName{
			Name:      "gpu-template",
			Namespace: "default",
		}, updated)
		require.NoError(t, err)

		assertQuantityEqual(t, *resource.NewQuantity(16, resource.DecimalSI), updated.Status.Capacity[corev1.ResourceCPU])
		assertQuantityEqual(t, resource.MustParse("64Gi"), updated.Status.Capacity[corev1.ResourceMemory])
		assertQuantityEqual(t, *resource.NewQuantity(2, resource.DecimalSI), updated.Status.Capacity[corev1.ResourceName("nvidia.com/gpu")])
	})

	t.Run("returns not found gracefully for missing template", func(t *testing.T) {
		scheme := newTestScheme()
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			Build()

		reconciler := &NutanixMachineTemplateReconciler{
			Client:           fakeClient,
			Scheme:           scheme,
			controllerConfig: &ControllerConfig{},
		}

		result, err := reconciler.Reconcile(context.Background(), ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name:      "nonexistent",
				Namespace: "default",
			},
		})

		require.NoError(t, err)
		assert.Equal(t, ctrl.Result{}, result)
	})

	t.Run("is idempotent when capacity already matches", func(t *testing.T) {
		scheme := newTestScheme()
		nmt := newNutanixMachineTemplate("worker-template", "default", infrav1.NutanixMachineSpec{
			VCPUsPerSocket: 4,
			VCPUSockets:    2,
			MemorySize:     resource.MustParse("8Gi"),
		})
		nmt.Status.Capacity = corev1.ResourceList{
			corev1.ResourceCPU:    *resource.NewQuantity(8, resource.DecimalSI),
			corev1.ResourceMemory: resource.MustParse("8Gi"),
		}

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(nmt).
			WithStatusSubresource(nmt).
			Build()

		reconciler := &NutanixMachineTemplateReconciler{
			Client:           fakeClient,
			Scheme:           scheme,
			controllerConfig: &ControllerConfig{},
		}

		result, err := reconciler.Reconcile(context.Background(), ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name:      "worker-template",
				Namespace: "default",
			},
		})

		require.NoError(t, err)
		assert.Equal(t, ctrl.Result{}, result)
	})

	t.Run("updates capacity when spec changes", func(t *testing.T) {
		scheme := newTestScheme()
		nmt := newNutanixMachineTemplate("worker-template", "default", infrav1.NutanixMachineSpec{
			VCPUsPerSocket: 4,
			VCPUSockets:    2,
			MemorySize:     resource.MustParse("8Gi"),
		})
		nmt.Status.Capacity = corev1.ResourceList{
			corev1.ResourceCPU:    *resource.NewQuantity(4, resource.DecimalSI),
			corev1.ResourceMemory: resource.MustParse("4Gi"),
		}

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(nmt).
			WithStatusSubresource(nmt).
			Build()

		reconciler := &NutanixMachineTemplateReconciler{
			Client:           fakeClient,
			Scheme:           scheme,
			controllerConfig: &ControllerConfig{},
		}

		result, err := reconciler.Reconcile(context.Background(), ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name:      "worker-template",
				Namespace: "default",
			},
		})

		require.NoError(t, err)
		assert.Equal(t, ctrl.Result{}, result)

		updated := &infrav1.NutanixMachineTemplate{}
		err = fakeClient.Get(context.Background(), types.NamespacedName{
			Name:      "worker-template",
			Namespace: "default",
		}, updated)
		require.NoError(t, err)

		assertQuantityEqual(t, *resource.NewQuantity(8, resource.DecimalSI), updated.Status.Capacity[corev1.ResourceCPU])
		assertQuantityEqual(t, resource.MustParse("8Gi"), updated.Status.Capacity[corev1.ResourceMemory])
	})
}

func TestNutanixMachineTemplateReconciler_SetupWithManager(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	ctx := context.Background()
	log := ctrl.Log.WithName("controller")
	scheme := newTestScheme()

	cache := mockctlclient.NewMockCache(mockCtrl)

	mgr := mockctlclient.NewMockManager(mockCtrl)
	mgr.EXPECT().GetCache().Return(cache).AnyTimes()
	mgr.EXPECT().GetScheme().Return(scheme).AnyTimes()
	mgr.EXPECT().GetControllerOptions().Return(config.Controller{MaxConcurrentReconciles: 1}).AnyTimes()
	mgr.EXPECT().GetLogger().Return(log).AnyTimes()
	mgr.EXPECT().Add(gomock.Any()).Return(nil).AnyTimes()

	restScope := mockmeta.NewMockRESTScope(mockCtrl)
	restScope.EXPECT().Name().Return(meta.RESTScopeNameNamespace).AnyTimes()

	restMapper := mockmeta.NewMockRESTMapper(mockCtrl)
	restMapper.EXPECT().RESTMapping(gomock.Any()).Return(&meta.RESTMapping{Scope: restScope}, nil).AnyTimes()

	mockClient := mockctlclient.NewMockClient(mockCtrl)
	mockClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	mockClient.EXPECT().RESTMapper().Return(restMapper).AnyTimes()

	reconciler := &NutanixMachineTemplateReconciler{
		Client: mockClient,
		Scheme: scheme,
		controllerConfig: &ControllerConfig{
			MaxConcurrentReconciles: 1,
			SkipNameValidation:      true,
		},
	}

	err := reconciler.SetupWithManager(ctx, mgr)
	assert.NoError(t, err)
}

func TestNutanixMachineTemplateReconciler_SetupWithManager_BuildError(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	ctx := context.Background()
	log := ctrl.Log.WithName("controller")
	scheme := newTestScheme()

	cache := mockctlclient.NewMockCache(mockCtrl)

	mgr := mockctlclient.NewMockManager(mockCtrl)
	mgr.EXPECT().GetCache().Return(cache).AnyTimes()
	mgr.EXPECT().GetScheme().Return(scheme).AnyTimes()
	mgr.EXPECT().GetControllerOptions().Return(config.Controller{MaxConcurrentReconciles: 1}).AnyTimes()
	mgr.EXPECT().GetLogger().Return(log).AnyTimes()
	mgr.EXPECT().Add(gomock.Any()).Return(errors.New("error")).AnyTimes()

	restScope := mockmeta.NewMockRESTScope(mockCtrl)
	restScope.EXPECT().Name().Return(meta.RESTScopeNameNamespace).AnyTimes()

	restMapper := mockmeta.NewMockRESTMapper(mockCtrl)
	restMapper.EXPECT().RESTMapping(gomock.Any()).Return(&meta.RESTMapping{Scope: restScope}, nil).AnyTimes()

	mockClient := mockctlclient.NewMockClient(mockCtrl)
	mockClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	mockClient.EXPECT().RESTMapper().Return(restMapper).AnyTimes()

	reconciler := &NutanixMachineTemplateReconciler{
		Client: mockClient,
		Scheme: scheme,
		controllerConfig: &ControllerConfig{
			MaxConcurrentReconciles: 1,
		},
	}

	err := reconciler.SetupWithManager(ctx, mgr)
	assert.Error(t, err)
}

func TestNewNutanixMachineTemplateReconciler(t *testing.T) {
	t.Run("creates reconciler with valid options", func(t *testing.T) {
		scheme := newTestScheme()
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

		reconciler, err := NewNutanixMachineTemplateReconciler(
			fakeClient,
			scheme,
			WithMaxConcurrentReconciles(2),
		)

		require.NoError(t, err)
		assert.NotNil(t, reconciler)
		assert.Equal(t, 2, reconciler.controllerConfig.MaxConcurrentReconciles)
	})

	t.Run("returns error for invalid options", func(t *testing.T) {
		scheme := newTestScheme()
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

		reconciler, err := NewNutanixMachineTemplateReconciler(
			fakeClient,
			scheme,
			WithMaxConcurrentReconciles(0),
		)

		require.Error(t, err)
		assert.Nil(t, reconciler)
	})
}

func strPtr(s string) *string {
	return &s
}
