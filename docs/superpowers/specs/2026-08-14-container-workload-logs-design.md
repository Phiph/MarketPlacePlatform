# Container Workload Logs (via Argo CD)

## Problem

`Container` (`promises/container/`) runs a workload, but there's no way for
the team that requested it to see its logs. The `Container` design doc
(`docs/superpowers/specs/2026-08-14-container-promise-design.md`) flagged
this as a follow-up and initially sketched a broker-native
`client-go`-backed logs endpoint, explicitly ruling out ArgoCD ("a second
GitOps controller would duplicate [Flux], not add anything").

This design revisits that call. The requester wants Argo CD specifically,
for its multi-cluster story as this platform grows past two kind clusters:
centralized cluster credentials, a resource/status model built for
multi-cluster from the start, and a log API that doesn't require the broker
to hold and rotate its own kubeconfig per cluster. Argo CD is adopted here
**only** as a read/status/log engine behind the broker - not as a second
GitOps delivery mechanism and not as a second user-facing UI. Flux keeps
being the only thing that applies or prunes workload resources; Argo CD
never runs an automated sync.

## Goals

- A team can retrieve logs for a `Container` request they own, through the
  existing broker + marketplace UI - no second login, no second UI.
- Argo CD's own access to each registered cluster is scoped tightly (no
  `cluster-admin`).
- A team can only ever see/fetch logs for their own requests - enforced by
  Argo/Kubernetes RBAC, not by broker application logic policing itself.
- The design generalizes to more worker clusters later without broker code
  changes per cluster (Argo centralizes cluster credentials once,
  registration-time, not per-request).

## Non-goals

- **Argo CD does not replace Flux.** Every `Application` this design creates
  has sync automation disabled (`syncPolicy: {}` / no `automated` block).
  Flux remains the sole applier/pruner for `Deployment`/`Service`/`Namespace`
  objects on the worker cluster.
- **No standalone Argo CD UI access for end users.** A thin per-team local
  account exists for operator/debugging use (see "RBAC" below), but the
  product surface is the broker + marketplace UI, matching this platform's
  existing "broker+custom-UI instead of a second UI" precedent.
- **No full Capsule-parity tenancy mirror on the worker cluster.** This
  design adds one namespace per team on `kind-worker` (see "Worker
  namespaces" below) but does not mirror Capsule `Tenant`/`LimitRange`
  objects there. cpu/memory ceilings on worker workloads remain unenforced -
  unchanged from the `Container` design doc's existing "Known limitation".
- **No Argo Rollouts / progressive delivery.** Out of scope, as already
  decided in the `Container` design doc.
- **No cross-request log aggregation/search.** One request's logs at a time,
  matching the granularity everything else in the broker API already uses
  (`.../requests/{reqName}/...`).

## Architecture

- **Argo CD installs on `kind-platform`** (Helm, `hack/argo/` mirroring the
  `hack/kratix/` pattern), alongside Kratix/MinIO/cert-manager.
- **`kind-worker` is registered as an external managed cluster** in Argo CD:
  a `Secret` of type `Kubernetes cluster` holding a kubeconfig reachable over
  the kind docker network - the same connectivity trick
  `kratix-destination` already uses to reach the worker node's container IP
  for Flux.
- **Argo's own `ClusterRole`** on both registered clusters is scoped to what
  it actually needs (read/watch on workload resources, `pods/log`, and the
  verbs its diffing requires) - not `cluster-admin`.
- **Resource discovery is label-based, not apply-based.** Argo CD finds "its"
  resources via the tracking label `app.kubernetes.io/instance`, not by
  having applied them. `container-configure` stamps this label onto the
  `Deployment`/`Service` it writes; Flux still does the actual `apply`.
- **One `Application` per `Container` request**, manual sync only, rendered
  by `container-configure` and delivered to `kind-platform` via the existing
  Flux `Destination` - the same "pipeline writes a manifest, Flux delivers
  it" pattern already used for every other output in this repo. No
  `ApplicationSet` or separate controller needed; new `Application`s appear
  automatically as new `Container` requests are submitted.
- **Naming is deterministic, no lookup required.** The `Application`'s
  `metadata.name` is the `Container` request's own name (same convention
  already used for the `Deployment`/`Service`, both also named after the
  request). Given a request name and its owning team, the broker can compute
  the `Application`'s name and namespace directly - no list/search call to
  Argo needed before fetching resource tree/logs.

## Worker namespaces

Today `container-configure` hardcodes `namespace = "default"` on the worker
side (`pipeline.py:59`), regardless of which team's namespace the `Container`
resource itself lives in on the platform cluster. Meaningful per-team RBAC
scoping in Argo requires each team's worker-side resources to actually be in
their own namespace, so this design narrows that gap (without fully closing
the tenancy parity gap the `Container` design doc already flagged as "a
multi-cluster problem bigger than `Container` alone"):

- `container-configure` uses `resource.get_namespace()` (the platform-side
  namespace the `Container` request lives in, e.g. `team-checkout`) as the
  worker-side namespace name too - same name, both clusters.
- If that namespace doesn't yet exist on `kind-worker`, the pipeline emits a
  `Namespace` manifest for it (labelled `marketplace.kratix.io/team`, same
  convention `promises/team`'s pipeline already uses), delivered by Flux
  like everything else.
- **Explicitly out of scope:** Capsule `Tenant`/`LimitRange` mirroring to
  worker namespaces. cpu/memory ceilings remain unenforced on worker
  workloads, unchanged from the existing known limitation.

## RBAC

Two layers, matching this platform's existing principle that the Kubernetes
API server (or here, Argo/K8s RBAC together) is the actual enforcer, not
application code:

1. **Argo's access to the clusters** (backend/service-account layer): scoped
   `ClusterRole`, no `cluster-admin`, as above.
2. **Who can see what** (tenant layer): each team gets its own Argo
   `AppProject`, created by `promises/team`'s pipeline alongside the
   `Namespace` it already writes:
   - `destinations` restricted to `kind-worker`'s `team-<team>` namespace.
   - `sourceNamespaces` restricted to the platform-side `team-<team>`
     namespace (Argo CD's "Applications in any namespace" feature) - so a
     `Container` request's `Application` object lives in that same
     `team-<team>` namespace on `kind-platform`, governed by the exact same
     Capsule `GlobalTenantResource` Group-binding that already restricts
     everything else in that namespace to that team. `kubectl`/API access to
     `Application` objects inherits the real boundary for free.
   - One Argo project-role token per team, scoped to that `AppProject`
     (`get` + `logs` only - no `sync`/`delete`), minted by the broker at
     team-provisioning time (`broker-provision-teams`) via one imperative
     call to Argo's API, and stored as a Secret the broker can read later.
     Token minting is stateful/non-idempotent, which is why this lives in
     the broker's provisioning step rather than as a declarative pipeline
     output - the `AppProject` itself is declarative (pipeline-owned); the
     token is not.
   - The broker uses the *calling team's own* token for every Argo API call
     it makes on that team's behalf - never a shared, broker-wide credential.
     A bug in the broker's routing logic still can't leak another team's
     logs, because the boundary is enforced by Argo/K8s RBAC underneath, the
     same way `impersonate.go` already does for the Kubernetes API.

## Components

| Piece | Where | What |
|---|---|---|
| Argo CD install | new `hack/argo/` + Makefile target | Helm install on `kind-platform`, values scoping its ClusterRole |
| Worker cluster registration | Makefile target | Argo `Secret` (type `Kubernetes cluster`) for `kind-worker` |
| Per-team `AppProject` | `promises/team` pipeline | New output alongside the existing `Namespace` |
| Per-team Argo API token | Broker, `broker-provision-teams` | One imperative mint call + Secret storage |
| Worker namespace-per-team | `container-configure` pipeline | Replace hardcoded `"default"` with `resource.get_namespace()`; emit `Namespace` if missing |
| Tracking label + `Application` | `container-configure` pipeline | `app.kubernetes.io/instance` label on `Deployment`/`Service`; one `Application` (manual sync) per request |
| Argo API client | new `broker/internal/argoclient` | Resource tree + pod logs (SSE) over HTTP |
| Logs endpoint | `broker/internal/api` | `GET /api/promises/{name}/requests/{reqName}/logs` |
| UI logs view | `ui/src/components/RequestsTable.tsx` | Per-request "View logs" action, streaming the broker endpoint |

## Data flow

1. Team created → `promises/team` pipeline emits platform `Namespace` (as
   today) + new `AppProject` → both Flux-delivered to `kind-platform`.
2. `broker-provision-teams` mints that team's Argo project-role token,
   stores it.
3. Team requests a `Container` → resource lands in that team's platform
   namespace, as today.
4. `container-configure` runs: writes worker-side `Namespace` (if needed),
   `Deployment`, `Service` (tracking-labelled) → delivered to `kind-worker`;
   writes an `Application` (manual sync) → delivered to `kind-platform`. Both
   via the existing Flux `Destination`s, no new delivery mechanism.
5. Argo CD (already watching `kind-worker`) discovers the labelled
   `Deployment`/`Service`/pods for that `Application`. It takes no sync
   action - Flux already applied everything.
6. UI requests logs for that `Container` request → broker resolves the
   request's `Application` name → uses that team's stored token → calls
   Argo's resource-tree API to find the pod(s), then its pod-logs API →
   streams the result back to the UI.

## Error handling

- `Application` not yet delivered (pipeline/Flux lag): broker returns 404
  "not ready", not 500.
- No pods yet, or pod crash-looping: broker surfaces Argo's own status
  message rather than failing opaquely.
- Team's Argo token missing (provisioning bug, should not happen in normal
  operation): 500, logged clearly; covered by a broker test asserting
  provisioning always produces a token.
- Argo API unreachable: 502, no internal detail leaked.

## Testing

- **Pipeline unit tests** (`container-configure`, Python): namespace derived
  from `resource.get_namespace()` instead of hardcoded; `Namespace` manifest
  emitted only when needed; tracking label present on `Deployment`/`Service`;
  `Application` manifest has the expected `destination`/`project`/manual
  `syncPolicy`.
- **Pipeline unit tests** (`promises/team`): `AppProject` manifest has the
  expected `destinations`/`sourceNamespaces` scoped to that team only.
- **Broker tests**: mocked Argo server (same precedent as the existing mock
  server implementing version routes), covering the logs endpoint's success
  path and all four error-handling cases above. A negative test asserting
  team A's token cannot fetch team B's logs (mock returns 403 for the wrong
  project, broker surfaces that correctly rather than retrying with a
  different credential).
- **UI**: manual verification that the "View logs" action streams output for
  a running request in `make dev`/`make ui-mock`.
- **End-to-end** (manual, `make up`): submit `Container` requests under two
  different teams; confirm each team's stored token can only fetch its own
  request's logs via the broker endpoint, and that Argo CD shows no sync
  activity (Flux remains the sole applier) - `kubectl get events` on
  `kind-worker` should show no Argo-attributed apply/prune events for these
  objects.

## Open follow-ups (not built here)

- Full Capsule-parity tenancy mirror on the worker cluster (quotas, RBAC) -
  flagged as its own project in the `Container` design doc, unchanged by
  this design.
- Extending this pattern to other workload-producing Promises, if any are
  added later (`Container` is currently the only one).
- Log retention/search beyond what Argo/the underlying pods already provide.
