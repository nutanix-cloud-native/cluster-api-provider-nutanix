
GORELEASER_PARALLELISM ?= $(shell nproc --ignore=1)
GORELEASER_DEBUG ?= false

ifndef GORELEASER_CURRENT_TAG
export GORELEASER_CURRENT_TAG=$(GIT_TAG)
endif

.PHONY: build-snapshot
build-snapshot: ## Builds a snapshot with goreleaser
build-snapshot: ; $(info $(M) building snapshot $*)
	goreleaser --debug=$(GORELEASER_DEBUG) \
		build \
		--snapshot \
		--clean \
		--parallelism=$(GORELEASER_PARALLELISM) \

.PHONY: release
release: ## Builds a release with goreleaser
release: ; $(info $(M) building release $*)
	goreleaser --debug=$(GORELEASER_DEBUG) \
		release \
		--clean \
		--parallelism=$(GORELEASER_PARALLELISM) \
		--timeout=60m \
		$(GORELEASER_FLAGS)

.PHONY: release-snapshot
release-snapshot: ## Builds a snapshot release with goreleaser
release-snapshot: ; $(info $(M) building snapshot release $*)
	goreleaser --debug=$(GORELEASER_DEBUG) \
		release \
		--snapshot \
		--skip=publish \
		--clean \
		--parallelism=$(GORELEASER_PARALLELISM) \
		--timeout=60m

RELEASE_DIR := $(REPO_ROOT)/out

.PHONY: release-manifests
release-manifests: go-generate generate-templates.cluster
	mkdir -p $(RELEASE_DIR)
	kustomize build config/default > $(RELEASE_DIR)/infrastructure-components.yaml
	cp $(TEMPLATES_DIR)/cluster-template*.yaml $(RELEASE_DIR)
	cp $(REPO_ROOT)/metadata.yaml $(RELEASE_DIR)/metadata.yaml
