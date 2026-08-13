# Editing existing requests

## Problem

The broker's REST contract (`broker/internal/api/server.go`) only exposes
create/list/get/delete for Promise resource requests, on both the flat
(`/promises/{name}/requests`) and project/environment-scoped
(`/projects/{project}/environments/{environment}/promises/{name}/requests`)
routes. There is no update path. Consequently the UI's `RequestsTable`
(shared by `ServiceDetailPage`, `ProjectDetailPage`, and `RequestsPage`) only
offers view and delete actions - a user who wants to change, say, a
database's `size` has to delete the request and resubmit it under a new
name.

## Goals

- A team can edit the `spec` of an existing request, in place, from any of
  the three pages that list requests today.
- Editing follows the same tenant-isolation and RBAC path every other
  broker write does (impersonated per-team client) - no new trust boundary.
- `Environment` requests remain non-editable through the generic route, for
  the same reason they're non-*creatable* through it (see below).

## Non-goals

- Editing `metadata` (name, labels) - only `.spec` changes.
- A generic diff/merge UI - the edit form re-renders the exact same
  `SchemaForm` the create flow uses, just pre-filled.
- Editing `Project`/`Environment`-specific broker-composed fields
  (`spec.team`, `spec.businessUnit`) - blocked, see below.

## Broker API

### New routes

```
PUT /promises/{name}/requests/{reqName}
PUT /projects/{project}/environments/{environment}/promises/{name}/requests/{reqName}
```

Registered in `Handler()` immediately after their `DELETE` counterparts,
same path patterns. Body: `{"spec": {...}}` - no `name` field, since the
path already identifies the request being edited.

Handlers follow the file's existing flat/scoped-wrapper-plus-`do*` shape:

- `updateRequest` / `updateScopedRequest` - resolve `entry`, `team`,
  `namespace` (identical to `deleteRequest`/`deleteScopedRequest`), then
  call `doUpdateRequest`.
- `doUpdateRequest(w, r, entry, team, namespace)`:
  1. Reject `entry.Name == "environment"` with 403 before touching the
     body or the client - identical guard, identical rationale, and
     identical error message shape to `doSubmitRequest`'s environment
     guard (spec.team/spec.businessUnit are RBAC-relevant and
     broker-composed; spec.project is meaningless to change post-creation
     since the namespace name is already fixed).
  2. Decode `{Spec map[string]interface{}}` from the body.
  3. Build the impersonated client via `s.admin.Groups.ForGroup(tenant.Group(team))`.
  4. Call `resourceapi.Update(ctx, client, entry, namespace, reqName, spec)`.
  5. Map errors: `apierrors.IsNotFound` (surfaced as `ok=false` from
     `resourceapi.Update`) → 404; `apierrors.IsInvalid` → 400;
     `apierrors.IsForbidden` → 403; `apierrors.IsConflict` → 409 (new -
     the first write path that can actually race, since it reads before
     writing); anything else → 502. Success → 200 with the updated object.

### `resourceapi.Update`

```go
func Update(ctx context.Context, client dynamic.Interface, entry catalog.Entry, namespace, name string, spec map[string]interface{}) (obj *unstructured.Unstructured, ok bool, err error)
```

Get-modify-write, not a patch:

1. `client.Resource(entry.GVR()).Namespace(namespace).Get(...)` - `IsNotFound`
   → `(nil, false, nil)`, same convention as `Get`/`Delete`.
2. Replace `existing.Object["spec"]` with `spec` wholesale.
3. `.Update(ctx, existing, metav1.UpdateOptions{})` - the fetched
   `resourceVersion` rides along automatically, giving optimistic
   concurrency for free; a concurrent edit surfaces as `IsConflict`.
4. Wrap non-nil errors with the same `fmt.Errorf("...%q in namespace %q: %w", ...)` shape `Get`/`Delete` use.

**Full replace, not JSON merge-patch.** The form always submits the
complete current spec state, including omitting fields the user has
cleared. A merge-patch only overwrites keys present in the patch body, so a
cleared optional field would silently stick around server-side. Full
replace via `Update` matches `Submit`'s existing semantics (which also
writes a complete spec on create) and has no such footgun.

## UI

### `api.ts`

Two new calls, same shape as the existing submit pair:

```ts
updateRequest(apiKey, promiseName, reqName, spec) // PUT /promises/{name}/requests/{reqName}
updateScopedRequest(apiKey, project, environment, promiseName, reqName, spec) // PUT scoped route
```

### `RequestEditDialog` (new component)

Same shell as `RequestDetailDialog` (a `Dialog`), but body is `SchemaForm`
pre-filled from `request.spec`, plus a "Save changes" button. Owns its own
`spec` state, reset from `request.spec` whenever the dialog opens for a new
request.

### `RequestsTable`

Two new optional props:

- `schemaFor?: (req: ResourceRequest) => JsonSchema | undefined`
- `onSaveEdit?: (req: ResourceRequest, spec: Record<string, unknown>) => Promise<void>`

When both are supplied, a pencil icon joins the existing eye/trash icons.
The table owns "which row is being edited" state itself (same pattern as
its existing delete-confirmation state) and renders one `RequestEditDialog`
bound to that row. Omitting the props (as any future caller could) simply
hides the edit affordance - non-breaking for any caller that doesn't wire
it up.

### Call sites

All three existing `RequestsTable` consumers get wired up, since they
share the one component and leaving edit working on only one would read as
a bug on the others:

- **`ServiceDetailPage`** - already has `entry.schema` and knows the
  current target (flat vs. project/environment) from its existing
  `target` state; `onSaveEdit` calls `updateRequest` or
  `updateScopedRequest` accordingly, then `loadRequests()`.
- **`ProjectDetailPage`** - already fans out `listScopedRequests` per
  catalog entry per environment and tags each row with `promiseName`; it
  needs to additionally keep each entry's `schema` in that same fan-out
  (it already has the full `CatalogEntry`, just wasn't retaining
  `.schema`) so `schemaFor` can look it up per row.
- **`RequestsPage`** ("My Requests") - same change as `ProjectDetailPage`,
  flat-only.

## Testing

- **Go**: unit tests on `doUpdateRequest` following the existing
  `TestDoSubmitRequest_*` pattern in `server_test.go` - no fake Kubernetes
  client needed, since the environment guard returns before the client is
  ever touched:
  - rejects an `environment` update via the generic route (403, before
    body decode)
  - allows other promises through to normal body validation
- **`resourceapi.Update`**: no unit test - the package has no tests for
  `Get`/`Delete` either (no fake `dynamic.Interface` in the test
  dependencies today), so this doesn't set a new precedent either way.
- **Manual**: exercised against the live `kind-platform` cluster + UI dev
  server already running in this session - edit a request's size on each
  of the three pages, confirm the change lands (`kubectl get database ...
  -o yaml`) and the RBAC guard still 403s an environment edit attempt via
  `curl`.
