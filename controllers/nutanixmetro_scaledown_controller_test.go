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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	capiv1beta2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	balancerMSName = "ms1"
	balancerFDA    = "fd-dh1"
	balancerFDB    = "fd-dh2"
)

// balancerCluster is the owning Cluster for balancer test MachineSets. Reconcile looks it up to
// honor cluster/MachineSet pause, so it must exist in the fake client.
func balancerCluster() *capiv1beta2.Cluster {
	return &capiv1beta2.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: placementClusterName, Namespace: placementNamespace},
	}
}

// newBalancerReconciler wires a MetroScaleDownBalancerReconciler over a fake client preloaded with
// objs plus the owning Cluster.
func newBalancerReconciler(g *WithT, objs ...client.Object) *MetroScaleDownBalancerReconciler {
	scheme := newPlacementScheme(g)
	all := append([]client.Object{balancerCluster()}, objs...)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(all...).Build()
	return &MetroScaleDownBalancerReconciler{Client: fakeClient, Scheme: scheme}
}

// balancerMS builds a MachineSet on the given failure domain with an optional replica count.
func balancerMS(fd string, replicas *int32) *capiv1beta2.MachineSet {
	return &capiv1beta2.MachineSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      balancerMSName,
			Namespace: placementNamespace,
			Labels:    map[string]string{capiv1beta2.ClusterNameLabel: placementClusterName},
		},
		Spec: capiv1beta2.MachineSetSpec{
			Replicas: replicas,
			Template: capiv1beta2.MachineTemplateSpec{
				Spec: capiv1beta2.MachineSpec{FailureDomain: fd},
			},
		},
	}
}

// balancerMachineInSet builds a Machine attributed to balancerMSName owning the same-named
// NutanixMachine.
func balancerMachineInSet(name string) *capiv1beta2.Machine {
	return placementMachineInSet(name, name, "wmd", balancerMSName)
}

// balancerTerminating marks a Machine as being deleted (fake client requires a finalizer to persist
// a deletion timestamp).
func balancerTerminating(m *capiv1beta2.Machine) *capiv1beta2.Machine {
	now := metav1.Now()
	m.DeletionTimestamp = &now
	m.Finalizers = []string{"test.nutanix.com/finalizer"}
	return m
}

// reconcileBalancer runs a single reconcile keyed on the MachineSet.
func reconcileBalancer(g *WithT, r *MetroScaleDownBalancerReconciler) {
	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Name: balancerMSName, Namespace: placementNamespace},
	})
	g.Expect(err).ToNot(HaveOccurred())
}

// hasDeleteAnnotation returns whether the named Machine currently carries the CAPI delete annotation.
func hasDeleteAnnotation(g *WithT, r *MetroScaleDownBalancerReconciler, name string) bool {
	m := &capiv1beta2.Machine{}
	g.Expect(r.Get(context.Background(), client.ObjectKey{Name: name, Namespace: placementNamespace}, m)).To(Succeed())
	_, ok := m.Annotations[capiv1beta2.DeleteMachineAnnotation]
	return ok
}

// hasManagedAnnotation returns whether the named Machine carries our ownership marker.
func hasManagedAnnotation(g *WithT, r *MetroScaleDownBalancerReconciler, name string) bool {
	m := &capiv1beta2.Machine{}
	g.Expect(r.Get(context.Background(), client.ObjectKey{Name: name, Namespace: placementNamespace}, m)).To(Succeed())
	_, ok := m.Annotations[managedDeleteMachineAnnotation]
	return ok
}

func TestMetroScaleDownBalancerReconcile(t *testing.T) {
	t.Run("balanced group is a no-op", func(t *testing.T) {
		g := NewWithT(t)
		r := newBalancerReconciler(g,
			balancerMS(metroFailureDomainPrefix+"metro0", nil),
			placementNM("m-a1", balancerFDA, false), balancerMachineInSet("m-a1"),
			placementNM("m-a2", balancerFDA, false), balancerMachineInSet("m-a2"),
			placementNM("m-b1", balancerFDB, false), balancerMachineInSet("m-b1"),
			placementNM("m-b2", balancerFDB, false), balancerMachineInSet("m-b2"),
		)

		reconcileBalancer(g, r)

		for _, name := range []string{"m-a1", "m-a2", "m-b1", "m-b2"} {
			g.Expect(hasDeleteAnnotation(g, r, name)).To(BeFalse(), name)
		}
	})

	t.Run("imbalanced group marks the excess on the larger site", func(t *testing.T) {
		g := NewWithT(t)
		// site A has 3, site B has 1 -> excess 2, both victims from A (smallest names first).
		r := newBalancerReconciler(g,
			balancerMS(metroFailureDomainPrefix+"metro0", nil),
			placementNM("m-a1", balancerFDA, false), balancerMachineInSet("m-a1"),
			placementNM("m-a2", balancerFDA, false), balancerMachineInSet("m-a2"),
			placementNM("m-a3", balancerFDA, false), balancerMachineInSet("m-a3"),
			placementNM("m-b1", balancerFDB, false), balancerMachineInSet("m-b1"),
		)

		reconcileBalancer(g, r)

		g.Expect(hasDeleteAnnotation(g, r, "m-a1")).To(BeTrue())
		g.Expect(hasDeleteAnnotation(g, r, "m-a2")).To(BeTrue())
		g.Expect(hasManagedAnnotation(g, r, "m-a1")).To(BeTrue())
		g.Expect(hasDeleteAnnotation(g, r, "m-a3")).To(BeFalse())
		g.Expect(hasDeleteAnnotation(g, r, "m-b1")).To(BeFalse())
	})

	t.Run("pending scale-down keeps the remainder balanced", func(t *testing.T) {
		g := NewWithT(t)
		// 3 per site, replicas 4 -> delete 2: one from each site keeps 2/2.
		r := newBalancerReconciler(g,
			balancerMS(metroFailureDomainPrefix+"metro0", ptr.To(int32(4))),
			placementNM("m-a1", balancerFDA, false), balancerMachineInSet("m-a1"),
			placementNM("m-a2", balancerFDA, false), balancerMachineInSet("m-a2"),
			placementNM("m-a3", balancerFDA, false), balancerMachineInSet("m-a3"),
			placementNM("m-b1", balancerFDB, false), balancerMachineInSet("m-b1"),
			placementNM("m-b2", balancerFDB, false), balancerMachineInSet("m-b2"),
			placementNM("m-b3", balancerFDB, false), balancerMachineInSet("m-b3"),
		)

		reconcileBalancer(g, r)

		aMarked := 0
		for _, n := range []string{"m-a1", "m-a2", "m-a3"} {
			if hasDeleteAnnotation(g, r, n) {
				aMarked++
			}
		}
		bMarked := 0
		for _, n := range []string{"m-b1", "m-b2", "m-b3"} {
			if hasDeleteAnnotation(g, r, n) {
				bMarked++
			}
		}
		g.Expect(aMarked).To(Equal(1))
		g.Expect(bMarked).To(Equal(1))
	})

	t.Run("stale managed annotation is cleaned up when balanced", func(t *testing.T) {
		g := NewWithT(t)
		stale := balancerMachineInSet("m-a1")
		stale.Annotations = map[string]string{
			capiv1beta2.DeleteMachineAnnotation: "true",
			managedDeleteMachineAnnotation:      "true",
		}
		r := newBalancerReconciler(g,
			balancerMS(metroFailureDomainPrefix+"metro0", nil),
			placementNM("m-a1", balancerFDA, false), stale,
			placementNM("m-b1", balancerFDB, false), balancerMachineInSet("m-b1"),
		)

		reconcileBalancer(g, r)

		g.Expect(hasDeleteAnnotation(g, r, "m-a1")).To(BeFalse())
		g.Expect(hasManagedAnnotation(g, r, "m-a1")).To(BeFalse())
	})

	t.Run("operator-set delete annotation is respected", func(t *testing.T) {
		g := NewWithT(t)
		// Balanced group; a machine carries an operator delete annotation (no managed marker).
		operatorMarked := balancerMachineInSet("m-a1")
		operatorMarked.Annotations = map[string]string{capiv1beta2.DeleteMachineAnnotation: "yes"}
		r := newBalancerReconciler(g,
			balancerMS(metroFailureDomainPrefix+"metro0", nil),
			placementNM("m-a1", balancerFDA, false), operatorMarked,
			placementNM("m-b1", balancerFDB, false), balancerMachineInSet("m-b1"),
		)

		reconcileBalancer(g, r)

		g.Expect(hasDeleteAnnotation(g, r, "m-a1")).To(BeTrue())
		g.Expect(hasManagedAnnotation(g, r, "m-a1")).To(BeFalse())
	})

	t.Run("paused MachineSet is a no-op", func(t *testing.T) {
		g := NewWithT(t)
		// Imbalanced (A=2, B=1) but paused, so the controller must not mark anyone.
		ms := balancerMS(metroFailureDomainPrefix+"metro0", nil)
		ms.Annotations = map[string]string{capiv1beta2.PausedAnnotation: ""}
		r := newBalancerReconciler(g,
			ms,
			placementNM("m-a1", balancerFDA, false), balancerMachineInSet("m-a1"),
			placementNM("m-a2", balancerFDA, false), balancerMachineInSet("m-a2"),
			placementNM("m-b1", balancerFDB, false), balancerMachineInSet("m-b1"),
		)

		reconcileBalancer(g, r)

		for _, name := range []string{"m-a1", "m-a2", "m-b1"} {
			g.Expect(hasDeleteAnnotation(g, r, name)).To(BeFalse(), name)
		}
	})

	t.Run("non-metro pool is skipped", func(t *testing.T) {
		g := NewWithT(t)
		// A machine carries a managed annotation that would be cleaned if the controller acted.
		preAnnotated := balancerMachineInSet("m-a1")
		preAnnotated.Annotations = map[string]string{
			capiv1beta2.DeleteMachineAnnotation: "true",
			managedDeleteMachineAnnotation:      "true",
		}
		r := newBalancerReconciler(g,
			balancerMS("", nil),
			placementNM("m-a1", balancerFDA, false), preAnnotated,
		)

		reconcileBalancer(g, r)

		// Untouched because the pool is not a stretched-metro pool.
		g.Expect(hasDeleteAnnotation(g, r, "m-a1")).To(BeTrue())
		g.Expect(hasManagedAnnotation(g, r, "m-a1")).To(BeTrue())
	})

	t.Run("MetroSite pool is skipped", func(t *testing.T) {
		g := NewWithT(t)
		r := newBalancerReconciler(g,
			balancerMS(metroSiteFailureDomainPrefix+"site-1", nil),
			placementNM("m-a1", balancerFDA, false), balancerMachineInSet("m-a1"),
			placementNM("m-a2", balancerFDA, false), balancerMachineInSet("m-a2"),
			placementNM("m-b1", balancerFDB, false), balancerMachineInSet("m-b1"),
		)

		reconcileBalancer(g, r)

		for _, name := range []string{"m-a1", "m-a2", "m-b1"} {
			g.Expect(hasDeleteAnnotation(g, r, name)).To(BeFalse(), name)
		}
	})

	t.Run("terminating machines are skipped and never annotated", func(t *testing.T) {
		g := NewWithT(t)
		// Live: A=2, B=1 (excess 1 -> one victim from A). A also has a terminating machine that must
		// not be counted or annotated.
		r := newBalancerReconciler(g,
			balancerMS(metroFailureDomainPrefix+"metro0", nil),
			placementNM("m-a1", balancerFDA, false), balancerMachineInSet("m-a1"),
			placementNM("m-a2", balancerFDA, false), balancerMachineInSet("m-a2"),
			placementNM("m-a3", balancerFDA, true), balancerTerminating(balancerMachineInSet("m-a3")),
			placementNM("m-b1", balancerFDB, false), balancerMachineInSet("m-b1"),
		)

		reconcileBalancer(g, r)

		g.Expect(hasDeleteAnnotation(g, r, "m-a1")).To(BeTrue())
		g.Expect(hasDeleteAnnotation(g, r, "m-a2")).To(BeFalse())
		g.Expect(hasDeleteAnnotation(g, r, "m-a3")).To(BeFalse())
		g.Expect(hasDeleteAnnotation(g, r, "m-b1")).To(BeFalse())
	})

	t.Run("machines without a placement label yet are ignored", func(t *testing.T) {
		g := NewWithT(t)
		// One machine has no NutanixMachine site label; balanced otherwise -> no-op.
		r := newBalancerReconciler(g,
			balancerMS(metroFailureDomainPrefix+"metro0", nil),
			placementNM("m-a1", balancerFDA, false), balancerMachineInSet("m-a1"),
			placementNM("m-b1", balancerFDB, false), balancerMachineInSet("m-b1"),
			placementNM("m-x1", "", false), balancerMachineInSet("m-x1"),
		)

		reconcileBalancer(g, r)

		for _, name := range []string{"m-a1", "m-b1", "m-x1"} {
			g.Expect(hasDeleteAnnotation(g, r, name)).To(BeFalse(), name)
		}
	})
}
