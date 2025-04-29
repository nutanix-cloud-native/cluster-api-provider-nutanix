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
	"time"

	credentialTypes "github.com/nutanix-cloud-native/prism-go-client/environment/credentials"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	apitypes "k8s.io/apimachinery/pkg/types"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	coreinformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	ctrlutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	nutanixclient "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/pkg/client"
	nctx "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/pkg/context"
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

	// TODO: Get NutanixPrismIdentity and establish client connection
	// TODO: Create or update anti-affinity policy in Prism
	// TODO: Update policy status with information from Prism

	// For now, just log and mark it as ready
	logger.Info("Setting NutanixVMAntiAffinityPolicy as ready", "policy", policy.Name)
	policy.Status.Ready = true

	// Example of setting a condition
	conditions.MarkTrue(policy, "Available")

	return ctrl.Result{}, nil
}

func (r *NutanixVMAntiAffinityPolicyReconciler) reconcileDelete(ctx context.Context, policy *infrav1.NutanixVMAntiAffinityPolicy) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Deleting NutanixVMAntiAffinityPolicy", "policy", policy.Name)

	// TODO: Connect to Prism and delete the anti-affinity policy if it exists

	// Remove finalizer to allow deletion
	ctrlutil.RemoveFinalizer(policy, infrav1.NutanixVMAntiAffinityPolicyFinalizer)
	
	return ctrl.Result{}, nil
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