TEMPLATES_DIR := templates

.PHONY: generate-templates.cluster-e2e
generate-templates.cluster-e2e: generate-templates.cluster-e2e.v1beta1 generate-templates.cluster-e2e.v1alpha4 ## Generate cluster templates for all versions

generate-templates.cluster-e2e.v1alpha4: ## Generate cluster templates for v1alpha4
	kustomize build $(NUTANIX_E2E_TEMPLATES)/v1alpha4/cluster-template --load-restrictor LoadRestrictionsNone > $(NUTANIX_E2E_TEMPLATES)/v1alpha4/cluster-generate-templates.yaml

generate-templates.cluster-e2e.v1beta1: # Generate cluster templates for v1beta1
	kustomize build $(NUTANIX_E2E_TEMPLATES)/v1beta1/cluster-template --load-restrictor LoadRestrictionsNone > $(NUTANIX_E2E_TEMPLATES)/v1beta1/cluster-generate-templates.yaml 
	kustomize build $(NUTANIX_E2E_TEMPLATES)/v1beta1/cluster-template-no-secret --load-restrictor LoadRestrictionsNone > $(NUTANIX_E2E_TEMPLATES)/v1beta1/cluster-template-no-secret.yaml
	kustomize build $(NUTANIX_E2E_TEMPLATES)/v1beta1/cluster-template-no-nutanix-cluster --load-restrictor LoadRestrictionsNone > $(NUTANIX_E2E_TEMPLATES)/v1beta1/cluster-template-no-nutanix-cluster.yaml
	kustomize build $(NUTANIX_E2E_TEMPLATES)/v1beta1/cluster-template-additional-categories --load-restrictor LoadRestrictionsNone > $(NUTANIX_E2E_TEMPLATES)/v1beta1/cluster-template-additional-categories.yaml
	kustomize build $(NUTANIX_E2E_TEMPLATES)/v1beta1/cluster-template-no-nmt --load-restrictor LoadRestrictionsNone > $(NUTANIX_E2E_TEMPLATES)/v1beta1/cluster-template-no-nmt.yaml
	kustomize build $(NUTANIX_E2E_TEMPLATES)/v1beta1/cluster-template-project --load-restrictor LoadRestrictionsNone > $(NUTANIX_E2E_TEMPLATES)/v1beta1/cluster-template-project.yaml
	kustomize build $(NUTANIX_E2E_TEMPLATES)/v1beta1/cluster-template-ccm --load-restrictor LoadRestrictionsNone > $(NUTANIX_E2E_TEMPLATES)/v1beta1/cluster-template-ccm.yaml
	kustomize build $(NUTANIX_E2E_TEMPLATES)/v1beta1/cluster-template-upgrades --load-restrictor LoadRestrictionsNone > $(NUTANIX_E2E_TEMPLATES)/v1beta1/cluster-template-upgrades.yaml
	kustomize build $(NUTANIX_E2E_TEMPLATES)/v1beta1/cluster-template-md-remediation --load-restrictor LoadRestrictionsNone > $(NUTANIX_E2E_TEMPLATES)/v1beta1/cluster-template-md-remediation.yaml
	kustomize build $(NUTANIX_E2E_TEMPLATES)/v1beta1/cluster-template-kcp-remediation --load-restrictor LoadRestrictionsNone > $(NUTANIX_E2E_TEMPLATES)/v1beta1/cluster-template-kcp-remediation.yaml
	kustomize build $(NUTANIX_E2E_TEMPLATES)/v1beta1/cluster-template-kcp-scale-in --load-restrictor LoadRestrictionsNone > $(NUTANIX_E2E_TEMPLATES)/v1beta1/cluster-template-kcp-scale-in.yaml
	kustomize build $(NUTANIX_E2E_TEMPLATES)/v1beta1/cluster-template-csi --load-restrictor LoadRestrictionsNone > $(NUTANIX_E2E_TEMPLATES)/v1beta1/cluster-template-csi.yaml

generate-templates.cluster-e2e.no-kubeproxy: ##Generate cluster templates without kubeproxy
	# v1alpha4
	kustomize build $(NUTANIX_E2E_TEMPLATES)/v1alpha4/no-kubeproxy/cluster-template --load-restrictor LoadRestrictionsNone > $(NUTANIX_E2E_TEMPLATES)/v1alpha4/cluster-generate-templates.yaml

	# v1beta1
	kustomize build $(NUTANIX_E2E_TEMPLATES)/v1beta1/no-kubeproxy/cluster-template --load-restrictor LoadRestrictionsNone > $(NUTANIX_E2E_TEMPLATES)/v1beta1/cluster-generate-templates.yaml
	kustomize build $(NUTANIX_E2E_TEMPLATES)/v1beta1/no-kubeproxy/cluster-template-no-secret --load-restrictor LoadRestrictionsNone > $(NUTANIX_E2E_TEMPLATES)/v1beta1/cluster-template-no-secret.yaml
	kustomize build $(NUTANIX_E2E_TEMPLATES)/v1beta1/no-kubeproxy/cluster-template-no-nutanix-cluster --load-restrictor LoadRestrictionsNone > $(NUTANIX_E2E_TEMPLATES)/v1beta1/cluster-template-no-nutanix-cluster.yaml
	kustomize build $(NUTANIX_E2E_TEMPLATES)/v1beta1/no-kubeproxy/cluster-template-additional-categories --load-restrictor LoadRestrictionsNone > $(NUTANIX_E2E_TEMPLATES)/v1beta1/cluster-template-additional-categories.yaml
	kustomize build $(NUTANIX_E2E_TEMPLATES)/v1beta1/no-kubeproxy/cluster-template-no-nmt --load-restrictor LoadRestrictionsNone > $(NUTANIX_E2E_TEMPLATES)/v1beta1/cluster-template-no-nmt.yaml
	kustomize build $(NUTANIX_E2E_TEMPLATES)/v1beta1/no-kubeproxy/cluster-template-project --load-restrictor LoadRestrictionsNone > $(NUTANIX_E2E_TEMPLATES)/v1beta1/cluster-template-project.yaml
	kustomize build $(NUTANIX_E2E_TEMPLATES)/v1beta1/no-kubeproxy/cluster-template-ccm --load-restrictor LoadRestrictionsNone > $(NUTANIX_E2E_TEMPLATES)/v1beta1/cluster-template-ccm.yaml
	kustomize build $(NUTANIX_E2E_TEMPLATES)/v1beta1/no-kubeproxy/cluster-template-upgrades --load-restrictor LoadRestrictionsNone > $(NUTANIX_E2E_TEMPLATES)/v1beta1/cluster-template-upgrades.yaml
	kustomize build $(NUTANIX_E2E_TEMPLATES)/v1beta1/no-kubeproxy/cluster-template-md-remediation --load-restrictor LoadRestrictionsNone > $(NUTANIX_E2E_TEMPLATES)/v1beta1/cluster-template-md-remediation.yaml
	kustomize build $(NUTANIX_E2E_TEMPLATES)/v1beta1/no-kubeproxy/cluster-template-kcp-remediation --load-restrictor LoadRestrictionsNone > $(NUTANIX_E2E_TEMPLATES)/v1beta1/cluster-template-kcp-remediation.yaml
	kustomize build $(NUTANIX_E2E_TEMPLATES)/v1beta1/no-kubeproxy/cluster-template-kcp-scale-in --load-restrictor LoadRestrictionsNone > $(NUTANIX_E2E_TEMPLATES)/v1beta1/cluster-template-kcp-scale-in.yaml
	kustomize build $(NUTANIX_E2E_TEMPLATES)/v1beta1/no-kubeproxy/cluster-template-csi --load-restrictor LoadRestrictionsNone > $(NUTANIX_E2E_TEMPLATES)/v1beta1/cluster-template-csi.yaml

generate-templates.cluster: ## Generate cluster templates for all flavors
	kustomize build $(TEMPLATES_DIR)/base > $(TEMPLATES_DIR)/cluster-generate-templates.yaml
	kustomize build $(TEMPLATES_DIR)/csi > $(TEMPLATES_DIR)/cluster-template-csi.yaml
	kustomize build $(TEMPLATES_DIR)/ccm > $(TEMPLATES_DIR)/cluster-template-ccm.yaml