# Credential Management
Cluster API Provider Nutanix Cloud Infrastructure (CAPX) interacts with Nutanix Prism Central (PC) APIs to manage the required Kubernetes cluster infrastructure resources.

PC credentials are required to authenticate to the PC APIs. CAPX currently supports two mechanisms to supply the required credentials:
- Credentials injected in CAPX Manager
- Workload cluster specific credentials

## Credentials injected in CAPX Manager
By default, credentials will be injected in the CAPX Manager when the Cluster API Provider Nutanix Cloud Infrastructure is initialized. See the [getting started guide](./getting_started.md) for more information on the initialization.

Upon initialization a `nutanix-creds` secret will automatically be created in the `capx-system` namespace. This secret will contain the values supplied via the `NUTANIX_USER` and `NUTANIX_PASSWORD` parameters. 

The `nutanix-creds` secret will be used for workload cluster deployment if no other credential is supplied.

### Example
An example of the automatically created `nutanix-creds` secret can be found below:
```
---
apiVersion: v1
kind: Secret
type: Opaque
metadata:
  name: nutanix-creds
  namespace: capx-system
stringData:
  NUTANIX_USER: "<nutanix-user>"
  NUTANIX_PASSWORD: "<nutanix-password>"
```

## Workload cluster specific credentials
Users can override the [credentials injected in CAPX Manager](#credentials-injected-in-capx-manager) by supplying a credential specific to a workload cluster. The credentials can be supplied by creating a secret in the same namespace as the `NutanixCluster` namespace. 

The secret can be referenced by adding a `credentialRef` inside the `prismCentral` attribute contained in the `NutanixCluster`. 
The secret will also be deleted when the `NutanixCluster` is deleted.

Note: There is a 1:1 relation between the secret and the `NutanixCluster` object. 

### Example
Create a secret in the namespace of the `NutanixCluster`:

```
---
apiVersion: v1
kind: Secret
metadata:
  name: "<my-secret>"
  namespace: "<nutanixcluster-namespace>"
stringData:
  NUTANIX_PASSWORD: "<nutanix-password>"
  NUTANIX_USER: "<nutanix-user>"
```

Add `credentialRef` to the `NutanixCluster`:

```
apiVersion: infrastructure.cluster.x-k8s.io/v1beta1
kind: NutanixCluster
metadata:
  name: "<nutanixcluster-name>"
  namespace: "<nutanixcluster-namespace>"
spec:
  prismCentral:
    credentialRef:
      name: "<my-secret>"
      kind: Secret
...
```