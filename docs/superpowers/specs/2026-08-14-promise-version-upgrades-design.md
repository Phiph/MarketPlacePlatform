# Self-service Promise version upgrades

## Problem

Kratix tracks a Promise's evolution as a series of `PromiseRevision` objects
(one immutable snapshot per `kratix.io/promise-version`) and ties each
resource request to a specific revision via a `ResourceBinding`
(`spec.version`, defaulting to `latest`). Moving a request to a different
revision is just editing that binding's `.spec.version` - Kratix reconciles
the Resource Configure workflow against the new revision automatically.

The broker (`broker/internal/api/server.go`) has no idea any of this exists.
A team has no way to see that a Promise they've requested against has a
newer version, and no way to move their request to it (or back) without
reaching for `kubectl` directly - which defeats the whole point of the
broker being the thing teams talk to instead of the Kubernetes API (see
`project-marketplace-broker-api` memory).

**Two different "versions" already coexist in this codebase and must stay
distinct.** `catalog.Entry.Version` is the CRD *schema* version
(`v1alpha1`) - which `versions[]` entry in `spec.api.spec.versions` a
Promise's request schema was picked from. This spec is about the *Promise*
version (`kratix.io/promise-version`, e.g. `v0.1.0` -> `v0.2.0`) - an
unrelated axis. New fields/types introduced below are named to keep them
visibly separate (`PromiseVersion`, never `Version`).

## Goals

- A team can see, for a Promise they've requested against, which version
  their request is currently bound to and whether other versions exist.
- A team can move their own request to any known revision of that Promise -
  forward (upgrade) or back (rollback) - through the broker, scoped to
  their own namespace via the same impersonated-client path every other
  write already uses. No new trust boundary.
- Before moving a request, the broker checks the request's current `spec`
  would still be valid against the target revision's schema, and rejects
  with a clear error if not - rather than letting the move succeed and the
  Resource Configure workflow fail asynchronously.

## Non-goals

- **UI.** This project's established order is broker API before UI (see
  `project-marketplace-broker-api` memory - the broker exists specifically
  to give a UI a contract to build against). This spec ends at the broker
  contract; a follow-on spec designs the UI's version picker / upgrade
  affordance against it.
- **Platform-initiated push upgrades** (canary rollouts, "move every
  `dev` environment, hold `prod` back"). The broker's auth model today
  (`broker/config/teams.yaml`) has exactly one identity tier - a caller is
  always exactly one team, RBAC-boundaried to that team's own namespace.
  There's no cross-namespace "platform" identity to build a push path on
  top of, and inventing one is a bigger change than this spec's scope.
  Deferred to a follow-on spec; see "Future work" below for the shape it'd
  likely take.
- **Switching Promise installation to `PromiseRelease`.** Promises in this
  repo are installed directly (`kubectl apply` / `kratix` CLI, via the
  Makefile), not through a `PromiseRelease` object. This spec doesn't
  change that. See "Open risk" below for what that means for testing.

## Broker API

### New read endpoints

```
GET /promises/{name}/versions
GET /promises/{name}/requests/{reqName}/version
GET /projects/{project}/environments/{environment}/promises/{name}/requests/{reqName}/version
```

`GET /promises/{name}/versions` lists every `PromiseRevision` for that
Promise, read via `s.admin.Dynamic` - the broker's own client, not a
team-impersonated one. This matches how `GET /promises` and
`GET /promises/{name}` already work: *which versions exist* is catalog
information, the same trust level as the catalog itself, not something
that needs per-team RBAC. Response:

```json
[
  {"version": "v0.1.0", "latest": false, "createdAt": "2026-07-01T12:00:00Z"},
  {"version": "v0.2.0", "latest": true,  "createdAt": "2026-08-10T09:30:00Z"}
]
```

`GET .../requests/{reqName}/version` returns the calling team's own
request's current binding state - team-impersonated client, same as
`doGetRequest`:

```json
{"boundVersion": "v0.1.0", "latestVersion": "v0.2.0", "upgradeAvailable": true}
```

`boundVersion` is resolved to an actual version even when the underlying
`ResourceBinding.spec.version` is the literal string `"latest"`, so a
caller never has to cross-reference the versions list just to know what
"latest" currently means. 404 (`"no such request: %s"`, same message
`doGetRequest` already uses) if the request itself doesn't exist; also 404
if the request exists but its binding doesn't yet (the same narrow
creation race `bindingapi.Get` documents below) - the caller can't
distinguish these two cases from the response, which is fine since both
mean "there's nothing to show yet."

This is a **new, separate sub-resource** rather than added fields on the
existing `GET .../requests/{reqName}` response. That endpoint does
`writeJSON(w, http.StatusOK, obj.Object)` - it passes the raw Kubernetes
object straight through, unmodified, and the UI's existing `RequestsTable`
et al. read `.spec`/`.status`/`.metadata` directly off that shape (see
`2026-08-13-request-editing-design.md`). Injecting synthetic fields into
that object, or wrapping it in an envelope, would touch a contract three
existing UI call sites already depend on. A sibling sub-resource avoids
that entirely and mirrors the existing pattern of dedicated endpoints for
things that aren't plain CRUD (`POST /environments`).

### New write endpoint

```
POST /promises/{name}/requests/{reqName}/version
POST /projects/{project}/environments/{environment}/promises/{name}/requests/{reqName}/version
```

Body: `{"version": "v0.2.0"}`. Team-impersonated client
(`s.admin.Groups.ForGroup(tenant.Group(team))`), same as every other
request-mutating route. No directionality check - moving to an older
revision uses this exact same endpoint, since Kratix itself treats a
`ResourceBinding` move as "point at revision X," not "upgrade" specifically.
Handler (`doSetRequestVersion`, following the file's existing
flat/scoped-wrapper-plus-`do*` shape):

1. Decode `{Version string}`. 400 if empty.
2. Look up the target `PromiseRevision`'s schema via
   `catalog.RevisionSchema(ctx, s.admin.Dynamic, entry.Name, version)`. 404
   (`"no such promise version: %s"`) if that version doesn't exist.
3. Fetch the request's current spec (`resourceapi.Get`, already-impersonated
   client) and validate it against the target schema with
   `catalog.ValidateAgainstSchema(schema, spec)`. Non-empty result -> 400
   with the list of problems (e.g. `["missing required field \"size\""]`).
4. `bindingapi.SetVersion(ctx, client, namespace, entry.Name, reqName, version)`.
   `apierrors.IsNotFound` -> 404 (no such request, or its binding hasn't
   been created yet - see `bindingapi` below); `IsConflict` -> 409, same
   "reload and try again" message `doUpdateRequest` already uses for the
   same race; `IsForbidden` -> 403.
5. 200, body identical to the `GET .../version` shape above (recomputed
   post-move).

### `catalog` package additions

Promises embed a CRD manifest at `spec.api`; `PromiseRevision` embeds the
exact same manifest one level deeper, at `spec.promiseSpec.api` (a revision
is a snapshot of "the full promise.spec"). `parseEntry` already has the
logic to pull `{group, kind, plural, scope, version, schema}` out of that
shape (via `pickVersion`) - factor the CRD-walking part out into a shared
helper so revision-schema lookups reuse it instead of duplicating it:

```go
// unexported, used by both parseEntry (reads spec.api) and RevisionSchema
// (reads spec.promiseSpec.api) - same CRD manifest shape either way.
func parseCRD(apiObj map[string]interface{}) (group, kind, plural, scope, crdVersion string, schema map[string]interface{}, ok bool)

var PromiseRevisionGVR = schema.GroupVersionResource{
	Group: "platform.kratix.io", Version: "v1alpha1", Resource: "promiserevisions",
}

type Revision struct {
	Version   string    `json:"version"`
	Latest    bool      `json:"latest"`
	CreatedAt time.Time `json:"createdAt"`
}

// ListRevisions returns every PromiseRevision for promiseName, sorted
// newest-first. Filters by the kratix.io/promise-name label Kratix sets on
// every revision it creates.
func ListRevisions(ctx context.Context, client dynamic.Interface, promiseName string) ([]Revision, error)

// RevisionSchema returns the request schema embedded in the named
// PromiseRevision - the same shape Entry.Schema holds, just sourced from a
// specific historical revision instead of the Promise's current spec.
// ok is false if no such version exists for this Promise.
func RevisionSchema(ctx context.Context, client dynamic.Interface, promiseName, version string) (schema map[string]interface{}, ok bool, err error)

// ValidateAgainstSchema checks spec against an OpenAPI v3 schema
// (properties/type/enum/required only - the subset every Promise in this
// repo's schemas actually uses; see promises/*/promise.yaml). Returns a
// human-readable problem per violation, empty when spec is valid. Not a
// full JSON Schema validator - deliberately, to avoid pulling in
// k8s.io/apiextensions-apiserver's structural-schema/CEL machinery for
// schemas this simple, matching the project's existing
// stdlib-over-framework preference (see project-marketplace-broker-api
// memory: no framework for the broker itself either).
func ValidateAgainstSchema(schema map[string]interface{}, spec map[string]interface{}) []string
```

`Entry` also gains a `PromiseVersion string` field, read off the Promise
object's existing `kratix.io/promise-version` label in `parseEntry` -
already-available data, zero extra calls.

### New `bindingapi` package

Mirrors `resourceapi`'s shape (one small package per Kubernetes object type
the broker manages, matching the existing `catalog` / `resourceapi` split):

```go
package bindingapi

var BindingGVR = schema.GroupVersionResource{
	Group: "platform.kratix.io", Version: "v1alpha1", Resource: "resourcebindings",
}

// Get finds the ResourceBinding for a request by the two labels Kratix
// sets on every binding it creates (kratix.io/promise-name,
// kratix.io/resource-name) - the docs show the identical lookup via
// `kubectl get resourcebindings -l ...`, so the binding's own object name
// is treated as opaque/Kratix-owned, never constructed by this package.
func Get(ctx context.Context, client dynamic.Interface, namespace, promiseName, resourceName string) (obj *unstructured.Unstructured, ok bool, err error)

// SetVersion moves an existing binding to version (get-modify-write, same
// optimistic-concurrency shape as resourceapi.Update - the fetched
// resourceVersion rides along on the Update call, so a concurrent move
// surfaces as IsConflict). ok is false if no binding exists yet for this
// request.
func SetVersion(ctx context.Context, client dynamic.Interface, namespace, promiseName, resourceName, version string) (obj *unstructured.Unstructured, ok bool, err error)
```

A `ResourceBinding` is created automatically by Kratix the moment a
resource request is submitted (bound to `latest`) - `SetVersion` only ever
moves an existing one; it never creates one from scratch. A `Get` miss
(`ok=false`) surfaces as 404, which in practice should only happen in the
narrow window between a request being created and Kratix's own controller
creating its binding.

## RBAC

Teams currently have **no access at all** to the `platform.kratix.io` API
group - confirmed by reading
`promises/business-unit/workflows/promise/configure/dependencies/configure-deps/resources/marketplace-rbac.yaml`,
whose aggregated `ClusterRole` only covers `demo.kratix.io` (every
Promise's own CRDs). `ResourceBinding` lives in `platform.kratix.io`, so
without a change, every call in this spec would 403 for every team.

Add a second rule to that same `ClusterRole` (still aggregated into
`edit`, still enforced per-namespace by the existing `team-rbac.yaml`
`GlobalTenantResource` - no new isolation mechanism, purely "add the
verb"):

```yaml
- apiGroups: ["platform.kratix.io"]
  resources: ["resourcebindings"]
  verbs: ["get", "list", "watch", "patch"]
```

No RBAC change needed for `PromiseRevision` - it's read via the broker's
own admin client (see above), same as `Promise` objects already are.

## Testing

Following the repo's existing three-tier strategy
(`broker/internal/resourceapi/resource_test.go`,
`broker/internal/api/server_update_test.go`,
`broker/internal/api/journey_integration_test.go`):

- **Fake unit tests** (`catalog`, `bindingapi`, `server`): seed
  `PromiseRevision` and `ResourceBinding` objects directly into the fake
  dynamic client, same as any other object `k8sclient.NewFake` seeds today
  - no dependency on how revisions get created in a real cluster. Cover:
  `ListRevisions` ordering/latest-flag, `RevisionSchema` miss on an unknown
  version, `ValidateAgainstSchema` against the `database` Promise's actual
  schema (missing required field, wrong enum value, valid spec passes),
  `bindingapi.Get`/`SetVersion` label-selector lookup and the
  get-modify-write conflict path, and `doSetRequestVersion`'s full
  happy/400/404/409 matrix.
- **`BROKER_FAKE_K8S=1` HTTP tier**: the UI-facing contract - exercise the
  new routes over real HTTP against the fake backend, same as the existing
  fake-backed suite the UI's vitest tests hit directly.
- **Real-cluster integration tier**: see "Open risk" immediately below -
  this tier can prove the RBAC change actually works (a team's
  impersonated client really can read/patch its own `ResourceBinding` and
  really can't touch another team's), but proving an *actual* version move
  reconciles end-to-end needs a second real `PromiseRevision` to exist,
  which this spec doesn't yet have a recipe for.

## Open risk

Kratix auto-creates a `PromiseRevision` "when a Promise is installed" and
"every time a Promise version changes" - the docs describe this in terms
of `PromiseRelease`, which this repo deliberately isn't adopting yet (see
Non-goals). It's unconfirmed whether directly re-applying the `database`
Promise object with a bumped `kratix.io/promise-version` label (this
repo's actual install path) also triggers revision creation, or whether
that mechanism is specific to the `PromiseRelease` controller.

This needs verifying against the real `kind-platform` cluster during
implementation. If direct re-apply doesn't trigger it, the real-cluster
test tier will need `database` to grow a second `promise.yaml` variant
(`v0.2.0`) and a way to install it that does trigger revision creation -
which may mean this spec's real-cluster coverage is partial until a
follow-on spec addresses the `PromiseRelease` switch. It doesn't block the
fake-tier work, which needs no real Kratix behavior at all.

## Future work

- **UI**: a version badge + picker on each request's detail view, calling
  `GET/POST .../requests/{reqName}/version`; a "N versions available"
  affordance on the catalog page from `GET /promises/{name}/versions`.
- **Platform-push upgrades**: would need a second auth tier beyond today's
  one-team-per-key model - likely a new `platform` entry in
  `teams.yaml`'s shape (or a separate file) mapped to a Kubernetes identity
  with read/patch on `ResourceBinding` across every team namespace, plus a
  bulk endpoint (e.g. `POST /promises/{name}/versions/{version}/rollout`
  with a namespace/environment filter). Deliberately not designed further
  here since it's a materially bigger change (new trust tier) than
  self-service.
- **`PromiseRelease` adoption**: switching `database`'s install path would
  make version bumps real (an HTTP source serving versioned `promise.yaml`
  files) instead of hand-applied, resolving the open risk above and giving
  every Promise in this repo real, demonstrable multi-revision behavior.
