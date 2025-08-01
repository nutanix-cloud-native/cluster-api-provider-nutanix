package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/cluster-api/util/flags"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlconfig "sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	"github.com/nutanix-cloud-native/cluster-api-provider-nutanix/controllers"
	mockctlclient "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/mocks/ctlclient"
	mockmeta "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/mocks/k8sapimachinery"
	mockk8sclient "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/mocks/k8sclient"
)

func TestInitializeFlags(t *testing.T) {
	tt := []struct {
		name   string
		args   []string
		want   *options
		cmpOpt cmp.Option
	}{
		{
			name: "our own flags",
			args: []string{
				"cmd",
				"--leader-elect=true",
				"--max-concurrent-reconciles=5",
				"--health-probe-bind-address=:8081",
				"--rate-limiter-base-delay=500ms",
				"--rate-limiter-max-delay=10s",
				"--rate-limiter-bucket-size=1000",
				"--rate-limiter-qps=50",
			},
			want: &options{
				enableLeaderElection:    true,
				maxConcurrentReconciles: 5,
				healthProbeAddr:         ":8081",
				rateLimiterBaseDelay:    500 * time.Millisecond,
				rateLimiterMaxDelay:     10 * time.Second,
				rateLimiterBucketSize:   1000,
				rateLimiterQPS:          50,
			},
			cmpOpt: cmpopts.IgnoreFields(options{},
				"managerOptions",
				"zapOptions",
			),
		},
		{
			name: "Cluster API flags",
			args: []string{
				"cmd",
				"--diagnostics-address=:9999",
				"--insecure-diagnostics=true",
			},
			want: &options{
				managerOptions: flags.ManagerOptions{
					DiagnosticsAddress:  ":9999",
					InsecureDiagnostics: true,
				},
			},
			cmpOpt: cmpopts.IgnoreFields(options{},
				"enableLeaderElection",
				"maxConcurrentReconciles",
				"healthProbeAddr",
				"rateLimiterBaseDelay",
				"rateLimiterMaxDelay",
				"rateLimiterBucketSize",
				"rateLimiterQPS",

				// Controller-runtime defaults these values,
				// so we ignore them.
				"managerOptions.TLSMinVersion",
				"managerOptions.TLSCipherSuites",
			),
		},
		// Unfortunately, we cannot test parsing of the single controller-runtime flag,
		// --kubeconfig, because its values is not exported. However, we do effectively test parsing
		// by testing manager initialization; that creates loads a kubeconfig specified by the flag.
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			os.Args = tc.args

			// Clear flags initialized by any other test.
			flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
			pflag.CommandLine = pflag.NewFlagSet(os.Args[0], pflag.ExitOnError)

			got := initializeFlags()
			if diff := cmp.Diff(tc.want, got, cmp.AllowUnexported(options{}), tc.cmpOpt); diff != "" {
				t.Errorf("MakeGatewayInfo() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestInitializeConfig(t *testing.T) {
	tt := []struct {
		name    string
		args    []string
		want    managerConfig
		wantErr bool
	}{
		{
			name: "pass with misc. options",
			args: []string{
				"cmd",
				// Our options.
				"--leader-elect=true",
				"--max-concurrent-reconciles=5",
				"--health-probe-bind-address=:8081",
				"--rate-limiter-base-delay=500ms",
				"--rate-limiter-max-delay=10s",
				"--rate-limiter-bucket-size=1000",
				"--rate-limiter-qps=50",
				// Cluster API options.
				"--insecure-diagnostics=true",
				"--diagnostics-address=:9999",
				"--insecure-diagnostics=false",
				// Controller-runtime options.
				"--kubeconfig=testdata/kubeconfig",
			},
			want:    managerConfig{},
			wantErr: false,
		},
		{
			name: "fail with missing kubeconfig",
			args: []string{
				"cmd",
				"--kubeconfig=notfound",
			},
			wantErr: true,
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			os.Args = tc.args

			// Clear flags initialized by any other test.
			flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
			pflag.CommandLine = pflag.NewFlagSet(os.Args[0], pflag.ExitOnError)

			opts := initializeFlags()

			got, err := initializeConfig(opts)
			if tc.wantErr {
				if err == nil {
					t.Errorf("unexpected error: %s", err)
				}
				return
			}

			assert.Equal(t, got.enableLeaderElection, opts.enableLeaderElection)
			assert.Equal(t, got.healthProbeAddr, opts.healthProbeAddr)
			assert.Equal(t, got.concurrentReconcilesNutanixCluster, opts.maxConcurrentReconciles)
			assert.Equal(t, got.concurrentReconcilesNutanixMachine, opts.maxConcurrentReconciles)
			assert.Equal(t, got.metricsServerOpts.BindAddress, opts.managerOptions.DiagnosticsAddress)

			assert.NotNil(t, got.rateLimiter)
			assert.True(t, got.logger.Enabled())
			assert.Equal(t, got.restConfig.Host, "https://example.com:6443")
		})
	}
}

func TestSetupLogger(t *testing.T) {
	logger := setupLogger()
	assert.NotNil(t, logger)
}

func TestInitializeManagerWithNilRestConfig(t *testing.T) {
	config := &managerConfig{
		restConfig: nil,
	}

	_, err := initializeManager(config)
	assert.Error(t, err)
}

func TestInitializeManager(t *testing.T) {
	testEnv := &envtest.Environment{}
	cfg, err := testEnv.Start()
	require.NoError(t, err)
	defer func() {
		err := testEnv.Stop()
		require.NoError(t, err)
	}()

	config := &managerConfig{
		enableLeaderElection:               false,
		healthProbeAddr:                    ":8081",
		concurrentReconcilesNutanixCluster: 1,
		concurrentReconcilesNutanixMachine: 1,
		logger:                             setupLogger(),
		restConfig:                         cfg,
	}

	mgr, err := initializeManager(config)
	assert.NoError(t, err)
	assert.NotNil(t, mgr)
}

func TestAddHealthChecks(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mgr := mockctlclient.NewMockManager(ctrl)
	mgr.EXPECT().AddHealthzCheck("healthz", gomock.Any()).Return(nil)
	mgr.EXPECT().AddReadyzCheck("readyz", gomock.Any()).Return(nil)

	err := addHealthChecks(mgr)
	assert.NoError(t, err)

	mgr = mockctlclient.NewMockManager(ctrl)
	mgr.EXPECT().AddHealthzCheck("healthz", gomock.Any()).Return(fmt.Errorf("error"))
	err = addHealthChecks(mgr)
	assert.Error(t, err)

	mgr = mockctlclient.NewMockManager(ctrl)
	mgr.EXPECT().AddHealthzCheck("healthz", gomock.Any()).Return(nil)
	mgr.EXPECT().AddReadyzCheck("readyz", gomock.Any()).Return(fmt.Errorf("error"))
	err = addHealthChecks(mgr)
	assert.Error(t, err)
}

func TestRunManagerCreateInformerFailure(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	failingRestConfig := &rest.Config{
		ExecProvider: &api.ExecConfig{},
		AuthProvider: &api.AuthProviderConfig{},
	}

	mgr := mockctlclient.NewMockManager(ctrl)
	mgr.EXPECT().GetConfig().Return(failingRestConfig)
	err := runManager(context.Background(), mgr, nil)
	assert.Error(t, err)
}

func testRunManagerCommon(t *testing.T, ctrl *gomock.Controller) (*mockctlclient.MockManager, *managerConfig, *envtest.Environment) {
	mgr := mockctlclient.NewMockManager(ctrl)

	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
		Scheme:                scheme,
	}
	cfg, err := testEnv.Start()

	require.NoError(t, err)

	rateLimiter, err := compositeRateLimiter(500*time.Millisecond, 15*time.Minute, 100, 10)
	require.NoError(t, err)
	config := &managerConfig{
		enableLeaderElection:               false,
		logger:                             setupLogger(),
		concurrentReconcilesNutanixCluster: 1,
		concurrentReconcilesNutanixMachine: 1,
		restConfig:                         cfg,
		rateLimiter:                        rateLimiter,
		skipNameValidation:                 true, // Enable for tests to allow duplicate controller names
	}

	restScope := mockmeta.NewMockRESTScope(ctrl)
	restScope.EXPECT().Name().Return(meta.RESTScopeNameNamespace).AnyTimes()

	restMapper := mockmeta.NewMockRESTMapper(ctrl)
	restMapper.EXPECT().RESTMapping(gomock.Any()).Return(&meta.RESTMapping{Scope: restScope}, nil).AnyTimes()

	client := mockctlclient.NewMockClient(ctrl)
	client.EXPECT().Scheme().Return(scheme).AnyTimes()
	client.EXPECT().RESTMapper().Return(restMapper).AnyTimes()

	cache := mockctlclient.NewMockCache(ctrl)

	mgr.EXPECT().GetConfig().Return(cfg).AnyTimes()
	mgr.EXPECT().GetClient().Return(client).AnyTimes()
	mgr.EXPECT().GetScheme().Return(scheme).AnyTimes()
	mgr.EXPECT().GetLogger().Return(config.logger).AnyTimes()
	mgr.EXPECT().GetControllerOptions().Return(ctrlconfig.Controller{
		MaxConcurrentReconciles: config.concurrentReconcilesNutanixCluster,
		NeedLeaderElection:      &config.enableLeaderElection,
	}).AnyTimes()
	mgr.EXPECT().Add(gomock.Any()).Return(nil).AnyTimes()
	mgr.EXPECT().GetCache().Return(cache).AnyTimes()

	return mgr, config, testEnv
}

func TestRunManagerStartFailure(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mgr, config, testenv := testRunManagerCommon(t, ctrl)
	defer func() {
		_ = testenv.Stop()
	}()

	mgr.EXPECT().Start(gomock.Any()).Return(fmt.Errorf("error"))
	err := runManager(context.Background(), mgr, config)
	assert.Error(t, err)
}

func TestRunManagerInvalidConfig(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mgr, config, testenv := testRunManagerCommon(t, ctrl)
	defer func() {
		_ = testenv.Stop()
	}()

	config.concurrentReconcilesNutanixCluster = 0

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := runManager(ctx, mgr, config)
	assert.Error(t, err)

	config.concurrentReconcilesNutanixCluster = 1
	config.concurrentReconcilesNutanixMachine = 0
	err = runManager(ctx, mgr, config)
	assert.Error(t, err)
}

func TestRunManager(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mgr, config, testenv := testRunManagerCommon(t, ctrl)
	defer func() {
		_ = testenv.Stop()
	}()

	mgr.EXPECT().Start(gomock.Any()).Return(nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := runManager(ctx, mgr, config)
	assert.NoError(t, err)
}

func TestSetupControllersInvalidOpt(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mgr := mockctlclient.NewMockManager(ctrl)
	client := mockctlclient.NewMockClient(ctrl)
	mgr.EXPECT().GetClient().Return(client).AnyTimes()
	mgr.EXPECT().GetScheme().Return(scheme).AnyTimes()
	secretInformer := mockk8sclient.NewMockSecretInformer(ctrl)
	configMapInformer := mockk8sclient.NewMockConfigMapInformer(ctrl)

	invalidOpts := []controllers.ControllerConfigOpts{
		controllers.WithMaxConcurrentReconciles(0),
	}
	err := setupNutanixClusterController(context.Background(), mgr, secretInformer, configMapInformer, invalidOpts...)
	assert.Error(t, err)
	err = setupNutanixMachineController(context.Background(), mgr, secretInformer, configMapInformer, invalidOpts...)
	assert.Error(t, err)
}

func TestSetupControllersFailedAddToManager(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mgr := mockctlclient.NewMockManager(ctrl)
	cache := mockctlclient.NewMockCache(ctrl)

	restScope := mockmeta.NewMockRESTScope(ctrl)
	restScope.EXPECT().Name().Return(meta.RESTScopeNameNamespace).AnyTimes()

	restMapper := mockmeta.NewMockRESTMapper(ctrl)
	restMapper.EXPECT().RESTMapping(gomock.Any()).Return(&meta.RESTMapping{Scope: restScope}, nil).AnyTimes()

	client := mockctlclient.NewMockClient(ctrl)
	client.EXPECT().RESTMapper().Return(restMapper).AnyTimes()

	mgr.EXPECT().GetClient().Return(client).AnyTimes()
	mgr.EXPECT().GetCache().Return(cache).AnyTimes()
	mgr.EXPECT().GetScheme().Return(scheme).AnyTimes()
	mgr.EXPECT().GetLogger().Return(setupLogger()).AnyTimes()
	mgr.EXPECT().GetControllerOptions().Return(ctrlconfig.Controller{
		MaxConcurrentReconciles: 1,
	}).AnyTimes()
	mgr.EXPECT().Add(gomock.Any()).Return(fmt.Errorf("error")).AnyTimes()
	secretInformer := mockk8sclient.NewMockSecretInformer(ctrl)
	configMapInformer := mockk8sclient.NewMockConfigMapInformer(ctrl)

	copts := []controllers.ControllerConfigOpts{
		controllers.WithMaxConcurrentReconciles(1),
	}
	err := setupNutanixClusterController(context.Background(), mgr, secretInformer, configMapInformer, copts...)
	assert.Error(t, err)
	err = setupNutanixMachineController(context.Background(), mgr, secretInformer, configMapInformer, copts...)
	assert.Error(t, err)
}

func TestRateLimiter(t *testing.T) {
	tests := []struct {
		name        string
		baseDelay   time.Duration
		maxDelay    time.Duration
		maxBurst    int
		qps         int
		expectedErr string
	}{
		{
			name:      "valid rate limiter",
			baseDelay: 500 * time.Millisecond,
			maxDelay:  15 * time.Minute,
			maxBurst:  100,
			qps:       10,
		},
		{
			name:        "negative base delay",
			baseDelay:   -500 * time.Millisecond,
			maxDelay:    15 * time.Minute,
			maxBurst:    100,
			qps:         10,
			expectedErr: "baseDelay cannot be negative",
		},
		{
			name:        "negative max delay",
			baseDelay:   500 * time.Millisecond,
			maxDelay:    -15 * time.Minute,
			maxBurst:    100,
			qps:         10,
			expectedErr: "maxDelay cannot be negative",
		},
		{
			name:        "maxDelay should be greater than or equal to baseDelay",
			baseDelay:   500 * time.Millisecond,
			maxDelay:    400 * time.Millisecond,
			maxBurst:    100,
			qps:         10,
			expectedErr: "maxDelay should be greater than or equal to baseDelay",
		},
		{
			name:        "bucketSize must be positive",
			baseDelay:   500 * time.Millisecond,
			maxDelay:    15 * time.Minute,
			maxBurst:    0,
			qps:         10,
			expectedErr: "bucketSize must be positive",
		},
		{
			name:        "qps must be positive",
			baseDelay:   500 * time.Millisecond,
			maxDelay:    15 * time.Minute,
			maxBurst:    100,
			qps:         0,
			expectedErr: "minimum QPS must be positive",
		},
		{
			name:        "bucketSize must be greater than or equal to qps",
			baseDelay:   500 * time.Millisecond,
			maxDelay:    15 * time.Minute,
			maxBurst:    10,
			qps:         100,
			expectedErr: "bucketSize must be at least as large as the QPS to handle bursts effectively",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := compositeRateLimiter(tt.baseDelay, tt.maxDelay, tt.maxBurst, tt.qps)
			if tt.expectedErr != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestKubebuilderValidations(t *testing.T) {
	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}

	cfg, err := testEnv.Start()
	assert.NoError(t, err)
	assert.NotNil(t, cfg)

	defer func() {
		err := testEnv.Stop()
		require.NoError(t, err)
	}()

	err = infrav1.AddToScheme(scheme)
	assert.NoError(t, err)

	err = infrav1.AddToScheme(scheme)
	assert.NoError(t, err)

	k8sClient, err := client.New(cfg, client.Options{Scheme: scheme})
	assert.NoError(t, err)
	assert.NotNil(t, k8sClient)

	cases := []struct {
		testName        string
		machineTemplate infrav1.NutanixMachineTemplate
		wantErr         bool
		errTextContains string
	}{
		{
			"machine template with image",
			infrav1.NutanixMachineTemplate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "machinetemplatewithimage",
					Namespace: metav1.NamespaceDefault,
				},
				Spec: infrav1.NutanixMachineTemplateSpec{
					Template: infrav1.NutanixMachineTemplateResource{
						Spec: infrav1.NutanixMachineSpec{
							VCPUsPerSocket: 1,
							VCPUSockets:    1,
							MemorySize:     resource.Quantity{},
							Cluster: infrav1.NutanixResourceIdentifier{
								Type: infrav1.NutanixIdentifierUUID,
								UUID: ptr.To("550e8400-e29b-41d4-a716-446655440000"),
							},
							Image: &infrav1.NutanixResourceIdentifier{
								Type: infrav1.NutanixIdentifierName,
								Name: ptr.To("rockylinux-9-v1.31.4"),
							},
							SystemDiskSize: resource.Quantity{},
						},
					},
				},
			},
			false,
			"",
		},
		{
			"machine template with image lookup",
			infrav1.NutanixMachineTemplate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "machinetemplatewithimagelookup",
					Namespace: metav1.NamespaceDefault,
				},
				Spec: infrav1.NutanixMachineTemplateSpec{
					Template: infrav1.NutanixMachineTemplateResource{
						Spec: infrav1.NutanixMachineSpec{
							VCPUsPerSocket: 1,
							VCPUSockets:    1,
							Cluster: infrav1.NutanixResourceIdentifier{
								Type: infrav1.NutanixIdentifierUUID,
								UUID: ptr.To("550e8400-e29b-41d4-a716-446655440001"),
							},
							MemorySize: resource.Quantity{},
							ImageLookup: &infrav1.NutanixImageLookup{
								BaseOS: "rockylinux-9",
							},
							SystemDiskSize: resource.Quantity{},
						},
					},
				},
			},
			false,
			"",
		},
		{
			"machine template without lookup or image",
			infrav1.NutanixMachineTemplate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "machinetemplatenolookuporimage",
					Namespace: metav1.NamespaceDefault,
				},
				Spec: infrav1.NutanixMachineTemplateSpec{
					Template: infrav1.NutanixMachineTemplateResource{
						Spec: infrav1.NutanixMachineSpec{
							VCPUsPerSocket: 1,
							VCPUSockets:    1,
							Cluster: infrav1.NutanixResourceIdentifier{
								Type: infrav1.NutanixIdentifierUUID,
								UUID: ptr.To("550e8400-e29b-41d4-a716-446655440002"),
							},
							MemorySize:     resource.Quantity{},
							SystemDiskSize: resource.Quantity{},
						},
					},
				},
			},
			true,
			"Either 'image' or 'imageLookup' must be set, but not both",
		},
		{
			"machine template with both lookup or image",
			infrav1.NutanixMachineTemplate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "machinetemplateimageandlookup",
					Namespace: metav1.NamespaceDefault,
				},
				Spec: infrav1.NutanixMachineTemplateSpec{
					Template: infrav1.NutanixMachineTemplateResource{
						Spec: infrav1.NutanixMachineSpec{
							VCPUsPerSocket: 1,
							Cluster: infrav1.NutanixResourceIdentifier{
								Type: infrav1.NutanixIdentifierUUID,
								UUID: ptr.To("550e8400-e29b-41d4-a716-446655440003"),
							},
							VCPUSockets: 1,
							ImageLookup: &infrav1.NutanixImageLookup{
								BaseOS: "rockylinux-9",
							},
							Image: &infrav1.NutanixResourceIdentifier{
								Type: infrav1.NutanixIdentifierName,
								Name: ptr.To("rockylinux-9-v1.31.4"),
							},
							MemorySize:     resource.Quantity{},
							SystemDiskSize: resource.Quantity{},
						},
					},
				},
			},
			true,
			"Either 'image' or 'imageLookup' must be set, but not both",
		},
	}
	for _, tt := range cases {
		t.Run(tt.testName, func(t *testing.T) {
			err = k8sClient.Create(context.Background(), &tt.machineTemplate)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errTextContains)
			} else {
				assert.NoError(t, err)
			}
			//nolint:errcheck // this is just for house keeping and doesn't actually do anything with testing the functionality.
			k8sClient.Delete(context.Background(), &tt.machineTemplate)
		})
	}
}
