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
	"strings"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	nctx "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/pkg/context"
	prismclientv3 "github.com/nutanix-cloud-native/prism-go-client/v3"
	"github.com/nutanix-cloud-native/prism-go-client/v3/models"
	"k8s.io/apimachinery/pkg/runtime"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	coreinformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/utils/ptr"
	capiutil "sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	ctrlutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// NutanixVirtualHADomainReconciler reconciles an NutanixVirtualHADomain object
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

	// Add finalizer first if not set yet
	if !ctrlutil.ContainsFinalizer(vHADomain, infrav1.NutanixVirtualHADomainFinalizer) {
		if ctrlutil.AddFinalizer(vHADomain, infrav1.NutanixVirtualHADomainFinalizer) {
			// Add finalizer first avoid the race condition between init and delete.
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
	err = r.Get(ctx, nclKey, ntnxCluster)
	if err != nil {
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

	// Handle deletion
	if !vHADomain.DeletionTimestamp.IsZero() {
		if err = r.reconcileDelete(rctx); err != nil {
			log.Error(err, "failed at deletion reconciling")
		}
		return ctrl.Result{}, err
	}

	err = r.reconcileNormal(rctx)
	if err != nil {
		log.Error(err, "failed at normal reconciling, restore to original spec.")
		vHADomain.Spec = vHADomainOrig.Spec
	}
	return ctrl.Result{}, err
}

func (r *NutanixVirtualHADomainReconciler) reconcileDelete(rctx *nctx.VHADomainContext) error {
	log := ctrl.LoggerFrom(rctx.Context)
	log.Info("Handling NutanixVirtualHADomain deletion")

	if rctx.NutanixCluster.DeletionTimestamp.IsZero() {
		return fmt.Errorf("cannot delete the vHADomain because its corresponding NutanixCluster %s is not in deletion", rctx.NutanixCluster.Name)
	}

	if err := r.deleteVHADomainPCResources(rctx); err != nil {
		return fmt.Errorf("failed to clean up vHADomain PC resources: %w", err)
	}

	// Remove the finalizer from the NutanixVirtualHADomain object
	ctrlutil.RemoveFinalizer(rctx.VHADomain, infrav1.NutanixVirtualHADomainFinalizer)
	log.Info("Removed finalizer %q from the vHADomain object", infrav1.NutanixVirtualHADomainFinalizer)

	return nil
}

func (r *NutanixVirtualHADomainReconciler) reconcileNormal(rctx *nctx.VHADomainContext) error {
	log := ctrl.LoggerFrom(rctx.Context)
	log.Info("Handling NutanixVirtualHADomain reconciling")

	namespace := rctx.VHADomain.Namespace

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

	if err := r.ensureVHADomainPCResources(rctx, metroObj, failureDomains); err != nil {
		rctx.VHADomain.Status.Ready = false
		return err
	}

	rctx.VHADomain.Status.Ready = len(rctx.VHADomain.Spec.Categories) == 2 &&
		rctx.VHADomain.Spec.ProtectionPolicy != nil &&
		len(rctx.VHADomain.Spec.RecoveryPlans) == 2

	return nil
}

func vhaCategoryKey(vHADomainName string) string {
	return fmt.Sprintf("capx-vha-%s", vHADomainName)
}

func vhaProtectionPolicyName(vHADomainName string) string {
	return fmt.Sprintf("capx-vha-%s", vHADomainName)
}

func vhaRecoveryPlanName(vHADomainName string, index int) string {
	return fmt.Sprintf("capx-vha-%s-rp-%d", vHADomainName, index)
}

// ensureVHADomainPCResources creates or validates the PC resources (categories,
// protection policy, recovery plans) for the vHA domain and stores their
// identifiers in the spec. All spec fields are set together to maintain the
// invariant that they are either all set or all unset.
func (r *NutanixVirtualHADomainReconciler) ensureVHADomainPCResources(
	rctx *nctx.VHADomainContext,
	metroObj *infrav1.NutanixMetro,
	failureDomains []*infrav1.NutanixFailureDomain,
) error {
	log := ctrl.LoggerFrom(rctx.Context)

	if len(rctx.VHADomain.Spec.Categories) == 2 &&
		rctx.VHADomain.Spec.ProtectionPolicy != nil &&
		len(rctx.VHADomain.Spec.RecoveryPlans) == 2 {
		log.Info("vHADomain PC resources already exist in spec, skipping creation")
		return nil
	}

	peUUIDs := make([]string, 0, len(failureDomains))
	for _, fd := range failureDomains {
		peUUID, err := GetPEUUID(rctx.Context, rctx.ConvergedClient,
			fd.Spec.PrismElementCluster.Name, fd.Spec.PrismElementCluster.UUID)
		if err != nil {
			return fmt.Errorf("failed to get PE UUID for failure domain %s: %w", fd.Name, err)
		}
		peUUIDs = append(peUUIDs, peUUID)
	}

	categories, err := r.getOrCreateVHADomainCategories(rctx, metroObj)
	if err != nil {
		return err
	}

	protectionPolicy, err := r.getOrCreateVHADomainProtectionPolicy(rctx, categories, peUUIDs)
	if err != nil {
		return err
	}

	recoveryPlans, err := r.getOrCreateVHADomainRecoveryPlans(rctx, categories, peUUIDs)
	if err != nil {
		return err
	}

	rctx.VHADomain.Spec.Categories = categories
	rctx.VHADomain.Spec.ProtectionPolicy = protectionPolicy
	rctx.VHADomain.Spec.RecoveryPlans = recoveryPlans

	log.Info("Successfully ensured vHADomain PC resources",
		"categories", categories,
		"protectionPolicy", protectionPolicy.DisplayString(),
		"recoveryPlans", len(recoveryPlans))

	return nil
}

// getOrCreateVHADomainCategories creates or retrieves the two categories
// (one per failure domain) for the vHA domain.
func (r *NutanixVirtualHADomainReconciler) getOrCreateVHADomainCategories(
	rctx *nctx.VHADomainContext,
	metroObj *infrav1.NutanixMetro,
) ([]infrav1.NutanixCategoryIdentifier, error) {
	log := ctrl.LoggerFrom(rctx.Context)
	categoryKey := vhaCategoryKey(rctx.VHADomain.Name)

	categories := make([]infrav1.NutanixCategoryIdentifier, 0, 2)
	for _, fdRef := range metroObj.Spec.FailureDomains {
		ci := &infrav1.NutanixCategoryIdentifier{
			Key:   categoryKey,
			Value: fdRef.Name,
		}
		_, err := getOrCreateCategory(rctx.Context, rctx.ConvergedClient, ci)
		if err != nil {
			return nil, fmt.Errorf("failed to get or create category for failure domain %s: %w", fdRef.Name, err)
		}
		categories = append(categories, *ci)
		log.V(1).Info("Ensured vHA category", "key", ci.Key, "value", ci.Value)
	}

	return categories, nil
}

// getOrCreateVHADomainProtectionPolicy creates or retrieves the protection
// policy for the vHA domain. The policy protects VMs tagged with the vHA
// categories using synchronous replication (RPO=0) between the two PEs.
func (r *NutanixVirtualHADomainReconciler) getOrCreateVHADomainProtectionPolicy(
	rctx *nctx.VHADomainContext,
	categories []infrav1.NutanixCategoryIdentifier,
	peUUIDs []string,
) (*infrav1.NutanixResourceIdentifier, error) {
	log := ctrl.LoggerFrom(rctx.Context)
	ppName := vhaProtectionPolicyName(rctx.VHADomain.Name)

	existing, err := findProtectionRuleByName(rctx, ppName)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		log.Info("Protection policy already exists", "name", ppName, "uuid", *existing.Metadata.UUID)
		return &infrav1.NutanixResourceIdentifier{
			Type: infrav1.NutanixIdentifierUUID,
			UUID: existing.Metadata.UUID,
		}, nil
	}

	categoryValues := make([]string, 0, len(categories))
	for _, c := range categories {
		categoryValues = append(categoryValues, c.Value)
	}

	ppInput := &prismclientv3.ProtectionRuleInput{
		APIVersion: "3.1",
		Metadata: &prismclientv3.Metadata{
			Kind: ptr.To("protection_rule"),
		},
		Spec: &prismclientv3.ProtectionRuleSpec{
			Name:        ppName,
			Description: fmt.Sprintf("CAPX vHA domain protection policy for %s", rctx.VHADomain.Name),
			Resources: &prismclientv3.ProtectionRuleResources{
				CategoryFilter: &prismclientv3.CategoryFilter{
					Type: ptr.To("CATEGORIES_MATCH_ANY"),
					Params: map[string][]string{
						categories[0].Key: categoryValues,
					},
					KindList: []*string{ptr.To("vm")},
				},
				OrderedAvailabilityZoneList: []*prismclientv3.OrderedAvailabilityZoneList{
					{ClusterUUID: peUUIDs[0]},
					{ClusterUUID: peUUIDs[1]},
				},
				AvailabilityZoneConnectivityList: []*prismclientv3.AvailabilityZoneConnectivityList{
					{
						SourceAvailabilityZoneIndex:      ptr.To(int64(0)),
						DestinationAvailabilityZoneIndex: ptr.To(int64(1)),
						SnapshotScheduleList: []*prismclientv3.SnapshotScheduleList{
							{
								RecoveryPointObjectiveSecs: ptr.To(int64(0)),
								SnapshotType:               "CRASH_CONSISTENT",
								LocalSnapshotRetentionPolicy: &prismclientv3.SnapshotRetentionPolicy{
									NumSnapshots: ptr.To(int64(1)),
								},
							},
						},
					},
					{
						SourceAvailabilityZoneIndex:      ptr.To(int64(1)),
						DestinationAvailabilityZoneIndex: ptr.To(int64(0)),
						SnapshotScheduleList: []*prismclientv3.SnapshotScheduleList{
							{
								RecoveryPointObjectiveSecs: ptr.To(int64(0)),
								SnapshotType:               "CRASH_CONSISTENT",
								LocalSnapshotRetentionPolicy: &prismclientv3.SnapshotRetentionPolicy{
									NumSnapshots: ptr.To(int64(1)),
								},
							},
						},
					},
				},
			},
		},
	}

	resp, err := rctx.NutanixClient.V3.CreateProtectionRule(rctx.Context, ppInput)
	if err != nil {
		return nil, fmt.Errorf("failed to create protection policy %s: %w", ppName, err)
	}
	if resp == nil || resp.Metadata == nil || resp.Metadata.UUID == nil {
		return nil, fmt.Errorf("protection policy %s created but response missing UUID", ppName)
	}

	log.Info("Created protection policy", "name", ppName, "uuid", *resp.Metadata.UUID)
	return &infrav1.NutanixResourceIdentifier{
		Type: infrav1.NutanixIdentifierUUID,
		UUID: resp.Metadata.UUID,
	}, nil
}

// getOrCreateVHADomainRecoveryPlans creates or retrieves the two recovery
// plans for the vHA domain. Each plan covers failover in one direction
// between the two failure domains.
func (r *NutanixVirtualHADomainReconciler) getOrCreateVHADomainRecoveryPlans(
	rctx *nctx.VHADomainContext,
	categories []infrav1.NutanixCategoryIdentifier,
	peUUIDs []string,
) ([]infrav1.NutanixResourceIdentifier, error) {
	recoveryPlans := make([]infrav1.NutanixResourceIdentifier, 0, 2)

	for i := 0; i < 2; i++ {
		rp, err := r.getOrCreateVHADomainRecoveryPlan(rctx, categories, peUUIDs, i)
		if err != nil {
			return nil, err
		}
		recoveryPlans = append(recoveryPlans, *rp)
	}

	return recoveryPlans, nil
}

func (r *NutanixVirtualHADomainReconciler) getOrCreateVHADomainRecoveryPlan(
	rctx *nctx.VHADomainContext,
	categories []infrav1.NutanixCategoryIdentifier,
	peUUIDs []string,
	primaryIndex int,
) (*infrav1.NutanixResourceIdentifier, error) {
	log := ctrl.LoggerFrom(rctx.Context)
	rpName := vhaRecoveryPlanName(rctx.VHADomain.Name, primaryIndex)

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

	rpInput := &models.RecoveryPlanIntentInput{
		APIVersion: "3.1",
		Metadata: &models.RecoveryPlanMetadata{
			Kind: "recovery_plan",
		},
		Spec: &models.RecoveryPlan{
			Name:        ptr.To(rpName),
			Description: fmt.Sprintf("CAPX vHA domain recovery plan for %s (primary: index %d)", rctx.VHADomain.Name, primaryIndex),
			Resources: &models.RecoveryPlanResources{
				StageList: []*models.RecoveryPlanStage{
					{
						StageWork: &models.RecoveryPlanStageStageWork{
							RecoverEntities: &models.RecoverEntities{
								EntityInfoList: []*models.RecoverEntitiesEntityInfoListItems0{
									{
										Categories: map[string]string{
											categories[primaryIndex].Key: categories[primaryIndex].Value,
										},
									},
								},
							},
						},
						DelayTimeSecs: ptr.To(int64(0)),
					},
				},
				Parameters: &models.RecoveryPlanResourcesParameters{
					PrimaryLocationIndex: int64(primaryIndex),
					AvailabilityZoneList: []*models.AvailabilityZoneInformation{
						{
							AvailabilityZoneURL: ptr.To(""),
							ClusterReferenceList: []*models.ClusterReference{
								{Kind: "cluster", UUID: ptr.To(peUUIDs[0])},
							},
						},
						{
							AvailabilityZoneURL: ptr.To(""),
							ClusterReferenceList: []*models.ClusterReference{
								{Kind: "cluster", UUID: ptr.To(peUUIDs[1])},
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

	log.Info("Created recovery plan", "name", rpName, "uuid", resp.Metadata.UUID)
	return &infrav1.NutanixResourceIdentifier{
		Type: infrav1.NutanixIdentifierUUID,
		UUID: ptr.To(resp.Metadata.UUID),
	}, nil
}

// deleteVHADomainPCResources cleans up all PC resources created for the vHA domain.
// Deletion order: recovery plans -> protection policy -> categories.
func (r *NutanixVirtualHADomainReconciler) deleteVHADomainPCResources(rctx *nctx.VHADomainContext) error {
	log := ctrl.LoggerFrom(rctx.Context)

	for _, rp := range rctx.VHADomain.Spec.RecoveryPlans {
		rpUUID := rp.String()
		if rpUUID == "" {
			continue
		}
		log.Info("Deleting recovery plan", "identifier", rp.DisplayString())
		if _, err := rctx.NutanixClient.V3.DeleteRecoveryPlan(rctx.Context, rpUUID); err != nil {
			if !strings.Contains(fmt.Sprint(err), "ENTITY_NOT_FOUND") {
				return fmt.Errorf("failed to delete recovery plan %s: %w", rp.DisplayString(), err)
			}
			log.V(1).Info("Recovery plan already deleted", "identifier", rp.DisplayString())
		}
	}

	if rctx.VHADomain.Spec.ProtectionPolicy != nil {
		ppUUID := rctx.VHADomain.Spec.ProtectionPolicy.String()
		if ppUUID != "" {
			log.Info("Deleting protection policy", "identifier", rctx.VHADomain.Spec.ProtectionPolicy.DisplayString())
			if _, err := rctx.NutanixClient.V3.DeleteProtectionRule(rctx.Context, ppUUID); err != nil {
				if !strings.Contains(fmt.Sprint(err), "ENTITY_NOT_FOUND") {
					return fmt.Errorf("failed to delete protection policy %s: %w",
						rctx.VHADomain.Spec.ProtectionPolicy.DisplayString(), err)
				}
				log.V(1).Info("Protection policy already deleted",
					"identifier", rctx.VHADomain.Spec.ProtectionPolicy.DisplayString())
			}
		}
	}

	if len(rctx.VHADomain.Spec.Categories) > 0 {
		categoryIdentifiers := make([]*infrav1.NutanixCategoryIdentifier, 0, len(rctx.VHADomain.Spec.Categories))
		for i := range rctx.VHADomain.Spec.Categories {
			categoryIdentifiers = append(categoryIdentifiers, &rctx.VHADomain.Spec.Categories[i])
		}
		log.Info("Deleting vHA domain categories", "count", len(categoryIdentifiers))
		if err := deleteCategoryKeyValues(rctx.Context, rctx.ConvergedClient, categoryIdentifiers); err != nil {
			return fmt.Errorf("failed to delete vHA domain categories: %w", err)
		}
	}

	log.Info("Successfully cleaned up vHADomain PC resources")
	return nil
}

func findProtectionRuleByName(rctx *nctx.VHADomainContext, name string) (*prismclientv3.ProtectionRuleResponse, error) {
	resp, err := rctx.NutanixClient.V3.ListAllProtectionRules(rctx.Context, fmt.Sprintf("name==%s", name))
	if err != nil {
		return nil, fmt.Errorf("failed to list protection rules: %w", err)
	}

	for _, pr := range resp.Entities {
		if pr.Spec != nil && pr.Spec.Name == name {
			return pr, nil
		}
	}
	return nil, nil
}

func findRecoveryPlanByName(rctx *nctx.VHADomainContext, name string) (*models.RecoveryPlanIntentResource, error) {
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
