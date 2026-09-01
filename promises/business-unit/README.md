# `business-unit` Promise

Provisions a business unit's **Capsule Tenant** (https://projectcapsule.dev) - the mechanism
that turns "this business unit's teams shouldn't touch resources outside their own namespace"
from an application-code convention into something Kubernetes' own RBAC and admission
webhooks enforce. Teams live inside a business unit's Tenant (see the sibling `team` Promise,
`promises/team/`) but are isolated from each other within it - see "Why `owners` is empty"
below.

Marked `marketplace.kratix.io/visible: "false"`: provisioning a business unit is an
operator/day-2 action, not something end-user teams self-serve through the catalog (unlike
`database`).

**Operational evidence** (see the root `README.md`'s "Marketplace metadata convention"):
owner `platform-team`, lifecycle `stable`, support `#platform-eng`, policy `internal`.

- `promise.yaml` - the `BusinessUnit` CRD (`demo.kratix.io/v1alpha1`) plus two workflows:
  - **promise workflow** (`workflows/promise/configure/dependencies/configure-deps`) -
    installs Capsule itself (operator, CRDs, a `CapsuleConfiguration`, and the shared
    `GlobalTenantResource` that makes team-level isolation work - see below) as a static
    dependency, once, when the Promise is installed.
  - **resource workflow** (`workflows/resource/configure/business-unit-configure`) - a Python
    pipeline that runs per-request, reading the request's name (the business unit), optional
    `spec.namespaceQuota` (default `5`), and optional `spec.resourceQuotas` (a flat map of
    hard limits, e.g. `{cpu: "20", memory: "40Gi"}`), and writing a Capsule `Tenant` named
    after the business unit, labeled `marketplace.kratix.io/managed: "true"`.
- `example-resource.yaml` - a sample `BusinessUnit` request, named `platform-org` to match
  `broker/config/teams.yaml`.

Both workflow stages carry `destinationSelectors: [{matchLabels: {environment: platform}}]`
(`promise.yaml`) so Capsule and every business unit's `Tenant` land on the **platform**
cluster - not the worker - since that's where Capsule's own webhooks and the marketplace
broker actually run. `platform-cluster` is registered with `strictMatchLabels: true`
(`clusters/platform/kratix/kratix-release.yaml`), so it only receives output that explicitly asks for it
this way.

## Why `owners` is empty

Capsule's own `Tenant.spec.owners` model grants every owner equal access to *every* namespace
in the Tenant - fine when a Tenant belongs to exactly one team, wrong once multiple teams
share one business unit's Tenant (payments and checkout, say): listing both teams' Groups as
owners would let either one see the other's namespace. So `business-unit-configure`'s pipeline
deliberately leaves `owners: []` - nobody gets tenant-wide access via Capsule's normal
mechanism. (A BU-admin persona - someone who legitimately needs to see every namespace in the
BU - isn't modelled yet; add an owners entry when that's needed.)

Team-level access instead comes from `team-rbac.yaml`, a `GlobalTenantResource` installed
once alongside Capsule, not per business unit. Its `tenantSelector` matches every
`marketplace.kratix.io/managed: "true"` Tenant (i.e. every business unit), and `scope:
Namespace` makes it render its template **once per namespace** across all of them, with that
one namespace's own object available in the template - so each rendered `RoleBinding`'s
subject Group is derived from *that namespace's own* `marketplace.kratix.io/team` label (set
by the `team` Promise's pipeline), not anything Tenant-wide. A team's namespace only ever gets
a `RoleBinding` scoped to itself, never its business-unit-mates'. Bound to `ClusterRole/edit`
(not `admin`) deliberately - a team can create/manage resources in its own namespace but can't
grant itself broader access via more RBAC objects.

Verified live: two teams (`payments`, `checkout`) provisioned under the same business unit -
`kubectl auth can-i list configmaps -n team-checkout --as-group=marketplace:team-payments`
answers `no`, despite both sharing one Capsule Tenant.

## Why teams also need `marketplace-tenant-resources`

Kubernetes' built-in `admin`/`edit`/`view` ClusterRoles are *aggregated* (a native RBAC
mechanism, unrelated to Capsule) - they only include rules from other ClusterRoles explicitly
labeled `rbac.authorization.k8s.io/aggregate-to-{admin,edit}: "true"`. Kratix auto-generates a
ClusterRole per Promise (e.g. `database-promise-controller`, scoped to
`demo.kratix.io/databases`) for its own controller/pipeline ServiceAccount, but doesn't label
it for aggregation - so out of the box, `team-rbac.yaml`'s `edit` binding gives a team full
rights over core resources (`Pod`, `ConfigMap`, ...) in its namespace but **zero** RBAC on any
Promise's CRD, and every broker call 403s. `resources/marketplace-rbac.yaml` closes this gap
with one aggregated ClusterRole covering the whole `demo.kratix.io` API group (the convention
every Promise in this repo uses) - verified live: without it, `kubectl auth can-i create
databases.demo.kratix.io -n team-payments --as-group=marketplace:team-payments` answers `no`;
with it, `yes`.

## Provisioning order matters: a business unit's Tenant must land before any of its teams'

`promises/team/`'s pipeline ships a `Namespace` referencing this Tenant by name (see that
Promise's README). Both a `BusinessUnit`'s `Tenant` output and a `Team`'s `Namespace` output
are delivered to the platform cluster through the **same shared** Flux `Kustomization`
(`kratix-workload-resources` - every resource-configure output for a Destination lands in one
bucket path, regardless of which Promise or which individual request produced it). Flux
dry-runs its *entire* batch before applying any of it, and if a `Team`'s `Namespace` is in
that batch before its business unit's `Tenant` actually exists on the cluster, Capsule's
webhook denies it (`tenants.capsule.clastix.io "<bu>" not found`) - which fails the whole
batch, which means the `Tenant` (bundled in that same batch) never gets applied either,
forever. Verified live: submitting a `BusinessUnit` and its `Team` requests back-to-back (as
`make broker-provision-teams` originally did) reproduces this permanently, the same failure
mode `promises/team`'s own header comment describes avoiding for Tenant+Namespace *within one
resource's own output* - it turns out the same risk exists *across different resources*,
because Flux's batching doesn't know or care about Kratix's resource boundaries.

The fix: `make broker-provision-teams` applies every `BusinessUnit` request first, **waits for
each one's Tenant to actually exist on the cluster**, and only then applies `Team` requests -
by the time a team's `Namespace` reaches the shared bucket, its business unit's `Tenant` is
long since a real, independently-applied object, so there's no batch for it to be blocked
inside. If you're provisioning by hand rather than via that target, follow the same order:
apply `BusinessUnit`, confirm with `kubectl get tenants.capsule.clastix.io <bu-name>`, only
then apply `Team` requests referencing it.

## Installing Capsule

`workflows/promise/configure/dependencies/configure-deps/resources/` holds Capsule's rendered
manifests, generated once and committed - the same pattern `promises/database` uses for the
Postgres Operator (`cp /resources/* /kratix/output`, nothing templated at pipeline run time).
Regenerate them with:

```bash
helm repo add projectcapsule https://projectcapsule.github.io/charts
helm template capsule projectcapsule/capsule --version 0.13.11 \
  --namespace capsule-system --include-crds \
  > /tmp/capsule-rendered.yaml
```

No values override is needed - chart defaults are correct as-is: `manager.options.users`
(`[{kind: Group, name: projectcapsule.dev}]`) is exactly the group the broker's impersonated
clients carry alongside each team's own Group (see
`broker/internal/k8sclient/impersonate.go`). `manager.options.administrators` does need one
entry, though - see "Gotcha: `administrators` entries have no `namespace` field" below for why
and what it's for.

**Gotcha: strip Helm-hook-only resources before committing.** This chart ships several
`batch/v1` `Job`s annotated `helm.sh/hook: pre-install,pre-upgrade` / `pre-delete` (CRD
lifecycle management, and a pre-delete cleanup job) plus their supporting
`ConfigMap`/`ServiceAccount`/`RBAC`. Those annotations only mean something to the real Helm
engine during `helm install`/`upgrade`/`uninstall` - under this Promise's "copy static YAML"
delivery, a plain `kubectl apply` (or Flux) has no hook engine and just applies every object
unconditionally, immediately, every time. The `pre-delete` job is destructive in that
scenario: it deletes the webhook TLS secret and the `capsule-namespace-deleter`/
`-provisioner` `ClusterRole`s right after they're created. Filter every document carrying a
`helm.sh/hook` annotation out of the rendered output before splitting it into
`resources/*.yaml` (e.g. `yq eval 'select(.metadata.annotations."helm.sh/hook" == null)'`).
None of this loses functionality: the actual CRDs are already included via `--include-crds`
as plain `CustomResourceDefinition` objects (not the hook Job's ConfigMap-wrapped copies),
and Capsule's controller registers/updates its own `ValidatingWebhookConfiguration`/
`MutatingWebhookConfiguration` dynamically at runtime from the `CapsuleConfiguration`
object's `spec.admission` block - there's no static webhook-config manifest to ship at all.

**Gotcha: the chart doesn't render a `capsule-system` `Namespace`.** `helm install
--create-namespace` normally creates it; `helm template` doesn't render anything for that flag
at all. Add one by hand to `resources/` (see `resources/namespace.yaml`) - without it, Flux's
`kratix-workload-dependencies` `Kustomization` fails immediately (`ServiceAccount/capsule-system/capsule
not found: namespaces "capsule-system" not found`) since every other object in the chart's
output targets that namespace.

**Gotcha: `administrators`/`users` entries have no `namespace` field.**
`manager.options.administrators` needs Flux's `kustomize-controller` identity, since it's the
one applying `team`'s `Namespace` output and teams aren't Capsule owners (see "Why `owners` is
empty" above) - without it, Capsule's webhook rejects every team namespace as coming from an
unrecognized caller. The `CapsuleConfiguration` CRD's schema for `administrators` (and `users`)
is just `{kind: User|Group|ServiceAccount, name: string}` - there's nowhere to put a
ServiceAccount's namespace separately (confirmed against the rendered CRD's
`openAPIV3Schema`; a `namespace` key gets rejected with `field not declared in schema`). For
`kind: ServiceAccount`, `name` must be the fully-qualified Kubernetes username Capsule
compares admission requests' `UserInfo.Username` against:
`system:serviceaccount:flux-system:kustomize-controller` (confirmed via `kubectl get
deployment kustomize-controller -n flux-system
-o jsonpath='{.spec.template.spec.serviceAccountName}'` against `kind-platform` - the
`kratix-workload-*` `Kustomization`s don't set `spec.serviceAccountName`, so Flux applies with
that Deployment's own identity).

Capsule's webhooks need cert-manager, which this cluster already has
(`clusters/platform/cert-manager/cert-manager-release.yaml`, Flux-managed) - no new prerequisite.

## Try it

```bash
make promise-build promise-load PROMISE_DIR=promises/business-unit
kubectl --context kind-platform apply -f promises/business-unit/promise.yaml
kubectl --context kind-platform apply -f promises/business-unit/example-resource.yaml
```

Then, once the Tenant exists (see "Provisioning order matters" above for why this matters
before provisioning any teams under it):

```bash
kubectl --context kind-platform get businessunits.demo.kratix.io platform-org -w
kubectl --context kind-platform get tenants.capsule.clastix.io platform-org
```

`make broker-provision-teams` (see the top-level `README.md`) is the easiest way to also
provision the demo teams under it in the right order.

## Iterating

After editing
`workflows/resource/configure/business-unit-configure/kratix-guide-business-unit-resource-pipeline/scripts/pipeline.py`:

```bash
make promise-build promise-load PROMISE_DIR=promises/business-unit
kubectl --context kind-platform delete businessunit platform-org
kubectl --context kind-platform apply -f promises/business-unit/example-resource.yaml
```

## Team -> Projects -> Environments

The layer this README used to sketch as "future direction" is now built: a team can own
multiple **Projects** (`promises/project/`), each with multiple **Environments**
(`promises/environment/`) - `dev`, `staging`, `prod`, ... - and each environment is its own
deployable namespace (`project-<team>-<project>-<environment>`) that Promise resource requests land
in, traceable back to the owning team via the same `marketplace.kratix.io/team` label this
README describes above. As predicted, the Capsule/RBAC mechanism above needed zero changes to
support it - see `promises/environment/README.md` for how. The real work landed in the broker's
request-routing and the UI instead; see the root `README.md`'s "Marketplace broker API"
section for the endpoints.
