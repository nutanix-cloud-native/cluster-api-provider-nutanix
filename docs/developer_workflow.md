# Developer workflow

This document outlines how to

## Build the source code

1. Download source code:

    ```shell
    git clone https://github.com/nutanix-cloud-native/cluster-api-provider-nutanix.git
    cd cluster-api-provider-nutanix
    ```

1. Build a container image with the source code:

    ```shell
    make docker-build
    ```

## Create a local management cluster

1. Create a [KIND](https://kind.sigs.k8s.io/) cluster:

   ```shell
    make kind-create
    ```

This will configure [kubectl](https://kubernetes.io/docs/reference/kubectl/) for the local cluster.

## Build a CAPI Image for Nutanix

1. Follow the instructions [here](https://image-builder.sigs.k8s.io/capi/providers/nutanix.html#building-capi-images-for-nutanix-cloud-platform-ncp) to build a CAPI image for Nutanix.
   The image name printed at the end of the build will be needed to create a Kubernetes cluster.

## Prepare local clusterctl

1. Make a copy of the [clusterctl](https://cluster-api.sigs.k8s.io/clusterctl/overview) configuration file:

    ```shell
    cp -n clusterctl.yaml.tmpl clusterctl.yaml
    ```

1. Update `./clusterctl.yaml` with appropriate configuration.

1. Setup `clusterctl` with the configuration:

    ```shell
    make prepare-local-clusterctl
    ```

## Deploy cluster-api-provider-nutanix provider and CRDs on local management cluster

1. Deploy the provider, along with CAPI core controllers and CRDs:

    ```shell
    make deploy
    ```

1. Verify the provider Pod is `READY`:

    ```shell
    kubectl get pods -n capx-system
    ```

## Create a test workload cluster

1. Create a workload cluster:

    ```shell
    make test-clusterctl-create
    ```

   Optionally, to use a unique cluster name:

    ```shell
    make test-clusterctl-create TEST_CLUSTER_NAME=<>
    ```

1. Get the workload cluster kubeconfig. This will write out the kubeconfig file in the local directory as `<cluster-name>.workload.kubeconfig`:

    ```shell
    make test-kubectl-workload 
    ```

   When using a unique cluster name set `TEST_CLUSTER_NAME` variable:

    ```shell
    make test-kubectl-workload TEST_CLUSTER_NAME=<>
    ```

## Debugging failures

1. Check the cluster resources:

    ```shell
    kubectl get cluster-api --namespace capx-test-ns
    ```

1. Check the provider logs:

    ```shell
    kubectl logs -n capx-system -l cluster.x-k8s.io/provider=infrastructure-nutanix
    ```

1. Check status of individual Nodes by using the address from the corresponding `NutanixMachine`:

    ```shell
    ssh capiuser@<address>
    ```

    * Check cloud-init bootstrap logs:

        ```shell
        tail /var/log/cloud-init-output.log
        ```

    * Check journalctl logs for Kubelet and Containerd
    * Check Containerd containers:

        ```shell
        crictl ps -a
        ```

## Cleanup

1. Delete the test workload cluster:

    ```shell
    make test-clusterctl-delete
    ```

   When using a unique cluster name set `TEST_CLUSTER_NAME` variable:

    ```shell
    make test-clusterctl-delete TEST_CLUSTER_NAME=<>

1. Delete the management KIND cluster:

    ```shell
    make kind-delete
    ```
