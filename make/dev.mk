IMG := ghcr.io/nutanix-cloud-native/cluster-api-provider-nutanix/controller:latest
IMG_REPO := $(word 1,$(subst :, ,$(IMG)))
IMG_TAG := $(word 2,$(subst :, ,$(IMG)))
LOCAL_PROVIDER_VERSION ?= ${IMG_TAG}
ifeq ($(IMG_TAG),)
IMG_TAG := latest
endif

ifeq ($(LOCAL_PROVIDER_VERSION),latest)
LOCAL_PROVIDER_VERSION := v0.0.0
endif

.PHONY: dev.run-on-kind
dev.run-on-kind: ## Run the controller in a local KinD cluster.
dev.run-on-kind: export KUBECONFIG := $(KIND_KUBECONFIG)
dev.run-on-kind: kind.create go-generate
dev.run-on-kind: ; $(info $(M) deploying on local KinD cluster - $(KIND_CLUSTER_NAME))
ifndef SKIP_BUILD
	$(MAKE) release-snapshot
endif
	kind load docker-image --name $(KIND_CLUSTER_NAME) \
		ko.local/cluster-api-provider-nutanix:$$(gojq -r '.version' dist/metadata.json)
	$(MAKE) dev.prepare-local-clusterctl dev.deploy IMG=ko.local/cluster-api-provider-nutanix:$$(gojq -r '.version' dist/metadata.json)

.PHONY: dev.install-crds
dev.install-crds: ## Install CRDs into the K8s cluster specified in ~/.kube/config.
dev.install-crds: go-generate; $(info $(M) installing CRDs to the Kubernetes cluster)
	kustomize build config/crd | kubectl apply -f -

ignore-not-found := false

.PHONY: uninstall
dev.uninstall-crds: ## Uninstall CRDs from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
dev.uninstall-crds: go-generate; $(info $(M) uninstalling CRDs from the Kubernetes cluster)
	kustomize build config/crd | kubectl delete --ignore-not-found=$(ignore-not-found) -f -

.PHONY: deploy
dev.deploy: go-generate ## Deploy controller to the K8s cluster specified in ~/.kube/config.
dev.deploy: ; $(info $(M) deploying controller to the Kubernetes cluster)
	clusterctl delete --infrastructure nutanix:${LOCAL_PROVIDER_VERSION} --include-crd || true
	clusterctl init --infrastructure nutanix:${LOCAL_PROVIDER_VERSION} -v 9

.PHONY: dev.undeploy
dev.undeploy: ## Undeploy controller from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
dev.undeploy: go-generate; $(info $(M) undeploying controller from the Kubernetes cluster)
	kustomize build config/default | kubectl delete --ignore-not-found=$(ignore-not-found) -f -

PHONY: dev.prepare-local-clusterctl
dev.prepare-local-clusterctl: go-generate
	mkdir -p ~/.cluster-api/overrides/infrastructure-nutanix/$(LOCAL_PROVIDER_VERSION)
	cd config/manager && kustomize edit set image controller=$(IMG)
	kustomize build config/default > ~/.cluster-api/overrides/infrastructure-nutanix/$(LOCAL_PROVIDER_VERSION)/infrastructure-components.yaml
	cp ./metadata.yaml ~/.cluster-api/overrides/infrastructure-nutanix/$(LOCAL_PROVIDER_VERSION)/
	cp ./templates/cluster-template*.yaml ~/.cluster-api/overrides/infrastructure-nutanix/$(LOCAL_PROVIDER_VERSION)/
	env LOCAL_PROVIDER_VERSION=$(LOCAL_PROVIDER_VERSION) \
		envsubst -no-unset -no-empty -no-digit -i ./clusterctl.yaml -o ~/.cluster-api/clusterctl.yaml