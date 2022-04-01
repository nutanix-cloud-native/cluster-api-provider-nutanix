# Getting Started

This is a guide on how to get started with Cluster API Provider Nutanix Cloud Infrastructure. To learn more about cluster API in more depth, check out the [Cluster API book](https://cluster-api.sigs.k8s.io/).

## Production workflow

### Build OS image for NutanixMachineTemplate resource
To build OS image for NutanixMachineTemplate, checkout [Nutanix OS Image builder](../tools/imagebuilder/README.md)

### Configure and Install Cluster API Provider Nutanix Cloud Infrastructure
To initialize Cluster API Provider Nutanix Cloud Infrastructure, clusterctl requires following variables, which should be set in either ~/.cluster-api/clusterctl.yaml or as environment variables
<pre>
NUTANIX_ENDPOINT: ""
NUTANIX_USER: ""
NUTANIX_PASSWORD: ""

KUBERNETES_VERSION: "v1.21.0"
WORKER_MACHINE_COUNT: 3
NUTANIX_SSH_AUTHORIZED_KEY: ""

NUTANIX_CLUSTER_UUID: ""
NUTANIX_MACHINE_TEMPLATE_IMAGE_UUID: ""
</pre>

you can also see the required list of variables by running by following command
<pre>
clusterctl generate cluster mycluster -i nutanix --list-variables           
Required Variables:
  - KUBERNETES_VERSION
  - NUTANIX_CLUSTER_UUID
  - NUTANIX_MACHINE_TEMPLATE_IMAGE_UUID
  - NUTANIX_SSH_AUTHORIZED_KEY

Optional Variables:
  - CLUSTER_NAME          (defaults to mycluster)
  - NAMESPACE             (defaults to current Namespace in the KubeConfig file)
  - WORKER_MACHINE_COUNT  (defaults to 0)

</pre>

Now you can instantiate Cluster API with the following
<pre>
clusterctl init -i nutanix
</pre>

### Deploy a workload Cluser on Nutanix Cloud Infrastructure
<pre>
export TEST_CLUSTER_NAME=mytestcluster1
export TEST_NAMESPACE=mytestnamespace
clusterctl generate cluster ${TEST_CLUSTER_NAME} \
    -i nutanix \
    --target-namespace ${TEST_NAMESPACE}  \
    --kubernetes-version v1.21.1 \
    --control-plane-machine-count 1 \
    --worker-machine-count 3 > ./cluster.yaml
kubectl create ns $(TEST_NAMESPACE)
kubectl apply -f ./cluster.yaml -n $(TEST_NAMESPACE)
</pre>

### Access workload Cluster
To access resources on the cluster, you can get the kubeconfig from the secret as following
<pre>
kubectl -n ${TEST_NAMESPACE} get secret ${TEST_CLUSTER_NAME}-kubeconfig -o json | jq -r .data.value | base64 --decode > ${TEST_CLUSTER_NAME}.kubeconfig
kubectl --kubeconfig ./{TEST_CLUSTER_NAME}.kubeconfig get nodes 
</pre>

## Developer workflow

### Download source code
<pre>
git clone https://github.com/nutanix-cloud-native/cluster-api-provider-nutanix.git
cd cluster-api-provider-nutanix
</pre>

### Build source code
<pre>
make docker-build
</pre>

### Create test management cluster
<pre>
make kind-create
</pre>

### Deploy cluster-api-provider-nutanix CRDs on test management cluster
<pre>
make deploy
</pre>
### Deploy test workload cluster
Note: Update ./clusterctl.yaml with appropriate configuration before running following commands
<pre>
make prepare-local-clusterctl
make deploy-test
</pre>

### Delete test workfload cluster
<pre>
make undeploy-test
</pre>
