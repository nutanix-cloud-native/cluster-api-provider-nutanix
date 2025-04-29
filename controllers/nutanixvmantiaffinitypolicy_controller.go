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
	"fmt"
	"k8s.io/utils/ptr"
	"strings"
	"time"

	"github.com/nutanix-cloud-native/prism-go-client/environment/credentials"
	v4 "github.com/nutanix-cloud-native/prism-go-client/v4"
	prismv4 "github.com/nutanix/ntnx-api-golang-clients/prism-go-client/v4/models/prism/v4/config"
	"github.com/nutanix/ntnx-api-golang-clients/vmm-go-client/v4/models/vmm/v4/ahv/policies"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	apitypes "k8s.io/apimachinery/pkg/types"
	coreinformers "k8s.io/client-go/informers/core/v1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	ctrlutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
)

// NutanixVMAntiAffinityPolicyReconciler reconciles a NutanixVMAntiAffinityPolicy object
type NutanixVMAntiAffinityPolicyReconciler struct {
	Client            client.Client
	SecretInformer    coreinformers.SecretInformer
	ConfigMapInformer coreinformers.ConfigMapInformer
	Scheme            *runtime.Scheme
	controllerConfig  *ControllerConfig
}

func NewNutanixVMAntiAffinityPolicyReconciler(client client.Client, secretInformer coreinformers.SecretInformer, configMapInformer coreinformers.ConfigMapInformer, scheme *runtime.Scheme, copts ...ControllerConfigOpts) (*NutanixVMAntiAffinityPolicyReconciler, error) {
	controllerConf := &ControllerConfig{}
	for _, opt := range copts {
		if err := opt(controllerConf); err != nil {
			return nil, err
		}
	}
	return &NutanixVMAntiAffinityPolicyReconciler{
		Client:            client,
		SecretInformer:    secretInformer,
		ConfigMapInformer: configMapInformer,
		Scheme:            scheme,
		controllerConfig:  controllerConf,
	}, nil
}

//+kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nutanixvmantiaffinitypolicies,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nutanixvmantiaffinitypolicies/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nutanixvmantiaffinitypolicies/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=secrets;configmaps,verbs=get;list;watch
//+kubebuilder:rbac:groups=credentials.nutanix.com,resources=nutanixprismidentities,verbs=get;list;watch

// Reconcile handles the reconciliation of NutanixVMAntiAffinityPolicy resources.
func (r *NutanixVMAntiAffinityPolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the NutanixVMAntiAffinityPolicy instance
	policy := &infrav1.NutanixVMAntiAffinityPolicy{}
	if err := r.Client.Get(ctx, req.NamespacedName, policy); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}

		return ctrl.Result{}, err
	}

	// Initialize the patch helper
	patchHelper, err := patch.NewHelper(policy, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Always attempt to patch the object and status after each reconciliation
	defer func() {
		// Always reconcile the finalizer
		if err := patchHelper.Patch(ctx, policy); err != nil {
			logger.Error(err, "failed to patch NutanixVMAntiAffinityPolicy")
		}
	}()

	// Add finalizer if it doesn't exist
	if !ctrlutil.ContainsFinalizer(policy, infrav1.NutanixVMAntiAffinityPolicyFinalizer) {
		if ok := ctrlutil.AddFinalizer(policy, infrav1.NutanixVMAntiAffinityPolicyFinalizer); !ok {
			logger.Info("Error adding finalizer to NutanixVMAntiAffinityPolicy", "policy", policy.Name)
			return ctrl.Result{}, fmt.Errorf("failed to add finalizer to NutanixVMAntiAffinityPolicy %s", policy.Name)
		}
		return ctrl.Result{}, nil
	}

	// Handle deleted policy
	if !policy.ObjectMeta.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, policy)
	}

	// Handle non-deleted policy
	return r.reconcileNormal(ctx, policy)
}

func (r *NutanixVMAntiAffinityPolicyReconciler) reconcileNormal(ctx context.Context, policy *infrav1.NutanixVMAntiAffinityPolicy) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling NutanixVMAntiAffinityPolicy", "policy", policy.Name)

	// Get the NutanixPrismIdentity
	prismIdentity := &credentials.NutanixPrismIdentity{}
	identityNamespacedName := apitypes.NamespacedName{
		Name:      policy.Spec.IdentityRef,
		Namespace: policy.Namespace,
	}

	if err := r.Client.Get(ctx, identityNamespacedName, prismIdentity); err != nil {
		logger.Error(err, "Failed to get NutanixPrismIdentity", "identityRef", policy.Spec.IdentityRef)
		conditions.MarkFalse(policy, "Available", "IdentityNotFound", capiv1.ConditionSeverityError, "Failed to get NutanixPrismIdentity: %v", err)
		return ctrl.Result{}, err
	}

	// Get or create a V4 client using the PrismIdentity
	v4Client, err := getPrismCentralV4ClientForPrismIdentity(ctx, prismIdentity, r.SecretInformer, r.ConfigMapInformer)
	if err != nil {
		logger.Error(err, "Failed to get V4 client", "identityRef", policy.Spec.IdentityRef)
		conditions.MarkFalse(policy, "Available", "ClientError", capiv1.ConditionSeverityError, "Failed to create V4 client: %v", err)
		return ctrl.Result{}, err
	}

	// Check if the policy exists - if it has a UUID, try to get it
	if policy.Status.PolicyUUID != "" {
		// Try to get the existing policy by UUID
		existingPolicy, err := v4Client.VmAntiAffinityPoliciesApiInstance.GetVmAntiAffinityPolicyById(&policy.Status.PolicyUUID)
		if err != nil {
			// If the policy doesn't exist in Prism, reset the UUID
			if strings.Contains(fmt.Sprint(err), "ENTITY_NOT_FOUND") {
				logger.Info("Anti-affinity policy no longer exists in Prism, will recreate", "policyUUID", policy.Status.PolicyUUID)
				policy.Status.PolicyUUID = ""
			} else {
				logger.Error(err, "Failed to get anti-affinity policy from Prism", "policyUUID", policy.Status.PolicyUUID)
				conditions.MarkFalse(policy, "Available", "FetchFailed", capiv1.ConditionSeverityError, "Failed to get policy from Prism: %v", err)
				return ctrl.Result{}, err
			}
		} else if existingPolicy != nil && existingPolicy.GetData() != nil {
			fetchedPolicy, ok := existingPolicy.GetData().(*policies.VmAntiAffinityPolicy)
			if !ok {
				logger.Error(err, "Failed to get data from existing policy", "policy", existingPolicy.GetData())
			}

			// Policy exists, update it if needed
			logger.Info("Found existing anti-affinity policy in Prism", "policyName", fetchedPolicy.Name)

			// TODO: Implement update logic if needed

			// Update the status with current information
			policy.Status.Ready = true

			policy.Status.NumCompliantVms = fetchedPolicy.NumCompliantVms
			policy.Status.NumNonCompliantVms = fetchedPolicy.NumNonCompliantVms
			policy.Status.NumPendingVms = fetchedPolicy.NumPendingVms

			// Mark policy as available
			conditions.MarkTrue(policy, "Available")
			return ctrl.Result{}, nil
		}
	}

	// Create a new policy if it doesn't exist or needs to be recreated
	if policy.Status.PolicyUUID == "" {
		// Build the anti-affinity policy request
		policyName := policy.Spec.Name

		// Create the policy request body
		vmAntiAffinityPolicy := &policies.VmAntiAffinityPolicy{
			Name: &policyName,
		}

		// Set description if provided
		if policy.Spec.Description != "" {
			vmAntiAffinityPolicy.Description = &policy.Spec.Description
		}

		// Add categories if provided
		if len(policy.Spec.Categories) > 0 {
			categories := make([]policies.CategoryReference, 0, len(policy.Spec.Categories))
			for _, cat := range policy.Spec.Categories {
				// Check if the category exists in Prism, if not create it
				categoryExtId, err := getOrCreateCategoryV4(ctx, v4Client, cat.Key, cat.Value)
				if err != nil {
					logger.Error(err, "Failed to get or create category", "key", cat.Key, "value", cat.Value)
					conditions.MarkFalse(policy, "Available", "CategoryError", capiv1.ConditionSeverityError, "Failed to get or create category: %v", err)
					return ctrl.Result{}, err
				}

				// Add the Category ExtId to the policy
				categoryRef := policies.CategoryReference{
					ExtId: ptr.To(categoryExtId),
				}
				categories = append(categories, categoryRef)
			}
			vmAntiAffinityPolicy.Categories = categories
		}

		// Create the policy in Prism
		logger.Info("Creating new anti-affinity policy in Prism", "policyName", policyName)
		createResponse, err := v4Client.VmAntiAffinityPoliciesApiInstance.CreateVmAntiAffinityPolicy(vmAntiAffinityPolicy)
		if err != nil {
			logger.Error(err, "Failed to create anti-affinity policy in Prism", "policyName", policyName)
			conditions.MarkFalse(policy, "Available", "CreationFailed", capiv1.ConditionSeverityError, "Failed to create policy in Prism: %v", err)
			return ctrl.Result{}, err
		}

		// Extract the policy UUID from the response
		if createResponse != nil && createResponse.GetData() != nil {
			policyRef, ok := createResponse.GetData().(*prismv4.TaskReference)
			if !ok {
				logger.Error(err, "Failed to get data from create response", "response", createResponse.GetData())
			}

			policyUUID := *policyRef.ExtId

			// Update the status with the new policy UUID
			policy.Status.PolicyUUID = policyUUID
			policy.Status.Ready = true
			policy.Status.CreateTime = &metav1.Time{Time: time.Now()}

			// Mark policy as available
			conditions.MarkTrue(policy, "Available")

			logger.Info("Successfully created anti-affinity policy in Prism", "policyName", policyName, "policyUUID", policyUUID)
		}
	}

	return ctrl.Result{}, nil
}

func (r *NutanixVMAntiAffinityPolicyReconciler) reconcileDelete(ctx context.Context, policy *infrav1.NutanixVMAntiAffinityPolicy) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Deleting NutanixVMAntiAffinityPolicy", "policy", policy.Name)

	// If the policy exists in Prism (has a UUID), we need to delete it
	if policy.Status.PolicyUUID != "" {
		// Get the NutanixPrismIdentity
		prismIdentity := &credentials.NutanixPrismIdentity{}
		identityNamespacedName := apitypes.NamespacedName{
			Name:      policy.Spec.IdentityRef,
			Namespace: policy.Namespace,
		}

		if err := r.Client.Get(ctx, identityNamespacedName, prismIdentity); err != nil {
			// If the identity is not found, we can't delete the policy in Prism
			// but we should still remove the finalizer to allow deletion of the k8s resource
			if errors.IsNotFound(err) {
				logger.Info("PrismIdentity not found, cannot delete policy in Prism", "identityRef", policy.Spec.IdentityRef)
				ctrlutil.RemoveFinalizer(policy, infrav1.NutanixVMAntiAffinityPolicyFinalizer)
				return ctrl.Result{}, nil
			}
			logger.Error(err, "Failed to get NutanixPrismIdentity", "identityRef", policy.Spec.IdentityRef)
			return ctrl.Result{}, err
		}

		// Get V4 client for the PrismIdentity
		v4Client, err := getPrismCentralV4ClientForPrismIdentity(ctx, prismIdentity, r.SecretInformer, r.ConfigMapInformer)
		if err != nil {
			logger.Error(err, "Failed to get V4 client for PrismIdentity", "identityRef", policy.Spec.IdentityRef)
			// If we can't get the client, we should still remove the finalizer to allow deletion of the k8s resource
			ctrlutil.RemoveFinalizer(policy, infrav1.NutanixVMAntiAffinityPolicyFinalizer)
			return ctrl.Result{}, nil
		}

		// Delete the policy in Prism
		logger.Info("Deleting anti-affinity policy in Prism", "policyUUID", policy.Status.PolicyUUID)
		_, err = v4Client.VmAntiAffinityPoliciesApiInstance.DeleteVmAntiAffinityPolicyById(&policy.Status.PolicyUUID)
		if err != nil {
			// If the policy doesn't exist in Prism, we can still proceed with deletion of the k8s resource
			if strings.Contains(fmt.Sprint(err), "ENTITY_NOT_FOUND") {
				logger.Info("Anti-affinity policy not found in Prism, proceeding with deletion", "policyUUID", policy.Status.PolicyUUID)
			} else {
				logger.Error(err, "Failed to delete anti-affinity policy in Prism", "policyUUID", policy.Status.PolicyUUID)
				// Return an error only if it's not a not-found error
				return ctrl.Result{}, err
			}
		} else {
			logger.Info("Successfully deleted anti-affinity policy in Prism", "policyUUID", policy.Status.PolicyUUID)
		}
	}

	// Remove finalizer to allow deletion
	ctrlutil.RemoveFinalizer(policy, infrav1.NutanixVMAntiAffinityPolicyFinalizer)

	return ctrl.Result{}, nil
}

// getOrCreateCategory checks if a category with the given key-value pair exists,
// creates it if not found, and returns the Category ExtId
func getOrCreateCategoryV4(ctx context.Context, v4Client *v4.Client, key, value string) (string, error) {
	logger := log.FromContext(ctx)

	// First, try to find an existing category with the given key-value
	filter := fmt.Sprintf("key==%s;value==%s", key, value)
	response, err := v4Client.CategoriesApiInstance.ListCategories(nil, nil, &filter, nil, nil, nil)
	if err != nil {
		return "", fmt.Errorf("failed to list categories: %w", err)
	}

	// If found, return its ExtId
	if response != nil && response.GetData() != nil {
		data := response.GetData()
		if categories, ok := data.([]*prismv4.Category); ok && len(categories) > 0 {
			logger.Info("Found existing category", "key", key, "value", value, "extId", *categories[0].ExtId)
			return *categories[0].ExtId, nil
		}
	}

	// Category not found, create a new one
	logger.Info("Creating new category", "key", key, "value", value)
	category := &prismv4.Category{
		Key:         &key,
		Value:       &value,
		Description: ptr.To("Created by CAPX NutanixVMAntiAffinityPolicy controller"),
	}

	createResponse, err := v4Client.CategoriesApiInstance.CreateCategory(category)
	if err != nil {
		return "", fmt.Errorf("failed to create category: %w", err)
	}

	if createResponse == nil || createResponse.GetData() == nil {
		return "", fmt.Errorf("unexpected empty response when creating category")
	}

	createdCategory, ok := createResponse.GetData().(*prismv4.Category)
	if !ok || createdCategory.ExtId == nil {
		return "", fmt.Errorf("failed to get ExtId from created category response")
	}

	logger.Info("Successfully created category", "key", key, "value", value, "extId", *createdCategory.ExtId)
	return *createdCategory.ExtId, nil
}

// SetupWithManager sets up the NutanixVMAntiAffinityPolicy controller with the Manager.
func (r *NutanixVMAntiAffinityPolicyReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	copts := controller.Options{
		MaxConcurrentReconciles: r.controllerConfig.MaxConcurrentReconciles,
		RateLimiter:             r.controllerConfig.RateLimiter,
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&infrav1.NutanixVMAntiAffinityPolicy{}). // Watch the controlled resource
		WithOptions(copts).
		Complete(r)
}
