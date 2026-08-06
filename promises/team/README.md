# `team` Promise

Provisions a team's namespace inside its business unit's Capsule Tenant (see the sibling
`business-unit` Promise, `promises/business-unit/` - **provision the business unit first**,
and confirm its Tenant actually exists, before submitting any `Team` request that references
it; see that Promise's README, "Provisioning order matters", for why).

Marked `marketplace.kratix.io/visible: "false"`: provisioning a team is an operator/day-2
action, not something end-user teams self-serve through the catalog (unlike `database`).

**Operational evidence** (see the root `README.md`'s "Marketplace metadata convention"):
owner `platform-team`, lifecycle `stable`, support `#platform-eng`, policy `internal`.

- `promise.yaml` - the `Team` CRD (`demo.kratix.io/v1alpha1`), with a single required field,
  `spec.businessUnit` (the name of an already-provisioned `BusinessUnit`). No promise-level
  dependency workflow here - Capsule and its CRDs are installed once by `business-unit`'s own
  dependency step, and this Promise assumes they're already present.
- `workflows/resource/configure/team-configure` - a Python pipeline that runs per-request,
  reading the request's name (the team) and `spec.businessUnit`, and writing **only** a
  `Namespace` named `team-<name>`, labeled `capsule.clastix.io/tenant: <businessUnit>` (points
  at the parent business unit's Tenant by name) and `marketplace.kratix.io/team: <name>` (read
  by the shared `GlobalTenantResource` - see `promises/business-unit/README.md` - to bind this
  namespace's RBAC to the right Group).
- `example-resource.yaml` - a sample `Team` request, named `payments`, referencing business
  unit `platform-org` - matches `broker/config/teams.yaml`.

This Promise never creates or touches a Capsule `Tenant` - only `business-unit` does. That
separation (a `Team`'s `Namespace` always references a Tenant provisioned by a *different,
earlier* resource request, never one created in the same batch) is what keeps this safe to
ship declaratively via Flux: see `promises/business-unit/README.md`'s "Provisioning order
matters" section for the deadlock this avoids, and why the ordering still has to be respected
even though `Tenant` and `Namespace` come from two different Promises.

`destinationSelectors: [{matchLabels: {environment: platform}}]` (`promise.yaml`) lands every
team's `Namespace` on the **platform** cluster, same reasoning as `business-unit` - see that
Promise's README.

The namespace naming convention (`team-<name>`) is single-sourced in
`broker/internal/tenant/tenant.go`'s `Namespace()` function - this pipeline's `namespace_name`
must stay byte-for-byte identical to it, and the Kubernetes Group convention
(`marketplace:team-<name>`) is likewise single-sourced in that file's `Group()` - both are
computed from the `marketplace.kratix.io/team` label by the shared `GlobalTenantResource`
template, not by this pipeline directly. There's no way to share code across Go and Python
here, so all three carry comments pointing at each other.

## Try it

```bash
# Provision the business unit first (see promises/business-unit/README.md) and confirm:
kubectl --context kind-platform get tenants.capsule.clastix.io platform-org

make promise-build promise-load PROMISE_DIR=promises/team
kubectl --context kind-platform apply -f promises/team/promise.yaml
kubectl --context kind-platform apply -f promises/team/example-resource.yaml
```

Then:

```bash
kubectl --context kind-platform get teams.demo.kratix.io payments -w
kubectl --context kind-platform get ns team-payments --show-labels
kubectl --context kind-platform get rolebindings -n team-payments
```

The `RoleBinding` in that last command comes from the shared `GlobalTenantResource`, not from
this Promise's own output - give it a few reconcile cycles (`resyncPeriod: 60s`, see
`promises/business-unit/workflows/promise/configure/dependencies/configure-deps/resources/team-rbac.yaml`)
if it's not there immediately.

## Iterating

After editing
`workflows/resource/configure/team-configure/kratix-guide-team-resource-pipeline/scripts/pipeline.py`:

```bash
make promise-build promise-load PROMISE_DIR=promises/team
kubectl --context kind-platform delete team payments
kubectl --context kind-platform apply -f promises/team/example-resource.yaml
```
