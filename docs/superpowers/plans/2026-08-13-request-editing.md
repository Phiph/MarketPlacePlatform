# Request Editing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a team edit the `.spec` of an existing request in place, from every page that lists requests, instead of having to delete and resubmit.

**Architecture:** A new `PUT` route (flat and project/environment-scoped) on the broker does a get-modify-write full replace of the target resource's `.spec`, reusing the same impersonated-client/RBAC path every other write uses. The UI's shared `RequestsTable` gains an optional edit affordance (pencil icon → dialog with the existing `SchemaForm`, pre-filled) that all three pages that render it wire up identically.

**Tech Stack:** Go (`net/http` `ServeMux`, `k8s.io/client-go` dynamic client) for the broker; React + TypeScript + shadcn/ui for the frontend. No JS/TS test runner exists in this repo (`ui/package.json` has no `vitest`/`jest`) — UI verification is `npm run build` (type-check) plus manual browser checks against the live broker.

## Global Constraints

- Update is a **full replace of `.spec`**, not a JSON merge-patch — the caller always submits the complete desired spec (matches `Submit`'s create semantics; a merge-patch can't cleanly remove a field the user cleared in the form).
- `Environment` requests are **not editable** through the generic route — same guard, same rationale as `doSubmitRequest`'s existing environment block (spec.team/spec.businessUnit are RBAC-relevant and broker-composed; spec.project is meaningless to change post-creation).
- Every new broker error path must follow the file's existing `apierrors.Is*` → HTTP status mapping convention in `broker/internal/api/server.go`.
- No new UI dependencies — reuse `SchemaForm`, `Dialog`, `Button`, `lucide-react` icons already in the project.

---

### Task 1: `resourceapi.Update` + commit prior uncommitted work

**Files:**
- Modify: `broker/internal/resourceapi/resource.go`
- (Housekeeping, not new code) `promises/database/promise.yaml` and `docs/superpowers/specs/2026-08-13-request-editing-design.md` are already changed/created from earlier in this session and still uncommitted — commit them first so this task's diff is clean.

**Interfaces:**
- Consumes: `catalog.Entry.GVR()` (existing), `dynamic.Interface` (existing).
- Produces: `resourceapi.Update(ctx context.Context, client dynamic.Interface, entry catalog.Entry, namespace, name string, spec map[string]interface{}) (obj *unstructured.Unstructured, ok bool, err error)` — Task 2's `doUpdateRequest` calls this directly.

- [ ] **Step 1: Commit the pre-existing uncommitted changes**

```bash
git add promises/database/promise.yaml docs/superpowers/specs/2026-08-13-request-editing-design.md docs/superpowers/plans/2026-08-13-request-editing.md
git commit -m "docs: add size enum to database Promise schema; add request-editing design + plan"
```

- [ ] **Step 2: Add `Update` to `resourceapi`**

Add this function to `broker/internal/resourceapi/resource.go`, immediately after the existing `Delete` function:

```go
// Update replaces an existing request's .spec wholesale - not a merge
// patch. Callers (the broker's update handlers) always submit the complete
// desired spec, matching how Submit's create semantics already work; a
// merge patch would silently leave behind any field the caller omitted
// because the user cleared it in the form. ok is false if no such request
// exists.
func Update(ctx context.Context, client dynamic.Interface, entry catalog.Entry, namespace, name string, spec map[string]interface{}) (obj *unstructured.Unstructured, ok bool, err error) {
	existing, err := client.Resource(entry.GVR()).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("getting %s %q in namespace %q: %w", entry.Kind, name, namespace, err)
	}

	existing.Object["spec"] = spec

	updated, err := client.Resource(entry.GVR()).Namespace(namespace).Update(ctx, existing, metav1.UpdateOptions{})
	if err != nil {
		return nil, false, fmt.Errorf("updating %s %q in namespace %q: %w", entry.Kind, name, namespace, err)
	}
	return updated, true, nil
}
```

No new imports needed - `metav1`, `apierrors`, `fmt`, and `dynamic` are already imported in this file.

- [ ] **Step 3: Verify it compiles**

Run: `cd broker && go build ./...`
Expected: no output, exit code 0.

- [ ] **Step 4: Commit**

```bash
git add broker/internal/resourceapi/resource.go
git commit -m "feat(broker): add resourceapi.Update for full-spec-replace request edits"
```

---

### Task 2: Broker `PUT` routes and handlers

**Files:**
- Modify: `broker/internal/api/server.go`
- Test: `broker/internal/api/server_test.go`

**Interfaces:**
- Consumes: `resourceapi.Update` (Task 1), existing `tenant.Namespace`, `tenant.ProjectEnvironmentNamespace`, `tenant.Group`, `s.admin.Groups.ForGroup`, `s.lookupPromise`, `teamFromContext`, `writeJSON`, `writeError` - all already in this file.
- Produces: `s.updateRequest`, `s.updateScopedRequest`, `s.doUpdateRequest` handler methods; two new registered routes `PUT /promises/{name}/requests/{reqName}` and `PUT /projects/{project}/environments/{environment}/promises/{name}/requests/{reqName}`.

- [ ] **Step 1: Write the failing tests**

Add to `broker/internal/api/server_test.go`, following the existing `TestDoSubmitRequest_*` pattern exactly (no fake Kubernetes client - these only exercise the guard/validation that returns before the client is ever touched):

```go
// doUpdateRequest is the single choke point behind both the flat and
// project-scoped update routes, same shape as doSubmitRequest above.

func TestDoUpdateRequest_RejectsEnvironmentViaGenericRoute(t *testing.T) {
	s := &Server{}
	entry := catalog.Entry{Name: "environment"}
	req := httptest.NewRequest(http.MethodPut, "/promises/environment/requests/prod",
		strings.NewReader(`{"spec":{"team":"someone-elses-team","businessUnit":"someone-elses-bu"}}`))
	w := httptest.NewRecorder()

	s.doUpdateRequest(w, req, entry, "attacker-team", "team-attacker-team")

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusForbidden, w.Body.String())
	}

	var resp struct{ Error string }
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshalling response body: %v", err)
	}
	if !strings.Contains(resp.Error, "/environments") {
		t.Errorf("error message %q doesn't point the caller at POST /environments", resp.Error)
	}
}

// The guard must fire before the body is even decoded, so it can't be
// bypassed by sending a body that would otherwise fail validation first.
func TestDoUpdateRequest_RejectsEnvironmentBeforeDecodingBody(t *testing.T) {
	s := &Server{}
	entry := catalog.Entry{Name: "environment"}
	req := httptest.NewRequest(http.MethodPut, "/promises/environment/requests/prod", strings.NewReader(`not valid json`))
	w := httptest.NewRecorder()

	s.doUpdateRequest(w, req, entry, "attacker-team", "team-attacker-team")

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusForbidden, w.Body.String())
	}
}

// Every other Promise must still reach the normal body-validation path
// (evidenced here by the "spec" required error, which only fires after the
// environment guard is skipped).
func TestDoUpdateRequest_AllowsOtherPromisesThroughGenericRoute(t *testing.T) {
	s := &Server{}
	entry := catalog.Entry{Name: "database"}
	req := httptest.NewRequest(http.MethodPut, "/promises/database/requests/my-db", strings.NewReader(`{}`))
	w := httptest.NewRecorder()

	s.doUpdateRequest(w, req, entry, "some-team", "team-some-team")

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	var resp struct{ Error string }
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshalling response body: %v", err)
	}
	if !strings.Contains(resp.Error, `"spec" is required`) {
		t.Errorf("error message %q, want the missing-spec validation error", resp.Error)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd broker && go test ./internal/api/... -run TestDoUpdateRequest -v`
Expected: FAIL to compile - `s.doUpdateRequest` doesn't exist yet (`s.doUpdateRequest undefined (type *Server has no field or method doUpdateRequest)`).

- [ ] **Step 3: Implement the handlers**

Add to `broker/internal/api/server.go`, immediately after `doDeleteRequest` (which currently ends the flat/scoped-request handler group, right before the `createEnvironment` function):

```go
func (s *Server) updateRequest(w http.ResponseWriter, r *http.Request) {
	entry, ok := s.lookupPromise(w, r)
	if !ok {
		return
	}
	team := teamFromContext(r.Context())
	s.doUpdateRequest(w, r, *entry, team, tenant.Namespace(team))
}

func (s *Server) updateScopedRequest(w http.ResponseWriter, r *http.Request) {
	entry, ok := s.lookupPromise(w, r)
	if !ok {
		return
	}
	team := teamFromContext(r.Context())
	namespace := tenant.ProjectEnvironmentNamespace(team, r.PathValue("project"), r.PathValue("environment"))
	s.doUpdateRequest(w, r, *entry, team, namespace)
}

// doUpdateRequest replaces an existing request's .spec wholesale (see
// resourceapi.Update). Same environment guard as doSubmitRequest, for the
// same reason: spec.team/spec.businessUnit are RBAC-relevant and
// broker-composed for Environment, and spec.project is meaningless to
// change post-creation since the namespace it produced is already fixed.
func (s *Server) doUpdateRequest(w http.ResponseWriter, r *http.Request, entry catalog.Entry, team, namespace string) {
	if entry.Name == "environment" {
		writeError(w, http.StatusForbidden, "environment requests can't be updated via this endpoint; use POST /environments")
		return
	}

	var body struct {
		Spec map[string]interface{} `json:"spec"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxRequestBody)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if body.Spec == nil {
		writeError(w, http.StatusBadRequest, "\"spec\" is required")
		return
	}

	client, err := s.admin.Groups.ForGroup(tenant.Group(team))
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	reqName := r.PathValue("reqName")
	updated, ok, err := resourceapi.Update(r.Context(), client, entry, namespace, reqName, body.Spec)
	switch {
	case apierrors.IsInvalid(err):
		writeError(w, http.StatusBadRequest, err.Error())
		return
	case apierrors.IsForbidden(err):
		writeError(w, http.StatusForbidden, err.Error())
		return
	case apierrors.IsConflict(err):
		writeError(w, http.StatusConflict, "the request was modified concurrently; reload and try again")
		return
	case err != nil:
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "no such request: "+reqName)
		return
	}
	writeJSON(w, http.StatusOK, updated.Object)
}
```

- [ ] **Step 4: Register the routes**

In `Handler()`, add a line right after the flat `DELETE` route (currently `apiMux.HandleFunc("DELETE /promises/{name}/requests/{reqName}", s.deleteRequest)`):

```go
	apiMux.HandleFunc("PUT /promises/{name}/requests/{reqName}", s.updateRequest)
```

And right after the scoped `DELETE` route (currently `apiMux.HandleFunc("DELETE /projects/{project}/environments/{environment}/promises/{name}/requests/{reqName}", s.deleteScopedRequest)`):

```go
	apiMux.HandleFunc("PUT /projects/{project}/environments/{environment}/promises/{name}/requests/{reqName}", s.updateScopedRequest)
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd broker && go test ./internal/api/... -run TestDoUpdateRequest -v`
Expected: `PASS` for all three tests.

- [ ] **Step 6: Run the full broker test suite**

Run: `make broker-test`
Expected: all packages pass, no regressions in existing tests.

- [ ] **Step 7: Manual smoke test against the live cluster**

A broker process may already be running from earlier in this session (on `:8878`, against `kind-platform`) - it's running old code, so stop it and restart so the new routes are live:

```bash
pkill -f 'go run ./cmd/broker' || true
cd broker && (BROKER_KUBE_CONTEXT=kind-platform go run ./cmd/broker > /tmp/broker.log 2>&1 &)
sleep 3
curl -s localhost:8878/healthz
```

Then exercise the new route against a real request (the `payments-prod-db` database created earlier in this session, in `data-engine`'s `prod` environment):

```bash
curl -s -X PUT -H "Authorization: Bearer demo-key-payments" \
  -d '{"spec":{"size":"10Gi"}}' \
  localhost:8878/api/projects/data-engine/environments/prod/promises/database/requests/payments-prod-db
```

Expected: `200` with the updated object, `.spec.size` now `"10Gi"`. Confirm on the cluster too:

```bash
kubectl --context kind-platform get database payments-prod-db -n project-payments-data-engine-prod -o jsonpath='{.spec.size}'
```

Expected output: `10Gi`.

And confirm the environment guard holds over the wire, not just in the unit test:

```bash
curl -s -o /dev/null -w '%{http_code}\n' -X PUT -H "Authorization: Bearer demo-key-payments" \
  -d '{"spec":{"team":"attacker"}}' \
  localhost:8878/api/promises/environment/requests/prod
```

Expected output: `403`.

- [ ] **Step 8: Commit**

```bash
git add broker/internal/api/server.go broker/internal/api/server_test.go
git commit -m "feat(broker): add PUT routes for editing existing requests"
```

---

### Task 3: `api.ts` client methods

**Files:**
- Modify: `ui/src/lib/api.ts`

**Interfaces:**
- Consumes: existing `request<T>` helper, `ResourceRequest` type.
- Produces: `api.updateRequest(apiKey: string, promiseName: string, reqName: string, spec: Record<string, unknown>) => Promise<ResourceRequest>` and `api.updateScopedRequest(apiKey: string, project: string, environment: string, promiseName: string, reqName: string, spec: Record<string, unknown>) => Promise<ResourceRequest>` - Tasks 6-8 call these.

- [ ] **Step 1: Add `updateRequest`**

In `ui/src/lib/api.ts`, add immediately after the existing `deleteRequest` entry:

```ts
  updateRequest: (apiKey: string, promiseName: string, reqName: string, spec: Record<string, unknown>) =>
    request<ResourceRequest>(
      apiKey,
      `/promises/${encodeURIComponent(promiseName)}/requests/${encodeURIComponent(reqName)}`,
      { method: 'PUT', body: JSON.stringify({ spec }) },
    ),
```

- [ ] **Step 2: Add `updateScopedRequest`**

Add immediately after the existing `deleteScopedRequest` entry (the last entry in the `api` object):

```ts
  updateScopedRequest: (
    apiKey: string,
    project: string,
    environment: string,
    promiseName: string,
    reqName: string,
    spec: Record<string, unknown>,
  ) =>
    request<ResourceRequest>(
      apiKey,
      `/projects/${encodeURIComponent(project)}/environments/${encodeURIComponent(environment)}/promises/${encodeURIComponent(promiseName)}/requests/${encodeURIComponent(reqName)}`,
      { method: 'PUT', body: JSON.stringify({ spec }) },
    ),
```

- [ ] **Step 3: Verify it type-checks**

Run: `cd ui && npm run build`
Expected: exit code 0, `dist/` produced, no TypeScript errors.

- [ ] **Step 4: Commit**

```bash
git add ui/src/lib/api.ts
git commit -m "feat(ui): add updateRequest/updateScopedRequest API client methods"
```

---

### Task 4: `RequestEditDialog` component

**Files:**
- Create: `ui/src/components/RequestEditDialog.tsx`

**Interfaces:**
- Consumes: `SchemaForm` (`schema`, `value`, `onChange` props, existing), `Dialog`/`DialogContent`/`DialogHeader`/`DialogTitle`/`DialogDescription`/`DialogFooter` (existing shadcn components, same set `RequestDetailDialog`/`ProjectDetailPage` already use), `JsonSchema` and `ResourceRequest` types (existing).
- Produces: `RequestEditDialog({ request, schema, open, onOpenChange, onSave })` component - Task 5 renders it.

- [ ] **Step 1: Write the component**

```tsx
import { useEffect, useState } from 'react'
import { Loader2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { SchemaForm } from '@/components/SchemaForm'
import type { JsonSchema, ResourceRequest } from '@/lib/types'

export function RequestEditDialog({
  request,
  schema,
  open,
  onOpenChange,
  onSave,
}: {
  request: ResourceRequest | null
  schema: JsonSchema | undefined
  open: boolean
  onOpenChange: (open: boolean) => void
  onSave: (spec: Record<string, unknown>) => Promise<void>
}) {
  const [spec, setSpec] = useState<Record<string, unknown>>({})
  const [saving, setSaving] = useState(false)

  // Reset to the request's current spec whenever the dialog opens for a
  // (possibly different) request - keyed off `open`/`request` rather than
  // every request prop change, so a mid-edit polling refresh elsewhere on
  // the page doesn't clobber what the user is typing.
  useEffect(() => {
    if (open) setSpec(request?.spec ?? {})
  }, [open, request])

  async function handleSave(e: React.FormEvent) {
    e.preventDefault()
    setSaving(true)
    try {
      await onSave(spec)
      onOpenChange(false)
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[80vh] max-w-2xl overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="font-mono">{request?.metadata.name}</DialogTitle>
          <DialogDescription>Edit this request's spec and save to apply the change.</DialogDescription>
        </DialogHeader>

        <form className="space-y-4" onSubmit={handleSave}>
          <SchemaForm schema={schema} value={spec} onChange={setSpec} />
          <DialogFooter>
            <Button type="submit" disabled={saving}>
              {saving && <Loader2 className="size-4 animate-spin" />}
              Save changes
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
```

- [ ] **Step 2: Verify it type-checks**

Run: `cd ui && npm run build`
Expected: exit code 0. (The component isn't imported anywhere yet, so this only confirms it compiles standalone - full behavior is verified once Task 5 wires it in.)

- [ ] **Step 3: Commit**

```bash
git add ui/src/components/RequestEditDialog.tsx
git commit -m "feat(ui): add RequestEditDialog component"
```

---

### Task 5: Wire editing into `RequestsTable`

**Files:**
- Modify: `ui/src/components/RequestsTable.tsx`

**Interfaces:**
- Consumes: `RequestEditDialog` (Task 4), `JsonSchema`/`ResourceRequest` types (existing).
- Produces: `RequestsTable` gains two new optional props - `schemaFor?: (req: ResourceRequest) => JsonSchema | undefined` and `onSaveEdit?: (req: ResourceRequest, spec: Record<string, unknown>) => Promise<void>` - Tasks 6-8 pass these in. When omitted, `RequestsTable` renders exactly as it does today (no edit icon) - non-breaking for any caller that doesn't opt in.

- [ ] **Step 1: Add the new props and edit-dialog state**

In `ui/src/components/RequestsTable.tsx`, change the imports at the top:

```tsx
import { useState } from 'react'
import { Eye, Pencil, Trash2 } from 'lucide-react'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Button } from '@/components/ui/button'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { StatusBadge } from '@/components/StatusBadge'
import { RequestDetailDialog } from '@/components/RequestDetailDialog'
import { RequestEditDialog } from '@/components/RequestEditDialog'
import type { JsonSchema, ResourceRequest } from '@/lib/types'

interface RequestsTableProps {
  requests: ResourceRequest[]
  onDelete: (name: string) => void
  deletingName?: string | null
  showKind?: boolean
  schemaFor?: (req: ResourceRequest) => JsonSchema | undefined
  onSaveEdit?: (req: ResourceRequest, spec: Record<string, unknown>) => Promise<void>
}

export function RequestsTable({ requests, onDelete, deletingName, showKind, schemaFor, onSaveEdit }: RequestsTableProps) {
  const [selected, setSelected] = useState<ResourceRequest | null>(null)
  const [pendingDelete, setPendingDelete] = useState<string | null>(null)
  const [editing, setEditing] = useState<ResourceRequest | null>(null)
```

- [ ] **Step 2: Add the pencil button next to the existing eye/trash icons**

Change the actions cell (currently just the `Eye` button followed by the delete `Button`) to:

```tsx
                <div className="flex justify-end gap-1" onClick={(e) => e.stopPropagation()}>
                  <Button variant="ghost" size="icon" className="size-8" onClick={() => setSelected(req)}>
                    <Eye className="size-4" />
                  </Button>
                  {onSaveEdit && (
                    <Button variant="ghost" size="icon" className="size-8" onClick={() => setEditing(req)}>
                      <Pencil className="size-4" />
                    </Button>
                  )}
                  <Button
                    variant="ghost"
                    size="icon"
                    className="size-8 text-destructive hover:text-destructive"
                    disabled={deletingName === req.metadata.name}
                    onClick={() => setPendingDelete(req.metadata.name)}
                  >
                    <Trash2 className="size-4" />
                  </Button>
                </div>
```

- [ ] **Step 3: Render the edit dialog**

Immediately after the existing `<RequestDetailDialog .../>` line, add:

```tsx
      {onSaveEdit && (
        <RequestEditDialog
          request={editing}
          schema={editing ? schemaFor?.(editing) : undefined}
          open={editing !== null}
          onOpenChange={(open) => !open && setEditing(null)}
          onSave={(spec) => onSaveEdit(editing!, spec)}
        />
      )}
```

- [ ] **Step 4: Verify it type-checks**

Run: `cd ui && npm run build`
Expected: exit code 0. No caller passes `schemaFor`/`onSaveEdit` yet, so behavior is still unchanged in the running app at this point - confirmed visually once Task 6 wires the first caller.

- [ ] **Step 5: Commit**

```bash
git add ui/src/components/RequestsTable.tsx
git commit -m "feat(ui): add edit affordance to RequestsTable"
```

---

### Task 6: Wire editing into `ServiceDetailPage`

**Files:**
- Modify: `ui/src/pages/ServiceDetailPage.tsx`

**Interfaces:**
- Consumes: `api.updateRequest`/`api.updateScopedRequest` (Task 3), `RequestsTable`'s `schemaFor`/`onSaveEdit` props (Task 5).
- Produces: nothing new consumed by later tasks - this is the first fully end-to-end-testable page.

- [ ] **Step 1: Add the update handler**

In `ui/src/pages/ServiceDetailPage.tsx`, add a new function immediately after the existing `handleDelete`:

```tsx
  async function handleUpdate(req: ResourceRequest, spec: Record<string, unknown>) {
    if (!session) return
    if (target) {
      await api.updateScopedRequest(session.apiKey, target.project, target.environment, name, req.metadata.name, spec)
    } else {
      await api.updateRequest(session.apiKey, name, req.metadata.name, spec)
    }
    toast.success(`Updated "${req.metadata.name}"`)
    loadRequests()
  }
```

(`ResourceRequest` is already imported on this page's `import type` line - no import changes needed.)

- [ ] **Step 2: Pass the new props to `RequestsTable`**

Change the existing `<RequestsTable requests={requests} onDelete={handleDelete} deletingName={deletingName} />` to:

```tsx
              <RequestsTable
                requests={requests}
                onDelete={handleDelete}
                deletingName={deletingName}
                schemaFor={() => specSchema}
                onSaveEdit={handleUpdate}
              />
```

- [ ] **Step 3: Verify it type-checks**

Run: `cd ui && npm run build`
Expected: exit code 0.

- [ ] **Step 4: Manual browser verification**

The broker (restarted in Task 2 Step 7) and UI dev server should still be running from earlier in this session (`localhost:5173`). If not: `make broker-run` and `make ui-dev` in separate terminals, or `make dev` for both.

In the browser: sign in as `payments`, go to Catalog → Postgres Database → "Your requests", click the pencil icon on `payments-prod-db`, change `size` in the dropdown, save. Confirm the toast says "Updated", the table's status badge still shows the request, and:

```bash
kubectl --context kind-platform get database payments-prod-db -n project-payments-data-engine-prod -o jsonpath='{.spec.size}'
```

reflects the new value.

- [ ] **Step 5: Commit**

```bash
git add ui/src/pages/ServiceDetailPage.tsx
git commit -m "feat(ui): wire request editing into ServiceDetailPage"
```

---

### Task 7: Wire editing into `ProjectDetailPage`

**Files:**
- Modify: `ui/src/pages/ProjectDetailPage.tsx`

**Interfaces:**
- Consumes: `api.updateScopedRequest` (Task 3), `RequestsTable`'s `schemaFor`/`onSaveEdit` props (Task 5).

- [ ] **Step 1: Track each row's schema alongside its `promiseName`**

Change the `EnvironmentRequest` interface and the `import type` line at the top:

```tsx
import type { Environment, JsonSchema, ResourceRequest } from '@/lib/types'
```

```tsx
interface EnvironmentRequest extends ResourceRequest {
  promiseName: string
  schema?: JsonSchema
}
```

- [ ] **Step 2: Carry `entry.schema` through the existing fan-out**

In `load()`, change:

```tsx
              try {
                const reqs = await api.listScopedRequests(session.apiKey, project, env.metadata.name, entry.name)
                return reqs.map((r) => ({ ...r, promiseName: entry.name }))
              } catch {
```

to:

```tsx
              try {
                const reqs = await api.listScopedRequests(session.apiKey, project, env.metadata.name, entry.name)
                return reqs.map((r) => ({ ...r, promiseName: entry.name, schema: entry.schema }))
              } catch {
```

- [ ] **Step 3: Add the update handler**

Add immediately after the existing `handleDeleteRequest`:

```tsx
  async function handleUpdateRequest(environment: string, req: EnvironmentRequest, spec: Record<string, unknown>) {
    if (!session) return
    await api.updateScopedRequest(session.apiKey, project, environment, req.promiseName, req.metadata.name, spec)
    toast.success(`Updated "${req.metadata.name}"`)
    void load()
  }
```

- [ ] **Step 4: Pass the new props to `RequestsTable`**

Change the existing `<RequestsTable requests={envRequests} showKind deletingName={deletingReqName} onDelete={(reqName) => void handleDeleteRequest(env.metadata.name, reqName)} />` to:

```tsx
                    <RequestsTable
                      requests={envRequests}
                      showKind
                      deletingName={deletingReqName}
                      onDelete={(reqName) => void handleDeleteRequest(env.metadata.name, reqName)}
                      schemaFor={(req) => (req as EnvironmentRequest).schema}
                      onSaveEdit={(req, spec) => handleUpdateRequest(env.metadata.name, req as EnvironmentRequest, spec)}
                    />
```

- [ ] **Step 5: Verify it type-checks**

Run: `cd ui && npm run build`
Expected: exit code 0.

- [ ] **Step 6: Manual browser verification**

In the browser: sign in as `checkout`, go to Projects → `data-engine`, click the pencil icon on `checkout-prod-db` under the `prod` environment card, change `size`, save. Confirm via:

```bash
kubectl --context kind-platform get database checkout-prod-db -n project-checkout-data-engine-prod -o jsonpath='{.spec.size}'
```

- [ ] **Step 7: Commit**

```bash
git add ui/src/pages/ProjectDetailPage.tsx
git commit -m "feat(ui): wire request editing into ProjectDetailPage"
```

---

### Task 8: Wire editing into `RequestsPage`

**Files:**
- Modify: `ui/src/pages/RequestsPage.tsx`

**Interfaces:**
- Consumes: `api.updateRequest` (Task 3), `RequestsTable`'s `schemaFor`/`onSaveEdit` props (Task 5).

- [ ] **Step 1: Track each row's schema alongside its `promiseName`**

Change the `import type` line and the `TaggedRequest` interface:

```tsx
import type { CatalogEntry, JsonSchema, ResourceRequest } from '@/lib/types'
```

```tsx
interface TaggedRequest extends ResourceRequest {
  promiseName: string
  schema?: JsonSchema
}
```

- [ ] **Step 2: Carry `entry.schema` through the existing fan-out**

In `load()`, change:

```tsx
            const reqs = await api.listRequests(session.apiKey, entry.name)
            return reqs.map((r) => ({ ...r, promiseName: entry.name }))
```

to:

```tsx
            const reqs = await api.listRequests(session.apiKey, entry.name)
            return reqs.map((r) => ({ ...r, promiseName: entry.name, schema: entry.schema }))
```

- [ ] **Step 3: Add the update handler**

Add immediately after the existing `handleDelete`:

```tsx
  async function handleUpdate(req: TaggedRequest, spec: Record<string, unknown>) {
    if (!session) return
    await api.updateRequest(session.apiKey, req.promiseName, req.metadata.name, spec)
    toast.success(`Updated "${req.metadata.name}"`)
    void load()
  }
```

- [ ] **Step 4: Pass the new props to `RequestsTable`**

Change the existing `<RequestsTable requests={requests} showKind deletingName={deletingName} onDelete={...} />` to:

```tsx
          <RequestsTable
            requests={requests}
            showKind
            deletingName={deletingName}
            onDelete={(reqName) => {
              const match = requests.find((r) => r.metadata.name === reqName)
              if (match) void handleDelete(match.promiseName, reqName)
            }}
            schemaFor={(req) => (req as TaggedRequest).schema}
            onSaveEdit={(req, spec) => handleUpdate(req as TaggedRequest, spec)}
          />
```

- [ ] **Step 5: Verify it type-checks**

Run: `cd ui && npm run build`
Expected: exit code 0.

- [ ] **Step 6: Manual browser verification**

In the browser: go to "My Requests" (still signed in as whichever team), click the pencil icon on any flat (non-project-scoped) request row, change a field, save. Confirm the toast and the updated value in the table/detail dialog.

- [ ] **Step 7: Commit**

```bash
git add ui/src/pages/RequestsPage.tsx
git commit -m "feat(ui): wire request editing into RequestsPage"
```

---

### Task 9: End-to-end verification pass

**Files:** none (verification only, no code changes expected).

- [ ] **Step 1: Full broker test suite**

Run: `make broker-test`
Expected: all tests pass.

- [ ] **Step 2: Full UI build**

Run: `cd ui && npm run build`
Expected: exit code 0.

- [ ] **Step 3: Cross-check the environment guard one more time over HTTP**

```bash
curl -s -o /dev/null -w '%{http_code}\n' -X PUT -H "Authorization: Bearer demo-key-checkout" \
  -d '{"spec":{"team":"attacker"}}' \
  localhost:8878/api/promises/environment/requests/prod
```

Expected: `403`.

- [ ] **Step 4: Confirm each of the three pages was exercised in the browser**

Cross-reference Task 6 Step 4, Task 7 Step 6, and Task 8 Step 6 above all passed in this session. If any was skipped, do it now before considering the feature done.
