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
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	nctx "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/pkg/context"
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

	r := newVHAReconciler(g)
	rctx := &nctx.VHADomainContext{
		Context:        ctx,
		Cluster:        cluster,
		NutanixCluster: ntnxCluster,
		VHADomain:      vHADomain,
	}

	err := r.reconcileDelete(rctx)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("is not in deletion"))

	cond := conditions.Get(vHADomain, infrav1.VHADomainSafeForDeletionCondition)
	g.Expect(cond).NotTo(BeNil())
	g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
	g.Expect(cond.Reason).To(Equal(infrav1.VHADomainOwnerNotInDeletion))

	// The finalizer must be retained so deletion stays blocked.
	g.Expect(ctrlutil.ContainsFinalizer(vHADomain, infrav1.NutanixVirtualHADomainFinalizer)).To(BeTrue())
}

func TestVHADomainReconcileDelete_AllowedWhenOwnersInDeletion(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()

	// Both the owning Cluster and NutanixCluster are themselves in deletion, so the
	// vHADomain is safe to clean up. With an empty spec there are no Prism Central
	// resources to remove, so the finalizer must be dropped.
	now := metav1.Now()
	cluster := &capiv1beta2.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:              vhaClusterName,
			Namespace:         vhaNamespace,
			DeletionTimestamp: &now,
			Finalizers:        []string{"test/keep"},
		},
		Spec: capiv1beta2.ClusterSpec{
			InfrastructureRef: capiv1beta2.ContractVersionedObjectReference{Name: vhaNtnxCluster},
		},
	}
	ntnxCluster := &infrav1.NutanixCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:              vhaNtnxCluster,
			Namespace:         vhaNamespace,
			DeletionTimestamp: &now,
			Finalizers:        []string{"test/keep"},
		},
	}
	vHADomain := vhaDomainObj("d1", vhaClusterName)

	r := newVHAReconciler(g)
	rctx := &nctx.VHADomainContext{
		Context:        ctx,
		Cluster:        cluster,
		NutanixCluster: ntnxCluster,
		VHADomain:      vHADomain,
	}

	err := r.reconcileDelete(rctx)
	g.Expect(err).NotTo(HaveOccurred())

	cond := conditions.Get(vHADomain, infrav1.VHADomainSafeForDeletionCondition)
	g.Expect(cond).NotTo(BeNil())
	g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
	g.Expect(cond.Reason).To(Equal(infrav1.VHADomainCleanupSucceeded))

	g.Expect(ctrlutil.ContainsFinalizer(vHADomain, infrav1.NutanixVirtualHADomainFinalizer)).To(BeFalse())
}

func TestVHADomainReconcileDelete_BlockedWhenOnlyNutanixClusterLive(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()

	// The owning Cluster is already in deletion but the NutanixCluster is not, so the
	// vHADomain deletion must still be blocked and the blocking message must call out
	// the NutanixCluster.
	now := metav1.Now()
	cluster := &capiv1beta2.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:              vhaClusterName,
			Namespace:         vhaNamespace,
			DeletionTimestamp: &now,
			Finalizers:        []string{"test/keep"},
		},
	}
	ntnxCluster := &infrav1.NutanixCluster{
		ObjectMeta: metav1.ObjectMeta{Name: vhaNtnxCluster, Namespace: vhaNamespace},
	}
	vHADomain := vhaDomainObj("d1", vhaClusterName)

	r := newVHAReconciler(g)
	rctx := &nctx.VHADomainContext{
		Context:        ctx,
		Cluster:        cluster,
		NutanixCluster: ntnxCluster,
		VHADomain:      vHADomain,
	}

	err := r.reconcileDelete(rctx)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring(vhaNtnxCluster))
	g.Expect(err.Error()).To(ContainSubstring("is not in deletion"))
	g.Expect(ctrlutil.ContainsFinalizer(vHADomain, infrav1.NutanixVirtualHADomainFinalizer)).To(BeTrue())
}

func TestVHADomainReconcile_NotFound(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()

	r := newVHAReconciler(g)
	res, err := r.Reconcile(ctx, reconcile.Request{
		NamespacedName: client.ObjectKey{Name: "does-not-exist", Namespace: vhaNamespace},
	})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(res).To(Equal(reconcile.Result{}))
}

func TestVHADomainReconcile_MissingCluster(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()

	// The vHADomain references a Cluster via its label, but that Cluster does not
	// exist, so Reconcile must surface the lookup error instead of proceeding.
	vHADomain := vhaDomainObj("d1", "missing-cluster")

	r := newVHAReconciler(g, vHADomain)
	_, err := r.Reconcile(ctx, reconcile.Request{
		NamespacedName: client.ObjectKey{Name: vHADomain.Name, Namespace: vHADomain.Namespace},
	})
	g.Expect(err).To(HaveOccurred())
}

func TestVHADomainReconcileNormal_AddsFinalizer(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()

	// A vHADomain without the finalizer must get it added and reconcileNormal must
	// return early (before any Prism Central work) so the finalizer is persisted first.
	vHADomain := vhaDomainObj("d1", vhaClusterName)
	vHADomain.Finalizers = nil

	r := newVHAReconciler(g)
	rctx := &nctx.VHADomainContext{Context: ctx, VHADomain: vHADomain}

	err := r.reconcileNormal(rctx)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(ctrlutil.ContainsFinalizer(vHADomain, infrav1.NutanixVirtualHADomainFinalizer)).To(BeTrue())
}

func TestVHADomainReconcileNormal_MetroNotFound(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()

	// With the finalizer already present, reconcileNormal proceeds to resolve the
	// referenced NutanixMetro. When it is absent the reconcile fails and readiness
	// must be cleared.
	vHADomain := vhaDomainObj("d1", vhaClusterName)
	vHADomain.Status.Ready = true

	r := newVHAReconciler(g)
	rctx := &nctx.VHADomainContext{Context: ctx, VHADomain: vHADomain}

	err := r.reconcileNormal(rctx)
	g.Expect(err).To(HaveOccurred())
	g.Expect(vHADomain.Status.Ready).To(BeFalse())
}

func TestVHADomainEnsurePCResources_RequiresTwoFailureDomains(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()

	vHADomain := vhaDomainObj("d1", vhaClusterName)
	r := newVHAReconciler(g)
	rctx := &nctx.VHADomainContext{Context: ctx, VHADomain: vHADomain}

	fds := []*infrav1.NutanixFailureDomain{
		{ObjectMeta: metav1.ObjectMeta{Name: "fd-1", Namespace: vhaNamespace}},
	}
	err := r.ensureVHADomainPCResources(rctx, fds)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("exactly 2 failure domains"))
}
