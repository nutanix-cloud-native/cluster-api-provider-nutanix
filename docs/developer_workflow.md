# Developer workflow

## Download source code
<pre>
git clone https://github.com/nutanix-cloud-native/cluster-api-provider-nutanix.git
cd cluster-api-provider-nutanix
</pre>

## Build source code
<pre>
make docker-build
</pre>

## Create test management cluster
<pre>
make kind-create
</pre>

## Prepare local clusterctl
<pre>
cp -n clusterctl.yaml.tmpl clusterctl.yaml
</pre>
**Note**: Update `./clusterctl.yaml` with appropriate configuration

<pre>
make prepare-local-clusterctl
</pre>

## Deploy cluster-api-provider-nutanix CRDs on test management cluster
<pre>
make deploy
</pre>

## Deploy test workload cluster
<pre>
make test-clusterctl-create
</pre>
**Note**: Optionally, use a unique cluster name
<pre>
make test-clusterctl-create TEST_CLUSTER_NAME=<>
</pre>

## Get test workload cluster kubeconfig
<pre>
make test-kubectl-workload 
</pre>
**Note**: Optionally, use the same unique cluster name
<pre>
make test-kubectl-workload TEST_CLUSTER_NAME=<>
</pre>

## Delete test workload cluster
<pre>
make test-clusterctl-delete
</pre>
**Note**: Optionally, use the same unique cluster name
<pre>
make test-clusterctl-delete TEST_CLUSTER_NAME=<>
</pre>