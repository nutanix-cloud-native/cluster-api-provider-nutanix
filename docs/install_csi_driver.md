# Nutanix CSI Driver installation with CAPX

The Nutanix CSI driver is fully supported on CAPI/CAPX deployed clusters where all the nodes meet the [Nutanix CSI driver prerequisites](#capi-workload-cluster-prerequisites-for-nutanix-csi-driver).

There are two methods to install the Nutanix CSI driver on a CAPI/CAPX cluster:

- Helm
- ClusterResourceSet

For more information, check the next sections.



## CAPI Workload cluster prerequisites for Nutanix CSI Driver

Kubernetes workers need the following prerequisite to use the Nutanix CSI Drivers:

- iSCSI initiator package (for Volumes based block storage)
- NFS client package ( for Files based storage)

These packages can already be present in the image you use with your Infrastructure provider or you can also rely on your bootstrap provider to install them. More info are available in the [Prerequisites docs](https://portal.nutanix.com/page/documents/details?targetId=CSI-Volume-Driver-v2_5:csi-csi-plugin-prerequisites-r.html).

The packages name and installation method will also vary depending on the OS you plan to use.

In the example below, we will see how to use the `kubeadm` bootstrap provider to deploy these packages on top of an Ubuntu 20.04 image.

The `kubeadm` bootstrap provider allows defining `preKubeadmCommands` that will be launched before kubernetes cluster creation.

These `preKubeadmCommands` can be defined both in `KubeadmControlPlane` for master nodes and in `KubeadmConfigTemplate` for worker nodes.

In our case with an Ubuntu 20.04 image, you need to modify both `KubeadmControlPlane` and `KubeadmConfigTemplate` as in the example below in order to install the prerequisites on all nodes.

```yaml
spec:
  template:
    spec:
      # .......
      preKubeadmCommands:
      - echo "before kubeadm call" > /var/log/prekubeadm.log
      - apt update
      - apt install -y nfs-common open-iscsi
      - systemctl enable --now iscsid
```



## Install Nutanix CSI Driver with Helm

The following procedure needs to be applied on a ready workload cluster.
You can retrieve your workload cluster's kubeconfig and connect with the following command:

```shell
clusterctl get kubeconfig $CLUSTER_NAME -n $CLUSTER_NAMESPACE > $CLUSTER_NAME-KUBECONFIG
export KUBECONFIG=$(pwd)/$CLUSTER_NAME-KUBECONFIG
```

Once connected to your cluster, you just need to follow the [official documentation](https://portal.nutanix.com/page/documents/details?targetId=CSI-Volume-Driver-v2_5:csi-csi-driver-install-t.html).

First you need to install the [nutanix-csi-snapshot](https://github.com/nutanix/helm/tree/master/charts/nutanix-csi-snapshot) chart and next the [nutanix-csi-storage](https://github.com/nutanix/helm/tree/master/charts/nutanix-csi-storage) chart.

A quick summary of the procedure will be:

```shell
#Add the official Nutanix Helm repo and get the latest update
helm repo add nutanix https://nutanix.github.io/helm/
helm repo update

# Install the nutanix-csi-snapshot chart
helm install nutanix-csi-snapshot nutanix/nutanix-csi-snapshot -n ntnx-system --create-namespace

# Install the nutanix-csi-storage chart
helm install nutanix-storage nutanix/nutanix-csi-storage -n ntnx-system 
```

*WARNING*: To allow the Nutanix CSI driver to deploy correctly, you must have a fully functional CNI deployment.



## Install Nutanix CSI Driver with ClusterResourceSet

The `ClusterResourceSet` feature was introduced to provide a way to automatically apply a set of resources (such as CNI/CSI) defined by administrators to matching newly created/existing workload clusters.

### Enabling ClusterResourceSet feature

For now `ClusterResourceSet` is an experimental feature that needs to be enabled during the initialization of your management cluster with the `EXP_CLUSTER_RESOURCE_SET` feature gate.

To do this you can add `EXP_CLUSTER_RESOURCE_SET: "true"` in your `clusterctl`configuration file or just `export EXP_CLUSTER_RESOURCE_SET=true` before initializing the management cluster with `clusterctl init`.

If your management cluster is already initialized, you can also enable the `ClusterResourceSet` by changing configuration of the  `capi-controller-manager` deployment in the `capi-system` namespace.

```shell
kubectl edit deployment -n capi-system capi-controller-manager
```

Just search the following part

```yaml
  - args:
    - --leader-elect
    - --metrics-bind-addr=localhost:8080
    - --feature-gates=MachinePool=false,ClusterResourceSet=true,ClusterTopology=false
```

and replace `ClusterResourceSet=false` by `ClusterResourceSet=true`

Editing the `deployment` resource will cause Kubernetes to automatically start new versions of the containers with the feature enabled.



### Prepare Nutanix CSI ClusterResourceSet

#### Create the ConfigMap for the CNI Plugin

The first step is to create a `ConfigMap` that contains a YAML manifest with all resources to install the Nutanix CSI driver.

As the Nutanix CSI is provided as Helm Charts, we will use `helm` to extract it before creating the `ConfigMap`.

```shell
helm repo add nutanix https://nutanix.github.io/helm/
helm repo update

kubectl create ns ntnx-system --dry-run=client -o yaml > nutanix-csi-namespace.yaml
helm template nutanix-csi-snapshot nutanix/nutanix-csi-snapshot -n ntnx-system > nutanix-csi-snapshot.yaml
helm template nutanix-csi-snapshot nutanix/nutanix-csi-storage -n ntnx-system > nutanix-csi-storage.yaml

kubectl create configmap nutanix-csi-crs --from-file=nutanix-csi-namespace.yaml --from-file=nutanix-csi-snapshot.yaml --from-file=nutanix-csi-storage.yaml
```

#### Create the ClusterResourceSet

Next you need to create the `ClusterResourceSet` resource that will map your `ConfigMap` defined above to clusters using `clusterSelector` of your choice.

Please find an example below, this `ClusterResourceSet` needs to be created inside your management cluster.

```yaml
---
apiVersion: addons.cluster.x-k8s.io/v1alpha3
kind: ClusterResourceSet
metadata:
  name: nutanix-csi-crs
spec:
  clusterSelector:
    matchLabels:
      csi: nutanix 
  resources:
  - kind: ConfigMap
    name: nutanix-csi-crs
```

The `clusterSelector` field controls how Cluster API will match this `ClusterResourceSet` on one or more workload clusters. In this case, I’m using a `matchLabels` approach where the `ClusterResourceSet` will be applied to all workload clusters having the `csi: nutanix` label present. If the label isn’t present, the `ClusterResourceSet` won’t apply to that workload cluster.

The `resources` field references the ConfigMap created above, which contains the manifests for installing the Nutanix CSI driver.

#### Assign the ClusterResourceSet to a workload cluster

Assign this `ClusterResourceSet` to your workload cluster is pretty simple, you just need to add the right label to your `Cluster` resource.

You can do it before a workload cluster creation by editing the output of the `clusterctl generate cluster` command or modify an already deployed workload cluster.

In both case `Cluster`  resources should look like this

```yaml
apiVersion: cluster.x-k8s.io/v1beta1
kind: Cluster
metadata:
  name: workload-cluster-name
  namespace: workload-cluster-namespace
  labels:
    csi: nutanix
# ...
```

*WARNING*: To allow the Nutanix CSI driver to deploy correctly, you must have a fully functional CNI deployment.



## Nutanix CSI Driver Configuration

Once your driver is installed , you have to configure it in order to use it. For that it is necessary to define `Secret` and `StorageClass`.

This can be done manually in your workload clusters or using `ClusterResourceSet` in the management cluster as explained above.

The full documentation is available here  [Nutanix Support & Insights/CSI](https://portal.nutanix.com/page/documents/list?type=software&&filterKey=product&filterVal=CSI) 
