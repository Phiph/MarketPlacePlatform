# Operational Evidence Design

## Context

Review feedback against the CNCF platform-capabilities framing flagged "Operational
Evidence" as unaddressed: the four Promises added since the initial database demo
(`business-unit`, `environment`, `project`, `team`) carry no owner, lifecycle, support, or
policy metadata. What exists instead is prose - `promises/business-unit/README.md` (214
lines) genuinely explains the ownership model, the empty-`owners` decision, and
provisioning order, but that's git-and-README knowledge. A finance or security actor
auditing the platform can't query any of it from the running system; they'd need someone
to walk them through the docs. A "harness plan" that would have covered this convention
was previously scoped but shelved and never built.

This design closes that gap using the same mechanism the repo already has for
`display-name`/`description`: well-known annotations on the `Promise` object itself,
parsed by the broker and exposed through the catalog API it already serves.

## Goal

Make owner, lifecycle stage, support contact, and data/compliance classification
queryable from the running platform, for every installed Promise, without touching the
UI or adding a new endpoint.

## Non-goals

- No new broker endpoint - `GET /api/promises?all=true` and `GET /api/promises/{name}`
  already return every installed Promise regardless of marketplace visibility; this reuses
  them.
- No UI changes. The feedback's bar is "queryable from the running platform," which the
  broker API alone satisfies. Surfacing this in `ui/src` is a natural fast-follow, not part
  of this pass.
- No admission-time enforcement (e.g. a validating webhook blocking `kubectl apply` of a
  non-compliant Promise). Enforcement here is a repo-level test plus a queryable
  completeness flag - matches the weight of the rest of this demo repo's tooling, and
  actually satisfies the feedback (which asks for queryability, not a hard gate).
- No change to `marketplace.kratix.io/team`, the existing label that tags *consumer*
  ownership of resources a Promise creates. The new `owner` annotation is a different
  concept - see below.

## Metadata convention

Four new annotations on the `Promise` object's `metadata.annotations`, alongside the
existing `marketplace.kratix.io/display-name` and `marketplace.kratix.io/description`:

| Annotation | Meaning | Values |
|---|---|---|
| `marketplace.kratix.io/owner` | Team accountable for *this Promise's* pipeline and maintenance | free-text team slug, e.g. `platform-team` |
| `marketplace.kratix.io/lifecycle` | Maturity stage | one of `experimental` \| `stable` \| `deprecated` |
| `marketplace.kratix.io/support` | Where to get help with this Promise | free-text contact (Slack channel, email, etc.) |
| `marketplace.kratix.io/policy` | Data/compliance classification | one of `internal` \| `confidential` \| `regulated` |

**`owner` vs. the existing `marketplace.kratix.io/team` label**: `team` (set by the `team`
Promise's pipeline on the namespaces/resources it creates) answers "which consuming team
does this *resource* belong to." `owner` answers "which platform team maintains *this
Promise's own pipeline code*." They can be the same string in practice but are different
axes - a Promise's `owner` doesn't change per-request the way a resource's `team` label
does.

All four annotations are **required on every installed Promise**, regardless of its
`marketplace.kratix.io/visible` value. Three of the four Promises this feedback names
(`business-unit`, `project`, `team`) are intentionally `visible: "false"` - operator-only
or routed through dedicated broker endpoints rather than the generic catalog - so gating
the requirement on visibility would exempt exactly the Promises the feedback is about.
Operational evidence answers "what's running on the platform and who's accountable," which
is a property of *installation*, not of catalog listing.

`lifecycle` and `policy` are validated against their fixed enum. `owner` and `support` are
free text, following the same precedent as `display-name`/`description`.

## Broker changes (`broker/internal/catalog/catalog.go`)

- Add four new annotation-key constants: `AnnotationOwner`, `AnnotationLifecycle`,
  `AnnotationSupport`, `AnnotationPolicy`.
- Add `Owner`, `Lifecycle`, `Support`, `Policy string` fields to `Entry`. Populated in
  `parseEntry` the same way `DisplayName`/`Description` are today - read straight from
  `obj.GetAnnotations()`, no fallback default. A missing value stays an empty string; it's
  surfaced structurally, not papered over.
- Add `MissingEvidence []string` to `Entry`, computed in `parseEntry`: the subset of
  `{"owner", "lifecycle", "support", "policy"}` whose annotation is absent, or (for
  `lifecycle`/`policy` specifically) present but not one of the allowed enum values. Empty
  slice when complete. This field is the queryable answer to "does this Promise have
  operational evidence" - available today via `GET /api/promises?all=true` (list, any
  visibility) and `GET /api/promises/{name}` (single, ignores visibility entirely). Both
  endpoints already exist and require no new auth scoping; a caller who can already list
  the catalog can already see evidence completeness.
- Backfill real annotation values on all five Promises (`database`, `business-unit`,
  `environment`, `project`, `team`) in their `promise.yaml` files, so the repo demonstrates
  the convention rather than just defining it. Values come from what's already true in
  practice (e.g. `database`'s existing maturity, `business-unit`'s operator-only nature).

## Enforcement: repo-level lint test

A new Go test, `broker/internal/catalog/evidence_lint_test.go`:

- Globs `../../../promises/*/promise.yaml` (three levels up from
  `broker/internal/catalog/` to the repo root, then into `promises/`) - the real checked-in
  files, distinct from `catalog_test.go`'s synthetic in-memory fixtures, which don't touch
  the actual YAML on disk.
- Unmarshals each file into `unstructured.Unstructured` via `sigs.k8s.io/yaml` (already an
  indirect dependency in `broker/go.mod`; this makes it direct).
- Runs the same `parseEntry` used by the live broker, and fails - naming the specific
  Promise and the specific missing/invalid annotation - if `MissingEvidence` is non-empty
  for any Promise.

Runs under the existing `make broker-test` (`cd broker && go test ./...`). No new tooling,
no CI wiring, since the repo has no CI workflows today. A sixth Promise added later without
evidence annotations fails this test immediately instead of surfacing as a gap discovered
by a future audit.

## Documentation updates

- Root `README.md`, "Marketplace metadata convention" section: document the four new
  annotations alongside the existing three, including the `owner`-vs-`team` distinction
  above - this is the canonical place someone looks to understand the convention.
- Each Promise's `README.md` (`database`, `business-unit`, `environment`, `project`,
  `team`): one line stating its actual owner/lifecycle/support/policy values, so prose and
  queryable metadata don't drift apart.
- No change to `promises/business-unit/README.md`'s existing "Why `owners` is empty"
  section - that's Capsule's `Tenant.spec.owners` RBAC model, an unrelated use of the word
  "owners."

## Testing

- Extend `catalog_test.go`'s existing fixture-based tests to cover `parseEntry` populating
  the four new `Entry` fields and computing `MissingEvidence` correctly: all-present,
  each-field-individually-missing, and invalid-enum-value cases for `lifecycle`/`policy`.
- The lint test (above) is the regression guard for the five real Promises staying
  compliant; the fixture tests are the regression guard for `parseEntry`'s logic itself.

## Summary of file changes

- `broker/internal/catalog/catalog.go` - new constants, `Entry` fields, `parseEntry` logic
- `broker/internal/catalog/catalog_test.go` - new field/`MissingEvidence` coverage
- `broker/internal/catalog/evidence_lint_test.go` - new file, real-YAML lint test
- `broker/go.mod` / `go.sum` - `sigs.k8s.io/yaml` promoted to direct dependency
- `promises/{database,business-unit,environment,project,team}/promise.yaml` - four new
  annotations each
- `promises/{database,business-unit,environment,project,team}/README.md` - one-line
  evidence summary each
- `README.md` - "Marketplace metadata convention" section extended
