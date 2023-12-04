# Developer workflow

## Download source code
<pre>
git clone https://github.com/nutanix-cloud-native/cluster-api-provider-nutanix.git
cd cluster-api-provider-nutanix
</pre>

## Build source code
<pre>
make build-snapshot
</pre>

## Deploy cluster-api-provider-nutanix CRDs on test management cluster
<pre>
make dev.run-on-kind
</pre>

## Deploy test workload cluster
Note: Update ./clusterctl.yaml with appropriate configuration before running following commands
<pre>
make test.clusterctl-create
</pre>

## Get test workload cluster kubeconfig
<pre>
make test.kubectl-workload
</pre>

## Delete test workload cluster
<pre>
make test.clusterctl-delete
</pre>