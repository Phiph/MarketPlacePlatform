# Project Namespace Projection to Worker Destinations Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `Database`/`Container` requests land their worker-cluster workload in the requesting project's own namespace (`project-{team}-{project}-{environment}`) instead of a shared `default` namespace.

**Architecture:** `promises/environment`'s pipeline already writes the one canonical `Namespace` object for a project-environment; widen its `destinationSelectors` so that object also lands on the worker destination (Kratix's selector list is OR-matched). `database`'s and `container`'s resource pipelines then read `resource.get_namespace()` (the request's own namespace on the platform cluster, already correct) instead of hardcoding `"default"`.

**Tech Stack:** Kratix Promises (YAML), Python pipelines (`kratix_sdk`, PyYAML), pytest for pipeline unit tests, kind + Flux for the local demo clusters.

**Spec:** `docs/superpowers/specs/2026-09-01-project-namespace-projection-design.md`

## Global Constraints

- Namespace naming stays exactly `project-{team}-{project}-{environment}`, computed identically by `broker/internal/tenant.ProjectEnvironmentNamespace()` and `promises/environment`'s `namespace_name()` — this plan does not touch either of those computations, only where the resulting name is *used*.
- No RBAC, Capsule `Tenant`, or `LimitRange` changes on the worker cluster — namespace creation only (spec "Non-goals").
- No changes to the broker, to the `Database`/`Container` CRD schemas, to the Zalando Postgres operator config, or to `promises/team`/`promises/business-unit` (spec "Components").
- No `kratix.io/promise-version` bump on `promises/environment/promise.yaml` — this is a local dev/demo platform and the spec's non-goals explicitly defer migration tooling to `make down && make up`.
- Pipeline modules that gain new tests must follow the testability convention `promises/container`'s pipeline already established: `import kratix_sdk` / `import yaml` happen **inside `main()`**, not at module level, so `test_pipeline.py` can import the module's pure functions without `kratix_sdk` installed. Verified locally via `python3 -m pytest test_pipeline.py -v` run from inside each pipeline's `scripts/` directory (no `pytest` on `PATH` in this environment — use `python3 -m pytest`).

---

### Task 1: Widen `promises/environment`'s destinationSelectors to include the worker destination

**Files:**
- Modify: `promises/environment/promise.yaml:16-22`
- Modify: `promises/environment/README.md` (namespace bullet + "Try it" section)

**Interfaces:**
- Consumes: nothing from other tasks.
- Produces: the `Namespace` object `environment-configure` already writes (name `project-{team}-{project}-{environment}`) now gets scheduled to Destination `worker-1` (label `environment: dev`) in addition to `platform-cluster` (label `environment: platform`). Tasks 3 and 4 depend on this namespace existing on the worker cluster at runtime (not at test time — their unit tests don't touch a live cluster).

- [ ] **Step 1: Edit `promise.yaml`'s `destinationSelectors`**

In `promises/environment/promise.yaml`, replace:

```yaml
spec:
  # Same reasoning as promises/team/promise.yaml: the output Namespace has to
  # land on the platform cluster, since that's where Capsule's webhooks and
  # the marketplace broker run.
  destinationSelectors:
  - matchLabels:
      environment: platform
```

with:

```yaml
spec:
  # The output Namespace lands on BOTH clusters. Platform: same reasoning
  # as promises/team/promise.yaml - that's where Capsule's webhooks and the
  # marketplace broker run. Worker: so database/container (and any future
  # workload-producing Promise) have a matching namespace to land their
  # output in, instead of falling back to "default" - see
  # docs/superpowers/specs/2026-09-01-project-namespace-projection-design.md.
  # destinationSelectors use OR semantics across list entries: a
  # Destination matching ANY one of these receives the scheduled output.
  destinationSelectors:
  - matchLabels:
      environment: platform
  - matchLabels:
      environment: dev
```

- [ ] **Step 2: Verify the YAML parses with exactly two selector entries**

Run from the repo root:

```bash
python3 -c "
import yaml
doc = yaml.safe_load(open('promises/environment/promise.yaml'))
selectors = doc['spec']['destinationSelectors']
expected = [{'matchLabels': {'environment': 'platform'}}, {'matchLabels': {'environment': 'dev'}}]
assert selectors == expected, selectors
print('OK', selectors)
"
```

Expected output: `OK [{'matchLabels': {'environment': 'platform'}}, {'matchLabels': {'environment': 'dev'}}]`

- [ ] **Step 3: Update `promises/environment/README.md`**

Replace this bullet (the first one in the top file-listing block, describing `promise.yaml`):

```markdown
- `promise.yaml` - the `Environment` CRD (`demo.kratix.io/v1alpha1`), with three required
  fields: `spec.project` (name of an already-created `Project`, same namespace),
  `spec.team`, `spec.businessUnit`.
```

with:

```markdown
- `promise.yaml` - the `Environment` CRD (`demo.kratix.io/v1alpha1`), with three required
  fields: `spec.project` (name of an already-created `Project`, same namespace),
  `spec.team`, `spec.businessUnit`. `spec.destinationSelectors` schedules this Promise's
  output to **both** the platform cluster and the worker cluster (`worker-1`) - see
  `docs/superpowers/specs/2026-09-01-project-namespace-projection-design.md` - so the
  `Namespace` the pipeline below writes lands on both, giving `database`/`container`
  requests into this project-environment a matching namespace on the worker cluster too.
```

Then in the "Try it" section, replace:

```bash
kubectl --context kind-platform get environments.demo.kratix.io -n team-checkout dev -w
kubectl --context kind-platform get ns project-checkout-checkout-service-dev --show-labels
kubectl --context kind-platform get rolebindings -n project-checkout-checkout-service-dev
```

with:

```bash
kubectl --context kind-platform get environments.demo.kratix.io -n team-checkout dev -w
kubectl --context kind-platform get ns project-checkout-checkout-service-dev --show-labels
kubectl --context kind-platform get rolebindings -n project-checkout-checkout-service-dev
kubectl --context kind-worker get ns project-checkout-checkout-service-dev --show-labels
```

- [ ] **Step 4: Commit**

```bash
git add promises/environment/promise.yaml promises/environment/README.md
git commit -m "feat: project environment namespace to the worker destination"
```

---

### Task 2: Make `promises/environment`'s pipeline testable and add a regression test

**Files:**
- Modify: `promises/environment/workflows/resource/configure/environment-configure/kratix-guide-environment-resource-pipeline/scripts/pipeline.py`
- Create: `promises/environment/workflows/resource/configure/environment-configure/kratix-guide-environment-resource-pipeline/scripts/test_pipeline.py`

**Interfaces:**
- Consumes: nothing from other tasks.
- Produces: `build_namespace(team: str, project: str, environment: str, business_unit: str) -> dict` — a module-level function other tests/tasks can import from `pipeline`. `namespace_name(team, project, environment) -> str` keeps its existing name/signature.

This task doesn't change the pipeline's *output* (still the same `Namespace` shape) — it only extracts the manifest-building logic into a testable function, following the pattern `promises/container`'s pipeline already uses (imports moved inside `main()` so the module is importable without `kratix_sdk` installed).

- [ ] **Step 1: Write the failing test**

Create `test_pipeline.py`:

```python
from pipeline import build_namespace, namespace_name


def test_namespace_name():
    assert namespace_name("team-a", "checkout", "dev") == "project-team-a-checkout-dev"


def test_build_namespace():
    namespace = build_namespace("team-a", "checkout", "dev", "acme-corp")

    assert namespace["apiVersion"] == "v1"
    assert namespace["kind"] == "Namespace"
    assert namespace["metadata"] == {
        "name": "project-team-a-checkout-dev",
        "labels": {
            "capsule.clastix.io/tenant": "acme-corp",
            "marketplace.kratix.io/team": "team-a",
            "marketplace.kratix.io/project": "checkout",
            "marketplace.kratix.io/environment": "dev",
        },
    }
```

- [ ] **Step 2: Run the test to verify it fails**

Run (from the `scripts/` directory containing `pipeline.py`):

```bash
cd promises/environment/workflows/resource/configure/environment-configure/kratix-guide-environment-resource-pipeline/scripts
python3 -m pytest test_pipeline.py -v
```

Expected: `ImportError: cannot import name 'build_namespace' from 'pipeline'` (and also fails to collect because `pipeline.py` currently does `import kratix_sdk as ks` at module level, which isn't installed here — that import error is expected too).

- [ ] **Step 3: Refactor `pipeline.py`**

Replace the full file content with:

```python
def namespace_name(team: str, project: str, environment: str) -> str:
    return f"project-{team}-{project}-{environment}"


# Read by the shared GlobalTenantResource (see promises/business-unit's
# team-rbac.yaml) to derive this namespace's owning team's Kubernetes Group -
# unchanged from how promises/team's Namespace output uses the same label.
LABEL_TEAM = "marketplace.kratix.io/team"

# Traceability only, not read by any RBAC mechanism.
LABEL_PROJECT = "marketplace.kratix.io/project"
LABEL_ENVIRONMENT = "marketplace.kratix.io/environment"


# Only ever builds a Namespace - never a Tenant, same separation
# promises/team's pipeline relies on (see that Promise's README,
# "Provisioning order matters" in promises/business-unit/README.md) to
# stay safe to ship declaratively via Flux: the referenced business
# unit's Tenant is expected to already exist.
def build_namespace(team: str, project: str, environment: str, business_unit: str) -> dict:
    return {
        "apiVersion": "v1",
        "kind": "Namespace",
        "metadata": {
            "name": namespace_name(team, project, environment),
            "labels": {
                "capsule.clastix.io/tenant": business_unit,
                LABEL_TEAM: team,
                LABEL_PROJECT: project,
                LABEL_ENVIRONMENT: environment,
            },
        },
    }


def main():
    import kratix_sdk as ks
    import yaml

    sdk = ks.KratixSDK()
    resource = sdk.read_resource_input()
    environment = resource.get_name()
    project = resource.get_value("spec.project")
    # team/businessUnit are broker-owned fields (see promise.yaml) - this
    # pipeline just trusts whatever the request carries, the same way
    # promises/team's pipeline trusts spec.businessUnit: enforcing that
    # trust is the broker's job (POST /api/environments composes these
    # itself), not this pipeline's.
    team = resource.get_value("spec.team")
    business_unit = resource.get_value("spec.businessUnit")

    namespace = build_namespace(team, project, environment, business_unit)
    ns = namespace["metadata"]["name"]
    sdk.write_output("namespace.yaml", yaml.safe_dump(namespace).encode("utf-8"))

    status = ks.Status()
    status.set("namespace", ns)
    status.set("project", project)
    status.set("team", team)
    status.set(
        "message",
        f"Environment {environment} provisioning - namespace {ns} for project {project} (team {team})",
    )
    sdk.write_status(status)


if __name__ == "__main__":
    main()
```

(This is the same manifest shape as before — `build_namespace` is `main()`'s old inline dict literal, extracted; `namespace_name` is unchanged; `import kratix_sdk`/`import yaml` moved from module level into `main()`.)

- [ ] **Step 4: Run the tests to verify they pass**

```bash
python3 -m pytest test_pipeline.py -v
```

Expected: `2 passed`

- [ ] **Step 5: Commit**

```bash
git add promises/environment/workflows/resource/configure/environment-configure/kratix-guide-environment-resource-pipeline/scripts/pipeline.py promises/environment/workflows/resource/configure/environment-configure/kratix-guide-environment-resource-pipeline/scripts/test_pipeline.py
git commit -m "test: extract build_namespace in environment pipeline for testability"
```

---

### Task 3: `promises/database`'s pipeline reads the request's own namespace

**Files:**
- Modify: `promises/database/workflows/resource/configure/database-configure/kratix-guide-database-resource-pipeline/scripts/pipeline.py`
- Create: `promises/database/workflows/resource/configure/database-configure/kratix-guide-database-resource-pipeline/scripts/test_pipeline.py`

**Interfaces:**
- Consumes: nothing from other tasks (depends on Task 1's namespace existing on the worker cluster only at runtime, not at test/build time).
- Produces: `build_manifest(name: str, namespace: str, size: str) -> dict` — module-level, importable without `kratix_sdk`.

- [ ] **Step 1: Write the failing test**

Create `test_pipeline.py`:

```python
from pipeline import build_manifest


def test_build_manifest_uses_given_namespace():
    manifest = build_manifest("example-database", "project-team-a-checkout-dev", "1Gi")

    assert manifest["apiVersion"] == "acid.zalan.do/v1"
    assert manifest["kind"] == "postgresql"
    assert manifest["metadata"] == {
        "name": "example-database",
        "namespace": "project-team-a-checkout-dev",
    }
    assert manifest["spec"]["volume"] == {"size": "1Gi"}


def test_build_manifest_defaults():
    manifest = build_manifest("db1", "project-team-b-billing-staging", "2Gi")

    assert manifest["spec"]["teamId"] == "kratix"
    assert manifest["spec"]["enableLogicalBackup"] is True
    assert manifest["spec"]["numberOfInstances"] == 2
    assert manifest["spec"]["users"] == {"team-a": ["superuser", "createdb"]}
    assert manifest["spec"]["postgresql"] == {"version": "16"}
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
cd promises/database/workflows/resource/configure/database-configure/kratix-guide-database-resource-pipeline/scripts
python3 -m pytest test_pipeline.py -v
```

Expected: fails to collect (`ModuleNotFoundError: No module named 'kratix_sdk'` from `pipeline.py`'s current module-level import, and/or `ImportError: cannot import name 'build_manifest'`).

- [ ] **Step 3: Refactor `pipeline.py`**

Replace the full file content with:

```python
def build_manifest(name: str, namespace: str, size: str) -> dict:
    return {
        "apiVersion": "acid.zalan.do/v1",
        "kind": "postgresql",
        "metadata": {"name": name, "namespace": namespace},
        "spec": {
            "teamId": "kratix",
            "enableLogicalBackup": True,
            "volume": {"size": size},
            "numberOfInstances": 2,
            "users": {"team-a": ["superuser", "createdb"]},
            "postgresql": {"version": "16"},
        },
    }


def main():
    import kratix_sdk as ks
    import yaml

    sdk = ks.KratixSDK()
    resource = sdk.read_resource_input()
    name = resource.get_name()
    size = resource.get_value("spec.size")
    namespace = resource.get_namespace()

    manifest = build_manifest(name, namespace, size)
    data = yaml.safe_dump(manifest).encode("utf-8")
    sdk.write_output("database.yaml", data)

    status = ks.Status()
    status.set("teamId", "kratix")
    sdk.write_status(status)


if __name__ == "__main__":
    main()
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
python3 -m pytest test_pipeline.py -v
```

Expected: `2 passed`

- [ ] **Step 5: Commit**

```bash
git add promises/database/workflows/resource/configure/database-configure/kratix-guide-database-resource-pipeline/scripts/pipeline.py promises/database/workflows/resource/configure/database-configure/kratix-guide-database-resource-pipeline/scripts/test_pipeline.py
git commit -m "fix: database pipeline uses the request's own namespace on the worker cluster"
```

---

### Task 4: `promises/container`'s pipeline reads the request's own namespace

**Files:**
- Modify: `promises/container/workflows/resource/configure/container-configure/kratix-guide-container-resource-pipeline/scripts/pipeline.py:48-67`
- Modify: `promises/container/README.md` (intro paragraph + "Known limitation" section)

**Interfaces:**
- Consumes: nothing from other tasks (depends on Task 1's namespace existing on the worker cluster only at runtime).
- Produces: no interface change — `build_deployment(name, namespace, spec)` and `build_service(name, namespace, spec)` already take `namespace` as a parameter (see existing `test_pipeline.py`); only what `main()` passes as that argument changes.

- [ ] **Step 1: Edit `pipeline.py`'s `main()`**

Replace:

```python
    name = resource.get_name()
    # Hardcoded, not resource.get_namespace(): the worker cluster (where this
    # Deployment/Service land) has no per-team/environment namespaces yet -
    # see docs/superpowers/specs/2026-08-14-container-promise-design.md,
    # "Known limitation". Matches database's existing precedent.
    namespace = "default"
```

with:

```python
    name = resource.get_name()
    namespace = resource.get_namespace()
```

- [ ] **Step 2: Run the existing tests to confirm they still pass**

```bash
cd promises/container/workflows/resource/configure/container-configure/kratix-guide-container-resource-pipeline/scripts
python3 -m pytest test_pipeline.py -v
```

Expected: `4 passed` (these tests call `build_deployment`/`build_service` directly with an explicit namespace argument, so this change doesn't require editing them — they already prove those functions place whatever namespace they're given into `metadata.namespace`).

- [ ] **Step 3: Update `promises/container/README.md`**

Replace:

```markdown
Runs a single container image as a Kubernetes `Deployment`, with an optional
`Service` when `spec.port` is set - the low-level workload primitive for
this marketplace. See
`docs/superpowers/specs/2026-08-14-container-promise-design.md` for the
full design, including why the API stays low-level (a future `Service`
compound Promise will bundle this with `database` behind a simpler API)
and the current worker-cluster tenancy limitation (next paragraph).
```

with:

```markdown
Runs a single container image as a Kubernetes `Deployment`, with an optional
`Service` when `spec.port` is set - the low-level workload primitive for
this marketplace. See
`docs/superpowers/specs/2026-08-14-container-promise-design.md` for the
full design, including why the API stays low-level (a future `Service`
compound Promise will bundle this with `database` behind a simpler API).
See "Namespace" and "Known limitation" below for how/where this pipeline's
output lands.
```

Replace:

```markdown
**Known limitation:** the `Deployment`/`Service` this pipeline writes
always land in the `default` namespace on the worker cluster
(`kind-worker`), regardless of which namespace the `Container` request
itself lives in on the platform cluster - the worker cluster has no
per-team/environment namespaces yet. Matches `database`'s existing
precedent; see the design doc's "Known limitation" section for why this
isn't fixed here. It also means no cpu/memory ceiling is enforced on this workload today -
see the design doc's "Known limitation" section for why.
```

with:

```markdown
**Namespace:** the `Deployment`/`Service` this pipeline writes land in the
same project-environment namespace the `Container` request itself lives in
on the platform cluster (`project-<team>-<project>-<environment>`), read
via `resource.get_namespace()`. That namespace is created on the worker
cluster by `promises/environment`'s pipeline, not by this one - see
`docs/superpowers/specs/2026-09-01-project-namespace-projection-design.md`.

**Known limitation:** that worker-side namespace carries no RBAC or
resource-quota enforcement - no Capsule `Tenant`/`LimitRange` is mirrored
to `kind-worker`, so no cpu/memory ceiling is enforced on this workload
today. See the project-namespace-projection design doc's "Non-goals" for
why full tenant parity on the worker cluster stays a separate, larger
follow-up.
```

- [ ] **Step 4: Commit**

```bash
git add promises/container/workflows/resource/configure/container-configure/kratix-guide-container-resource-pipeline/scripts/pipeline.py promises/container/README.md
git commit -m "fix: container pipeline uses the request's own namespace on the worker cluster"
```

---

### Task 5: Manual end-to-end verification against the local kind clusters

**Files:** none (verification only).

**Interfaces:**
- Consumes: the full chain from Tasks 1–4.
- Produces: nothing further consumes this task.

This task needs real kind clusters with images pulled and Flux syncing, which this sandboxed environment cannot do (Docker image pulls hang here — verify on your own machine, not in this session).

- [ ] **Step 1: Rebuild the local demo environment**

```bash
make down && make up
```

- [ ] **Step 2: Provision a project + environment, then request a Database and a Container into it**

Follow `promises/project/README.md` and `promises/environment/README.md`'s "Try it" sections to create a project and an environment (or reuse the `checkout-service`/`dev` example resources already in this repo), then:

```bash
kubectl --context kind-platform apply -f promises/database/example-resource.yaml -n project-checkout-checkout-service-dev
kubectl --context kind-platform apply -f promises/container/example-resource.yaml -n project-checkout-checkout-service-dev
```

(Adjust the namespace to whatever project/environment you provisioned.)

- [ ] **Step 3: Confirm the namespace exists on the worker cluster and both workloads land in it**

```bash
kubectl --context kind-worker get namespace project-checkout-checkout-service-dev
kubectl --context kind-worker get postgresqls -n project-checkout-checkout-service-dev
kubectl --context kind-worker get deployments,services -n project-checkout-checkout-service-dev
```

Expected: the namespace exists, and both the `postgresql` and the `Deployment`/`Service` are inside it — not in `default`.

- [ ] **Step 4: Confirm nothing regressed on the platform cluster**

```bash
kubectl --context kind-platform get ns project-checkout-checkout-service-dev --show-labels
kubectl --context kind-platform get rolebindings -n project-checkout-checkout-service-dev
```

Expected: same as before this change — the platform-side namespace and its `RoleBinding` are unaffected.

No commit for this task (verification only).
