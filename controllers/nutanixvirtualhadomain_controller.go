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
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	nutanixclient "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/pkg/client"
	nctx "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/pkg/context"
	"github.com/nutanix-cloud-native/prism-go-client/converged"
	v3models "github.com/nutanix-cloud-native/prism-go-client/v3/models"
	dpModels "github.com/nutanix/ntnx-api-golang-clients/datapolicies-go-client/v4/models/datapolicies/v4/config"
	dpCommon "github.com/nutanix/ntnx-api-golang-clients/datapolicies-go-client/v4/models/dataprotection/v4/common"
	corev1 "k8s.io/api/core/v1"
	kapierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	coreinformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/utils/ptr"
	capiutil "sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	ctrlutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	// VHADomainDefaultCategoryKey is the default vHADomain category key (shared across all clusters).
	VHADomainDefaultCategoryKey = "k8s-vha-native-site"

	// vhaDefaultMovementGroup is the name of the single movement group generated for a vHA domain.
	vhaDefaultMovementGroup = "default"

	// vhaResyncInterval is how often a successfully-reconciled vHADomain is re-enqueued so that its
	// Prism Central resources (categories, protection policy, recovery plans) are re-validated.
	// Those resources live in Prism Central and cannot be watched, so periodic requeue is used to
	// detect and heal out-of-band deletion/drift.
	vhaResyncInterval = 5 * time.Minute
)

// NutanixVirtualHADomainReconciler reconciles a NutanixVirtualHADomain object
type NutanixVirtualHADomainReconciler struct {
	client.Client
	SecretInformer    coreinformers.SecretInformer
	ConfigMapInformer coreinformers.ConfigMapInformer
	Scheme            *runtime.Scheme
	controllerConfig  *ControllerConfig
}

func NewNutanixVirtualHADomainReconciler(client client.Client, secretInformer coreinformers.SecretInformer, configMapInformer coreinformers.ConfigMapInformer, scheme *runtime.Scheme, copts ...ControllerConfigOpts) (*NutanixVirtualHADomainReconciler, error) {
	controllerConf := &ControllerConfig{}
	for _, opt := range copts {
		if err := opt(controllerConf); err != nil {
			return nil, err
		}
	}

	return &NutanixVirtualHADomainReconciler{
		Client:            client,
		SecretInformer:    secretInformer,
		ConfigMapInformer: configMapInformer,
		Scheme:            scheme,
		controllerConfig:  controllerConf,
	}, nil
}

// SetupWithManager sets up the NutanixVirtualHADomain controller with the Manager.
func (r *NutanixVirtualHADomainReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	copts := controller.Options{
		MaxConcurrentReconciles: r.controllerConfig.MaxConcurrentReconciles,
		RateLimiter:             r.controllerConfig.RateLimiter,
		SkipNameValidation:      ptr.To(r.controllerConfig.SkipNameValidation),
	}

	return ctrl.NewControllerManagedBy(mgr).
		Named("nutanixvirtualhadomain-controller").
		For(&infrav1.NutanixVirtualHADomain{}).
		WithOptions(copts).
		Complete(r)
}

// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nutanixclusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nutanixvirtualhadomains,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nutanixvirtualhadomains/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nutanixvirtualhadomains/finalizers,verbs=get;update;patch

func (r *NutanixVirtualHADomainReconciler) Reconcile(ctx context.Context, req ctrl.Request) (res ctrl.Result, reterr error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Reconciling the NutanixVirtualHADomain")

	// Fetch the NutanixVirtualHADomain object
	vHADomain, err := getNutanixVHADomainObject(ctx, r.Client, req.Name, req.Namespace)
	if err != nil {
		if kapierrors.IsNotFound(err) {
			log.V(1).Info("NutanixVirtualHADomain not found, likely already deleted")
			return reconcile.Result{}, nil
		}
		log.Error(err, "Failed at reconciling")
		return reconcile.Result{}, err
	}

	// keep the original object for restoring its spec if reconciling fails
	vHADomainOrig := vHADomain.DeepCopy()

	// Initialize the patch helper.
	patchHelper, err := patch.NewHelper(vHADomain, r.Client)
	if err != nil {
		log.Error(err, "Failed to configure the patch helper")
		return ctrl.Result{Requeue: true}, nil
	}

	defer func() {
		// Always attempt to Patch the NutanixVirtualHADomain object and its status after each reconciliation.
		if err := patchHelper.Patch(ctx, vHADomain); err != nil {
			reterr = kerrors.NewAggregate([]error{reterr, err})
			log.Error(reterr, "Failed to patch NutanixVirtualHADomain")
		} else {
			log.Info("Patched NutanixVirtualHADomain", "status", vHADomain.Status, "finalizers", vHADomain.Finalizers)
		}
	}()

	// Handle deletion first, before resolving the owning Cluster/NutanixCluster or
	// the Prism Central clients. Those owners may already be garbage-collected; if
	// we required them here, the lookup would error and we would never reach the
	// deletion handler, trapping the finalizer and blocking deletion forever.
	if !vHADomain.DeletionTimestamp.IsZero() {
		if err = r.reconcileDelete(ctx, vHADomain); err != nil {
			log.Error(err, "failed at deletion reconciling")
		}
		return ctrl.Result{}, err
	}

	// Add finalizer if not set yet.
	if !ctrlutil.ContainsFinalizer(vHADomain, infrav1.NutanixVirtualHADomainFinalizer) {
		if ctrlutil.AddFinalizer(vHADomain, infrav1.NutanixVirtualHADomainFinalizer) {
			log.Info("Added the finalizer to the object", "finalizers", vHADomain.Finalizers)
			return reconcile.Result{}, nil
		}
	}

	// Fetch the CAPI Cluster.
	cluster, err := capiutil.GetClusterFromMetadata(ctx, r.Client, vHADomain.ObjectMeta)
	if err != nil {
		log.Error(err, "vHADomain is missing cluster label or cluster does not exist")
		return reconcile.Result{}, err
	}

	// Fetch the NutanixCluster
	ntnxCluster := &infrav1.NutanixCluster{}
	nclKey := client.ObjectKey{
		Namespace: cluster.Namespace,
		Name:      cluster.Spec.InfrastructureRef.Name,
	}
	if err = r.Get(ctx, nclKey, ntnxCluster); err != nil {
		log.Error(err, "Failed to fetch the NutanixCluster")
		return reconcile.Result{}, err
	}

	// Set the NutanixCluster as the vHADomain's controller owner if not set yet
	hasOwner, err := ctrlutil.HasOwnerReference(vHADomain.OwnerReferences, ntnxCluster, r.Scheme)
	if err != nil {
		log.Error(err, "Failed to check if the NutanixCluster is the owner")
		return reconcile.Result{}, err
	}
	if !hasOwner {
		if err = ctrlutil.SetControllerReference(ntnxCluster, vHADomain, r.Scheme); err != nil {
			log.Error(err, "Failed to set the NutanixCluster as the owner of the vHADomain")
			return reconcile.Result{}, err
		}
	}

	v3Client, err := getPrismCentralClientForCluster(ctx, ntnxCluster, r.SecretInformer, r.ConfigMapInformer)
	if err != nil {
		log.Error(err, "error occurred while fetching prism central client")
		return reconcile.Result{}, err
	}
	convergedClient, err := getPrismCentralConvergedV4ClientForCluster(ctx, ntnxCluster, r.SecretInformer, r.ConfigMapInformer)
	if err != nil {
		log.Error(err, "error occurred while fetching prism central converged client")
		return reconcile.Result{}, err
	}
	rctx := &nctx.VHADomainContext{
		Context:         ctx,
		Cluster:         cluster,
		NutanixCluster:  ntnxCluster,
		VHADomain:       vHADomain,
		NutanixClient:   v3Client,
		ConvergedClient: convergedClient,
	}

	if err = r.reconcileNormal(rctx); err != nil {
		log.Error(err, "failed at normal reconciling, restore to original spec.")
		vHADomain.Spec = vHADomainOrig.Spec
		return ctrl.Result{}, err
	}

	// Periodically re-enqueue so the vHADomain's Prism Central resources are re-validated. PC
	// resources cannot be watched, so this requeue is what detects out-of-band deletion/drift.
	return ctrl.Result{RequeueAfter: vhaResyncInterval}, nil
}

func (r *NutanixVirtualHADomainReconciler) reconcileDelete(ctx context.Context, vHADomain *infrav1.NutanixVirtualHADomain) error {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Handling NutanixVirtualHADomain deletion")

	// Resolve the owning Cluster and NutanixCluster tolerantly. A NotFound means the
	// owner has already been garbage-collected, which is the terminal state of its
	// deletion and therefore safe for the vHADomain to be cleaned up.
	var blockingErrs []error

	cluster, err := capiutil.GetClusterFromMetadata(ctx, r.Client, vHADomain.ObjectMeta)
	switch {
	case err == nil:
		if cluster.DeletionTimestamp.IsZero() {
			blockingErrs = append(blockingErrs, fmt.Errorf("cluster %s is not in deletion", cluster.Name))
		}
	case kapierrors.IsNotFound(err):
		log.V(1).Info("Owning Cluster already deleted, treating as safe for deletion")
		cluster = nil
	default:
		// Missing cluster label or a transient lookup error. We cannot resolve the
		// owner, so proceed with best-effort cleanup rather than trapping the
		// finalizer forever.
		log.V(1).Info("Could not resolve owning Cluster, proceeding with best-effort deletion", "error", err.Error())
		cluster = nil
	}

	// Determine the NutanixCluster name from the resolved Cluster's infrastructureRef
	// when available, otherwise fall back to the controller owner reference.
	ntnxClusterName := ""
	if cluster != nil {
		ntnxClusterName = cluster.Spec.InfrastructureRef.Name
	} else {
		for _, ref := range vHADomain.OwnerReferences {
			if ref.Kind == infrav1.NutanixClusterKind {
				ntnxClusterName = ref.Name
				break
			}
		}
	}

	var ntnxCluster *infrav1.NutanixCluster
	if ntnxClusterName != "" {
		nc := &infrav1.NutanixCluster{}
		nclKey := client.ObjectKey{Namespace: vHADomain.Namespace, Name: ntnxClusterName}
		switch err := r.Get(ctx, nclKey, nc); {
		case err == nil:
			ntnxCluster = nc
			if ntnxCluster.DeletionTimestamp.IsZero() {
				blockingErrs = append(blockingErrs, fmt.Errorf("NutanixCluster %s is not in deletion", ntnxCluster.Name))
			}
		case kapierrors.IsNotFound(err):
			log.V(1).Info("Owning NutanixCluster already deleted, skipping PC resource cleanup")
		default:
			return fmt.Errorf("failed to fetch the NutanixCluster %s: %w", ntnxClusterName, err)
		}
	}

	if len(blockingErrs) > 0 {
		err := errors.Join(blockingErrs...)
		conditions.Set(vHADomain, metav1.Condition{
			Type:    infrav1.VHADomainSafeForDeletionCondition,
			Status:  metav1.ConditionFalse,
			Reason:  infrav1.VHADomainOwnerNotInDeletion,
			Message: err.Error(),
		})

		log.Error(err, "cannot delete the vHADomain since its corresponding Cluster/NutanixCluster is not in deletion")
		return err
	}

	// Best-effort cleanup of the PC resources. This is only possible while the
	// NutanixCluster (and therefore its Prism Central credentials) still exists. If
	// it is already gone, the PC resources are re-adopted by name on recreate, so
	// skipping cleanup here does not cause duplication.
	if ntnxCluster != nil {
		if err := r.cleanupVHADomainPCResources(ctx, vHADomain, ntnxCluster); err != nil {
			e1 := fmt.Errorf("failed to clean up vHADomain PC resources: %w", err)
			conditions.Set(vHADomain, metav1.Condition{
				Type:    infrav1.VHADomainSafeForDeletionCondition,
				Status:  metav1.ConditionFalse,
				Reason:  infrav1.VHADomainFailedCleanup,
				Message: e1.Error(),
			})

			log.Error(e1, "cannot delete the vHADomain")
			return e1
		}
	} else {
		log.Info("Owning NutanixCluster is not available, skipping PC resource cleanup")
	}

	// The owning Cluster/NutanixCluster is in deletion (or already gone) and the PC resources have
	// been cleaned up, so the object is now safe for deletion.
	conditions.Set(vHADomain, metav1.Condition{
		Type:   infrav1.VHADomainSafeForDeletionCondition,
		Status: metav1.ConditionTrue,
		Reason: infrav1.VHADomainCleanupSucceeded,
	})

	// Remove the finalizer from the NutanixVirtualHADomain object
	ctrlutil.RemoveFinalizer(vHADomain, infrav1.NutanixVirtualHADomainFinalizer)
	log.Info("Removed finalizer from the vHADomain object", "finalizer", infrav1.NutanixVirtualHADomainFinalizer)

	return nil
}

// cleanupVHADomainPCResources builds the Prism Central clients for the given
// NutanixCluster and removes the vHADomain's PC resources.
func (r *NutanixVirtualHADomainReconciler) cleanupVHADomainPCResources(
	ctx context.Context,
	vHADomain *infrav1.NutanixVirtualHADomain,
	ntnxCluster *infrav1.NutanixCluster,
) error {
	v3Client, err := getPrismCentralClientForCluster(ctx, ntnxCluster, r.SecretInformer, r.ConfigMapInformer)
	if err != nil {
		return fmt.Errorf("error occurred while fetching prism central client: %w", err)
	}
	convergedClient, err := getPrismCentralConvergedV4ClientForCluster(ctx, ntnxCluster, r.SecretInformer, r.ConfigMapInformer)
	if err != nil {
		return fmt.Errorf("error occurred while fetching prism central converged client: %w", err)
	}

	rctx := &nctx.VHADomainContext{
		Context:         ctx,
		NutanixCluster:  ntnxCluster,
		VHADomain:       vHADomain,
		NutanixClient:   v3Client,
		ConvergedClient: convergedClient,
	}
	return r.deleteVHADomainPCResources(rctx)
}

func (r *NutanixVirtualHADomainReconciler) reconcileNormal(rctx *nctx.VHADomainContext) error {
	log := ctrl.LoggerFrom(rctx.Context)
	log.Info("Handling NutanixVirtualHADomain reconciling")

	namespace := rctx.VHADomain.Namespace

	// fetch NutanixMetro and its failureDomain CRs
	metroObj, err := getNutanixMetroObject(rctx.Context, r.Client, rctx.VHADomain.Spec.MetroRef.Name, namespace)
	if err != nil {
		rctx.VHADomain.Status.Ready = false
		return err
	}

	failureDomains := make([]*infrav1.NutanixFailureDomain, 0, len(metroObj.Spec.FailureDomains))
	for _, fdRef := range metroObj.Spec.FailureDomains {
		fd, err := getNutanixFailureDomainObject(rctx.Context, r.Client, fdRef.Name, namespace)
		if err != nil {
			rctx.VHADomain.Status.Ready = false
			return err
		}
		failureDomains = append(failureDomains, fd)
	}

	// Get or create the PC resources of the vHA domain and persist their UUIDs in the spec.
	if err := r.ensureVHADomainPCResources(rctx, failureDomains); err != nil {
		rctx.VHADomain.Status.Ready = false
		return err
	}

	// Compute readiness from the (now resolved) spec.
	allGroupsReady := len(rctx.VHADomain.Spec.MovementGroups) > 0
	for i := range rctx.VHADomain.Spec.MovementGroups {
		movementGroup := rctx.VHADomain.Spec.MovementGroups[i]
		groupReady := len(movementGroup.CategoryRecoveryPlans) > 0
		for j := range movementGroup.CategoryRecoveryPlans {
			if movementGroup.CategoryRecoveryPlans[j].RecoveryPlan.String() == "" {
				groupReady = false
			}
		}
		if !groupReady {
			allGroupsReady = false
		}
	}

	rctx.VHADomain.Status.Ready = rctx.VHADomain.Spec.ProtectionPolicy != nil &&
		rctx.VHADomain.Spec.ProtectionPolicy.String() != "" &&
		allGroupsReady

	return nil
}

// ensureVHADomainPCResources creates or validates the PC resources (categories,
// protection policy, recovery plans) for the vHA domain. All resources are
// derived entirely from the metro's failure domains; any user-provided
// movementGroups/protectionGroup are ignored and overwritten with the
// controller-generated values.
//
// When the vHADomain is already ready, the function instead validates that the
// generated PC resources still exist in Prism Central and sets the
// VHADomainPCResourcesValidated condition accordingly.
func (r *NutanixVirtualHADomainReconciler) ensureVHADomainPCResources(
	rctx *nctx.VHADomainContext,
	failureDomains []*infrav1.NutanixFailureDomain,
) error {
	log := ctrl.LoggerFrom(rctx.Context)

	// A vHA domain spans exactly the two prism elements of its metro.
	if len(failureDomains) != 2 {
		return fmt.Errorf("vHA domain requires a metro with exactly 2 failure domains, got %d", len(failureDomains))
	}

	if rctx.VHADomain.Status.Ready {
		log.Info("vHADomain is ready, validating PC resources still exist")
		return r.validateVHADomainPCResources(rctx, failureDomains)
	}

	// Resolve the PE UUID, name and subnet for each failure domain, preserving
	// the metro's failure-domain ordering (used as the per-PE index for the
	// generated categories and recovery plans).
	// These slices are positionally indexed by failure-domain order: peUUIDs[i], peNames[i] and
	// subnetNames[i] all describe failureDomains[i]. subnetNames must therefore be index-assigned
	// (not conditionally appended) so a failure domain without subnets does not shift the entries of
	// the others and map the wrong subnet to a PE in the recovery plan network mapping.
	peUUIDs := make([]string, len(failureDomains))
	peNames := make([]string, len(failureDomains))
	subnetNames := make([]string, len(failureDomains))
	for i, fd := range failureDomains {
		peUUID, err := GetPEUUID(rctx.Context, rctx.ConvergedClient,
			fd.Spec.PrismElementCluster.Name, fd.Spec.PrismElementCluster.UUID)
		if err != nil {
			return fmt.Errorf("failed to get PE UUID for failure domain %s: %w", fd.Name, err)
		}
		peUUIDs[i] = peUUID
		peNames[i] = fd.Spec.PrismElementCluster.String()
		if len(fd.Spec.Subnets) > 0 {
			subnetNames[i] = fd.Spec.Subnets[0].String()
		}
	}

	azURL, err := getDomainManagerExtId(rctx)
	if err != nil {
		return fmt.Errorf("failed to get local availability zone URL: %w", err)
	}

	// Determine the movement groups to reconcile. User-provided group names are
	// preserved (their contents are regenerated); when none are provided a single
	// "default" group is synthesized. Names are sorted for deterministic ordering.
	groupNames := make([]string, 0, len(rctx.VHADomain.Spec.MovementGroups))
	for i := range rctx.VHADomain.Spec.MovementGroups {
		groupNames = append(groupNames, rctx.VHADomain.Spec.MovementGroups[i].Name)
	}
	if len(groupNames) == 0 {
		groupNames = append(groupNames, vhaDefaultMovementGroup)
	}
	sort.Strings(groupNames)

	// Generate one category and one recovery plan per failure domain, per group.
	// All categories are collected so the protection policy can tag them.
	movementGroups := make([]infrav1.NutanixMovementGroup, 0, len(groupNames))
	allCategories := make([]infrav1.NutanixCategoryIdentifier, 0, len(groupNames)*len(failureDomains))
	for _, group := range groupNames {
		categories, err := r.getOrCreateVHADomainGroupCategories(rctx, group, failureDomains)
		if err != nil {
			return err
		}
		allCategories = append(allCategories, categories...)

		categoryRecoveryPlans := make([]infrav1.NutanixCategoryRecoveryPlan, len(failureDomains))
		for i, fd := range failureDomains {
			categoryRecoveryPlans[i] = infrav1.NutanixCategoryRecoveryPlan{
				Category:         categories[i],
				FailureDomainRef: corev1.LocalObjectReference{Name: fd.Name},
			}
		}
		movementGroups = append(movementGroups, infrav1.NutanixMovementGroup{
			Name:                  group,
			CategoryRecoveryPlans: categoryRecoveryPlans,
		})
	}

	// Generate and ensure the protection policy (synchronous replication, RPO=0)
	// tagged with all generated categories across the two PEs.
	protectionPolicy, err := r.getOrCreateVHADomainProtectionPolicy(rctx, allCategories, peUUIDs)
	if err != nil {
		return err
	}

	// Generate and ensure one recovery plan per failure domain, per group, and
	// store the resolved UUID back into the corresponding mapping.
	recoveryPlanCount := 0
	for gi := range movementGroups {
		group := movementGroups[gi].Name
		for i := range movementGroups[gi].CategoryRecoveryPlans {
			recoveryPlan, err := r.getOrCreateVHADomainRecoveryPlan(rctx, group, peUUIDs, peNames, subnetNames, i, azURL)
			if err != nil {
				return err
			}
			movementGroups[gi].CategoryRecoveryPlans[i].RecoveryPlan = *recoveryPlan
			recoveryPlanCount++
		}
	}

	// Persist the generated resources back into the spec.
	rctx.VHADomain.Spec.MovementGroups = movementGroups
	rctx.VHADomain.Spec.ProtectionPolicy = protectionPolicy

	conditions.Set(rctx.VHADomain, metav1.Condition{
		Type:   infrav1.VHADomainPCResourcesValidatedCondition,
		Status: metav1.ConditionTrue,
		Reason: infrav1.VHADomainPCResourcesValidReason,
	})

	log.Info("Successfully ensured vHADomain PC resources",
		"movementGroups", len(movementGroups),
		"categories", len(allCategories),
		"protectionPolicy", protectionPolicy.DisplayString(),
		"recoveryPlans", recoveryPlanCount)

	return nil
}

// validateVHADomainPCResources verifies that all PC resources referenced by the
// vHADomain spec still exist in Prism Central (categories, protection policy,
// recovery plans). The result is reflected in the VHADomainPCResourcesValidated
// condition.
func (r *NutanixVirtualHADomainReconciler) validateVHADomainPCResources(
	rctx *nctx.VHADomainContext,
	failureDomains []*infrav1.NutanixFailureDomain,
) error {
	log := ctrl.LoggerFrom(rctx.Context)
	var validationErrors []error

	fdIndexByName := make(map[string]int, len(failureDomains))
	for idx, fd := range failureDomains {
		fdIndexByName[fd.Name] = idx
	}

	seen := make(map[string]struct{})
	for _, movementGroup := range rctx.VHADomain.Spec.MovementGroups {
		for i := range movementGroup.CategoryRecoveryPlans {
			category := movementGroup.CategoryRecoveryPlans[i].Category
			key := category.Key + "/" + category.Value
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			found, err := getCategory(rctx.Context, rctx.ConvergedClient, category.Key, category.Value)
			if err != nil {
				validationErrors = append(validationErrors,
					fmt.Errorf("failed to validate category %s/%s: %w", category.Key, category.Value, err))
				continue
			}
			if found == nil {
				validationErrors = append(validationErrors,
					fmt.Errorf("category %s/%s no longer exists in Prism Central", category.Key, category.Value))
			}
		}
	}

	if pp := rctx.VHADomain.Spec.ProtectionPolicy; pp != nil {
		if err := r.validateProtectionPolicyExists(rctx, pp); err != nil {
			validationErrors = append(validationErrors, err)
		}
	}

	for _, movementGroup := range rctx.VHADomain.Spec.MovementGroups {
		groupName := movementGroup.Name
		for i := range movementGroup.CategoryRecoveryPlans {
			crp := movementGroup.CategoryRecoveryPlans[i]
			idx, ok := fdIndexByName[crp.FailureDomainRef.Name]
			if !ok {
				validationErrors = append(validationErrors,
					fmt.Errorf("movementGroup %s: failureDomainRef %q is not part of metro %q",
						groupName, crp.FailureDomainRef.Name, rctx.VHADomain.Spec.MetroRef.Name))
				continue
			}
			if err := r.validateRecoveryPlanExists(rctx, &crp.RecoveryPlan, groupName, idx); err != nil {
				validationErrors = append(validationErrors, err)
			}
		}
	}

	if len(validationErrors) > 0 {
		joinedErr := errors.Join(validationErrors...)
		errMsg := joinedErr.Error()
		log.Info("vHADomain PC resources validation failed", "errors", errMsg)
		rctx.VHADomain.Status.Ready = false
		conditions.Set(rctx.VHADomain, metav1.Condition{
			Type:    infrav1.VHADomainPCResourcesValidatedCondition,
			Status:  metav1.ConditionFalse,
			Reason:  infrav1.VHADomainPCResourcesValidationFailedReason,
			Message: errMsg,
		})
		return joinedErr
	}

	log.V(1).Info("All vHADomain PC resources validated successfully")
	conditions.Set(rctx.VHADomain, metav1.Condition{
		Type:   infrav1.VHADomainPCResourcesValidatedCondition,
		Status: metav1.ConditionTrue,
		Reason: infrav1.VHADomainPCResourcesValidReason,
	})
	return nil
}

func (r *NutanixVirtualHADomainReconciler) validateProtectionPolicyExists(
	rctx *nctx.VHADomainContext,
	pp *infrav1.NutanixResourceIdentifier,
) error {
	display := pp.DisplayString()
	if pp.IsUUID() {
		_, err := rctx.ConvergedClient.DataPolicies.ProtectionPolicies.Get(rctx.Context, *pp.UUID)
		if err == nil {
			return nil
		}
		if converged.IsNotFound(err) {
			return fmt.Errorf("protection policy %s no longer exists in Prism Central", display)
		}
		return fmt.Errorf("failed to validate protection policy %s: %w", display, err)
	}
	if pp.IsName() {
		found, err := findProtectionPolicyByName(rctx, *pp.Name)
		if err != nil {
			return fmt.Errorf("failed to validate protection policy %s: %w", display, err)
		}
		if found == nil {
			return fmt.Errorf("protection policy %s no longer exists in Prism Central", display)
		}
	}
	return nil
}

func (r *NutanixVirtualHADomainReconciler) validateRecoveryPlanExists(
	rctx *nctx.VHADomainContext,
	rp *infrav1.NutanixResourceIdentifier,
	group string,
	idx int,
) error {
	display := rp.DisplayString()
	if rp.IsUUID() {
		_, err := rctx.NutanixClient.V3.GetRecoveryPlan(rctx.Context, *rp.UUID)
		if err == nil {
			return nil
		}
		if strings.Contains(fmt.Sprint(err), "ENTITY_NOT_FOUND") {
			return fmt.Errorf("recovery plan %s no longer exists in Prism Central", display)
		}
		return fmt.Errorf("failed to validate recovery plan %s: %w", display, err)
	}
	rpName := vhaRecoveryPlanName(rctx.VHADomain.Name, group, idx)
	found, err := findRecoveryPlanByName(rctx, rpName)
	if err != nil {
		return fmt.Errorf("failed to validate recovery plan %s: %w", display, err)
	}
	if found == nil {
		return fmt.Errorf("recovery plan %s no longer exists in Prism Central", display)
	}
	return nil
}

// vhaCategoryValue returns the category value generated for a vHA domain's
// movement group at the given prism-element index. The category key is shared
// across all clusters (VHADomainDefaultCategoryKey).
func vhaCategoryValue(vHADomainName, group string, idx int) string {
	return fmt.Sprintf("k8s-vha-capx-%s-%s-%d", vHADomainName, group, idx)
}

// vhaRecoveryPlanName returns the recovery plan name generated for a vHA domain's
// movement group at the given primary prism-element index.
func vhaRecoveryPlanName(vHADomainName, group string, idx int) string {
	return fmt.Sprintf("k8s-vha-capx-%s-%s-%d", vHADomainName, group, idx)
}

// vhaProtectionPolicyName returns the protection policy name generated for a vHA domain.
func vhaProtectionPolicyName(vHADomainName string) string {
	return fmt.Sprintf("k8s-vha-capx-%s", vHADomainName)
}

// getOrCreateVHADomainGroupCategories generates one category per failure domain
// for the given movement group (keyed by VHADomainDefaultCategoryKey, valued by
// the group name and the failure domain's index in the metro) and ensures it
// exists in Prism Central. The returned categories are ordered to match the
// failure domains.
func (r *NutanixVirtualHADomainReconciler) getOrCreateVHADomainGroupCategories(
	rctx *nctx.VHADomainContext,
	group string,
	failureDomains []*infrav1.NutanixFailureDomain,
) ([]infrav1.NutanixCategoryIdentifier, error) {
	log := ctrl.LoggerFrom(rctx.Context)

	categories := make([]infrav1.NutanixCategoryIdentifier, 0, len(failureDomains))
	for i := range failureDomains {
		ci := infrav1.NutanixCategoryIdentifier{
			Key:   VHADomainDefaultCategoryKey,
			Value: vhaCategoryValue(rctx.VHADomain.Name, group, i),
		}
		if _, err := getOrCreateCategory(rctx.Context, rctx.ConvergedClient, &ci); err != nil {
			return nil, fmt.Errorf("movementGroup %s: failed to get or create category %s/%s: %w", group, ci.Key, ci.Value, err)
		}
		categories = append(categories, ci)
		log.V(1).Info("Ensured vHA category", "group", group, "key", ci.Key, "value", ci.Value)
	}

	return categories, nil
}

// getOrCreateVHADomainProtectionPolicy creates or retrieves the protection
// policy for the vHA domain. The policy protects VMs tagged with the vHA
// categories using synchronous replication (RPO=0) between the two PEs. The
// returned identifier is always resolved to a UUID.
func (r *NutanixVirtualHADomainReconciler) getOrCreateVHADomainProtectionPolicy(
	rctx *nctx.VHADomainContext,
	categories []infrav1.NutanixCategoryIdentifier,
	peUUIDs []string,
) (*infrav1.NutanixResourceIdentifier, error) {
	log := ctrl.LoggerFrom(rctx.Context)
	ppName := vhaProtectionPolicyName(rctx.VHADomain.Name)

	existing, err := findProtectionPolicyByName(rctx, ppName)
	if err != nil {
		return nil, err
	}
	if existing != nil && existing.ExtId != nil {
		log.Info("Protection policy already exists", "name", ppName, "uuid", *existing.ExtId)
		return &infrav1.NutanixResourceIdentifier{
			Type: infrav1.NutanixIdentifierUUID,
			UUID: existing.ExtId,
		}, nil
	}

	if len(peUUIDs) != 2 {
		return nil, fmt.Errorf("expected exactly 2 prism elements for the metro to create protection policy, got %d", len(peUUIDs))
	}

	pcExtId, err := getDomainManagerExtId(rctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get Prism Central ExtId: %w", err)
	}

	categoryExtIds := make([]string, 0, len(categories))
	for _, c := range categories {
		cat, err := getOrCreateCategory(rctx.Context, rctx.ConvergedClient, &infrav1.NutanixCategoryIdentifier{
			Key:   c.Key,
			Value: c.Value,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to get category ExtId for %s/%s: %w", c.Key, c.Value, err)
		}
		if cat.ExtId == nil {
			return nil, fmt.Errorf("category %s/%s has no ExtId", c.Key, c.Value)
		}
		categoryExtIds = append(categoryExtIds, *cat.ExtId)
	}

	subLoc0 := dpModels.NewNutanixCluster()
	subLoc0.ClusterExtIds = []string{peUUIDs[0]}
	loc0 := dpModels.NewReplicationLocation()
	loc0.Label = ptr.To("loc-0")
	loc0.DomainManagerExtId = ptr.To(pcExtId)
	loc0.IsPrimary = ptr.To(true)
	if err := loc0.SetReplicationSubLocation(*subLoc0); err != nil {
		return nil, fmt.Errorf("failed to set replication sub-location for loc-0: %w", err)
	}

	subLoc1 := dpModels.NewNutanixCluster()
	subLoc1.ClusterExtIds = []string{peUUIDs[1]}
	loc1 := dpModels.NewReplicationLocation()
	loc1.Label = ptr.To("loc-1")
	loc1.DomainManagerExtId = ptr.To(pcExtId)
	if err := loc1.SetReplicationSubLocation(*subLoc1); err != nil {
		return nil, fmt.Errorf("failed to set replication sub-location for loc-1: %w", err)
	}

	schedule0to1 := dpModels.NewSchedule()
	schedule0to1.RecoveryPointObjectiveTimeSeconds = ptr.To(0)
	schedule0to1.RecoveryPointType = dpCommon.RECOVERYPOINTTYPE_CRASH_CONSISTENT.Ref()

	schedule1to0 := dpModels.NewSchedule()
	schedule1to0.RecoveryPointObjectiveTimeSeconds = ptr.To(0)
	schedule1to0.RecoveryPointType = dpCommon.RECOVERYPOINTTYPE_CRASH_CONSISTENT.Ref()

	ppInput := &dpModels.ProtectionPolicy{
		Name:        ptr.To(ppName),
		Description: ptr.To(fmt.Sprintf("CAPX vHA domain protection policy for %s", rctx.VHADomain.Name)),
		CategoryIds: categoryExtIds,
		ReplicationLocations: []dpModels.ReplicationLocation{
			*loc0,
			*loc1,
		},
		ReplicationConfigurations: []dpModels.ReplicationConfiguration{
			{
				SourceLocationLabel: ptr.To("loc-0"),
				RemoteLocationLabel: ptr.To("loc-1"),
				Schedule:            schedule0to1,
			},
			{
				SourceLocationLabel: ptr.To("loc-1"),
				RemoteLocationLabel: ptr.To("loc-0"),
				Schedule:            schedule1to0,
			},
		},
	}

	resp, err := rctx.ConvergedClient.DataPolicies.ProtectionPolicies.Create(rctx.Context, ppInput)
	if err != nil {
		return nil, fmt.Errorf("failed to create protection policy %s: %w", ppName, err)
	}
	if resp == nil || resp.ExtId == nil {
		return nil, fmt.Errorf("protection policy %s created but response missing ExtId", ppName)
	}

	log.Info("Created protection policy", "name", ppName, "uuid", *resp.ExtId)
	return &infrav1.NutanixResourceIdentifier{
		Type: infrav1.NutanixIdentifierUUID,
		UUID: resp.ExtId,
	}, nil
}

// getOrCreateVHADomainRecoveryPlan creates or retrieves the recovery plan whose
// primary location is the prism element at primaryIndex (the other PE is the
// recovery location). The recovery plan is created via the v3 API with the full
// network mapping / witness / availability-zone configuration. The returned
// identifier is resolved to a UUID.
func (r *NutanixVirtualHADomainReconciler) getOrCreateVHADomainRecoveryPlan(
	rctx *nctx.VHADomainContext,
	group string,
	peUUIDs []string,
	peNames []string,
	subnetNames []string,
	primaryIndex int,
	azURL string,
) (*infrav1.NutanixResourceIdentifier, error) {
	log := ctrl.LoggerFrom(rctx.Context)

	if len(peUUIDs) != 2 || len(peNames) != 2 {
		return nil, fmt.Errorf("expected exactly 2 prism elements for the metro to create recovery plan, got %d", len(peUUIDs))
	}

	rpName := vhaRecoveryPlanName(rctx.VHADomain.Name, group, primaryIndex)

	existing, err := findRecoveryPlanByName(rctx, rpName)
	if err != nil {
		return nil, err
	}
	if existing != nil && existing.Metadata != nil && existing.Metadata.UUID != "" {
		log.Info("Recovery plan already exists", "name", rpName, "uuid", existing.Metadata.UUID)
		return &infrav1.NutanixResourceIdentifier{
			Type: infrav1.NutanixIdentifierUUID,
			UUID: ptr.To(existing.Metadata.UUID),
		}, nil
	}

	categoryKey := VHADomainDefaultCategoryKey
	categoryVal := vhaCategoryValue(rctx.VHADomain.Name, group, primaryIndex)

	networkMappingAZList := make(
		[]*v3models.RecoveryPlanResourcesParametersNetworkMappingListItems0AvailabilityZoneNetworkMappingListItems0,
		0, len(peUUIDs),
	)
	for i := range peUUIDs {
		subnetName := ""
		if i < len(subnetNames) {
			subnetName = subnetNames[i]
		}
		networkMappingAZList = append(networkMappingAZList,
			&v3models.RecoveryPlanResourcesParametersNetworkMappingListItems0AvailabilityZoneNetworkMappingListItems0{
				AvailabilityZoneURL: ptr.To(azURL),
				ClusterReferenceList: []*v3models.ClusterReference{
					{Kind: "cluster", Name: peNames[i], UUID: ptr.To(peUUIDs[i])},
				},
				RecoveryNetwork: &v3models.RecoveryPlanNetwork{
					Name:       subnetName,
					SubnetList: []*v3models.RecoveryPlanSubnetConfig{},
				},
				RecoveryIPAssignmentList: []*v3models.RecoveryPlanVMIPAssignment{},
				TestIPAssignmentList:     []*v3models.RecoveryPlanVMIPAssignment{},
			},
		)
	}

	rpInput := &v3models.RecoveryPlanIntentInput{
		APIVersion: "3.1",
		Metadata: &v3models.RecoveryPlanMetadata{
			Kind: "recovery_plan",
		},
		Spec: &v3models.RecoveryPlan{
			Name:        ptr.To(rpName),
			Description: fmt.Sprintf("CAPX vHA domain recovery plan for %s movement group %q (primary: index %d)", rctx.VHADomain.Name, group, primaryIndex),
			Resources: &v3models.RecoveryPlanResources{
				StageList: []*v3models.RecoveryPlanStage{
					{
						StageWork: &v3models.RecoveryPlanStageStageWork{
							RecoverEntities: &v3models.RecoverEntities{
								EntityInfoList: []*v3models.RecoverEntitiesEntityInfoListItems0{
									{
										Categories: map[string]string{
											categoryKey: categoryVal,
										},
										ScriptList:                []*v3models.RecoveryPlanScriptConfig{},
										VolumeGroupAttachmentList: []*v3models.RecoverEntitiesEntityInfoListItems0VolumeGroupAttachmentListItems0{},
									},
								},
							},
						},
						DelayTimeSecs: ptr.To(int64(0)),
					},
				},
				VolumeGroupRecoveryInfoList: []*v3models.RecoveryPlanVolumeGroupRecoveryInfo{
					{
						CategoryFilter: &v3models.CategoryFilter{
							Params: map[string][]string{
								categoryKey: {categoryVal},
							},
							Type:     ptr.To("CATEGORIES_MATCH_ANY"),
							KindList: []string{},
						},
						VolumeGroupConfigInfoList: []*v3models.RecoveryPlanVolumeGroupRecoveryInfoVolumeGroupConfigInfoListItems0{},
					},
				},
				Parameters: &v3models.RecoveryPlanResourcesParameters{
					PrimaryLocationIndex:     int64(primaryIndex),
					DataServiceIPMappingList: []*v3models.RecoveryPlanResourcesParametersDataServiceIPMappingListItems0{},
					FloatingIPAssignmentList: []*v3models.RecoveryPlanResourcesParametersFloatingIPAssignmentListItems0{},
					NetworkMappingList: []*v3models.RecoveryPlanResourcesParametersNetworkMappingListItems0{
						{
							AvailabilityZoneNetworkMappingList: networkMappingAZList,
						},
					},
					WitnessConfigurationList: []*v3models.WitnessConfiguration{
						{
							WitnessAddress:             azURL,
							WitnessFailoverTimeoutSecs: 30,
						},
					},
					AvailabilityZoneList: []*v3models.AvailabilityZoneInformation{
						{
							AvailabilityZoneURL: ptr.To(azURL),
							ClusterReferenceList: []*v3models.ClusterReference{
								{Kind: "cluster", Name: peNames[0], UUID: ptr.To(peUUIDs[0])},
							},
						},
						{
							AvailabilityZoneURL: ptr.To(azURL),
							ClusterReferenceList: []*v3models.ClusterReference{
								{Kind: "cluster", Name: peNames[1], UUID: ptr.To(peUUIDs[1])},
							},
						},
					},
				},
			},
		},
	}

	resp, err := rctx.NutanixClient.V3.CreateRecoveryPlan(rctx.Context, rpInput)
	if err != nil {
		return nil, fmt.Errorf("failed to create recovery plan %s: %w", rpName, err)
	}
	if resp == nil || resp.Metadata == nil || resp.Metadata.UUID == "" {
		return nil, fmt.Errorf("recovery plan %s created but response missing UUID", rpName)
	}

	rpUUID := resp.Metadata.UUID
	log.Info("Created recovery plan, waiting for task to complete", "name", rpName, "uuid", rpUUID)

	if resp.Status == nil || resp.Status.ExecutionContext == nil || resp.Status.ExecutionContext.TaskUUID == nil {
		return nil, fmt.Errorf("recovery plan %s created but response missing task UUID", rpName)
	}

	taskUUID, ok := resp.Status.ExecutionContext.TaskUUID.(string)
	if !ok {
		return nil, fmt.Errorf("recovery plan %s: task UUID is not a string", rpName)
	}
	log.Info("Waiting for recovery plan creation task", "name", rpName, "taskUUID", taskUUID)
	if err := nutanixclient.WaitForTaskToSucceed(rctx.Context, rctx.NutanixClient, taskUUID); err != nil {
		return nil, fmt.Errorf("recovery plan %s creation task failed: %w", rpName, err)
	}

	log.Info("Recovery plan created successfully", "name", rpName, "uuid", rpUUID)
	return &infrav1.NutanixResourceIdentifier{
		Type: infrav1.NutanixIdentifierUUID,
		UUID: ptr.To(rpUUID),
	}, nil
}

// deleteVHADomainPCResources cleans up all PC resources referenced by the vHA
// domain. Deletion order: recovery plans -> protection policy -> categories.
func (r *NutanixVirtualHADomainReconciler) deleteVHADomainPCResources(rctx *nctx.VHADomainContext) error {
	log := ctrl.LoggerFrom(rctx.Context)

	if err := r.deleteVHADomainRecoveryPlans(rctx); err != nil {
		return err
	}
	if err := r.deleteVHADomainProtectionPolicy(rctx); err != nil {
		return err
	}
	if err := r.deleteVHADomainCategories(rctx); err != nil {
		return err
	}

	log.Info("Successfully cleaned up vHADomain PC resources")
	return nil
}

// deleteVHADomainRecoveryPlans deletes every recovery plan referenced by the vHA
// domain's movement groups, tolerating plans that are already gone.
func (r *NutanixVirtualHADomainReconciler) deleteVHADomainRecoveryPlans(rctx *nctx.VHADomainContext) error {
	log := ctrl.LoggerFrom(rctx.Context)

	for _, movementGroup := range rctx.VHADomain.Spec.MovementGroups {
		groupName := movementGroup.Name
		for i := range movementGroup.CategoryRecoveryPlans {
			rp := movementGroup.CategoryRecoveryPlans[i].RecoveryPlan
			rpUUID := rp.String()
			if rpUUID == "" {
				continue
			}
			log.Info("Deleting recovery plan", "movementGroup", groupName, "identifier", rp.DisplayString())
			resp, err := rctx.NutanixClient.V3.DeleteRecoveryPlan(rctx.Context, rpUUID)
			if err != nil {
				if !strings.Contains(fmt.Sprint(err), "ENTITY_NOT_FOUND") {
					return fmt.Errorf("failed to delete recovery plan %s: %w", rp.DisplayString(), err)
				}
				log.V(1).Info("Recovery plan already deleted", "identifier", rp.DisplayString())
				continue
			}

			// Wait for the deletion task to complete before proceeding. Recovery plan
			// deletion is asynchronous and, until it finishes, the plan still references
			// the vHA categories. Deleting those categories next (deleteVHADomainCategories)
			// would otherwise race the in-flight deletion and fail with the category still
			// in use, which is then silently swallowed and leaks the category in Prism Central.
			if resp != nil && resp.Status != nil && resp.Status.ExecutionContext != nil && resp.Status.ExecutionContext.TaskUUID != nil {
				taskUUID, ok := resp.Status.ExecutionContext.TaskUUID.(string)
				if ok && taskUUID != "" {
					log.V(1).Info("Waiting for recovery plan deletion task", "identifier", rp.DisplayString(), "taskUUID", taskUUID)
					if err := nutanixclient.WaitForTaskToSucceed(rctx.Context, rctx.NutanixClient, taskUUID); err != nil {
						return fmt.Errorf("recovery plan %s deletion task failed: %w", rp.DisplayString(), err)
					}
				}
			}
		}
	}
	return nil
}

// deleteVHADomainProtectionPolicy deletes the vHA domain's protection policy,
// tolerating a policy that is already gone.
func (r *NutanixVirtualHADomainReconciler) deleteVHADomainProtectionPolicy(rctx *nctx.VHADomainContext) error {
	log := ctrl.LoggerFrom(rctx.Context)

	if rctx.VHADomain.Spec.ProtectionPolicy == nil {
		return nil
	}
	pp := rctx.VHADomain.Spec.ProtectionPolicy
	ppUUID := pp.String()
	if ppUUID == "" {
		return nil
	}
	log.Info("Deleting protection policy", "identifier", pp.DisplayString())
	if err := rctx.ConvergedClient.DataPolicies.ProtectionPolicies.Delete(rctx.Context, ppUUID); err != nil {
		if !converged.IsNotFound(err) {
			return fmt.Errorf("failed to delete protection policy %s: %w", pp.DisplayString(), err)
		}
		log.V(1).Info("Protection policy already deleted", "identifier", pp.DisplayString())
	}
	return nil
}

// deleteVHADomainCategories deletes the de-duplicated set of categories
// referenced by the vHA domain's movement groups.
func (r *NutanixVirtualHADomainReconciler) deleteVHADomainCategories(rctx *nctx.VHADomainContext) error {
	log := ctrl.LoggerFrom(rctx.Context)

	categoryIdentifiers := make([]*infrav1.NutanixCategoryIdentifier, 0)
	seen := make(map[string]struct{})
	for _, movementGroup := range rctx.VHADomain.Spec.MovementGroups {
		for i := range movementGroup.CategoryRecoveryPlans {
			category := movementGroup.CategoryRecoveryPlans[i].Category
			key := category.Key + "/" + category.Value
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			categoryIdentifiers = append(categoryIdentifiers, &infrav1.NutanixCategoryIdentifier{
				Key:   category.Key,
				Value: category.Value,
			})
		}
	}
	if len(categoryIdentifiers) > 0 {
		log.Info("Deleting vHA domain categories", "count", len(categoryIdentifiers))
		if err := deleteCategoryKeyValues(rctx.Context, rctx.ConvergedClient, categoryIdentifiers); err != nil {
			return fmt.Errorf("failed to delete vHA domain categories: %w", err)
		}
	}
	return nil
}

func getDomainManagerExtId(rctx *nctx.VHADomainContext) (string, error) {
	domainManagers, err := rctx.ConvergedClient.DomainManager.List(rctx.Context)
	if err != nil {
		return "", fmt.Errorf("failed to list domain managers: %w", err)
	}
	if len(domainManagers) == 0 {
		return "", fmt.Errorf("no domain managers found")
	}
	if domainManagers[0].ExtId == nil {
		return "", fmt.Errorf("domain manager has no ExtId")
	}
	return *domainManagers[0].ExtId, nil
}

func findProtectionPolicyByName(rctx *nctx.VHADomainContext, name string) (*dpModels.ProtectionPolicy, error) {
	policies, err := rctx.ConvergedClient.DataPolicies.ProtectionPolicies.List(
		rctx.Context, converged.WithFilter(fmt.Sprintf("name eq '%s'", name)),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list protection policies: %w", err)
	}

	for i := range policies {
		if policies[i].Name != nil && *policies[i].Name == name {
			return &policies[i], nil
		}
	}
	return nil, nil
}

func findRecoveryPlanByName(rctx *nctx.VHADomainContext, name string) (*v3models.RecoveryPlanIntentResource, error) {
	resp, err := rctx.NutanixClient.V3.ListAllRecoveryPlans(rctx.Context, fmt.Sprintf("name==%s", name))
	if err != nil {
		return nil, fmt.Errorf("failed to list recovery plans: %w", err)
	}

	for _, rp := range resp.Entities {
		if rp.Spec != nil && rp.Spec.Name != nil && *rp.Spec.Name == name {
			return rp, nil
		}
	}
	return nil, nil
}
