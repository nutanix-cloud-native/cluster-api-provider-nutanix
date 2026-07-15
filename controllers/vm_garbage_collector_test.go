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

package controllers

import (
	"context"
	"testing"
	"time"

	"github.com/nutanix-cloud-native/prism-go-client/converged"
	prismModels "github.com/nutanix/ntnx-api-golang-clients/prism-go-client/v4/models/prism/v4/config"
	vmmModels "github.com/nutanix/ntnx-api-golang-clients/vmm-go-client/v4/models/vmm/v4/ahv/config"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	capiv1beta2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	mockconverged "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/mocks/converged"
)

const (
	gcNamespace   = "default"
	gcClusterName = "test-cluster"
)

func newGCFakeClient(g *WithT, objs ...client.Object) client.Client {
	scheme := newVHAScheme(g)
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}

// gcVM builds a VM with the given identity, optionally stamped with the cluster ownership category
// and the CAPX providerID custom attribute, and created createdAgo in the past.
func gcVM(name, extID, categoryExtID string, withCategory, withProviderID bool, createdAgo time.Duration) *vmmModels.Vm {
	vm := vmmModels.NewVm()
	vm.Name = ptr.To(name)
	vm.ExtId = ptr.To(extID)
	if withCategory {
		vm.Categories = []vmmModels.CategoryReference{{ExtId: ptr.To(categoryExtID)}}
	}
	if withProviderID {
		vm.CustomAttributes = []string{vmCustomAttributePrefix4ProviderID + extID}
	}
	created := time.Now().Add(-createdAgo)
	vm.CreateTime = &created
	return vm
}

func TestIsOrphanedCAPXVM(t *testing.T) {
	const catExt = "cat-ext-id"

	tests := []struct {
		name          string
		vm            *vmmModels.Vm
		expectedUUIDs map[string]struct{}
		expectedNames map[string]struct{}
		want          bool
	}{
		{
			name: "orphan is reaped",
			vm:   gcVM("node-orphan", "uuid-orphan", catExt, true, true, time.Hour),
			want: true,
		},
		{
			name: "not CAPX provisioned (no providerID attribute)",
			vm:   gcVM("node-orphan", "uuid-orphan", catExt, true, false, time.Hour),
			want: false,
		},
		{
			name: "missing cluster category",
			vm:   gcVM("node-orphan", "uuid-orphan", catExt, false, true, time.Hour),
			want: false,
		},
		{
			name: "within creation grace period",
			vm:   gcVM("node-new", "uuid-new", catExt, true, true, time.Minute),
			want: false,
		},
		{
			name:          "still managed by UUID",
			vm:            gcVM("node-live", "uuid-live", catExt, true, true, time.Hour),
			expectedUUIDs: map[string]struct{}{"uuid-live": {}},
			want:          false,
		},
		{
			name:          "still managed by name",
			vm:            gcVM("node-live", "uuid-live", catExt, true, true, time.Hour),
			expectedNames: map[string]struct{}{"node-live": {}},
			want:          false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			uuids := tt.expectedUUIDs
			if uuids == nil {
				uuids = map[string]struct{}{}
			}
			names := tt.expectedNames
			if names == nil {
				names = map[string]struct{}{}
			}
			g.Expect(isOrphanedCAPXVM(tt.vm, catExt, uuids, names)).To(Equal(tt.want))
		})
	}
}

func TestGarbageCollectOrphanedVMs_DeletesOnlyOrphan(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	const catExt = "cat-ext-id"

	// A live worker still tracked by a NutanixMachine + Machine.
	liveNM := &infrav1.NutanixMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "node-live",
			Namespace: gcNamespace,
			Labels:    map[string]string{capiv1beta2.ClusterNameLabel: gcClusterName},
		},
		Status: infrav1.NutanixMachineStatus{VmUUID: "uuid-live"},
	}
	liveMachine := &capiv1beta2.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "node-live",
			Namespace: gcNamespace,
			Labels:    map[string]string{capiv1beta2.ClusterNameLabel: gcClusterName},
		},
	}

	k8sClient := newGCFakeClient(g, liveNM, liveMachine)

	mockConverged := NewMockConvergedClient(ctrl)

	// getCategory resolves the cluster ownership category.
	mockConverged.MockCategories.EXPECT().List(ctx, gomock.Any()).Return([]prismModels.Category{{
		ExtId: ptr.To(catExt),
		Key:   ptr.To(infrav1.DefaultCAPICategoryKeyForName),
		Value: ptr.To(gcClusterName),
	}}, nil)

	// The category-filtered VM list returns the live VM and one orphan.
	liveVM := gcVM("node-live", "uuid-live", catExt, true, true, time.Hour)
	orphanVM := gcVM("node-orphan", "uuid-orphan", catExt, true, true, time.Hour)
	mockConverged.MockVMs.EXPECT().List(ctx, gomock.Any()).Return([]vmmModels.Vm{*liveVM, *orphanVM}, nil)

	// Only the orphan must be deleted.
	delOp := mockconverged.NewMockOperation[converged.NoEntity](ctrl)
	delOp.EXPECT().UUID().Return("delete-task-uuid")
	mockConverged.MockVMs.EXPECT().DeleteAsync(ctx, "uuid-orphan").Return(delOp, nil)

	g.Expect(garbageCollectOrphanedVMs(ctx, k8sClient, mockConverged.Client, gcClusterName, gcNamespace)).To(Succeed())
}

func TestGarbageCollectOrphanedVMs_NoCategoryNoOp(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	k8sClient := newGCFakeClient(g)

	mockConverged := NewMockConvergedClient(ctrl)
	// Category not found -> the GC must be a no-op and never list VMs.
	mockConverged.MockCategories.EXPECT().List(ctx, gomock.Any()).Return([]prismModels.Category{}, nil)

	g.Expect(garbageCollectOrphanedVMs(ctx, k8sClient, mockConverged.Client, gcClusterName, gcNamespace)).To(Succeed())
}

func TestManagedVMIdentifiers(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()

	nm := &infrav1.NutanixMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "nm-1",
			Namespace: gcNamespace,
			Labels:    map[string]string{capiv1beta2.ClusterNameLabel: gcClusterName},
		},
		Spec:   infrav1.NutanixMachineSpec{ProviderID: "nutanix://uuid-from-providerid"},
		Status: infrav1.NutanixMachineStatus{VmUUID: "uuid-from-status"},
	}
	machine := &capiv1beta2.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "machine-1",
			Namespace: gcNamespace,
			Labels:    map[string]string{capiv1beta2.ClusterNameLabel: gcClusterName},
		},
	}

	k8sClient := newGCFakeClient(g, nm, machine)

	uuids, names, err := managedVMIdentifiers(ctx, k8sClient, gcClusterName, gcNamespace)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(uuids).To(HaveKey("uuid-from-status"))
	g.Expect(uuids).To(HaveKey("uuid-from-providerid"))
	g.Expect(names).To(HaveKey("nm-1"))
	g.Expect(names).To(HaveKey("machine-1"))
}
