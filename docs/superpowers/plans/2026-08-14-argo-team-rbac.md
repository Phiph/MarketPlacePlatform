# Per-Team Argo CD RBAC Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Every team gets its own read-only Argo CD `AppProject` (created declaratively by the `team` Promise's pipeline, delivered by Flux like everything else) and a scoped API token the broker can later use on that team's behalf. Depends on `docs/superpowers/plans/2026-08-14-argo-install.md` (Argo CD running on `kind-platform`, `worker-1` registered) - already executed and verified.

**Architecture:** `team-configure`'s pipeline gains a second output alongside the `Namespace` it already writes: an `AppProject` scoped to that team's own worker-side namespace, with a `viewer` role permitting only `applications, get` and `logs, get` - no sync/delete, and `namespaceResourceWhitelist`/`clusterResourceWhitelist` both empty as a second, independent guarantee that this project could never apply anything even if something were misconfigured later. A new Makefile target then mints one API token per team against that `AppProject`'s role and stores it as a `Secret` in the team's own platform-side namespace (`team-<team>`) - readable later by the broker's existing per-team impersonated client, the same one used for every other Kubernetes call the broker makes, requiring no new broker privilege.

**Tech Stack:** Python (Kratix pipeline, pytest), `kubectl`/`curl`/`yq` (Makefile provisioning), Argo CD's REST API.

## Global Constraints

- Every `AppProject` lives in the `argocd` namespace (Argo CD's control-plane namespace) - `AppProject` is namespace-scoped and must live there; this is different from where the per-request `Application` objects will live in the next plan (a later plan's concern, not this one).
- `AppProject.metadata.name` is the team name itself (e.g. `checkout`), distinct from the Kubernetes namespace `team-checkout`.
- The role is named `viewer`, granting exactly `p, proj:<team>:viewer, applications, get, <team>/*, allow` and `p, proj:<team>:viewer, logs, get, <team>/*, allow` - no other verbs, ever.
- `namespaceResourceWhitelist: []` and `clusterResourceWhitelist: []` on every `AppProject` - defense in depth for "Argo never applies anything," independent of any Application's own sync policy.
- The minted token is stored as a `Secret` named `argocd-team-token` (key `token`) in that team's own platform-side namespace (`team-<team>`) - readable by that team's own Kubernetes Group via the existing `edit` ClusterRole binding (`promises/business-unit/.../team-rbac.yaml`), and by the broker's existing per-team impersonated client.
- Known upstream quirk (tracked as [argoproj/argo-cd#2718](https://github.com/argoproj/argo-cd/issues/2718)): a freshly-minted token briefly lives in `AppProject.spec.roles[].jwtTokens` before Argo's controller moves it to `status.jwtTokensByRole`; only entries in `status` survive a subsequent declarative re-apply of the `AppProject`. The provisioning step must poll for the token's presence in `status.jwtTokensByRole` before considering it durable - see Task 3.

---

### Task 1: `team-configure` pipeline emits a scoped `AppProject`

**Files:**
- Modify: `promises/team/workflows/resource/configure/team-configure/kratix-guide-team-resource-pipeline/scripts/pipeline.py`
- Create: `promises/team/workflows/resource/configure/team-configure/kratix-guide-team-resource-pipeline/scripts/test_pipeline.py`

**Interfaces:**
- Consumes: `namespace_name(team)` (already defined in `pipeline.py:8-9`).
- Produces: `build_app_project(team: str) -> dict`, returning a manifest whose `metadata.name` equals `team` and whose `spec.destinations[0].namespace` equals `namespace_name(team)`. Later plans (the `container-configure` pipeline, the broker) rely on this exact naming: `AppProject` name == team name, in namespace `argocd`.

- [ ] **Step 1: Write the failing tests**

`promises/team/workflows/resource/configure/team-configure/kratix-guide-team-resource-pipeline/scripts/test_pipeline.py`:

```python
from pipeline import build_app_project


def test_build_app_project_scopes_destinations_to_one_team():
    project = build_app_project("checkout")

    assert project["apiVersion"] == "argoproj.io/v1alpha1"
    assert project["kind"] == "AppProject"
    assert project["metadata"] == {"name": "checkout", "namespace": "argocd"}
    assert project["spec"]["destinations"] == [{"name": "worker-1", "namespace": "team-checkout"}]
    assert project["spec"]["sourceNamespaces"] == ["team-checkout"]


def test_build_app_project_never_allows_sync():
    project = build_app_project("checkout")

    assert project["spec"]["namespaceResourceWhitelist"] == []
    assert project["spec"]["clusterResourceWhitelist"] == []


def test_build_app_project_role_is_read_only():
    project = build_app_project("checkout")
    roles = project["spec"]["roles"]

    assert len(roles) == 1
    assert roles[0]["name"] == "viewer"
    assert roles[0]["policies"] == [
        "p, proj:checkout:viewer, applications, get, checkout/*, allow",
        "p, proj:checkout:viewer, logs, get, checkout/*, allow",
    ]


def test_build_app_project_uses_team_name_not_namespace():
    project = build_app_project("payments")

    assert project["metadata"]["name"] == "payments"
    assert project["metadata"]["namespace"] == "argocd"
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
cd promises/team/workflows/resource/configure/team-configure/kratix-guide-team-resource-pipeline/scripts
pip install pytest kratix-sdk
python -m pytest test_pipeline.py -v
```

Expected: `ImportError: cannot import name 'build_app_project'`.

- [ ] **Step 3: Implement `build_app_project` and wire it into `main()`**

Add to `pipeline.py`, after the existing `LABEL_TEAM` constant and before `def main():`:

```python
# Where Argo CD itself runs - AppProject is namespace-scoped and must live
# in Argo's own control-plane namespace, unlike the Namespace this pipeline
# also writes (which lives wherever the team's own workloads do). See
# docs/superpowers/specs/2026-08-14-container-workload-logs-design.md,
# "RBAC".
#
# Naming source of truth: the Makefile's ARGO_WORKER_CLUSTER_NAME and
# ARGO_ROLE variables (argo-provision-teams target) must compute/hold the
# identical strings - the two can't share code across languages, so both
# sides carry a comment pointing at the other, same convention as
# namespace_name()'s cross-reference to tenant.go's Namespace() below.
ARGO_NAMESPACE = "argocd"
ARGO_WORKER_CLUSTER_NAME = "worker-1"
ARGO_ROLE = "viewer"


def build_app_project(team: str) -> dict:
    ns = namespace_name(team)
    return {
        "apiVersion": "argoproj.io/v1alpha1",
        "kind": "AppProject",
        "metadata": {"name": team, "namespace": ARGO_NAMESPACE},
        "spec": {
            # Wildcarded rather than pinned to this repo's URL: which
            # source(s) an Application in this project may reference is a
            # per-Application concern (see the next plan), not something
            # this team-scoping layer needs to constrain.
            "sourceRepos": ["*"],
            "destinations": [{"name": ARGO_WORKER_CLUSTER_NAME, "namespace": ns}],
            "sourceNamespaces": [ns],
            # Argo never applies anything in this design (Flux is the sole
            # applier - see the design doc's "Architecture"). Empty
            # whitelists are a second, independent guarantee of that: even
            # a misconfigured Application in this project has nothing it's
            # allowed to sync.
            "namespaceResourceWhitelist": [],
            "clusterResourceWhitelist": [],
            "roles": [
                {
                    "name": ARGO_ROLE,
                    "description": f"Read-only ({team}): application status and pod logs only, no sync/delete.",
                    "policies": [
                        f"p, proj:{team}:{ARGO_ROLE}, applications, get, {team}/*, allow",
                        f"p, proj:{team}:{ARGO_ROLE}, logs, get, {team}/*, allow",
                    ],
                }
            ],
        },
    }
```

Modify `main()` to also write this output and record it in status:

```python
    namespace = {
        "apiVersion": "v1",
        "kind": "Namespace",
        "metadata": {
            "name": ns,
            "labels": {
                "capsule.clastix.io/tenant": business_unit,
                LABEL_TEAM: team,
            },
        },
    }
    sdk.write_output("namespace.yaml", yaml.safe_dump(namespace).encode("utf-8"))

    app_project = build_app_project(team)
    sdk.write_output("app-project.yaml", yaml.safe_dump(app_project).encode("utf-8"))

    status = ks.Status()
    status.set("namespace", ns)
    status.set("businessUnit", business_unit)
    status.set("argoProject", team)
    sdk.write_status(status)
```

(Only the `sdk.write_output("app-project.yaml", ...)` line and the `status.set("argoProject", team)` line are new - the rest of `main()` is unchanged.)

- [ ] **Step 4: Run tests to verify they pass**

```bash
python -m pytest test_pipeline.py -v
```

Expected: all 4 tests `PASSED`.

- [ ] **Step 5: Commit**

```bash
git add promises/team/workflows/resource/configure/team-configure/kratix-guide-team-resource-pipeline/scripts/pipeline.py \
        promises/team/workflows/resource/configure/team-configure/kratix-guide-team-resource-pipeline/scripts/test_pipeline.py
git commit -m "team: pipeline emits a per-team read-only Argo CD AppProject"
```

---

### Task 2: Enable Applications-in-any-namespace on Argo CD

**Files:**
- Modify: `hack/argo/platform-values.yaml`

**Interfaces:**
- Produces: the `argocd-cm` `ConfigMap` gains `application.namespaces: team-*`, which is what lets an `Application` object (created by the next plan) live inside a `team-<name>` namespace instead of only inside `argocd` - required for that plan's RBAC to mean anything.

- [ ] **Step 1: Add the config**

Modify `hack/argo/platform-values.yaml`, adding a `cm` key alongside the existing `params`:

```yaml
configs:
  params:
    # Plain HTTP inside the cluster - matches this repo's existing "insecure:
    # true" pattern for MinIO (hack/kratix/platform-values.yaml); there's no
    # cert-manager-issued cert for argocd-server and none is needed for local
    # dev. `make argo-ui` (Task 4) reaches it via port-forward.
    server.insecure: true
  cm:
    # Lets an Application object live inside a team's own namespace
    # (team-<name>) instead of only inside argocd's own namespace, so the
    # same Kubernetes RBAC that already governs everything else in that
    # namespace (promises/business-unit's team-rbac.yaml GlobalTenantResource)
    # also governs who can read that team's Application objects directly via
    # kubectl/the K8s API. Wildcarded once here; each AppProject's own
    # sourceNamespaces field (see promises/team's pipeline) is what actually
    # restricts a given project to its own one namespace - this setting only
    # turns the feature on server-wide. See
    # docs/superpowers/specs/2026-08-14-container-workload-logs-design.md,
    # "RBAC".
    application.namespaces: "team-*"
```

- [ ] **Step 2: Apply and verify**

```bash
make argo-install
kubectl --context kind-platform -n argocd get cm argocd-cm -o jsonpath='{.data.application\.namespaces}'
```

Expected output: `team-*`

- [ ] **Step 3: Commit**

```bash
git add hack/argo/platform-values.yaml
git commit -m "infra: allow Argo CD Applications to live in team-* namespaces"
```

---

### Task 3: Mint and store a per-team Argo CD API token

**Files:**
- Modify: `Makefile` (`argo-provision-teams` target, wired into `demo-setup`)

**Interfaces:**
- Consumes: `broker/config/teams.yaml` (existing, lists every team), the `AppProject` from Task 1 (by team name), Argo's `viewer` role (Task 1).
- Produces: a `Secret` `argocd-team-token` (key `token`) in each `team-<name>` namespace on `kind-platform`. This is the credential the broker will read in a later plan - a fixed name/key pair (`argocd-team-token` / `token`), not something later plans need to rediscover.

- [ ] **Step 1: Add the target**

Add to the `##@ Argo CD` section, after `argo-register-worker`:

```makefile
.PHONY: argo-provision-teams
argo-provision-teams: ## Mint a scoped Argo CD API token per team (broker/config/teams.yaml) and store it as a Secret in that team's namespace
	@admin_pw=$$(kubectl --context $(PLATFORM_CTX) -n $(ARGO_NAMESPACE) get secret argocd-initial-admin-secret -o jsonpath='{.data.password}' 2>/dev/null | base64 -d); \
	if [ -z "$$admin_pw" ]; then echo "argocd-initial-admin-secret not found (already rotated? see make argo-admin-password)"; exit 1; fi; \
	kubectl --context $(PLATFORM_CTX) -n $(ARGO_NAMESPACE) port-forward svc/argocd-server 8080:443 >/tmp/argo-provision-teams-pf.log 2>&1 & \
	pf_pid=$$!; \
	trap "kill $$pf_pid 2>/dev/null" EXIT; \
	sleep 2; \
	session=$$(curl -sk -X POST https://localhost:8080/api/v1/session \
		-H 'Content-Type: application/json' \
		-d "{\"username\":\"admin\",\"password\":\"$$admin_pw\"}" | yq -p json -r '.token'); \
	if [ -z "$$session" ] || [ "$$session" = "null" ]; then echo "Failed to log into Argo CD"; exit 1; fi; \
	yq '.businessUnits | to_entries | .[] | .value.teams | keys | .[]' broker/config/teams.yaml | while read -r team; do \
		ns=team-$$team; \
		if kubectl --context $(PLATFORM_CTX) -n "$$ns" get secret argocd-team-token >/dev/null 2>&1; then \
			echo "argocd-team-token already exists in $$ns, skipping $$team"; \
			continue; \
		fi; \
		echo "Waiting for AppProject $$team..."; \
		found=""; \
		for i in $$(seq 1 60); do \
			kubectl --context $(PLATFORM_CTX) -n $(ARGO_NAMESPACE) get appproject "$$team" >/dev/null 2>&1 && found=1 && break; \
			sleep 2; \
		done; \
		if [ -z "$$found" ]; then echo "AppProject $$team never appeared after 120s"; exit 1; fi; \
		echo "Minting Argo CD token for team $$team..."; \
		token=$$(curl -sk -X POST "https://localhost:8080/api/v1/projects/$$team/roles/$(ARGO_ROLE)/token" \
			-H "Authorization: Bearer $$session" | yq -p json -r '.token'); \
		if [ -z "$$token" ] || [ "$$token" = "null" ]; then echo "Failed to mint a token for $$team"; exit 1; fi; \
		echo "Waiting for the token to be recorded in AppProject status (argoproj/argo-cd#2718 - a Flux reconcile of the declarative AppProject before this would otherwise wipe an unrecorded token)..."; \
		recorded=""; \
		for i in $$(seq 1 30); do \
			count=$$(kubectl --context $(PLATFORM_CTX) -n $(ARGO_NAMESPACE) get appproject "$$team" -o jsonpath="{.status.jwtTokensByRole.$(ARGO_ROLE).items}" 2>/dev/null | yq -p json 'length' 2>/dev/null); \
			[ -n "$$count" ] && [ "$$count" != "0" ] && recorded=1 && break; \
			sleep 1; \
		done; \
		if [ -z "$$recorded" ]; then echo "Token for $$team never appeared in AppProject status after 30s"; exit 1; fi; \
		kubectl --context $(PLATFORM_CTX) -n "$$ns" create secret generic argocd-team-token --from-literal=token="$$token"; \
		echo "Stored argocd-team-token in $$ns"; \
	done
```

Add, alongside the other `ARGO_*` variables defined in the argo-install plan (near `ARGO_WORKER_CLUSTER_NAME`):

```makefile
# Naming source of truth: promises/team's pipeline.py's ARGO_ROLE constant
# must hold the identical string - see that file's comment for why this
# can't be shared code across languages.
ARGO_ROLE := viewer
```

- [ ] **Step 2: Wire into `demo-setup`**

Modify the `demo-setup` target's recipe (`Makefile:376-416`) to call the new target immediately after `broker-provision-teams`:

```makefile
	$(MAKE) --no-print-directory broker-provision-teams
	$(MAKE) --no-print-directory argo-provision-teams
	@echo "Waiting for team-checkout's namespace (the example project/environment live there)..."
```

(Only the `argo-provision-teams` line is new, inserted between the existing `broker-provision-teams` call and the `echo` that follows it.)

- [ ] **Step 3: Run it and verify**

```bash
make argo-provision-teams
```

Expected: `Stored argocd-team-token in team-payments` and `Stored argocd-team-token in team-checkout` (or `already exists, skipping` if re-run).

```bash
kubectl --context kind-platform -n team-checkout get secret argocd-team-token -o jsonpath='{.data.token}' | base64 -d | cut -c1-20
```

Expected: the start of a JWT (`eyJhbGciOiJ...`).

```bash
kubectl --context kind-platform -n argocd get appproject checkout -o jsonpath='{.status.jwtTokensByRole.viewer.items}'
```

Expected: a non-empty JSON array with one entry (`iat`, and an `id` if the chart version assigns one).

- [ ] **Step 4: Verify the boundary actually holds**

This is the acceptance check for the whole plan - team A's token must not work against team B's project.

```bash
checkout_token=$(kubectl --context kind-platform -n team-checkout get secret argocd-team-token -o jsonpath='{.data.token}' | base64 -d)
kubectl --context kind-platform -n argocd port-forward svc/argocd-server 8080:443 &
PF_PID=$!
sleep 2
echo "Own project (expect 200/success):"
curl -sk -o /dev/null -w '%{http_code}\n' https://localhost:8080/api/v1/projects/checkout \
  -H "Authorization: Bearer $checkout_token"
echo "Someone else's project (expect 403):"
curl -sk -o /dev/null -w '%{http_code}\n' https://localhost:8080/api/v1/projects/payments \
  -H "Authorization: Bearer $checkout_token"
kill $PF_PID
```

Expected: `200` for `checkout`'s own project, `403` for `payments`.

- [ ] **Step 5: Commit**

```bash
git add Makefile
git commit -m "team: mint and store a per-team scoped Argo CD API token"
```

---

### Task 4: Docs

**Files:**
- Modify: `README.md`
- Modify: `promises/team/README.md`

**Interfaces:** None (documentation only).

- [ ] **Step 1: Document in the root README**

Add a short paragraph after the existing "Argo CD" content added by the previous plan (or, if that plan didn't touch `README.md` beyond the Makefile-targets table, add one near the "Multi-tenancy" section), explaining: each team gets a read-only Argo CD `AppProject` (created by `promises/team`'s pipeline) and an API token stored as `argocd-team-token` in its own namespace, scoped to that project's `viewer` role (status/logs only, no sync/delete) - provisioned automatically by `make demo-setup` / `make up`.

- [ ] **Step 2: Update `promises/team/README.md`**

Add a bullet describing the new `AppProject` output alongside the existing description of the `Namespace` output, referencing `docs/superpowers/specs/2026-08-14-container-workload-logs-design.md` for the full design.

- [ ] **Step 3: Commit**

```bash
git add README.md promises/team/README.md
git commit -m "docs: document per-team Argo CD RBAC provisioning"
```
