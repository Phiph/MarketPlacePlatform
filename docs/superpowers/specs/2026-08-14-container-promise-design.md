# Container Promise

## Problem

The marketplace's catalog offers workloads that store or organize things
(`Database`, `Team`, `Project`, `Environment`, `BusinessUnit`) but nothing
that actually *runs* something. A team that gets a `Database` provisioned
still has no way to run the application code that talks to it. This adds
`Container`: a primitive Promise that runs a single container image as a
Kubernetes `Deployment`, with an optional `Service` if it needs to be
reachable in-cluster.

`Container` is deliberately low-level - full image/replicas/cpu/memory/
port/env detail, no simplification. A future `Service` compound Promise
(not built here) will bundle `Container` with `Database` behind a friendlier,
tiered API; `Container` needs to expose the raw knobs for that layer to
have something to simplify.

## Goals

- Requesting a `Container` produces a running `Deployment` in the
  requester's namespace, sized exactly as specified (no hidden tiers).
- Setting `port` also produces a `Service`, so the workload is reachable by
  other resources in the same namespace without extra steps.
- Follows the same tenancy model as every other Promise here: no
  team/environment/businessUnit fields on the CRD itself, isolation comes
  entirely from which namespace the resource is requested into (see
  `promises/environment/promise.yaml`). **This applies to the `Container`
  resource itself, on the platform cluster - not to the `Deployment`/
  `Service` the pipeline emits.** See "Known limitation" below.
- cpu/memory are not currently ceiling-enforced on the resulting
  worker-side workload - see "Known limitation" below. The existing
  Capsule `Tenant` `LimitRange` (set via `BusinessUnit`,
  `promises/business-unit/promise.yaml`) only reaches the **platform**
  cluster, where `Container`'s own resource lives - not the worker
  cluster, where the `Deployment`/`Service` the pipeline emits actually
  land.

## Known limitation

The worker cluster (`kind-worker`, Destination `worker-1` - where `Container`'s
`Deployment`/`Service` land, per the README's "this is where scheduled
workloads normally land") has no per-team/environment namespaces today.
Only the platform cluster gets those, via `Team`/`Environment`/
`BusinessUnit`'s Capsule `Tenant`/`Namespace` objects. `Database`'s pipeline
works around this identically: it hardcodes `namespace: "default"` on the
worker side regardless of which namespace the `Database` resource itself
lives in on the platform cluster - a simplification its own README flags
as tutorial-level, not the "real" multi-tenant pattern.

`Container` follows the same precedent for v0.1.0: the pipeline hardcodes
`namespace: "default"` for the `Deployment`/`Service` it writes. The
`Container` resource itself is still namespace-scoped (and RBAC'd) on the
platform cluster like everything else - only the *resulting workload* on
the worker cluster is not yet tenant-isolated.

**This is an extension point, not a design decision to revisit lightly:**
making the worker cluster tenant-aware means deciding how namespaces (and
their RBAC/quotas) get mirrored onto *any* worker destination, which is a
multi-cluster problem bigger than `Container` alone - other Promises
targeting the worker cluster hit the same gap. Worth a design of its own
once more than one workload-producing Promise exists.

**This also means no cpu/memory ceiling is enforced on the resulting
workload.** `business-unit`/`team`/`environment` all carry
`destinationSelectors: [{matchLabels: {environment: platform}}]`, so their
Capsule `Tenant`/`Namespace`/`LimitRange` objects never reach the worker
cluster. `Container`'s `Deployment`/`Service` land in `default` on
`worker-1`, outside any Tenant, so a request like `cpu: "64"` or
`memory: "512Gi"` is faithfully emitted with no admission-time check.
Same extension point as the namespace gap above - solving one likely
solves both.

## Non-goals

- **No operator/dependency.** Unlike `Database` (Zalando Postgres
  operator), `Container` targets native Kubernetes `Deployment`/`Service`
  objects directly. No `workflows/promise/configure/dependencies` step.
- **No compute tiers.** Rejected in favor of raw `cpu`/`memory` fields -
  see "Problem" above for why the primitive stays low-level.
- **No numeric validation on cpu/memory in the CRD schema.** OpenAPI
  `minimum`/`maximum` only apply to numeric types; `cpu`/`memory` are
  Kubernetes quantity strings (`"500m"`, `"512Mi"`), so `pattern` regex is
  used instead. No ceiling is enforced on the resulting worker-side
  workload either - see "Known limitation" for why (the worker cluster has
  no Capsule Tenant/LimitRange, unlike the platform cluster).
- **No ArgoCD/ApplicationSets.** This platform already delivers every
  Promise's pipeline output via Flux (`kind-worker`'s `flux-worker`,
  `kind-platform`'s `flux-platform`); a second GitOps controller would
  duplicate that, not add anything. What actually motivated the Argo
  question - log/status visibility - is a broker/UI concern, not a
  delivery-layer one (see below).
- **No log-streaming endpoint in this change.** Worth building later:
  the broker already holds a `client-go` client, so a
  `GET .../containers/{name}/logs` endpoint (proxying the pod-logs API)
  gives the same visibility Argo's UI would, inside the existing
  broker+custom-UI pattern instead of a second UI. Flagged as follow-up,
  not built here.
- **No environment-promotion workflow.** "Progression between
  environments" fits this platform's existing model as: request the same
  `Container` spec again under a different `Environment`'s namespace. No
  new mechanism needed; flagged as a broker/UI follow-up (e.g. a "promote"
  action that copies a request's spec into the next environment), not
  built here.
- **No Argo Rollouts / progressive delivery** (canary, blue-green,
  promotion gates). Confirmed out of scope for now; would be a follow-up
  design of its own if needed later.

## API

`containers.demo.kratix.io/v1alpha1`, kind `Container`, namespaced - same
group as every other Promise here.

```yaml
spec:
  api:
    apiVersion: apiextensions.k8s.io/v1
    kind: CustomResourceDefinition
    metadata:
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
        served: true
        storage: true
        schema:
          openAPIV3Schema:
            type: object
            properties:
              spec:
                type: object
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
                      required: [name, value]
                required:
                - image
                - cpu
                - memory
```

No operator-only fields - unlike `Environment` (which has broker-owned
`team`/`businessUnit`), `Container`'s tenancy comes entirely from the
namespace it's requested into, so nothing on the spec needs to be
broker-composed.

## Pipeline

Resource-configure workflow only (no promise-level dependency), Python SDK
- matching `database`'s convention (`workflows/resource/configure/database-configure/.../pipeline.py`).

`workflows/resource/configure/container-configure/kratix-guide-container-resource-pipeline/scripts/pipeline.py`:

1. Read `spec.image`, `spec.replicas` (default `1`), `spec.cpu`,
   `spec.memory`, `spec.port` (optional), `spec.env` (optional) off the
   `Container` resource.
2. Write a `Deployment` manifest to `/kratix/output` - one container named
   after the resource, image/replicas/resources/env populated from the
   spec above, resource requests and limits both set to the given
   cpu/memory (no separate request-vs-limit distinction for v0.1.0),
   namespace hardcoded to `default` (see "Known limitation" above -
   matches `Database`'s existing precedent on the worker cluster).
3. If `spec.port` is set, also write a `Service` manifest (`ClusterIP`,
   single port forwarding to the container port) so the workload is
   reachable from other resources in the same namespace.

Delivered to the worker cluster via the existing Flux `Destination`,
unchanged - no new `destinationSelectors` needed beyond the default.

## Testing

- **Pipeline unit test** (Python): given a spec with/without `port` and
  with/without `env`, assert the written `Deployment` (and `Service`, when
  `port` is set) manifests have the expected fields. Matches the level of
  testing already in `database`'s pipeline, if any exists there to follow.
- **End-to-end** (`make promise-demo`-equivalent for this Promise): build
  and load the pipeline image, install the Promise, apply
  `example-resource.yaml`, confirm a `Deployment` (and `Service`, since the
  example sets `port`) appear on `kind-worker` and the pod goes `Running`.
- **Manual**: request a `Container` without `port` and confirm no `Service`
  is created - the one conditional branch in the pipeline.
