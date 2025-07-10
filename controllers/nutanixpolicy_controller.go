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
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	coreinformers "k8s.io/client-go/informers/core/v1"
	workqueue "k8s.io/client-go/util/workqueue"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/google/uuid"
	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	"github.com/nutanix-cloud-native/prism-go-client/facade"
	prismclientv3 "github.com/nutanix-cloud-native/prism-go-client/v3"
	"sigs.k8s.io/controller-runtime/pkg/log"

	nctx "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/pkg/context"

	prismModels "github.com/nutanix/ntnx-api-golang-clients/prism-go-client/v4/models/prism/v4/config"
	policiesv4 "github.com/nutanix/ntnx-api-golang-clients/vmm-go-client/v4/models/vmm/v4/ahv/policies"
)

const (
	// NutanixVMAntiAffinityPolicyFinalizerName is the name of the finalizer for NutanixVMAntiAffinityPolicy
	NutanixVMAntiAffinityPolicyFinalizerName = "nutanixvmaffinitypolicy.infrastructure.cluster.x-k8s.io/finalizer"

	// NutanixVMAntiAffinityPolicyFinalizerGroup is the group of the finalizer for NutanixVMAntiAffinityPolicy
	NutanixVMAntiAffinityPolicyFinalizerGroup = "infrastructure.cluster.x-k8s.io"

	// ClusterNameAnnotation is the annotation used to identify the cluster name
	ClusterNameAnnotation = "infrastructure.cluster.x-k8s.io/cluster-name"

	// CleanupPolicyAnnotation is the annotation used to indicate that the manually created policy should be cleaned up
	CleanupPolicyAnnotationName = "infrastructure.cluster.x-k8s.io/cleanup-policy"

	// CategoryCleanupAnnotationPrefix is the prefix for the annotation used to indicate that the category should be cleaned up
	CategoryCleanupAnnotationPrefix = "infrastructure.cluster.x-k8s.io/category-cleanup-"

	ClusterAnnotationReady   capiv1.ConditionType = "ClusterAnnotationReady"
	ClusterIdentityReady     capiv1.ConditionType = "ClusterIdentityReady"
	PcClientReady            capiv1.ConditionType = "PcClientReady"
	PcVersionCompatibility   capiv1.ConditionType = "PcVersionCompatibility"
	AnnotationsReconciled    capiv1.ConditionType = "AnnotationsReconciled"
	FinalizerAdded           capiv1.ConditionType = "FinalizerAdded"
	CategoriesReady          capiv1.ConditionType = "CategoriesReady"
	PolicyAlreadyPresentInPC capiv1.ConditionType = "PolicyAlreadyPresentInPC"
	PolicyCreated            capiv1.ConditionType = "PolicyCreated"
	PolicyUpdated            capiv1.ConditionType = "PolicyUpdated"
	PolicyDeleted            capiv1.ConditionType = "PolicyDeleted"
	CategoriesCleanedUp      capiv1.ConditionType = "CategoriesCleanedUp"
	FinalizerRemoved         capiv1.ConditionType = "FinalizerRemoved"
)

type nutanixPreflightReconcileFunction func(ectx *nctx.ExtendedContext, scope *nctx.VMAntiAffinityPolicyScope) (*nctx.VMAntiAffinityPolicyScope, error)
type nutanixPolicyReconcileFunction[T any] func(pctx *nctx.VMAntiAffinityPolicyContext[T]) (ctrl.Result, error)

type nutanixPreflightReconcilerUnitOfWork struct {
	// Reconciler is the NutanixPolicyReconciler instance.
	Reconciler *NutanixPolicyReconciler

	// PreflightReconcileFunc is the function to be executed for preflight reconciliation.
	PreflightReconcileFunc nutanixPreflightReconcileFunction

	// StepCondition is the condition to be set after the step is executed.
	StepCondition capiv1.ConditionType
}

type nutanixPolicyReconcilerUnitOfWork[T any] struct {
	// Reconciler is the NutanixPolicyReconciler instance.
	Reconciler *NutanixPolicyReconciler

	// ReconcileFunc is the function to be executed for reconciliation.
	ReconcileFunc nutanixPolicyReconcileFunction[T]

	// StepCondition is the condition to be set after the step is executed.
	StepCondition capiv1.ConditionType
}

type UnitOfWork[inT any, outT any] interface {
	Run(inT) outT
}

func (uow *nutanixPreflightReconcilerUnitOfWork) Run(ectx *nctx.ExtendedContext, scope *nctx.VMAntiAffinityPolicyScope) (*nctx.VMAntiAffinityPolicyScope, error) {
	return PreflightReconcileFuncRun(
		ectx,
		scope,
		uow.PreflightReconcileFunc,
		uow.StepCondition,
	)
}

func (uow *nutanixPolicyReconcilerUnitOfWork[T]) Run(pctx *nctx.VMAntiAffinityPolicyContext[T]) (ctrl.Result, error) {
	return ReconcileFuncRun[T](
		pctx,
		uow.ReconcileFunc,
		uow.StepCondition,
	)
}

func ReconcileFuncRun[T any](
	pctx *nctx.VMAntiAffinityPolicyContext[T],
	reconcileFunc nutanixPolicyReconcileFunction[T],
	stepCondition capiv1.ConditionType,
) (ctrl.Result, error) {
	log := log.FromContext(pctx.Context)
	log.Info("Running reconcile function", "StepCondition", stepCondition)

	// Run the reconcile function
	result, err := reconcileFunc(pctx)
	if err != nil {
		log.Error(err, "Failed to run reconcile function")
		return ctrl.Result{}, err
	}

	// Mark the condition as true if the step is successful
	conditions.MarkTrue(pctx.NutanixVMAntiAffinityPolicy, stepCondition)

	return result, nil
}

func PreflightReconcileFuncRun(
	ectx *nctx.ExtendedContext,
	scope *nctx.VMAntiAffinityPolicyScope,
	preflightReconcileFunc nutanixPreflightReconcileFunction,
	stepCondition capiv1.ConditionType,
) (*nctx.VMAntiAffinityPolicyScope, error) {
	log := log.FromContext(ectx.Context)
	log.Info("Running preflight reconcile function", "StepCondition", stepCondition)

	// Run the preflight reconcile function
	newScope, err := preflightReconcileFunc(ectx, scope)
	if err != nil {
		log.Error(err, "Failed to run preflight reconcile function")

		// Mark the condition as false if the step fails
		conditions.MarkFalse(
			scope.NutanixVMAntiAffinityPolicy,
			stepCondition,
			fmt.Sprintf("%w", err),
			capiv1.ConditionSeverityError,
			"Failed to run preflight reconcile function: %s", err.Error(),
		)

		return nil, err
	}

	// Mark the condition as true if the step is successful
	conditions.MarkTrue(newScope.NutanixVMAntiAffinityPolicy, stepCondition)

	return newScope, nil
}

type NutanixPolicyReconciler struct {
	client.Client
	SecretInformer    coreinformers.SecretInformer
	ConfigMapInformer coreinformers.ConfigMapInformer
	Scheme            *runtime.Scheme
	controllerConfig  *ControllerConfig

	// Add any additional fields needed for the reconciler
	failedToFindPolicyUuid map[string]int
	mutex                  sync.Mutex
}

func NewNutanixPolicyReconciler(
	client client.Client,
	secretInformer coreinformers.SecretInformer,
	configMapInformer coreinformers.ConfigMapInformer,
	scheme *runtime.Scheme,
	copts ...ControllerConfigOpts,
) (*NutanixPolicyReconciler, error) {
	controllerConfig := &ControllerConfig{}
	for _, opt := range copts {
		if err := opt(controllerConfig); err != nil {
			return nil, fmt.Errorf("failed to apply controller config option: %w", err)
		}
	}

	return &NutanixPolicyReconciler{
		Client:            client,
		Scheme:            scheme,
		SecretInformer:    secretInformer,
		ConfigMapInformer: configMapInformer,
		controllerConfig:  controllerConfig,

		failedToFindPolicyUuid: make(map[string]int),
		mutex:                  sync.Mutex{},
	}, nil
}

func (r *NutanixPolicyReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	copts := controller.Options{
		MaxConcurrentReconciles: r.controllerConfig.MaxConcurrentReconciles,
		RateLimiter:             workqueue.DefaultTypedControllerRateLimiter[reconcile.Request](),
	}

	// Set index for NutanixVMAntiAffinityPolicy to allow mapping to NutanixCluster
	if err := mgr.GetFieldIndexer().IndexField(ctx, &infrav1.NutanixVMAntiAffinityPolicy{},
		fmt.Sprintf("metadata,annotation.%s", ClusterNameAnnotation),
		func(rawObj client.Object) []string {
			nutanixVMAntiAffinityPolicy, ok := rawObj.(*infrav1.NutanixVMAntiAffinityPolicy)
			if !ok {
				log.FromContext(ctx).Error(fmt.Errorf("expected NutanixVMAntiAffinityPolicy object, got %T", rawObj), "Failed to index NutanixVMAntiAffinityPolicy")
				return nil
			}
			// Return the cluster name from the annotation
			if clusterName, ok := nutanixVMAntiAffinityPolicy.Annotations[ClusterNameAnnotation]; ok {
				return []string{clusterName}
			}

			return nil
		}); err != nil {
		return fmt.Errorf("failed to index NutanixVMAntiAffinityPolicy: %w", err)
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&infrav1.NutanixVMAntiAffinityPolicy{}).
		Watches(&infrav1.NutanixCluster{},
			handler.EnqueueRequestsFromMapFunc(
				r.mapNutanixClusterToNutanixVMAntiAffinityPolicy(),
			),
		).
		WithOptions(copts).
		Complete(r)
}

// mapNutanixClusterToNutanixVMAntiAffinityPolicy maps NutanixCluster objects to NutanixVMAntiAffinityPolicy objects.
func (r *NutanixPolicyReconciler) mapNutanixClusterToNutanixVMAntiAffinityPolicy() handler.MapFunc {
	return func(ctx context.Context, o client.Object) []reconcile.Request {
		log := log.FromContext(ctx)
		log.Info("Mapping NutanixCluster to NutanixVMAntiAffinityPolicy")
		nutanixCluster, ok := o.(*infrav1.NutanixCluster)
		if !ok {
			log.Error(fmt.Errorf("expected NutanixCluster object, got %T", o), "Failed to map NutanixCluster to NutanixVMAntiAffinityPolicy")
			return nil
		}
		log.Info("NutanixCluster found", "Name", nutanixCluster.Name, "Namespace", nutanixCluster.Namespace)
		// Get all NutanixVMAntiAffinityPolicy objects that have the cluster annotation matching the NutanixCluster name
		nutanixVMAntiAffinityPolicyList := &infrav1.NutanixVMAntiAffinityPolicyList{}
		if err := r.List(ctx, nutanixVMAntiAffinityPolicyList, client.InNamespace(nutanixCluster.Namespace),
			client.MatchingFields{fmt.Sprintf("metadata,annotation.%s", ClusterNameAnnotation): nutanixCluster.Name}); err != nil {
			log.Error(err, "Failed to list NutanixVMAntiAffinityPolicy objects for NutanixCluster",
				"Name", nutanixCluster.Name, "Namespace", nutanixCluster.Namespace)
			return nil
		}
		log.Info("Found NutanixVMAntiAffinityPolicy objects for NutanixCluster",
			"Count", len(nutanixVMAntiAffinityPolicyList.Items), "Name", nutanixCluster.Name, "Namespace", nutanixCluster.Namespace)
		// Create a reconcile request for each NutanixVMAntiAffinityPolicy object found
		requests := make([]reconcile.Request, 0, len(nutanixVMAntiAffinityPolicyList.Items))
		for _, policy := range nutanixVMAntiAffinityPolicyList.Items {
			log.Info("Enqueuing NutanixVMAntiAffinityPolicy for reconciliation",
				"Name", policy.Name, "Namespace", policy.Namespace)
			// Create a reconcile request for the NutanixVMAntiAffinityPolicy
			requests = append(requests, reconcile.Request{
				NamespacedName: client.ObjectKey{
					Name:      policy.Name,
					Namespace: policy.Namespace,
				},
			})
		}
		log.Info("Mapped NutanixCluster to NutanixVMAntiAffinityPolicy",
			"Count", len(requests), "Name", nutanixCluster.Name, "Namespace", nutanixCluster.Namespace)
		return requests
	}
}

func (r *NutanixPolicyReconciler) preflightCheckClusterAnnotation(
	ectx *nctx.ExtendedContext,
	scope *nctx.VMAntiAffinityPolicyScope,
) (*nctx.VMAntiAffinityPolicyScope, error) {
	log := log.FromContext(ectx.Context)
	log.Info("Preflight check for cluster annotation")

	// Check if the NutanixVMAntiAffinityPolicy has the cluster annotation
	if _, ok := scope.NutanixVMAntiAffinityPolicy.Annotations[ClusterNameAnnotation]; !ok {
		log.Info("NutanixVMAntiAffinityPolicy does not have the cluster annotation, skipping reconciliation")

		return scope, fmt.Errorf("NutanixVMAntiAffinityPolicy does not have the cluster annotation")
	}

	return scope, nil
}

func (r *NutanixPolicyReconciler) preflightCheckClusterIdentity(
	ectx *nctx.ExtendedContext,
	scope *nctx.VMAntiAffinityPolicyScope,
) (*nctx.VMAntiAffinityPolicyScope, error) {
	log := log.FromContext(ectx.Context)
	log.Info("Preflight check for cluster identity")

	// Get NutanixCluster from the cluster annotation
	clusterName := scope.NutanixVMAntiAffinityPolicy.Annotations[ClusterNameAnnotation]
	nutanixCluster := &infrav1.NutanixCluster{}
	if err := r.Get(ectx.Context, client.ObjectKey{Namespace: scope.NutanixVMAntiAffinityPolicy.Namespace, Name: clusterName}, nutanixCluster); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("NutanixCluster not found, ignoring since object must be deleted")
			return scope, nil
		}
		// Error reading the object - requeue the request.
		log.Error(err, "Failed to get NutanixCluster")
		return scope, fmt.Errorf("failed to get NutanixCluster: %w", err)
	}

	scope.NutanixCluster = nutanixCluster
	return scope, nil
}

// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;update;delete
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nutanixvmantiaffinitypolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nutanixvmantiaffinitypolicies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nutanixvmantiaffinitypolicies/finalizers,verbs=update

// Reconcile works with NutanixVMAntiAffinityPolicy objects and handles the reconciliation logic.
// It is responsible for creating, updating, and deleting NutanixVMAntiAffinityPolicy resources
// based on the desired state defined in the NutanixVMAntiAffinityPolicySpec.
// It also manages the status of the NutanixVMAntiAffinityPolicy resources and handles any errors that occur during reconciliation.
// It uses the controller-runtime library to interact with the Kubernetes API and manage the lifecycle of NutanixVMAntiAffinityPolicy resources.
// It is called by the controller manager when a NutanixVMAntiAffinityPolicy resource is created, updated, or deleted.
func (r *NutanixPolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	log.Info("Reconciling NutanixVMAntiAffinityPolicy")

	preflightReconcileRegistry := []nutanixPreflightReconcilerUnitOfWork{
		{
			StepCondition:          ClusterAnnotationReady,
			PreflightReconcileFunc: r.preflightCheckClusterAnnotation,
			Reconciler:             r,
		},
		{
			StepCondition:          ClusterIdentityReady,
			PreflightReconcileFunc: r.preflightCheckClusterIdentity,
			Reconciler:             r,
		},
	}

	// Fetch the NutanixVMAntiAffinityPolicy instance
	nutanixVMAntiAffinityPolicy := &infrav1.NutanixVMAntiAffinityPolicy{}
	if err := r.Get(ctx, req.NamespacedName, nutanixVMAntiAffinityPolicy); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("NutanixVMAntiAffinityPolicy not found, ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}

		// Error reading the object - requeue the request.
		log.Error(err, "Failed to get NutanixVMAntiAffinityPolicy")
		return ctrl.Result{}, err
	}

	// Initialize the patch helper.
	patchHelper, err := patch.NewHelper(nutanixVMAntiAffinityPolicy, r.Client)
	if err != nil {
		log.Error(err, "failed to configure the patch helper")
		return ctrl.Result{Requeue: true}, nil
	}

	defer func() {
		// Always patch the NutanixVMAntiAffinityPolicy after reconciliation
		if err := patchHelper.Patch(ctx, nutanixVMAntiAffinityPolicy, patch.WithStatusObservedGeneration{}); err != nil {
			log.Error(err, "Failed to patch NutanixVMAntiAffinityPolicy after reconciliation")
		}
	}()

	ectx := &nctx.ExtendedContext{
		Context:        ctx,
		K8sPatchHelper: patchHelper,
	}

	scope := &nctx.VMAntiAffinityPolicyScope{
		NutanixCluster:              nil, // Will be set later after checking the cluster annotation
		NutanixVMAntiAffinityPolicy: nutanixVMAntiAffinityPolicy,
	}

	for _, preflightReconcileUnit := range preflightReconcileRegistry {
		log.Info("Running preflight reconcile step", "ConditionType", preflightReconcileUnit.StepCondition)
		// Run the preflight reconcile function
		scope, err = preflightReconcileUnit.Run(ectx, scope)
		if scope == nil {
			log.Error(fmt.Errorf("preflight reconcile function returned nil scope"), "Failed to run preflight reconcile function")
			return ctrl.Result{}, fmt.Errorf("preflight reconcile function returned nil scope")
		}
		if err != nil {
			log.Error(err, "Failed to run preflight reconcile function")
			return ctrl.Result{RequeueAfter: time.Second * 30}, nil
		}
	}

	if !scope.NutanixCluster.DeletionTimestamp.IsZero() {
		log.Info("NutanixCluster is marked for deletion, deleting NutanixVMAntiAffinityPolicy")
		// If the NutanixCluster is marked for deletion, we should delete the NutanixVMAntiAffinityPolicy as well
		if err := r.Delete(ctx, nutanixVMAntiAffinityPolicy); err != nil {
			log.Error(err, "Failed to delete NutanixVMAntiAffinityPolicy for deleted NutanixCluster")
			conditions.MarkFalse(
				nutanixVMAntiAffinityPolicy,
				ClusterIdentityReady,
				"ClusterMarkedForDeletion",
				capiv1.ConditionSeverityWarning,
				"NutanixCluster %s is marked for deletion, deleting NutanixVMAntiAffinityPolicy", scope.NutanixCluster.Name,
			)
			if err := patchHelper.Patch(ctx, nutanixVMAntiAffinityPolicy, patch.WithStatusObservedGeneration{}); err != nil {
				log.Error(err, "Failed to patch NutanixVMAntiAffinityPolicy after deleting for NutanixCluster marked for deletion")
				return ctrl.Result{}, err
			}
		}
	}

	// Get Nutanix v4 API client
	v4FacadeClient, err := getFacadePrismCentralV4ClientForCluster(ctx, scope.NutanixCluster, r.SecretInformer, r.ConfigMapInformer)
	if err != nil {
		log.Error(err, "Failed to get Nutanix v4 API client")
		conditions.MarkFalse(
			nutanixVMAntiAffinityPolicy,
			PcClientReady,
			"NoClient",
			capiv1.ConditionSeverityWarning,
			"Failed to get Nutanix v4 API client",
		)

		if err := patchHelper.Patch(ctx, nutanixVMAntiAffinityPolicy, patch.WithStatusObservedGeneration{}); err != nil {
			log.Error(err, "Failed to patch NutanixVMAntiAffinityPolicy after getting Nutanix v4 API client")
		}
	}

	var v3Client *prismclientv3.Client

	if v4FacadeClient == nil {
		v3Client, err = getPrismCentralClientForCluster(ctx, scope.NutanixCluster, r.SecretInformer, r.ConfigMapInformer)
		if err != nil {
			log.Error(err, "Failed to get Nutanix v3 API client")
			conditions.MarkFalse(
				nutanixVMAntiAffinityPolicy,
				PcClientReady,
				"NoClient",
				capiv1.ConditionSeverityWarning,
				"Failed to get Nutanix v3 API client",
			)

			if err := patchHelper.Patch(ctx, nutanixVMAntiAffinityPolicy, patch.WithStatusObservedGeneration{}); err != nil {
				log.Error(err, "Failed to patch NutanixVMAntiAffinityPolicy after getting Nutanix v3 API client")
			}
			return ctrl.Result{}, err
		}
	}

	// Mark the condition as true if the Nutanix v4 API client is ready
	conditions.MarkTrue(nutanixVMAntiAffinityPolicy, PcClientReady)

	// Create VMAniffinityPolicyContext
	vmAntiAffinityPolicyContextFacadeV4 := &nctx.VMAntiAffinityPolicyContext[facade.FacadeClientV4]{
		NutanixReconcileContext: nctx.NutanixReconcileContext[facade.FacadeClientV4]{
			ExtendedContext: *ectx,
			NutanixClient:   v4FacadeClient,
		},
		VMAntiAffinityPolicyScope: *scope,
	}

	vmAntiAffinityPolicyContextV3 := &nctx.VMAntiAffinityPolicyContext[*prismclientv3.Client]{
		NutanixReconcileContext: nctx.NutanixReconcileContext[*prismclientv3.Client]{
			ExtendedContext: *ectx,
			NutanixClient:   v3Client,
		},
		VMAntiAffinityPolicyScope: *scope,
	}

	v4ReconcileList := []nutanixPolicyReconcilerUnitOfWork[facade.FacadeClientV4]{
		{
			ReconcileFunc: func(pctx *nctx.VMAntiAffinityPolicyContext[facade.FacadeClientV4]) (ctrl.Result, error) {
				return r.ReconcileNutanixPCVersionCompatibilityV4(pctx)
			},
			StepCondition: PcVersionCompatibility,
		},
		{
			ReconcileFunc: func(pctx *nctx.VMAntiAffinityPolicyContext[facade.FacadeClientV4]) (ctrl.Result, error) {
				return r.ReconcileAnnotations(pctx)
			},
			StepCondition: AnnotationsReconciled,
		},
		{
			ReconcileFunc: func(pctx *nctx.VMAntiAffinityPolicyContext[facade.FacadeClientV4]) (ctrl.Result, error) {
				return r.ReconcileFinalizer(pctx)
			},
			StepCondition: FinalizerAdded,
		},
		{
			ReconcileFunc: func(pctx *nctx.VMAntiAffinityPolicyContext[facade.FacadeClientV4]) (ctrl.Result, error) {
				return r.ReconcileCategories(pctx)
			},
			StepCondition: CategoriesReady,
		},
		{
			ReconcileFunc: func(pctx *nctx.VMAntiAffinityPolicyContext[facade.FacadeClientV4]) (ctrl.Result, error) {
				return r.ReconcilePolicyCreate(pctx)
			},
			StepCondition: PolicyCreated,
		},
		{
			ReconcileFunc: func(pctx *nctx.VMAntiAffinityPolicyContext[facade.FacadeClientV4]) (ctrl.Result, error) {
				return r.ReconcilePolicyAlreadyPresentInPC(pctx)
			},
			StepCondition: PolicyAlreadyPresentInPC,
		},
		{
			ReconcileFunc: func(pctx *nctx.VMAntiAffinityPolicyContext[facade.FacadeClientV4]) (ctrl.Result, error) {
				return r.ReconcilePolicyUpdate(pctx)
			},
			StepCondition: PolicyUpdated,
		},
	}

	v3ReconcileList := []nutanixPolicyReconcilerUnitOfWork[*prismclientv3.Client]{
		{
			ReconcileFunc: func(pctx *nctx.VMAntiAffinityPolicyContext[*prismclientv3.Client]) (ctrl.Result, error) {
				return r.ReconcileNutanixPCVersionCompatibilityV3(pctx)
			},
			StepCondition: PcVersionCompatibility,
		},
	}

	if v4FacadeClient != nil {
		// Check if the NutanixVMAntiAffinityPolicy is marked for deletion
		if !nutanixVMAntiAffinityPolicy.DeletionTimestamp.IsZero() {
			// The NutanixVMAntiAffinityPolicy is marked for deletion, so we need to handle the deletion logic
			log.Info("NutanixVMAntiAffinityPolicy is marked for deletion")
			return r.reconcileDelete(vmAntiAffinityPolicyContextFacadeV4)
		}

		for _, v4ReconcileUnit := range v4ReconcileList {
			log.Info("Running v4 reconcile step", "ConditionType", v4ReconcileUnit.StepCondition)
			// Run the v4 reconcile function
			result, err := v4ReconcileUnit.Run(vmAntiAffinityPolicyContextFacadeV4)
			if err != nil {
				log.Error(err, "Failed to run v4 reconcile function")
				return result, err
			}
			if result.Requeue || result.RequeueAfter > 0 {
				return result, nil // Requeue if needed
			}
		}
	} else if v3Client != nil {
		for _, v3ReconcileUnit := range v3ReconcileList {
			log.Info("Running v3 reconcile step", "ConditionType", v3ReconcileUnit.StepCondition)
			// Run the v3 reconcile function
			result, err := v3ReconcileUnit.Run(vmAntiAffinityPolicyContextV3)
			if err != nil {
				log.Error(err, "Failed to run v3 reconcile function")
				return result, err
			}
			if result.Requeue || result.RequeueAfter > 0 {
				return result, nil // Requeue if needed
			}
		}
	}

	log.Error(fmt.Errorf("no Nutanix v4 or v3 API client found"), "Failed to get Nutanix API client")
	return ctrl.Result{}, errors.New("no Nutanix v4 or v3 API client found")
}

func (r *NutanixPolicyReconciler) ReconcileNutanixPCVersionCompatibilityV4(pctx *nctx.VMAntiAffinityPolicyContext[facade.FacadeClientV4]) (ctrl.Result, error) {
	log := log.FromContext(pctx.Context)
	log.Info("Reconciling Nutanix PC version compatibility")

	return ctrl.Result{}, nil
}

func (r *NutanixPolicyReconciler) ReconcileNutanixPCVersionCompatibilityV3(pctx *nctx.VMAntiAffinityPolicyContext[*prismclientv3.Client]) (ctrl.Result, error) {
	log := log.FromContext(pctx.Context)
	log.Info("Reconciling Nutanix PC version compatibility for v3 client")

	return ctrl.Result{RequeueAfter: 5 * time.Minute}, errors.New("Nutanix VM-VM anti-affinity policies is not supported for current PC version")
}

func (r *NutanixPolicyReconciler) ReconcileFinalizer(pctx *nctx.VMAntiAffinityPolicyContext[facade.FacadeClientV4]) (ctrl.Result, error) {
	log := log.FromContext(pctx.Context)
	log.Info("Reconciling finalizer for NutanixVMAntiAffinityPolicy")

	// Get the NutanixVMAntiAffinityPolicy
	nutanixVMAntiAffinityPolicy := pctx.NutanixVMAntiAffinityPolicy

	// Check if the NutanixVMAntiAffinityPolicy already has the finalizer
	if controllerutil.ContainsFinalizer(nutanixVMAntiAffinityPolicy, NutanixVMAntiAffinityPolicyFinalizerName) {
		log.Info("NutanixVMAntiAffinityPolicy already has the finalizer, skipping")
		return ctrl.Result{}, nil
	}

	// Add the finalizer to the NutanixVMAntiAffinityPolicy
	controllerutil.AddFinalizer(nutanixVMAntiAffinityPolicy, NutanixVMAntiAffinityPolicyFinalizerName)
	log.Info("Added finalizer to NutanixVMAntiAffinityPolicy", "Finalizer", NutanixVMAntiAffinityPolicyFinalizerName)

	return ctrl.Result{}, nil
}

func (r *NutanixPolicyReconciler) ReconcileCategories(pctx *nctx.VMAntiAffinityPolicyContext[facade.FacadeClientV4]) (ctrl.Result, error) {
	log := log.FromContext(pctx.Context)
	log.Info("Reconciling categories for NutanixVMAntiAffinityPolicy")

	// Get or create categories
	_, err := r.GetOrCreateCategories(pctx)
	if err != nil {
		log.Error(err, "Failed to get or create categories for NutanixVMAntiAffinityPolicy")

		// Requeue the request to check again later
		return ctrl.Result{Requeue: true}, err
	}

	return ctrl.Result{}, nil
}

func (r *NutanixPolicyReconciler) ReconcilePolicyCreate(pctx *nctx.VMAntiAffinityPolicyContext[facade.FacadeClientV4]) (ctrl.Result, error) {
	log := log.FromContext(pctx.Context)
	log.Info("Reconciling policy creation for NutanixVMAntiAffinityPolicy")

	// Check if the policy is already present in PC
	if pctx.NutanixVMAntiAffinityPolicy.Status.UUID != "" {
		log.Info("NutanixVMAntiAffinityPolicy should be present in PC", "UUID", pctx.NutanixVMAntiAffinityPolicy.Status.UUID)
		return ctrl.Result{}, nil
	}

	policyName := GetPolicyName(pctx)
	log.Info("Trying to get Nutanix VM Anti-Affinity Policy by name", "Name", policyName)

	// Try to get the policy by name
	policies, err := pctx.NutanixClient.ListAntiAffinityPolicies(
		facade.WithPage(0),
		facade.WithLimit(100),
		facade.WithFilter(fmt.Sprintf("name eq %s", policyName)),
	)

	if err != nil {
		log.Error(err, "Failed to list Nutanix VM Anti-Affinity Policies")
		return ctrl.Result{Requeue: true}, fmt.Errorf("failed to list Nutanix VM Anti-Affinity Policies: %w", err)
	}

	if len(policies) == 0 {
		log.Info("Nutanix VM Anti-Affinity Policy not found, creating a new one", "Name", policyName)

		// Get or create categories
		policyCategories, err := r.GetOrCreateCategories(pctx)
		if err != nil {
			log.Error(err, "Failed to get or create categories for NutanixVMAntiAffinityPolicy")
			return ctrl.Result{RequeueAfter: 2 * time.Second}, err
		}

		// Create a new policy
		newPolicyWaiter, err := pctx.NutanixClient.CreateAntiAffinityPolicy(
			policiesv4.VmAntiAffinityPolicy{
				Name:        &policyName,
				Description: &pctx.NutanixVMAntiAffinityPolicy.Spec.Description,
				Categories:  policyCategories,
			},
		)
		if err != nil {
			log.Error(err, "Failed to create Nutanix VM Anti-Affinity Policy")
			return ctrl.Result{Requeue: true}, fmt.Errorf("failed to create Nutanix VM Anti-Affinity Policy: %w", err)
		}

		newPolicy, err := newPolicyWaiter.WaitForTaskCompletion()
		if err != nil {
			log.Error(err, "Failed to create Nutanix VM Anti-Affinity Policy")
			return ctrl.Result{Requeue: true}, fmt.Errorf("failed to create Nutanix VM Anti-Affinity Policy: %w", err)
		}
		// Update the status of the NutanixVMAntiAffinityPolicy with the UUID of the created policy
		pctx.NutanixVMAntiAffinityPolicy.Status.UUID = *newPolicy[0].ExtId
		pctx.NutanixVMAntiAffinityPolicy.Status.CleanupPolicy = true // Mark the policy for cleanup

		log.Info("Nutanix VM Anti-Affinity Policy created", "UUID", newPolicy[0].ExtId)
	}

	return ctrl.Result{}, nil
}

func (r *NutanixPolicyReconciler) ReconcilePolicyAlreadyPresentInPC(pctx *nctx.VMAntiAffinityPolicyContext[facade.FacadeClientV4]) (ctrl.Result, error) {
	log := log.FromContext(pctx.Context)
	log.Info("Reconciling if policy is already present in PC for NutanixVMAntiAffinityPolicy")

	// Check if the policy is already present in PC
	nutanixVMAntiAffinityPolicy := pctx.NutanixVMAntiAffinityPolicy
	if nutanixVMAntiAffinityPolicy.Status.UUID == "" {
		log.Info("NutanixVMAntiAffinityPolicy does not have a UUID, requeuing", "Name", nutanixVMAntiAffinityPolicy.Name)
		return ctrl.Result{Requeue: true}, nil
	}

	// Get the policy by UUID
	policy, err := pctx.NutanixClient.GetAntiAffinityPolicy(nutanixVMAntiAffinityPolicy.Status.UUID)
	if err != nil {
		log.Error(err, "Failed to get Nutanix VM Anti-Affinity Policy by UUID", "UUID", nutanixVMAntiAffinityPolicy.Status.UUID)
	}

	if policy == nil {
		log.Info("Nutanix VM Anti-Affinity Policy not found by UUID, requeuing", "UUID", nutanixVMAntiAffinityPolicy.Status.UUID)
		// Requeue the request to check again later
		return ctrl.Result{Requeue: true}, fmt.Errorf("Nutanix VM Anti-Affinity Policy not found by UUID: %s", nutanixVMAntiAffinityPolicy.Status.UUID)
	}

	return ctrl.Result{}, nil
}

func (r *NutanixPolicyReconciler) ReconcilePolicyUpdate(pctx *nctx.VMAntiAffinityPolicyContext[facade.FacadeClientV4]) (ctrl.Result, error) {
	log := log.FromContext(pctx.Context)
	log.Info("Reconciling policy update for NutanixVMAntiAffinityPolicy")

	// Get the NutanixVMAntiAffinityPolicy
	nutanixVMAntiAffinityPolicy := pctx.NutanixVMAntiAffinityPolicy

	pcPolicy, err := pctx.NutanixClient.GetAntiAffinityPolicy(nutanixVMAntiAffinityPolicy.Status.UUID)
	if err != nil {
		log.Error(err, "Failed to get Nutanix VM Anti-Affinity Policy by UUID", "UUID", nutanixVMAntiAffinityPolicy.Status.UUID)
		// Requeue the request to check again later
		return ctrl.Result{Requeue: true}, fmt.Errorf("failed to get Nutanix VM Anti-Affinity Policy by UUID: %w", err)
	}

	// Check if policy shoud be updated
	if requiredUpdate(pcPolicy, pctx) {
		log.Info("Nutanix VM Anti-Affinity Policy needs to be updated", "UUID", pcPolicy.ExtId)

		// Update the policy with the categories from the NutanixVMAntiAffinityPolicy
		if _, err := r.UpdatePolicy(pcPolicy, pctx); err != nil {
			log.Error(err, "Failed to update Nutanix VM Anti-Affinity Policy")
			conditions.MarkFalse(
				nutanixVMAntiAffinityPolicy,
				PolicyUpdated,
				"PolicyUpdateError",
				capiv1.ConditionSeverityWarning,
				"Failed to update Nutanix VM Anti-Affinity Policy",
			)
			if err := pctx.K8sPatchHelper.Patch(pctx.Context, nutanixVMAntiAffinityPolicy, patch.WithStatusObservedGeneration{}); err != nil {
				log.Error(err, "Failed to patch NutanixVMAntiAffinityPolicy after policy update error")
				return ctrl.Result{}, err
			}
			// Requeue the request to check again later
			return ctrl.Result{Requeue: true}, nil
		}
		log.Info("Nutanix VM Anti-Affinity Policy updated successfully", "UUID", pcPolicy.ExtId)
		conditions.MarkTrue(nutanixVMAntiAffinityPolicy, PolicyUpdated)
	} else {
		log.Info("Nutanix VM Anti-Affinity Policy is up to date", "UUID", pcPolicy.ExtId)
	}

	// Update the status of the NutanixVMAntiAffinityPolicy with the UUID of the created policy
	if nutanixVMAntiAffinityPolicy.Status.UUID != *pcPolicy.ExtId {
		log.Info("Updating NutanixVMAntiAffinityPolicy status with the UUID of the created policy", "UUID", pcPolicy.ExtId)
		nutanixVMAntiAffinityPolicy.Status.UUID = *pcPolicy.ExtId
	}

	return ctrl.Result{}, nil
}

func (r *NutanixPolicyReconciler) reconcileDelete(pctx *nctx.VMAntiAffinityPolicyContext[facade.FacadeClientV4]) (ctrl.Result, error) {
	log := log.FromContext(pctx.Context)
	log.Info("Reconciling deletion of NutanixVMAntiAffinityPolicy")

	// Get the NutanixVMAntiAffinityPolicy
	nutanixVMAntiAffinityPolicy := pctx.NutanixVMAntiAffinityPolicy

	patchHelper, err := patch.NewHelper(nutanixVMAntiAffinityPolicy, r.Client)
	if err != nil {
		log.Error(err, "Failed to create patch helper for NutanixVMAntiAffinityPolicy")
		return ctrl.Result{Requeue: true}, nil
	}

	defer func() {
		// Always patch the NutanixVMAntiAffinityPolicy after reconciliation
		if err := patchHelper.Patch(pctx.Context, nutanixVMAntiAffinityPolicy, patch.WithStatusObservedGeneration{}); err != nil {
			log.Error(err, "Failed to patch NutanixVMAntiAffinityPolicy after reconciliation")
		}
	}()

	// Check if the NutanixVMAntiAffinityPolicy has the finalizer
	if !controllerutil.ContainsFinalizer(nutanixVMAntiAffinityPolicy, NutanixVMAntiAffinityPolicyFinalizerName) {
		log.Info("NutanixVMAntiAffinityPolicy does not have the finalizer, skipping deletion")
		return ctrl.Result{}, nil
	}

	// Check if the policy should be cleaned up
	if nutanixVMAntiAffinityPolicy.Status.CleanupPolicy {
		log.Info("NutanixVMAntiAffinityPolicy is marked for cleanup, deleting the policy")
		log.Info("Deleting Nutanix VM Anti-Affinity Policy", "UUID", nutanixVMAntiAffinityPolicy.Status.UUID)

		if nutanixVMAntiAffinityPolicy.Status.UUID != "" {
			err = r.DeleteVmAntiAffinityPolicy(pctx)
			if err != nil {
				log.Error(err, "Failed to delete Nutanix VM Anti-Affinity Policy")
				conditions.MarkFalse(
					nutanixVMAntiAffinityPolicy,
					PolicyDeleted,
					"DeleteError",
					capiv1.ConditionSeverityWarning,
					"Failed to delete Nutanix VM Anti-Affinity Policy",
				)

				if err := patchHelper.Patch(pctx.Context, nutanixVMAntiAffinityPolicy, patch.WithStatusObservedGeneration{}); err != nil {
					log.Error(err, "Failed to patch NutanixVMAntiAffinityPolicy after delete error")
					return ctrl.Result{}, err
				}
				// Requeue the request to check again later
				return ctrl.Result{Requeue: true}, nil
			}
		}
	}

	// Mark the condition as true if the policy is deleted
	conditions.MarkTrue(nutanixVMAntiAffinityPolicy, PolicyDeleted)

	// Check if there are categories to clean up
	if len(nutanixVMAntiAffinityPolicy.Status.CategoriesCleanupList) > 0 {
		err := r.DeleteCategories(pctx)
		if err != nil {
			log.Error(err, "Failed to delete categories associated with NutanixVMAntiAffinityPolicy")
			conditions.MarkFalse(
				nutanixVMAntiAffinityPolicy,
				CategoriesCleanedUp,
				"CategoryDeleteError",
				capiv1.ConditionSeverityWarning,
				"Failed to delete categories associated with NutanixVMAntiAffinityPolicy: %v", err,
			)

			if err := patchHelper.Patch(pctx.Context, nutanixVMAntiAffinityPolicy, patch.WithStatusObservedGeneration{}); err != nil {
				log.Error(err, "Failed to patch NutanixVMAntiAffinityPolicy after category delete error")
				return ctrl.Result{}, err
			}

			// Requeue the request to check again later
			return ctrl.Result{Requeue: true}, nil
		}
	} else {
		log.Info("No categories to clean up for NutanixVMAntiAffinityPolicy")
	}

	// Mark the condition as true if the categories are cleaned up
	conditions.MarkTrue(nutanixVMAntiAffinityPolicy, CategoriesCleanedUp)

	// Remove the finalizer from the NutanixVMAntiAffinityPolicy
	log.Info("Removing finalizer from NutanixVMAntiAffinityPolicy")
	if updated := controllerutil.RemoveFinalizer(nutanixVMAntiAffinityPolicy, NutanixVMAntiAffinityPolicyFinalizerName); !updated {
		log.Error(fmt.Errorf("failed to remove finalizer from NutanixVMAntiAffinityPolicy"), "Failed to remove finalizer")

		conditions.MarkFalse(
			nutanixVMAntiAffinityPolicy,
			FinalizerRemoved,
			"FinalizerError",
			capiv1.ConditionSeverityWarning,
			"Failed to remove finalizer from NutanixVMAntiAffinityPolicy",
		)

		if err := patchHelper.Patch(pctx.Context, nutanixVMAntiAffinityPolicy, patch.WithStatusObservedGeneration{}); err != nil {
			log.Error(err, "Failed to patch NutanixVMAntiAffinityPolicy after finalizer removal error")
			return ctrl.Result{}, err
		}

		// Requeue the request to check again later
		return ctrl.Result{Requeue: true}, nil
	}

	// Mark the condition as true if the finalizer is removed
	conditions.MarkTrue(nutanixVMAntiAffinityPolicy, FinalizerRemoved)

	log.Info("Finalizer removed from NutanixVMAntiAffinityPolicy successfully")

	if err := patchHelper.Patch(pctx.Context, nutanixVMAntiAffinityPolicy, patch.WithStatusObservedGeneration{}); err != nil {
		log.Error(err, "Failed to patch NutanixVMAntiAffinityPolicy after finalizer removal")
		return ctrl.Result{}, err
	}
	log.Info("NutanixVMAntiAffinityPolicy deleted successfully")

	// Return empty result to indicate that the reconciliation is complete
	return ctrl.Result{}, nil
}

func (r *NutanixPolicyReconciler) ReconcileAnnotations(pctx *nctx.VMAntiAffinityPolicyContext[facade.FacadeClientV4]) (ctrl.Result, error) {
	log := log.FromContext(pctx.Context)
	log.Info("Reconciling annotations for NutanixVMAntiAffinityPolicy")

	nutanixVMAntiAffinityPolicy := pctx.NutanixVMAntiAffinityPolicy

	// Check annotation for cleanup policy
	if cleanupPolicyUuidString, ok := nutanixVMAntiAffinityPolicy.Annotations[CleanupPolicyAnnotationName]; ok {
		policyUUID, err := uuid.Parse(cleanupPolicyUuidString)
		if err != nil {
			log.Error(err, "Failed to parse cleanup policy UUID from annotation", "Annotation", CleanupPolicyAnnotationName)
			return ctrl.Result{Requeue: true}, fmt.Errorf("failed to parse cleanup policy UUID from annotation: %w", err)
		}
		log.Info("Cleanup policy UUID found in annotation", "UUID", policyUUID)

		// Check if the policy already adopted
		if nutanixVMAntiAffinityPolicy.Status.UUID == "" {
			return ctrl.Result{}, nil
		}

		if nutanixVMAntiAffinityPolicy.Status.UUID != policyUUID.String() {
			log.Info("NutanixVMAntiAffinityPolicy UUID does not match the cleanup policy UUID, updating status", "CurrentUUID", nutanixVMAntiAffinityPolicy.Status.UUID, "CleanupPolicyUUID", policyUUID.String())
			return ctrl.Result{}, fmt.Errorf("NutanixVMAntiAffinityPolicy UUID does not match the cleanup policy UUID: %s != %s", nutanixVMAntiAffinityPolicy.Status.UUID, policyUUID.String())
		}

		log.Info("NutanixVMAntiAffinityPolicy UUID matches the cleanup policy UUID, updating status")
		nutanixVMAntiAffinityPolicy.Status.CleanupPolicy = true

		// Patch the NutanixVMAntiAffinityPolicy to update the status
		if err := pctx.K8sPatchHelper.Patch(pctx.Context, nutanixVMAntiAffinityPolicy, patch.WithStatusObservedGeneration{}); err != nil {
			log.Error(err, "Failed to patch NutanixVMAntiAffinityPolicy after updating cleanup policy status")
			return ctrl.Result{}, fmt.Errorf("failed to patch NutanixVMAntiAffinityPolicy after updating cleanup policy status: %w", err)
		}
		log.Info("NutanixVMAntiAffinityPolicy status updated with cleanup policy", "UUID", nutanixVMAntiAffinityPolicy.Status.UUID)
	}

	// Check if the NutanixVMAntiAffinityPolicy has annotations for categories to clean up
	for annotationKey, categoryKeyValue := range nutanixVMAntiAffinityPolicy.Annotations {
		if strings.HasPrefix(annotationKey, CategoryCleanupAnnotationPrefix) {
			// Extract the key and value from the annotation
			parts := strings.SplitN(categoryKeyValue, ":", 2)
			if len(parts) != 2 {
				log.Error(fmt.Errorf("invalid category cleanup annotation format"), "Invalid category cleanup annotation format", "Annotation", categoryKeyValue)
				return ctrl.Result{}, fmt.Errorf("invalid category cleanup annotation format: %s", categoryKeyValue)
			}
			key := parts[0]
			value := parts[1]

			log.Info("Category cleanup annotation found", "Key", key, "Value", value)

			// Add the category to the cleanup list in the status
			nutanixVMAntiAffinityPolicy.Status.CategoriesCleanupList = append(
				nutanixVMAntiAffinityPolicy.Status.CategoriesCleanupList,
				infrav1.NutanixCategoryIdentifier{
					Key:   key,
					Value: value,
				},
			)

			if err := pctx.K8sPatchHelper.Patch(pctx.Context, nutanixVMAntiAffinityPolicy, patch.WithStatusObservedGeneration{}); err != nil {
				log.Error(err, "Failed to patch NutanixVMAntiAffinityPolicy after adding category to cleanup list")
				return ctrl.Result{}, fmt.Errorf("failed to patch NutanixVMAntiAffinityPolicy after adding category to cleanup list: %w", err)
			}
		}
	}

	return ctrl.Result{}, nil
}

func (r *NutanixPolicyReconciler) GetOrCreateCategories(pctx *nctx.VMAntiAffinityPolicyContext[facade.FacadeClientV4]) ([]policiesv4.CategoryReference, error) {
	log := log.FromContext(pctx.Context)
	log.Info("Getting or creating categories for NutanixVMAntiAffinityPolicy")

	nutanixVMAntiAffinityPolicy := pctx.NutanixVMAntiAffinityPolicy
	policyCategories := make([]policiesv4.CategoryReference, 0)

	for _, category := range nutanixVMAntiAffinityPolicy.Spec.Categories {
		// Check if category exists
		page := 0
		limit := 100
		filter := fmt.Sprintf("(key eq '%s') and (value eq '%s')", category.Key, category.Value)

		log.Info("Checking for existing Nutanix Category", "Key", category.Key, "Value", category.Value)
		pcCategories, err := pctx.NutanixClient.ListCategories(
			facade.WithPage(page),
			facade.WithLimit(limit),
			facade.WithFilter(filter),
		)
		if err != nil {
			log.Error(err, "Failed to list Nutanix Categories", "Filter", filter)
			return nil, fmt.Errorf("failed to list Nutanix Categories: %w", err)
		}

		if len(pcCategories) == 0 {
			log.Info("Nutanix Category not found, creating new", "Key", category.Key)

			newCategory := prismModels.NewCategory()
			newCategory.Key = &category.Key
			newCategory.Value = &category.Value

			category, err := pctx.NutanixClient.CreateCategory(newCategory)
			if err != nil {
				log.Error(err, "Failed to create Nutanix Category", "Key", category.Key, "Value", category.Value)
				return nil, fmt.Errorf("failed to create Nutanix Category: %w", err)
			}

			// Add category to status cleanup list
			nutanixVMAntiAffinityPolicy.Status.CategoriesCleanupList = append(
				nutanixVMAntiAffinityPolicy.Status.CategoriesCleanupList,
				infrav1.NutanixCategoryIdentifier{
					Key:   *category.Key,
					Value: *category.Value,
				},
			)
			if err := pctx.K8sPatchHelper.Patch(pctx.Context, nutanixVMAntiAffinityPolicy, patch.WithStatusObservedGeneration{}); err != nil {
				log.Error(err, "Failed to patch NutanixVMAntiAffinityPolicy after creating category")
				return nil, fmt.Errorf("failed to patch NutanixVMAntiAffinityPolicy after creating category: %w", err)
			}

			log.Info("Nutanix Category created successfully", "Key", category.Key, "Value", category.Value, "UUID", newCategory.ExtId)
		} else {
			log.Info("Nutanix Category found", "Key", category.Key, "Value", category.Value, "UUID", pcCategories[0].ExtId)
			policyCategories = append(policyCategories, policiesv4.CategoryReference{
				ExtId: pcCategories[0].ExtId,
			})
		}
	}
	log.Info("All categories processed for NutanixVMAntiAffinityPolicy", "CategoriesCount", len(policyCategories))
	return policyCategories, nil
}

func (r *NutanixPolicyReconciler) GetVmAntiAffinityPolicy(pctx *nctx.VMAntiAffinityPolicyContext[facade.FacadeClientV4]) (*policiesv4.VmAntiAffinityPolicy, error) {
	log := log.FromContext(pctx.Context)
	log.Info("Trying  to get Nutanix VM Anti-Affinity Policy")

	// Check if UUID is set in the status
	if pctx.NutanixVMAntiAffinityPolicy.Status.UUID != "" {
		log.Info("Nutanix VM Anti-Affinity Policy UUID found in status, fetching by UUID", "UUID", pctx.NutanixVMAntiAffinityPolicy.Status.UUID)

		// Fetch the existing policy by UUID
		policy, err := pctx.NutanixClient.GetAntiAffinityPolicy(pctx.NutanixVMAntiAffinityPolicy.Status.UUID)
		if err != nil {
			log.Error(err, "Failed to get Nutanix VM Anti-Affinity Policy by UUID", "UUID", pctx.NutanixVMAntiAffinityPolicy.Status.UUID)
			return nil, fmt.Errorf("failed to get Nutanix VM Anti-Affinity Policy by UUID: %w", err)
		}

		log.Info("Nutanix VM Anti-Affinity Policy fetched successfully", "UUID", policy.ExtId)
		return policy, nil
	}

	// Trying to get the policy by name
	policyName := GetPolicyName(pctx)

	filter := fmt.Sprintf("name eq '%s'", policyName)
	page := 0
	limit := 100

	policies, err := pctx.NutanixClient.ListAntiAffinityPolicies(
		facade.WithPage(page),
		facade.WithLimit(limit),
		facade.WithFilter(filter),
	)
	if err != nil {
		log.Error(err, "Failed to list Nutanix VM Anti-Affinity Policies", "Filter", filter)
		return nil, fmt.Errorf("failed to list Nutanix VM Anti-Affinity Policies: %w", err)
	}
	if len(policies) == 0 {
		log.Info("No Nutanix VM Anti-Affinity Policies found", "Filter", filter)
		return nil, nil
	}

	log.Info("Nutanix VM Anti-Affinity Policy found", "Name", policies[0].Name, "UUID", policies[0].ExtId)
	return &policies[0], nil
}

func GetPolicyName(pctx *nctx.VMAntiAffinityPolicyContext[facade.FacadeClientV4]) string {
	result := pctx.NutanixVMAntiAffinityPolicy.Spec.Name
	if result == "" {
		// Generate name if not provided based on cluster name and namespace, policy metadata
		result = fmt.Sprintf("%s-%s", pctx.NutanixCluster.GetNamespacedName(), pctx.NutanixVMAntiAffinityPolicy.Name)
	}
	return result
}

func (r *NutanixPolicyReconciler) CreateVMAntiAffinityPolicy(pctx *nctx.VMAntiAffinityPolicyContext[facade.FacadeClientV4]) (*policiesv4.VmAntiAffinityPolicy, error) {
	log := log.FromContext(pctx.Context)
	log.Info("Creating Nutanix VM Anti-Affinity Policy")

	nutanixVMAntiAffinityPolicy := pctx.NutanixVMAntiAffinityPolicy

	// Get or create categories
	policyCategories, err := r.GetOrCreateCategories(pctx)
	if err != nil {
		log.Error(err, "Failed to get or create categories for NutanixVMAntiAffinityPolicy")
		return nil, err
	}

	// Create a new Nutanix VM Anti-Affinity Policy
	policyName := GetPolicyName(pctx)
	policyDescription := nutanixVMAntiAffinityPolicy.Spec.Description

	newPolicyWaiter, err := pctx.NutanixClient.CreateAntiAffinityPolicy(
		policiesv4.VmAntiAffinityPolicy{
			Name:        &policyName,
			Description: &policyDescription,
			Categories:  policyCategories,
		},
	)
	if err != nil {
		log.Error(err, "Failed to create Nutanix VM Anti-Affinity Policy", "Name", policyName, "Description", policyDescription)
		return nil, fmt.Errorf("failed to create Nutanix VM Anti-Affinity Policy: %w", err)
	}

	// Wait for the policy creation task to complete
	newPolicies, err := newPolicyWaiter.WaitForTaskCompletion()
	if err != nil {
		log.Error(err, "Failed to wait for Nutanix VM Anti-Affinity Policy creation task to complete", "Name", policyName, "Description", policyDescription)
		return nil, fmt.Errorf("failed to wait for Nutanix VM Anti-Affinity Policy creation task to complete: %w", err)
	}
	if len(newPolicies) == 0 {
		log.Error(fmt.Errorf("no policies returned after creation"), "Failed to create Nutanix VM Anti-Affinity Policy", "Name", policyName, "Description", policyDescription)
		return nil, fmt.Errorf("no policies returned after creation for Nutanix VM Anti-Affinity Policy: %s", policyName)
	}
	newPolicy := newPolicies[0]
	log.Info("Nutanix VM Anti-Affinity Policy created successfully", "Name", policyName, "Description", policyDescription, "UUID", newPolicy.ExtId)

	return newPolicy, nil
}

func (r *NutanixPolicyReconciler) UpdatePolicy(policy *policiesv4.VmAntiAffinityPolicy, pctx *nctx.VMAntiAffinityPolicyContext[facade.FacadeClientV4]) (*policiesv4.VmAntiAffinityPolicy, error) {
	log := log.FromContext(pctx.Context)
	log.Info("Updating Nutanix VM Anti-Affinity Policy", "UUID", policy.ExtId)

	nutanixVMAntiAffinityPolicy := pctx.NutanixVMAntiAffinityPolicy

	if pctx.NutanixVMAntiAffinityPolicy.Spec.Name != "" {
		policy.Name = &pctx.NutanixVMAntiAffinityPolicy.Spec.Name
	} else {
		policyName := fmt.Sprintf("%s-%s", pctx.NutanixCluster.GetNamespacedName(), nutanixVMAntiAffinityPolicy.Name)
		policy.Name = &policyName
	}

	if nutanixVMAntiAffinityPolicy.Spec.Description != "" {
		policy.Description = &nutanixVMAntiAffinityPolicy.Spec.Description
	}

	// Get or create categories
	policyCategories, err := r.GetOrCreateCategories(pctx)
	if err != nil {
		log.Error(err, "Failed to get or create categories for NutanixVMAntiAffinityPolicy")
		return nil, err
	}
	policy.Categories = policyCategories

	updatedPolicyWaiter, err := pctx.NutanixClient.UpdateAntiAffinityPolicy(*policy.ExtId, *policy)
	if err != nil {
		log.Error(err, "Failed to create Nutanix VM Anti-Affinity Policy update task", "UUID", policy.ExtId)
		return nil, fmt.Errorf("failed to create Nutanix VM Anti-Affinity Policy update task: %w", err)
	}

	// Wait for the update task to complete
	updatedPolicies, err := updatedPolicyWaiter.WaitForTaskCompletion()
	if err != nil {
		log.Error(err, "Failed to wait for Nutanix VM Anti-Affinity Policy update task to complete", "UUID", policy.ExtId)
		return nil, fmt.Errorf("failed to wait for Nutanix VM Anti-Affinity Policy update task to complete: %w", err)
	}
	if len(updatedPolicies) == 0 {
		log.Error(fmt.Errorf("no policies returned after update"), "Failed to update Nutanix VM Anti-Affinity Policy", "UUID", policy.ExtId)
		return nil, fmt.Errorf("no policies returned after update for Nutanix VM Anti-Affinity Policy: %s", policy.ExtId)
	}
	updatedPolicy := updatedPolicies[0]
	log.Info("Nutanix VM Anti-Affinity Policy updated successfully", "UUID", updatedPolicy.ExtId)
	return updatedPolicy, nil
}

func (r *NutanixPolicyReconciler) DeleteCategories(pctx *nctx.VMAntiAffinityPolicyContext[facade.FacadeClientV4]) error {
	log := log.FromContext(pctx.Context)
	log.Info("Deleting categories for NutanixVMAntiAffinityPolicy")

	nutanixVMAntiAffinityPolicy := pctx.NutanixVMAntiAffinityPolicy

	// Check if there are categories to delete
	if len(nutanixVMAntiAffinityPolicy.Status.CategoriesCleanupList) == 0 {
		log.Info("No categories to delete for NutanixVMAntiAffinityPolicy")
		return nil
	}

	for _, category := range nutanixVMAntiAffinityPolicy.Status.CategoriesCleanupList {
		log.Info("Deleting category", "Key", category.Key, "Value", category.Value)
		page := 0
		limit := 100
		filter := fmt.Sprintf("(key eq '%s') and (value eq '%s')", category.Key, category.Value)

		pcCategories, err := pctx.NutanixClient.ListCategories(
			facade.WithPage(page),
			facade.WithLimit(limit),
			facade.WithFilter(filter),
		)
		if err != nil {
			log.Error(err, "Failed to list Nutanix Categories", "Filter", filter)
			return fmt.Errorf("failed to list Nutanix Categories: %w", err)
		}

		for _, pcCategory := range pcCategories {
			if err := pctx.NutanixClient.DeleteCategory(*pcCategory.ExtId); err != nil {
				log.Error(err, "Failed to delete Nutanix Category", "Key", category.Key, "Value", category.Value)
				return fmt.Errorf("failed to delete Nutanix Category: %w", err)
			}
			log.Info("Nutanix Category deleted successfully", "Key", category.Key, "Value", category.Value, "UUID", pcCategory.ExtId)
		}
	}

	return nil
}

func (r *NutanixPolicyReconciler) DeleteVmAntiAffinityPolicy(pctx *nctx.VMAntiAffinityPolicyContext[facade.FacadeClientV4]) error {
	log := log.FromContext(pctx.Context)
	log.Info("Deleting Nutanix VM Anti-Affinity Policy")

	nutanixVMAntiAffinityPolicy := pctx.NutanixVMAntiAffinityPolicy

	// Check if the policy UUID is set
	if nutanixVMAntiAffinityPolicy.Status.UUID == "" {
		log.Info("Nutanix VM Anti-Affinity Policy UUID is empty, nothing to delete")
		return nil
	}

	// Get the existing policy by UUID
	deleteWaiter, err := pctx.NutanixClient.DeleteAntiAffinityPolicy(nutanixVMAntiAffinityPolicy.Status.UUID)
	if err != nil {
		log.Error(err, "Failed to create Nutanix VM Anti-Affinity Policy delete task", "UUID", nutanixVMAntiAffinityPolicy.Status.UUID)
		return fmt.Errorf("failed to create Nutanix VM Anti-Affinity Policy delete task: %w", err)
	}
	// Wait for the delete task to complete
	_, err = deleteWaiter.WaitForTaskCompletion()
	if err != nil {
		log.Error(err, "Failed to wait for Nutanix VM Anti-Affinity Policy delete task to complete", "UUID", nutanixVMAntiAffinityPolicy.Status.UUID)
		return fmt.Errorf("failed to wait for Nutanix VM Anti-Affinity Policy delete task to complete: %w", err)
	}

	log.Info("Nutanix VM Anti-Affinity Policy deleted successfully", "UUID", nutanixVMAntiAffinityPolicy.Status.UUID)
	return nil
}

func requiredUpdate(policy *policiesv4.VmAntiAffinityPolicy, pctx *nctx.VMAntiAffinityPolicyContext[facade.FacadeClientV4]) bool {
	log := log.FromContext(pctx.Context)
	nutanixVMAntiAffinityPolicy := pctx.NutanixVMAntiAffinityPolicy
	log.Info("Checking if Nutanix VM Anti-Affinity Policy requires update", "UUID", policy.ExtId)

	// Check if the policy name or description has changed
	if policy.Name == nil || *policy.Name != nutanixVMAntiAffinityPolicy.Spec.Name {
		return true
	}
	if policy.Description == nil || *policy.Description != nutanixVMAntiAffinityPolicy.Spec.Description {
		return true
	}

	// fetch all categories references
	policyCategories := make([]prismModels.Category, 0)
	for _, categoryRef := range policy.Categories {
		// Fetch the category by ExtId
		category, err := pctx.NutanixClient.GetCategory(*categoryRef.ExtId)
		if err != nil {
			log.Error(err, "Failed to get Nutanix Category by ExtId", "ExtId", categoryRef.ExtId)
			return true // If we can't fetch the category, we assume it has changed
		}
		policyCategories = append(policyCategories, *category)
	}

	specCategories := nutanixVMAntiAffinityPolicy.Spec.Categories

	sort.SliceStable(policyCategories, func(i, j int) bool {
		if *policyCategories[i].Key == *policyCategories[j].Key {
			return *policyCategories[i].Value < *policyCategories[j].Value
		}
		return *policyCategories[i].Key < *policyCategories[j].Key
	})

	sort.SliceStable(specCategories, func(i, j int) bool {
		if nutanixVMAntiAffinityPolicy.Spec.Categories[i].Key == nutanixVMAntiAffinityPolicy.Spec.Categories[j].Key {
			return nutanixVMAntiAffinityPolicy.Spec.Categories[i].Value < nutanixVMAntiAffinityPolicy.Spec.Categories[j].Value
		}
		return nutanixVMAntiAffinityPolicy.Spec.Categories[i].Key < nutanixVMAntiAffinityPolicy.Spec.Categories[j].Key
	})

	for i, category := range policyCategories {
		if i >= len(specCategories) || category.Key == nil || category.Value == nil ||
			specCategories[i].Key != *category.Key || specCategories[i].Value != *category.Value {
			log.Info("Nutanix VM Anti-Affinity Policy categories have changed", "CategoryKey", category.Key, "CategoryValue", category.Value)
			return true
		}
	}

	return false
}
