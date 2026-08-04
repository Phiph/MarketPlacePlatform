SHELL := /usr/bin/env bash

# -----------------------------------------------------------------------------
# Local Kratix dev environment
#
# Spins up two KinD (Kubernetes-in-Docker) clusters on this machine:
#   - "platform" - runs the Kratix controller, MinIO (state store) and Flux
#   - "worker"   - a Destination that Kratix schedules workloads onto
#
# Plus the tooling to actually build Promises against it:
#   - a local image registry, wired into both clusters' containerd
#   - the kratix CLI, for scaffolding new Promises
#   - a starter Promise (promises/database) and generic build/load/demo targets
#   - metrics-server + k9s/log targets for watching what's running
#
# Kratix itself is installed via its published Helm charts (syntasso/kratix on
# the platform cluster, syntasso/kratix-destination on the worker) rather than
# vendoring the Kratix source repo - nothing but this repo's own files and
# whatever Helm/kind/docker cache on your machine.
# -----------------------------------------------------------------------------

PLATFORM_CLUSTER := platform
WORKER_CLUSTER    := worker
PLATFORM_CTX      := kind-$(PLATFORM_CLUSTER)
WORKER_CTX        := kind-$(WORKER_CLUSTER)

KIND_NODE_IMAGE      ?= kindest/node:v1.33.1
CERT_MANAGER_VERSION ?= v1.15.0
KRATIX_HELM_REPO     := https://syntasso.github.io/helm-charts

# Newest Flux release that still serves the Bucket/Kustomization v1beta1 API
# the (deprecated) syntasso/kratix-destination chart hardcodes for Destination
# delivery - v2.7.0 dropped v1beta1 for Bucket. v2.6.4 still serves v1 too, so
# our own manifests (clusters/platform/, hack/kratix/platform-infra-source.yaml)
# use the current stable APIs; only the chart's own objects are stuck on the
# old one. Re-check this pin (`flux install --version=vX --export`, inspect
# the CRD's spec.versions) before bumping past v2.6.x.
FLUX_VERSION ?= v2.6.4

REGISTRY_NAME := kind-registry
REGISTRY_PORT := 5001

KRATIX_CLI_VERSION ?= v0.17.0
KRATIX_CLI         := bin/kratix
KRATIX_CLI_OS       = $(shell uname -s)
KRATIX_CLI_ARCH      = $(shell uname -m)

PROMISE_DIR ?= promises/database

INFRA_DIR      ?= clusters/platform
INFRA_ARTIFACT  = platform-infra
INFRA_TAG      := $(shell date +%Y%m%d%H%M%S)

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show available targets
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z0-9_-]+:.*?## / {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2} /^##@/ {printf "\n\033[1m%s\033[0m\n", substr($$0, 5)}' $(MAKEFILE_LIST)

##@ Cluster lifecycle

.PHONY: deps
deps: ## Check/install local prerequisites (docker, kind, kubectl, helm, yq, k9s, go) and fetch the kratix CLI
	@command -v docker >/dev/null || { echo "Docker is required: https://www.docker.com/products/docker-desktop"; exit 1; }
	@docker info >/dev/null 2>&1 || { echo "Docker is installed but the daemon isn't running"; exit 1; }
	@command -v kind    >/dev/null || brew install kind
	@command -v kubectl >/dev/null || brew install kubectl
	@command -v helm    >/dev/null || brew install helm
	@command -v yq      >/dev/null || brew install yq
	@command -v k9s     >/dev/null || brew install k9s
	@command -v go      >/dev/null || brew install go
	@command -v flux    >/dev/null || brew install fluxcd/tap/flux
	@$(MAKE) --no-print-directory $(KRATIX_CLI)

.PHONY: up
up: deps registry-start ## Create both clusters and install Kratix via Helm (idempotent)
	$(MAKE) --no-print-directory clusters
	$(MAKE) --no-print-directory registry-configure
	$(MAKE) --no-print-directory cert-manager
	$(MAKE) --no-print-directory kratix-platform
	$(MAKE) --no-print-directory minio
	$(MAKE) --no-print-directory kratix-worker
	$(MAKE) --no-print-directory kratix-platform-destination
	$(MAKE) --no-print-directory metrics-server
	@echo ""
	@echo "Local registry:   localhost:$(REGISTRY_PORT)"
	@echo "Platform context: $(PLATFORM_CTX)"
	@echo "Worker context:   $(WORKER_CTX)"

.PHONY: clusters
clusters: ## Create the platform + worker kind clusters (idempotent)
	@kind get clusters 2>/dev/null | grep -qx $(PLATFORM_CLUSTER) || \
		kind create cluster --name $(PLATFORM_CLUSTER) --image $(KIND_NODE_IMAGE) --config hack/kind/platform-config.yaml
	@kind get clusters 2>/dev/null | grep -qx $(WORKER_CLUSTER) || \
		kind create cluster --name $(WORKER_CLUSTER) --image $(KIND_NODE_IMAGE) --config hack/kind/worker-config.yaml

.PHONY: cert-manager
cert-manager: ## Install cert-manager on the platform cluster (required by the kratix chart's webhooks)
	kubectl --context $(PLATFORM_CTX) apply -f https://github.com/cert-manager/cert-manager/releases/download/$(CERT_MANAGER_VERSION)/cert-manager.yaml
	kubectl --context $(PLATFORM_CTX) wait --for=condition=available --timeout=180s \
		-n cert-manager deployment/cert-manager deployment/cert-manager-cainjector deployment/cert-manager-webhook

.PHONY: minio
minio: ## Install the local (dev-only, ephemeral) MinIO state store on the platform cluster
	kubectl --context $(PLATFORM_CTX) apply -f hack/kind/minio-install.yaml
	kubectl --context $(PLATFORM_CTX) wait --for=condition=ready --timeout=120s -n kratix-platform-system pod -l run=minio
	kubectl --context $(PLATFORM_CTX) wait --for=condition=complete --timeout=120s -n default job/minio-create-bucket

.PHONY: kratix-platform
kratix-platform: ## Install Kratix on the platform cluster (Helm chart: syntasso/kratix)
	helm --kube-context $(PLATFORM_CTX) upgrade --install kratix kratix \
		--repo $(KRATIX_HELM_REPO) -f hack/kratix/platform-values.yaml --wait --timeout 5m

FLUX_INSTALL_URL := https://github.com/fluxcd/flux2/releases/download/$(FLUX_VERSION)/install.yaml
FLUX_DEPLOYMENTS := deployment/source-controller deployment/kustomize-controller deployment/helm-controller

# kubectl apply of the pinned release manifest, not `flux install --version=`:
# the flux CLI refuses to install a version it considers too far from its own
# (brew tracks latest, so this errors as soon as the two drift).

.PHONY: flux-worker
flux-worker: ## Install Flux on the worker cluster, pinned to $(FLUX_VERSION) (see the pin comment above)
	kubectl --context $(WORKER_CTX) apply -f $(FLUX_INSTALL_URL)
	kubectl --context $(WORKER_CTX) wait --for=condition=available --timeout=120s -n flux-system $(FLUX_DEPLOYMENTS)

.PHONY: flux-platform
flux-platform: ## Install Flux on the platform cluster, pinned to $(FLUX_VERSION) (see the pin comment above)
	kubectl --context $(PLATFORM_CTX) apply -f $(FLUX_INSTALL_URL)
	kubectl --context $(PLATFORM_CTX) wait --for=condition=available --timeout=120s -n flux-system $(FLUX_DEPLOYMENTS)

.PHONY: kratix-worker
kratix-worker: flux-worker ## Register the worker cluster as a Destination (Helm chart: syntasso/kratix-destination)
	@platform_ip=$$(docker inspect -f '{{ (index .NetworkSettings.Networks "kind").IPAddress }}' $(PLATFORM_CLUSTER)-control-plane); \
	helm --kube-context $(WORKER_CTX) upgrade --install kratix-destination kratix-destination \
		--repo $(KRATIX_HELM_REPO) \
		--set installFlux=false \
		--set config.path=worker-1 \
		--set config.namespace=flux-system \
		--set config.secretRef.name=minio-credentials \
		--set config.secretRef.values.accesskey=bWluaW9hZG1pbg== \
		--set config.secretRef.values.secretkey=bWluaW9hZG1pbg== \
		--set config.bucket.insecure=true \
		--set config.bucket.endpoint=$$platform_ip:31337 \
		--set config.bucket.bucket=kratix \
		--wait --timeout 5m
	kubectl --context $(PLATFORM_CTX) wait destination worker-1 --for=condition=Ready --timeout=300s

.PHONY: kratix-platform-destination
kratix-platform-destination: flux-platform ## Register the platform cluster itself as a Destination (Helm chart: syntasso/kratix-destination)
	helm --kube-context $(PLATFORM_CTX) upgrade --install kratix-destination kratix-destination \
		--repo $(KRATIX_HELM_REPO) \
		--set installFlux=false \
		--set config.path=platform-cluster \
		--set config.namespace=flux-system \
		--set config.secretRef.name=minio-credentials \
		--set config.secretRef.values.accesskey=bWluaW9hZG1pbg== \
		--set config.secretRef.values.secretkey=bWluaW9hZG1pbg== \
		--set config.bucket.insecure=true \
		--set config.bucket.endpoint=minio.kratix-platform-system.svc.cluster.local:80 \
		--set config.bucket.bucket=kratix \
		--wait --timeout 5m
	kubectl --context $(PLATFORM_CTX) wait destination platform-cluster --for=condition=Ready --timeout=300s

.PHONY: restart
restart: down up ## Delete and recreate both clusters from scratch

.PHONY: down
down: ## Delete the local kind clusters
	kind delete clusters $(PLATFORM_CLUSTER) $(WORKER_CLUSTER) 2>/dev/null || true

.PHONY: destroy
destroy: down registry-stop ## Delete the clusters and the local registry

.PHONY: status
status: ## Show the state of both clusters
	@echo "== platform ($(PLATFORM_CTX)): kratix-platform-system =="
	@kubectl --context $(PLATFORM_CTX) get pods -n kratix-platform-system
	@echo ""
	@echo "== platform ($(PLATFORM_CTX)): destinations =="
	@kubectl --context $(PLATFORM_CTX) get destinations
	@echo ""
	@echo "== platform ($(PLATFORM_CTX)): flux-system =="
	@kubectl --context $(PLATFORM_CTX) get pods -n flux-system
	@echo ""
	@echo "== worker ($(WORKER_CTX)): flux-system =="
	@kubectl --context $(WORKER_CTX) get pods -n flux-system

.PHONY: platform-context
platform-context: ## Point kubectl at the platform cluster
	kubectl config use-context $(PLATFORM_CTX)

.PHONY: worker-context
worker-context: ## Point kubectl at the worker cluster
	kubectl config use-context $(WORKER_CTX)

##@ Local registry

.PHONY: registry-start
registry-start: ## Start the local docker registry container (idempotent)
	@if [ "$$(docker inspect -f '{{.State.Running}}' $(REGISTRY_NAME) 2>/dev/null)" != "true" ]; then \
		docker run -d --restart=always -p 127.0.0.1:$(REGISTRY_PORT):5000 --network bridge --name $(REGISTRY_NAME) registry:3; \
	fi

.PHONY: registry-configure
registry-configure: ## Wire the local registry into both clusters' containerd (run after clusters exist)
	@for cluster in $(PLATFORM_CLUSTER) $(WORKER_CLUSTER); do \
		for node in $$(kind get nodes --name $$cluster 2>/dev/null); do \
			docker exec "$$node" mkdir -p "/etc/containerd/certs.d/localhost:$(REGISTRY_PORT)"; \
			printf '[host."http://%s:5000"]\n' "$(REGISTRY_NAME)" | docker exec -i "$$node" cp /dev/stdin "/etc/containerd/certs.d/localhost:$(REGISTRY_PORT)/hosts.toml"; \
		done; \
	done
	@docker network connect kind $(REGISTRY_NAME) 2>/dev/null || true
	@kubectl --context $(PLATFORM_CTX) apply -f hack/kind/local-registry-configmap.yaml
	@kubectl --context $(WORKER_CTX) apply -f hack/kind/local-registry-configmap.yaml

.PHONY: registry
registry: registry-start registry-configure ## Ensure the local registry is running and wired into both clusters

.PHONY: registry-stop
registry-stop: ## Stop and remove the local registry container
	@docker rm -f $(REGISTRY_NAME) >/dev/null 2>&1 || true

##@ Platform infra (Flux/GitOps prototype)
#
# $(INFRA_DIR) is reconciled onto the platform cluster by the same Flux
# `flux-platform` installs (pinned to $(FLUX_VERSION) - see that var's
# comment above for why not literal-latest). Not a second Flux instance:
# kratix-destination's chart runs with installFlux=false and just registers
# the Destination against this one.
#
# `infra-push` bundles the folder as an OCI artifact in the local registry
# (no git remote needed for local dev); `infra-apply` points Flux at it.
# Applied with kubectl rather than `flux create`, so it's easy to diff/review
# as a file - not because of any API version mismatch (our own manifests use
# the current v1/v2 APIs; only kratix-destination's own objects are stuck on
# the old v1beta1 one). Swapping the OCIRepository for a GitRepository
# against a real git remote later is a one-object change - $(INFRA_DIR)'s
# contents don't need to move.

.PHONY: infra-push
infra-push: ## Push $(INFRA_DIR) as an OCI artifact to the local registry
	@command -v flux >/dev/null || { echo "flux CLI is required: brew install fluxcd/tap/flux"; exit 1; }
	flux push artifact oci://localhost:$(REGISTRY_PORT)/$(INFRA_ARTIFACT):$(INFRA_TAG) \
		--path=$(INFRA_DIR) --source="local dev" --revision="$(INFRA_TAG)"

.PHONY: infra-apply
infra-apply: ## Point the platform cluster's Flux at the pushed artifact (idempotent)
	kubectl --context $(PLATFORM_CTX) apply -f hack/kratix/platform-infra-source.yaml
	kubectl --context $(PLATFORM_CTX) patch ocirepository $(INFRA_ARTIFACT) -n flux-system \
		--type merge -p '{"spec":{"ref":{"tag":"$(INFRA_TAG)"}}}'

.PHONY: infra
infra: infra-push infra-apply ## Push $(INFRA_DIR) and reconcile it onto the platform cluster

##@ Promises

$(KRATIX_CLI):
	mkdir -p bin
	curl -sL https://github.com/syntasso/kratix-cli/releases/download/$(KRATIX_CLI_VERSION)/kratix-cli_$(KRATIX_CLI_OS)_$(KRATIX_CLI_ARCH).tar.gz | tar -xz -C bin kratix
	chmod +x $(KRATIX_CLI)

.PHONY: cli
cli: $(KRATIX_CLI) ## Fetch the kratix CLI (scaffolds new Promises: bin/kratix init promise ...) into bin/kratix

.PHONY: promise-build
promise-build: ## Build every pipeline image defined in $(PROMISE_DIR)/promise.yaml
	@yq '(.spec.workflows.promise.configure // []) + (.spec.workflows.resource.configure // []) | .[].spec.containers[] | .name + "\t" + .image' $(PROMISE_DIR)/promise.yaml | \
	while IFS=$$'\t' read -r name image; do \
		dir=$$(find $(PROMISE_DIR)/workflows -type d -name "$$name"); \
		echo "Building $$image from $$dir"; \
		docker build -t "$$image" "$$dir"; \
	done

.PHONY: promise-load
promise-load: ## Load $(PROMISE_DIR)'s built pipeline images into the platform cluster
	@yq '(.spec.workflows.promise.configure // []) + (.spec.workflows.resource.configure // []) | .[].spec.containers[].image' $(PROMISE_DIR)/promise.yaml | \
	while read -r image; do \
		echo "Loading $$image into $(PLATFORM_CLUSTER)"; \
		kind load docker-image "$$image" --name $(PLATFORM_CLUSTER); \
	done

.PHONY: promise-demo
promise-demo: promise-build promise-load ## Build, load, and install $(PROMISE_DIR), then request its example resource
	kubectl --context $(PLATFORM_CTX) apply -f $(PROMISE_DIR)/promise.yaml
	@echo "Waiting for the Promise's CRD to be established..."
	@for i in $$(seq 1 60); do \
		kubectl --context $(PLATFORM_CTX) get crd databases.demo.kratix.io >/dev/null 2>&1 && break; \
		sleep 2; \
	done
	kubectl --context $(PLATFORM_CTX) apply -f $(PROMISE_DIR)/example-resource.yaml
	@echo ""
	@echo "Watch the request:  kubectl --context $(PLATFORM_CTX) get databases.demo.kratix.io example-database -w"
	@echo "Watch the worker:   kubectl --context $(WORKER_CTX) get pods -w"

##@ Broker API

.PHONY: broker-build
broker-build: ## Build the marketplace broker binary (bin/broker)
	cd broker && go build -o ../bin/broker ./cmd/broker

.PHONY: broker-run
broker-run: ## Run the marketplace broker against the platform cluster (localhost:8878, or $BROKER_ADDR)
	cd broker && BROKER_KUBE_CONTEXT=$(PLATFORM_CTX) go run ./cmd/broker

.PHONY: broker-test
broker-test: ## Run the broker's Go tests
	cd broker && go test ./...

##@ Marketplace UI

.PHONY: ui-install
ui-install: ## Install the UI's npm dependencies
	cd ui && npm install

.PHONY: dev
dev: ## Run the broker + UI dev server together (Ctrl-C stops both); UI proxies /api to the broker, no setup needed
	@trap 'kill 0' EXIT INT TERM; \
	$(MAKE) --no-print-directory broker-run & \
	$(MAKE) --no-print-directory ui-dev & \
	wait

.PHONY: ui-dev
ui-dev: ## Run the marketplace UI dev server alone (localhost:5173) against an already-running `make broker-run`
	cd ui && npm run dev

.PHONY: ui-mock
ui-mock: ## Run the UI's mock broker (localhost:8878) - no cluster needed, for UI-only work
	cd ui && npm run mock-broker

.PHONY: ui-build
ui-build: ## Type-check and production-build the UI
	cd ui && npm run build

##@ Day-2 visibility

.PHONY: metrics-server
metrics-server: ## Install metrics-server on both clusters (powers `make top`)
	@for ctx in $(PLATFORM_CTX) $(WORKER_CTX); do \
		kubectl --context $$ctx apply -f https://github.com/kubernetes-sigs/metrics-server/releases/latest/download/components.yaml >/dev/null; \
		kubectl --context $$ctx patch deployment metrics-server -n kube-system --type=json \
			-p '[{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--kubelet-insecure-tls"}]' >/dev/null 2>&1 || true; \
		kubectl --context $$ctx rollout status deployment metrics-server -n kube-system --timeout=120s; \
	done

.PHONY: top
top: ## Show CPU/memory usage per pod on both clusters
	@echo "== platform =="
	@kubectl --context $(PLATFORM_CTX) top pods -A
	@echo ""
	@echo "== worker =="
	@kubectl --context $(WORKER_CTX) top pods -A

.PHONY: logs-platform
logs-platform: ## Tail the Kratix controller logs
	kubectl --context $(PLATFORM_CTX) -n kratix-platform-system logs -f deployment/kratix-platform-controller-manager

.PHONY: logs-flux-worker
logs-flux-worker: ## Tail Flux's logs on the worker cluster
	kubectl --context $(WORKER_CTX) -n flux-system logs -f -l app=source-controller --tail=50

.PHONY: logs-flux-platform
logs-flux-platform: ## Tail Flux's logs on the platform cluster (the platform-cluster Destination)
	kubectl --context $(PLATFORM_CTX) -n flux-system logs -f -l app=source-controller --tail=50

.PHONY: k9s-platform
k9s-platform: ## Open k9s on the platform cluster
	k9s --context $(PLATFORM_CTX)

.PHONY: k9s-worker
k9s-worker: ## Open k9s on the worker cluster
	k9s --context $(WORKER_CTX)
