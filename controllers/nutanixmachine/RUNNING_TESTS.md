# Running NutanixMachine Tests

This document explains how to run and set up tests for the NutanixMachine VMReady functionality using standard Go testing.

> **Note**: For test writing guidelines and conventions, see [WRITING_TESTS.md](./WRITING_TESTS.md)

## Test Strategy

### Unit Tests
- **Files**: `v3_test.go`, `v4_test.go`
- **Purpose**: Test individual functions in isolation using mock Nutanix clients
- **Framework**: Standard Go testing with GoMock for mocking
- **Mock Clients**: Use generated mocks from `/mocks/nutanix/` (V3) and `/mocks/nutanixv4/` (V4)
- **Coverage**: Every function in `v3.go` and `v4.go`, including full reconciliation steps (`NutanixMachineVMReadyV3` and `NutanixMachineVMReadyV4`)

### Integration Tests
- **Directory**: `integration_test/`
- **Files**: `vmready_reconciliation_test.go`, `README.md`
- **Purpose**: Test end-to-end reconciliation flows using real Nutanix clients and fake Kubernetes clients
- **Framework**: Standard Go testing
- **Approach**: Call actual `NutanixMachineVMReadyV3` and `NutanixMachineVMReadyV4` reconciliation functions with real Nutanix clients

## Unit Test Examples

### V3 Unit Tests
- `TestV3NutanixMachineVMReadyV3` - Full reconciliation with mock V3 client
- `TestV3FindVmV3` - VM lookup functionality 
- `TestV3GetSubnetAndPEUUIDsV3` - Network configuration lookup
- `TestV3AddBootTypeToVMV3` - Boot type configuration
- `TestV3GetSystemDiskV3` - System disk creation

### V4 Unit Tests  
- `TestV4NutanixMachineVMReadyV4` - Full reconciliation with mock V4 facade client
- `TestV4FindVmV4` - VM lookup functionality using V4 API
- `TestV4GetSubnetAndPEUUIDsV4` - Network configuration lookup
- `TestV4GPUConfiguration` - GPU configuration testing
- `TestV4CategoryManagement` - Category creation and management

## Running Tests

### Using Makefile (Recommended)

From the **project root directory**, you can use these convenient make targets:

```bash
# Run unit tests only
make unit-test

# Run integration tests only (requires environment setup - see below)
make integration-test
```

The Makefile targets automatically handle dependencies and use appropriate timeouts.

### Manual Execution

#### Unit Tests

```bash
# Run all unit tests in this package
cd controllers/nutanixmachine
go test -v .

# Run V3-specific tests only
go test -v . -run TestV3

# Run V4-specific tests only  
go test -v . -run TestV4

# Run with race detection
go test -v . -race

# Run with coverage
go test -v . -cover

# Generate coverage report
go test -v . -coverprofile=coverage.out
go tool cover -html=coverage.out
```

### Integration Tests

```bash
# Run integration tests (requires environment setup)
cd controllers/nutanixmachine/integration_test
go test -tags=integration -v .

# Run specific integration test
go test -tags=integration -v . -run TestEnvironmentVariables

# Run with timeout
go test -tags=integration -v . -timeout=30m
```

## Test Categories

### V3 Unit Tests (`v3_test.go`)
- `TestV3CreateValidNutanixMachineResource` - V3 API resource validation
- `TestV3UUIDBasedResourceIdentifiers` - V3 UUID-based identifiers
- `TestV3MultipleSubnetConfigurations` - V3 multiple subnet support
- `TestV3MinimumValidConfigurations` - V3 minimum resource requirements
- `TestV3MaximumReasonableConfigurations` - V3 maximum resource limits
- `TestV3InvalidConfigurationDetection` - V3 validation error detection
- `TestV3MemoryQuantityParsing` - V3 memory quantity parsing
- `TestV3MemoryQuantityComparison` - V3 memory quantity comparison
- `TestV3DiskQuantityParsing` - V3 disk quantity parsing
- `TestV3DiskQuantityComparison` - V3 disk quantity comparison
- `TestV3BootTypeConfiguration` - V3 boot type configuration

### V4 Unit Tests (`v4_test.go`)
- `TestV4CreateValidNutanixMachineResource` - V4 API resource validation
- `TestV4UUIDBasedResourceIdentifiers` - V4 UUID-based identifiers
- `TestV4MultipleSubnetConfigurations` - V4 multiple subnet support (enhanced)
- `TestV4MinimumValidConfigurations` - V4 minimum resource requirements
- `TestV4MaximumReasonableConfigurations` - V4 maximum resource limits (enhanced)
- `TestV4InvalidConfigurationDetection` - V4 validation error detection
- `TestV4MemoryQuantityParsing` - V4 memory quantity parsing (larger sizes)
- `TestV4MemoryQuantityComparison` - V4 memory quantity comparison
- `TestV4DiskQuantityParsing` - V4 disk quantity parsing (larger sizes)
- `TestV4DiskQuantityComparison` - V4 disk quantity comparison
- `TestV4BootTypeConfiguration` - V4 boot type configuration
- `TestV4GPUConfiguration` - V4-specific GPU configuration
- `TestV4DataDiskConfiguration` - V4-specific data disk configuration
- `TestV4ProjectAssignment` - V4-specific project assignment

### Integration Tests
- `TestEnvironmentVariables` - Environment validation
- `TestNutanixPrismCentralConnection` - API connectivity testing
- `TestInfrastructureResources` - Infrastructure resource verification
- `TestNutanixMachineResourceCreation` - Resource creation validation
- `TestV3APIConnectivityAndPermissions` - V3 API access testing
- `TestV4APIConnectivityAndPermissions` - V4 API access testing
- `TestErrorHandlingAndValidation` - Error handling validation

## Environment Setup for Integration Tests

Integration tests require Nutanix credentials and cluster information. You can provide these in two ways:

### Option 1: Environment Variables

```bash
export NUTANIX_ENDPOINT=prism-central.example.com
export NUTANIX_USER=admin
export NUTANIX_PASSWORD=your-password-here
export NUTANIX_PORT=9440  # Optional, defaults to 9440
export NUTANIX_INSECURE=false  # Optional, set to true to skip SSL verification
export NUTANIX_PRISM_ELEMENT_CLUSTER_NAME=your-pe-cluster-name
export NUTANIX_SUBNET_NAME=your-subnet-name
export NUTANIX_MACHINE_TEMPLATE_IMAGE_NAME=your-machine-image-name
```

### Option 2: .env File (Recommended for Local Development)

```bash
# Copy the example file and edit with your values
cp integration_test/env.example .env
# Edit .env with your actual credentials
```

**Environment Variable Precedence:**
- If any required environment variables are already set, the `.env` file will be ignored
- If no environment variables are set, the integration test will automatically look for and load a `.env` file from:
  - Current directory (`.env`)
  - Parent directory (`../.env`) 
  - Project root (`../../.env`, `../../../.env`)
  - Home directory (`~/.nutanix.env`)

**Note:** The `.env` file is already included in `.gitignore` to prevent accidental credential commits.

## Test Philosophy

### Unit Tests
- **Focus**: Resource validation and configuration testing using standard Go testing
- **Scope**: API-specific functionality differences
- **Dependencies**: None (no external dependencies)
- **Execution**: Fast execution suitable for CI/CD
- **Framework**: Standard Go `testing` package with `t.Run()` for subtests

### Integration Tests  
- **Focus**: Actual connectivity to Nutanix infrastructure
- **Scope**: Environment configuration and API access validation
- **Dependencies**: Requires live Nutanix environment
- **Execution**: Longer execution time with network calls
- **Safety**: Non-destructive testing (no VM creation)
- **Framework**: Standard Go `testing` package with `TestMain` for setup

## Test Structure and Patterns

### Unit Test Pattern
```go
func TestFeatureName(t *testing.T) {
    // Setup test data
    testData := createTestData()
    
    // Execute test logic
    result := functionUnderTest(testData)
    
    // Validate results
    if result != expectedValue {
        t.Errorf("Expected %v, got %v", expectedValue, result)
    }
}
```

### Integration Test Pattern
```go
func TestIntegrationFeature(t *testing.T) {
    t.Run("SpecificAspect", func(t *testing.T) {
        // Test specific aspect
        result, err := apiCall()
        if err != nil {
            t.Fatalf("Unexpected error: %v", err)
        }
        // Validate result
    })
}
```

### Subtest Pattern
```go
func TestMultipleScenarios(t *testing.T) {
    testCases := []struct {
        name     string
        input    string
        expected string
    }{
        {"scenario1", "input1", "output1"},
        {"scenario2", "input2", "output2"},
    }
    
    for _, tc := range testCases {
        t.Run(tc.name, func(t *testing.T) {
            result := processInput(tc.input)
            if result != tc.expected {
                t.Errorf("Expected %s, got %s", tc.expected, result)
            }
        })
    }
}
```

## Running Specific Tests

### Unit Tests
```bash
# Run all V3 tests
go test -v . -run TestV3

# Run all V4 tests
go test -v . -run TestV4

# Run specific test function
go test -v . -run TestV3CreateValidNutanixMachineResource

# Run tests with specific pattern
go test -v . -run "TestV4.*Configuration"
```

### Integration Tests
```bash
# Run all integration tests
go test -tags=integration -v .

# Run environment tests only
go test -tags=integration -v . -run TestEnvironment

# Run API connectivity tests
go test -tags=integration -v . -run ".*API.*"

# Run resource creation tests
go test -tags=integration -v . -run TestNutanixMachineResourceCreation
```

## Relationship to E2E Tests

These tests complement the full e2e test suite:

- **Unit Tests**: Fast, isolated validation of resource specifications
- **Integration Tests**: Environment and connectivity validation
- **E2E Tests**: Full lifecycle testing including actual VM creation

The separation allows for:
- **Development Feedback**: Quick feedback during development (unit tests)
- **Environment Validation**: Verify environment setup (integration tests)  
- **System Validation**: Full system validation (e2e tests)

## Benefits of Standard Go Testing

### Advantages over Ginkgo/Gomega:
- **Simplicity**: No additional framework dependencies
- **Performance**: Faster execution without framework overhead
- **Tooling**: Better integration with standard Go tooling
- **Debugging**: Easier debugging with standard Go debugger
- **CI/CD**: Simpler CI/CD integration
- **Learning Curve**: Lower learning curve for Go developers

### Standard Features Used:
- `testing.T` for test functions
- `t.Run()` for subtests and parallel execution
- `t.Error()` and `t.Errorf()` for test failures
- `t.Fatal()` and `t.Fatalf()` for critical failures
- `t.Skip()` for conditional test skipping
- `TestMain()` for test setup and teardown
- Build tags (`//go:build integration`) for test categorization

## Adding New Tests

### For V3-specific functionality:
- Add test functions to `v3_test.go`
- Use prefix `TestV3` for function names
- Follow existing patterns and naming conventions
- Focus on V3 API specific behaviors

### For V4-specific functionality:
- Add test functions to `v4_test.go`
- Use prefix `TestV4` for function names
- Test V4 enhanced features and capabilities
- Include V4-specific validation logic

### For cross-API functionality:
- Add test functions to `integration_test/vmready_integration_test.go`
- Test environment and connectivity aspects
- Ensure tests remain safe and non-destructive
- Use subtests for different aspects of functionality

### Test Function Naming:
- Unit tests: `Test[V3|V4]<FeatureName>`
- Integration tests: `Test<FeatureName>`
- Subtests: Use descriptive names with `t.Run("SubtestName", func(t *testing.T) {...})`

## Coverage and Quality

### Running Coverage:
```bash
# Unit test coverage
cd controllers/nutanixmachine
go test -v . -cover

# Integration test coverage
cd controllers/nutanixmachine/integration_test
go test -tags=integration -v . -cover

# Generate detailed coverage report
go test -v . -coverprofile=coverage.out
go tool cover -html=coverage.out -o coverage.html
```

### Test Quality Guidelines:
- **Clear naming**: Test function names should clearly indicate what is being tested
- **Focused scope**: Each test should test one specific behavior
- **Error messages**: Use descriptive error messages with expected vs actual values
- **Setup/teardown**: Use `TestMain` for integration test setup
- **Subtests**: Use subtests for related test scenarios
- **Documentation**: Include comments for complex test logic