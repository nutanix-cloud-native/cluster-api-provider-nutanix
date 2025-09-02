# NutanixMachine VMReady Reconciliation Integration Tests

This directory contains integration tests for the NutanixMachine VMReady reconciliation functions using real Nutanix clients.

> **Note**: For comprehensive testing guidance across all approaches, see [VMReady Testing Guide](../../../test/README_VMREADY_TESTING.md)

## Overview

These tests focus on testing the actual reconciliation logic of the `NutanixMachineVMReadyV3` and `NutanixMachineVMReadyV4` functions with real Nutanix API clients. The tests verify that the reconciliation functions correctly:

- Process NutanixMachine resources
- Handle different API versions (V3 and V4)
- Validate configurations
- Interact with real Nutanix Prism Central

## Test Structure

### Main Test Files
- `vmready_reconciliation_test.go` - Main integration tests for VMReady reconciliation functions

### Test Categories

#### V3 Reconciliation Tests (`TestV3NutanixMachineVMReadyReconciliation`)
- `V3ReconcileWithNameBasedIdentifiers` - Tests V3 reconciliation with name-based resource identifiers
- `V3ReconcileWithMinimumConfiguration` - Tests V3 reconciliation with minimum resource configuration

#### V4 Reconciliation Tests (`TestV4NutanixMachineVMReadyReconciliation`)  
- `V4ReconcileWithBasicConfiguration` - Tests V4 reconciliation with basic configuration
- `V4ReconcileWithGPUConfiguration` - Tests V4 reconciliation with GPU configuration (V4 specific)
- `V4ReconcileWithDataDisks` - Tests V4 reconciliation with data disk configuration (V4 enhanced)
- `V4ReconcileWithGPUConfiguration` - Tests V4 reconciliation with project assignment (V4 specific)

## Prerequisites

### Required Environment Variables
Set these environment variables before running the tests:

```bash
export NUTANIX_ENDPOINT="your-prism-central-ip"
export NUTANIX_USER="your-username"  
export NUTANIX_PASSWORD="your-password"
export NUTANIX_PRISM_ELEMENT_CLUSTER_NAME="your-pe-cluster"
export NUTANIX_SUBNET_NAME="your-subnet-name"
export NUTANIX_MACHINE_TEMPLATE_IMAGE_NAME="your-image-name"

# Optional
export NUTANIX_PORT="9440"
export NUTANIX_INSECURE="true"
```

### Infrastructure Requirements
- Active Nutanix Prism Central environment
- At least one Prism Element cluster
- Network subnet configured
- VM image available
- Valid credentials with permissions for VM operations

## Running Integration Tests

### Run All Integration Tests

```bash
# From the integration_test directory
cd controllers/nutanixmachine/integration_test

# Run all integration tests
go test -tags=integration -v .

# Run with timeout for longer operations
go test -tags=integration -v . -timeout=30m
```

### Run Specific Test Categories

```bash
# Run only V3 reconciliation tests
go test -tags=integration -v . -run TestV3NutanixMachineVMReadyReconciliation

# Run only V4 reconciliation tests
go test -tags=integration -v . -run TestV4NutanixMachineVMReadyReconciliation

# Run specific test scenario
go test -tags=integration -v . -run TestV3.*NameBasedIdentifiers
```

### Run with Race Detection

```bash
go test -tags=integration -v . -race
```

## What These Tests Do

### Non-Destructive Testing
These integration tests are **non-destructive** and focus on testing the reconciliation logic rather than actual VM creation:

1. **Client Connectivity**: Verify real V3/V4 Nutanix client initialization
2. **Resource Validation**: Test that reconciliation functions validate NutanixMachine specs correctly
3. **API Interaction**: Verify that reconciliation functions interact with Nutanix APIs appropriately
4. **Error Handling**: Test graceful handling of missing infrastructure or configuration errors
5. **Logic Flow**: Verify the reconciliation logic flows correctly through different scenarios

### Expected Outcomes
- Tests may fail if the specified infrastructure (clusters, subnets, images) doesn't exist - this is expected
- The goal is to verify that the reconciliation functions handle these scenarios gracefully
- Tests should complete without panics or crashes
- Reconciliation should attempt to process resources and handle errors appropriately

## Test Architecture

### Fake Kubernetes Client
- Uses `controller-runtime/pkg/client/fake` for Kubernetes API operations
- Creates test Cluster, Machine, and NutanixMachine resources in memory
- No actual Kubernetes cluster required

### Real Nutanix Clients
- Uses real `prism-go-client/v3.Client` for V3 API testing
- Uses real `facade.FacadeClientV4` for V4 API testing (when available)
- Connects to actual Nutanix Prism Central specified in environment variables

### Test Scope Creation
- Creates proper `NutanixExtendedContext` with real clients and fake k8s client
- Creates `NutanixMachineScope` with test resources
- Calls actual reconciliation functions (`NutanixMachineVMReadyV3`, `NutanixMachineVMReadyV4`)

## Debugging Tests

### Verbose Output
```bash
go test -tags=integration -v . -test.v=true
```

### Environment Issues
```bash
# Test only environment setup
go test -tags=integration -v . -run TestMain
```

### Specific API Version Issues  
```bash
# Test only V3 functionality
go test -tags=integration -v . -run TestV3

# Test only V4 functionality (if available)
go test -tags=integration -v . -run TestV4
```

## Common Issues and Solutions

### Environment Variables Not Set
**Error**: `required environment variable NUTANIX_ENDPOINT is not set`
**Solution**: Set all required environment variables as listed above

### Client Connection Failures
**Error**: Failed to initialize Nutanix client
**Solution**: 
- Verify Nutanix endpoint is reachable
- Check credentials are correct
- Ensure firewall allows connection on specified port

### Infrastructure Not Found
**Error**: Failed to find cluster/subnet/image
**Solution**: This is expected behavior - tests verify graceful error handling

### V4 Client Initialization Success
**Behavior**: V4 tests run when V4 client initializes successfully
**Note**: V4 client uses the same credentials as V3 but with facade interface

## Relationship to Other Tests

These integration tests complement:

- **Unit Tests** (`../v3_test.go`, `../v4_test.go`): Fast, isolated validation of resource specifications
- **Controller Tests**: Full controller lifecycle testing
- **E2E Tests**: Complete cluster lifecycle with actual VM creation

The integration tests bridge the gap by:
- Testing reconciliation functions with real API clients
- Validating API connectivity and authentication
- Verifying reconciliation logic without infrastructure changes
- Providing confidence in reconciliation behavior before E2E testing

## Adding New Tests

### For New V3 Reconciliation Scenarios
Add test functions to `TestV3NutanixMachineVMReadyReconciliation`:
```go
t.Run("V3NewScenario", func(t *testing.T) {
    // Create test resources with specific configuration
    cluster, machine, nutanixMachine := createTestResourcesV3("v3-new-scenario")
    
    // Modify nutanixMachine for specific test case
    nutanixMachine.Spec.SomeField = someValue
    
    // Run reconciliation test
    // ... test implementation
})
```

### For New V4 Reconciliation Scenarios
Add test functions to `TestV4NutanixMachineVMReadyReconciliation`:
```go
t.Run("V4NewFeature", func(t *testing.T) {
    // Create test resources with V4-specific configuration
    cluster, machine, nutanixMachine := createTestResourcesV4("v4-new-feature")
    
    // Add V4-specific fields
    nutanixMachine.Spec.V4Feature = v4Value
    
    // Run reconciliation test
    // ... test implementation
})
```

## Safety Considerations

### Non-Destructive by Design
- Tests do not create, modify, or delete actual VMs
- Tests do not modify Nutanix infrastructure
- Tests only call reconciliation functions to verify logic flow
- Safe to run against production Nutanix environments (with appropriate caution)

### Data Protection
- Tests use fake Kubernetes client to avoid affecting real clusters
- Environment variables should be set to test/development Nutanix environment when possible
- No persistent state changes in Nutanix infrastructure

## Benefits

1. **Reconciliation Logic Validation**: Tests actual reconciliation function behavior
2. **API Client Integration**: Verifies real Nutanix client integration
3. **Error Handling**: Tests graceful handling of various failure scenarios
4. **Configuration Validation**: Tests different NutanixMachine configurations
5. **Version Compatibility**: Tests both V3 and V4 API paths
6. **Development Confidence**: Provides confidence in reconciliation logic before E2E testing