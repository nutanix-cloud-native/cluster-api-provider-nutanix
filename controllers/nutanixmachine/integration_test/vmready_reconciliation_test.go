//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/joho/godotenv"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/patch"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	"github.com/nutanix-cloud-native/cluster-api-provider-nutanix/controllers"
	"github.com/nutanix-cloud-native/cluster-api-provider-nutanix/controllers/nutanixmachine"
	prismGoClient "github.com/nutanix-cloud-native/prism-go-client"
	"github.com/nutanix-cloud-native/prism-go-client/facade"
	facadeV4 "github.com/nutanix-cloud-native/prism-go-client/facade/v4"
	"github.com/nutanix-cloud-native/prism-go-client/utils"
	prismGoClientV3 "github.com/nutanix-cloud-native/prism-go-client/v3"
)

var (
	ctx        context.Context
	testConfig *IntegrationTestConfig
	reconciler *nutanixmachine.NutanixMachineReconciler

	// Real Nutanix clients for integration testing
	v3Client *prismGoClientV3.Client
	v4Client facade.FacadeClientV4
)

// IntegrationTestConfig holds configuration for integration tests
type IntegrationTestConfig struct {
	NutanixEndpoint string
	NutanixUser     string
	NutanixPassword string
	NutanixPort     string
	NutanixInsecure bool
	PEClusterName   string
	SubnetName      string
	ImageName       string
}

func TestMain(m *testing.M) {
	var err error
	ctx = context.Background()

	// Setup
	err = setupIntegrationTests()
	if err != nil {
		fmt.Printf("Failed to setup integration tests: %v\n", err)
		os.Exit(1)
	}

	// Run tests
	code := m.Run()

	// Cleanup
	teardownIntegrationTests()

	os.Exit(code)
}

func setupIntegrationTests() error {
	var err error

	// Setup logging for controller-runtime
	log.SetLogger(zap.New(zap.UseDevMode(true), zap.WriteTo(os.Stdout)))

	// Load .env file if it exists and no environment variables are set
	err = loadEnvFileIfNeeded()
	if err != nil {
		return fmt.Errorf("failed to load environment configuration: %v", err)
	}

	// Validate environment variables are set
	err = ValidateRequiredEnvironmentVariables()
	if err != nil {
		return fmt.Errorf("required environment variables must be set: %v", err)
	}

	// Get test configuration from environment
	testConfig = &IntegrationTestConfig{
		NutanixEndpoint: os.Getenv("NUTANIX_ENDPOINT"),
		NutanixUser:     os.Getenv("NUTANIX_USER"),
		NutanixPassword: os.Getenv("NUTANIX_PASSWORD"),
		NutanixPort:     getEnvOrDefault("NUTANIX_PORT", "9440"),
		PEClusterName:   os.Getenv("NUTANIX_PRISM_ELEMENT_CLUSTER_NAME"),
		SubnetName:      os.Getenv("NUTANIX_SUBNET_NAME"),
		ImageName:       os.Getenv("NUTANIX_MACHINE_TEMPLATE_IMAGE_NAME"),
	}

	if insecure := os.Getenv("NUTANIX_INSECURE"); insecure == "true" {
		testConfig.NutanixInsecure = true
	}

	// Initialize real V3 client
	v3Client, err = initNutanixV3Client(testConfig)
	if err != nil {
		return fmt.Errorf("failed to initialize Nutanix V3 client: %v", err)
	}

	// Initialize real V4 client
	v4Client, err = initNutanixV4Client(testConfig)
	if err != nil {
		return fmt.Errorf("failed to initialize Nutanix V4 client: %v", err)
	}

	// Setup fake k8s client for testing
	scheme := runtime.NewScheme()
	_ = infrav1.AddToScheme(scheme)
	_ = clusterv1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	// Create reconciler with real clients
	reconciler = &nutanixmachine.NutanixMachineReconciler{
		Client: fakeClient,
		Scheme: scheme,
	}

	return nil
}

func TestV3NutanixMachineVMReadyReconciliation(t *testing.T) {
	t.Run("V3ReconcileWithNameBasedIdentifiers", func(t *testing.T) {
		vmName := "test-machine-v3-names-a1b2c3d4"

		// Cleanup any existing VM with this name before starting
		cleanupVMAfterTest(t, vmName)

		// Setup cleanup to run after test completes
		defer func() {
			cleanupVMAfterTest(t, vmName)
		}()

		// Create test resources
		cluster, machine, nutanixMachine, nutanixCluster := createTestResourcesV3("v3-names")

		// Override the machine name to match our test VM name
		machine.Name = vmName

		// Create k8s resources
		createKubernetesResources(t, cluster, machine, nutanixMachine)

		// Create patch helper for the NutanixMachine
		patchHelper, err := patch.NewHelper(nutanixMachine, reconciler.Client)
		if err != nil {
			t.Fatalf("Failed to create patch helper: %v", err)
		}

		// Create NutanixExtendedContext with real clients
		nctx := &controllers.NutanixExtendedContext{
			ExtendedContext: controllers.ExtendedContext{
				Context:     ctx,
				Client:      reconciler.Client,
				PatchHelper: patchHelper,
			},
			NutanixClients: &controllers.NutanixClients{
				V3Client: v3Client,
				V4Facade: v4Client,
			},
		}

		// Create scope
		scope := nutanixmachine.NewNutanixMachineScope(nutanixCluster, nutanixMachine, cluster, machine)

		// Test V3 VMReady reconciliation
		t.Logf("Testing V3 VMReady reconciliation with name-based identifiers")
		result, err := reconciler.NutanixMachineVMReadyV3(nctx, scope)

		if err != nil {
			t.Logf("V3 reconciliation error (expected if infrastructure doesn't exist): %v", err)
		} else {
			t.Logf("V3 reconciliation completed successfully: %+v", result)
		}

		// Assert that error is nil
		if err != nil {
			t.Fatalf("V3 reconciliation error: %v", err)
		}

		// Assert that result.Result.Requeue is false
		if result.Result.Requeue {
			t.Fatalf("V3 reconciliation result.Result.Requeue is true")
		}
	})

	t.Run("V3ReconcileWithMinimumConfiguration", func(t *testing.T) {
		vmName := "test-machine-v3-minimum-e5f6g7h8"

		// Cleanup any existing VM with this name before starting
		cleanupVMAfterTest(t, vmName)

		// Setup cleanup to run after test completes
		defer func() {
			cleanupVMAfterTest(t, vmName)
		}()

		// Create test resources with minimum configuration
		cluster, machine, nutanixMachine, nutanixCluster := createTestResourcesV3("v3-minimum")

		// Override the machine name to match our test VM name
		machine.Name = vmName

		// Set minimum configuration
		nutanixMachine.Spec.VCPUSockets = 1
		nutanixMachine.Spec.VCPUsPerSocket = 1
		nutanixMachine.Spec.MemorySize = resource.MustParse("1Gi")
		nutanixMachine.Spec.SystemDiskSize = resource.MustParse("20Gi")

		// Create k8s resources
		createKubernetesResources(t, cluster, machine, nutanixMachine)

		// Create patch helper for the NutanixMachine
		patchHelper, err := patch.NewHelper(nutanixMachine, reconciler.Client)
		if err != nil {
			t.Fatalf("Failed to create patch helper: %v", err)
		}

		// Create NutanixExtendedContext with real clients
		nctx := &controllers.NutanixExtendedContext{
			ExtendedContext: controllers.ExtendedContext{
				Context:     ctx,
				Client:      reconciler.Client,
				PatchHelper: patchHelper,
			},
			NutanixClients: &controllers.NutanixClients{
				V3Client: v3Client,
				V4Facade: v4Client,
			},
		}

		// Create scope
		scope := nutanixmachine.NewNutanixMachineScope(nutanixCluster, nutanixMachine, cluster, machine)

		// Test V3 VMReady reconciliation
		t.Logf("Testing V3 VMReady reconciliation with minimum configuration")
		result, err := reconciler.NutanixMachineVMReadyV3(nctx, scope)

		if err != nil {
			t.Logf("V3 reconciliation error: %v", err)
		} else {
			t.Logf("V3 reconciliation completed successfully: %+v", result)
		}

		// Assert that error is not nil
		if err == nil {
			t.Fatalf("V3 reconciliation error is nil")
		}

	})
}

func TestV4NutanixMachineVMReadyReconciliation(t *testing.T) {
	if v4Client == nil {
		t.Skip("V4 client not available, skipping V4 tests")
	}

	t.Run("V4ReconcileWithBasicConfiguration", func(t *testing.T) {
		vmName := "test-machine-v4-basic-i9j0k1l2"

		// Cleanup any existing VM with this name before starting
		cleanupVMAfterTest(t, vmName)

		// Setup cleanup to run after test completes
		defer func() {
			cleanupVMAfterTest(t, vmName)
		}()

		// Create test resources
		cluster, machine, nutanixMachine, nutanixCluster := createTestResourcesV4("v4-basic")

		// Override the machine name to match our test VM name
		machine.Name = vmName

		// Create k8s resources
		createKubernetesResources(t, cluster, machine, nutanixMachine)

		// Create patch helper for the NutanixMachine
		patchHelper, err := patch.NewHelper(nutanixMachine, reconciler.Client)
		if err != nil {
			t.Fatalf("Failed to create patch helper: %v", err)
		}

		// Create NutanixExtendedContext with real clients
		nctx := &controllers.NutanixExtendedContext{
			ExtendedContext: controllers.ExtendedContext{
				Context:     ctx,
				Client:      reconciler.Client,
				PatchHelper: patchHelper,
			},
			NutanixClients: &controllers.NutanixClients{
				V3Client: v3Client,
				V4Facade: v4Client,
			},
		}

		// Create scope
		scope := nutanixmachine.NewNutanixMachineScope(nutanixCluster, nutanixMachine, cluster, machine)

		// Test V4 VMReady reconciliation
		t.Logf("Testing V4 VMReady reconciliation with basic configuration")
		result, err := reconciler.NutanixMachineVMReadyV4(nctx, scope)

		if err != nil {
			t.Logf("V4 reconciliation error: %v", err)
		} else {
			t.Logf("V4 reconciliation completed successfully: %+v", result)
		}

		// Assert that error is nil
		if err != nil {
			t.Fatalf("V4 reconciliation error: %v", err)
		}

		// Assert that result.Result.Requeue is false
		if result.Result.Requeue {
			t.Fatalf("V4 reconciliation result.Result.Requeue is true")
		}
	})

	t.Run("V4ReconcileWithGPUConfiguration", func(t *testing.T) {
		vmName := "test-machine-v4-gpu-m3n4o5p6"

		// Cleanup any existing VM with this name before starting
		cleanupVMAfterTest(t, vmName)

		// Setup cleanup to run after test completes
		defer func() {
			cleanupVMAfterTest(t, vmName)
		}()

		// Create test resources with GPU configuration
		cluster, machine, nutanixMachine, nutanixCluster := createTestResourcesV4("v4-gpu")

		// Override the machine name to match our test VM name
		machine.Name = vmName

		// Add GPU configuration (V4 specific feature)
		nutanixMachine.Spec.GPUs = []infrav1.NutanixGPU{
			{
				Type:     "PASSTHROUGH",
				Name:     utils.StringPtr("Ampere 40"),
				DeviceID: utils.Int64Ptr(8757),
			},
		}

		// Create k8s resources
		createKubernetesResources(t, cluster, machine, nutanixMachine)

		// Create patch helper for the NutanixMachine
		patchHelper, err := patch.NewHelper(nutanixMachine, reconciler.Client)
		if err != nil {
			t.Fatalf("Failed to create patch helper: %v", err)
		}

		// Create NutanixExtendedContext with real clients
		nctx := &controllers.NutanixExtendedContext{
			ExtendedContext: controllers.ExtendedContext{
				Context:     ctx,
				Client:      reconciler.Client,
				PatchHelper: patchHelper,
			},
			NutanixClients: &controllers.NutanixClients{
				V3Client: v3Client,
				V4Facade: v4Client,
			},
		}

		// Create scope
		scope := nutanixmachine.NewNutanixMachineScope(nutanixCluster, nutanixMachine, cluster, machine)

		// Test V4 VMReady reconciliation
		t.Logf("Testing V4 VMReady reconciliation with GPU configuration")
		result, err := reconciler.NutanixMachineVMReadyV4(nctx, scope)

		if err != nil {
			t.Logf("V4 reconciliation error: %v", err)
		} else {
			t.Logf("V4 reconciliation completed successfully: %+v", result)
		}

		// Assert that error is nil
		if err != nil {
			t.Fatalf("V4 reconciliation error: %v", err)
		}

		// Assert that result.Result.Requeue is false
		if result.Result.Requeue {
			t.Fatalf("V4 reconciliation result.Result.Requeue is true")
		}
	})
}

// Helper functions

func createTestResourcesV3(suffix string) (*clusterv1.Cluster, *clusterv1.Machine, *infrav1.NutanixMachine, *infrav1.NutanixCluster) {
	cluster := &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("test-cluster-%s", suffix),
			Namespace: "default",
		},
	}

	machine := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("test-machine-%s", suffix),
			Namespace: "default",
			Labels: map[string]string{
				clusterv1.ClusterNameLabel: cluster.Name,
			},
		},
		Spec: clusterv1.MachineSpec{
			ClusterName: cluster.Name,
			Bootstrap: clusterv1.Bootstrap{
				DataSecretName: utils.StringPtr("test-bootstrap-secret"),
			},
		},
	}

	nutanixMachine := &infrav1.NutanixMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("test-nutanix-machine-%s", suffix),
			Namespace: "default",
		},
		Spec: infrav1.NutanixMachineSpec{
			VCPUSockets:    2,
			VCPUsPerSocket: 1,
			MemorySize:     resource.MustParse("2Gi"),
			SystemDiskSize: resource.MustParse("20Gi"),
			Image: &infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierName,
				Name: utils.StringPtr(testConfig.ImageName),
			},
			Cluster: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierName,
				Name: utils.StringPtr(testConfig.PEClusterName),
			},
			Subnets: []infrav1.NutanixResourceIdentifier{
				{
					Type: infrav1.NutanixIdentifierName,
					Name: utils.StringPtr(testConfig.SubnetName),
				},
			},
		},
	}

	nutanixCluster := &infrav1.NutanixCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("test-nutanix-cluster-%s", suffix),
			Namespace: "default",
		},
		Spec: infrav1.NutanixClusterSpec{
			// Minimal spec for testing - the actual Prism Central configuration
			// is handled by the real clients initialized separately
		},
	}

	return cluster, machine, nutanixMachine, nutanixCluster
}

func createTestResourcesV4(suffix string) (*clusterv1.Cluster, *clusterv1.Machine, *infrav1.NutanixMachine, *infrav1.NutanixCluster) {
	cluster, machine, nutanixMachine, nutanixCluster := createTestResourcesV3(suffix)

	// V4 may support larger configurations
	nutanixMachine.Spec.MemorySize = resource.MustParse("4Gi")
	nutanixMachine.Spec.SystemDiskSize = resource.MustParse("40Gi")

	return cluster, machine, nutanixMachine, nutanixCluster
}

func createKubernetesResources(t *testing.T, cluster *clusterv1.Cluster, machine *clusterv1.Machine, nutanixMachine *infrav1.NutanixMachine) {
	err := reconciler.Client.Create(ctx, cluster)
	if err != nil {
		t.Fatalf("Failed to create cluster: %v", err)
	}

	err = reconciler.Client.Create(ctx, machine)
	if err != nil {
		t.Fatalf("Failed to create machine: %v", err)
	}

	err = reconciler.Client.Create(ctx, nutanixMachine)
	if err != nil {
		t.Fatalf("Failed to create nutanix machine: %v", err)
	}
}

// loadEnvFileIfNeeded loads environment variables from .env file if it exists
// and no required environment variables are already set
func loadEnvFileIfNeeded() error {
	// Check if any required environment variables are already set
	requiredVars := []string{
		"NUTANIX_ENDPOINT",
		"NUTANIX_USER",
		"NUTANIX_PASSWORD",
		"NUTANIX_PRISM_ELEMENT_CLUSTER_NAME",
		"NUTANIX_SUBNET_NAME",
		"NUTANIX_MACHINE_TEMPLATE_IMAGE_NAME",
	}

	// Count how many env vars are already set
	setCount := 0
	for _, envVar := range requiredVars {
		if os.Getenv(envVar) != "" {
			setCount++
		}
	}

	// If some environment variables are already set, don't load .env file
	if setCount > 0 {
		fmt.Printf("Found %d/%d environment variables already set, skipping .env file loading\n", setCount, len(requiredVars))
		return nil
	}

	// Look for .env file in multiple locations
	possibleEnvFiles := []string{
		".env",          // Current directory
		"../.env",       // Parent directory
		"../../.env",    // Project root (likely)
		"../../../.env", // Project root alternative
		filepath.Join(os.Getenv("HOME"), ".nutanix.env"), // Home directory
	}

	var envFile string
	for _, file := range possibleEnvFiles {
		if _, err := os.Stat(file); err == nil {
			envFile = file
			break
		}
	}

	if envFile == "" {
		fmt.Println("No .env file found and no environment variables set")
		return nil
	}

	fmt.Printf("Loading environment variables from: %s\n", envFile)
	err := godotenv.Load(envFile)
	if err != nil {
		return fmt.Errorf("error loading .env file %s: %v", envFile, err)
	}

	fmt.Println("Successfully loaded environment variables from .env file")
	return nil
}

func ValidateRequiredEnvironmentVariables() error {
	requiredVars := []string{
		"NUTANIX_ENDPOINT",
		"NUTANIX_USER",
		"NUTANIX_PASSWORD",
		"NUTANIX_PRISM_ELEMENT_CLUSTER_NAME",
		"NUTANIX_SUBNET_NAME",
		"NUTANIX_MACHINE_TEMPLATE_IMAGE_NAME",
	}

	for _, envVar := range requiredVars {
		if os.Getenv(envVar) == "" {
			return fmt.Errorf("required environment variable %s is not set", envVar)
		}
	}

	return nil
}

func initNutanixV3Client(config *IntegrationTestConfig) (*prismGoClientV3.Client, error) {
	cred := prismGoClient.Credentials{
		URL:      fmt.Sprintf("%s:%s", config.NutanixEndpoint, config.NutanixPort),
		Endpoint: config.NutanixEndpoint,
		Username: config.NutanixUser,
		Password: config.NutanixPassword,
		Port:     config.NutanixPort,
		Insecure: config.NutanixInsecure,
	}

	return prismGoClientV3.NewV3Client(cred)
}

func initNutanixV4Client(config *IntegrationTestConfig) (facade.FacadeClientV4, error) {
	cred := prismGoClient.Credentials{
		URL:      fmt.Sprintf("%s:%s", config.NutanixEndpoint, config.NutanixPort),
		Endpoint: config.NutanixEndpoint,
		Username: config.NutanixUser,
		Password: config.NutanixPassword,
		Port:     config.NutanixPort,
		Insecure: config.NutanixInsecure,
	}

	return facadeV4.NewFacadeV4Client(cred)
}

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// teardownIntegrationTests performs cleanup after all tests are done
func teardownIntegrationTests() {
	fmt.Println("Starting integration test cleanup...")

	// Clean up any test VMs that may have been created
	if v3Client != nil {
		cleanupTestVMs()
	}

	fmt.Println("Integration test cleanup completed")
}

// cleanupTestVMs removes any VMs created during testing
func cleanupTestVMs() {
	// List of test VM names to clean up
	testVMNames := []string{
		"test-machine-v3-names-a1b2c3d4",
		"test-machine-v3-minimum-e5f6g7h8",
		"test-machine-v4-basic-i9j0k1l2",
		"test-machine-v4-gpu-m3n4o5p6",
	}

	for _, vmName := range testVMNames {
		err := deleteVMByName(vmName)
		if err != nil {
			fmt.Printf("Warning: Failed to cleanup test VM %s: %v\n", vmName, err)
		} else {
			fmt.Printf("Successfully cleaned up test VM: %s\n", vmName)
		}
	}
}

// deleteVMByName deletes a VM by name using V3 API with V4 fallback
func deleteVMByName(vmName string) error {
	// Try V3 API first
	if v3Client != nil {
		err := deleteVMByNameV3(vmName)
		if err == nil {
			return nil
		}
		fmt.Printf("V3 deletion failed for VM %s: %v, trying V4 API\n", vmName, err)
	}

	// Fallback to V4 API
	if v4Client != nil {
		return deleteVMByNameV4(vmName)
	}

	return fmt.Errorf("both V3 and V4 clients are unavailable")
}

// deleteVMByNameV3 deletes a VM by name using V3 API
func deleteVMByNameV3(vmName string) error {
	// List all VMs (avoiding buggy FIQL filters) and filter by name programmatically
	vmListResponse, err := v3Client.V3.ListAllVM(ctx, "")
	if err != nil {
		return fmt.Errorf("failed to list VMs: %v", err)
	}

	if vmListResponse == nil || len(vmListResponse.Entities) == 0 {
		fmt.Printf("VM %s not found via V3 API (already deleted or never created)\n", vmName)
		return nil
	}

	// Filter by name programmatically
	for _, vm := range vmListResponse.Entities {
		if vm.Spec != nil && vm.Spec.Name != nil && *vm.Spec.Name == vmName {
			if vm.Metadata == nil || vm.Metadata.UUID == nil {
				continue
			}

			vmUUID := *vm.Metadata.UUID
			fmt.Printf("Deleting test VM via V3 API: %s (UUID: %s)\n", vmName, vmUUID)

			// Delete the VM
			_, err := v3Client.V3.DeleteVM(ctx, vmUUID)
			if err != nil {
				return fmt.Errorf("failed to delete VM %s (UUID: %s): %v", vmName, vmUUID, err)
			}

			fmt.Printf("Successfully initiated deletion via V3 API of VM: %s\n", vmName)
			return nil
		}
	}

	fmt.Printf("VM %s not found via V3 API (already deleted or never created)\n", vmName)
	return nil
}

// deleteVMByNameV4 deletes a VM by name using V4 API
func deleteVMByNameV4(vmName string) error {
	// Use V4 API with filter to search for VM by name
	filter := fmt.Sprintf("name eq '%s'", vmName)

	vms, err := v4Client.ListVMs(facade.WithFilter(filter))
	if err != nil {
		return fmt.Errorf("failed to list VMs via V4 API with filter: %v", err)
	}

	if len(vms) == 0 {
		fmt.Printf("VM %s not found via V4 API (already deleted or never created)\n", vmName)
		return nil
	}

	// Should find exactly one VM with the filter
	for _, vm := range vms {
		if vm.ExtId == nil {
			continue
		}

		vmUUID := *vm.ExtId
		fmt.Printf("Deleting test VM via V4 API: %s (UUID: %s)\n", vmName, vmUUID)

		// Delete the VM
		taskWaiter, err := v4Client.DeleteVM(vmUUID)
		if err != nil {
			return fmt.Errorf("failed to delete VM %s (UUID: %s) via V4 API: %v", vmName, vmUUID, err)
		}

		_, err = taskWaiter.WaitForTaskCompletion()
		if err != nil {
			return fmt.Errorf("failed to wait for task completion: %v", err)
		}

		fmt.Printf("Successfully initiated deletion via V4 API of VM: %s\n", vmName)
		return nil
	}

	fmt.Printf("VM %s not found via V4 API (already deleted or never created)\n", vmName)
	return nil
}

// cleanupVMAfterTest is a helper function to clean up a specific VM after a test
func cleanupVMAfterTest(t *testing.T, vmName string) {
	t.Helper()

	err := deleteVMByName(vmName)
	if err != nil {
		t.Logf("Warning: Failed to cleanup VM %s after test: %v", vmName, err)
	} else {
		t.Logf("Successfully cleaned up VM %s after test", vmName)
	}
}
