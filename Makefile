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

# Darwin (macOS) or Linux - including WSL2, which reports "Linux" here same as
# native. Anything else (bare Windows cmd/PowerShell) can't run this Makefile
# at all since it needs bash - see `deps`'s catch-all case and README.md's
# "Windows (WSL2)" section.
UNAME_S := $(shell uname -s)

# Only used on Linux/WSL2 (see `deps-linux`) for the tools with no apt package
# of their own. "latest" resolves via GitHub's /releases/latest redirect (no
# API calls, so no unauthenticated rate-limit surprises) - override any of
# these to pin a specific tag instead, same pattern as $(KRATIX_CLI_VERSION)/
# $(FLUX_VERSION) below.
KIND_LINUX_VERSION ?= latest
YQ_LINUX_VERSION    ?= latest
K9S_LINUX_VERSION   ?= latest

# Empty when already root or when sudo isn't present (e.g. some containers) -
# in that case the install commands below run unprefixed and simply fail if
# they actually needed root, same as any other missing-prereq failure here.
SUDO := $(shell [ "$$(id -u)" != "0" ] && command -v sudo >/dev/null 2>&1 && echo sudo)

KIND_NODE_IMAGE      ?= kindest/node:v1.33.1
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

ARGO_HELM_REPO         := https://argoproj.github.io/argo-helm
ARGO_CD_CHART_VERSION  ?= 10.3.3
ARGO_NAMESPACE         := argocd
ARGO_WORKER_CLUSTER_NAME := worker-1
# Naming source of truth: promises/team's pipeline.py's ARGO_ROLE constant
# must hold the identical string - see that file's comment for why this
# can't be shared code across languages.
ARGO_ROLE := viewer

PROMISE_DIR ?= promises/database

INFRA_DIR      ?= clusters/platform
INFRA_ARTIFACT  = platform-infra
INFRA_TAG      := $(shell date +%Y%m%d%H%M%S)

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show available targets
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z0-9_-]+:.*?## / {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2} /^##@/ {printf "\n\033[1m%s\033[0m\n", substr($$0, 5)}' $(MAKEFILE_LIST)

##@ Preflight

.PHONY: doctor
doctor: ## Check the local machine is ready for `make up` (Docker resources, disk space, local registry port) - runs automatically as the first step of `up`
	@echo "Running preflight checks..."
	@command -v docker >/dev/null || { echo "FAIL: Docker is required: https://www.docker.com/products/docker-desktop"; exit 1; }
	@docker info >/dev/null 2>&1 || { echo "FAIL: Docker is installed but the daemon isn't running"; exit 1; }
	@ncpu=$$(docker info --format '{{.NCPU}}' 2>/dev/null || echo 0); \
	mem_gb=$$(( $$(docker info --format '{{.MemTotal}}' 2>/dev/null || echo 0) / 1073741824 )); \
	[ "$$ncpu" -ge 4 ] 2>/dev/null || echo "WARN: Docker only sees $${ncpu} CPU(s) - two kind clusters plus Kratix/Flux/Argo CD/MinIO/Capsule want 4+. If things run slow or pods get evicted, raise this in Docker Desktop's Settings > Resources."; \
	[ "$$mem_gb" -ge 8 ] 2>/dev/null || echo "WARN: Docker only sees ~$${mem_gb}GB RAM - 8GB+ recommended for this stack. If pods start crash-looping or getting OOMKilled, raise this in Docker Desktop's Settings > Resources."
	@free_gb=$$(( $$(df -Pk . | awk 'NR==2 {print $$4}') / 1048576 )); \
	[ "$$free_gb" -ge 20 ] 2>/dev/null || echo "WARN: only ~$${free_gb}GB free disk space here - kind node images plus built pipeline images can use 15-20GB."
	@if (echo > /dev/tcp/127.0.0.1/$(REGISTRY_PORT)) 2>/dev/null; then \
		if [ "$$(docker inspect -f '{{.State.Running}}' $(REGISTRY_NAME) 2>/dev/null)" != "true" ]; then \
			echo "FAIL: port $(REGISTRY_PORT) is already in use by something other than $(REGISTRY_NAME) - free it, or override with 'make up REGISTRY_PORT=<port>'"; \
			exit 1; \
		fi; \
	fi
	@echo "Preflight checks passed."

##@ Cluster lifecycle

.PHONY: deps
deps: ## Check/install local prerequisites (docker, kind, kubectl, helm, yq, k9s, go, flux) and fetch the kratix CLI - macOS via Homebrew, Linux/WSL2 via apt + upstream installers
	@command -v docker >/dev/null || { echo "Docker is required: https://www.docker.com/products/docker-desktop"; exit 1; }
	@docker info >/dev/null 2>&1 || { echo "Docker is installed but the daemon isn't running"; exit 1; }
	@case "$(UNAME_S)" in \
		Darwin) \
			command -v kind    >/dev/null || brew install kind; \
			command -v kubectl >/dev/null || brew install kubectl; \
			command -v helm    >/dev/null || brew install helm; \
			command -v yq      >/dev/null || brew install yq; \
			command -v k9s     >/dev/null || brew install k9s; \
			command -v go      >/dev/null || brew install go; \
			command -v flux    >/dev/null || brew install fluxcd/tap/flux; \
			;; \
		Linux) \
			$(MAKE) --no-print-directory deps-linux; \
			;; \
		*) \
			echo "Unsupported OS '$(UNAME_S)'."; \
			echo "On Windows: install WSL2 plus a Linux distro, enable Docker Desktop's WSL2 integration for it, then run 'make up' from inside that WSL2 shell - see README.md's \"Windows (WSL2)\" section."; \
			exit 1; \
			;; \
	esac
	@$(MAKE) --no-print-directory $(KRATIX_CLI)

.PHONY: deps-linux
deps-linux: ## (internal, called by `deps` on Linux/WSL2) install kind/kubectl/helm/yq/k9s/flux/go via apt (go only) plus each tool's official upstream installer
	@command -v go >/dev/null || { \
		if command -v apt-get >/dev/null; then \
			echo "Installing go via apt..."; \
			$(SUDO) apt-get update -y && $(SUDO) apt-get install -y golang-go; \
		else \
			echo "go not found and no apt-get here - install manually: https://go.dev/dl/"; \
		fi; \
	}
	@command -v kubectl >/dev/null || { \
		echo "Installing kubectl (official upstream binary)..."; \
		arch=$$(uname -m | sed -e 's/x86_64/amd64/' -e 's/aarch64/arm64/'); \
		ver=$$(curl -fsSL https://dl.k8s.io/release/stable.txt); \
		curl -fsSLo /tmp/kubectl "https://dl.k8s.io/release/$$ver/bin/linux/$$arch/kubectl"; \
		chmod +x /tmp/kubectl && $(SUDO) mv /tmp/kubectl /usr/local/bin/kubectl; \
	}
	@command -v helm >/dev/null || { \
		echo "Installing helm (official install script)..."; \
		curl -fsSL https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | $(SUDO) bash; \
	}
	@command -v kind >/dev/null || { \
		echo "Installing kind ($(KIND_LINUX_VERSION))..."; \
		arch=$$(uname -m | sed -e 's/x86_64/amd64/' -e 's/aarch64/arm64/'); \
		tag="$(KIND_LINUX_VERSION)"; \
		[ "$$tag" = "latest" ] && tag=$$(curl -fsSL -o /dev/null -w '%{url_effective}' https://github.com/kubernetes-sigs/kind/releases/latest | sed 's#.*/tag/##'); \
		curl -fsSLo /tmp/kind "https://kind.sigs.k8s.io/dl/$$tag/kind-linux-$$arch"; \
		chmod +x /tmp/kind && $(SUDO) mv /tmp/kind /usr/local/bin/kind; \
	}
	@command -v yq >/dev/null || { \
		echo "Installing yq ($(YQ_LINUX_VERSION))..."; \
		arch=$$(uname -m | sed -e 's/x86_64/amd64/' -e 's/aarch64/arm64/'); \
		tag="$(YQ_LINUX_VERSION)"; \
		[ "$$tag" = "latest" ] && tag=$$(curl -fsSL -o /dev/null -w '%{url_effective}' https://github.com/mikefarah/yq/releases/latest | sed 's#.*/tag/##'); \
		curl -fsSLo /tmp/yq "https://github.com/mikefarah/yq/releases/download/$$tag/yq_linux_$$arch"; \
		chmod +x /tmp/yq && $(SUDO) mv /tmp/yq /usr/local/bin/yq; \
	}
	@command -v k9s >/dev/null || { \
		echo "Installing k9s ($(K9S_LINUX_VERSION))..."; \
		arch=$$(uname -m | sed -e 's/x86_64/amd64/' -e 's/aarch64/arm64/'); \
		tag="$(K9S_LINUX_VERSION)"; \
		[ "$$tag" = "latest" ] && tag=$$(curl -fsSL -o /dev/null -w '%{url_effective}' https://github.com/derailed/k9s/releases/latest | sed 's#.*/tag/##'); \
		curl -fsSL "https://github.com/derailed/k9s/releases/download/$$tag/k9s_Linux_$$arch.tar.gz" | $(SUDO) tar -xz -C /usr/local/bin k9s; \
	}
	@command -v flux >/dev/null || { \
		echo "Installing the flux CLI (official install script)..."; \
		curl -fsSL https://fluxcd.io/install.sh | $(SUDO) bash; \
	}

.PHONY: up
up: doctor deps registry-start ## Create both clusters, install Kratix, and provision the full demo (all Promises, teams, example project/environment/database) - idempotent
	@set -e; \
	start=$$(date +%s); \
	echo "[1/9] Creating clusters..."; \
	$(MAKE) --no-print-directory clusters; \
	echo "[2/9] Wiring the local registry into both clusters..."; \
	$(MAKE) --no-print-directory registry-configure; \
	echo "[3/9] Installing Flux on the platform cluster..."; \
	$(MAKE) --no-print-directory flux-platform; \
	echo "[4/9] Reconciling platform infra (cert-manager, Kratix, MinIO) via Flux..."; \
	$(MAKE) --no-print-directory infra; \
	kubectl --context $(PLATFORM_CTX) wait --for=condition=Ready --timeout=5m -n flux-system kustomization/minio || { echo "FAIL: platform infra (cert-manager/kratix/minio) didn't reconcile - check 'flux get kustomizations -n flux-system' and 'flux get helmreleases -n flux-system'"; exit 1; }; \
	echo "[5/9] Registering the worker cluster as a Destination..."; \
	$(MAKE) --no-print-directory kratix-worker; \
	echo "[6/9] Registering the platform cluster as a Destination..."; \
	$(MAKE) --no-print-directory kratix-platform-destination; \
	echo "[7/9] Installing metrics-server..."; \
	$(MAKE) --no-print-directory metrics-server; \
	echo "[8/9] Installing Argo CD and registering the worker cluster..."; \
	$(MAKE) --no-print-directory argo-register-worker; \
	echo "[9/9] Provisioning the demo (Promises, teams, example requests)..."; \
	$(MAKE) --no-print-directory demo-setup; \
	elapsed=$$(( $$(date +%s) - start )); \
	echo ""; \
	echo "Done in $$(( elapsed / 60 ))m $$(( elapsed % 60 ))s."; \
	echo ""; \
	echo "Local registry:   localhost:$(REGISTRY_PORT)"; \
	echo "Platform context: $(PLATFORM_CTX)"; \
	echo "Worker context:   $(WORKER_CTX)"; \
	echo ""; \
	echo "Next steps:"; \
	echo "  make dev     # run the broker + UI, then browse the catalog at http://localhost:5173"; \
	echo "  make verify  # confirm the demo came up healthy"

.PHONY: clusters
clusters: ## Create the platform + worker kind clusters (idempotent)
	@kind get clusters 2>/dev/null | grep -qx $(PLATFORM_CLUSTER) || \
		kind create cluster --name $(PLATFORM_CLUSTER) --image $(KIND_NODE_IMAGE) --config hack/kind/platform-config.yaml
	@kind get clusters 2>/dev/null | grep -qx $(WORKER_CLUSTER) || \
		kind create cluster --name $(WORKER_CLUSTER) --image $(KIND_NODE_IMAGE) --config hack/kind/worker-config.yaml

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

##@ Argo CD (read-only status/log engine - see docs/superpowers/specs/2026-08-14-container-workload-logs-design.md)

.PHONY: argo-install
argo-install: ## Install Argo CD on the platform cluster (Helm chart: argo/argo-cd)
	helm --kube-context $(PLATFORM_CTX) upgrade --install argocd argo-cd \
		--repo $(ARGO_HELM_REPO) --version $(ARGO_CD_CHART_VERSION) \
		--namespace $(ARGO_NAMESPACE) --create-namespace \
		-f hack/argo/platform-values.yaml --wait --timeout 5m

.PHONY: argo-register-worker
argo-register-worker: argo-install ## Register kind-worker with Argo CD as a read-only external cluster
	kubectl --context $(WORKER_CTX) apply -f hack/argo/worker-serviceaccount.yaml
	@echo "Waiting for the argocd-manager ServiceAccount token to populate..."
	@token=""; \
	for i in $$(seq 1 30); do \
		token=$$(kubectl --context $(WORKER_CTX) -n kube-system get secret argocd-manager-token -o jsonpath='{.data.token}' 2>/dev/null | base64 -d); \
		[ -n "$$token" ] && break; \
		sleep 1; \
	done; \
	if [ -z "$$token" ]; then echo "Timed out waiting for argocd-manager-token"; exit 1; fi; \
	worker_server=$$(kind get kubeconfig --internal --name $(WORKER_CLUSTER) | yq '.clusters[0].cluster.server'); \
	worker_ca=$$(kind get kubeconfig --internal --name $(WORKER_CLUSTER) | yq '.clusters[0].cluster.certificate-authority-data'); \
	if [ -z "$$worker_server" ] || [ "$$worker_server" = "null" ]; then echo "Could not read worker API server address"; exit 1; fi; \
	if [ -z "$$worker_ca" ] || [ "$$worker_ca" = "null" ]; then echo "Could not read worker CA data"; exit 1; fi; \
	worker_config=$$(printf '{"bearerToken":"%s","tlsClientConfig":{"insecure":false,"caData":"%s"}}' "$$token" "$$worker_ca"); \
	kubectl --context $(PLATFORM_CTX) -n $(ARGO_NAMESPACE) create secret generic $(ARGO_WORKER_CLUSTER_NAME)-cluster \
		--from-literal=name=$(ARGO_WORKER_CLUSTER_NAME) \
		--from-literal=server=$$worker_server \
		--from-literal=config="$$worker_config" \
		--dry-run=client -o yaml | kubectl --context $(PLATFORM_CTX) apply -f -
	kubectl --context $(PLATFORM_CTX) -n $(ARGO_NAMESPACE) label secret $(ARGO_WORKER_CLUSTER_NAME)-cluster argocd.argoproj.io/secret-type=cluster --overwrite

.PHONY: argo-provision-teams
argo-provision-teams: ## Mint a scoped Argo CD API token per team (broker/config/teams.yaml) and store it as a Secret in that team's namespace
	@admin_pw=$$(kubectl --context $(PLATFORM_CTX) -n $(ARGO_NAMESPACE) get secret argocd-initial-admin-secret -o jsonpath='{.data.password}' 2>/dev/null | base64 -d); \
	if [ -z "$$admin_pw" ]; then echo "argocd-initial-admin-secret not found (already rotated? see make argo-admin-password)"; exit 1; fi; \
	kubectl --context $(PLATFORM_CTX) -n $(ARGO_NAMESPACE) port-forward svc/argocd-server 8080:443 >/tmp/argo-provision-teams-pf.log 2>&1 & \
	pf_pid=$$!; \
	trap "kill $$pf_pid 2>/dev/null" EXIT; \
	sleep 2; \
	login_body=$$(printf '{"username":"admin","password":"%s"}' "$$admin_pw"); \
	session=$$(curl -sk -X POST http://localhost:8080/api/v1/session \
		-H 'Content-Type: application/json' \
		-d "$$login_body" | yq -p json -r '.token'); \
	if [ -z "$$session" ] || [ "$$session" = "null" ]; then echo "Failed to log into Argo CD"; exit 1; fi; \
	teams=$$(yq '.businessUnits | to_entries | .[] | .value.teams | keys | .[]' broker/config/teams.yaml); \
	if [ -z "$$teams" ]; then echo "No teams found in broker/config/teams.yaml"; exit 1; fi; \
	for team in $$teams; do \
		ns=team-$$team; \
		if kubectl --context $(PLATFORM_CTX) -n "$$ns" get secret argocd-team-token >/dev/null 2>&1; then \
			echo "argocd-team-token already exists in $$ns, skipping $$team"; \
			continue; \
		fi; \
		echo "Waiting for namespace $$ns..."; \
		ns_found=""; \
		for i in $$(seq 1 60); do \
			kubectl --context $(PLATFORM_CTX) get ns "$$ns" >/dev/null 2>&1 && ns_found=1 && break; \
			sleep 2; \
		done; \
		if [ -z "$$ns_found" ]; then echo "Namespace $$ns never appeared after 120s"; exit 1; fi; \
		echo "Waiting for AppProject $$team..."; \
		found=""; \
		for i in $$(seq 1 60); do \
			kubectl --context $(PLATFORM_CTX) -n $(ARGO_NAMESPACE) get appproject "$$team" >/dev/null 2>&1 && found=1 && break; \
			sleep 2; \
		done; \
		if [ -z "$$found" ]; then echo "AppProject $$team never appeared after 120s"; exit 1; fi; \
		echo "Minting Argo CD token for team $$team..."; \
		token=$$(curl -sk -X POST "http://localhost:8080/api/v1/projects/$$team/roles/$(ARGO_ROLE)/token" \
			-H "Authorization: Bearer $$session" -H "Content-Type: application/json" | yq -p json -r '.token'); \
		if [ -z "$$token" ] || [ "$$token" = "null" ]; then echo "Failed to mint a token for $$team"; exit 1; fi; \
		echo "Waiting for the token to be recorded in AppProject status (argoproj/argo-cd#2718 - a Flux reconcile of the declarative AppProject before this would otherwise wipe an unrecorded token)..."; \
		recorded=""; \
		for i in $$(seq 1 30); do \
			count=$$(kubectl --context $(PLATFORM_CTX) -n $(ARGO_NAMESPACE) get appproject "$$team" -o jsonpath="{.status.jwtTokensByRole.$(ARGO_ROLE).items}" 2>/dev/null | yq -p json 'length' 2>/dev/null); \
			[ -n "$$count" ] && [ "$$count" != "0" ] && recorded=1 && break; \
			sleep 1; \
		done; \
		if [ -z "$$recorded" ]; then echo "Token for $$team never appeared in AppProject status after 30s"; exit 1; fi; \
		if kubectl --context $(PLATFORM_CTX) -n "$$ns" create secret generic argocd-team-token --from-literal=token="$$token"; then \
			echo "Stored argocd-team-token in $$ns"; \
		else \
			echo "Failed to store argocd-team-token in $$ns"; \
			exit 1; \
		fi; \
	done

.PHONY: argo-admin-password
argo-admin-password: ## Print the Argo CD initial admin password
	@kubectl --context $(PLATFORM_CTX) -n $(ARGO_NAMESPACE) get secret argocd-initial-admin-secret -o jsonpath='{.data.password}' | base64 -d; echo

.PHONY: argo-ui
argo-ui: ## Port-forward the Argo CD UI to http://localhost:8080 (Ctrl-C to stop)
	@echo "Argo CD UI: http://localhost:8080 (user: admin, password: make argo-admin-password)"
	kubectl --context $(PLATFORM_CTX) -n $(ARGO_NAMESPACE) port-forward svc/argocd-server 8080:443

##@ Cluster lifecycle

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
	@echo ""
	@echo "== platform ($(PLATFORM_CTX)): argocd =="
	@kubectl --context $(PLATFORM_CTX) get pods -n argocd

##@ Verification

.PHONY: verify
verify: ## Confirm `make up` came up healthy: cluster contexts respond, Kratix pods are Running, Destinations are Ready, and the broker serves a real catalog
	@echo "Checking cluster contexts..."; \
	kubectl --context $(PLATFORM_CTX) get nodes >/dev/null || { echo "FAIL: platform context $(PLATFORM_CTX) isn't responding"; exit 1; }; \
	kubectl --context $(WORKER_CTX) get nodes >/dev/null || { echo "FAIL: worker context $(WORKER_CTX) isn't responding"; exit 1; }; \
	echo "Checking platform infra Kustomizations..."; \
	for k in cert-manager kratix minio; do \
		ready=$$(kubectl --context $(PLATFORM_CTX) get kustomization "$$k" -n flux-system -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null); \
		if [ "$$ready" != "True" ]; then echo "FAIL: Kustomization $$k (flux-system) is not Ready"; exit 1; fi; \
	done; \
	echo "Checking Kratix pods..."; \
	not_running=$$(kubectl --context $(PLATFORM_CTX) get pods -n kratix-platform-system --no-headers 2>/dev/null | grep -v -E 'Running|Completed' || true); \
	if [ -n "$$not_running" ]; then echo "FAIL: pods not Running/Completed in kratix-platform-system:"; echo "$$not_running"; exit 1; fi; \
	echo "Checking Destinations..."; \
	for d in worker-1 platform-cluster; do \
		ready=$$(kubectl --context $(PLATFORM_CTX) get destination "$$d" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null); \
		if [ "$$ready" != "True" ]; then echo "FAIL: Destination $$d is not Ready"; exit 1; fi; \
	done; \
	echo "Checking the broker builds and serves a catalog..."; \
	(cd broker && go build -o /tmp/verify-broker ./cmd/broker) || { echo "FAIL: broker failed to build"; exit 1; }; \
	cd broker; \
	BROKER_KUBE_CONTEXT=$(PLATFORM_CTX) /tmp/verify-broker >/tmp/verify-broker.log 2>&1 & \
	pid=$$!; \
	trap "kill $$pid 2>/dev/null" EXIT; \
	ok=""; \
	for i in $$(seq 1 30); do \
		curl -sf http://localhost:8878/healthz >/dev/null 2>&1 && ok=1 && break; \
		sleep 1; \
	done; \
	if [ -z "$$ok" ]; then echo "FAIL: broker never became healthy - see /tmp/verify-broker.log"; exit 1; fi; \
	count=$$(curl -sf -H "Authorization: Bearer demo-key-payments" http://localhost:8878/api/promises | yq -p json '. | length' 2>/dev/null); \
	if [ -z "$$count" ] || [ "$$count" -lt 1 ] 2>/dev/null; then echo "FAIL: /api/promises returned no catalog entries"; exit 1; fi; \
	echo "Broker healthy, catalog has $$count visible entries."; \
	echo ""; \
	echo "All checks passed - the demo is healthy."

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

##@ Platform infra (Flux/GitOps)
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
promise-demo: promise-build promise-load ## Build, load, and install $(PROMISE_DIR) at v0.0.1, then request its example resource
	kubectl --context $(PLATFORM_CTX) apply -f $(PROMISE_DIR)/promise-v0.0.1.yaml
	@crd=$$(yq '.spec.api.metadata.name' $(PROMISE_DIR)/promise-v0.0.1.yaml); \
	echo "Waiting for the Promise's CRD ($$crd) to be established..."; \
	for i in $$(seq 1 60); do \
		kubectl --context $(PLATFORM_CTX) get crd "$$crd" >/dev/null 2>&1 && break; \
		sleep 2; \
	done
	kubectl --context $(PLATFORM_CTX) apply -f $(PROMISE_DIR)/example-resource.yaml
	@crd=$$(yq '.spec.api.metadata.name' $(PROMISE_DIR)/promise-v0.0.1.yaml); \
	name=$$(yq '.metadata.name' $(PROMISE_DIR)/example-resource.yaml); \
	echo ""; \
	echo "Watch the request:  kubectl --context $(PLATFORM_CTX) get $$crd $$name -w"; \
	echo "Watch the worker:   kubectl --context $(WORKER_CTX) get pods -w"; \
	echo ""; \
	echo "Still at v0.0.1 here deliberately - demo-setup installs v0.2.0 on"; \
	echo "top once team-payments exists, giving the demo a real second"; \
	echo "Promise revision to upgrade a request between."

# Installs every Promise the broker demo needs (business-unit, team, project,
# environment - database comes from promise-demo above) and seeds them with
# example requests, so `make up` alone leaves something to browse instead of
# an empty catalog. business-unit/team objects come from broker-provision-teams
# (broker/config/teams.yaml is the actual source of truth the broker's own
# auth uses - promises/business-unit and promises/team's own example-resource.yaml
# describe the identical "platform-org"/"payments" objects but aren't applied
# here separately, to avoid two sources of truth for the same data); project
# and environment's example-resource.yaml are applied directly since nothing
# else provisions them. See README.md's "Marketplace broker API" section -
# this target automates exactly that sequence.
#
# Also seeds a real, visible upgrade-available database once team-payments
# exists: promise-demo installed database at v0.0.1 only, deliberately -
# example-resource-team.yaml is submitted while that's still the only
# revision, its binding is pinned to v0.0.1 (a ResourceBinding tracking the
# literal "latest" resolves dynamically - see bindingapi.Version - so without
# pinning it'd silently follow the v0.2.0 upgrade below instead of showing an
# upgrade as available), then promise.yaml (v0.2.0) is installed on top,
# giving Kratix a real second PromiseRevision to offer.
.PHONY: demo-setup
demo-setup: promise-demo ## Install every demo Promise, provision teams, and seed example project/environment/database requests
	@for dir in business-unit team project environment; do \
		$(MAKE) --no-print-directory promise-build promise-load PROMISE_DIR=promises/$$dir; \
		kubectl --context $(PLATFORM_CTX) apply -f promises/$$dir/promise.yaml; \
	done
	@for crd in businessunits teams projects environments; do \
		echo "Waiting for $$crd.demo.kratix.io to be established..."; \
		for i in $$(seq 1 60); do \
			kubectl --context $(PLATFORM_CTX) get crd $$crd.demo.kratix.io >/dev/null 2>&1 && break; \
			sleep 2; \
		done; \
	done
	$(MAKE) --no-print-directory broker-provision-teams
	$(MAKE) --no-print-directory argo-provision-teams
	@echo "Waiting for team-checkout's namespace (the example project/environment live there)..."
	@for i in $$(seq 1 60); do \
		kubectl --context $(PLATFORM_CTX) get ns team-checkout >/dev/null 2>&1 && break; \
		sleep 2; \
	done
	kubectl --context $(PLATFORM_CTX) apply -f promises/project/example-resource.yaml
	kubectl --context $(PLATFORM_CTX) apply -f promises/environment/example-resource.yaml
	@echo "Waiting for team-payments's namespace..."
	@for i in $$(seq 1 60); do \
		kubectl --context $(PLATFORM_CTX) get ns team-payments >/dev/null 2>&1 && break; \
		sleep 2; \
	done
	kubectl --context $(PLATFORM_CTX) apply -f promises/database/example-resource-team.yaml
	@echo "Waiting for its ResourceBinding..."
	@for i in $$(seq 1 60); do \
		kubectl --context $(PLATFORM_CTX) get resourcebindings -n team-payments -l kratix.io/resource-name=example-database -o jsonpath='{.items[0].metadata.name}' 2>/dev/null | grep -q . && break; \
		sleep 2; \
	done
	@binding=$$(kubectl --context $(PLATFORM_CTX) get resourcebindings -n team-payments -l kratix.io/resource-name=example-database -o jsonpath='{.items[0].metadata.name}'); \
	kubectl --context $(PLATFORM_CTX) patch resourcebinding "$$binding" -n team-payments --type=merge -p '{"spec":{"version":"v0.0.1"}}'
	@echo "Upgrading database to v0.2.0 (adds highAvailability) - team-payments/example-database stays pinned to v0.0.1 above, so it shows as upgrade-available..."
	kubectl --context $(PLATFORM_CTX) apply -f promises/database/promise.yaml
	@echo ""
	@echo "Example data ready to browse:"
	@echo "  Database:    example-database (default namespace, and team-payments - the latter pinned to v0.0.1, v0.2.0 available)"
	@echo "  Project:     checkout-service (team-checkout namespace)"
	@echo "  Environment: dev, under checkout-service"
	@echo "  Teams:       payments, checkout (business unit platform-org) - see broker/config/teams.yaml for API keys"

##@ Broker API

.PHONY: broker-build
broker-build: ## Build the marketplace broker binary (bin/broker)
	cd broker && go build -o ../bin/broker ./cmd/broker

.PHONY: broker-run
broker-run: ## Run the marketplace broker against the platform cluster (localhost:8878, or $BROKER_ADDR)
	cd broker && BROKER_KUBE_CONTEXT=$(PLATFORM_CTX) go run ./cmd/broker

.PHONY: broker-test
broker-test: ## Run the broker's Go tests (fast - no cluster needed)
	cd broker && go test ./...

.PHONY: broker-test-integration
broker-test-integration: ## Run the broker's real-cluster integration tests - needs `make up` already done (it now provisions everything these tests need)
	cd broker && BROKER_KUBE_CONTEXT=$(PLATFORM_CTX) go test -tags=integration ./...

.PHONY: broker-run-fake
broker-run-fake: ## Run the broker against an in-memory fake Kubernetes backend (localhost:8878) - no cluster needed, real broker code/routing/JSON
	cd broker && BROKER_FAKE_K8S=1 go run ./cmd/broker

.PHONY: broker-provision-teams
broker-provision-teams: ## Submit a BusinessUnit + Team request for every entry in broker/config/teams.yaml
	@yq '.businessUnits | keys | .[]' broker/config/teams.yaml | while read -r bu; do \
		echo "Provisioning business unit $$bu"; \
		printf 'apiVersion: demo.kratix.io/v1alpha1\nkind: BusinessUnit\nmetadata:\n  name: %s\n  namespace: default\nspec: {}\n' "$$bu" | \
			kubectl --context $(PLATFORM_CTX) apply -f -; \
	done
	@yq '.businessUnits | keys | .[]' broker/config/teams.yaml | while read -r bu; do \
		echo "Waiting for business unit $$bu's Capsule Tenant (must exist before any Team request references it - see promises/business-unit/README.md)..."; \
		for i in $$(seq 1 60); do \
			kubectl --context $(PLATFORM_CTX) get tenants.capsule.clastix.io "$$bu" >/dev/null 2>&1 && break; \
			sleep 2; \
		done; \
		kubectl --context $(PLATFORM_CTX) get tenants.capsule.clastix.io "$$bu" >/dev/null 2>&1 || \
			{ echo "Tenant $$bu never appeared after 120s - check 'kubectl get kustomization -n flux-system' and the businessunit's own status"; exit 1; }; \
	done
	@yq '.businessUnits | to_entries | .[] | .key as $$bu | .value.teams | keys | .[] | $$bu + " " + .' broker/config/teams.yaml | while read -r bu team; do \
		echo "Provisioning team $$team (business unit $$bu)"; \
		printf 'apiVersion: demo.kratix.io/v1alpha1\nkind: Team\nmetadata:\n  name: %s\n  namespace: default\nspec:\n  businessUnit: %s\n' "$$team" "$$bu" | \
			kubectl --context $(PLATFORM_CTX) apply -f -; \
	done

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
