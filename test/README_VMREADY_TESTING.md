# NutanixMachine VMReady Testing Guide

This document provides comprehensive guidance for testing NutanixMachine VMReady functionality across different test approaches.

## Testing Approaches

### 1. E2E Tests (Ginkgo/Gomega)
**Location**: `test/e2e/`  
**Framework**: Ginkgo/Gomega  
**Purpose**: Full lifecycle testing including actual VM creation  
**Command**: `make test-e2e GINKGO_FOCUS="VMReady"`

### 2. Integration Tests (Standard Go)
**Location**: `controllers/nutanixmachine/integration_test/`  
**Framework**: Standard Go testing  
**Purpose**: Reconciliation logic testing with real Nutanix clients  
**Command**: `go test -tags=integration -v .`

### 3. Unit Tests (Standard Go)
**Location**: `controllers/nutanixmachine/`  
**Framework**: Standard Go testing with mocks  
**Purpose**: Individual function testing in isolation  
**Command**: `go test -v .`

## Prerequisites

### Required Environment Variables
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
export CONTROL_PLANE_ENDPOINT_IP="your-control-plane-ip"
```

## Running Tests

### E2E Tests (Full Lifecycle)
```bash
# All VMReady tests
make test-e2e GINKGO_FOCUS="VMReady"

# V3 API specific
make test-e2e GINKGO_FOCUS="V3 API Tests"

# V4 API specific  
make test-e2e GINKGO_FOCUS="V4 API Tests"

# Environment validation only
make test-e2e GINKGO_FOCUS="Environment and Configuration Tests"
```

### Integration Tests (Reconciliation Logic)
```bash
cd controllers/nutanixmachine/integration_test

# All reconciliation tests
go test -tags=integration -v .

# V3 reconciliation only
go test -tags=integration -v . -run TestV3NutanixMachineVMReadyReconciliation

# V4 reconciliation only
go test -tags=integration -v . -run TestV4NutanixMachineVMReadyReconciliation
```

### Unit Tests (Isolated Functions)
```bash
cd controllers/nutanixmachine

# All unit tests
go test -v .

# V3 tests only
go test -v . -run TestV3

# V4 tests only
go test -v . -run TestV4
```

## Test Categories

### What Each Test Type Covers

#### E2E Tests
- ✅ Full VM lifecycle (create, configure, delete)
- ✅ Real infrastructure interaction
- ✅ End-to-end cluster creation
- ⚠️ **Resource Impact**: Creates actual VMs

#### Integration Tests  
- ✅ Reconciliation logic validation
- ✅ Real Nutanix client connectivity
- ✅ Error handling scenarios
- ✅ **Safe**: No VM creation

#### Unit Tests
- ✅ Individual function validation
- ✅ Resource specification testing  
- ✅ Configuration validation
- ✅ **Fast**: No external dependencies

## Test Safety

### Non-Destructive Tests
- Integration tests: Test connectivity and logic without creating resources
- Unit tests: Use mocks and local validation only

### Resource-Creating Tests
- E2E tests: Create actual VMs - use dedicated test environments
- Some VM creation tests may be marked `Skip()` for safety

## Quick Reference

| Test Type | Command | Duration | Safety | Purpose |
|-----------|---------|----------|--------|---------|
| Unit | `go test -v .` | ~1 min | ✅ Safe | Function validation |
| Integration | `go test -tags=integration -v .` | ~5 min | ✅ Safe | Logic + connectivity |
| E2E | `make test-e2e GINKGO_FOCUS="VMReady"` | ~30 min | ⚠️ Creates VMs | Full lifecycle |

## Troubleshooting

### Environment Issues
```bash
# Check environment variables
env | grep NUTANIX

# Test connectivity
curl -k https://$NUTANIX_ENDPOINT:$NUTANIX_PORT/api/nutanix/v3/clusters
```

### Common Solutions
- **Missing env vars**: Set all required NUTANIX_* variables
- **Connection failed**: Check endpoint, credentials, network access
- **Resource not found**: Expected for some integration tests - verifies error handling
- **VM creation timeout**: Check cluster capacity, increase timeout values

For detailed testing instructions, see:
- **E2E Testing**: Use `make test-e2e` commands for full lifecycle testing
- **Integration Testing**: See `controllers/nutanixmachine/integration_test/README.md` for reconciliation testing
- **Unit Testing**: See `controllers/nutanixmachine/README_TESTS.md` for detailed unit test guidance
