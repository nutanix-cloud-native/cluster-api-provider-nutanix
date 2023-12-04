E2E_DIR := $(REPO_ROOT)/test/e2e
NUTANIX_E2E_TEMPLATES := $(E2E_DIR)/data/infrastructure-nutanix

JUNIT_REPORT_FILE := "junit.e2e_suite.1.xml"
GINKGO_SKIP := "clusterctl-Upgrade"
GINKGO_FOCUS := ""
GINKGO_NODES  := 1
E2E_CONF_FILE  := $(E2E_DIR)/config/nutanix.yaml
ARTIFACTS := $(REPO_ROOT)/_artifacts
SKIP_RESOURCE_CLEANUP := false
USE_EXISTING_CLUSTER := false
GINKGO_NOCOLOR := false
FLAVOR := e2e

# set ginkgo focus flags, if any
ifneq ($(strip $(GINKGO_FOCUS)),)
_FOCUS_ARGS := $(foreach arg,$(strip $(GINKGO_FOCUS)),--focus="$(arg)")
endif

# to set multiple ginkgo skip flags, if any
ifneq ($(strip $(GINKGO_SKIP)),)
_SKIP_ARGS := $(foreach arg,$(strip $(GINKGO_SKIP)),--skip="$(arg)")
endif

.PHONY: test.e2e
test.e2e: build-snapshot generate-templates.cluster-e2e generate-templates.cluster ## Run the end-to-end tests
	mkdir -p $(ARTIFACTS)
	NUTANIX_LOG_LEVEL=debug ginkgo -vv \
		--trace \
		--tags=e2e \
		--label-filter=$(LABEL_FILTER_ARGS) \
		$(_SKIP_ARGS) \
		$(_FOCUS_ARGS) \
		--nodes=$(GINKGO_NODES) \
		--no-color=$(GINKGO_NOCOLOR) \
		--output-dir="$(ARTIFACTS)" \
		--junit-report=$(JUNIT_REPORT_FILE) \
		--timeout="24h" \
		$(GINKGO_ARGS) ./test/e2e -- \
		-e2e.artifacts-folder="$(ARTIFACTS)" \
		-e2e.config="$(E2E_CONF_FILE)" \
		-e2e.skip-resource-cleanup=$(SKIP_RESOURCE_CLEANUP) \
		-e2e.use-existing-cluster=$(USE_EXISTING_CLUSTER)

.PHONY: test.e2e.no-kubeproxy
test.e2e.no-kubeproxy: build-snapshot generate-templates.cluster-e2e.no-kubeproxy generate-templates.cluster ## Run the end-to-end tests without kubeproxy
	mkdir -p $(ARTIFACTS)
	NUTANIX_LOG_LEVEL=debug $(GINKGO) -vv \
		--trace \
		--tags=e2e \
		--label-filter=$(LABEL_FILTER_ARGS) \
		$(_SKIP_ARGS) \
		--nodes=$(GINKGO_NODES) \
		--no-color=$(GINKGO_NOCOLOR) \
		--output-dir="$(ARTIFACTS)" \
		--junit-report=$(JUNIT_REPORT_FILE) \
		--timeout="24h" \
		$(GINKGO_ARGS) ./test/e2e -- \
		-e2e.artifacts-folder="$(ARTIFACTS)" \
		-e2e.config="$(E2E_CONF_FILE)" \
		-e2e.skip-resource-cleanup=$(SKIP_RESOURCE_CLEANUP) \
		-e2e.use-existing-cluster=$(USE_EXISTING_CLUSTER)

.PHONY: list-e2e
list-e2e: build-snapshot generate-templates.cluster-e2e generate-templates.cluster ## Run the end-to-end tests
	mkdir -p $(ARTIFACTS)
	ginkgo -v --trace --dry-run --tags=e2e --label-filter="$(LABEL_FILTERS)" $(_SKIP_ARGS) --nodes=$(GINKGO_NODES) \
	    --no-color=$(GINKGO_NOCOLOR) --output-dir="$(ARTIFACTS)" \
	    $(GINKGO_ARGS) ./test/e2e -- \
	    -e2e.artifacts-folder="$(ARTIFACTS)" \
	    -e2e.config="$(E2E_CONF_FILE)" \
	    -e2e.skip-resource-cleanup=$(SKIP_RESOURCE_CLEANUP) -e2e.use-existing-cluster=$(USE_EXISTING_CLUSTER)

.PHONY: test.e2e.calico
test.e2e.calico: CNI=$(CNI_PATH_CALICO)
test.e2e.calico: test.e2e

.PHONY: test.e2e.flannel
test.e2e.flannel: CNI=$(CNI_PATH_FLANNEL)
test.e2e.flannel: test.e2e

.PHONY: test.e2e.cilium
test.e2e.cilium: CNI=$(CNI_PATH_CILIUM)
test.e2e.cilium: test.e2e

.PHONY: test.e2e.cilium-no-kubeproxy
test.e2e.cilium.no-kubeproxy: CNI=$(CNI_PATH_CILIUM_NO_KUBEPROXY)
test.e2e.cilium.no-kubeproxy: test.e2e.no-kubeproxy

.PHONY: test.e2e.all-cni
test.e2e.all-cni: test.e2e test.e2e.calico test.e2e.flannel test.e2e.cilium test.e2e.cilium-no-kubeproxy

TEST_NAMESPACE:= capx-test-ns
TEST_CLUSTER_NAME := mycluster

.PHONY: test.clusterctl-create
test.clusterctl-create: ## Run the tests using clusterctl
	which clusterctl
	clusterctl version
	clusterctl config repositories | grep nutanix
	clusterctl generate cluster $(TEST_CLUSTER_NAME) -i nutanix:$(LOCAL_PROVIDER_VERSION) --list-variables -v 10
	clusterctl generate cluster $(TEST_CLUSTER_NAME) -i nutanix:$(LOCAL_PROVIDER_VERSION) --target-namespace $(TEST_NAMESPACE)  -v 10 > ./cluster.yaml
	kubectl create ns $(TEST_NAMESPACE) || true
	kubectl apply -f ./cluster.yaml -n $(TEST_NAMESPACE)

.PHONY: test.clusterctl-delete
test.clusterctl-delete: ## Delete clusterctl created cluster
	kubectl -n $(TEST_NAMESPACE) delete cluster $(TEST_CLUSTER_NAME)

.PHONY: test-kubectl-bootstrap
test.kubectl-bootstrap: ## Run kubectl queries to get all capx management/bootstrap related objects
	kubectl get ns
	kubectl get all --all-namespaces
	kubectl -n capx-system get all
	kubectl -n $(TEST_NAMESPACE) get Cluster,NutanixCluster,Machine,NutanixMachine,KubeAdmControlPlane,MachineHealthCheck,nodes
	kubectl -n capx-system get pod

.PHONY: test-kubectl-workload
test.kubectl-workload: ## Run kubectl queries to get all capx workload related objects
	kubectl -n $(TEST_NAMESPACE) get secret
	kubectl -n $(TEST_NAMESPACE) get secret $(TEST_CLUSTER_NAME)-kubeconfig -o json | jq -r .data.value | base64 --decode > $(TEST_CLUSTER_NAME).workload.kubeconfig
	kubectl --kubeconfig ./$(TEST_CLUSTER_NAME).workload.kubeconfig get nodes,ns
