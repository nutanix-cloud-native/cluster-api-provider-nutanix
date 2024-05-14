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

package main

import (
	"errors"
	"flag"
	"os"
	"time"

	"go.uber.org/zap/zapcore"
	"golang.org/x/time/rate"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	bootstrapv1 "sigs.k8s.io/cluster-api/bootstrap/kubeadm/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	infrav1alpha4 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1alpha4"
	infrav1beta1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	"github.com/nutanix-cloud-native/cluster-api-provider-nutanix/controllers"
	//+kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

// gitCommitHash is the git commit hash of the code that is running.
var gitCommitHash string

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(capiv1.AddToScheme(scheme))
	utilruntime.Must(bootstrapv1.AddToScheme(scheme))
	utilruntime.Must(infrav1alpha4.AddToScheme(scheme))
	utilruntime.Must(infrav1beta1.AddToScheme(scheme))

	//+kubebuilder:scaffold:scheme
}

const (
	// DefaultMaxConcurrentReconciles is the default maximum number of concurrent reconciles
	defaultMaxConcurrentReconciles = 10
)

func main() {
	var (
		metricsAddr             string
		enableLeaderElection    bool
		probeAddr               string
		maxConcurrentReconciles int
		baseDelay               time.Duration
		maxDelay                time.Duration
		bucketSize              int
		qps                     int
	)

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false, "Enable leader election for controller manager. Enabling this will ensure there is only one active controller manager.")
	flag.IntVar(&maxConcurrentReconciles, "max-concurrent-reconciles", defaultMaxConcurrentReconciles, "The maximum number of allowed, concurrent reconciles.")
	flag.DurationVar(&baseDelay, "rate-limiter-base-delay", 500*time.Millisecond, "The base delay for the rate limiter.")
	flag.DurationVar(&maxDelay, "rate-limiter-max-delay", 15*time.Minute, "The maximum delay for the rate limiter.")
	flag.IntVar(&bucketSize, "rate-limiter-bucket-size", 100, "The bucket size for the rate limiter.")
	flag.IntVar(&qps, "rate-limiter-qps", 10, "The QPS for the rate limiter.")

	opts := zap.Options{
		TimeEncoder: zapcore.RFC3339TimeEncoder,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	setupLog.Info("Initializing Nutanix Cluster API Infrastructure Provider", "Git Hash", gitCommitHash)

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		MetricsBindAddress:     metricsAddr,
		Port:                   9443,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "f265110d.cluster.x-k8s.io",
	})
	if err != nil {
		setupLog.Error(err, "unable to create manager")
		os.Exit(1)
	}

	rateLimiter, err := compositeRateLimiter(baseDelay, maxDelay, bucketSize, qps)
	if err != nil {
		setupLog.Error(err, "unable to create composite rate limiter")
		os.Exit(1)
	}

	// Set up the context that's going to be used in controllers and for the manager.
	ctx := ctrl.SetupSignalHandler()

	// Create a secret informer for the Nutanix client
	clientset, err := kubernetes.NewForConfig(mgr.GetConfig())
	if err != nil {
		setupLog.Error(err, "unable to create clientset for management cluster")
		os.Exit(1)
	}

	informerFactory := informers.NewSharedInformerFactory(clientset, time.Minute)
	secretInformer := informerFactory.Core().V1().Secrets()
	informer := secretInformer.Informer()
	go informer.Run(ctx.Done())
	cache.WaitForCacheSync(ctx.Done(), informer.HasSynced)

	configMapInformer := informerFactory.Core().V1().ConfigMaps()
	cmInformer := configMapInformer.Informer()
	go cmInformer.Run(ctx.Done())
	cache.WaitForCacheSync(ctx.Done(), cmInformer.HasSynced)

	clusterCtrl, err := controllers.NewNutanixClusterReconciler(mgr.GetClient(),
		secretInformer,
		configMapInformer,
		mgr.GetScheme(),
		controllers.WithMaxConcurrentReconciles(maxConcurrentReconciles),
		controllers.WithRateLimiter(rateLimiter),
	)
	if err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "NutanixCluster")
		os.Exit(1)
	}

	if err = clusterCtrl.SetupWithManager(ctx, mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "NutanixCluster")
		os.Exit(1)
	}
	machineCtrl, err := controllers.NewNutanixMachineReconciler(
		mgr.GetClient(),
		secretInformer,
		configMapInformer,
		mgr.GetScheme(),
		controllers.WithMaxConcurrentReconciles(maxConcurrentReconciles),
		controllers.WithRateLimiter(rateLimiter),
	)
	if err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "NutanixMachine")
		os.Exit(1)
	}
	if err = machineCtrl.SetupWithManager(ctx, mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "NutanixMachine")
		os.Exit(1)
	}
	//+kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting CAPX Controller Manager")
	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

// compositeRateLimiter will build a limiter similar to the default from DefaultControllerRateLimiter but with custom values.
func compositeRateLimiter(baseDelay, maxDelay time.Duration, bucketSize, qps int) (workqueue.RateLimiter, error) {
	// Validate the rate limiter configuration
	if err := validateRateLimiterConfig(baseDelay, maxDelay, bucketSize, qps); err != nil {
		return nil, err
	}
	exponentialBackoffLimiter := workqueue.NewItemExponentialFailureRateLimiter(baseDelay, maxDelay)
	bucketLimiter := &workqueue.BucketRateLimiter{Limiter: rate.NewLimiter(rate.Limit(qps), bucketSize)}
	return workqueue.NewMaxOfRateLimiter(exponentialBackoffLimiter, bucketLimiter), nil
}

// validateRateLimiterConfig validates the rate limiter configuration parameters
func validateRateLimiterConfig(baseDelay, maxDelay time.Duration, bucketSize, qps int) error {
	// Check if baseDelay is a non-negative value
	if baseDelay < 0 {
		return errors.New("baseDelay cannot be negative")
	}

	// Check if maxDelay is non-negative and greater than or equal to baseDelay
	if maxDelay < 0 {
		return errors.New("maxDelay cannot be negative")
	}

	if maxDelay < baseDelay {
		return errors.New("maxDelay should be greater than or equal to baseDelay")
	}

	// Check if bucketSize is a positive number
	if bucketSize <= 0 {
		return errors.New("bucketSize must be positive")
	}

	// Check if qps is a positive number
	if qps <= 0 {
		return errors.New("minimum QPS must be positive")
	}

	// Check if bucketSize is at least as large as the QPS
	if bucketSize < qps {
		return errors.New("bucketSize must be at least as large as the QPS to handle bursts effectively")
	}

	return nil
}
