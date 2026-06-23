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

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	capiv1beta2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	ctrlutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
)

const (
	vhaNamespace   = "default"
	vhaClusterName = "test-cluster"
	vhaNtnxCluster = "test-ntnxcluster"
)

func newVHAScheme(g *WithT) *runtime.Scheme {
	scheme := runtime.NewScheme()
	g.Expect(infrav1.AddToScheme(scheme)).To(Succeed())
	g.Expect(capiv1beta2.AddToScheme(scheme)).To(Succeed())
	return scheme
}

func newVHAReconciler(g *WithT, objs ...client.Object) *NutanixVirtualHADomainReconciler {
	scheme := newVHAScheme(g)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
	return &NutanixVirtualHADomainReconciler{
		Client: fakeClient,
		Scheme: scheme,
	}
}

// vhaDomainObj builds a NutanixVirtualHADomain carrying the cluster-name label and the finalizer.
func vhaDomainObj(name, clusterName string) *infrav1.NutanixVirtualHADomain {
	return &infrav1.NutanixVirtualHADomain{
		ObjectMeta: metav1.ObjectMeta{
			Name:       name,
			Namespace:  vhaNamespace,
			Labels:     map[string]string{capiv1beta2.ClusterNameLabel: clusterName},
			Finalizers: []string{infrav1.NutanixVirtualHADomainFinalizer},
		},
		Spec: infrav1.NutanixVirtualHADomainSpec{
			MetroRef: corev1.LocalObjectReference{Name: "metro-test"},
		},
	}
}

func TestVHADomainNameHelpers(t *testing.T) {
	g := NewWithT(t)

	g.Expect(vhaCategoryValue("d1", "default", 0)).To(Equal("k8s-vha-capx-d1-default-0"))
	g.Expect(vhaCategoryValue("d1", "default", 1)).To(Equal("k8s-vha-capx-d1-default-1"))
	g.Expect(vhaRecoveryPlanName("d1", "default", 0)).To(Equal("k8s-vha-capx-d1-default-0"))
	g.Expect(vhaProtectionPolicyName("d1")).To(Equal("k8s-vha-capx-d1"))
}

func TestVHADomainReconcileDelete_BlockedWhenOwnerNotInDeletion(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()

	cluster := &capiv1beta2.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: vhaClusterName, Namespace: vhaNamespace},
		Spec: capiv1beta2.ClusterSpec{
			InfrastructureRef: capiv1beta2.ContractVersionedObjectReference{Name: vhaNtnxCluster},
		},
	}
	ntnxCluster := &infrav1.NutanixCluster{
		ObjectMeta: metav1.ObjectMeta{Name: vhaNtnxCluster, Namespace: vhaNamespace},
	}
	vHADomain := vhaDomainObj("d1", vhaClusterName)

	r := newVHAReconciler(g, cluster, ntnxCluster)

	err := r.reconcileDelete(ctx, vHADomain)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("is not in deletion"))

	cond := conditions.Get(vHADomain, infrav1.VHADomainSafeForDeletionCondition)
	g.Expect(cond).NotTo(BeNil())
	g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
	g.Expect(cond.Reason).To(Equal(infrav1.VHADomainOwnerNotInDeletion))

	// The finalizer must be retained so deletion stays blocked.
	g.Expect(ctrlutil.ContainsFinalizer(vHADomain, infrav1.NutanixVirtualHADomainFinalizer)).To(BeTrue())
}

func TestVHADomainReconcileDelete_AllowedWhenOwnerAlreadyGone(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()

	// The owning Cluster referenced by the label no longer exists (already garbage-collected),
	// and the vHADomain has no controller owner reference to resolve a NutanixCluster from. The
	// deletion must proceed (best-effort) and remove the finalizer.
	vHADomain := vhaDomainObj("d1", "gone-cluster")

	r := newVHAReconciler(g)

	err := r.reconcileDelete(ctx, vHADomain)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(ctrlutil.ContainsFinalizer(vHADomain, infrav1.NutanixVirtualHADomainFinalizer)).To(BeFalse())
}
