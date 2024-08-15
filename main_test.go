package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd/api"
	ctrlconfig "sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"github.com/nutanix-cloud-native/cluster-api-provider-nutanix/controllers"
	mockctlclient "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/mocks/ctlclient"
	mockmeta "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/mocks/k8sapimachinery"
	mockk8sclient "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/mocks/k8sclient"
)

func TestParseFlags(t *testing.T) {
	config := &managerConfig{}
	os.Args = []string{"cmd", "--leader-elect=true", "--max-concurrent-reconciles=5", "--diagnostics-address=:8081", "--insecure-diagnostics=true"}
	parseFlags(config)

	assert.Equal(t, true, config.enableLeaderElection)
	assert.Equal(t, 5, config.concurrentReconcilesNutanixCluster)
	assert.Equal(t, 5, config.concurrentReconcilesNutanixMachine)
	assert.Equal(t, ":8081", config.diagnosticsOptions.DiagnosticsAddress)
	assert.Equal(t, true, config.diagnosticsOptions.InsecureDiagnostics)
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
		probeAddr:                          ":8081",
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
	client := mockctlclient.NewMockClient(ctrl)
	cache := mockctlclient.NewMockCache(ctrl)
	mgr.EXPECT().GetCache().Return(cache).AnyTimes()
	mgr.EXPECT().GetClient().Return(client).AnyTimes()
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
