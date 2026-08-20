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
	"fmt"
	"sort"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	capiv1beta2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// managedDeleteMachineAnnotation marks the cluster.x-k8s.io/delete-machine annotations that this
// controller owns. It lets the balancer add/remove the CAPI delete annotation only on machines it
// manages, so an operator's manually-set delete-machine annotation is respected (counted, never
// cleared).
const managedDeleteMachineAnnotation = "metro.nutanix.com/managed-delete-machine"

// MetroScaleDownBalancerReconciler keeps a stretched-NutanixMetro worker MachineSet balanced across
// its two Prism Element sites during scale-down.
//
// A worker pool placed on a stretched NutanixMetro failure domain exposes a single
// spec.failureDomain to CAPI, so CAPI's MachineSet delete policy is site-blind and can delete the
// machines of one site preferentially, starving that Prism Element (and, for Rook-Ceph clusters,
// wedging OSD scheduling). This controller biases deletion by maintaining the
// cluster.x-k8s.io/delete-machine annotation (documented by CAPI as top priority on every delete
// policy) on the machines of the over-represented site, so scale-down drains the fuller site first
// and the two sites stay balanced.
//
// Known limitation: marking guarantees the imbalance is corrected first. A single scale-down larger
// than needed to reach balance will, past the balance point, fall back to CAPI's site-blind policy
// for the extra victims; the controller re-reconciles and re-marks after each batch. This is a
// strict improvement and cannot re-create the one-sided collapse for normal scale-downs.
type MetroScaleDownBalancerReconciler struct {
	client.Client
	Scheme           *runtime.Scheme
	controllerConfig *ControllerConfig
}

// NewMetroScaleDownBalancerReconciler creates a MetroScaleDownBalancerReconciler.
func NewMetroScaleDownBalancerReconciler(client client.Client, scheme *runtime.Scheme, copts ...ControllerConfigOpts) (*MetroScaleDownBalancerReconciler, error) {
	controllerConf := &ControllerConfig{}
	for _, opt := range copts {
		if err := opt(controllerConf); err != nil {
			return nil, err
		}
	}

	return &MetroScaleDownBalancerReconciler{
		Client:           client,
		Scheme:           scheme,
		controllerConfig: controllerConf,
	}, nil
}

// SetupWithManager sets up the MetroScaleDownBalancer controller with the Manager.
func (r *MetroScaleDownBalancerReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	copts := controller.Options{
		MaxConcurrentReconciles: r.controllerConfig.MaxConcurrentReconciles,
		RateLimiter:             r.controllerConfig.RateLimiter,
		SkipNameValidation:      ptr.To(r.controllerConfig.SkipNameValidation),
	}

	return ctrl.NewControllerManagedBy(mgr).
		Named("MetroScaleDownBalancer-controller").
		For(&capiv1beta2.MachineSet{}).
		Watches(
			&capiv1beta2.Machine{},
			handler.EnqueueRequestsFromMapFunc(
				r.mapMachineToMachineSet(),
			),
		).
		WithOptions(copts).
		Complete(r)
}

// mapMachineToMachineSet enqueues the MachineSet that owns a Machine so a per-machine change (create,
// placement label update, deletion) re-evaluates the whole balancing group.
func (r *MetroScaleDownBalancerReconciler) mapMachineToMachineSet() handler.MapFunc {
	return func(ctx context.Context, o client.Object) []ctrl.Request {
		log := ctrl.LoggerFrom(ctx)
		machine, ok := o.(*capiv1beta2.Machine)
		if !ok {
			log.Error(fmt.Errorf("expected a Machine object but was %T", o), "unexpected type")
			return nil
		}

		msName := machine.Labels[capiv1beta2.MachineSetNameLabel]
		if msName == "" {
			return nil
		}

		return []ctrl.Request{{
			NamespacedName: client.ObjectKey{Name: msName, Namespace: machine.Namespace},
		}}
	}
}

// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=machinesets;machinesets/status,verbs=get;list;watch
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=machines;machines/status,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nutanixmachines,verbs=get;list;watch

func (r *MetroScaleDownBalancerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	ms := &capiv1beta2.MachineSet{}
	if err := r.Get(ctx, req.NamespacedName, ms); err != nil {
		if errors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		log.Error(err, "failed to fetch the MachineSet")
		return reconcile.Result{}, err
	}

	// Only stretched NutanixMetro worker pools are site-blind on scale-down. NutanixMetroSite pools
	// are already pinned to a single site, and non-metro pools have per-machine failure domains, so
	// both are left untouched (guaranteeing zero behavior change for them).
	if !isNutanixMetroFailureDomain(ms.Spec.Template.Spec.FailureDomain) {
		return reconcile.Result{}, nil
	}
	if !ms.DeletionTimestamp.IsZero() {
		return reconcile.Result{}, nil
	}

	machines, err := r.listMachineSetMachines(ctx, ms)
	if err != nil {
		log.Error(err, "failed to list Machines for the MachineSet")
		return reconcile.Result{}, err
	}

	victims, err := r.selectVictims(ctx, ms, machines)
	if err != nil {
		log.Error(err, "failed to select scale-down victims")
		return reconcile.Result{}, err
	}

	if err := r.applyDeleteAnnotations(ctx, machines, victims); err != nil {
		log.Error(err, "failed to apply delete-machine annotations")
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

// listMachineSetMachines returns the Machines owned by the given MachineSet.
func (r *MetroScaleDownBalancerReconciler) listMachineSetMachines(ctx context.Context, ms *capiv1beta2.MachineSet) ([]*capiv1beta2.Machine, error) {
	machineList := &capiv1beta2.MachineList{}
	if err := r.List(ctx, machineList,
		client.InNamespace(ms.Namespace),
		client.MatchingLabels{capiv1beta2.MachineSetNameLabel: ms.Name},
	); err != nil {
		return nil, err
	}

	machines := make([]*capiv1beta2.Machine, 0, len(machineList.Items))
	for i := range machineList.Items {
		machines = append(machines, &machineList.Items[i])
	}
	return machines, nil
}

// selectVictims computes the set of Machine names that should carry the delete-machine annotation so
// that scale-down keeps the metro's two sites balanced.
//
// It groups live (non-deleting) machines by their native site (resolved from the owning
// NutanixMachine's metro.nutanix.com/native-failuredomain label), then greedily picks victims from
// whichever site is currently larger. The number of victims K is:
//   - the pending scale-down amount (spec.replicas < live machine count), when a scale-down is
//     pending, so the exact machines CAPI is about to remove are the balanced ones; otherwise
//   - the imbalance (excess = larger - smaller), as a steady-state bias so a future scale-down still
//     prefers the fuller site.
func (r *MetroScaleDownBalancerReconciler) selectVictims(ctx context.Context, ms *capiv1beta2.MachineSet, machines []*capiv1beta2.Machine) (map[string]struct{}, error) {
	// bySite holds live machines with a known native site, keyed by site name.
	bySite := map[string][]*capiv1beta2.Machine{}
	liveTotal := 0
	knownTotal := 0
	for _, m := range machines {
		if !m.DeletionTimestamp.IsZero() {
			continue
		}
		liveTotal++

		site, ok, err := r.machineSite(ctx, m)
		if err != nil {
			return nil, err
		}
		if !ok {
			// The placement label is not set yet; this machine cannot be attributed to a site this
			// pass. It will be reconsidered once CAPX records its native failure domain.
			continue
		}
		bySite[site] = append(bySite[site], m)
		knownTotal++
	}

	victims := map[string]struct{}{}
	if knownTotal == 0 {
		return victims, nil
	}

	// Sort sites by name for deterministic tie-breaking, and each site's machines newest-first so we
	// prefer to remove the youngest machine of a site (matching CAPI's own default preference and
	// minimizing disruption to long-lived workloads).
	siteNames := make([]string, 0, len(bySite))
	for name := range bySite {
		siteNames = append(siteNames, name)
	}
	sort.Strings(siteNames)
	for _, name := range siteNames {
		sortMachinesNewestFirst(bySite[name])
	}

	excess := siteExcess(bySite, siteNames)

	pendingDelete := 0
	if ms.Spec.Replicas != nil && int(*ms.Spec.Replicas) < liveTotal {
		pendingDelete = liveTotal - int(*ms.Spec.Replicas)
	}

	k := excess
	if pendingDelete > 0 {
		k = pendingDelete
	}
	if k > knownTotal {
		k = knownTotal
	}
	if k <= 0 {
		return victims, nil
	}

	// Greedy: each step remove one machine from whichever site currently has the most remaining
	// machines (ties resolve to the lexicographically-smallest site name). This keeps the remainder
	// as balanced as possible for any K.
	remaining := map[string][]*capiv1beta2.Machine{}
	for name, list := range bySite {
		remaining[name] = append([]*capiv1beta2.Machine(nil), list...)
	}
	for i := 0; i < k; i++ {
		pick := ""
		best := -1
		for _, name := range siteNames {
			if n := len(remaining[name]); n > best {
				best = n
				pick = name
			}
		}
		if best <= 0 {
			break
		}
		victim := remaining[pick][0]
		remaining[pick] = remaining[pick][1:]
		victims[victim.Name] = struct{}{}
	}

	return victims, nil
}

// applyDeleteAnnotations reconciles the delete-machine annotation on every machine in the group to
// match the desired victim set, respecting operator-set annotations.
func (r *MetroScaleDownBalancerReconciler) applyDeleteAnnotations(ctx context.Context, machines []*capiv1beta2.Machine, victims map[string]struct{}) error {
	for _, m := range machines {
		if !m.DeletionTimestamp.IsZero() {
			continue
		}

		_, wantVictim := victims[m.Name]
		_, hasDelete := m.Annotations[capiv1beta2.DeleteMachineAnnotation]
		_, isManaged := m.Annotations[managedDeleteMachineAnnotation]

		switch {
		case wantVictim:
			// Already annotated (by us or by an operator): nothing to do; respect operator intent.
			if hasDelete {
				continue
			}
			if err := r.patchMachineAnnotations(ctx, m, true); err != nil {
				return err
			}
		default:
			// Only remove annotations that we own; never clear an operator's manual delete-machine.
			if isManaged {
				if err := r.patchMachineAnnotations(ctx, m, false); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// patchMachineAnnotations sets (mark=true) or clears (mark=false) the managed delete-machine
// annotations on a Machine using a conflict-safe patch.
func (r *MetroScaleDownBalancerReconciler) patchMachineAnnotations(ctx context.Context, m *capiv1beta2.Machine, mark bool) error {
	helper, err := patch.NewHelper(m, r.Client)
	if err != nil {
		return err
	}

	if mark {
		if m.Annotations == nil {
			m.Annotations = map[string]string{}
		}
		m.Annotations[capiv1beta2.DeleteMachineAnnotation] = "true"
		m.Annotations[managedDeleteMachineAnnotation] = "true"
	} else {
		delete(m.Annotations, capiv1beta2.DeleteMachineAnnotation)
		delete(m.Annotations, managedDeleteMachineAnnotation)
	}

	return helper.Patch(ctx, m)
}

// machineSite resolves the native metro site (NutanixFailureDomain name) a Machine is placed on, read
// from the owning NutanixMachine's metro.nutanix.com/native-failuredomain label. ok is false when the
// placement has not been recorded yet.
func (r *MetroScaleDownBalancerReconciler) machineSite(ctx context.Context, m *capiv1beta2.Machine) (string, bool, error) {
	infraName := m.Spec.InfrastructureRef.Name
	if infraName == "" {
		return "", false, nil
	}

	nm := &infrav1.NutanixMachine{}
	if err := r.Get(ctx, client.ObjectKey{Name: infraName, Namespace: m.Namespace}, nm); err != nil {
		if errors.IsNotFound(err) {
			return "", false, nil
		}
		return "", false, err
	}

	site, ok := nm.Labels[metroNativeFailureDomainLabelKey]
	if !ok || site == "" {
		return "", false, nil
	}
	return site, true, nil
}

// siteExcess returns the difference between the largest and smallest site machine counts across the
// given sites. It is zero when fewer than two sites are represented (no cross-site imbalance is
// possible).
func siteExcess(bySite map[string][]*capiv1beta2.Machine, siteNames []string) int {
	if len(siteNames) < 2 {
		return 0
	}
	minCount := -1
	maxCount := 0
	for _, name := range siteNames {
		n := len(bySite[name])
		if n > maxCount {
			maxCount = n
		}
		if minCount < 0 || n < minCount {
			minCount = n
		}
	}
	return maxCount - minCount
}

// sortMachinesNewestFirst orders machines by creation time descending, breaking ties by name for
// determinism.
func sortMachinesNewestFirst(machines []*capiv1beta2.Machine) {
	sort.SliceStable(machines, func(i, j int) bool {
		ti := machines[i].CreationTimestamp
		tj := machines[j].CreationTimestamp
		if ti.Equal(&tj) {
			return machines[i].Name < machines[j].Name
		}
		return ti.After(tj.Time)
	})
}
