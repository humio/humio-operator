
# Image URL to use all building/pushing image targets
IMG ?= humio/humio-operator:latest

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

help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

manifests: controller-gen ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	$(CONTROLLER_GEN) rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases
	hack/gen-crds.sh # NOTE: This line was custom added for the humio-operator project.

generate: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

fmt: ## Run go fmt against code.
	go fmt ./...

vet: ## Run go vet against code.
	go vet ./...

test: manifests generate fmt vet ginkgo ## Run tests.
	go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest
	$(SHELL) -c "\
		eval \$$($(GOBIN)/setup-envtest use -p env ${TEST_K8S_VERSION}); \
		export TEST_USING_ENVTEST=true; \
		$(GINKGO) --label-filter=envtest -vv --no-color --procs=3 -output-dir=${PWD} -keep-separate-reports -race --junit-report=test-results-junit.xml --randomize-suites --randomize-all -timeout 10m ./... -covermode=count -coverprofile cover.out \
	"

run-e2e-tests-local-kind: manifests generate fmt vet ## Run tests.
	hack/run-e2e-using-kind.sh

##@ Build

build: generate fmt vet ## Build manager binary.
	go build -ldflags="-s -w" -o bin/manager main.go

run: manifests generate fmt vet ## Run a controller from your host.
	go run ./main.go

docker-push: ## Push docker image with the manager.
	docker push ${IMG}

##@ Deployment

install: manifests kustomize ## Install CRDs into the K8s cluster specified in ~/.kube/config.
	$(KUSTOMIZE) build config/crd | kubectl apply -f -

uninstall: manifests kustomize ## Uninstall CRDs from the K8s cluster specified in ~/.kube/config.
	$(KUSTOMIZE) build config/crd | kubectl delete -f -

deploy: manifests kustomize ## Deploy controller to the K8s cluster specified in ~/.kube/config.
	cd config/manager && $(KUSTOMIZE) edit set image controller=${IMG}
	$(KUSTOMIZE) build config/default | kubectl apply -f -

undeploy: ## Undeploy controller from the K8s cluster specified in ~/.kube/config.
	$(KUSTOMIZE) build config/default | kubectl delete -f -


CONTROLLER_GEN = $(shell pwd)/bin/controller-gen
controller-gen: ## Download controller-gen locally.
	$(call go-install-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen@v0.14.0)

KUSTOMIZE = $(shell pwd)/bin/kustomize
kustomize: ## Download kustomize locally if necessary.
	$(call go-install-tool,$(KUSTOMIZE),sigs.k8s.io/kustomize/kustomize/v4@v4.5.2)

# go-install-tool will 'go install' any package $2 and install it to $1.
PROJECT_DIR := $(shell dirname $(abspath $(lastword $(MAKEFILE_LIST))))
define go-install-tool
@[ -f $(1) ] || { \
set -e ;\
TMP_DIR=$$(mktemp -d) ;\
cd $$TMP_DIR ;\
go mod init tmp ;\
go version ;\
echo "Downloading $(2)" ;\
GOBIN=$(PROJECT_DIR)/bin go install $(2) ;\
rm -rf $$TMP_DIR ;\
}
endef


# IMAGE_TAG_BASE defines the docker.io namespace and part of the image name for remote images.
# This variable is used to construct full image tags
IMAGE_TAG_BASE ?= humio/humio-operator

OS = $(shell go env GOOS)
ARCH = $(shell go env GOARCH)

# Run go fmt against code
fmt-simple:
	gofmt -l -w -s .

# Build the operator docker image
docker-build-operator:
	docker build --no-cache --pull -t ${IMG} ${IMG_BUILD_ARGS} .

# Build the helper docker image
docker-build-helper:
	cp LICENSE images/helper/
	docker build --no-cache --pull -t ${IMG} ${IMG_BUILD_ARGS} images/helper

# Build the logscale dummy docker image
docker-build-dummy:
	docker build --no-cache --pull -t ${IMG} ${IMG_BUILD_ARGS} images/logscale-dummy

clean:
	rm controllers_*.xml || true
	rm -r testbindir || true
	rm -r tmp || true
	kind delete cluster || true

.PHONY: ginkgo
ginkgo:
ifneq (,$(shell which ginkgo))
GINKGO=$(shell which ginkgo)
else
ifeq (,$(shell PATH=$$PATH:$(GOBIN) which ginkgo))
	@{ \
	set -ex ;\
	GINKGO_TMP_DIR=$$(mktemp -d) ;\
	cd $$GINKGO_TMP_DIR ;\
	export PATH=$$BIN_DIR:$$PATH ;\
	go mod init tmp ;\
	which go ;\
	go version ;\
	go get github.com/onsi/ginkgo/v2/ginkgo ;\
	go install github.com/onsi/ginkgo/v2/ginkgo ;\
	go get github.com/onsi/gomega/... ;\
	rm -rf $$GINKGO_TMP_DIR ;\
	}
endif
GINKGO=$(GOBIN)/ginkgo
endif

.PHONY: crdoc
crdoc:
ifneq (,$(shell which crdoc))
CRDOC=$(shell which crdoc)
else
ifeq (,$(shell PATH=$$PATH:$(GOBIN) which crdoc))
	@{ \
	set -ex ;\
	which go ;\
	go version ;\
	go install fybrik.io/crdoc@6247ceaefc6bdb5d1a038278477feeda509e4e0c ;\
	crdoc --version ;\
	}
endif
CRDOC=$(GOBIN)/crdoc
endif

apidocs: manifests crdoc
	$(CRDOC) --resources config/crd/bases --output docs/api.md
