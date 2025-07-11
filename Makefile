# Image URL to use all building/pushing image targets
IMG ?= imagepullsecret-patcher
IMG_REGISTRY ?= ttl.sh
IMG_TAG ?= dev

INSTALLER_NAMESPACE ?= imagepullsecret-patcher

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# Setting SHELL to bash allows bash commands to be executed by recipes.
# Options are set to exit when a recipe line exits non-zero or a piped command fails.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

.PHONY: all
all: build

##@ General

# The help target prints out all targets with their descriptions organized
# beneath their categories. The categories are represented by '##@' and the
# target descriptions by '##'. The awk command is responsible for reading the
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
	$(CONTROLLER_GEN) rbac:roleName= paths="./..." output:rbac:dir=deploy/helm/_generated/rbac

.PHONY: generate
generate: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt"  paths="./..."

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: test
test: manifests generate fmt vet lint setup-envtest ## Run tests.
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)" go test -v $$(go list ./... | grep -v /e2e) -coverprofile cover.out

# Utilize Kind or modify the e2e tests to load the image locally, enabling compatibility with other vendors.
KO_DOCKER_REPO    ?= ko.local/imagepullsecret-patcher
KIND_CLUSTER_NAME ?= kind-imagepullsecret-patcher
KIND_KUBECONFIG   ?= ${PWD}/.kubeconfig

.PHONY: e2e-kind
e2e-kind: kind
	$(KIND) get clusters | grep -q $(KIND_CLUSTER_NAME) || $(KIND) create cluster --name ${KIND_CLUSTER_NAME} --kubeconfig="${KIND_KUBECONFIG}" --image=kindest/node:v$(ENVTEST_K8S_VERSION) --config tests/e2e/kind.yaml

.PHONY: e2e-kind-destroy
e2e-kind-destroy:
	$(KIND) delete cluster --name ${KIND_CLUSTER_NAME}

.PHONY: e2e-local-gh-actions
e2e-local-gh-actions: e2e-kind e2e-gh-actions ## Run e2e tests in local Kind using chainsaw.

.PHONY: e2e-gh-actions
e2e-gh-actions: ko-build-kind ko-kind-install e2e ## Run e2e tests in Github Actions' Kind using chainsaw.

.PHONY: e2e
e2e: chainsaw ## Run e2e tests using chainsaw.
	$(CHAINSAW) test --test-dir ./tests/e2e/$(TESTS)

.PHONY: ko-build-local
ko-build-local: ko ## Build Docker image with KO
	KO_DOCKER_REPO=$(KO_DOCKER_REPO) $(KO) build --sbom=none --bare --platform linux/amd64 ./cmd/

.PHONY: ko-build-kind
ko-build-kind: kind ko-build-local ## Build and Load Docker image into kind cluster
	$(KIND) load docker-image $(KO_DOCKER_REPO) --name $(KIND_CLUSTER_NAME)

.PHONY: ko-kind-install
ko-kind-install: helm ko # Install Chart with built and loaded image into kind cluster
	$(KUBECTL) create namespace $(INSTALLER_NAMESPACE) --dry-run=client -o yaml | $(KUBECTL) apply --server-side --force-conflicts -f -
	$(HELM) template --namespace $(INSTALLER_NAMESPACE) imagepullsecret-patcher ./deploy/helm --set image.registry="",image.repository=$(KO_DOCKER_REPO),image.tag=latest,image.pullPolicy=Never,podAnnotations.foo=x$(shell date +%s),strategy.type=Recreate,env.CONFIG_DOCKERCONFIGJSON='\{"auths":{"example.com":{"auth":"Cg=="}}}' | $(KUBECTL) apply --server-side --force-conflicts -f -
	$(KUBECTL) --namespace $(INSTALLER_NAMESPACE) rollout status deployment/imagepullsecret-patcher --timeout=60s

.PHONY: lint
lint: golangci-lint ## Run golangci-lint linter & yamllint
	$(GOLANGCI_LINT) run

.PHONY: lint-fix
lint-fix: golangci-lint ## Run golangci-lint linter and perform fixes
	$(GOLANGCI_LINT) run --fix

##@ Build

.PHONY: build
build: manifests generate fmt vet ## Build manager binary.
	go build -o bin/manager cmd/main.go

.PHONY: run
run: manifests generate fmt vet ## Run a controller from your host.
	go run ./cmd/main.go


.PHONY: container-build-push
container-build-push: fmt vet ## Build and push container image.
	KO_DOCKER_REPO=${IMG_REGISTRY}/${IMG} ko build -t ${IMG_TAG} --bare --sbom=none cmd/main.go

.PHONY: container-build-local
container-build-local: fmt vet ## Build container image and load it into local container daemon.
	KO_DOCKER_REPO=${IMG_REGISTRY}/${IMG} ko build -t ${IMG_TAG} --bare --sbom=none --local cmd/main.go

.PHONY: build-installer
build-installer: manifests generate helm ## Generate a consolidated YAML with CRDs and deployment.
	mkdir -p dist
	echo -e "---\napiVersion: v1\nkind: Namespace\nmetadata:\n  name: ${INSTALLER_NAMESPACE}" > dist/install.yaml
	$(HELM) template deploy/helm $(shell test -e values-mine.yaml && echo "-f values-mine.yaml") --set image.registry=${IMG_REGISTRY},image.repository=${IMG},image.tag=${IMG_TAG} --name-template imagepullsecret-patcher --namespace ${INSTALLER_NAMESPACE} >> dist/install.yaml

##@ Deployment

ifndef ignore-not-found
  ignore-not-found = false
endif

.PHONY: deploy
deploy: manifests build-installer helm ## Deploy controller to the K8s cluster specified in ~/.kube/config.
	$(KUBECTL) --namespace ${INSTALLER_NAMESPACE} apply -f dist/install.yaml

.PHONY: undeploy
undeploy: helm ## Undeploy controller from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	$(KUBECTL) --namespace ${INSTALLER_NAMESPACE} delete --ignore-not-found=$(ignore-not-found) -f dist/install.yaml

##@ Dependencies

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

## Tool Binaries
KUBECTL ?= kubectl
HELM ?= $(LOCALBIN)/helm-$(HELM_VERSION)
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen-$(CONTROLLER_TOOLS_VERSION)
ENVTEST ?= $(LOCALBIN)/setup-envtest-$(ENVTEST_VERSION)
GOLANGCI_LINT = $(LOCALBIN)/golangci-lint-$(GOLANGCI_LINT_VERSION)
KIND = $(LOCALBIN)/kind-$(KIND_VERSION)
CHAINSAW = $(LOCALBIN)/chainsaw-$(CHAINSAW_VERSION)
KO = $(LOCALBIN)/ko-$(KO_VERSION)

## Tool Versions
# renovate: datasource=github-releases depName=helm/helm
HELM_VERSION ?= v3.18.4
# renovate: datasource=github-releases depName=kubernetes-sigs/controller-tools
CONTROLLER_TOOLS_VERSION ?= v0.18.0

#ENVTEST_VERSION is the version of controller-runtime release branch to fetch the envtest setup script (i.e. release-0.20)
ENVTEST_VERSION ?= $(shell go list -m -f "{{ .Version }}" sigs.k8s.io/controller-runtime | awk -F'[v.]' '{printf "release-%d.%d", $$2, $$3}')
#ENVTEST_K8S_VERSION is the version of Kubernetes to use for setting up ENVTEST binaries (i.e. 1.31)
ENVTEST_K8S_VERSION ?= $(shell go list -m -f "{{ .Version }}" k8s.io/api | awk -F'[v.]' '{printf "1.%d", $$3}')

# renovate: datasource=github-releases depName=golangci/golangci-lint
GOLANGCI_LINT_VERSION ?= v2.2.2

# renovate: datasource=github-releases depName=kubernetes-sigs/kind
KIND_VERSION ?= v0.29.0

# renovate: datasource=github-releases depName=kyverno/chainsaw
CHAINSAW_VERSION ?= v0.2.12

# renovate: datasource=github-releases depName=ko-build/ko
KO_VERSION ?= v0.18.0

.PHONY: helm
helm: $(HELM) ## Download helm locally if necessary.
$(HELM): $(LOCALBIN)
	$(call go-install-tool,$(HELM),helm.sh/helm/v3/cmd/helm,$(HELM_VERSION))

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary.
$(CONTROLLER_GEN): $(LOCALBIN)
	$(call go-install-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen,$(CONTROLLER_TOOLS_VERSION))

.PHONY: setup-envtest
setup-envtest: envtest ## Download the binaries required for ENVTEST in the local bin directory.
	@echo "Setting up envtest binaries for Kubernetes version $(ENVTEST_K8S_VERSION)..."
	@$(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path || { \
		echo "Error: Failed to set up envtest binaries for version $(ENVTEST_K8S_VERSION)."; \
		exit 1; \
	}

.PHONY: envtest
envtest: $(ENVTEST) ## Download setup-envtest locally if necessary.
$(ENVTEST): $(LOCALBIN)
	$(call go-install-tool,$(ENVTEST),sigs.k8s.io/controller-runtime/tools/setup-envtest,$(ENVTEST_VERSION))

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT) ## Download golangci-lint locally if necessary.
$(GOLANGCI_LINT): $(LOCALBIN)
	$(call go-install-tool,$(GOLANGCI_LINT),github.com/golangci/golangci-lint/v2/cmd/golangci-lint,${GOLANGCI_LINT_VERSION})

.PHONY: kind
kind: $(KIND) ## Download KIND locally if necessary.
$(KIND): $(LOCALBIN)
	$(call go-install-tool,$(KIND),sigs.k8s.io/kind,$(KIND_VERSION))

.PHONY: chainsaw
chainsaw: $(CHAINSAW) ## Download CHAINSAW locally if necessary.
$(CHAINSAW): $(LOCALBIN)
	$(call go-install-tool,$(CHAINSAW),github.com/kyverno/chainsaw,$(CHAINSAW_VERSION))

.PHONY: chainsaw
ko: $(KO) ## Download KO locally if necessary.
$(KO): $(LOCALBIN)
	$(call go-install-tool,$(KO),github.com/google/ko,$(KO_VERSION))

# go-install-tool will 'go install' any package with custom target and name of binary, if it doesn't exist
# $1 - target path with name of binary (ideally with version)
# $2 - package url which can be installed
# $3 - specific version of package
define go-install-tool
@[ -f $(1) ] || { \
set -e; \
package=$(2)@$(3) ;\
echo "Downloading $${package}" ;\
GOBIN=$(LOCALBIN) go install $${package} ;\
mv "$$(echo "$(1)" | sed "s/-$(3)$$//")" $(1) ;\
}
endef
