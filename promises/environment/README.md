# `environment` Promise

The second half of the **Team -> Projects -> Environments** layer (see the sibling `project`
Promise, `promises/project/`, for the first half). An Environment (`dev`, `staging`, `prod`,
...) is one deployable namespace belonging to a Project - a project isn't a single namespace,
it's a *set* of them, one per environment, each traceable back to the owning team via the same
`marketplace.kratix.io/team` label `promises/team/` already uses.

Marked `marketplace.kratix.io/visible: "false"` for a different reason than `business-unit`/
`team`: it's not operator-only, it's **broker-only** - see "Why `team`/`businessUnit` are
broker-owned fields" below for why a raw `kubectl apply` or the generic
`/api/promises/environment/requests` route are both unsafe paths for a team to use directly,
even though nothing technically stops it.

- `promise.yaml` - the `Environment` CRD (`demo.kratix.io/v1alpha1`), with three required
  fields: `spec.project` (name of an already-created `Project`, same namespace),
  `spec.team`, `spec.businessUnit`.
- `workflows/resource/configure/environment-configure` - a Python pipeline that runs
  per-request, reading the request's name (the environment) plus `spec.project`/`spec.team`/
  `spec.businessUnit`, and writing **only** a `Namespace` named
  `project-<team>-<project>-<environment>` (see "Why team is part of the namespace name"
  below), labeled:
  - `capsule.clastix.io/tenant: <businessUnit>` - same mechanism `team`'s pipeline uses.
  - `marketplace.kratix.io/team: <team>` - **the same label key** the existing
    `GlobalTenantResource` (`promises/business-unit`'s `team-rbac.yaml`) already watches. This
    is the whole reason zero RBAC changes were needed to add this layer: the shared
    `GlobalTenantResource` grants a team's Group access to any namespace carrying its own
    `marketplace.kratix.io/team` label, regardless of what the namespace is named or how deep
    in the ownership tree it sits.
  - `marketplace.kratix.io/project`, `marketplace.kratix.io/environment` - new, for
    traceability/UI filtering only. Not read by any RBAC mechanism.
- `example-resource.yaml` - a sample `Environment` request, `dev`, under project
  `checkout-service` (see `promises/project/example-resource.yaml`), team `checkout`, business
  unit `platform-org` - matching `broker/config/teams.yaml`.

## Why team is part of the namespace name

Namespaces are cluster-scoped in Kubernetes, but a `Project`'s name is only unique within its
own team's namespace - two different teams can both create a project called
`checkout-service` (nothing stops them; each lives in its own `team-<name>` namespace). If the
environment namespace were just `project-<project>-<environment>`, two teams picking the same
project+environment name would collide on one real `Namespace` object - and because this is
applied declaratively (`kubectl apply`/Flux), whichever team's `Environment` request
reconciled second would silently overwrite the first's `marketplace.kratix.io/team` label,
handing that namespace's RBAC to the wrong team without either team being told. Prefixing with
`team` (`broker/internal/tenant.ProjectEnvironmentNamespace()`, this pipeline's
`namespace_name()`) makes this collision-proof, the same way `team-<name>` namespaces already
rely on team names being unique across the whole directory.

## Why `team`/`businessUnit` are broker-owned fields

`spec.team` becomes a real RBAC-relevant label on a real namespace: whatever value this
pipeline reads is what the shared `GlobalTenantResource` binds that namespace's access to.
The CRD schema can't express "only the broker may set this field" - Kubernetes validation
has no such concept - so the safety has to live one layer up, in the broker itself:
`POST /api/environments` (see the root `README.md`) takes just `{name, project}` from the
caller and composes `spec.team`/`spec.businessUnit` itself from the authenticated caller's own
identity (`tenant.Directory`), the same way `tenant.Namespace(team)` is broker-computed today
rather than accepted from a request body. A team hand-crafting an `Environment` object with
someone else's team name (via `kubectl` or the fully generic request route) would mislabel the
resulting namespace's ownership - fine for an operator doing it deliberately for a reason, not
something a team's own resource request should ever be able to do by accident or malice. Use
the dedicated endpoint; treat the schema fields as read-only outside it.

## No provisioning-order hazard (unlike `business-unit` -> `team`)

Unlike `team`'s dependency on `business-unit`'s `Tenant` already existing (see that Promise's
README, "Provisioning order matters"), there's no equivalent race here: by the time any team
can authenticate to the broker at all, its own `team-<name>` namespace - and therefore its
business unit's `Tenant` - already exists (that's how `tenant.Group(team)` gets meaningful
RBAC in the first place). An `Environment`'s referenced `Project` is a same-namespace,
no-infrastructure object (see `promises/project/README.md`), so there's nothing racing to
apply here either.

## Operational note: namespace quota

`BusinessUnit.spec.namespaceQuota` (default `5`, see `promises/business-unit/`) caps how many
namespaces a business unit's Capsule Tenant may own in total - and every environment namespace
now counts against that same quota, not just one `team-<name>` namespace per team. Raise it
(`kubectl edit businessunit <bu>` or resubmit with a higher `spec.namespaceQuota`) before a
business unit's teams collectively provision more than a handful of project x environment
combinations.

## Try it

```bash
# Provision the project first (see promises/project/README.md):
kubectl --context kind-platform get projects.demo.kratix.io -n team-checkout checkout-service

make promise-build promise-load PROMISE_DIR=promises/environment
kubectl --context kind-platform apply -f promises/environment/promise.yaml
kubectl --context kind-platform apply -f promises/environment/example-resource.yaml
```

Then:

```bash
kubectl --context kind-platform get environments.demo.kratix.io -n team-checkout dev -w
kubectl --context kind-platform get ns project-checkout-checkout-service-dev --show-labels
kubectl --context kind-platform get rolebindings -n project-checkout-checkout-service-dev
```

The `RoleBinding` comes from the same shared `GlobalTenantResource` `team`'s namespaces get -
give it a few reconcile cycles (`resyncPeriod: 60s`) if it's not there immediately.

In normal use, submit this through the broker's `POST /api/environments` instead - see the root
`README.md`'s "Marketplace broker API" section, or the UI's Projects page.

## Iterating

After editing
`workflows/resource/configure/environment-configure/kratix-guide-environment-resource-pipeline/scripts/pipeline.py`:

```bash
make promise-build promise-load PROMISE_DIR=promises/environment
kubectl --context kind-platform delete environment dev -n team-checkout
kubectl --context kind-platform apply -f promises/environment/example-resource.yaml
```
