
# Image URL to use all building/pushing image targets
IMG ?= ghcr.io/nutanix-cloud-native/cluster-api-provider-nutanix/controller:latest


# Extract base and tag from IMG
IMG_REPO ?= $(word 1,$(subst :, ,${IMG}))
IMG_TAG ?= $(word 2,$(subst :, ,${IMG}))
ifeq (${IMG_TAG},)
IMG_TAG := latest
endif

# PLATFORMS is a list of platforms to build for.
PLATFORMS ?= linux/amd64,linux/arm64,linux/arm

# KIND_CLUSTER_NAME is the name of the kind cluster to use.
KIND_CLUSTER_NAME ?= capi-test

# ENVTEST_K8S_VERSION refers to the version of kubebuilder assets to be downloaded by envtest binary.
ENVTEST_K8S_VERSION = 1.23

#
# Directories.
#
# Full directory of where the Makefile resides
ROOT_DIR:=$(shell dirname $(realpath $(firstword $(MAKEFILE_LIST))))
EXP_DIR := exp
BIN_DIR := bin
TEST_DIR := test
TOOLS_DIR := hack/tools
TOOLS_BIN_DIR := $(abspath $(TOOLS_DIR)/$(BIN_DIR))
E2E_FRAMEWORK_DIR := $(TEST_DIR)/framework
CAPD_DIR := $(TEST_DIR)/infrastructure/docker
GO_INSTALL := ./scripts/go_install.sh

export PATH := $(abspath $(TOOLS_BIN_DIR)):$(PATH)

#
# Binaries.
#
# Note: Need to use abspath so we can invoke these from subdirectories
KO_VER := v0.11.2
KO_BIN := ko
KO := $(abspath $(TOOLS_BIN_DIR)/$(KO_BIN)-$(KO_VER))
KO_PKG := github.com/google/ko

KUSTOMIZE_VER := v4.5.4
KUSTOMIZE_BIN := kustomize
KUSTOMIZE := $(abspath $(TOOLS_BIN_DIR)/$(KUSTOMIZE_BIN)-$(KUSTOMIZE_VER))
KUSTOMIZE_PKG := sigs.k8s.io/kustomize/kustomize/v4

SETUP_ENVTEST_VER := v0.0.0-20211110210527-619e6b92dab9
SETUP_ENVTEST_BIN := setup-envtest
SETUP_ENVTEST := $(abspath $(TOOLS_BIN_DIR)/$(SETUP_ENVTEST_BIN)-$(SETUP_ENVTEST_VER))
SETUP_ENVTEST_PKG := sigs.k8s.io/controller-runtime/tools/setup-envtest

CONTROLLER_GEN_VER := v0.8.0
CONTROLLER_GEN_BIN := controller-gen
CONTROLLER_GEN := $(abspath $(TOOLS_BIN_DIR)/$(CONTROLLER_GEN_BIN)-$(CONTROLLER_GEN_VER))
CONTROLLER_GEN_PKG := sigs.k8s.io/controller-tools/cmd/controller-gen

GOTESTSUM_VER := v1.6.4
GOTESTSUM_BIN := gotestsum
GOTESTSUM := $(abspath $(TOOLS_BIN_DIR)/$(GOTESTSUM_BIN)-$(GOTESTSUM_VER))
GOTESTSUM_PKG := gotest.tools/gotestsum

CONVERSION_GEN_VER := v0.23.6
CONVERSION_GEN_BIN := conversion-gen
# We are intentionally using the binary without version suffix, to avoid the version
# in generated files.
CONVERSION_GEN := $(abspath $(TOOLS_BIN_DIR)/$(CONVERSION_GEN_BIN))
CONVERSION_GEN_PKG := k8s.io/code-generator/cmd/conversion-gen

ENVSUBST_VER := v2.0.0-20210730161058-179042472c46
ENVSUBST_BIN := envsubst
ENVSUBST := $(abspath $(TOOLS_BIN_DIR)/$(ENVSUBST_BIN)-$(ENVSUBST_VER))
ENVSUBST_PKG := github.com/drone/envsubst/v2/cmd/envsubst

GO_APIDIFF_VER := v0.1.0
GO_APIDIFF_BIN := go-apidiff
GO_APIDIFF := $(abspath $(TOOLS_BIN_DIR)/$(GO_APIDIFF_BIN)-$(GO_APIDIFF_VER))
GO_APIDIFF_PKG := github.com/joelanford/go-apidiff

KPROMO_VER := v3.3.0-beta.3
KPROMO_BIN := kpromo
KPROMO :=  $(abspath $(TOOLS_BIN_DIR)/$(KPROMO_BIN)-$(KPROMO_VER))
KPROMO_PKG := sigs.k8s.io/promo-tools/v3/cmd/kpromo

CONVERSION_VERIFIER_BIN := conversion-verifier
CONVERSION_VERIFIER := $(abspath $(TOOLS_BIN_DIR)/$(CONVERSION_VERIFIER_BIN))

TILT_PREPARE_BIN := tilt-prepare
TILT_PREPARE := $(abspath $(TOOLS_BIN_DIR)/$(TILT_PREPARE_BIN))

GOLANGCI_LINT_BIN := golangci-lint
GOLANGCI_LINT := $(abspath $(TOOLS_BIN_DIR)/$(GOLANGCI_LINT_BIN))

# CRD_OPTIONS define options to add to the CONTROLLER_GEN
CRD_OPTIONS ?= "crd:crdVersions=v1"

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# Setting SHELL to bash allows bash commands to be executed by recipes.
# This is a requirement for 'setup-envtest.sh' in the test target.
# Options are set to exit when a recipe line exits non-zero or a piped command fails.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

.PHONY: all
all: build

##@ General

# The help target prints out all targets with their descriptions organized
# beneath their categories. The categories are represented by '##@' and the
# target descriptions by '##'. The awk commands is responsible for reading the
# entire set of makefiles included in this invocation, looking for lines of the
# file as xyz: ## something, and then pretty-format the target and help. Then,
# if there's a line with ##@ something, that gets pretty-printed as a category.
# More info on the usage of ANSI control characters for terminal formatting:
# https://en.wikipedia.org/wiki/ANSI_escape_code#SGR_parameters
# More info on the awk command:
# http://linuxcommand.org/lc3_adv_awk.php

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)


##@ Development

.PHONY: manifests
manifests: controller-gen ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	$(CONTROLLER_GEN) $(CRD_OPTIONS) rbac:roleName=manager-role webhook paths="./..." output:crd:artifacts:config=config/crd/bases

.PHONY: generate
generate: controller-gen conversion-gen ## Generate code containing DeepCopy, DeepCopyInto, DeepCopyObject method implementations and API conversion implementations.
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

	$(CONVERSION_GEN) \
	--input-dirs=./api/v1alpha4 \
	--input-dirs=./api/v1beta1 \
	--build-tag=ignore_autogenerated_core \
	--output-file-base=zz_generated.conversion \
	--go-header-file=./hack/boilerplate.go.txt

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: test
test: manifests generate fmt vet envtest ## Run tests.
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION)  --arch=amd64 -p path)" go test ./... -coverprofile cover.out


kind-create: ## Create a kind cluster and deploy the latest supported cluster API version
	kind create cluster --name=${KIND_CLUSTER_NAME}
	clusterctl init -v 9

kind-delete: ## Delete the kind cluster
	kind delete cluster --name=${KIND_CLUSTER_NAME}

##@ Build

.PHONY: build
build: generate fmt vet ## Build manager binary.
	go build -o bin/manager main.go

.PHONY: run
run: manifests generate fmt vet ## Run a controller from your host.
	go run ./main.go

.PHONY: docker-build
docker-build: test ## Build docker image with the manager.
	KO_DOCKER_REPO=ko.local ko build -B --platform=${PLATFORMS} -t ${IMG_TAG} -L .

.PHONY: docker-push
docker-push: test ## Push docker image with the manager.
	KO_DOCKER_REPO=${IMG_REPO} ko build --bare --platform=${PLATFORMS} -t ${IMG_TAG} .

.PHONY: docker-push-kind
docker-push-kind: test ## Make docker image available to kind cluster.
	GOOS=linux GOARCH=${shell go env GOARCH} KO_DOCKER_REPO=ko.local ko build -B -t ${IMG_TAG} -L .
	docker tag ko.local/cluster-api-provider-nutanix:${IMG_TAG} ${IMG}
	kind load docker-image --name ${KIND_CLUSTER_NAME} ${IMG}

##@ Deployment

ifndef ignore-not-found
  ignore-not-found = false
endif

.PHONY: install
install: manifests kustomize ## Install CRDs into the K8s cluster specified in ~/.kube/config.
	$(KUSTOMIZE) build config/crd | kubectl apply -f -

.PHONY: uninstall
uninstall: manifests kustomize ## Uninstall CRDs from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	$(KUSTOMIZE) build config/crd | kubectl delete --ignore-not-found=$(ignore-not-found) -f -

.PHONY: deploy
deploy: manifests kustomize prepare-local-clusterctl docker-push-kind ## Deploy controller to the K8s cluster specified in ~/.kube/config.
	clusterctl init --infrastructure nutanix -v 9
	# cd config/manager && $(KUSTOMIZE) edit set image controller=${IMG}
	# $(KUSTOMIZE) build config/default | kubectl apply -f -

.PHONY: undeploy
undeploy: ## Undeploy controller from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	$(KUSTOMIZE) build config/default | kubectl delete --ignore-not-found=$(ignore-not-found) -f -

.PHONY: prepare-local-clusterctl
prepare-local-clusterctl: manifests kustomize  ## Prepare overide file for local clusterctl.
	mkdir -p ~/.cluster-api/overrides/infrastructure-nutanix/v0.2.0
	cd config/manager && $(KUSTOMIZE) edit set image controller=${IMG}
	$(KUSTOMIZE) build config/default > ~/.cluster-api/overrides/infrastructure-nutanix/v0.2.0/infrastructure-components.yaml
	cp ./metadata.yaml ~/.cluster-api/overrides/infrastructure-nutanix/v0.2.0
	cp ./cluster-template.yaml ~/.cluster-api/overrides/infrastructure-nutanix/v0.2.0
	cp ./clusterctl.yaml ~/.cluster-api/clusterctl.yaml.template

# go-get-tool will 'go get' any package $2 and install it to $1.
PROJECT_DIR := $(shell dirname $(abspath $(lastword $(MAKEFILE_LIST))))
define go-get-tool
@[ -f $(1) ] || { \
set -e ;\
echo "Downloading $(2)" ;\
GOBIN=$(PROJECT_DIR)/bin go install $(2) ;\
}
endef

## --------------------------------------
## Hack / Tools
## --------------------------------------

##@ hack/tools:

.PHONY: $(CONTROLLER_GEN_BIN)
$(CONTROLLER_GEN_BIN): $(CONTROLLER_GEN) ## Build a local copy of controller-gen.

.PHONY: $(CONVERSION_GEN_BIN)
$(CONVERSION_GEN_BIN): $(CONVERSION_GEN) ## Build a local copy of conversion-gen.

.PHONY: $(CONVERSION_VERIFIER_BIN)
$(CONVERSION_VERIFIER_BIN): $(CONVERSION_VERIFIER) ## Build a local copy of conversion-verifier.

.PHONY: $(GOTESTSUM_BIN)
$(GOTESTSUM_BIN): $(GOTESTSUM) ## Build a local copy of gotestsum.

.PHONY: $(GO_APIDIFF_BIN)
$(GO_APIDIFF_BIN): $(GO_APIDIFF) ## Build a local copy of go-apidiff

.PHONY: $(ENVSUBST_BIN)
$(ENVSUBST_BIN): $(ENVSUBST) ## Build a local copy of envsubst.

.PHONY: $(KUSTOMIZE_BIN)
$(KUSTOMIZE_BIN): $(KUSTOMIZE) ## Build a local copy of kustomize.

.PHONY: $(SETUP_ENVTEST_BIN)
$(SETUP_ENVTEST_BIN): $(SETUP_ENVTEST) ## Build a local copy of setup-envtest.

.PHONY: $(KPROMO_BIN)
$(KPROMO_BIN): $(KPROMO) ## Build a local copy of kpromo

.PHONY: $(TILT_PREPARE_BIN)
$(TILT_PREPARE_BIN): $(TILT_PREPARE) ## Build a local copy of tilt-prepare.

.PHONY: $(GOLANGCI_LINT_BIN)
$(GOLANGCI_LINT_BIN): $(GOLANGCI_LINT) ## Build a local copy of golangci-lint

$(CONTROLLER_GEN): # Build controller-gen from tools folder.
	GOBIN=$(TOOLS_BIN_DIR) $(GO_INSTALL) $(CONTROLLER_GEN_PKG) $(CONTROLLER_GEN_BIN) $(CONTROLLER_GEN_VER)

## We are forcing a rebuilt of conversion-gen via PHONY so that we're always using an up-to-date version.
## We can't use a versioned name for the binary, because that would be reflected in generated files.
.PHONY: $(CONVERSION_GEN)
$(CONVERSION_GEN): # Build conversion-gen from tools folder.
	GOBIN=$(TOOLS_BIN_DIR) $(GO_INSTALL) $(CONVERSION_GEN_PKG) $(CONVERSION_GEN_BIN) $(CONVERSION_GEN_VER)

$(CONVERSION_VERIFIER): $(TOOLS_DIR)/go.mod # Build conversion-verifier from tools folder.
	cd $(TOOLS_DIR); go build -tags=tools -o $(BIN_DIR)/conversion-verifier sigs.k8s.io/cluster-api/hack/tools/conversion-verifier

$(GOTESTSUM): # Build gotestsum from tools folder.
	GOBIN=$(TOOLS_BIN_DIR) $(GO_INSTALL) $(GOTESTSUM_PKG) $(GOTESTSUM_BIN) $(GOTESTSUM_VER)

$(GO_APIDIFF): # Build go-apidiff from tools folder.
	GOBIN=$(TOOLS_BIN_DIR) $(GO_INSTALL) $(GO_APIDIFF_PKG) $(GO_APIDIFF_BIN) $(GO_APIDIFF_VER)

$(ENVSUBST): # Build gotestsum from tools folder.
	GOBIN=$(TOOLS_BIN_DIR) $(GO_INSTALL) $(ENVSUBST_PKG) $(ENVSUBST_BIN) $(ENVSUBST_VER)

$(KUSTOMIZE): # Build kustomize from tools folder.
	CGO_ENABLED=0 GOBIN=$(TOOLS_BIN_DIR) $(GO_INSTALL) $(KUSTOMIZE_PKG) $(KUSTOMIZE_BIN) $(KUSTOMIZE_VER)

$(SETUP_ENVTEST): # Build setup-envtest from tools folder.
	GOBIN=$(TOOLS_BIN_DIR) $(GO_INSTALL) $(SETUP_ENVTEST_PKG) $(SETUP_ENVTEST_BIN) $(SETUP_ENVTEST_VER)

$(TILT_PREPARE): $(TOOLS_DIR)/go.mod # Build tilt-prepare from tools folder.
	cd $(TOOLS_DIR); go build -tags=tools -o $(BIN_DIR)/tilt-prepare sigs.k8s.io/cluster-api/hack/tools/tilt-prepare

$(KPROMO):
	GOBIN=$(TOOLS_BIN_DIR) $(GO_INSTALL) $(KPROMO_PKG) $(KPROMO_BIN) ${KPROMO_VER}

$(GOLANGCI_LINT): .github/workflows/golangci-lint.yml # Download golangci-lint using hack script into tools folder.
	hack/ensure-golangci-lint.sh \
		-b $(TOOLS_BIN_DIR) \
		$(shell cat .github/workflows/golangci-lint.yml | grep [[:space:]]version | sed 's/.*version: //')