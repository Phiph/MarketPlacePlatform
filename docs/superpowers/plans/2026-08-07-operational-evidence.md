# Operational Evidence Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make owner, lifecycle stage, support contact, and data/compliance policy classification queryable from the broker API for every installed Promise, closing the "operational evidence" gap flagged in CNCF-capabilities review feedback.

**Architecture:** Extend the existing `marketplace.kratix.io/` annotation convention (currently `visible`/`display-name`/`description`) with four new annotations — `owner`, `lifecycle`, `support`, `policy` — parsed by the broker's existing `catalog.parseEntry` into new `Entry` fields plus a computed `MissingEvidence []string`. Already-existing endpoints (`GET /api/promises?all=true`, `GET /api/promises/{name}`) expose it — no new endpoint. A Go test globbing the real `promises/*/promise.yaml` files enforces every installed Promise carries all four, independent of catalog visibility.

**Tech Stack:** Go 1.26 (broker module), `k8s.io/apimachinery` `unstructured.Unstructured`, `sigs.k8s.io/yaml` (already a direct dependency — no `go.mod` changes needed).

## Global Constraints

- New annotation keys, all under the existing `marketplace.kratix.io/` prefix: `owner`, `lifecycle`, `support`, `policy`.
- `lifecycle` must be one of `experimental` | `stable` | `deprecated`. `policy` must be one of `internal` | `confidential` | `regulated`. `owner`/`support` are free text.
- Required on **every installed Promise**, regardless of `marketplace.kratix.io/visible` — do not gate on visibility.
- No new broker endpoint, no new auth scoping, no UI changes (`ui/src` is out of scope for this plan).
- Design doc: `docs/superpowers/specs/2026-08-07-operational-evidence-design.md` — refer back to it if a step here seems to need a decision not covered.

---

### Task 1: Add operational evidence fields to `catalog.Entry`

**Files:**
- Modify: `broker/internal/catalog/catalog.go:36-40` (constants), `:44-56` (`Entry` struct), `:121-159` (`parseEntry`)
- Test: `broker/internal/catalog/catalog_test.go`

**Interfaces:**
- Produces: `catalog.AnnotationOwner`, `catalog.AnnotationLifecycle`, `catalog.AnnotationSupport`, `catalog.AnnotationPolicy` (string constants); `Entry.Owner`, `Entry.Lifecycle`, `Entry.Support`, `Entry.Policy` (string fields); `Entry.MissingEvidence []string` (nil/empty when complete, otherwise a subset of `{"owner", "lifecycle", "support", "policy"}` in that fixed order). Later tasks (2, and the running broker) rely on `parseEntry` populating these on every call, and on `MissingEvidence` being non-nil-but-empty (or nil) exactly when nothing is missing.

- [ ] **Step 1: Write the failing tests**

Add to `broker/internal/catalog/catalog_test.go`. First, add `"reflect"` to the existing `import` block (alongside `"testing"`):

```go
import (
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)
```

Then append these two test functions at the end of the file:

```go
func TestParseEntryOperationalEvidenceFields(t *testing.T) {
	obj := databasePromise()
	annotations := obj.Object["metadata"].(map[string]interface{})["annotations"].(map[string]interface{})
	annotations[AnnotationOwner] = "platform-team"
	annotations[AnnotationLifecycle] = "stable"
	annotations[AnnotationSupport] = "#platform-eng"
	annotations[AnnotationPolicy] = "confidential"

	entry, ok := parseEntry(obj)
	if !ok {
		t.Fatalf("expected the database Promise fixture to parse")
	}
	if entry.Owner != "platform-team" {
		t.Errorf("Owner = %q, want %q", entry.Owner, "platform-team")
	}
	if entry.Lifecycle != "stable" {
		t.Errorf("Lifecycle = %q, want %q", entry.Lifecycle, "stable")
	}
	if entry.Support != "#platform-eng" {
		t.Errorf("Support = %q, want %q", entry.Support, "#platform-eng")
	}
	if entry.Policy != "confidential" {
		t.Errorf("Policy = %q, want %q", entry.Policy, "confidential")
	}
	if len(entry.MissingEvidence) != 0 {
		t.Errorf("MissingEvidence = %v, want empty", entry.MissingEvidence)
	}
}

func TestParseEntryOperationalEvidenceMissing(t *testing.T) {
	tests := []struct {
		name        string
		mutate      func(annotations map[string]interface{})
		wantMissing []string
	}{
		{
			name:        "all evidence annotations absent",
			mutate:      func(annotations map[string]interface{}) {},
			wantMissing: []string{"owner", "lifecycle", "support", "policy"},
		},
		{
			name: "owner present, rest absent",
			mutate: func(annotations map[string]interface{}) {
				annotations[AnnotationOwner] = "platform-team"
			},
			wantMissing: []string{"lifecycle", "support", "policy"},
		},
		{
			name: "invalid lifecycle and policy values",
			mutate: func(annotations map[string]interface{}) {
				annotations[AnnotationOwner] = "platform-team"
				annotations[AnnotationLifecycle] = "made-up-stage"
				annotations[AnnotationSupport] = "#platform-eng"
				annotations[AnnotationPolicy] = "made-up-class"
			},
			wantMissing: []string{"lifecycle", "policy"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj := databasePromise()
			annotations := obj.Object["metadata"].(map[string]interface{})["annotations"].(map[string]interface{})
			tt.mutate(annotations)

			entry, ok := parseEntry(obj)
			if !ok {
				t.Fatalf("expected the fixture to parse")
			}
			if !reflect.DeepEqual(entry.MissingEvidence, tt.wantMissing) {
				t.Errorf("MissingEvidence = %v, want %v", entry.MissingEvidence, tt.wantMissing)
			}
		})
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd broker && go test ./internal/catalog/... -run TestParseEntryOperationalEvidence -v`
Expected: compile error — `AnnotationOwner`, `AnnotationLifecycle`, `AnnotationSupport`, `AnnotationPolicy`, `entry.Owner`, `entry.Lifecycle`, `entry.Support`, `entry.Policy`, and `entry.MissingEvidence` don't exist yet.

- [ ] **Step 3: Implement the constants**

In `broker/internal/catalog/catalog.go`, replace the existing const block (lines 36-40):

```go
const (
	LabelVisible          = "marketplace.kratix.io/visible"
	AnnotationDisplayName = "marketplace.kratix.io/display-name"
	AnnotationDescription = "marketplace.kratix.io/description"
	AnnotationOwner       = "marketplace.kratix.io/owner"
	AnnotationLifecycle   = "marketplace.kratix.io/lifecycle"
	AnnotationSupport     = "marketplace.kratix.io/support"
	AnnotationPolicy      = "marketplace.kratix.io/policy"
)

// validLifecycles and validPolicies are the fixed, queryable value sets for
// the lifecycle/policy evidence annotations - an unrecognised value counts
// the same as an absent one (see parseEntry).
var validLifecycles = map[string]bool{"experimental": true, "stable": true, "deprecated": true}
var validPolicies = map[string]bool{"internal": true, "confidential": true, "regulated": true}
```

- [ ] **Step 4: Add fields to the `Entry` struct**

Replace the `Entry` struct (lines 44-56):

```go
// Entry is one Promise as seen by the marketplace: enough to render it in a
// catalog and build a request against it.
type Entry struct {
	Name        string                 `json:"name"`
	DisplayName string                 `json:"displayName"`
	Description string                 `json:"description,omitempty"`
	Visible     bool                   `json:"visible"`
	Group       string                 `json:"group"`
	Version     string                 `json:"version"`
	Kind        string                 `json:"kind"`
	Plural      string                 `json:"plural"`
	Scope       string                 `json:"scope"`
	Schema      map[string]interface{} `json:"schema,omitempty"`
	Status      map[string]interface{} `json:"status,omitempty"`

	// Operational evidence: see README.md, "Marketplace metadata
	// convention". Owner/Lifecycle/Support/Policy are read straight off
	// the Promise's annotations with no fallback default - an empty
	// string means the annotation is absent. MissingEvidence is the
	// queryable answer to "does this Promise have complete operational
	// evidence": nil/empty when it does, otherwise the subset of
	// {"owner", "lifecycle", "support", "policy"} that's absent or (for
	// Lifecycle/Policy) not one of the fixed allowed values.
	Owner           string   `json:"owner,omitempty"`
	Lifecycle       string   `json:"lifecycle,omitempty"`
	Support         string   `json:"support,omitempty"`
	Policy          string   `json:"policy,omitempty"`
	MissingEvidence []string `json:"missingEvidence,omitempty"`
}
```

- [ ] **Step 5: Populate the fields in `parseEntry`**

In `parseEntry`, find this block:

```go
	displayName := obj.GetAnnotations()[AnnotationDisplayName]
	if displayName == "" {
		displayName = name
	}

	return Entry{
		Name:        name,
		DisplayName: displayName,
		Description: obj.GetAnnotations()[AnnotationDescription],
		Visible:     obj.GetLabels()[LabelVisible] == "true",
		Group:       group,
		Version:     version,
		Kind:        kind,
		Plural:      plural,
		Scope:       scope,
		Schema:      schemaObj,
		Status:      status,
	}, true
}
```

Replace it with:

```go
	displayName := obj.GetAnnotations()[AnnotationDisplayName]
	if displayName == "" {
		displayName = name
	}

	owner := obj.GetAnnotations()[AnnotationOwner]
	lifecycle := obj.GetAnnotations()[AnnotationLifecycle]
	support := obj.GetAnnotations()[AnnotationSupport]
	policy := obj.GetAnnotations()[AnnotationPolicy]

	var missing []string
	if owner == "" {
		missing = append(missing, "owner")
	}
	if !validLifecycles[lifecycle] {
		missing = append(missing, "lifecycle")
	}
	if support == "" {
		missing = append(missing, "support")
	}
	if !validPolicies[policy] {
		missing = append(missing, "policy")
	}

	return Entry{
		Name:            name,
		DisplayName:     displayName,
		Description:     obj.GetAnnotations()[AnnotationDescription],
		Visible:         obj.GetLabels()[LabelVisible] == "true",
		Group:           group,
		Version:         version,
		Kind:            kind,
		Plural:          plural,
		Scope:           scope,
		Schema:          schemaObj,
		Status:          status,
		Owner:           owner,
		Lifecycle:       lifecycle,
		Support:         support,
		Policy:          policy,
		MissingEvidence: missing,
	}, true
}
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `cd broker && go test ./internal/catalog/... -v`
Expected: PASS for all tests, including the two new ones and every pre-existing test in the package (`TestParseEntry`, `TestParseEntryDefaultsHiddenAndDisplayName`, `TestParseEntryMissingAPIIsSkipped`, `TestPickVersionPrefersStorage`, `TestPickVersionFallsBackToServed`).

- [ ] **Step 7: Commit**

```bash
git add broker/internal/catalog/catalog.go broker/internal/catalog/catalog_test.go
git commit -m "Add operational evidence fields to catalog.Entry

owner/lifecycle/support/policy, parsed from new marketplace.kratix.io
annotations, plus a computed MissingEvidence for querying gaps."
```

---

### Task 2: Enforce operational evidence on every checked-in Promise

**Files:**
- Create: `broker/internal/catalog/evidence_lint_test.go`
- Modify: `promises/business-unit/promise.yaml`, `promises/team/promise.yaml`, `promises/project/promise.yaml`, `promises/environment/promise.yaml`, `promises/database/promise.yaml`

**Interfaces:**
- Consumes: `parseEntry` and `Entry.MissingEvidence` from Task 1.
- Produces: nothing new consumed by later tasks — this task's deliverable is the enforcement test plus five now-compliant `promise.yaml` files.

- [ ] **Step 1: Write the lint test**

Create `broker/internal/catalog/evidence_lint_test.go`:

```go
package catalog

import (
	"os"
	"path/filepath"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

// TestPromiseYAMLFilesHaveOperationalEvidence guards against a checked-in
// Promise regressing on the marketplace.kratix.io/{owner,lifecycle,support,
// policy} convention (see the root README.md, "Marketplace metadata
// convention"). Every installed Promise needs this metadata queryable from
// the running platform, independent of marketplace.kratix.io/visible - see
// docs/superpowers/specs/2026-08-07-operational-evidence-design.md for why.
//
// Unlike catalog_test.go's synthetic fixtures, this reads the real checked-in
// files, three directories up from this package to the repo root.
func TestPromiseYAMLFilesHaveOperationalEvidence(t *testing.T) {
	matches, err := filepath.Glob("../../../promises/*/promise.yaml")
	if err != nil {
		t.Fatalf("globbing promise.yaml files: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("no promises/*/promise.yaml files found - is the glob path wrong?")
	}

	for _, path := range matches {
		path := path
		t.Run(path, func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading %s: %v", path, err)
			}

			var obj unstructured.Unstructured
			if err := yaml.Unmarshal(raw, &obj.Object); err != nil {
				t.Fatalf("parsing %s: %v", path, err)
			}

			entry, ok := parseEntry(&obj)
			if !ok {
				t.Fatalf("%s doesn't parse as a catalog entry (missing spec.api?)", path)
			}
			if len(entry.MissingEvidence) > 0 {
				t.Errorf("%s (Promise %q) is missing operational evidence: %v - add the corresponding marketplace.kratix.io/* annotation(s)",
					path, entry.Name, entry.MissingEvidence)
			}
		})
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd broker && go test ./internal/catalog/... -run TestPromiseYAMLFilesHaveOperationalEvidence -v`
Expected: FAIL, with one sub-test failure per `promises/*/promise.yaml`, each reporting `MissingEvidence: [owner lifecycle support policy]` (none of the five files have any of the four new annotations yet).

- [ ] **Step 3: Add evidence annotations to `promises/business-unit/promise.yaml`**

Find:

```yaml
  annotations:
    marketplace.kratix.io/display-name: Business Unit
    marketplace.kratix.io/description: >-
      Provisions a business unit's Capsule Tenant, with its own resource
      quotas. Teams (see the `team` Promise) get their own namespace inside
      it. Operator/day-2 only - not self-served through the catalog.
  name: business-unit
```

Replace with:

```yaml
  annotations:
    marketplace.kratix.io/display-name: Business Unit
    marketplace.kratix.io/description: >-
      Provisions a business unit's Capsule Tenant, with its own resource
      quotas. Teams (see the `team` Promise) get their own namespace inside
      it. Operator/day-2 only - not self-served through the catalog.
    marketplace.kratix.io/owner: platform-team
    marketplace.kratix.io/lifecycle: stable
    marketplace.kratix.io/support: "#platform-eng"
    marketplace.kratix.io/policy: internal
  name: business-unit
```

- [ ] **Step 4: Add evidence annotations to `promises/team/promise.yaml`**

Find:

```yaml
  annotations:
    marketplace.kratix.io/display-name: Team
    marketplace.kratix.io/description: >-
      Provisions a team's namespace inside its business unit's Capsule
      Tenant. Operator/day-2 only - not self-served through the catalog.
  name: team
```

Replace with:

```yaml
  annotations:
    marketplace.kratix.io/display-name: Team
    marketplace.kratix.io/description: >-
      Provisions a team's namespace inside its business unit's Capsule
      Tenant. Operator/day-2 only - not self-served through the catalog.
    marketplace.kratix.io/owner: platform-team
    marketplace.kratix.io/lifecycle: stable
    marketplace.kratix.io/support: "#platform-eng"
    marketplace.kratix.io/policy: internal
  name: team
```

- [ ] **Step 5: Add evidence annotations to `promises/project/promise.yaml`**

Find:

```yaml
  annotations:
    marketplace.kratix.io/display-name: Project
    marketplace.kratix.io/description: >-
      A logical grouping of environments (see the `environment` Promise) owned
      by one team. Self-served through the broker's dedicated Projects UI/API,
      not the generic ad-hoc catalog.
  name: project
```

Replace with:

```yaml
  annotations:
    marketplace.kratix.io/display-name: Project
    marketplace.kratix.io/description: >-
      A logical grouping of environments (see the `environment` Promise) owned
      by one team. Self-served through the broker's dedicated Projects UI/API,
      not the generic ad-hoc catalog.
    marketplace.kratix.io/owner: platform-team
    marketplace.kratix.io/lifecycle: experimental
    marketplace.kratix.io/support: "#platform-eng"
    marketplace.kratix.io/policy: internal
  name: project
```

- [ ] **Step 6: Add evidence annotations to `promises/environment/promise.yaml`**

Find:

```yaml
  annotations:
    marketplace.kratix.io/display-name: Environment
    marketplace.kratix.io/description: >-
      Provisions one environment (dev/staging/prod/...) of a Project as its
      own namespace. Self-served through the broker's dedicated
      `POST /api/environments` endpoint, not the generic ad-hoc catalog or a
      raw `kubectl apply` - see README.md for why.
  name: environment
```

Replace with:

```yaml
  annotations:
    marketplace.kratix.io/display-name: Environment
    marketplace.kratix.io/description: >-
      Provisions one environment (dev/staging/prod/...) of a Project as its
      own namespace. Self-served through the broker's dedicated
      `POST /api/environments` endpoint, not the generic ad-hoc catalog or a
      raw `kubectl apply` - see README.md for why.
    marketplace.kratix.io/owner: platform-team
    marketplace.kratix.io/lifecycle: experimental
    marketplace.kratix.io/support: "#platform-eng"
    marketplace.kratix.io/policy: internal
  name: environment
```

- [ ] **Step 7: Add evidence annotations to `promises/database/promise.yaml`**

Find:

```yaml
  annotations:
    marketplace.kratix.io/display-name: Postgres Database
    marketplace.kratix.io/description: A sized, managed Postgres database, provisioned on request.
  name: database
```

Replace with:

```yaml
  annotations:
    marketplace.kratix.io/display-name: Postgres Database
    marketplace.kratix.io/description: A sized, managed Postgres database, provisioned on request.
    marketplace.kratix.io/owner: platform-team
    marketplace.kratix.io/lifecycle: stable
    marketplace.kratix.io/support: "#platform-eng"
    marketplace.kratix.io/policy: confidential
  name: database
```

(`database` gets `policy: confidential`, not `internal` like the others — it's the one Promise here that actually provisions a place application data lands, unlike the tenancy/grouping Promises.)

- [ ] **Step 8: Run the test to verify it passes**

Run: `cd broker && go test ./internal/catalog/... -v`
Expected: PASS for every test, including all five `TestPromiseYAMLFilesHaveOperationalEvidence` sub-tests.

- [ ] **Step 9: Commit**

```bash
git add broker/internal/catalog/evidence_lint_test.go promises/business-unit/promise.yaml promises/team/promise.yaml promises/project/promise.yaml promises/environment/promise.yaml promises/database/promise.yaml
git commit -m "Enforce operational evidence annotations on every Promise

A Go test globs the real promises/*/promise.yaml files and fails if any
is missing owner/lifecycle/support/policy, regardless of catalog
visibility - the three flagged Promises (business-unit, project, team)
are intentionally visible:false, so gating on visibility would have
exempted exactly the Promises this was meant to cover."
```

---

### Task 3: Document the convention in the root README

**Files:**
- Modify: `README.md:228-247`

**Interfaces:**
- Consumes: the four annotation keys and enum values from Tasks 1-2 (no code interface — this is documentation).

- [ ] **Step 1: Update the "Marketplace metadata convention" section**

Find (README.md, starting at line 228):

```markdown
### Marketplace metadata convention

A Promise doesn't show up in the catalog just by being installed - the
Promise author opts it in (and describes it) with labels/annotations on the
`Promise` object itself, all under the `marketplace.kratix.io/` prefix:

| Key | Kind | Purpose |
|---|---|---|
| `marketplace.kratix.io/visible` | label | `"true"` to list it in `GET /api/promises`. **Default is hidden** if absent - installing a Promise never silently publishes it. |
| `marketplace.kratix.io/display-name` | annotation | Human-readable name shown in the catalog. Falls back to the Promise's `metadata.name` if absent. |
| `marketplace.kratix.io/description` | annotation | Free-text description. Omitted from the entry if absent. |

Visibility is a **label**, not an annotation, specifically so the broker can
filter with a Kubernetes `LabelSelector` at list time rather than fetching
every Promise and filtering after the fact. Display name and description are
**annotations** because label values are capped at 63 characters of a narrow
charset - too restrictive for a real name or sentence.
[`promises/database/promise.yaml`](promises/database/promise.yaml) carries
all three as the reference example.
```

Replace with:

```markdown
### Marketplace metadata convention

A Promise doesn't show up in the catalog just by being installed - the
Promise author opts it in (and describes it) with labels/annotations on the
`Promise` object itself, all under the `marketplace.kratix.io/` prefix:

| Key | Kind | Purpose |
|---|---|---|
| `marketplace.kratix.io/visible` | label | `"true"` to list it in `GET /api/promises`. **Default is hidden** if absent - installing a Promise never silently publishes it. |
| `marketplace.kratix.io/display-name` | annotation | Human-readable name shown in the catalog. Falls back to the Promise's `metadata.name` if absent. |
| `marketplace.kratix.io/description` | annotation | Free-text description. Omitted from the entry if absent. |
| `marketplace.kratix.io/owner` | annotation | Team accountable for *this Promise's* pipeline/maintenance - e.g. `platform-team`. Distinct from `marketplace.kratix.io/team` (set by the `team` Promise on the *resources* it creates), which tags per-request consumer ownership, not Promise-authoring ownership. |
| `marketplace.kratix.io/lifecycle` | annotation | Maturity stage: `experimental`, `stable`, or `deprecated`. |
| `marketplace.kratix.io/support` | annotation | Where to get help - a Slack channel, email, or on-call link. |
| `marketplace.kratix.io/policy` | annotation | Data/compliance classification: `internal`, `confidential`, or `regulated`. |

Visibility is a **label**, not an annotation, specifically so the broker can
filter with a Kubernetes `LabelSelector` at list time rather than fetching
every Promise and filtering after the fact. Display name and description are
**annotations** because label values are capped at 63 characters of a narrow
charset - too restrictive for a real name or sentence.
[`promises/database/promise.yaml`](promises/database/promise.yaml) carries
all seven as the reference example.

**Operational evidence.** The last four keys - `owner`, `lifecycle`,
`support`, `policy` - are **required on every installed Promise**,
regardless of `visible`: they answer "who's accountable, how mature is it,
who do I ask for help, what data policy applies" for a security or finance
actor auditing the platform, without anyone walking them through a README.
`GET /api/promises?all=true` and `GET /api/promises/{name}` both return
every installed Promise's evidence (and, for any gap, a `missingEvidence`
list naming exactly which annotations are absent or invalid) regardless of
catalog visibility - the broker doesn't gate this on whether a Promise is
self-served through the generic catalog.
`broker/internal/catalog/evidence_lint_test.go` fails `make broker-test` if
any checked-in Promise regresses on this.
```

- [ ] **Step 2: Verify the section renders correctly**

Run: `grep -n "Operational evidence" README.md`
Expected: one match, in the new paragraph just added.

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "Document the operational evidence annotations in the root README"
```

---

### Task 4: One-line evidence summary in each Promise README

**Files:**
- Modify: `promises/database/README.md`, `promises/business-unit/README.md`, `promises/team/README.md`, `promises/project/README.md`, `promises/environment/README.md`

**Interfaces:**
- Consumes: the same annotation values set on each `promise.yaml` in Task 2 (no code interface — documentation only, keeps prose and metadata from drifting apart).

- [ ] **Step 1: Add the summary line to `promises/database/README.md`**

Find (lines 7-10):

```markdown
Requesting a `Database` gets you a [Zalando Postgres Operator](https://github.com/zalando/postgres-operator)
`postgresql` custom resource, sized from `spec.size` on the request.

- `promise.yaml` - the `Database` CRD (`demo.kratix.io/v1alpha1`) plus two workflows:
```

Replace with:

```markdown
Requesting a `Database` gets you a [Zalando Postgres Operator](https://github.com/zalando/postgres-operator)
`postgresql` custom resource, sized from `spec.size` on the request.

**Operational evidence** (see the root `README.md`'s "Marketplace metadata convention"):
owner `platform-team`, lifecycle `stable`, support `#platform-eng`, policy `confidential` -
the one Promise here that provisions a place application data actually lands.

- `promise.yaml` - the `Database` CRD (`demo.kratix.io/v1alpha1`) plus two workflows:
```

- [ ] **Step 2: Add the summary line to `promises/business-unit/README.md`**

Find (lines 10-14):

```markdown
Marked `marketplace.kratix.io/visible: "false"`: provisioning a business unit is an
operator/day-2 action, not something end-user teams self-serve through the catalog (unlike
`database`).

- `promise.yaml` - the `BusinessUnit` CRD (`demo.kratix.io/v1alpha1`) plus two workflows:
```

Replace with:

```markdown
Marked `marketplace.kratix.io/visible: "false"`: provisioning a business unit is an
operator/day-2 action, not something end-user teams self-serve through the catalog (unlike
`database`).

**Operational evidence** (see the root `README.md`'s "Marketplace metadata convention"):
owner `platform-team`, lifecycle `stable`, support `#platform-eng`, policy `internal`.

- `promise.yaml` - the `BusinessUnit` CRD (`demo.kratix.io/v1alpha1`) plus two workflows:
```

- [ ] **Step 3: Add the summary line to `promises/team/README.md`**

Find (lines 8-11):

```markdown
Marked `marketplace.kratix.io/visible: "false"`: provisioning a team is an operator/day-2
action, not something end-user teams self-serve through the catalog (unlike `database`).

- `promise.yaml` - the `Team` CRD (`demo.kratix.io/v1alpha1`), with a single required field,
```

Replace with:

```markdown
Marked `marketplace.kratix.io/visible: "false"`: provisioning a team is an operator/day-2
action, not something end-user teams self-serve through the catalog (unlike `database`).

**Operational evidence** (see the root `README.md`'s "Marketplace metadata convention"):
owner `platform-team`, lifecycle `stable`, support `#platform-eng`, policy `internal`.

- `promise.yaml` - the `Team` CRD (`demo.kratix.io/v1alpha1`), with a single required field,
```

- [ ] **Step 4: Add the summary line to `promises/project/README.md`**

Find (lines 9-14):

```markdown
Marked `marketplace.kratix.io/visible: "false"`: not because it's operator-only like
`business-unit`/`team`, but because it's self-served through the broker's dedicated Projects
UI/API (`POST/GET/DELETE /api/promises/project/requests...` - see the root `README.md`'s
"Marketplace broker API" section) rather than the generic ad-hoc catalog-request flow.

- `promise.yaml` - the `Project` CRD (`demo.kratix.io/v1alpha1`), with a single optional field,
```

Replace with:

```markdown
Marked `marketplace.kratix.io/visible: "false"`: not because it's operator-only like
`business-unit`/`team`, but because it's self-served through the broker's dedicated Projects
UI/API (`POST/GET/DELETE /api/promises/project/requests...` - see the root `README.md`'s
"Marketplace broker API" section) rather than the generic ad-hoc catalog-request flow.

**Operational evidence** (see the root `README.md`'s "Marketplace metadata convention"):
owner `platform-team`, lifecycle `experimental`, support `#platform-eng`, policy `internal`.

- `promise.yaml` - the `Project` CRD (`demo.kratix.io/v1alpha1`), with a single optional field,
```

- [ ] **Step 5: Add the summary line to `promises/environment/README.md`**

Find (lines 9-15):

```markdown
Marked `marketplace.kratix.io/visible: "false"` for a different reason than `business-unit`/
`team`: it's not operator-only, it's **broker-only** - see "Why `team`/`businessUnit` are
broker-owned fields" below for why a raw `kubectl apply` or the generic
`/api/promises/environment/requests` route are both unsafe paths for a team to use directly,
even though nothing technically stops it.

- `promise.yaml` - the `Environment` CRD (`demo.kratix.io/v1alpha1`), with three required
```

Replace with:

```markdown
Marked `marketplace.kratix.io/visible: "false"` for a different reason than `business-unit`/
`team`: it's not operator-only, it's **broker-only** - see "Why `team`/`businessUnit` are
broker-owned fields" below for why a raw `kubectl apply` or the generic
`/api/promises/environment/requests` route are both unsafe paths for a team to use directly,
even though nothing technically stops it.

**Operational evidence** (see the root `README.md`'s "Marketplace metadata convention"):
owner `platform-team`, lifecycle `experimental`, support `#platform-eng`, policy `internal`.

- `promise.yaml` - the `Environment` CRD (`demo.kratix.io/v1alpha1`), with three required
```

- [ ] **Step 6: Verify every README's stated values match its `promise.yaml`**

Run:

```bash
for d in database business-unit team project environment; do
  echo "=== $d ==="
  grep -A3 "marketplace.kratix.io/owner" promises/$d/promise.yaml
  grep "Operational evidence" -A2 promises/$d/README.md
done
```

Expected: for each Promise, the `owner`/`lifecycle`/`support`/`policy` values in `promise.yaml` match what the README states.

- [ ] **Step 7: Commit**

```bash
git add promises/database/README.md promises/business-unit/README.md promises/team/README.md promises/project/README.md promises/environment/README.md
git commit -m "Add operational evidence summary to each Promise README

Keeps the prose and the queryable metadata from drifting apart."
```

---

### Task 5: Full verification

**Files:** none (verification only)

- [ ] **Step 1: Run the full broker test suite**

Run: `make broker-test`
Expected: PASS, with every test in `broker/internal/catalog` (and every other broker package) passing - including both new tests from Task 1 and the lint test from Task 2.

- [ ] **Step 2: Confirm the broker API actually returns the new fields**

This is a read of the code path, not a live-cluster check (no UI or deployment changes were made, so a live `make dev` run isn't needed to verify this). Run:

```bash
cd broker && go doc ./internal/catalog Entry
```

Expected: the printed struct includes `Owner`, `Lifecycle`, `Support`, `Policy`, and `MissingEvidence` fields.

- [ ] **Step 3: Confirm no stray changes**

Run: `git status`
Expected: clean working tree - every change from Tasks 1-4 was already committed.

- [ ] **Step 4: Review the commit sequence**

Run: `git log --oneline -6`
Expected: five commits on top of the design-doc commit, one per task, each independently reviewable.
