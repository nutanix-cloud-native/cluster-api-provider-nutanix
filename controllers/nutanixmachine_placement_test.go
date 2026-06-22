/*
Copyright 2024 Nutanix

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
	"sync"
	"testing"

	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	capiv1beta2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	nctx "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/pkg/context"
)

const (
	placementNamespace   = "default"
	placementClusterName = "test-cluster"
)

// newPlacementScheme returns a scheme registered with the types used by the metro placement code.
func newPlacementScheme(g *WithT) *runtime.Scheme {
	scheme := runtime.NewScheme()
	g.Expect(infrav1.AddToScheme(scheme)).To(Succeed())
	g.Expect(capiv1beta2.AddToScheme(scheme)).To(Succeed())
	return scheme
}

// placementFD builds a NutanixFailureDomain object with the given name.
func placementFD(name string) *infrav1.NutanixFailureDomain {
	return &infrav1.NutanixFailureDomain{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: placementNamespace},
	}
}

// placementNM builds a NutanixMachine carrying the cluster-name label. When fd is non-empty it is
// treated as already-placed (carries the native-failuredomain label). When terminating is true a
// deletion timestamp (and a finalizer, required by the fake client) is set.
func placementNM(name, fd string, terminating bool) *infrav1.NutanixMachine {
	labels := map[string]string{capiv1beta2.ClusterNameLabel: placementClusterName}
	if fd != "" {
		labels[metroNativeFailureDomainLabelKey] = fd
	}
	nm := &infrav1.NutanixMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: placementNamespace,
			Labels:    labels,
		},
	}
	if terminating {
		now := metav1.Now()
		nm.DeletionTimestamp = &now
		nm.Finalizers = []string{infrav1.NutanixMachineFinalizer}
	}
	return nm
}

// placementMachine builds a CAPI Machine that owns the NutanixMachine named infraName, attributed to
// the given MachineDeployment (empty deployment leaves the machine unattributed to a nodepool).
func placementMachine(name, infraName, deployment string) *capiv1beta2.Machine {
	labels := map[string]string{capiv1beta2.ClusterNameLabel: placementClusterName}
	if deployment != "" {
		labels[capiv1beta2.MachineDeploymentNameLabel] = deployment
	}
	return &capiv1beta2.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: placementNamespace,
			Labels:    labels,
		},
		Spec: capiv1beta2.MachineSpec{
			InfrastructureRef: capiv1beta2.ContractVersionedObjectReference{Name: infraName},
		},
	}
}

// placementMachineInSet builds a CAPI Machine attributed to both a MachineDeployment and a specific
// MachineSet, mirroring how CAPI labels worker machines. This is used to exercise rolling upgrades,
// where the old and new generations share a MachineDeployment but live in different MachineSets.
func placementMachineInSet(name, infraName, deployment, set string) *capiv1beta2.Machine {
	m := placementMachine(name, infraName, deployment)
	m.Labels[capiv1beta2.MachineSetNameLabel] = set
	return m
}

// newPlacementReconciler wires a reconciler whose Client and APIReader both point at a fake client
// preloaded with objs, plus the MachineContext needed by the placement helpers.
func newPlacementReconciler(g *WithT, objs ...client.Object) (*NutanixMachineReconciler, *nctx.MachineContext) {
	scheme := newPlacementScheme(g)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()

	reconciler := &NutanixMachineReconciler{
		Client:    fakeClient,
		APIReader: fakeClient,
		Scheme:    scheme,
	}
	rctx := &nctx.MachineContext{
		Context: context.Background(),
		Cluster: &capiv1beta2.Cluster{
			ObjectMeta: metav1.ObjectMeta{Name: placementClusterName, Namespace: placementNamespace},
		},
		NutanixCluster: &infrav1.NutanixCluster{
			ObjectMeta: metav1.ObjectMeta{Name: placementClusterName, Namespace: placementNamespace},
		},
		Datastore: map[string]*string{},
	}
	return reconciler, rctx
}

func TestMetroPlacementGroupKey(t *testing.T) {
	tests := []struct {
		name   string
		labels map[string]string
		want   string
	}{
		{
			name:   "nil labels are unattributed",
			labels: nil,
			want:   "",
		},
		{
			name:   "empty labels are unattributed",
			labels: map[string]string{},
			want:   "",
		},
		{
			name:   "MachineDeployment label",
			labels: map[string]string{capiv1beta2.MachineDeploymentNameLabel: "wmd"},
			want:   capiv1beta2.MachineDeploymentNameLabel + "=wmd",
		},
		{
			name:   "MachineSet label",
			labels: map[string]string{capiv1beta2.MachineSetNameLabel: "ms-1"},
			want:   capiv1beta2.MachineSetNameLabel + "=ms-1",
		},
		{
			name:   "MachinePool label",
			labels: map[string]string{capiv1beta2.MachinePoolNameLabel: "mp-1"},
			want:   capiv1beta2.MachinePoolNameLabel + "=mp-1",
		},
		{
			name:   "control plane label",
			labels: map[string]string{capiv1beta2.MachineControlPlaneLabel: ""},
			want:   capiv1beta2.MachineControlPlaneLabel,
		},
		{
			// MachineSet is preferred over MachineDeployment so each rollout generation (a distinct
			// MachineSet) balances independently of the old one being torn down.
			name: "MachineSet takes precedence over MachineDeployment",
			labels: map[string]string{
				capiv1beta2.MachineDeploymentNameLabel: "wmd",
				capiv1beta2.MachineSetNameLabel:        "ms-1",
				capiv1beta2.MachinePoolNameLabel:       "mp-1",
			},
			want: capiv1beta2.MachineSetNameLabel + "=ms-1",
		},
		{
			name: "MachineDeployment used when MachineSet is absent",
			labels: map[string]string{
				capiv1beta2.MachineDeploymentNameLabel: "wmd",
				capiv1beta2.MachinePoolNameLabel:       "mp-1",
			},
			want: capiv1beta2.MachineDeploymentNameLabel + "=wmd",
		},
		{
			name: "MachineSet takes precedence over MachinePool",
			labels: map[string]string{
				capiv1beta2.MachineSetNameLabel:  "ms-1",
				capiv1beta2.MachinePoolNameLabel: "mp-1",
			},
			want: capiv1beta2.MachineSetNameLabel + "=ms-1",
		},
		{
			name:   "empty nodepool label value is ignored",
			labels: map[string]string{capiv1beta2.MachineDeploymentNameLabel: ""},
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(metroPlacementGroupKey(tt.labels)).To(Equal(tt.want))
		})
	}
}

func TestMetroMachineGroupKeys(t *testing.T) {
	t.Run("maps NutanixMachine name to nodepool key via owning Machine", func(t *testing.T) {
		g := NewWithT(t)
		reconciler, rctx := newPlacementReconciler(g,
			placementMachine("m-a", "nm-a", "wmd"),
			placementMachine("m-b", "nm-b", "wmd-other"),
		)

		keys, err := reconciler.metroMachineGroupKeys(rctx, reconciler.Client)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(keys).To(HaveLen(2))
		g.Expect(keys["nm-a"]).To(Equal(capiv1beta2.MachineDeploymentNameLabel + "=wmd"))
		g.Expect(keys["nm-b"]).To(Equal(capiv1beta2.MachineDeploymentNameLabel + "=wmd-other"))
	})

	t.Run("skips machines with empty infrastructureRef name", func(t *testing.T) {
		g := NewWithT(t)
		reconciler, rctx := newPlacementReconciler(g,
			placementMachine("m-a", "nm-a", "wmd"),
			placementMachine("m-empty", "", "wmd"),
		)

		keys, err := reconciler.metroMachineGroupKeys(rctx, reconciler.Client)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(keys).To(HaveLen(1))
		g.Expect(keys).To(HaveKey("nm-a"))
		g.Expect(keys).ToNot(HaveKey(""))
	})

	t.Run("unattributed machine maps to empty key", func(t *testing.T) {
		g := NewWithT(t)
		reconciler, rctx := newPlacementReconciler(g,
			placementMachine("m-a", "nm-a", ""),
		)

		keys, err := reconciler.metroMachineGroupKeys(rctx, reconciler.Client)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(keys["nm-a"]).To(Equal(""))
	})

	t.Run("only includes machines belonging to the cluster", func(t *testing.T) {
		g := NewWithT(t)
		other := placementMachine("m-other", "nm-other", "wmd")
		other.Labels[capiv1beta2.ClusterNameLabel] = "another-cluster"
		reconciler, rctx := newPlacementReconciler(g,
			placementMachine("m-a", "nm-a", "wmd"),
			other,
		)

		keys, err := reconciler.metroMachineGroupKeys(rctx, reconciler.Client)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(keys).To(HaveLen(1))
		g.Expect(keys).To(HaveKey("nm-a"))
		g.Expect(keys).ToNot(HaveKey("nm-other"))
	})
}

func TestComputeMetroPlacementIndex(t *testing.T) {
	fdA := placementFD("fd-a")
	fdB := placementFD("fd-b")
	fdC := placementFD("fd-c")

	t.Run("first machine in an empty nodepool lands on the first FD", func(t *testing.T) {
		g := NewWithT(t)
		current := placementNM("nm-new", "", false)
		reconciler, rctx := newPlacementReconciler(g,
			current,
			placementMachine("nm-new", "nm-new", "wmd"),
		)
		rctx.NutanixMachine = current
		rctx.Machine = placementMachine("nm-new", "nm-new", "wmd")

		idx, err := reconciler.computeMetroPlacementIndex(rctx, []*infrav1.NutanixFailureDomain{fdA, fdB})
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(idx).To(Equal(0))
	})

	t.Run("balances away from an already-placed sibling in the same nodepool", func(t *testing.T) {
		g := NewWithT(t)
		current := placementNM("nm-new", "", false)
		placed := placementNM("nm-old", "fd-a", false)
		reconciler, rctx := newPlacementReconciler(g,
			current,
			placed,
			placementMachine("nm-new", "nm-new", "wmd"),
			placementMachine("nm-old", "nm-old", "wmd"),
		)
		rctx.NutanixMachine = current
		rctx.Machine = placementMachine("nm-new", "nm-new", "wmd")

		idx, err := reconciler.computeMetroPlacementIndex(rctx, []*infrav1.NutanixFailureDomain{fdA, fdB})
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(idx).To(Equal(1))
	})

	t.Run("ignores siblings in a different nodepool", func(t *testing.T) {
		g := NewWithT(t)
		current := placementNM("nm-new", "", false)
		// placed on fd-a, but in a different nodepool, so it must not steer this machine to fd-b.
		placed := placementNM("nm-other", "fd-a", false)
		reconciler, rctx := newPlacementReconciler(g,
			current,
			placed,
			placementMachine("nm-new", "nm-new", "wmd"),
			placementMachine("nm-other", "nm-other", "wmd-other"),
		)
		rctx.NutanixMachine = current
		rctx.Machine = placementMachine("nm-new", "nm-new", "wmd")

		idx, err := reconciler.computeMetroPlacementIndex(rctx, []*infrav1.NutanixFailureDomain{fdA, fdB})
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(idx).To(Equal(0))
	})

	t.Run("skips terminating siblings so their slot is freed", func(t *testing.T) {
		g := NewWithT(t)
		current := placementNM("nm-new", "", false)
		// A(live), B(live), C(terminating). Skipping C makes C the least-loaded FD -> index 2.
		reconciler, rctx := newPlacementReconciler(g,
			current,
			placementNM("nm-a", "fd-a", false),
			placementNM("nm-b", "fd-b", false),
			placementNM("nm-c", "fd-c", true),
			placementMachine("nm-new", "nm-new", "wmd"),
			placementMachine("nm-a", "nm-a", "wmd"),
			placementMachine("nm-b", "nm-b", "wmd"),
			placementMachine("nm-c", "nm-c", "wmd"),
		)
		rctx.NutanixMachine = current
		rctx.Machine = placementMachine("nm-new", "nm-new", "wmd")

		idx, err := reconciler.computeMetroPlacementIndex(rctx, []*infrav1.NutanixFailureDomain{fdA, fdB, fdC})
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(idx).To(Equal(2))
	})

	t.Run("concurrent pending siblings get distinct balanced slots", func(t *testing.T) {
		g := NewWithT(t)
		nmA := placementNM("nm-a", "", false)
		nmB := placementNM("nm-b", "", false)
		objs := []client.Object{
			nmA,
			nmB,
			placementMachine("nm-a", "nm-a", "wmd"),
			placementMachine("nm-b", "nm-b", "wmd"),
		}

		reconcilerA, rctxA := newPlacementReconciler(g, objs...)
		rctxA.NutanixMachine = nmA
		rctxA.Machine = placementMachine("nm-a", "nm-a", "wmd")
		idxA, err := reconcilerA.computeMetroPlacementIndex(rctxA, []*infrav1.NutanixFailureDomain{fdA, fdB})
		g.Expect(err).ToNot(HaveOccurred())

		reconcilerB, rctxB := newPlacementReconciler(g, objs...)
		rctxB.NutanixMachine = nmB
		rctxB.Machine = placementMachine("nm-b", "nm-b", "wmd")
		idxB, err := reconcilerB.computeMetroPlacementIndex(rctxB, []*infrav1.NutanixFailureDomain{fdA, fdB})
		g.Expect(err).ToNot(HaveOccurred())

		// Sorted by name: nm-a -> slot 0, nm-b -> slot 1. Deterministic regardless of interleaving.
		g.Expect(idxA).To(Equal(0))
		g.Expect(idxB).To(Equal(1))
	})

	t.Run("parallel reconciles over a shared client yield a balanced permutation", func(t *testing.T) {
		// Run this with `go test -race` to also catch data races on the shared client/cache.
		g := NewWithT(t)

		const machineCount = 4
		names := []string{"nm-1", "nm-2", "nm-3", "nm-4"}
		objs := make([]client.Object, 0, machineCount*2)
		for _, name := range names {
			objs = append(objs, placementNM(name, "", false))
			objs = append(objs, placementMachine(name, name, "wmd"))
		}

		// A single reconciler (and thus a single fake client) shared by all goroutines, so the
		// parallel reconciles race on the same backing store exactly as they would in production.
		reconciler, base := newPlacementReconciler(g, objs...)
		fds := []*infrav1.NutanixFailureDomain{fdA, fdB}

		results := make([]int, machineCount)
		errs := make([]error, machineCount)
		var wg sync.WaitGroup
		wg.Add(machineCount)
		for i, name := range names {
			go func(i int, name string) {
				defer wg.Done()
				rctx := &nctx.MachineContext{
					Context:        base.Context,
					Cluster:        base.Cluster,
					NutanixCluster: base.NutanixCluster,
					NutanixMachine: placementNM(name, "", false),
					Machine:        placementMachine(name, name, "wmd"),
					Datastore:      map[string]*string{},
				}
				results[i], errs[i] = reconciler.computeMetroPlacementIndex(rctx, fds)
			}(i, name)
		}
		wg.Wait()

		perFD := make([]int, len(fds))
		for i := range names {
			g.Expect(errs[i]).ToNot(HaveOccurred())
			g.Expect(results[i]).To(BeNumerically(">=", 0))
			g.Expect(results[i]).To(BeNumerically("<", len(fds)))
			perFD[results[i]]++
		}

		// 4 machines over 2 FDs must split exactly 2/2, and the assignment is deterministic
		// (sorted by name): nm-1->0, nm-2->1, nm-3->0, nm-4->1.
		g.Expect(perFD).To(Equal([]int{2, 2}))
		g.Expect(results).To(Equal([]int{0, 1, 0, 1}))
	})

	t.Run("falls back to cluster-wide balancing when nodepool is unknown", func(t *testing.T) {
		g := NewWithT(t)
		current := placementNM("nm-new", "", false)
		// placed sibling in a different nodepool, but current machine has no nodepool attribution,
		// so cluster-wide balancing counts it and steers the new machine to the other FD.
		placed := placementNM("nm-other", "fd-a", false)
		reconciler, rctx := newPlacementReconciler(g,
			current,
			placed,
			placementMachine("nm-other", "nm-other", "wmd-other"),
		)
		rctx.NutanixMachine = current
		rctx.Machine = nil // no owning Machine -> empty group key -> cluster-wide

		idx, err := reconciler.computeMetroPlacementIndex(rctx, []*infrav1.NutanixFailureDomain{fdA, fdB})
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(idx).To(Equal(1))
	})

	t.Run("rolling upgrade balances the new MachineSet independently of the old one", func(t *testing.T) {
		g := NewWithT(t)
		// Regression test for the surge-first rollout skew: the new generation must not inherit the
		// old generation's placement (which would skew it, e.g. 3-1). We make the old MachineSet
		// maximally adversarial by placing all of its (still-live) machines on fd-a; balancing by
		// MachineDeployment would push the whole new generation onto fd-b. Scoping to MachineSet
		// isolates the new generation, which must split 2-2.
		const (
			md    = "wmd"
			msOld = "wmd-old"
			msNew = "wmd-new"
		)

		objs := []client.Object{
			// Old generation: 4 live, already-placed machines, all on fd-a.
			placementNM("nm-o1", "fd-a", false), placementMachineInSet("nm-o1", "nm-o1", md, msOld),
			placementNM("nm-o2", "fd-a", false), placementMachineInSet("nm-o2", "nm-o2", md, msOld),
			placementNM("nm-o3", "fd-a", false), placementMachineInSet("nm-o3", "nm-o3", md, msOld),
			placementNM("nm-o4", "fd-a", false), placementMachineInSet("nm-o4", "nm-o4", md, msOld),
			// New generation: 4 pending machines in the new MachineSet.
			placementNM("nm-n1", "", false), placementMachineInSet("nm-n1", "nm-n1", md, msNew),
			placementNM("nm-n2", "", false), placementMachineInSet("nm-n2", "nm-n2", md, msNew),
			placementNM("nm-n3", "", false), placementMachineInSet("nm-n3", "nm-n3", md, msNew),
			placementNM("nm-n4", "", false), placementMachineInSet("nm-n4", "nm-n4", md, msNew),
		}

		reconciler, base := newPlacementReconciler(g, objs...)
		fds := []*infrav1.NutanixFailureDomain{fdA, fdB}

		newNames := []string{"nm-n1", "nm-n2", "nm-n3", "nm-n4"}
		perFD := make([]int, len(fds))
		for _, name := range newNames {
			rctx := &nctx.MachineContext{
				Context:        base.Context,
				Cluster:        base.Cluster,
				NutanixCluster: base.NutanixCluster,
				NutanixMachine: placementNM(name, "", false),
				Machine:        placementMachineInSet(name, name, md, msNew),
				Datastore:      map[string]*string{},
			}
			idx, err := reconciler.computeMetroPlacementIndex(rctx, fds)
			g.Expect(err).ToNot(HaveOccurred())
			perFD[idx]++
		}

		// The new MachineSet splits evenly despite the old generation being skewed onto fd-a.
		g.Expect(perFD).To(Equal([]int{2, 2}))
	})
}
