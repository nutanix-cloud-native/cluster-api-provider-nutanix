# Testing

This document describes the steps to test CAPX end-to-end.

### Requirements

TBD

### Environment variables

TBD

### Running the e2e tests

Run the following command to execute the CAPX e2e tests:

```shell
make test-e2e
```

The above command should build the CAPX manager image locally and use that image with the e2e test suite.

Running e2e with other CNIs can be done by invoking following command:
```shell
#run e2e with Calico:
make test-e2e-calico

#run e2e with Flannel:
make test-e2e-flannel

#run e2e tests with every CNI:
make test-e2e-all-cni
```
