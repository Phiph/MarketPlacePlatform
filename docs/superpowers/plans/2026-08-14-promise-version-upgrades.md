# Self-service Promise version upgrades Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** let a team see which Kratix Promise version its resource request is bound to, see what other versions exist, and move its own request to a different revision (upgrade or rollback) through the broker - self-service, no platform-team involvement.

**Architecture:** Kratix already tracks this as `PromiseRevision` (a snapshot per `kratix.io/promise-version`) and `ResourceBinding` (ties one request to one revision, `spec.version`). The broker gains: a `catalog` package extension to read revisions and validate a spec against a target revision's schema, a new `bindingapi` package to read/move bindings, two new HTTP endpoints (`GET`/`POST .../requests/{reqName}/version`) plus a catalog-level `GET /promises/{name}/versions`, and an RBAC grant so a team's already-impersonated client can actually touch its own namespace's bindings.

**Tech Stack:** Go 1.26, `k8s.io/client-go` dynamic client + `k8s.io/apimachinery/pkg/apis/meta/v1/unstructured`, stdlib `net/http` (no framework), Go's built-in `testing` + `k8s.io/client-go/dynamic/fake`.

## Global Constraints

- Go 1.26.5, no new framework dependency - matches the broker's existing stdlib-`net/http` design.
- No new heavy dependency for schema validation - write a small structural checker (required/type/enum/properties only) instead of pulling in `k8s.io/apiextensions-apiserver`. See spec's "Broker API > `catalog` package additions."
- Every read/write of a team's own namespaced objects (resource requests, `ResourceBinding`) goes through `s.admin.Groups.ForGroup(tenant.Group(team))` - the impersonated client - never `s.admin.Dynamic` directly. `PromiseRevision` (and `Promise`) reads are the opposite: always `s.admin.Dynamic`, never impersonated, since which versions exist is catalog-level information.
- No directionality check on version moves - moving to an older revision (rollback) uses the exact same code path as moving to a newer one.
- Out of scope (do not implement): the UI, a platform-push/bulk-upgrade auth tier, switching `database`'s install path to `PromiseRelease`. See the spec's "Non-goals."
- Spec: `docs/superpowers/specs/2026-08-14-promise-version-upgrades-design.md` - read it once before starting; every task below implements a piece of it.

---

## Task 1: `catalog.Entry` gains `PromiseVersion`; extract `parseCRD`

**Files:**
- Modify: `broker/internal/catalog/catalog.go`
- Test: `broker/internal/catalog/catalog_test.go`

**Interfaces:**
- Produces: `catalog.Entry.PromiseVersion string` (new field); `catalog.LabelPromiseVersion` (new const, `"kratix.io/promise-version"`); `parseCRD(apiObj map[string]interface{}) (group, kind, plural, scope, crdVersion string, schemaObj map[string]interface{}, ok bool)` (new unexported func) - Task 2 calls this to parse a `PromiseRevision`'s embedded CRD manifest the same way.

This is a pure refactor of `parseEntry` (no behavior change - existing tests must keep passing unmodified) plus one new field. `parseEntry` currently walks `obj.Object, "spec", "api", "spec", ...` directly; pulling that CRD-walking logic out into `parseCRD` lets Task 2 reuse it for `PromiseRevision.spec.promiseSpec.api`, which embeds the identical CRD manifest shape one level deeper.

- [ ] **Step 1: Update the shared test fixture to carry a promise-version label**

In `broker/internal/catalog/catalog_test.go`, add `LabelPromiseVersion: "v0.1.0"` to `databasePromise()`'s labels:

```go
"labels": map[string]interface{}{
    LabelVisible:        "true",
    LabelPromiseVersion: "v0.1.0",
},
```

- [ ] **Step 2: Add the failing assertion to `TestParseEntry`**

In `TestParseEntry`, after the existing `Group`/`Version` assertion, add:

```go
if entry.PromiseVersion != "v0.1.0" {
    t.Errorf("PromiseVersion = %q, want %q", entry.PromiseVersion, "v0.1.0")
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `cd broker && go test ./internal/catalog/... -run TestParseEntry -v`
Expected: FAIL - `entry.PromiseVersion` doesn't exist yet (compile error: `entry.PromiseVersion undefined`).

- [ ] **Step 4: Add the `PromiseVersion` field and `LabelPromiseVersion` const**

In `catalog.go`, add the const next to the other label/annotation consts:

```go
const (
	LabelVisible          = "marketplace.kratix.io/visible"
	LabelPromiseVersion   = "kratix.io/promise-version"
	AnnotationDisplayName = "marketplace.kratix.io/display-name"
	AnnotationDescription = "marketplace.kratix.io/description"
	AnnotationOwner       = "marketplace.kratix.io/owner"
	AnnotationLifecycle   = "marketplace.kratix.io/lifecycle"
	AnnotationSupport     = "marketplace.kratix.io/support"
	AnnotationPolicy      = "marketplace.kratix.io/policy"
)
```

Add the field to `Entry`, right after `Visible`:

```go
type Entry struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Description string `json:"description,omitempty"`
	Visible     bool   `json:"visible"`
	// PromiseVersion is this Promise's current kratix.io/promise-version
	// label - a different axis from Version below (the CRD *schema*
	// version, e.g. v1alpha1). See
	// docs/superpowers/specs/2026-08-14-promise-version-upgrades-design.md.
	PromiseVersion string                 `json:"promiseVersion,omitempty"`
	Group          string                 `json:"group"`
	Version        string                 `json:"version"`
	Kind           string                 `json:"kind"`
	Plural         string                 `json:"plural"`
	Scope          string                 `json:"scope"`
	Schema         map[string]interface{} `json:"schema,omitempty"`
	Status         map[string]interface{} `json:"status,omitempty"`

	Owner           string   `json:"owner,omitempty"`
	Lifecycle       string   `json:"lifecycle,omitempty"`
	Support         string   `json:"support,omitempty"`
	Policy          string   `json:"policy,omitempty"`
	MissingEvidence []string `json:"missingEvidence,omitempty"`
}
```

- [ ] **Step 5: Extract `parseCRD` and rewrite `parseEntry` to use it**

Add this new function (place it right above `pickVersion`):

```go
// parseCRD reads the CustomResourceDefinition manifest embedded at apiObj
// (a Promise's spec.api, or - identical shape - a PromiseRevision's
// spec.promiseSpec.api, since a revision snapshots a Promise's full spec
// verbatim) and picks its storage version's schema, via pickVersion.
func parseCRD(apiObj map[string]interface{}) (group, kind, plural, scope, crdVersion string, schemaObj map[string]interface{}, ok bool) {
	group, _, _ = unstructured.NestedString(apiObj, "spec", "group")
	kind, _, _ = unstructured.NestedString(apiObj, "spec", "names", "kind")
	plural, _, _ = unstructured.NestedString(apiObj, "spec", "names", "plural")
	scope, _, _ = unstructured.NestedString(apiObj, "spec", "scope")
	versions, _, _ := unstructured.NestedSlice(apiObj, "spec", "versions")

	if group == "" || kind == "" || plural == "" || len(versions) == 0 {
		return "", "", "", "", "", nil, false
	}

	crdVersion, schemaObj = pickVersion(versions)
	if crdVersion == "" {
		return "", "", "", "", "", nil, false
	}
	return group, kind, plural, scope, crdVersion, schemaObj, true
}
```

Replace the top of `parseEntry` (the five `unstructured.Nested*` calls through the `pickVersion` call) with:

```go
func parseEntry(obj *unstructured.Unstructured) (Entry, bool) {
	name := obj.GetName()

	apiObj, _, _ := unstructured.NestedMap(obj.Object, "spec", "api")
	group, kind, plural, scope, version, schemaObj, ok := parseCRD(apiObj)
	if !ok {
		return Entry{}, false
	}

	status, _, _ := unstructured.NestedMap(obj.Object, "status")
	// ... displayName/owner/lifecycle/support/policy/missing block is unchanged ...
```

Then add `PromiseVersion: obj.GetLabels()[LabelPromiseVersion],` to the returned `Entry{...}` literal, right after `Visible: obj.GetLabels()[LabelVisible] == "true",`.

- [ ] **Step 6: Run the full catalog test suite**

Run: `cd broker && go test ./internal/catalog/... -v`
Expected: PASS - every existing test (`TestParseEntry`, `TestParseEntryDefaultsHiddenAndDisplayName`, `TestParseEntryMissingAPIIsSkipped`, `TestPickVersionPrefersStorage`, `TestPickVersionFallsBackToServed`, `TestParseEntryOperationalEvidenceFields`, `TestParseEntryOperationalEvidenceMissing`) plus the new assertion.

- [ ] **Step 7: Commit**

```bash
cd broker && git add internal/catalog/catalog.go internal/catalog/catalog_test.go
git commit -m "catalog: add Entry.PromiseVersion, extract parseCRD for reuse by PromiseRevision parsing"
```

---

## Task 2: Read `PromiseRevision`s - `catalog.ListRevisions` / `catalog.RevisionSchema`

**Files:**
- Create: `broker/internal/catalog/revisions.go`
- Test: `broker/internal/catalog/revisions_test.go`

**Interfaces:**
- Consumes: `parseCRD` (Task 1).
- Produces: `catalog.PromiseRevisionGVR schema.GroupVersionResource`; `catalog.LabelPromiseName`, `catalog.LabelLatestRevision` (consts); `catalog.Revision{Version string, Latest bool, CreatedAt time.Time}`; `ListRevisions(ctx, client dynamic.Interface, promiseName string) ([]Revision, error)`; `RevisionSchema(ctx, client dynamic.Interface, promiseName, version string) (schemaObj map[string]interface{}, ok bool, err error)` - Task 5's `listPromiseVersions`/`doGetRequestVersion`/`doSetRequestVersion` handlers call these.

- [ ] **Step 1: Write the failing tests**

Create `broker/internal/catalog/revisions_test.go`:

```go
package catalog

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func revisionFixture(name, promiseName, version string, latest bool, createdAt string) *unstructured.Unstructured {
	labels := map[string]interface{}{LabelPromiseName: promiseName}
	if latest {
		labels[LabelLatestRevision] = "true"
	}
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "platform.kratix.io/v1alpha1",
		"kind":       "PromiseRevision",
		"metadata": map[string]interface{}{
			"name":              name,
			"labels":            labels,
			"creationTimestamp": createdAt,
		},
		"spec": map[string]interface{}{
			"version": version,
			"promiseSpec": map[string]interface{}{
				"api": map[string]interface{}{
					"apiVersion": "apiextensions.k8s.io/v1",
					"kind":       "CustomResourceDefinition",
					"spec": map[string]interface{}{
						"group": "demo.kratix.io",
						"names": map[string]interface{}{
							"kind":   "Database",
							"plural": "databases",
						},
						"scope": "Namespaced",
						"versions": []interface{}{
							map[string]interface{}{
								"name":    "v1alpha1",
								"served":  true,
								"storage": true,
								"schema": map[string]interface{}{
									"openAPIV3Schema": map[string]interface{}{
										"type": "object",
										"properties": map[string]interface{}{
											"spec": map[string]interface{}{
												"type":     "object",
												"required": []interface{}{"size"},
												"properties": map[string]interface{}{
													"size": map[string]interface{}{"type": "string"},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}}
}

func fakeRevisionsClient(objects ...runtime.Object) dynamic.Interface {
	gvrToListKind := map[schema.GroupVersionResource]string{PromiseRevisionGVR: "PromiseRevisionList"}
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), gvrToListKind, objects...)
}

func TestListRevisions_NewestFirstWithLatestFlag(t *testing.T) {
	client := fakeRevisionsClient(
		revisionFixture("database-v0.1.0", "database", "v0.1.0", false, "2026-01-01T00:00:00Z"),
		revisionFixture("database-v0.2.0", "database", "v0.2.0", true, "2026-02-01T00:00:00Z"),
	)

	revisions, err := ListRevisions(context.Background(), client, "database")
	if err != nil {
		t.Fatalf("ListRevisions: %v", err)
	}
	if len(revisions) != 2 {
		t.Fatalf("len(revisions) = %d, want 2", len(revisions))
	}
	if revisions[0].Version != "v0.2.0" || !revisions[0].Latest {
		t.Errorf("revisions[0] = %+v, want v0.2.0 marked latest, listed first (newest)", revisions[0])
	}
	if revisions[1].Version != "v0.1.0" || revisions[1].Latest {
		t.Errorf("revisions[1] = %+v, want v0.1.0, not latest", revisions[1])
	}
}

func TestListRevisions_FiltersByPromiseName(t *testing.T) {
	client := fakeRevisionsClient(
		revisionFixture("database-v0.1.0", "database", "v0.1.0", true, "2026-01-01T00:00:00Z"),
		revisionFixture("redis-v0.1.0", "redis", "v0.1.0", true, "2026-01-01T00:00:00Z"),
	)

	revisions, err := ListRevisions(context.Background(), client, "database")
	if err != nil {
		t.Fatalf("ListRevisions: %v", err)
	}
	if len(revisions) != 1 || revisions[0].Version != "v0.1.0" {
		t.Errorf("ListRevisions(\"database\") = %+v, want exactly the database revision", revisions)
	}
}

func TestRevisionSchema_Found(t *testing.T) {
	client := fakeRevisionsClient(revisionFixture("database-v0.2.0", "database", "v0.2.0", true, "2026-02-01T00:00:00Z"))

	schemaObj, ok, err := RevisionSchema(context.Background(), client, "database", "v0.2.0")
	if err != nil {
		t.Fatalf("RevisionSchema: %v", err)
	}
	if !ok {
		t.Fatal("RevisionSchema: ok = false, want true")
	}
	specSchema, _, _ := unstructured.NestedMap(schemaObj, "properties", "spec")
	if specSchema == nil {
		t.Errorf("RevisionSchema: expected a properties.spec schema, got %v", schemaObj)
	}
}

func TestRevisionSchema_UnknownVersion(t *testing.T) {
	client := fakeRevisionsClient(revisionFixture("database-v0.1.0", "database", "v0.1.0", true, "2026-01-01T00:00:00Z"))

	_, ok, err := RevisionSchema(context.Background(), client, "database", "v9.9.9")
	if err != nil {
		t.Fatalf("RevisionSchema: got err %v, want nil", err)
	}
	if ok {
		t.Error("RevisionSchema: ok = true, want false for an unknown version")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd broker && go test ./internal/catalog/... -run 'TestListRevisions|TestRevisionSchema' -v`
Expected: FAIL to compile - `PromiseRevisionGVR`, `LabelPromiseName`, `LabelLatestRevision`, `ListRevisions`, `RevisionSchema` are undefined.

- [ ] **Step 3: Create `revisions.go`**

```go
package catalog

import (
	"context"
	"fmt"
	"sort"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// PromiseRevisionGVR is where Kratix stores each snapshot of a Promise's
// spec at a specific version - see
// docs.kratix.io/main/reference/promises/promise-upgrade/promise-revisions.
var PromiseRevisionGVR = schema.GroupVersionResource{
	Group:    "platform.kratix.io",
	Version:  "v1alpha1",
	Resource: "promiserevisions",
}

// LabelPromiseName is the label Kratix sets on every PromiseRevision and
// ResourceBinding it creates, identifying which Promise they belong to.
const LabelPromiseName = "kratix.io/promise-name"

// LabelLatestRevision marks the one PromiseRevision Kratix currently
// considers latest for a given Promise.
const LabelLatestRevision = "kratix.io/latest-revision"

// Revision is one version of a Promise, as Kratix snapshotted it.
type Revision struct {
	Version   string    `json:"version"`
	Latest    bool      `json:"latest"`
	CreatedAt time.Time `json:"createdAt"`
}

// ListRevisions returns every PromiseRevision Kratix has created for
// promiseName, newest first.
func ListRevisions(ctx context.Context, client dynamic.Interface, promiseName string) ([]Revision, error) {
	list, err := client.Resource(PromiseRevisionGVR).List(ctx, metav1.ListOptions{
		LabelSelector: LabelPromiseName + "=" + promiseName,
	})
	if err != nil {
		return nil, fmt.Errorf("listing promise revisions for %q: %w", promiseName, err)
	}

	revisions := make([]Revision, 0, len(list.Items))
	for _, item := range list.Items {
		version, _, _ := unstructured.NestedString(item.Object, "spec", "version")
		if version == "" {
			continue
		}
		revisions = append(revisions, Revision{
			Version:   version,
			Latest:    item.GetLabels()[LabelLatestRevision] == "true",
			CreatedAt: item.GetCreationTimestamp().Time,
		})
	}

	sort.Slice(revisions, func(i, j int) bool { return revisions[i].CreatedAt.After(revisions[j].CreatedAt) })
	return revisions, nil
}

// RevisionSchema returns the request schema embedded in promiseName's
// PromiseRevision at version - the same shape Entry.Schema holds, sourced
// from spec.promiseSpec.api instead of a live Promise's spec.api. ok is
// false if no such version exists for this Promise.
func RevisionSchema(ctx context.Context, client dynamic.Interface, promiseName, version string) (schemaObj map[string]interface{}, ok bool, err error) {
	list, err := client.Resource(PromiseRevisionGVR).List(ctx, metav1.ListOptions{
		LabelSelector: LabelPromiseName + "=" + promiseName,
	})
	if err != nil {
		return nil, false, fmt.Errorf("listing promise revisions for %q: %w", promiseName, err)
	}

	for _, item := range list.Items {
		itemVersion, _, _ := unstructured.NestedString(item.Object, "spec", "version")
		if itemVersion != version {
			continue
		}
		apiObj, _, _ := unstructured.NestedMap(item.Object, "spec", "promiseSpec", "api")
		_, _, _, _, _, schemaObj, ok = parseCRD(apiObj)
		return schemaObj, ok, nil
	}
	return nil, false, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd broker && go test ./internal/catalog/... -v`
Expected: PASS - all catalog tests, including the four new ones.

- [ ] **Step 5: Commit**

```bash
cd broker && git add internal/catalog/revisions.go internal/catalog/revisions_test.go
git commit -m "catalog: add ListRevisions/RevisionSchema for reading PromiseRevisions"
```

---

## Task 3: `catalog.ValidateAgainstSchema`

**Files:**
- Create: `broker/internal/catalog/validate.go`
- Test: `broker/internal/catalog/validate_test.go`

**Interfaces:**
- Produces: `ValidateAgainstSchema(schemaObj, spec map[string]interface{}) []string` - Task 5's `doSetRequestVersion` calls this before moving a binding.

- [ ] **Step 1: Write the failing tests**

Create `broker/internal/catalog/validate_test.go`:

```go
package catalog

import "testing"

// databaseSpecSchema mirrors the openAPIV3Schema shape parseCRD/pickVersion
// produce for the real database Promise (see
// broker/cmd/broker/fake_seed.go and promises/database/promise.yaml).
func databaseSpecSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"spec": map[string]interface{}{
				"type":     "object",
				"required": []interface{}{"size"},
				"properties": map[string]interface{}{
					"size": map[string]interface{}{
						"type": "string",
						"enum": []interface{}{"1Gi", "5Gi", "10Gi", "50Gi"},
					},
					"highAvailability": map[string]interface{}{"type": "boolean"},
				},
			},
		},
	}
}

func TestValidateAgainstSchema(t *testing.T) {
	schemaObj := databaseSpecSchema()

	tests := []struct {
		name     string
		spec     map[string]interface{}
		wantErrs int
	}{
		{"valid spec", map[string]interface{}{"size": "10Gi"}, 0},
		{"valid spec with optional field", map[string]interface{}{"size": "10Gi", "highAvailability": true}, 0},
		{"missing required field", map[string]interface{}{"highAvailability": true}, 1},
		{"invalid enum value", map[string]interface{}{"size": "999Gi"}, 1},
		{"wrong type", map[string]interface{}{"size": "10Gi", "highAvailability": "yes"}, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			problems := ValidateAgainstSchema(schemaObj, tt.spec)
			if len(problems) != tt.wantErrs {
				t.Errorf("ValidateAgainstSchema(%v) = %v, want %d problem(s)", tt.spec, problems, tt.wantErrs)
			}
		})
	}
}

func TestValidateAgainstSchema_NoSpecSchema(t *testing.T) {
	problems := ValidateAgainstSchema(map[string]interface{}{}, map[string]interface{}{"anything": true})
	if problems != nil {
		t.Errorf("ValidateAgainstSchema with no spec schema = %v, want nil", problems)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd broker && go test ./internal/catalog/... -run TestValidateAgainstSchema -v`
Expected: FAIL to compile - `ValidateAgainstSchema` is undefined.

- [ ] **Step 3: Create `validate.go`**

```go
package catalog

import (
	"fmt"
	"reflect"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// ValidateAgainstSchema checks spec (a request's .spec, e.g. from
// resourceapi.Get) against schemaObj (an Entry.Schema-shaped OpenAPI v3
// schema for the full custom resource - schemaObj.properties.spec holds
// the actual field-level schema). Returns one human-readable problem per
// violation, empty when spec is valid.
//
// Checks properties/type/enum/required only - the subset every Promise in
// this repo's schemas actually uses (see promises/*/promise.yaml).
// Deliberately not a full JSON Schema validator, to avoid pulling in
// k8s.io/apiextensions-apiserver's structural-schema/CEL machinery for
// schemas this simple.
func ValidateAgainstSchema(schemaObj map[string]interface{}, spec map[string]interface{}) []string {
	specSchema, _, _ := unstructured.NestedMap(schemaObj, "properties", "spec")
	if specSchema == nil {
		return nil
	}
	return validateObject(specSchema, spec, "spec")
}

func validateObject(fieldSchema map[string]interface{}, value map[string]interface{}, path string) []string {
	var problems []string

	required, _, _ := unstructured.NestedStringSlice(fieldSchema, "required")
	for _, name := range required {
		if _, ok := value[name]; !ok {
			problems = append(problems, fmt.Sprintf("missing required field %q", path+"."+name))
		}
	}

	properties, _, _ := unstructured.NestedMap(fieldSchema, "properties")
	for name, rawPropSchema := range properties {
		propValue, present := value[name]
		if !present {
			continue
		}
		propSchema, ok := rawPropSchema.(map[string]interface{})
		if !ok {
			continue
		}
		problems = append(problems, validateValue(propSchema, propValue, path+"."+name)...)
	}

	return problems
}

func validateValue(fieldSchema map[string]interface{}, value interface{}, path string) []string {
	wantType, _, _ := unstructured.NestedString(fieldSchema, "type")

	if wantType != "" && !typeMatches(wantType, value) {
		return []string{fmt.Sprintf("field %q: want type %q, got %T", path, wantType, value)}
	}

	if enum, found, _ := unstructured.NestedSlice(fieldSchema, "enum"); found && !enumContains(enum, value) {
		return []string{fmt.Sprintf("field %q: value %v is not one of the allowed values %v", path, value, enum)}
	}

	if wantType == "object" {
		if obj, ok := value.(map[string]interface{}); ok {
			return validateObject(fieldSchema, obj, path)
		}
	}

	return nil
}

func typeMatches(wantType string, value interface{}) bool {
	switch wantType {
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "number":
		_, ok := value.(float64)
		return ok
	case "integer":
		f, ok := value.(float64)
		return ok && f == float64(int64(f))
	case "object":
		_, ok := value.(map[string]interface{})
		return ok
	case "array":
		_, ok := value.([]interface{})
		return ok
	default:
		return true
	}
}

func enumContains(enum []interface{}, value interface{}) bool {
	for _, allowed := range enum {
		if reflect.DeepEqual(allowed, value) {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd broker && go test ./internal/catalog/... -v`
Expected: PASS - all catalog tests, including the new validator tests.

- [ ] **Step 5: Commit**

```bash
cd broker && git add internal/catalog/validate.go internal/catalog/validate_test.go
git commit -m "catalog: add ValidateAgainstSchema for pre-flight checking a version move"
```

---

## Task 4: `bindingapi` package - read and move `ResourceBinding`s

**Files:**
- Create: `broker/internal/bindingapi/binding.go`
- Test: `broker/internal/bindingapi/binding_test.go`

**Interfaces:**
- Produces: `bindingapi.GVR schema.GroupVersionResource`; `Get(ctx, client dynamic.Interface, namespace, promiseName, resourceName string) (obj *unstructured.Unstructured, ok bool, err error)`; `Version(binding *unstructured.Unstructured, latestVersion string) string`; `SetVersion(ctx, client dynamic.Interface, namespace, promiseName, resourceName, version string) (obj *unstructured.Unstructured, ok bool, err error)` - Task 5's handlers call all three.

This package mirrors `resourceapi`'s shape: one small package per Kubernetes object type the broker manages.

- [ ] **Step 1: Write the failing tests**

Create `broker/internal/bindingapi/binding_test.go`:

```go
package bindingapi

import (
	"context"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
)

func bindingObject(namespace, name, promiseName, resourceName, version string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "platform.kratix.io/v1alpha1",
		"kind":       "ResourceBinding",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": namespace,
			"labels": map[string]interface{}{
				labelPromiseName:  promiseName,
				labelResourceName: resourceName,
			},
		},
		"spec": map[string]interface{}{
			"version": version,
		},
	}}
}

func fakeClient(objects ...runtime.Object) dynamic.Interface {
	gvrToListKind := map[schema.GroupVersionResource]string{GVR: "ResourceBindingList"}
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), gvrToListKind, objects...)
}

func TestGet_Found(t *testing.T) {
	client := fakeClient(bindingObject("team-payments", "database-my-db", "database", "my-db", "v0.1.0"))

	obj, ok, err := Get(context.Background(), client, "team-payments", "database", "my-db")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("Get: ok = false, want true")
	}
	version, _, _ := unstructured.NestedString(obj.Object, "spec", "version")
	if version != "v0.1.0" {
		t.Errorf("spec.version = %q, want %q", version, "v0.1.0")
	}
}

func TestGet_NotFound(t *testing.T) {
	client := fakeClient()

	obj, ok, err := Get(context.Background(), client, "team-payments", "database", "missing-db")
	if err != nil {
		t.Fatalf("Get: got err %v, want nil", err)
	}
	if ok {
		t.Error("Get: ok = true, want false")
	}
	if obj != nil {
		t.Errorf("Get: obj = %v, want nil", obj)
	}
}

func TestVersion_ResolvesLatest(t *testing.T) {
	binding := bindingObject("team-payments", "database-my-db", "database", "my-db", "latest")
	if got := Version(binding, "v0.2.0"); got != "v0.2.0" {
		t.Errorf("Version() = %q, want %q", got, "v0.2.0")
	}
}

func TestVersion_ResolvesConcrete(t *testing.T) {
	binding := bindingObject("team-payments", "database-my-db", "database", "my-db", "v0.1.0")
	if got := Version(binding, "v0.2.0"); got != "v0.1.0" {
		t.Errorf("Version() = %q, want %q", got, "v0.1.0")
	}
}

func TestSetVersion_Success(t *testing.T) {
	client := fakeClient(bindingObject("team-payments", "database-my-db", "database", "my-db", "v0.1.0"))

	updated, ok, err := SetVersion(context.Background(), client, "team-payments", "database", "my-db", "v0.2.0")
	if err != nil {
		t.Fatalf("SetVersion: %v", err)
	}
	if !ok {
		t.Fatal("SetVersion: ok = false, want true")
	}
	version, _, _ := unstructured.NestedString(updated.Object, "spec", "version")
	if version != "v0.2.0" {
		t.Errorf("spec.version = %q, want %q", version, "v0.2.0")
	}
}

func TestSetVersion_NotFound(t *testing.T) {
	client := fakeClient()

	_, ok, err := SetVersion(context.Background(), client, "team-payments", "database", "missing-db", "v0.2.0")
	if err != nil {
		t.Fatalf("SetVersion: got err %v, want nil", err)
	}
	if ok {
		t.Error("SetVersion: ok = true, want false")
	}
}

func TestSetVersion_PropagatesConflict(t *testing.T) {
	existing := bindingObject("team-payments", "database-my-db", "database", "my-db", "v0.1.0")
	gvrToListKind := map[schema.GroupVersionResource]string{GVR: "ResourceBindingList"}
	fake := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), gvrToListKind, existing)
	fake.PrependReactor("update", "resourcebindings", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewConflict(schema.GroupResource{Group: "platform.kratix.io", Resource: "resourcebindings"}, "database-my-db", nil)
	})

	_, ok, err := SetVersion(context.Background(), fake, "team-payments", "database", "my-db", "v0.2.0")
	if err == nil {
		t.Fatal("SetVersion: err = nil, want a conflict error")
	}
	if !apierrors.IsConflict(err) {
		t.Errorf("SetVersion: err = %v, want a conflict error", err)
	}
	if ok {
		t.Error("SetVersion: ok = true, want false on error")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd broker && go test ./internal/bindingapi/... -v`
Expected: FAIL to compile - the `bindingapi` package doesn't exist yet.

- [ ] **Step 3: Create `binding.go`**

```go
// Package bindingapi reads and moves the ResourceBinding tying a resource
// request to a specific PromiseRevision - see
// docs.kratix.io/main/reference/promises/promise-upgrade/resource-bindings.
package bindingapi

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// GVR is where Kratix stores ResourceBindings - namespaced, one per
// resource request.
var GVR = schema.GroupVersionResource{
	Group:    "platform.kratix.io",
	Version:  "v1alpha1",
	Resource: "resourcebindings",
}

const (
	labelPromiseName  = "kratix.io/promise-name"
	labelResourceName = "kratix.io/resource-name"
)

// Get finds the ResourceBinding for one resource request, by the two
// labels Kratix sets on every binding it creates. The binding's own object
// name is Kratix-owned and never constructed here - this is the same
// lookup the Kratix docs show via `kubectl get resourcebindings -l ...`.
// ok is false if no such binding exists yet (e.g. the narrow window
// between a request being created and Kratix's own controller creating
// its binding).
func Get(ctx context.Context, client dynamic.Interface, namespace, promiseName, resourceName string) (obj *unstructured.Unstructured, ok bool, err error) {
	list, err := client.Resource(GVR).Namespace(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelPromiseName + "=" + promiseName + "," + labelResourceName + "=" + resourceName,
	})
	if err != nil {
		return nil, false, fmt.Errorf("listing resource bindings for %s/%s in namespace %q: %w", promiseName, resourceName, namespace, err)
	}
	if len(list.Items) == 0 {
		return nil, false, nil
	}
	return &list.Items[0], true, nil
}

// Version returns the resolved version a binding currently points at:
// binding.spec.version verbatim if it's already a concrete version, or
// latestVersion if the binding says "latest" (the field's default).
func Version(binding *unstructured.Unstructured, latestVersion string) string {
	version, _, _ := unstructured.NestedString(binding.Object, "spec", "version")
	if version == "" || version == "latest" {
		return latestVersion
	}
	return version
}

// SetVersion moves an existing binding to version - get-modify-write, same
// optimistic-concurrency shape as resourceapi.Update: the fetched
// resourceVersion rides along on the Update call, so a concurrent move
// surfaces as a conflict (apierrors.IsConflict). ok is false if no binding
// exists yet for this request.
func SetVersion(ctx context.Context, client dynamic.Interface, namespace, promiseName, resourceName, version string) (obj *unstructured.Unstructured, ok bool, err error) {
	existing, ok, err := Get(ctx, client, namespace, promiseName, resourceName)
	if err != nil || !ok {
		return nil, ok, err
	}

	if err := unstructured.SetNestedField(existing.Object, version, "spec", "version"); err != nil {
		return nil, false, fmt.Errorf("setting spec.version on binding for %s/%s: %w", promiseName, resourceName, err)
	}

	updated, err := client.Resource(GVR).Namespace(namespace).Update(ctx, existing, metav1.UpdateOptions{})
	if err != nil {
		return nil, false, fmt.Errorf("updating resource binding for %s/%s in namespace %q: %w", promiseName, resourceName, namespace, err)
	}
	return updated, true, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd broker && go test ./internal/bindingapi/... -v`
Expected: PASS - all six tests.

- [ ] **Step 5: Commit**

```bash
cd broker && git add internal/bindingapi/
git commit -m "bindingapi: add package to read and move ResourceBindings"
```

---

## Task 5: Broker routes and handlers

**Files:**
- Modify: `broker/internal/api/server.go`
- Test: `broker/internal/api/server_version_test.go`

**Interfaces:**
- Consumes: `catalog.ListRevisions`, `catalog.RevisionSchema`, `catalog.ValidateAgainstSchema`, `catalog.Revision`, `catalog.PromiseRevisionGVR`, `catalog.LabelPromiseName`, `catalog.LabelLatestRevision` (Tasks 1-3); `bindingapi.GVR`, `bindingapi.Get`, `bindingapi.Version`, `bindingapi.SetVersion` (Task 4); existing `s.lookupPromise`, `teamFromContext`, `tenant.Namespace`, `tenant.ProjectEnvironmentNamespace`, `tenant.Group`, `resourceapi.Get`, `writeJSON`, `writeError`.
- Produces: `requestVersionInfo{BoundVersion, LatestVersion string; UpgradeAvailable bool}` (new response type) - this is the wire contract a future UI spec builds against.

- [ ] **Step 1: Write the failing tests**

Create `broker/internal/api/server_version_test.go`. It reuses `testDatabaseEntry`, `testDatabaseObject`, and `fakeGroupResolver` already defined in `server_update_test.go` (same package, same file's fixtures) - only adds what those don't have: a fake client that also serves `PromiseRevision`/`ResourceBinding`.

```go
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"marketplace-broker/internal/bindingapi"
	"marketplace-broker/internal/catalog"
	"marketplace-broker/internal/k8sclient"
)

// serverWithFakeVersionClient is serverWithFakeClient's counterpart for
// this file's tests: the fake client also serves PromiseRevision and
// ResourceBinding, and is wired as *both* s.admin.Dynamic and the
// per-team impersonated client - fake mode never models per-team RBAC
// (see k8sclient.NewFake's doc comment), so one shared fake is correct
// here exactly like it is in production's NewFake.
func serverWithFakeVersionClient(objects ...runtime.Object) *Server {
	gvrToListKind := map[schema.GroupVersionResource]string{
		testDatabaseEntry.GVR():    "DatabaseList",
		catalog.PromiseRevisionGVR: "PromiseRevisionList",
		bindingapi.GVR:             "ResourceBindingList",
	}
	fake := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), gvrToListKind, objects...)
	return &Server{admin: &k8sclient.Clients{
		Dynamic: fake,
		Groups:  fakeGroupResolver{client: fake},
	}}
}

func revisionObject(name, promiseName, version string, latest bool) *unstructured.Unstructured {
	labels := map[string]interface{}{catalog.LabelPromiseName: promiseName}
	if latest {
		labels[catalog.LabelLatestRevision] = "true"
	}
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "platform.kratix.io/v1alpha1",
		"kind":       "PromiseRevision",
		"metadata": map[string]interface{}{
			"name":              name,
			"labels":            labels,
			"creationTimestamp": "2026-01-01T00:00:00Z",
		},
		"spec": map[string]interface{}{
			"version": version,
			"promiseSpec": map[string]interface{}{
				"api": map[string]interface{}{
					"apiVersion": "apiextensions.k8s.io/v1",
					"kind":       "CustomResourceDefinition",
					"spec": map[string]interface{}{
						"group": "demo.kratix.io",
						"names": map[string]interface{}{
							"kind":   "Database",
							"plural": "databases",
						},
						"scope": "Namespaced",
						"versions": []interface{}{
							map[string]interface{}{
								"name":    "v1alpha1",
								"served":  true,
								"storage": true,
								"schema": map[string]interface{}{
									"openAPIV3Schema": map[string]interface{}{
										"type": "object",
										"properties": map[string]interface{}{
											"spec": map[string]interface{}{
												"type":     "object",
												"required": []interface{}{"size"},
												"properties": map[string]interface{}{
													"size": map[string]interface{}{"type": "string"},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}}
}

func testBindingObject(namespace, name, promiseName, resourceName, version string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "platform.kratix.io/v1alpha1",
		"kind":       "ResourceBinding",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": namespace,
			"labels": map[string]interface{}{
				"kratix.io/promise-name":  promiseName,
				"kratix.io/resource-name": resourceName,
			},
		},
		"spec": map[string]interface{}{
			"version": version,
		},
	}}
}

func TestDoGetRequestVersion_Success(t *testing.T) {
	s := serverWithFakeVersionClient(
		testDatabaseObject("team-payments", "my-db", map[string]interface{}{"size": "10Gi"}),
		testBindingObject("team-payments", "my-db-binding", "database", "my-db", "v0.1.0"),
		revisionObject("database-v0.1.0", "database", "v0.1.0", false),
		revisionObject("database-v0.2.0", "database", "v0.2.0", true),
	)

	req := httptest.NewRequest(http.MethodGet, "/promises/database/requests/my-db/version", nil)
	req.SetPathValue("reqName", "my-db")
	w := httptest.NewRecorder()

	s.doGetRequestVersion(w, req, testDatabaseEntry, "payments", "team-payments")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	var got requestVersionInfo
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshalling response body: %v", err)
	}
	want := requestVersionInfo{BoundVersion: "v0.1.0", LatestVersion: "v0.2.0", UpgradeAvailable: true}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestDoGetRequestVersion_RequestNotFound(t *testing.T) {
	s := serverWithFakeVersionClient()

	req := httptest.NewRequest(http.MethodGet, "/promises/database/requests/missing-db/version", nil)
	req.SetPathValue("reqName", "missing-db")
	w := httptest.NewRecorder()

	s.doGetRequestVersion(w, req, testDatabaseEntry, "payments", "team-payments")

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusNotFound, w.Body.String())
	}
}

func TestDoSetRequestVersion_Success(t *testing.T) {
	s := serverWithFakeVersionClient(
		testDatabaseObject("team-payments", "my-db", map[string]interface{}{"size": "10Gi"}),
		testBindingObject("team-payments", "my-db-binding", "database", "my-db", "v0.1.0"),
		revisionObject("database-v0.1.0", "database", "v0.1.0", false),
		revisionObject("database-v0.2.0", "database", "v0.2.0", true),
	)

	req := httptest.NewRequest(http.MethodPost, "/promises/database/requests/my-db/version", strings.NewReader(`{"version":"v0.2.0"}`))
	req.SetPathValue("reqName", "my-db")
	w := httptest.NewRecorder()

	s.doSetRequestVersion(w, req, testDatabaseEntry, "payments", "team-payments")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	var got requestVersionInfo
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshalling response body: %v", err)
	}
	want := requestVersionInfo{BoundVersion: "v0.2.0", LatestVersion: "v0.2.0", UpgradeAvailable: false}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestDoSetRequestVersion_UnknownVersion(t *testing.T) {
	s := serverWithFakeVersionClient(
		testDatabaseObject("team-payments", "my-db", map[string]interface{}{"size": "10Gi"}),
		testBindingObject("team-payments", "my-db-binding", "database", "my-db", "v0.1.0"),
		revisionObject("database-v0.1.0", "database", "v0.1.0", true),
	)

	req := httptest.NewRequest(http.MethodPost, "/promises/database/requests/my-db/version", strings.NewReader(`{"version":"v9.9.9"}`))
	req.SetPathValue("reqName", "my-db")
	w := httptest.NewRecorder()

	s.doSetRequestVersion(w, req, testDatabaseEntry, "payments", "team-payments")

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusNotFound, w.Body.String())
	}
}

func TestDoSetRequestVersion_InvalidForTargetSchema(t *testing.T) {
	// The target revision requires "size"; this request's stored spec
	// doesn't have it, so the move must be rejected before the binding is
	// ever touched.
	s := serverWithFakeVersionClient(
		testDatabaseObject("team-payments", "my-db", map[string]interface{}{}),
		testBindingObject("team-payments", "my-db-binding", "database", "my-db", "v0.1.0"),
		revisionObject("database-v0.2.0", "database", "v0.2.0", true),
	)

	req := httptest.NewRequest(http.MethodPost, "/promises/database/requests/my-db/version", strings.NewReader(`{"version":"v0.2.0"}`))
	req.SetPathValue("reqName", "my-db")
	w := httptest.NewRecorder()

	s.doSetRequestVersion(w, req, testDatabaseEntry, "payments", "team-payments")

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

func TestDoSetRequestVersion_MissingVersionField(t *testing.T) {
	s := serverWithFakeVersionClient()

	req := httptest.NewRequest(http.MethodPost, "/promises/database/requests/my-db/version", strings.NewReader(`{}`))
	req.SetPathValue("reqName", "my-db")
	w := httptest.NewRecorder()

	s.doSetRequestVersion(w, req, testDatabaseEntry, "payments", "team-payments")

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd broker && go test ./internal/api/... -run 'TestDoGetRequestVersion|TestDoSetRequestVersion' -v`
Expected: FAIL to compile - `doGetRequestVersion`, `doSetRequestVersion`, and `requestVersionInfo` don't exist yet.

- [ ] **Step 3: Add imports to `server.go`**

Add to the existing `import (...)` block: `"strings"`, `"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"`, `"marketplace-broker/internal/bindingapi"`.

- [ ] **Step 4: Register the new routes in `Handler()`**

After `apiMux.HandleFunc("GET /promises/{name}", s.getPromise)`, add:

```go
	apiMux.HandleFunc("GET /promises/{name}/versions", s.listPromiseVersions)
```

After the flat `DELETE /promises/{name}/requests/{reqName}` line, add:

```go
	apiMux.HandleFunc("GET /promises/{name}/requests/{reqName}/version", s.getRequestVersion)
	apiMux.HandleFunc("POST /promises/{name}/requests/{reqName}/version", s.setRequestVersion)
```

After the scoped `DELETE .../requests/{reqName}` line, add:

```go
	apiMux.HandleFunc("GET /projects/{project}/environments/{environment}/promises/{name}/requests/{reqName}/version", s.getScopedRequestVersion)
	apiMux.HandleFunc("POST /projects/{project}/environments/{environment}/promises/{name}/requests/{reqName}/version", s.setScopedRequestVersion)
```

- [ ] **Step 5: Add the handlers**

Add this block after `doDeleteRequest` (before `updateRequest`) - order doesn't matter functionally, but this keeps read-ish handlers grouped:

```go
func (s *Server) listPromiseVersions(w http.ResponseWriter, r *http.Request) {
	entry, ok := s.lookupPromise(w, r)
	if !ok {
		return
	}
	revisions, err := catalog.ListRevisions(r.Context(), s.admin.Dynamic, entry.Name)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, revisions)
}

// requestVersionInfo is the shared response shape for doGetRequestVersion
// and doSetRequestVersion.
type requestVersionInfo struct {
	BoundVersion     string `json:"boundVersion"`
	LatestVersion    string `json:"latestVersion"`
	UpgradeAvailable bool   `json:"upgradeAvailable"`
}

func latestVersion(revisions []catalog.Revision) string {
	for _, rev := range revisions {
		if rev.Latest {
			return rev.Version
		}
	}
	return ""
}

func (s *Server) getRequestVersion(w http.ResponseWriter, r *http.Request) {
	entry, ok := s.lookupPromise(w, r)
	if !ok {
		return
	}
	team := teamFromContext(r.Context())
	s.doGetRequestVersion(w, r, *entry, team, tenant.Namespace(team))
}

func (s *Server) getScopedRequestVersion(w http.ResponseWriter, r *http.Request) {
	entry, ok := s.lookupPromise(w, r)
	if !ok {
		return
	}
	team := teamFromContext(r.Context())
	namespace := tenant.ProjectEnvironmentNamespace(team, r.PathValue("project"), r.PathValue("environment"))
	s.doGetRequestVersion(w, r, *entry, team, namespace)
}

// doGetRequestVersion reports which PromiseRevision reqName is currently
// bound to, and whether a newer one is available. A separate sub-resource
// from doGetRequest deliberately: that handler passes the raw Kubernetes
// object straight through (writeJSON(w, ..., obj.Object)) and three UI
// call sites already depend on that exact shape - see
// docs/superpowers/specs/2026-08-14-promise-version-upgrades-design.md,
// "New read endpoints."
func (s *Server) doGetRequestVersion(w http.ResponseWriter, r *http.Request, entry catalog.Entry, team, namespace string) {
	reqName := r.PathValue("reqName")

	client, err := s.admin.Groups.ForGroup(tenant.Group(team))
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	if _, ok, err := resourceapi.Get(r.Context(), client, entry, namespace, reqName); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	} else if !ok {
		writeError(w, http.StatusNotFound, "no such request: "+reqName)
		return
	}

	revisions, err := catalog.ListRevisions(r.Context(), s.admin.Dynamic, entry.Name)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	latest := latestVersion(revisions)

	binding, ok, err := bindingapi.Get(r.Context(), client, namespace, entry.Name, reqName)
	switch {
	case apierrors.IsForbidden(err):
		writeError(w, http.StatusForbidden, err.Error())
		return
	case err != nil:
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "no such request: "+reqName)
		return
	}

	bound := bindingapi.Version(binding, latest)
	writeJSON(w, http.StatusOK, requestVersionInfo{
		BoundVersion:     bound,
		LatestVersion:    latest,
		UpgradeAvailable: bound != latest,
	})
}

func (s *Server) setRequestVersion(w http.ResponseWriter, r *http.Request) {
	entry, ok := s.lookupPromise(w, r)
	if !ok {
		return
	}
	team := teamFromContext(r.Context())
	s.doSetRequestVersion(w, r, *entry, team, tenant.Namespace(team))
}

func (s *Server) setScopedRequestVersion(w http.ResponseWriter, r *http.Request) {
	entry, ok := s.lookupPromise(w, r)
	if !ok {
		return
	}
	team := teamFromContext(r.Context())
	namespace := tenant.ProjectEnvironmentNamespace(team, r.PathValue("project"), r.PathValue("environment"))
	s.doSetRequestVersion(w, r, *entry, team, namespace)
}

// doSetRequestVersion moves an existing request's ResourceBinding to a
// different Promise revision - forward (upgrade) or back (rollback), no
// directionality check. Validates the request's current spec against the
// target revision's schema first, so a bad move 400s immediately instead
// of the Resource Configure workflow failing asynchronously later.
func (s *Server) doSetRequestVersion(w http.ResponseWriter, r *http.Request, entry catalog.Entry, team, namespace string) {
	var body struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxRequestBody)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if body.Version == "" {
		writeError(w, http.StatusBadRequest, "\"version\" is required")
		return
	}

	reqName := r.PathValue("reqName")

	schemaObj, ok, err := catalog.RevisionSchema(r.Context(), s.admin.Dynamic, entry.Name, body.Version)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "no such promise version: "+body.Version)
		return
	}

	client, err := s.admin.Groups.ForGroup(tenant.Group(team))
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	existing, ok, err := resourceapi.Get(r.Context(), client, entry, namespace, reqName)
	switch {
	case apierrors.IsForbidden(err):
		writeError(w, http.StatusForbidden, err.Error())
		return
	case err != nil:
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "no such request: "+reqName)
		return
	}

	spec, _, _ := unstructured.NestedMap(existing.Object, "spec")
	if problems := catalog.ValidateAgainstSchema(schemaObj, spec); len(problems) > 0 {
		writeError(w, http.StatusBadRequest, "spec is not valid for version "+body.Version+": "+strings.Join(problems, "; "))
		return
	}

	binding, ok, err := bindingapi.SetVersion(r.Context(), client, namespace, entry.Name, reqName, body.Version)
	switch {
	case apierrors.IsForbidden(err):
		writeError(w, http.StatusForbidden, err.Error())
		return
	case apierrors.IsConflict(err):
		writeError(w, http.StatusConflict, "the binding was modified concurrently; reload and try again")
		return
	case err != nil:
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "no such request: "+reqName)
		return
	}

	revisions, err := catalog.ListRevisions(r.Context(), s.admin.Dynamic, entry.Name)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	latest := latestVersion(revisions)
	bound := bindingapi.Version(binding, latest)

	writeJSON(w, http.StatusOK, requestVersionInfo{
		BoundVersion:     bound,
		LatestVersion:    latest,
		UpgradeAvailable: bound != latest,
	})
}
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `cd broker && go test ./internal/api/... -v`
Expected: PASS - every existing `api` test plus the six new ones.

- [ ] **Step 7: Build the whole module to catch anything the targeted test run missed**

Run: `cd broker && go build ./... && go vet ./...`
Expected: no errors.

- [ ] **Step 8: Commit**

```bash
cd broker && git add internal/api/server.go internal/api/server_version_test.go
git commit -m "api: add GET /promises/{name}/versions and GET/POST .../requests/{reqName}/version"
```

---

## Task 6: RBAC - grant teams access to `ResourceBinding`

**Files:**
- Modify: `promises/business-unit/workflows/promise/configure/dependencies/configure-deps/resources/marketplace-rbac.yaml`
- Test: `broker/internal/tenant/rbac_integration_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks (independent of the Go code).
- Produces: teams can `get`/`list`/`watch`/`patch` `resourcebindings.platform.kratix.io` in their own namespace - a real-cluster precondition Task 5's handlers need to actually work end-to-end outside the fake-backed tests.

This is the RBAC gap identified in the spec: teams currently have zero access to the `platform.kratix.io` API group at all.

- [ ] **Step 1: Add the new rule to `marketplace-rbac.yaml`**

Add a second rule to the existing `ClusterRole`'s `rules`:

```yaml
rules:
  - apiGroups: ["demo.kratix.io"]
    resources: ["*"]
    verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
  # ResourceBinding is what a resource request's bound Promise version
  # actually lives on (platform.kratix.io, not demo.kratix.io like every
  # Promise's own CRD above) - teams need get/list to see their own
  # requests' current version and patch to move them. See
  # docs/superpowers/specs/2026-08-14-promise-version-upgrades-design.md,
  # "RBAC."
  - apiGroups: ["platform.kratix.io"]
    resources: ["resourcebindings"]
    verbs: ["get", "list", "watch", "patch"]
```

- [ ] **Step 2: Write the failing real-cluster integration test**

This test can only run against a live `kind-platform` cluster (same as `TestRBACBoundary` above it in the same file). Add to `broker/internal/tenant/rbac_integration_test.go`, right after `configMapsResource`'s declaration:

```go
var resourceBindingsResource = schema.GroupVersionResource{Group: "platform.kratix.io", Version: "v1alpha1", Resource: "resourcebindings"}
```

Add the test function and its helper, after `TestRBACBoundary`:

```go
// TestRBACBoundary_ResourceBindings is TestRBACBoundary's counterpart for
// platform.kratix.io/ResourceBinding - the object the promise
// version-upgrade feature reads/patches (see
// docs/superpowers/specs/2026-08-14-promise-version-upgrades-design.md).
// Confirms the RBAC change in marketplace-rbac.yaml actually grants teams
// get/list/patch on their own namespace's bindings, and nothing on
// another team's - same boundary, same mechanism, different API group
// than TestRBACBoundary already proves.
func TestRBACBoundary_ResourceBindings(t *testing.T) {
	kubeContext := os.Getenv("BROKER_KUBE_CONTEXT")
	if kubeContext == "" {
		kubeContext = "kind-platform"
	}
	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(),
		&clientcmd.ConfigOverrides{CurrentContext: kubeContext},
	).ClientConfig()
	if err != nil {
		t.Fatalf("loading kubeconfig (context %q): %v", kubeContext, err)
	}

	groups := k8sclient.NewGroupClients(config)

	const teamA, teamB = "payments", "checkout"
	nsA, nsB := Namespace(teamA), Namespace(teamB)

	clientA, err := groups.ForGroup(Group(teamA))
	if err != nil {
		t.Fatalf("ForGroup(%s): %v", teamA, err)
	}
	clientB, err := groups.ForGroup(Group(teamB))
	if err != nil {
		t.Fatalf("ForGroup(%s): %v", teamB, err)
	}

	ctx := context.Background()

	for _, tc := range []struct {
		team   string
		client dynamic.Interface
		ns     string
	}{
		{teamA, clientA, nsA},
		{teamB, clientB, nsB},
	} {
		var lastErr error
		err := wait.PollUntilContextTimeout(ctx, 500*time.Millisecond, 15*time.Second, true, func(ctx context.Context) (bool, error) {
			lastErr = createResourceBinding(ctx, tc.client, tc.ns, "rbac-boundary-test")
			return lastErr == nil, nil
		})
		if err != nil {
			t.Fatalf("%s: create ResourceBinding in own namespace %q never succeeded: %v", tc.team, tc.ns, lastErr)
		}
		if _, err := tc.client.Resource(resourceBindingsResource).Namespace(tc.ns).List(ctx, metav1.ListOptions{}); err != nil {
			t.Fatalf("%s: list ResourceBindings in own namespace %q: %v", tc.team, tc.ns, err)
		}
	}

	if _, err := clientA.Resource(resourceBindingsResource).Namespace(nsB).List(ctx, metav1.ListOptions{}); !apierrors.IsForbidden(err) {
		t.Errorf("team A listing team B's namespace %q: got err=%v, want Forbidden", nsB, err)
	}
	if err := createResourceBinding(ctx, clientA, nsB, "rbac-boundary-attack"); !apierrors.IsForbidden(err) {
		t.Errorf("team A creating in team B's namespace %q: got err=%v, want Forbidden", nsB, err)
	}
}

func createResourceBinding(ctx context.Context, client dynamic.Interface, namespace, name string) error {
	obj := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "platform.kratix.io/v1alpha1",
		"kind":       "ResourceBinding",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": namespace,
		},
		"spec": map[string]interface{}{
			"promiseRef": map[string]interface{}{"name": "database"},
			"resourceRef": map[string]interface{}{
				"name":      "rbac-boundary-test-resource",
				"namespace": namespace,
			},
			"version": "latest",
		},
	}}
	_, err := client.Resource(resourceBindingsResource).Namespace(namespace).Create(ctx, obj, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		return nil
	}
	return err
}
```

No new imports needed - `wait`, `time`, `apierrors`, `metav1`, `unstructured`, `dynamic`, `schema`, `clientcmd`, `k8sclient`, `context`, `os` are all already imported for `TestRBACBoundary`.

**Before running this**, verify the real `ResourceBinding` CRD's actual `spec` field names against the live cluster - the exact shape wasn't confirmed against a real object during design (see the spec's "Open risk"):

```bash
kubectl --context kind-platform explain resourcebinding.spec
```

If `promiseRef`/`resourceRef` don't match what's actually there, adjust `createResourceBinding`'s `spec` map to match before proceeding.

- [ ] **Step 3: Rebuild and reload the `business-unit` Promise's dependency image, then reapply it**

The `resources/*.yaml` files are baked into the `configure-deps` pipeline image (`promises/business-unit/workflows/promise/configure/dependencies/configure-deps/scripts/pipeline.sh` just copies `/resources/*` into Kratix's output) - editing the YAML alone doesn't change anything on the cluster until that image is rebuilt, reloaded, and the Promise's configure workflow reruns:

```bash
make promise-build promise-load PROMISE_DIR=promises/business-unit
kubectl --context kind-platform apply -f promises/business-unit/promise.yaml
```

- [ ] **Step 4: Confirm the ClusterRole picked up the new rule**

```bash
kubectl --context kind-platform get clusterrole marketplace-tenant-resources -o yaml
```

Expected: the output's `rules` list includes both the `demo.kratix.io` rule and the new `platform.kratix.io`/`resourcebindings` rule.

- [ ] **Step 5: Run the integration test**

```bash
cd broker && go test -tags=integration ./internal/tenant/... -run TestRBACBoundary_ResourceBindings -v
```

Expected: PASS. If it fails with a schema-validation error on `createResourceBinding`'s object, the `spec` shape from Step 2's `kubectl explain` differs from what's assumed here - fix the fixture in `createResourceBinding`, not the RBAC rule.

- [ ] **Step 6: Commit**

```bash
git add promises/business-unit/workflows/promise/configure/dependencies/configure-deps/resources/marketplace-rbac.yaml broker/internal/tenant/rbac_integration_test.go
git commit -m "rbac: grant teams get/list/watch/patch on their own namespace's ResourceBindings"
```

---

## Task 7: Seed `PromiseRevision`/`ResourceBinding` fixtures for `BROKER_FAKE_K8S` mode

**Files:**
- Modify: `broker/cmd/broker/fake_seed.go`

**Interfaces:**
- Consumes: `catalog.PromiseRevisionGVR`, `catalog.LabelPromiseName`, `catalog.LabelLatestRevision`, `catalog.LabelPromiseVersion` (Tasks 1-2); `bindingapi.GVR` (Task 4).
- Produces: an `example-database` request, pinned to `v0.1.0`, while the `database` Promise itself is now at `v0.2.0` - a concrete upgrade-available scenario the `BROKER_FAKE_K8S=1` HTTP tier (and later, a UI dev server pointed at it) can exercise without a cluster.

**A real schema difference between the two revisions, not just two labels wrapping identical schemas.** `v0.1.0`'s schema is exactly the real `database` Promise's current schema (`promises/database/promise.yaml`: `size` only, required, enum-constrained) - so the "old" revision fixture matches production, not an invented shape. `v0.2.0` adds one new field, `highAvailability` (optional boolean, defaulting to unset/false) - a Promise author shipping HA support as a new capability without breaking anyone still on `v0.1.0`. This makes the fixture's upgrade scenario mean something concretely testable: `example-database`'s stored spec (`{"size": "1Gi"}`) is valid under both schemas (the field is optional, not required), so `ValidateAgainstSchema` allows the move and a client can then set `highAvailability: true` on a follow-up edit once upgraded - a realistic "upgrade first, opt into the new capability second" flow, and the natural next thing to try manually against `BROKER_FAKE_K8S=1` after Step 3's smoke test.

- [ ] **Step 1: Confirm the current fake-backed HTTP tests pass before changing the fixtures**

```bash
cd broker && BROKER_FAKE_K8S=1 go run ./cmd/broker &
sleep 1
curl -s -H "Authorization: Bearer demo-key-payments" localhost:8878/api/promises
kill %1
```

Expected: a JSON array containing the `database` Promise - baseline before the fixture change.

- [ ] **Step 2: Rewrite `fake_seed.go`**

```go
package main

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"marketplace-broker/internal/bindingapi"
	"marketplace-broker/internal/catalog"
)

// fakeGVRToListKind and fakeSeedObjects describe the in-memory catalog
// BROKER_FAKE_K8S mode serves: a single Database Promise, now at
// kratix.io/promise-version v0.2.0 with both its v0.1.0 and v0.2.0
// revisions on record, plus one example request still pinned to the
// older v0.1.0 revision via its ResourceBinding - enough for a client to
// exercise a full submit/get/edit/delete/version-move request lifecycle,
// including an upgrade-available scenario, without a cluster.
var fakeGVRToListKind = map[schema.GroupVersionResource]string{
	catalog.PromiseGVR:         "PromiseList",
	catalog.PromiseRevisionGVR: "PromiseRevisionList",
	bindingapi.GVR:             "ResourceBindingList",
	{Group: "demo.kratix.io", Version: "v1alpha1", Resource: "databases"}: "DatabaseList",
}

func fakeSeedObjects() []runtime.Object {
	return []runtime.Object{
		fakeDatabasePromise(),
		fakeDatabaseRevision("v0.1.0", false),
		fakeDatabaseRevision("v0.2.0", true),
		fakeExampleDatabaseRequest(),
		fakeExampleDatabaseBinding(),
	}
}

func fakeDatabasePromise() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "platform.kratix.io/v1alpha1",
		"kind":       "Promise",
		"metadata": map[string]interface{}{
			"name": "database",
			"labels": map[string]interface{}{
				catalog.LabelVisible:        "true",
				catalog.LabelPromiseVersion: "v0.2.0",
			},
			"annotations": map[string]interface{}{
				catalog.AnnotationDisplayName: "Postgres Database",
				catalog.AnnotationDescription: "A sized, managed Postgres database, provisioned on request.",
			},
		},
		"spec": map[string]interface{}{
			"api": fakeDatabaseCRDv2(),
		},
	}}
}

// fakeDatabaseCRDv1 is the CustomResourceDefinition manifest for the
// database Promise's v0.1.0 revision - deliberately identical to the real
// Promise's current schema (promises/database/promise.yaml: size only,
// required, enum-constrained), so the "old" revision fixture matches
// production rather than an invented shape.
func fakeDatabaseCRDv1() map[string]interface{} {
	return map[string]interface{}{
		"apiVersion": "apiextensions.k8s.io/v1",
		"kind":       "CustomResourceDefinition",
		"spec": map[string]interface{}{
			"group": "demo.kratix.io",
			"names": map[string]interface{}{
				"kind":   "Database",
				"plural": "databases",
			},
			"scope": "Namespaced",
			"versions": []interface{}{
				map[string]interface{}{
					"name":    "v1alpha1",
					"served":  true,
					"storage": true,
					"schema": map[string]interface{}{
						"openAPIV3Schema": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"spec": map[string]interface{}{
									"type":     "object",
									"required": []interface{}{"size"},
									"properties": map[string]interface{}{
										"size": map[string]interface{}{
											"type": "string",
											"enum": []interface{}{"1Gi", "5Gi", "10Gi", "50Gi"},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

// fakeDatabaseCRDv2 is the database Promise's v0.2.0 revision: the same
// shape as v1, plus one new optional field, highAvailability - a Promise
// author shipping HA support as a new capability without breaking any
// request still on v0.1.0 (the field is optional, not required, so a v0.1.0
// spec with no highAvailability key remains valid under this schema too).
// This is also the live Promise's current schema (fakeDatabasePromise's
// spec.api, above) - v0.2.0 is "latest".
func fakeDatabaseCRDv2() map[string]interface{} {
	return map[string]interface{}{
		"apiVersion": "apiextensions.k8s.io/v1",
		"kind":       "CustomResourceDefinition",
		"spec": map[string]interface{}{
			"group": "demo.kratix.io",
			"names": map[string]interface{}{
				"kind":   "Database",
				"plural": "databases",
			},
			"scope": "Namespaced",
			"versions": []interface{}{
				map[string]interface{}{
					"name":    "v1alpha1",
					"served":  true,
					"storage": true,
					"schema": map[string]interface{}{
						"openAPIV3Schema": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"spec": map[string]interface{}{
									"type":     "object",
									"required": []interface{}{"size"},
									"properties": map[string]interface{}{
										"size": map[string]interface{}{
											"type": "string",
											"enum": []interface{}{"1Gi", "5Gi", "10Gi", "50Gi"},
										},
										"highAvailability": map[string]interface{}{"type": "boolean"},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

// fakeDatabaseRevisionSchemas maps each simulated revision to its schema
// builder, so fakeDatabaseRevision can look up the right one per version -
// v0.1.0 and v0.2.0 genuinely differ (see fakeDatabaseCRDv1/v2 above),
// unlike a single shared schema wrapped in two differently-labeled
// revisions.
var fakeDatabaseRevisionSchemas = map[string]func() map[string]interface{}{
	"v0.1.0": fakeDatabaseCRDv1,
	"v0.2.0": fakeDatabaseCRDv2,
}

func fakeDatabaseRevision(version string, latest bool) *unstructured.Unstructured {
	labels := map[string]interface{}{catalog.LabelPromiseName: "database"}
	if latest {
		labels[catalog.LabelLatestRevision] = "true"
	}
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "platform.kratix.io/v1alpha1",
		"kind":       "PromiseRevision",
		"metadata": map[string]interface{}{
			"name":   "database-" + version,
			"labels": labels,
		},
		"spec": map[string]interface{}{
			"version": version,
			"promiseSpec": map[string]interface{}{
				"api": fakeDatabaseRevisionSchemas[version](),
			},
		},
	}}
}

func fakeExampleDatabaseRequest() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "demo.kratix.io/v1alpha1",
		"kind":       "Database",
		"metadata": map[string]interface{}{
			"name":      "example-database",
			"namespace": "team-payments",
		},
		"spec": map[string]interface{}{
			"size": "1Gi",
		},
	}}
}

func fakeExampleDatabaseBinding() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "platform.kratix.io/v1alpha1",
		"kind":       "ResourceBinding",
		"metadata": map[string]interface{}{
			"name":      "example-database-binding",
			"namespace": "team-payments",
			"labels": map[string]interface{}{
				"kratix.io/promise-name":  "database",
				"kratix.io/resource-name": "example-database",
			},
		},
		"spec": map[string]interface{}{
			"version": "v0.1.0",
		},
	}}
}
```

- [ ] **Step 3: Build and smoke-test**

```bash
cd broker && go build ./... && BROKER_FAKE_K8S=1 go run ./cmd/broker &
sleep 1
curl -s -H "Authorization: Bearer demo-key-payments" localhost:8878/api/promises/database/versions
curl -s -H "Authorization: Bearer demo-key-payments" localhost:8878/api/promises/database/requests/example-database/version
```

Expected: the first `curl` returns two revisions (`v0.1.0`, `v0.2.0`, the latter `"latest": true`); the second returns `{"boundVersion":"v0.1.0","latestVersion":"v0.2.0","upgradeAvailable":true}`.

Then exercise the actual upgrade-and-opt-in-to-HA flow this fixture was built for:

```bash
curl -X POST -H "Authorization: Bearer demo-key-payments" \
  -d '{"version":"v0.2.0"}' \
  localhost:8878/api/promises/database/requests/example-database/version

curl -X PUT -H "Authorization: Bearer demo-key-payments" \
  -d '{"spec":{"size":"1Gi","highAvailability":true}}' \
  localhost:8878/api/promises/database/requests/example-database

kill %1
```

Expected: the `POST .../version` call succeeds (`example-database`'s stored spec, `{"size":"1Gi"}`, validates fine against v0.2.0's schema since `highAvailability` is optional) and returns `{"boundVersion":"v0.2.0","latestVersion":"v0.2.0","upgradeAvailable":false}`; the follow-up `PUT` (the existing edit endpoint) then turns HA on now that the request is running against a revision whose schema supports it.

- [ ] **Step 4: Run the full Go test suite once more**

```bash
cd broker && go build ./... && go vet ./... && go test ./...
```

Expected: PASS across every package (this also re-confirms Task 5's fake-backed handler tests, and Task 1-4's unit tests, still hold with the richer seed data - they don't depend on `fake_seed.go`, which is `cmd/broker`-only, but this is the point where a regression in shared behavior would surface).

- [ ] **Step 5: Run the UI's fake-backed integration test**

```bash
cd ui && npm test -- api.integration.test.ts
```

Expected: PASS - `api.integration.test.ts` (see `ui/src/lib/api.integration.test.ts`) only asserts the `database` Promise is present and exercises submit/edit/404/401 with unique names, so the added `example-database` seed object doesn't conflict with any existing assertion.

- [ ] **Step 6: Commit**

```bash
cd broker && git add cmd/broker/fake_seed.go
git commit -m "fake_seed: seed PromiseRevision/ResourceBinding fixtures with an upgrade-available example"
```

---

## Task 8: Document the new endpoints in the README

**Files:**
- Modify: `README.md`

**Interfaces:**
- Consumes: nothing (documentation only).

- [ ] **Step 1: Add rows to the endpoints table**

In the `**Endpoints**` table (`README.md`, "Marketplace broker API" section), add these rows right after the `GET /api/promises/{name}` row:

```markdown
| GET | `/api/promises/{name}/versions` | List every known Promise revision for this Promise: `[{"version", "latest", "createdAt"}, ...]` |
```

And right after the `GET /api/promises/{name}/requests/{reqName}` row:

```markdown
| GET | `/api/promises/{name}/requests/{reqName}/version` | The request's current bound version: `{"boundVersion", "latestVersion", "upgradeAvailable"}` |
| POST | `/api/promises/{name}/requests/{reqName}/version` | Move the request to a different Promise revision (upgrade or rollback): `{"version": "..."}` |
```

- [ ] **Step 2: Add a curl example**

After the existing curl block in that section, add:

```bash
curl -H "Authorization: Bearer demo-key-payments" localhost:8878/api/promises/database/versions

curl -X POST -H "Authorization: Bearer demo-key-payments" \
  -d '{"version":"v0.2.0"}' \
  localhost:8878/api/promises/database/requests/my-db/version
```

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "docs: document the promise version endpoints in the broker API README section"
```
