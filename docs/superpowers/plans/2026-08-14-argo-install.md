# Argo CD Install & Worker Cluster Registration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Install Argo CD on `kind-platform` and register `kind-worker` as an externally-managed cluster, with Argo's access to `kind-worker` scoped to read/log-only (no `cluster-admin`) - the foundation the later container-workload-logs plans build on. This plan produces no application-specific behavior yet (no `AppProject`/`Application` objects) - it's done when Argo CD is running and can see `kind-worker`'s resources.

**Architecture:** Argo CD installs via its published Helm chart on `kind-platform`, mirroring how Kratix itself is installed (`hack/kratix/platform-values.yaml` → `hack/argo/platform-values.yaml`). `kind-worker` is registered as an external cluster using a narrowly-scoped `ServiceAccount` token (not `cluster-admin`), with the worker's docker-network-internal API server address obtained via `kind get kubeconfig --internal` - the officially supported way to get an address reachable from another container on the same `kind` docker network, used here instead of the existing `docker inspect` trick because it also hands back the correct CA data in the same call.

**Tech Stack:** Helm (`argo-helm/argo-cd` chart), `kubectl`, `kind`, `yq` (already a repo dependency via `make deps`).

## Global Constraints

- Argo CD Helm chart: repo `https://argoproj.github.io/argo-helm`, chart `argo-cd`, pinned version `10.3.3` (verified current stable as of 2026-08; re-check before bumping, same convention as `FLUX_VERSION`'s pin comment in the `Makefile`).
- Argo CD installs into namespace `argocd` on `kind-platform` (chart default).
- Argo's own service-account access to `kind-worker` MUST be read/log-only - no `create`/`update`/`patch`/`delete` verbs, no `cluster-admin` binding. This is a hard requirement from the design doc's RBAC section, not a suggestion.
- `kind-worker` registers in Argo under the display name `worker-1`, matching the existing Kratix `Destination` name for the same cluster - avoids introducing a second name for the same thing.
- No SSO/Dex in this plan - later plans (per-team RBAC) use local Argo accounts, not an identity provider.
- Every new Makefile target follows the existing repo convention: `##` comment for `make help`, `--wait`/explicit polling rather than fire-and-forget, `.PHONY`.

---

### Task 1: Install Argo CD on the platform cluster

**Files:**
- Create: `hack/argo/platform-values.yaml`
- Modify: `Makefile` (new `ARGO_*` variables, new `argo-install` target under a new `##@ Argo CD` section, wired into the `up` target)

**Interfaces:**
- Produces: `make argo-install` target (idempotent Helm upgrade/install), namespace `argocd` on `kind-platform` with Argo CD's server/repo-server/application-controller/redis running.

- [ ] **Step 1: Write the values file**

`hack/argo/platform-values.yaml`:

```yaml
# Values for `helm install argocd argo-cd` on the platform cluster.
# Argo CD here is a read-only status/log engine behind the broker, not a
# second GitOps delivery mechanism - see
# docs/superpowers/specs/2026-08-14-container-workload-logs-design.md.
# Flux remains the only thing that applies/prunes workload resources; every
# Application this platform creates uses manual sync (no automated block).

# No SSO/Dex - per-team access is handled with local Argo accounts in a
# later plan, not an identity provider.
dex:
  enabled: false

configs:
  params:
    # Plain HTTP inside the cluster - matches this repo's existing "insecure:
    # true" pattern for MinIO (hack/kratix/platform-values.yaml); there's no
    # cert-manager-issued cert for argocd-server and none is needed for local
    # dev. `make argo-ui` (Task 4) reaches it via port-forward.
    server.insecure: true
```

- [ ] **Step 2: Add Makefile variables and the `argo-install` target**

Add near the top of `Makefile`, alongside the other version pins (after the `KRATIX_CLI_VERSION` block):

```makefile
ARGO_HELM_REPO         := https://argoproj.github.io/argo-helm
ARGO_CD_CHART_VERSION  ?= 10.3.3
ARGO_NAMESPACE         := argocd
ARGO_WORKER_CLUSTER_NAME := worker-1
```

Add a new section near the end of the `##@ Cluster lifecycle` targets (after `kratix-platform-destination`, before `restart`):

```makefile
##@ Argo CD (read-only status/log engine, not a delivery mechanism - see
##@ docs/superpowers/specs/2026-08-14-container-workload-logs-design.md)

.PHONY: argo-install
argo-install: ## Install Argo CD on the platform cluster (Helm chart: argo/argo-cd)
	helm --kube-context $(PLATFORM_CTX) upgrade --install argocd argo-cd \
		--repo $(ARGO_HELM_REPO) --version $(ARGO_CD_CHART_VERSION) \
		--namespace $(ARGO_NAMESPACE) --create-namespace \
		-f hack/argo/platform-values.yaml --wait --timeout 5m
```

- [ ] **Step 3: Wire into `up`**

Modify the `up` target's recipe (currently ends at `demo-setup`) to add `argo-install` after `metrics-server` and before `demo-setup`:

```makefile
.PHONY: up
up: deps registry-start ## Create both clusters, install Kratix, and provision the full demo (all Promises, teams, example project/environment/database) - idempotent
	$(MAKE) --no-print-directory clusters
	$(MAKE) --no-print-directory registry-configure
	$(MAKE) --no-print-directory cert-manager
	$(MAKE) --no-print-directory kratix-platform
	$(MAKE) --no-print-directory minio
	$(MAKE) --no-print-directory kratix-worker
	$(MAKE) --no-print-directory kratix-platform-destination
	$(MAKE) --no-print-directory metrics-server
	$(MAKE) --no-print-directory argo-install
	$(MAKE) --no-print-directory argo-register-worker
	$(MAKE) --no-print-directory demo-setup
	@echo ""
	@echo "Local registry:   localhost:$(REGISTRY_PORT)"
	@echo "Platform context: $(PLATFORM_CTX)"
	@echo "Worker context:   $(WORKER_CTX)"
```

(`argo-register-worker` doesn't exist yet - that's Task 3. Adding the call now means `up` will fail until Task 3 lands; that's expected mid-plan and gets fixed by Task 3's own verification step. If you're executing this plan task-by-task with review gates between tasks, leave this line commented out until Task 3, then uncomment it as part of Task 3's own diff instead - either approach is fine, but don't leave `up` broken between review checkpoints.)

- [ ] **Step 4: Run it and verify**

Run: `make argo-install`

Expected: Helm reports `STATUS: deployed`. Then:

```bash
kubectl --context kind-platform -n argocd get pods
```

Expected: `argocd-server`, `argocd-repo-server`, `argocd-application-controller-0`, `argocd-redis` (or `argocd-redis-ha-*` depending on chart defaults) all `Running`/`1/1`.

- [ ] **Step 5: Commit**

```bash
git add hack/argo/platform-values.yaml Makefile
git commit -m "infra: install Argo CD on the platform cluster"
```

---

### Task 2: Scoped read-only ServiceAccount on the worker cluster

**Files:**
- Create: `hack/argo/worker-serviceaccount.yaml`

**Interfaces:**
- Produces: a `ServiceAccount` (`argocd-manager`, namespace `kube-system` on `kind-worker`) with a `ClusterRole` granting only `get`/`list`/`watch` on `namespaces`/`pods`/`services`/`events`/`deployments`/`replicasets`, plus `get` on `pods/log` - and a durable token `Secret` for it. This is what Task 3 reads to build Argo's cluster credential; it is deliberately **not** `cluster-admin` and has no write verbs at all, since Argo never applies anything to `kind-worker` in this design.

- [ ] **Step 1: Write the manifest**

`hack/argo/worker-serviceaccount.yaml`:

```yaml
# Applied to kind-worker (not kind-platform). Grants Argo CD read-only
# access to exactly the resource types it needs to build a resource tree
# and stream pod logs - no write verbs anywhere. Argo never applies/prunes
# resources on this cluster; Flux remains the sole applier. See
# docs/superpowers/specs/2026-08-14-container-workload-logs-design.md,
# "RBAC".
apiVersion: v1
kind: ServiceAccount
metadata:
  name: argocd-manager
  namespace: kube-system
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: argocd-manager-role
rules:
  - apiGroups: [""]
    resources: ["namespaces", "pods", "services", "events"]
    verbs: ["get", "list", "watch"]
  - apiGroups: [""]
    resources: ["pods/log"]
    verbs: ["get"]
  - apiGroups: ["apps"]
    resources: ["deployments", "replicasets"]
    verbs: ["get", "list", "watch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: argocd-manager-role-binding
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: argocd-manager-role
subjects:
  - kind: ServiceAccount
    name: argocd-manager
    namespace: kube-system
---
# Kubernetes 1.24+ no longer auto-creates a token Secret for a
# ServiceAccount - this durable token (as opposed to `kubectl create
# token`'s short-lived one) is what Argo's cluster Secret needs, since it's
# a long-running credential, not a one-off.
apiVersion: v1
kind: Secret
metadata:
  name: argocd-manager-token
  namespace: kube-system
  annotations:
    kubernetes.io/service-account.name: argocd-manager
type: kubernetes.io/service-account-token
```

- [ ] **Step 2: Apply it and verify the token populates**

```bash
kubectl --context kind-worker apply -f hack/argo/worker-serviceaccount.yaml
kubectl --context kind-worker -n kube-system get secret argocd-manager-token -o jsonpath='{.data.token}' | base64 -d | wc -c
```

Expected: a nonzero byte count (a populated JWT). If it prints nothing, the token controller hasn't run yet - wait a few seconds and retry (Task 3's Makefile target polls for this automatically).

- [ ] **Step 3: Commit**

```bash
git add hack/argo/worker-serviceaccount.yaml
git commit -m "infra: scoped read-only ServiceAccount for Argo CD on the worker cluster"
```

---

### Task 3: Register the worker cluster with Argo CD

**Files:**
- Modify: `Makefile` (new `argo-register-worker` target)

**Interfaces:**
- Consumes: `hack/argo/worker-serviceaccount.yaml` (Task 2) - the `argocd-manager`/`argocd-manager-token` names it defines.
- Produces: a `Secret` named `worker-1-cluster` in namespace `argocd` on `kind-platform`, labelled `argocd.argoproj.io/secret-type: cluster` - the declarative form of `argocd cluster add`, per [Argo CD's declarative setup docs](https://argo-cd.readthedocs.io/en/stable/operator-manual/declarative-setup/#clusters). This is what later plans' `Application` objects reference by cluster name (`worker-1`).

- [ ] **Step 1: Add the target**

Add to the `##@ Argo CD` section, after `argo-install`:

```makefile
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
	worker_config=$$(printf '{"bearerToken":"%s","tlsClientConfig":{"insecure":false,"caData":"%s"}}' "$$token" "$$worker_ca"); \
	kubectl --context $(PLATFORM_CTX) -n $(ARGO_NAMESPACE) create secret generic $(ARGO_WORKER_CLUSTER_NAME)-cluster \
		--from-literal=name=$(ARGO_WORKER_CLUSTER_NAME) \
		--from-literal=server=$$worker_server \
		--from-literal=config="$$worker_config" \
		--dry-run=client -o yaml | kubectl --context $(PLATFORM_CTX) apply -f -
	kubectl --context $(PLATFORM_CTX) -n $(ARGO_NAMESPACE) label secret $(ARGO_WORKER_CLUSTER_NAME)-cluster argocd.argoproj.io/secret-type=cluster --overwrite
```

- [ ] **Step 2: Uncomment/confirm the `up` wiring from Task 1 Step 3**

If you left the `argo-register-worker` line commented out in `up` during Task 1, uncomment it now (it should sit directly after `argo-install` and before `demo-setup`).

- [ ] **Step 3: Run it and verify**

Run: `make argo-register-worker`

Expected: no errors; then:

```bash
kubectl --context kind-platform -n argocd get secret worker-1-cluster -o jsonpath='{.data.name}' | base64 -d
```

Expected output: `worker-1`

- [ ] **Step 4: Verify Argo actually sees the cluster as connected**

This is the real acceptance check for this whole plan - Argo's controller has to successfully establish a connection using the scoped token, not just have the Secret exist.

```bash
kubectl --context kind-platform -n argocd port-forward svc/argocd-server 8080:443 &
PF_PID=$!
sleep 2
ADMIN_PW=$(kubectl --context kind-platform -n argocd get secret argocd-initial-admin-secret -o jsonpath='{.data.password}' | base64 -d)
curl -sk -X POST https://localhost:8080/api/v1/session \
  -H 'Content-Type: application/json' \
  -d "{\"username\":\"admin\",\"password\":\"$ADMIN_PW\"}" | \
  python3 -c 'import sys,json; print(json.load(sys.stdin)["token"])' > /tmp/argocd-token
TOKEN=$(cat /tmp/argocd-token)
curl -sk https://localhost:8080/api/v1/clusters -H "Authorization: Bearer $TOKEN" | \
  python3 -c 'import sys,json; d=json.load(sys.stdin); [print(c["name"], c["info"]["connectionState"]["status"]) for c in d["items"]]'
kill $PF_PID
```

Expected: a line reading `worker-1 Successful`. If it instead reads `Failed`, check the printed `connectionState.message` (add `, c["info"]["connectionState"].get("message")` to the print above) - the most likely cause is the worker's docker-network address not being reachable from an `argocd-application-controller` pod on `kind-platform`; re-run `kind get kubeconfig --internal --name worker` by hand and confirm the `server:` field resolves from inside a pod (`kubectl --context kind-platform run -it --rm debug --image=curlimages/curl --restart=Never -- curl -sk <that server>/version`).

- [ ] **Step 5: Commit**

```bash
git add Makefile
git commit -m "infra: register kind-worker with Argo CD as a read-only external cluster"
```

---

### Task 4: Convenience targets and docs

**Files:**
- Modify: `Makefile` (`argo-ui`, `argo-admin-password` targets; add Argo rows to `status`)
- Modify: `README.md` (document the new targets alongside the existing "Other targets" table)

**Interfaces:**
- Produces: `make argo-ui` (port-forward + prints the URL), `make argo-admin-password` (prints the initial admin password), both following the existing `k9s-platform`/`logs-platform` convenience-target style.

- [ ] **Step 1: Add the targets**

Append to the `##@ Argo CD` section:

```makefile
.PHONY: argo-admin-password
argo-admin-password: ## Print the Argo CD initial admin password
	@kubectl --context $(PLATFORM_CTX) -n $(ARGO_NAMESPACE) get secret argocd-initial-admin-secret -o jsonpath='{.data.password}' | base64 -d; echo

.PHONY: argo-ui
argo-ui: ## Port-forward the Argo CD UI to https://localhost:8080 (Ctrl-C to stop)
	@echo "Argo CD UI: https://localhost:8080 (user: admin, password: make argo-admin-password)"
	kubectl --context $(PLATFORM_CTX) -n $(ARGO_NAMESPACE) port-forward svc/argocd-server 8080:443
```

- [ ] **Step 2: Add Argo to `make status`**

Modify the `status` target to append, after the existing worker `flux-system` block:

```makefile
	@echo ""
	@echo "== platform ($(PLATFORM_CTX)): argocd =="
	@kubectl --context $(PLATFORM_CTX) get pods -n argocd
```

- [ ] **Step 3: Document in the README**

Add two rows to the `## Other targets` table in `README.md`, near `make k9s-platform`:

```markdown
make argo-ui              # port-forward the Argo CD UI to https://localhost:8080
make argo-admin-password  # print the Argo CD initial admin password
```

- [ ] **Step 4: Verify**

```bash
make argo-admin-password
```

Expected: a non-empty password string printed.

```bash
make argo-ui
```

Expected: prints the URL/credentials line and blocks on the port-forward (visit `https://localhost:8080` in a browser, accept the self-signed cert warning, log in with `admin`/the printed password, confirm `worker-1` shows under Settings → Clusters with a green "Successful" connection status). Ctrl-C to stop.

- [ ] **Step 5: Commit**

```bash
git add Makefile README.md
git commit -m "infra: Argo CD convenience targets (argo-ui, argo-admin-password) and status/README wiring"
```
