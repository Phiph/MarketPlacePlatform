# Project Namespace Projection to Worker Destinations

## Problem

A resource request (e.g. `Database`, `Container`) already lives in the
right namespace on the platform cluster -
`project-{team}-{project}-{environment}`, computed identically by the
broker (`broker/internal/tenant.ProjectEnvironmentNamespace()`) and by
`promises/environment`'s pipeline (`namespace_name()`). But the *output*
those Promises schedule to the worker cluster (`kind-worker`, Destination
`worker-1`) ignores that entirely: `database`'s and `container`'s resource
pipelines both hardcode `namespace: "default"` on the manifests they
write, regardless of which project/environment the request belongs to.
Every workload from every team/project/environment lands in one shared
`default` namespace on the worker cluster.

This is a known, deliberately deferred gap - the `Container` design doc
(`docs/superpowers/specs/2026-08-14-container-promise-design.md`, "Known
limitation") flags it explicitly: *"worth a design of its own once more
than one workload-producing Promise exists."* That's now true (`database`
+ `container`).

**Prior art note:** `docs/superpowers/specs/2026-08-14-container-workload-logs-design.md`
("Worker namespaces" section) separately proposed having
`container-configure` create a worker-side namespace directly, named after
`resource.get_namespace()`. That design predates the `Team` → `Project` →
`Environment` hierarchy and used a team-level namespace convention
(`team-<team>`); its namespace-creation piece was never implemented (the
pipeline still hardcodes `"default"` today). This design supersedes that
piece: it adopts the current project-environment-level convention instead,
and creates the namespace via `promises/environment` rather than inside
each workload pipeline (see "Approach" below). If the Argo CD logs work
resumes, it should build on the namespace this design produces rather than
re-deriving its own.

## Goals

- A `Database` or `Container` request's worker-side workload lands in a
  namespace named after the project/environment it belongs to
  (`project-{team}-{project}-{environment}`), not `default`.
- The same namespace name is used on both clusters for a given
  project/environment - one name, everywhere.
- Any future workload-producing Promise gets this behavior by construction
  (reads `resource.get_namespace()`), without needing its own
  namespace-creation logic.

## Non-goals

- **No RBAC or quota enforcement on the worker cluster.** This design
  creates the `Namespace` object there for routing/traceability only. No
  Capsule `Tenant`/`LimitRange` gets mirrored to `kind-worker`; cpu/memory
  ceilings on worker workloads remain unenforced, unchanged from the
  `Container` design doc's existing "Known limitation." Full tenant
  parity on the worker cluster stays a separate, bigger follow-up.
- **No changes to `Team` or `BusinessUnit` worker-side presence.** Only
  the project-environment namespace (the granularity workloads are
  actually requested into) gets projected. Team- and business-unit-level
  objects (Capsule `Tenant`, team `Namespace`) stay platform-only, as
  today.
- **No migration tooling for already-provisioned Environments.** This is
  a local dev/demo platform; existing local clusters pick this up via the
  usual `make down && make up` recovery path, not a bespoke backfill
  mechanism.
- **No change to the Zalando Postgres operator's scope.** It already
  watches `watched_namespace: '*'` and holds cluster-scoped RBAC
  (`ClusterRole`/`ClusterRoleBinding`), so it needs no changes to operate
  against a `postgresql` CR in any project-environment namespace.

## Approach

`promises/environment`'s pipeline is already the single place that
computes the canonical namespace name and writes the one `Namespace`
object for it - currently scheduled only to the platform cluster via
`destinationSelectors: [{matchLabels: {environment: platform}}]`. Kratix's
`destinationSelectors` use OR semantics across list entries (a Destination
matching *any* entry receives the scheduled output), so adding a second
entry matching `worker-1`'s label (`{matchLabels: {environment: dev}}`)
makes that same `Namespace` object land on both clusters, with no new
Promise, pipeline, or duplicated namespace-creation logic.

`database`'s and `container`'s resource pipelines then switch from
hardcoding `namespace: "default"` to `resource.get_namespace()` (a
`kratix_sdk` method that reads the request CR's own `metadata.namespace`
on the platform cluster - already the correct project-environment
namespace, set by the broker at request time).

**Ordering is already guaranteed, not newly enforced:** a `Database` or
`Container` request can only be created inside a project-environment
namespace that already exists (Kubernetes admission would reject the
request otherwise), and that namespace is only created by successfully
provisioning the corresponding `Environment` first. So by the time a
workload-producing Promise's pipeline runs, the namespace this design adds
to the worker cluster has already been scheduled there too.

### Why not have each workload pipeline create its own namespace?

Considered and rejected: every current and future workload-producing
Promise (`database`, `container`, ...) would need to duplicate the same
`Namespace`-emitting logic, each becoming a second source of truth for
that object's shape (labels, `capsule.clastix.io/tenant`, etc.). Kratix
applies all scheduled output for a destination via Flux regardless of
which Promise produced it, so multiple pipelines emitting an
identically-named `Namespace` object is not unsafe by itself - but it is
unnecessary duplication when `promises/environment` already owns this
object's definition on the platform cluster. Widening its
`destinationSelectors` reuses that single definition instead of forking
it.

## Architecture

```
Environment request (platform cluster)
  -> environment-configure pipeline
     -> writes ONE Namespace manifest
        (project-{team}-{project}-{environment})
  -> Kratix schedules it to every Destination matching
     destinationSelectors: [platform, worker]  (OR match)
        -> kind-platform: Namespace created (as today)
        -> kind-worker:   Namespace created (NEW)

Database/Container request (platform cluster, inside that namespace)
  -> resource pipeline reads resource.get_namespace()
     -> writes workload manifest with that namespace
  -> Kratix schedules it to worker-1 (default, no selector)
        -> kind-worker: workload lands in the project-environment
           namespace (previously: "default")
```

## Components

| Piece | Where | Change |
|---|---|---|
| Environment namespace scheduling | `promises/environment/promise.yaml` | Add a second `destinationSelectors` entry matching `worker-1` (`environment: dev`) |
| Environment docs | `promises/environment/README.md` | Note the `Namespace` output now lands on both clusters |
| Database pipeline | `promises/database/workflows/resource/configure/database-configure/kratix-guide-database-resource-pipeline/scripts/pipeline.py` | Replace hardcoded `"namespace": "default"` with `resource.get_namespace()` |
| Container pipeline | `promises/container/workflows/resource/configure/container-configure/kratix-guide-container-resource-pipeline/scripts/pipeline.py` | Replace hardcoded `namespace = "default"` (and its now-stale comment) with `resource.get_namespace()` |
| Container docs | `promises/container/README.md`, and the "Known limitation" section referenced from the design doc | Update to reflect the namespace half of the gap is resolved; RBAC/quota parity remains open |

No changes needed to: the broker (already places requests in the correct
namespace), the `Database`/`Container` CRD schemas, the Zalando operator
configuration, or `promises/team`/`promises/business-unit`.

## Error handling

- If an `Environment`'s `Namespace` hasn't yet been delivered to
  `worker-1` when a `Database`/`Container` pipeline runs (Flux delivery
  lag), the workload manifest still gets written with the correct
  namespace name; Flux will apply it once the namespace exists (Flux
  retries on missing-namespace errors like it does for any other ordering
  dependency across manifests in the same or a later sync). No new
  handling needed - this is the same eventual-consistency behavior Flux
  already provides for every other output in this repo.
- No new failure mode is introduced on the broker side - the request CR's
  namespace was already being set correctly before this change.

## Testing

- **`promises/environment` pipeline:** no unit tests exist today for this
  pipeline; add one asserting the `Namespace` manifest content is
  unchanged (this design only touches `promise.yaml`'s
  `destinationSelectors`, not the pipeline's output).
- **`promises/database` pipeline:** no unit tests exist today; add one
  covering `pipeline.py`'s manifest construction, asserting the output's
  `metadata.namespace` equals `resource.get_namespace()`'s value rather
  than `"default"`.
- **`promises/container` pipeline:** `test_pipeline.py` already exists;
  update/add cases asserting `build_deployment`/`build_service` receive
  the namespace from `resource.get_namespace()`, not the literal
  `"default"`.
- **End-to-end** (manual, `make up`): provision an `Environment` for a
  project, then request a `Database` and a `Container` into it; confirm
  via `kubectl --context kind-worker get namespace
  project-<team>-<project>-<environment>` that the namespace exists on
  the worker cluster, and that the resulting `postgresql`/`Deployment`
  objects land inside it rather than in `default`.

## Open follow-ups (not built here)

- Full Capsule-parity tenancy mirror on the worker cluster (RBAC,
  `LimitRange`/quota enforcement) - remains its own, larger design, as
  already flagged in the `Container` design doc.
- Resuming the Argo CD workload-logs design
  (`2026-08-14-container-workload-logs-design.md`): its "Worker
  namespaces" section should be revised to reuse this design's
  project-environment namespace instead of re-deriving a team-level one.
