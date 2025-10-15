# Writing NutanixMachine Tests

This document outlines Go testing conventions and best practices for writing NutanixMachine controller tests.

> **Note**: For test execution instructions, see [RUNNING_TESTS.md](./RUNNING_TESTS.md)

## Overview

The testing strategy for the `nutanixmachine` controller follows a comprehensive approach:

### Unit Tests
- **Purpose**: Test individual functions in isolation using mock Nutanix clients
- **Framework**: Standard Go testing with GoMock
- **Mock Clients**: Use existing generated mocks from `/mocks/nutanix/` and `/mocks/nutanixv4/`
- **Coverage**: Test every function in `v3.go` and `v4.go`, including full reconciliation steps

### Integration Tests  
- **Purpose**: Test end-to-end reconciliation flows using real Nutanix clients
- **Framework**: Standard Go testing
- **Clients**: Real Nutanix clients + fake Kubernetes clients
- **Coverage**: Test `NutanixMachineVMReadyV3` and `NutanixMachineVMReadyV4` reconciliation functions

## Test Function Naming Convention

### ✅ Required: Test Functions MUST Start with `Test*`

All Go test functions **MUST** start with `Test` followed by a descriptive name. This is a requirement of the Go testing framework.

#### Correct Function Names:
```go
func TestV3CreateValidNutanixMachineResource(t *testing.T) { }
func TestV4GPUConfiguration(t *testing.T) { }
func TestEnvironmentVariables(t *testing.T) { }
func TestActualVMCreationAndDeletion(t *testing.T) { }
```

#### ❌ Incorrect Function Names:
```go
func testV3Feature(t *testing.T) { }           // Missing capital "T"
func V3TestFeature(t *testing.T) { }           // Doesn't start with "Test"
func validateConfiguration(t *testing.T) { }   // Not a test function name
func checkMemory(t *testing.T) { }             // Not a test function name
```

### Naming Patterns by Test Type

#### Unit Tests (`v3_test.go`, `v4_test.go`)
- **Pattern**: `Test[V3|V4]<FeatureName>`
- **Framework**: Standard Go testing with GoMock
- **Mock Usage**: Required for testing individual functions with isolated dependencies
- **Examples**: 
  - `TestV3NutanixMachineVMReadyV3` - Full reconciliation with mocks
  - `TestV3FindVmV3` - VM lookup functionality
  - `TestV3GetSubnetAndPEUUIDsV3` - Network configuration lookup
  - `TestV4NutanixMachineVMReadyV4` - Full V4 reconciliation with mocks
  - `TestV4FindVmV4` - V4 VM lookup functionality
  - `TestV4GPUConfiguration` - GPU configuration testing

#### Integration Tests (`integration_test/vmready_integration_test.go`)
- **Pattern**: `Test<FeatureName>`
- **Examples**:
  - `TestEnvironmentVariables`
  - `TestNutanixPrismCentralConnection`
  - `TestInfrastructureResources`
  - `TestActualVMCreationAndDeletion`

#### Benchmark Tests (if any)
- **Pattern**: `Benchmark<FeatureName>`
- **Examples**:
  - `BenchmarkV3VMCreation`
  - `BenchmarkV4ResourceValidation`

#### Example Tests (if any)
- **Pattern**: `Example<FeatureName>`
- **Examples**:
  - `ExampleNutanixMachineCreation`

## Mock Client Usage in Unit Tests

### V3 Mock Client Setup
```go
import mocknutanixv3 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/mocks/nutanix"

func createMockV3Client(t *testing.T) (*gomock.Controller, *mocknutanixv3.MockService) {
    ctrl := gomock.NewController(t)
    mockV3Service := mocknutanixv3.NewMockService(ctrl)
    return ctrl, mockV3Service
}

func TestV3SomeFunction(t *testing.T) {
    ctrl, mockV3Service := createMockV3Client(t)
    defer ctrl.Finish()

    // Set up expectations
    mockV3Service.EXPECT().
        ListAllVM(gomock.Any(), gomock.Any()).
        Return(&prismclientv3.VMListIntentResponse{...}, nil)

    // Test your function
    reconciler := &NutanixMachineReconciler{}
    result, err := reconciler.SomeFunction(nctx, scope)
    
    // Assertions
    if err != nil {
        t.Errorf("Expected no error, got: %v", err)
    }
}
```

### V4 Mock Client Setup
```go
import mocknutanixv4 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/mocks/nutanixv4"

func createMockV4Client(t *testing.T) (*gomock.Controller, *mocknutanixv4.MockFacadeClientV4) {
    ctrl := gomock.NewController(t)
    mockV4Client := mocknutanixv4.NewMockFacadeClientV4(ctrl)
    return ctrl, mockV4Client
}

func TestV4SomeFunction(t *testing.T) {
    ctrl, mockV4Client := createMockV4Client(t)
    defer ctrl.Finish()

    // Set up expectations
    mockV4Client.EXPECT().
        ListVMs(gomock.Any()).
        Return([]vmmModels.Vm{...}, nil)

    // Test your function
    reconciler := &NutanixMachineReconciler{}
    result, err := reconciler.SomeFunction(nctx, scope)
    
    // Assertions
    if err != nil {
        t.Errorf("Expected no error, got: %v", err)
    }
}
```

## Test Structure Guidelines

### 1. Main Test Function Structure

```go
func TestFeatureName(t *testing.T) {
    // Setup (if needed)
    setupData := createTestData()
    
    // Test execution
    result, err := functionUnderTest(setupData)
    
    // Assertions
    if err != nil {
        t.Fatalf("Unexpected error: %v", err)
    }
    
    if result != expected {
        t.Errorf("Expected %v, got %v", expected, result)
    }
    
    // Cleanup (if needed)
    cleanup()
}
```

### 2. Subtest Pattern

```go
func TestMultipleScenarios(t *testing.T) {
    t.Run("Scenario1", func(t *testing.T) {
        // Test scenario 1
    })
    
    t.Run("Scenario2", func(t *testing.T) {
        // Test scenario 2
    })
}
```

### 3. Table-Driven Test Pattern

```go
func TestVariousInputs(t *testing.T) {
    testCases := []struct {
        name     string
        input    string
        expected string
        wantErr  bool
    }{
        {
            name:     "ValidInput",
            input:    "valid-input",
            expected: "expected-output",
            wantErr:  false,
        },
        {
            name:     "InvalidInput",
            input:    "invalid-input",
            expected: "",
            wantErr:  true,
        },
    }
    
    for _, tc := range testCases {
        t.Run(tc.name, func(t *testing.T) {
            result, err := processInput(tc.input)
            
            if tc.wantErr && err == nil {
                t.Error("Expected error but got none")
            }
            if !tc.wantErr && err != nil {
                t.Errorf("Unexpected error: %v", err)
            }
            if result != tc.expected {
                t.Errorf("Expected %q, got %q", tc.expected, result)
            }
        })
    }
}
```

## Test Categories and Organization

### Unit Tests
- **Location**: `v3_test.go`, `v4_test.go`
- **Purpose**: Test individual functions in isolation
- **Dependencies**: None (mocked dependencies)
- **Naming**: `Test[V3|V4]<FeatureName>`

### Integration Tests
- **Location**: `integration_test/vmready_integration_test.go`
- **Purpose**: Test interactions between components and external systems
- **Dependencies**: Real Nutanix environment
- **Naming**: `Test<FeatureName>`
- **Tags**: `//go:build integration`

### Helper Functions

Helper functions should **NOT** start with `Test` and should use descriptive names:

```go
// ✅ Correct helper function names
func createTestNutanixMachine() *infrav1.NutanixMachine { }
func setupTestEnvironment() error { }
func validateVMConfiguration(vm *VM) error { }
func cleanupTestResources() { }

// ❌ Incorrect - looks like test functions
func TestHelperFunction() { }  // Will be run as a test!
```

## Special Functions

### TestMain
Used for test setup and teardown:

```go
func TestMain(m *testing.M) {
    // Setup
    setupCode()
    
    // Run tests
    code := m.Run()
    
    // Cleanup
    cleanupCode()
    
    // Exit
    os.Exit(code)
}
```

### Package-level Test Setup
```go
func TestPackageSetup(t *testing.T) {
    // Package-level initialization tests
}
```

## Common Mistakes to Avoid

### 1. ❌ Forgetting the `Test` prefix
```go
// Wrong - will not be executed as a test
func v3CreateVM(t *testing.T) { }

// Correct
func TestV3CreateVM(t *testing.T) { }
```

### 2. ❌ Using lowercase `test`
```go
// Wrong - will not be executed as a test
func testV3CreateVM(t *testing.T) { }

// Correct
func TestV3CreateVM(t *testing.T) { }
```

### 3. ❌ Mixing test and helper functions
```go
// Wrong - helper function looks like a test
func TestValidateConfiguration() error { }  // Missing *testing.T but starts with Test

// Correct - clearly a helper function
func validateConfiguration() error { }
```

### 4. ❌ Non-descriptive names
```go
// Wrong - unclear what is being tested
func TestFunc1(t *testing.T) { }
func TestStuff(t *testing.T) { }

// Correct - clear and descriptive
func TestV3VMMemoryValidation(t *testing.T) { }
func TestV4DataDiskConfiguration(t *testing.T) { }
```

## Test Discovery and Execution

Go's test runner automatically discovers and executes functions that:

1. **Start with `Test`**
2. **Take exactly one parameter of type `*testing.T`**
3. **Are in files ending with `_test.go`**

### Running Tests

```bash
# Run all tests
go test ./...

# Run tests with specific pattern
go test -run TestV3

# Run integration tests
go test -tags=integration -run TestEnvironment

# Run with verbose output
go test -v

# Run with coverage
go test -cover
```

## Current Test Function Compliance

✅ **All current test functions are correctly named and follow Go conventions:**

### Unit Tests (v3_test.go)
- `TestV3CreateValidNutanixMachineResource`
- `TestV3UUIDBasedResourceIdentifiers`
- `TestV3MultipleSubnetConfigurations`
- `TestV3MinimumValidConfigurations`
- `TestV3MaximumReasonableConfigurations`
- `TestV3InvalidConfigurationDetection`
- `TestV3MemoryQuantityParsing`
- `TestV3MemoryQuantityComparison`
- `TestV3DiskQuantityParsing`
- `TestV3DiskQuantityComparison`
- `TestV3BootTypeConfiguration`

### Unit Tests (v4_test.go)
- `TestV4CreateValidNutanixMachineResource`
- `TestV4UUIDBasedResourceIdentifiers`
- `TestV4MultipleSubnetConfigurations`
- `TestV4MinimumValidConfigurations`
- `TestV4MaximumReasonableConfigurations`
- `TestV4InvalidConfigurationDetection`
- `TestV4MemoryQuantityParsing`
- `TestV4MemoryQuantityComparison`
- `TestV4DiskQuantityParsing`
- `TestV4DiskQuantityComparison`
- `TestV4BootTypeConfiguration`
- `TestV4GPUConfiguration`
- `TestV4DataDiskConfiguration`
- `TestV4ProjectAssignment`

### Integration Tests (integration_test/vmready_reconciliation_test.go)
- `TestV3NutanixMachineVMReadyReconciliation`
- `TestV4NutanixMachineVMReadyReconciliation`

## Summary

✅ **Current Status**: All test functions already follow proper Go testing conventions.

The test suite is well-organized with:
- Proper `Test*` function naming
- Clear API version prefixes (`TestV3*`, `TestV4*`)
- Descriptive function names
- Good separation between unit and integration tests
- Proper use of subtests and table-driven tests

No refactoring is needed for function naming compliance.
