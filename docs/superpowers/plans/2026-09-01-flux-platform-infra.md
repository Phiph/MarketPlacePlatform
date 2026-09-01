# Flux-Managed Platform Infra Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move cert-manager, Kratix, and MinIO from imperative `make up` steps (`kubectl apply`/`helm upgrade --install`) to layered Flux `Kustomization`s in `clusters/platform/`, chained with `dependsOn` so each only applies once the previous is Ready.

**Architecture:** `clusters/platform/` gains three subfolders (`cert-manager/`, `kratix/`, `minio/`), each sourced by its own Flux `Kustomization` in `hack/kratix/platform-infra-source.yaml`, chained `cert-manager -> kratix -> minio` via `spec.dependsOn`. `make up` installs Flux on the platform cluster earlier (right after the clusters exist), then runs `make infra` (push + apply) and waits for the `minio` Kustomization to go Ready, replacing the old separate `cert-manager`/`kratix-platform`/`minio` Makefile targets, which are deleted. Destination registration (`kratix-worker`/`kratix-platform-destination`) is unaffected - it resolves a Docker-network IP at runtime, a bad fit for a static Flux manifest.

**Tech Stack:** Flux (`Kustomization`, `HelmRelease`, `HelmRepository`, `OCIRepository` CRDs), Helm charts (`jetstack/cert-manager`, `syntasso/kratix`), GNU Make, `kubectl`.

**Spec:** `docs/superpowers/specs/2026-09-01-flux-platform-infra-design.md`

## Global Constraints

- No version pin added for the Kratix Helm chart - the new `HelmRelease` must float to latest (`version: "*"`), matching the current unpinned `helm upgrade --install kratix kratix --repo ...` command exactly.
- `kratix-worker` and `kratix-platform-destination` stay as imperative Makefile/Helm-CLI targets - out of scope, not touched by any task below.
- `flux-worker` and the worker cluster are unaffected - only the platform cluster's infra bootstrap changes.
- All new/moved Flux manifests keep `wait: true` on their `Kustomization`s - `dependsOn` blocking only works if each layer actually waits for its own resources to be healthy before reporting Ready.

---

## Important note on testing against the live cluster

This worktree has a live, already-provisioned pair of kind clusters (`kind-platform`, `kind-worker`) with cert-manager, Kratix, and MinIO already installed **imperatively** (the old way). Applying the new `kratix`/`minio` Flux layers to *this* cluster would attempt to take over an already-running Kratix Helm release and MinIO deployment - the same kind of collision the current README documents for cert-manager, but untested for Kratix/MinIO and with a real user demo depending on this cluster staying healthy. **Do not run `make infra` (or manually apply the new `kratix`/`minio` Kustomizations) against the live `kind-platform` cluster before Task 5.** Tasks 1-4 verify structural/static correctness only (YAML validity, `make -n` dry runs, and - safely, since it's read-only - `make verify`'s new checks failing for the expected reason). Task 5 is the one task that actually exercises the new Flux chain end-to-end, and it starts by tearing the clusters down first.

---

### Task 1: Layered Flux manifests for cert-manager, Kratix, and MinIO

**Files:**
- Move: `clusters/platform/cert-manager-repo.yaml` -> `clusters/platform/cert-manager/cert-manager-repo.yaml`
- Move: `clusters/platform/cert-manager-release.yaml` -> `clusters/platform/cert-manager/cert-manager-release.yaml`
- Create: `clusters/platform/kratix/kratix-repo.yaml`
- Create: `clusters/platform/kratix/kratix-release.yaml`
- Move: `hack/kind/minio-install.yaml` -> `clusters/platform/minio/minio-install.yaml`
- Delete: `hack/kratix/platform-values.yaml`
- Modify: `hack/kratix/platform-infra-source.yaml`
- Modify: `hack/argo/platform-values.yaml:16` (comment only)
- Modify: `promises/business-unit/promise.yaml:23` (comment only)
- Modify: `promises/business-unit/README.md:34` (prose only)

**Interfaces:**
- Produces: Flux `Kustomization`s named `cert-manager`, `kratix`, `minio` in namespace `flux-system` on the platform cluster - Task 2 (Makefile `up`) waits on `kustomization/minio`, Task 3 (`make verify`) checks all three by name.
- Produces: `HelmRelease` named `kratix` in namespace `flux-system`, `targetNamespace: default` - matches the Helm release name/namespace the old imperative `helm upgrade --install kratix kratix` (no `-n` flag, so default context namespace) already created, so Flux can take over the existing release rather than creating a colliding second one.

- [ ] **Step 1: Move the cert-manager manifests into their own subfolder**

```bash
mkdir -p clusters/platform/cert-manager
git mv clusters/platform/cert-manager-repo.yaml clusters/platform/cert-manager/cert-manager-repo.yaml
git mv clusters/platform/cert-manager-release.yaml clusters/platform/cert-manager/cert-manager-release.yaml
```

No content changes to these two files - only their location moves.

- [ ] **Step 2: Create the Kratix HelmRepository**

Create `clusters/platform/kratix/kratix-repo.yaml`:

```yaml
# Chart source for kratix-release.yaml.
apiVersion: source.toolkit.fluxcd.io/v1
kind: HelmRepository
metadata:
  name: syntasso
  namespace: flux-system
spec:
  interval: 1h
  url: https://syntasso.github.io/helm-charts
```

- [ ] **Step 3: Create the Kratix HelmRelease**

Create `clusters/platform/kratix/kratix-release.yaml`, inlining the values that used to live in `hack/kratix/platform-values.yaml`:

```yaml
# Replaces the Makefile's old `kratix-platform` target (`helm upgrade
# --install kratix kratix --repo ... -f hack/kratix/platform-values.yaml
# --wait --timeout 5m`, no explicit namespace, so it landed in "default" -
# targetNamespace below matches that). version is intentionally unpinned
# ("*"), matching that command's own lack of a --version flag - see the
# design spec's Non-goals for why no pin was added here.
#
# Points Kratix at the local MinIO (../minio/minio-install.yaml) and
# pre-registers both the worker cluster and the platform cluster itself as
# Destinations (some Promises - e.g. compound Promises - require the platform
# cluster to also be a valid Destination).
#
# Secret keys (accessKeyID/secretAccessKey) must match what Kratix's own S3
# writer expects - see https://github.com/syntasso/kratix/blob/main/lib/writers/s3.go
#
# The chart's default values.yaml ships a non-empty *deprecated* stateStores
# list that (a) creates its own "default" BucketStateStore + minio-credentials
# Secret, colliding by name with the ones below, and (b) applies as a
# post-install hook that runs *after* additionalResources, so it would
# silently overwrite our correct secret keys with the wrong ones. Disable it.
apiVersion: helm.toolkit.fluxcd.io/v2
kind: HelmRelease
metadata:
  name: kratix
  namespace: flux-system
spec:
  interval: 1h
  targetNamespace: default
  install:
    createNamespace: true
  chart:
    spec:
      chart: kratix
      version: "*"
      sourceRef:
        kind: HelmRepository
        name: syntasso
        namespace: flux-system
  values:
    stateStores: []
    destinations: []
    additionalResources:
      - apiVersion: v1
        kind: Secret
        metadata:
          name: minio-credentials
          namespace: default
        type: Opaque
        stringData:
          accessKeyID: minioadmin
          secretAccessKey: minioadmin
      - apiVersion: platform.kratix.io/v1alpha1
        kind: BucketStateStore
        metadata:
          name: default
        spec:
          endpoint: minio.kratix-platform-system.svc.cluster.local
          insecure: true
          bucketName: kratix
          authMethod: accessKey
          secretRef:
            name: minio-credentials
            namespace: default
      - apiVersion: platform.kratix.io/v1alpha1
        kind: Destination
        metadata:
          name: worker-1
          labels:
            environment: dev
        spec:
          path: worker-1
          stateStoreRef:
            name: default
            kind: BucketStateStore
      # strictMatchLabels: true keeps this out of the default "no
      # destinationSelectors -> every Destination" scheduling behaviour (see
      # https://docs.kratix.io/main/reference/destinations/multidestination-management),
      # so existing Promises that don't explicitly ask for the platform cluster
      # (like the promises/database starter) keep landing on worker-1 only.
      - apiVersion: platform.kratix.io/v1alpha1
        kind: Destination
        metadata:
          name: platform-cluster
          labels:
            environment: platform
        spec:
          path: platform-cluster
          strictMatchLabels: true
          stateStoreRef:
            name: default
            kind: BucketStateStore
```

- [ ] **Step 4: Move the MinIO manifest and update its ordering comment**

```bash
mkdir -p clusters/platform/minio
git mv hack/kind/minio-install.yaml clusters/platform/minio/minio-install.yaml
```

In `clusters/platform/minio/minio-install.yaml`, update the top comment block. Replace:

```yaml
# Ephemeral, single-pod MinIO for local dev - not for production use.
# Vendored (not fetched at apply-time) from:
# https://github.com/syntasso/kratix/blob/main/config/samples/minio-install.yaml
#
# The kratix-platform-system Namespace and the default/minio-credentials
# Secret are deliberately NOT declared here - the Kratix Helm chart already
# owns both (the namespace as where its controller-manager runs, the secret
# via platform-values.yaml's additionalResources), and Helm refuses to adopt
# resources it didn't create. The `minio` Makefile target now runs after
# `kratix-platform` so both exist by the time this file is applied.
```

with:

```yaml
# Ephemeral, single-pod MinIO for local dev - not for production use.
# Vendored (not fetched at apply-time) from:
# https://github.com/syntasso/kratix/blob/main/config/samples/minio-install.yaml
#
# The kratix-platform-system Namespace and the default/minio-credentials
# Secret are deliberately NOT declared here - the Kratix Helm chart already
# owns both (the namespace as where its controller-manager runs, the secret
# via ../kratix/kratix-release.yaml's additionalResources), and Helm refuses
# to adopt resources it didn't create. The `minio` Kustomization's
# `dependsOn: [{name: kratix}]` (see ../../../hack/kratix/platform-infra-source.yaml)
# is what guarantees both exist by the time this file is applied.
```

- [ ] **Step 5: Delete the now-unused Kratix values file**

```bash
git rm hack/kratix/platform-values.yaml
```

- [ ] **Step 6: Update the three doc-pointer comments that referenced it**

In `hack/argo/platform-values.yaml`, replace:

```yaml
    # Plain HTTP inside the cluster - matches this repo's existing "insecure:
    # true" pattern for MinIO (hack/kratix/platform-values.yaml); there's no
```

with:

```yaml
    # Plain HTTP inside the cluster - matches this repo's existing "insecure:
    # true" pattern for MinIO (clusters/platform/kratix/kratix-release.yaml); there's no
```

In `promises/business-unit/promise.yaml`, replace:

```yaml
  # and the broker both run there, and platform-cluster is the only
  # Destination registered with `strictMatchLabels: true` (see
  # hack/kratix/platform-values.yaml), so it only receives output that
  # explicitly asks for it via this selector.
```

with:

```yaml
  # and the broker both run there, and platform-cluster is the only
  # Destination registered with `strictMatchLabels: true` (see
  # clusters/platform/kratix/kratix-release.yaml), so it only receives output that
  # explicitly asks for it via this selector.
```

In `promises/business-unit/README.md`, replace:

```markdown
cluster - not the worker - since that's where Capsule's own webhooks and the marketplace
broker actually run. `platform-cluster` is registered with `strictMatchLabels: true`
(`hack/kratix/platform-values.yaml`), so it only receives output that explicitly asks for it
this way.
```

with:

```markdown
cluster - not the worker - since that's where Capsule's own webhooks and the marketplace
broker actually run. `platform-cluster` is registered with `strictMatchLabels: true`
(`clusters/platform/kratix/kratix-release.yaml`), so it only receives output that explicitly asks for it
this way.
```

- [ ] **Step 7: Rewrite the Kustomization chain**

Replace the full contents of `hack/kratix/platform-infra-source.yaml`:

```yaml
# Points the platform cluster's Flux (installed by the `flux-platform`
# Makefile target, pinned to $(FLUX_VERSION)) at $(INFRA_DIR)
# (clusters/platform/), bundled as an OCI artifact in the local registry by
# `make infra-push`. `make infra-apply` applies this file, then patches the
# OCIRepository's tag to whatever infra-push just pushed.
#
# clusters/platform/ has three layers (cert-manager, kratix, minio), each
# its own Kustomization sourced from a different subfolder of the same
# artifact, chained via `dependsOn` so Flux won't apply a layer until the
# previous one is fully Ready - see
# docs/superpowers/specs/2026-09-01-flux-platform-infra-design.md for why
# the ordering matters (Kratix needs cert-manager's webhooks; MinIO needs
# the Namespace/Secret Kratix's chart creates).
apiVersion: source.toolkit.fluxcd.io/v1
kind: OCIRepository
metadata:
  name: platform-infra
  namespace: flux-system
spec:
  interval: 1m
  url: oci://kind-registry:5000/platform-infra
  ref:
    tag: init
  insecure: true
---
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: cert-manager
  namespace: flux-system
spec:
  interval: 5m
  sourceRef:
    kind: OCIRepository
    name: platform-infra
  path: "./cert-manager"
  prune: true
  wait: true
  timeout: 3m
---
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: kratix
  namespace: flux-system
spec:
  dependsOn:
    - name: cert-manager
  interval: 5m
  sourceRef:
    kind: OCIRepository
    name: platform-infra
  path: "./kratix"
  prune: true
  wait: true
  timeout: 5m
---
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: minio
  namespace: flux-system
spec:
  dependsOn:
    - name: kratix
  interval: 5m
  sourceRef:
    kind: OCIRepository
    name: platform-infra
  path: "./minio"
  prune: true
  wait: true
  timeout: 5m
```

- [ ] **Step 8: Validate every new/moved YAML file parses**

Run:

```bash
python3 -c "
import yaml, glob
paths = glob.glob('clusters/platform/**/*.yaml', recursive=True) + ['hack/kratix/platform-infra-source.yaml']
for p in paths:
    docs = list(yaml.safe_load_all(open(p)))
    assert docs, f'{p}: no documents'
    print(f'{p}: {len(docs)} document(s) OK')
"
```

Expected: one line per file, no exceptions. Confirm `clusters/platform/` now contains exactly `cert-manager/`, `kratix/`, `minio/` (no stray top-level `.yaml` files):

```bash
find clusters/platform -maxdepth 1
```

Expected: only the three subfolder names, no files directly under `clusters/platform/`.

- [ ] **Step 9: Commit**

```bash
git add clusters/platform hack/kratix/platform-infra-source.yaml hack/kratix/platform-values.yaml hack/kind/minio-install.yaml hack/argo/platform-values.yaml promises/business-unit/promise.yaml promises/business-unit/README.md
git commit -m "$(cat <<'EOF'
feat: layer cert-manager/kratix/minio as dependsOn-chained Flux Kustomizations

Splits clusters/platform/ into cert-manager/, kratix/, and minio/
subfolders, each its own Kustomization chained via spec.dependsOn, so
Flux enforces the same ordering the imperative Makefile targets used
to (cert-manager's webhooks before Kratix's chart, Kratix's
Namespace/Secret before MinIO). Makefile wiring follows in a
subsequent commit.
EOF
)"
```

---

### Task 2: Wire the Flux chain into `make up`, remove the imperative targets

**Files:**
- Modify: `Makefile:48` (remove `CERT_MANAGER_VERSION` var)
- Modify: `Makefile:186-220` (`up` target)
- Modify: `Makefile:229-244` (remove `cert-manager`, `minio`, `kratix-platform` targets)
- Modify: `Makefile:489` (`##@ Platform infra` section header)

**Interfaces:**
- Consumes: Flux `Kustomization` named `minio` in `flux-system` (produced by Task 1) - `up` waits on it by that exact name.
- Consumes: `make infra` (pre-existing target, unchanged) - `up` now calls it directly instead of the deleted `cert-manager`/`kratix-platform`/`minio` targets.

- [ ] **Step 1: Remove the now-unused `CERT_MANAGER_VERSION` variable**

In `Makefile`, remove line 48:

```makefile
CERT_MANAGER_VERSION ?= v1.15.0
```

(Delete the line entirely - `KRATIX_HELM_REPO` on the line below is still used by `kratix-worker`/`kratix-platform-destination` and stays.)

- [ ] **Step 2: Reorder and rewrite the `up` target**

Replace the full `up` target (from `.PHONY: up` through the line ending `echo "  make verify  # confirm the demo came up healthy"`):

```makefile
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
```

- [ ] **Step 3: Delete the imperative `cert-manager`, `minio`, and `kratix-platform` targets**

In `Makefile`, delete this whole block (it sits between the `clusters` target and the `FLUX_INSTALL_URL` variable comment):

```makefile
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
```

So that `clusters:` (ending `kind create cluster --name $(WORKER_CLUSTER) ...worker-config.yaml`) is immediately followed by the blank line and then the `FLUX_INSTALL_URL := ...` comment block.

- [ ] **Step 4: Rename the "Platform infra" section header**

In `Makefile`, replace:

```makefile
##@ Platform infra (Flux/GitOps prototype)
```

with:

```makefile
##@ Platform infra (Flux/GitOps)
```

(It's the real bootstrap path now, not a prototype.)

- [ ] **Step 5: Sanity-check the Makefile with a dry run**

```bash
make -n up 2>&1 | grep -E '^\[|cert-manager|kratix-platform|^minio'
```

Expected: the nine `[n/9] ...` echo lines in order, `flux-platform` and `infra` mentioned in step 3/4's echoes, and no output line referencing a target literally named `cert-manager`, `kratix-platform`, or `minio` (the strings `cert-manager`/`kratix`/`minio` inside the new step-4 echo text itself are fine and expected - this check is confirming the *targets* are gone, not the words).

```bash
grep -n "^cert-manager:\|^minio:\|^kratix-platform:\|CERT_MANAGER_VERSION" Makefile
```

Expected: no output (all four removed).

```bash
make help 2>&1 | grep -E "cert-manager|kratix-platform|^  minio"
```

Expected: no output - those three targets no longer appear in the help listing.

- [ ] **Step 6: Commit**

```bash
git add Makefile
git commit -m "$(cat <<'EOF'
feat: reorder make up around the Flux platform-infra chain

Installs Flux on the platform cluster before cert-manager/Kratix/MinIO
instead of after, then reconciles all three through make infra and
waits on the minio Kustomization. Removes the now-redundant
cert-manager/minio/kratix-platform imperative targets.
EOF
)"
```

---

### Task 3: Extend `make verify` with Kustomization health checks

**Files:**
- Modify: `Makefile:424-436` (`verify` target)

**Interfaces:**
- Consumes: Flux `Kustomization`s named `cert-manager`, `kratix`, `minio` in `flux-system` (produced by Task 1).

- [ ] **Step 1: Add the three Kustomization checks**

In `Makefile`, in the `verify` target, replace:

```makefile
	@echo "Checking cluster contexts..."; \
	kubectl --context $(PLATFORM_CTX) get nodes >/dev/null || { echo "FAIL: platform context $(PLATFORM_CTX) isn't responding"; exit 1; }; \
	kubectl --context $(WORKER_CTX) get nodes >/dev/null || { echo "FAIL: worker context $(WORKER_CTX) isn't responding"; exit 1; }; \
	echo "Checking Kratix pods..."; \
```

with:

```makefile
	@echo "Checking cluster contexts..."; \
	kubectl --context $(PLATFORM_CTX) get nodes >/dev/null || { echo "FAIL: platform context $(PLATFORM_CTX) isn't responding"; exit 1; }; \
	kubectl --context $(WORKER_CTX) get nodes >/dev/null || { echo "FAIL: worker context $(WORKER_CTX) isn't responding"; exit 1; }; \
	echo "Checking platform infra Kustomizations..."; \
	for k in cert-manager kratix minio; do \
		ready=$$(kubectl --context $(PLATFORM_CTX) get kustomization "$$k" -n flux-system -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null); \
		if [ "$$ready" != "True" ]; then echo "FAIL: Kustomization $$k (flux-system) is not Ready"; exit 1; fi; \
	done; \
	echo "Checking Kratix pods..."; \
```

- [ ] **Step 2: Confirm the recipe is still valid shell**

```bash
make -n verify >/dev/null && echo "make -n verify: OK (Makefile parses)"
```

Expected: `make -n verify: OK (Makefile parses)` - this only proves the target's shell is well-formed, not that the checks pass yet (they won't, until Task 5 actually installs the Flux chain).

- [ ] **Step 3: Run it for real against the live (not-yet-migrated) cluster**

```bash
make verify
```

Expected: **FAIL**, specifically with `FAIL: Kustomization cert-manager (flux-system) is not Ready` (this cluster still has cert-manager installed the old imperative way, per this task's live-cluster caveat above - the `cert-manager` Kustomization doesn't exist yet). This confirms the new check is correctly wired and fails for the right, expected reason - not a shell syntax error. Do not treat this failure as a bug to fix; it's expected until Task 5 stands up the new chain.

- [ ] **Step 4: Commit**

```bash
git add Makefile
git commit -m "$(cat <<'EOF'
test: check the platform-infra Kustomizations in make verify

Fails fast with a specific Kustomization name if cert-manager, kratix,
or minio never reconciled, instead of surfacing only as a downstream
symptom (pods not Running, Destinations not Ready).
EOF
)"
```

---

### Task 4: Rewrite the README's platform-infra documentation

**Files:**
- Modify: `README.md:85-126` (`## Platform infra (Flux/GitOps)` section)
- Modify: `README.md:401-446` (`## How this works` section)

**Interfaces:** None - documentation only.

- [ ] **Step 1: Rewrite the "Platform infra" section**

In `README.md`, replace the full section from `## Platform infra (Flux/GitOps)` through the paragraph ending `reconcile.fluxcd.io/requestedAt="$(date -u +%Y-%m-%dT%H:%M:%SZ)" --overwrite\` to force an immediate retry rather than waiting for the next interval).` (i.e. everything up to, but not including, `## Building a Promise`):

```markdown
## Platform infra (Flux/GitOps)

[`clusters/platform/`](clusters/platform) is the platform cluster's own infra - cert-manager,
Kratix, and MinIO - declared as layered Flux objects instead of the `kubectl apply`/`helm
upgrade --install` calls the Makefile used to run directly. It's reconciled by the same Flux
`flux-platform` installs (see "How this works" below) - not a second instance, and not the
same thing as the Flux `kratix-worker`/`kratix-platform-destination` use to deliver Promise
workloads to Destinations.

```bash
make infra   # push clusters/platform as an OCI artifact + point Flux at it
```

`infra-push` bundles the folder into the local registry (`oci://localhost:5001/platform-infra`)
rather than requiring a git remote, which keeps the loop local: edit a file under
`clusters/platform/`, `make infra`, and Flux reconciles the change in place - no push to
`origin` needed. `infra-apply` applies [`hack/kratix/platform-infra-source.yaml`](hack/kratix/platform-infra-source.yaml)
(the `OCIRepository` + three `Kustomization`s pointing at it) and patches the tag to whatever
was just pushed. Swapping the `OCIRepository` for a `GitRepository` against a real git remote
later is a one-object change - `clusters/platform/`'s contents don't need to move.

`make up` runs `infra` itself as part of bringing up a fresh cluster (see "How this works"
below) - there's no imperative cert-manager/Kratix/MinIO install to hand off from anymore.
`clusters/platform/` has three subfolders, each its own `Kustomization`, chained with
`dependsOn` so Flux only moves on to the next layer once the previous one is fully `Ready`:

1. `cert-manager/` - the `jetstack` Helm repo + a `cert-manager` `HelmRelease` (required by
   the Kratix chart's webhooks)
2. `kratix/` - depends on `cert-manager` - the `syntasso` Helm repo + a `kratix`
   `HelmRelease` (the same values the old `kratix-platform` Makefile target passed via
   `-f hack/kratix/platform-values.yaml`, now inlined directly into the `HelmRelease`)
3. `minio/` - depends on `kratix` - the dev-only MinIO manifest, which needs the
   `kratix-platform-system` Namespace and `default/minio-credentials` Secret the Kratix
   chart creates (Helm refuses to adopt resources it didn't create, so this really does have
   to come after, not just conventionally)

`make verify` checks all three `Kustomization`s report `Ready=True` as part of confirming the
platform came up healthy.
```

- [ ] **Step 2: Rewrite the "How this works" numbered list**

In `README.md`, replace the full section from `## How this works` through the paragraph ending `...whatever the chart last pinned.` (i.e. everything up to, but not including, the line `The \`kratix\` CLI (fetched to \`bin/kratix\`, git-ignored) is what scaffolds new`):

```markdown
## How this works

Nothing is cloned or vendored - `make up` runs `doctor` (the preflight checks
described above), then `deps` (installs whatever's missing for your OS), then
`registry-start`, then, in order:

1. `clusters` - two kind clusters (`hack/kind/{platform,worker}-config.yaml`: upstream
   [Kratix](https://github.com/syntasso/kratix)'s port-mappings plus a
   `containerdConfigPatches` block for the local registry)
2. `registry-configure` - wires `kind-registry` into both clusters' containerd
   ([kind's documented local-registry pattern](https://kind.sigs.k8s.io/docs/user/local-registry/))
3. `flux-platform` - installs Flux on the platform cluster (pinned to `FLUX_VERSION` - see the
   Makefile comment on that var for why not literal-latest), ahead of everything it's about
   to reconcile
4. `infra` - pushes [`clusters/platform/`](clusters/platform) as an OCI artifact to the local
   registry and points the just-installed Flux at it, then `make up` waits for the `minio`
   `Kustomization` to report Ready. This is what actually installs cert-manager, Kratix, and
   MinIO - see "Platform infra" above for the three-layer `dependsOn` chain. Kratix is
   configured (inlined into `clusters/platform/kratix/kratix-release.yaml`) to point at MinIO
   (not running yet - nothing needs it live until a Promise pipeline actually writes to it)
   and pre-register both `worker-1` and `platform-cluster` as `Destination`s.
5. `kratix-worker` - installs Flux on the worker cluster (`flux-worker`, pinned to
   `FLUX_VERSION`), then registers the worker cluster as a Destination via the companion
   `syntasso/kratix-destination` chart (`installFlux=false`, since Flux is already there),
   pointed at the platform's MinIO over the kind docker network (the two clusters are
   separate Docker containers, so this uses the platform node's container IP, not a
   Kubernetes Service DNS name)
6. `kratix-platform-destination` - same idea (`installFlux=false` `kratix-destination` chart;
   `flux-platform` is already installed by step 3, so this is idempotent), this time on the
   platform cluster itself, pointed at its own in-cluster MinIO Service (no docker-network IP
   needed) - this is what registers `kind-platform` itself as the `platform-cluster`
   Destination
7. `metrics-server` on both clusters

The `kratix-destination` chart is marked deprecated upstream (still published and
functional, just not where Syntasso is investing further) - it's used here for the
Destination-registration/Bucket/Kustomization wiring, which is far less bespoke code than
reimplementing that by hand, but *not* for installing Flux itself (`installFlux=false`):
Flux is installed explicitly by `flux-worker`/`flux-platform` so its version is under our
control rather than whatever the chart last pinned.
```

- [ ] **Step 3: Check for leftover stale references**

```bash
grep -n "additive, not yet wired\|two full cert-manager installs\|GitOps prototype" README.md
```

Expected: no output (all three phrases removed by the rewrite above).

```bash
grep -n "hack/kratix/platform-values.yaml\|hack/kind/minio-install.yaml" README.md
```

Expected: no output (both paths point to files that no longer exist after Task 1).

- [ ] **Step 4: Commit**

```bash
git add README.md
git commit -m "$(cat <<'EOF'
docs: describe the Flux-managed platform infra chain in README

Replaces the "additive, not yet wired into make up" / dual-install
collision caveats (no longer applicable - there's no imperative
install left to collide with) with the actual three-layer
dependsOn chain and updated make up step numbering.
EOF
)"
```

---

### Task 5: Fresh-cluster end-to-end validation

**Files:** None - validation only, no code changes.

**Interfaces:** None.

**This task requires tearing down and recreating both kind clusters, which pulls container images from the network.** If you're running in an environment where `docker pull` hangs or is unreliable, run this task on a machine where it isn't before treating the plan as done - don't force it through in a sandboxed environment that can't complete the pulls.

- [ ] **Step 1: Confirm a clean slate**

```bash
git status
```

Expected: clean (Tasks 1-4 all committed).

- [ ] **Step 2: Tear down and recreate both clusters**

```bash
make down
make up
```

Expected: completes with the new `[1/9]` through `[9/9]` step banners (no `[3/10] Installing cert-manager...` or similar from the old numbering), and specifically does not hang or fail at `[4/9] Reconciling platform infra...`.

- [ ] **Step 3: Run `make verify`**

```bash
make verify
```

Expected: `All checks passed - the demo is healthy.` - this time including the three Kustomization checks added in Task 3, now passing for real.

- [ ] **Step 4: Confirm the layering actually blocked, not just eventually converged**

```bash
kubectl --context kind-platform get kustomization -n flux-system cert-manager kratix minio -o custom-columns=NAME:.metadata.name,READY:.status.conditions[-1].status
kubectl --context kind-platform get helmrelease -n flux-system kratix -o custom-columns=NAME:.metadata.name,READY:.status.conditions[-1].status,CHART:.status.history[0].chartVersion
```

Expected: all three Kustomizations `Ready=True`; the `kratix` HelmRelease `Ready=True` with a chart version populated (confirms it actually installed via Flux, not left over from a stale imperative release).

- [ ] **Step 5: Confirm `make infra` standalone still works for the local edit loop**

```bash
make infra
```

Expected: completes without error (idempotent re-push/re-apply of an unchanged `clusters/platform/`) - confirms the "edit a file under `clusters/platform/`, run `make infra`" loop documented in the README still works post-migration.

- [ ] **Step 6: Confirm `dependsOn` actually blocks, not just eventually converges**

Temporarily break the `kratix` layer with a chart version that doesn't exist:

```bash
sed -i.bak 's/version: "\*"/version: "0.0.0-does-not-exist"/' clusters/platform/kratix/kratix-release.yaml
make infra
sleep 15
kubectl --context kind-platform get kustomization -n flux-system cert-manager kratix minio -o custom-columns=NAME:.metadata.name,READY:.status.conditions[-1].status,MESSAGE:.status.conditions[-1].message
```

Expected: `cert-manager` still `Ready=True` (unaffected), `kratix` `Ready=False` (chart version not found), `minio` still `Ready=Unknown` or shows a `dependsOn` message rather than attempting to apply - confirming the chain actually blocks on failure instead of applying every layer regardless.

Revert the break and reconcile back to healthy:

```bash
mv clusters/platform/kratix/kratix-release.yaml.bak clusters/platform/kratix/kratix-release.yaml
make infra
kubectl --context kind-platform wait --for=condition=Ready --timeout=5m -n flux-system kustomization/minio
```

Expected: `kustomization.kustomize.toolkit.fluxcd.io/minio condition met` - back to healthy, matching Step 3/4's state. This step makes no repo changes (the `.bak` revert restores the exact committed file) - nothing to commit.
