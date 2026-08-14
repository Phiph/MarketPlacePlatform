# Container Promise Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `Container` Kratix Promise - a primitive that runs one container image as a Kubernetes `Deployment` (plus an optional `Service`), scaffolded and installed the same way every other Promise in this repo is.

**Architecture:** A `demo.kratix.io/v1alpha1` `Container` CRD with no promise-level dependency (unlike `database`'s Postgres operator - this targets native `Deployment`/`Service` objects directly). A single Python resource-configure pipeline reads the request's spec and writes those manifests to `/kratix/output`; Flux (already installed) delivers them to the worker cluster, unchanged from how `database` already works.

**Tech Stack:** Kratix CLI (`bin/kratix`) for scaffolding, Python 3.12 + `kratix-sdk` + `PyYAML` for the pipeline, `pytest` for the pipeline's pure-function unit tests, `make`/`kubectl`/`kind`/`yq` for build/install/validate - all already used by the sibling `database`/`team`/`business-unit` Promises.

## Global Constraints

- API group is `demo.kratix.io` (every existing Promise in `promises/` uses this group - see `promises/*/promise.yaml`).
- Promise version is `v0.1.0` (matches `team`/`environment`/`business-unit`; only the tutorial-derived `database` is still on `v0.0.1`).
- No per-promise `Makefile` - this repo centralizes `promise-build`/`promise-load`/`promise-demo` in the root `Makefile`, driven by a `PROMISE_DIR=` override (confirmed: no `Makefile` exists under any `promises/*/` directory).
- **Known limitation, out of scope to fix here:** the worker cluster (`kind-worker`, Destination `worker-1`) has no per-team/environment namespaces yet - only the platform cluster does. The pipeline hardcodes `namespace: "default"` for the `Deployment`/`Service` it writes, matching `database`'s existing precedent. See `docs/superpowers/specs/2026-08-14-container-promise-design.md`, "Known limitation", for the full reasoning - do not attempt to fix this as part of this plan.
- Full design/rationale (API schema, why no Argon/ArgoCD, why no compute tiers): `docs/superpowers/specs/2026-08-14-container-promise-design.md`. Every task below implements a specific section of that spec.
- This repo's Python pipelines have no existing unit-test convention (`database`/`business-unit`'s pipelines have zero tests, validated only end-to-end via `make promise-demo`). Task 3 introduces one anyway, scoped tightly to the pipeline's pure YAML-building logic (no I/O, no `kratix_sdk` dependency) - this is additive, not a repo-wide testing mandate.

---

### Task 1: Scaffold the Container Promise via the kratix CLI

**Files:**
- Create: `promises/container/promise.yaml` (scaffolded, then hand-edited in Task 2)
- Create: `promises/container/example-resource.yaml` (scaffolded, then hand-edited in Task 4)
- Create: `promises/container/README.md` (scaffolded, then replaced in Task 4)
- Create: `promises/container/workflows/resource/configure/container-configure/kratix-guide-container-resource-pipeline/Dockerfile`
- Create: `promises/container/workflows/resource/configure/container-configure/kratix-guide-container-resource-pipeline/scripts/pipeline.py` (scaffolded boilerplate, then replaced in Task 3)

**Interfaces:**
- Produces: the directory `promises/container/` with the structure every later task edits into place. The pipeline image name `kratix-guide/container-resource-pipeline:v0.1.0` (used by `promise-build`/`promise-load` in Task 6) is set here, via the `--image` flag below - later tasks must not change it.

- [ ] **Step 1: Fetch the kratix CLI if not already present**

Run: `make --no-print-directory bin/kratix`
Expected: exits 0. If `bin/kratix` already exists, this is a no-op (the Makefile target is a file-based prerequisite).

- [ ] **Step 2: Scaffold the base Promise structure**

Run:
```bash
./bin/kratix init promise container --group demo.kratix.io --kind Container --version v1alpha1 --dir promises/container
```
Expected output: `container promise bootstrapped in the promises/container directory`. This creates `promises/container/{promise.yaml,example-resource.yaml,README.md}`.

- [ ] **Step 3: Add the scalar API properties**

Run:
```bash
./bin/kratix update api --dir promises/container \
  --property image:string \
  --property replicas:integer \
  --property cpu:string \
  --property memory:string \
  --property port:integer
```
Expected output: `Promise api updated`. (The CLI's `--property` flag has no `array` type, so `spec.env` is added by hand in Task 2 instead.)

- [ ] **Step 4: Scaffold the resource pipeline**

Run:
```bash
./bin/kratix add container resource/configure/container-configure --image kratix-guide/container-resource-pipeline:v0.1.0 --language python --dir promises/container
```
Expected output includes `generated the resource/configure/container-configure/kratix-guide-container-resource-pipeline in promises/container/promise.yaml` and `Customise your container by editing workflows/resource/configure/container-configure/kratix-guide-container-resource-pipeline/scripts/pipeline.py`.

- [ ] **Step 5: Verify the generated tree**

Run: `find promises/container -type f | sort`
Expected output (exact paths):
```
promises/container/README.md
promises/container/example-resource.yaml
promises/container/promise.yaml
promises/container/workflows/resource/configure/container-configure/kratix-guide-container-resource-pipeline/Dockerfile
promises/container/workflows/resource/configure/container-configure/kratix-guide-container-resource-pipeline/scripts/pipeline.py
```

- [ ] **Step 6: Commit the scaffold**

```bash
git add promises/container
git commit -m "$(cat <<'EOF'
feat(container): scaffold Container Promise via kratix CLI

Base structure only - CRD schema, example resource, README, and
pipeline logic are hand-finished in following commits.
EOF
)"
```

---

### Task 2: Finalize the Container CRD schema and marketplace metadata

**Files:**
- Modify: `promises/container/promise.yaml` (entire file - replace scaffolded content)

**Interfaces:**
- Consumes: nothing from other tasks.
- Produces: the final `Container` CRD schema (`image`, `replicas`, `cpu`, `memory`, `port`, `env` under `spec`) that Task 3's pipeline reads via `resource.get_value("spec.<field>")`, and that Task 4's `example-resource.yaml` and README describe.

- [ ] **Step 1: Replace `promises/container/promise.yaml` with the final version**

The CLI scaffold has no marketplace annotations, no validation keywords (`pattern`/`minimum`/`maximum`/`default`/`required`), and no `env` field (the CLI can't express array-of-object properties). Replace the whole file:

```yaml
apiVersion: platform.kratix.io/v1alpha1
kind: Promise
metadata:
  creationTimestamp: null
  labels:
    kratix.io/promise-version: v0.1.0
    marketplace.kratix.io/visible: "true"
  annotations:
    marketplace.kratix.io/display-name: Container
    marketplace.kratix.io/description: A single containerized workload, sized and configured on request.
    marketplace.kratix.io/owner: platform-team
    marketplace.kratix.io/lifecycle: experimental
    marketplace.kratix.io/support: "#platform-eng"
    marketplace.kratix.io/policy: internal
  name: container
spec:
  api:
    apiVersion: apiextensions.k8s.io/v1
    kind: CustomResourceDefinition
    metadata:
      creationTimestamp: null
      name: containers.demo.kratix.io
    spec:
      group: demo.kratix.io
      names:
        kind: Container
        plural: containers
        singular: container
      scope: Namespaced
      versions:
      - name: v1alpha1
        schema:
          openAPIV3Schema:
            properties:
              spec:
                properties:
                  image:
                    type: string
                    description: Container image, including tag (e.g. "nginx:1.27").
                  replicas:
                    type: integer
                    minimum: 1
                    default: 1
                  cpu:
                    type: string
                    pattern: '^[0-9]+m?$'
                    description: CPU request/limit as a Kubernetes quantity (e.g. "500m", "2").
                  memory:
                    type: string
                    pattern: '^[0-9]+(Ei|Pi|Ti|Gi|Mi|Ki)?$'
                    description: Memory request/limit as a Kubernetes quantity (e.g. "512Mi", "2Gi").
                  port:
                    type: integer
                    minimum: 1
                    maximum: 65535
                    description: Container port to expose. When set, a Service is also created.
                  env:
                    type: array
                    items:
                      type: object
                      properties:
                        name:
                          type: string
                        value:
                          type: string
                      required:
                      - name
                      - value
                required:
                - image
                - cpu
                - memory
                type: object
            type: object
        served: true
        storage: true
    status:
      acceptedNames:
        kind: ""
        plural: ""
      conditions: null
      storedVersions: null
  workflows:
    config: {}
    promise: {}
    resource:
      configure:
      - apiVersion: platform.kratix.io/v1alpha1
        kind: Pipeline
        metadata:
          name: container-configure
        spec:
          containers:
          - image: kratix-guide/container-resource-pipeline:v0.1.0
            name: kratix-guide-container-resource-pipeline
status:
  workflows: 0
  workflowsFailed: 0
  workflowsSucceeded: 0
```

- [ ] **Step 2: Validate the YAML parses and the schema is well-formed**

Run: `yq eval '.spec.api.spec.versions[0].schema.openAPIV3Schema.properties.spec.required' promises/container/promise.yaml`
Expected output:
```
- image
- cpu
- memory
```

- [ ] **Step 3: Commit**

```bash
git add promises/container/promise.yaml
git commit -m "feat(container): finalize CRD schema and marketplace metadata"
```

---

### Task 3: Implement the resource pipeline (TDD on the pure manifest-building logic)

**Files:**
- Modify: `promises/container/workflows/resource/configure/container-configure/kratix-guide-container-resource-pipeline/scripts/pipeline.py` (entire file - replace scaffolded "Hello World" boilerplate)
- Create: `promises/container/workflows/resource/configure/container-configure/kratix-guide-container-resource-pipeline/scripts/test_pipeline.py`

**Interfaces:**
- Consumes: the CRD field names from Task 2 (`image`, `replicas`, `cpu`, `memory`, `port`, `env`).
- Produces: `build_deployment(name: str, namespace: str, spec: dict) -> dict` and `build_service(name: str, namespace: str, spec: dict) -> dict | None`, both pure (no I/O, no `kratix_sdk`/`yaml` imports needed to call them) - Task 6's end-to-end validation relies on `main()` wiring these to `kratix_sdk`, not on their internals.

`kratix_sdk` and `pytest` aren't installable in this sandbox (no PyPI access - see the sandbox's known network restriction), so this task keeps `build_deployment`/`build_service` free of any import that needs installing, and defers `import kratix_sdk as ks` / `import yaml` to inside `main()`. This makes the TDD steps below runnable with a bare `pytest` install, and is also just better factoring - the manifest-building logic has nothing to do with the SDK.

- [ ] **Step 1: Write the failing tests**

Create `promises/container/workflows/resource/configure/container-configure/kratix-guide-container-resource-pipeline/scripts/test_pipeline.py`:

```python
from pipeline import build_deployment, build_service


def test_build_deployment_minimal():
    spec = {"image": "nginx:1.27", "cpu": "250m", "memory": "128Mi"}
    deployment = build_deployment("example-container", "default", spec)

    assert deployment["apiVersion"] == "apps/v1"
    assert deployment["kind"] == "Deployment"
    assert deployment["metadata"] == {"name": "example-container", "namespace": "default"}
    assert deployment["spec"]["replicas"] == 1
    container = deployment["spec"]["template"]["spec"]["containers"][0]
    assert container["name"] == "example-container"
    assert container["image"] == "nginx:1.27"
    assert container["resources"] == {
        "requests": {"cpu": "250m", "memory": "128Mi"},
        "limits": {"cpu": "250m", "memory": "128Mi"},
    }
    assert "ports" not in container
    assert "env" not in container


def test_build_deployment_with_replicas_port_and_env():
    spec = {
        "image": "nginx:1.27",
        "cpu": "250m",
        "memory": "128Mi",
        "replicas": 2,
        "port": 80,
        "env": [{"name": "GREETING", "value": "hello"}],
    }
    deployment = build_deployment("example-container", "default", spec)

    assert deployment["spec"]["replicas"] == 2
    container = deployment["spec"]["template"]["spec"]["containers"][0]
    assert container["ports"] == [{"containerPort": 80}]
    assert container["env"] == [{"name": "GREETING", "value": "hello"}]


def test_build_service_returns_none_without_port():
    spec = {"image": "nginx:1.27", "cpu": "250m", "memory": "128Mi"}
    assert build_service("example-container", "default", spec) is None


def test_build_service_with_port():
    spec = {"image": "nginx:1.27", "cpu": "250m", "memory": "128Mi", "port": 80}
    service = build_service("example-container", "default", spec)

    assert service["apiVersion"] == "v1"
    assert service["kind"] == "Service"
    assert service["metadata"] == {"name": "example-container", "namespace": "default"}
    assert service["spec"] == {
        "selector": {"app": "example-container"},
        "ports": [{"port": 80, "targetPort": 80}],
    }
```

- [ ] **Step 2: Install pytest and run the tests to verify they fail**

Run:
```bash
pip install --quiet pytest
cd promises/container/workflows/resource/configure/container-configure/kratix-guide-container-resource-pipeline/scripts
python3 -m pytest test_pipeline.py -v
cd -
```
Expected: `ImportError: cannot import name 'build_deployment' from 'pipeline'` (the scaffolded `pipeline.py` only has a `main()` function).

- [ ] **Step 3: Replace `pipeline.py` with the full implementation**

```python
def build_deployment(name, namespace, spec):
    replicas = spec.get("replicas", 1)
    container = {
        "name": name,
        "image": spec["image"],
        "resources": {
            "requests": {"cpu": spec["cpu"], "memory": spec["memory"]},
            "limits": {"cpu": spec["cpu"], "memory": spec["memory"]},
        },
    }
    port = spec.get("port")
    if port:
        container["ports"] = [{"containerPort": port}]
    env = spec.get("env")
    if env:
        container["env"] = [{"name": e["name"], "value": e["value"]} for e in env]

    return {
        "apiVersion": "apps/v1",
        "kind": "Deployment",
        "metadata": {"name": name, "namespace": namespace},
        "spec": {
            "replicas": replicas,
            "selector": {"matchLabels": {"app": name}},
            "template": {
                "metadata": {"labels": {"app": name}},
                "spec": {"containers": [container]},
            },
        },
    }


def build_service(name, namespace, spec):
    port = spec.get("port")
    if not port:
        return None
    return {
        "apiVersion": "v1",
        "kind": "Service",
        "metadata": {"name": name, "namespace": namespace},
        "spec": {
            "selector": {"app": name},
            "ports": [{"port": port, "targetPort": port}],
        },
    }


def main():
    import kratix_sdk as ks
    import yaml

    sdk = ks.KratixSDK()
    resource = sdk.read_resource_input()
    name = resource.get_name()
    # Hardcoded, not resource.get_namespace(): the worker cluster (where this
    # Deployment/Service land) has no per-team/environment namespaces yet -
    # see docs/superpowers/specs/2026-08-14-container-promise-design.md,
    # "Known limitation". Matches database's existing precedent.
    namespace = "default"
    spec = {
        "image": resource.get_value("spec.image"),
        "replicas": resource.get_value("spec.replicas", default=1),
        "cpu": resource.get_value("spec.cpu"),
        "memory": resource.get_value("spec.memory"),
        "port": resource.get_value("spec.port", default=None),
        "env": resource.get_value("spec.env", default=None),
    }

    deployment = build_deployment(name, namespace, spec)
    sdk.write_output("deployment.yaml", yaml.safe_dump(deployment).encode("utf-8"))

    service = build_service(name, namespace, spec)
    if service:
        sdk.write_output("service.yaml", yaml.safe_dump(service).encode("utf-8"))

    status = ks.Status()
    status.set("image", spec["image"])
    if service:
        status.set("service", name)
    sdk.write_status(status)


if __name__ == "__main__":
    main()
```

- [ ] **Step 4: Run the tests again to verify they pass**

Run:
```bash
cd promises/container/workflows/resource/configure/container-configure/kratix-guide-container-resource-pipeline/scripts
python3 -m pytest test_pipeline.py -v
cd -
```
Expected: `4 passed`.

- [ ] **Step 5: Commit**

```bash
git add promises/container/workflows/resource/configure/container-configure/kratix-guide-container-resource-pipeline/scripts
git commit -m "feat(container): implement resource pipeline with tested manifest builders"
```

---

### Task 4: Write the example resource and the Promise README

**Files:**
- Modify: `promises/container/example-resource.yaml` (entire file - replace scaffolded content)
- Modify: `promises/container/README.md` (entire file - replace scaffolded boilerplate)

**Interfaces:**
- Consumes: the CRD schema from Task 2 and the pipeline behavior from Task 3.
- Produces: the request Task 6's end-to-end validation applies.

- [ ] **Step 1: Replace `promises/container/example-resource.yaml`**

Uses a public image (no local registry push needed) and exercises every field, including `port` (so both `Deployment` and `Service` get created) and `env`:

```yaml
apiVersion: demo.kratix.io/v1alpha1
kind: Container
metadata:
  name: example-container
  namespace: default
spec:
  image: nginx:1.27
  replicas: 2
  cpu: "250m"
  memory: "128Mi"
  port: 80
  env:
  - name: GREETING
    value: hello-from-container-promise
```

- [ ] **Step 2: Replace `promises/container/README.md`**

```markdown
# `container` Promise

Runs a single container image as a Kubernetes `Deployment`, with an optional
`Service` when `spec.port` is set - the low-level workload primitive for
this marketplace. See
`docs/superpowers/specs/2026-08-14-container-promise-design.md` for the
full design, including why the API stays low-level (a future `Service`
compound Promise will bundle this with `database` behind a simpler API)
and the current worker-cluster tenancy limitation (next paragraph).

Marked `marketplace.kratix.io/visible: "true"`: self-served through the
catalog, like `database`.

**Operational evidence** (see the root `README.md`'s "Marketplace metadata
convention"): owner `platform-team`, lifecycle `experimental`, support
`#platform-eng`, policy `internal`.

- `promise.yaml` - the `Container` CRD (`demo.kratix.io/v1alpha1`), no
  promise-level dependency workflow (unlike `database`'s Postgres operator)
  - this Promise targets native `Deployment`/`Service` objects directly.
- `workflows/resource/configure/container-configure` - a Python pipeline
  that runs per-request, reading `spec.image`/`replicas`/`cpu`/`memory`/
  `port`/`env` and writing a `Deployment` manifest (plus a `Service`
  manifest when `spec.port` is set).
- `example-resource.yaml` - a sample `Container` request, `nginx:1.27`
  with a `port` and one `env` var set, so both the `Deployment` and
  `Service` code paths get exercised.

**Known limitation:** the `Deployment`/`Service` this pipeline writes
always land in the `default` namespace on the worker cluster
(`kind-worker`), regardless of which namespace the `Container` request
itself lives in on the platform cluster - the worker cluster has no
per-team/environment namespaces yet. Matches `database`'s existing
precedent; see the design doc's "Known limitation" section for why this
isn't fixed here.

## Try it

```bash
make promise-build promise-load PROMISE_DIR=promises/container
kubectl --context kind-platform apply -f promises/container/promise.yaml
kubectl --context kind-platform apply -f promises/container/example-resource.yaml
```

Then:

```bash
kubectl --context kind-platform get containers.demo.kratix.io example-container -w
kubectl --context kind-worker get deployments,services,pods -l app=example-container
```

## Iterating

After editing
`workflows/resource/configure/container-configure/kratix-guide-container-resource-pipeline/scripts/pipeline.py`:

```bash
make promise-build promise-load PROMISE_DIR=promises/container
kubectl --context kind-platform delete container example-container
kubectl --context kind-platform apply -f promises/container/example-resource.yaml
```
```

- [ ] **Step 3: Commit**

```bash
git add promises/container/example-resource.yaml promises/container/README.md
git commit -m "feat(container): add example resource and Promise README"
```

---

### Task 5: Fix `promise-demo`'s hardcoded CRD name

**Files:**
- Modify: `Makefile:282-293` (the `promise-demo` target)

**Interfaces:**
- Consumes: nothing from other tasks - this is a pre-existing bug, independent of `Container`.
- Produces: a `promise-demo` target that works correctly for any `PROMISE_DIR`, which Task 6 relies on for `Container`'s own end-to-end validation.

The current target hardcodes `databases.demo.kratix.io` in its CRD-established wait loop and `example-database` in its final echo hint - both wrong for every Promise except `database`. Since it already had a real `database` CRD to fall back on, this bug was silent; it isn't for `Container`.

- [ ] **Step 1: Read the current target**

```bash
sed -n '282,293p' Makefile
```
Confirm it matches:
```makefile
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
```

- [ ] **Step 2: Replace it with a version that derives the CRD name and resource name from `$(PROMISE_DIR)`**

```makefile
.PHONY: promise-demo
promise-demo: promise-build promise-load ## Build, load, and install $(PROMISE_DIR), then request its example resource
	kubectl --context $(PLATFORM_CTX) apply -f $(PROMISE_DIR)/promise.yaml
	@crd=$$(yq '.spec.api.metadata.name' $(PROMISE_DIR)/promise.yaml); \
	echo "Waiting for the Promise's CRD ($$crd) to be established..."; \
	for i in $$(seq 1 60); do \
		kubectl --context $(PLATFORM_CTX) get crd "$$crd" >/dev/null 2>&1 && break; \
		sleep 2; \
	done
	kubectl --context $(PLATFORM_CTX) apply -f $(PROMISE_DIR)/example-resource.yaml
	@crd=$$(yq '.spec.api.metadata.name' $(PROMISE_DIR)/promise.yaml); \
	name=$$(yq '.metadata.name' $(PROMISE_DIR)/example-resource.yaml); \
	echo ""; \
	echo "Watch the request:  kubectl --context $(PLATFORM_CTX) get $$crd $$name -w"; \
	echo "Watch the worker:   kubectl --context $(WORKER_CTX) get pods -w"
```

- [ ] **Step 3: Regression-check against `database` (the only Promise this target has ever been used against)**

Run: `make promise-demo`
Expected: identical behavior to before this change - waits for `databases.demo.kratix.io`, applies `promises/database/example-resource.yaml`, and echoes
```
Watch the request:  kubectl --context kind-platform get databases.demo.kratix.io example-database -w
Watch the worker:   kubectl --context kind-worker get pods -w
```
(Skip this step if a `database` Promise is already installed from earlier in this session and re-running would just no-op harmlessly on `kubectl apply` - either way, confirm the echoed commands above are exactly right by inspection.)

- [ ] **Step 4: Commit**

```bash
git add Makefile
git commit -m "fix(makefile): derive promise-demo's CRD/resource name from PROMISE_DIR

Was hardcoded to database's CRD name and example resource name, so it
silently gave the wrong 'watch' hint for every other Promise."
```

---

### Task 6: End-to-end validation

**Files:** none (verification only).

**Interfaces:**
- Consumes: everything from Tasks 1-5.

- [ ] **Step 1: Ensure the local cluster is up**

Run: `make up`
Expected: exits 0 (idempotent - no-op if already running).

- [ ] **Step 2: Run the fixed `promise-demo` against `Container`**

Run: `make promise-demo PROMISE_DIR=promises/container`
Expected: builds `kratix-guide/container-resource-pipeline:v0.1.0`, loads it into `kind-platform`, applies `promises/container/promise.yaml`, waits for `containers.demo.kratix.io` to be established, applies `promises/container/example-resource.yaml`, and echoes the two `kubectl ... -w` hints with `containers.demo.kratix.io example-container` and `kind-worker`.

- [ ] **Step 3: Confirm the `Container` resource reports success**

Run: `kubectl --context kind-platform get containers.demo.kratix.io example-container -o jsonpath='{.status}'`
Expected (allow a few seconds for the pipeline Job to run first): a status block including `"service":"example-container"` and `"image":"nginx:1.27"` (set by `main()`'s `status.set(...)` calls in Task 3).

- [ ] **Step 4: Confirm the `Deployment`, `Service`, and `Pod`s landed on the worker cluster**

Run: `kubectl --context kind-worker get deployments,services,pods -l app=example-container`
Expected: one `Deployment` (`example-container`, `2/2` ready once pods start), one `Service` (`example-container`, `ClusterIP`, port `80`), and two `Pod`s (from `replicas: 2`) eventually `Running`.

- [ ] **Step 5: Confirm the `env` var landed in the Pod**

Run: `kubectl --context kind-worker exec deploy/example-container -- printenv GREETING`
Expected: `hello-from-container-promise`.

- [ ] **Step 6: Confirm the no-`port` path skips the `Service`**

Apply a second request without `port`:
```bash
cat <<'EOF' | kubectl --context kind-platform apply -f -
apiVersion: demo.kratix.io/v1alpha1
kind: Container
metadata:
  name: example-container-no-port
  namespace: default
spec:
  image: nginx:1.27
  cpu: "100m"
  memory: "64Mi"
EOF
```
Then: `kubectl --context kind-worker get service example-container-no-port`
Expected: `Error from server (NotFound)` - no `Service` was created, since `spec.port` was omitted. Clean up: `kubectl --context kind-platform delete container example-container-no-port`.

- [ ] **Step 7: Clean up the first request too, confirming deletion cascades**

Run:
```bash
kubectl --context kind-platform delete container example-container
kubectl --context kind-worker get deployments,services -l app=example-container
```
Expected: the second command returns `No resources found` once Kratix's delete workflow finishes (may take a few seconds).
