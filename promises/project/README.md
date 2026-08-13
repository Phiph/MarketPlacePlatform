# `project` Promise

The first half of the **Team -> Projects -> Environments** layer sketched in
`promises/business-unit/README.md`'s original "future direction" note: a team can own multiple
Projects, and each Project can have multiple Environments (see the sibling `environment`
Promise, `promises/environment/`) - a project isn't itself a namespace, it's the grouping
record an Environment references by name.

Marked `marketplace.kratix.io/visible: "false"`: not because it's operator-only like
`business-unit`/`team`, but because it's self-served through the broker's dedicated Projects
UI/API (`POST/GET/DELETE /api/promises/project/requests...` - see the root `README.md`'s
"Marketplace broker API" section) rather than the generic ad-hoc catalog-request flow.

**Operational evidence** (see the root `README.md`'s "Marketplace metadata convention"):
owner `platform-team`, lifecycle `experimental`, support `#platform-eng`, policy `internal`.

- `promise.yaml` - the `Project` CRD (`demo.kratix.io/v1alpha1`), with a single optional field,
  `spec.description`. One resource workflow only - no promise-level dependency workflow, no
  `destinationSelectors` (see "Why no infra output" below).
- `workflows/resource/configure/project-configure` - a Python pipeline that runs per-request,
  reading the request's name and optional `spec.description`, and writing **only status** -
  no output manifest at all.
- `example-resource.yaml` - a sample `Project` request, `checkout-service`, living in the
  `team-checkout` namespace (matching `broker/config/teams.yaml`'s `checkout` team).

## Why no infra output

Unlike every other Promise in this repo, this pipeline never writes anything to
`/kratix/output`. A Project doesn't correspond to a Capsule `Tenant`, a `Namespace`, or any
other piece of infrastructure by itself - it's purely a logical grouping that an `Environment`
request (`promises/environment/`) points at via `spec.project`. All the infrastructure (a real,
RBAC-isolated namespace) gets created once an Environment is requested under it, not when the
Project itself is created. Status (`status.name`, `status.description`) is the only observable
effect of submitting one - enough for the broker/UI to list a team's Projects and for an
Environment request to be validated against ("does this project exist in the caller's own
namespace?") without needing to read any other object.

## Where a Project lives

A Project request is submitted into the owning team's own namespace (`team-<name>`), the same
as any other self-served Promise request (e.g. `database`) - **not** applied by an operator into
`default` like `business-unit`/`team` are. That's what makes it safe to expose through the
fully generic broker routes with zero extra broker-side code: `spec.description` carries no
identity-sensitive information, so there's nothing for the broker to protect by composing the
request itself (contrast with `environment`'s `spec.team`/`spec.businessUnit` - see that
Promise's README for why those *do* need broker-side composition).

## Try it

```bash
make promise-build promise-load PROMISE_DIR=promises/project
kubectl --context kind-platform apply -f promises/project/promise.yaml
kubectl --context kind-platform apply -f promises/project/example-resource.yaml
```

Then:

```bash
kubectl --context kind-platform get projects.demo.kratix.io -n team-checkout checkout-service -w
```

In normal use this happens through the broker instead of `kubectl` directly - see the root
`README.md`'s "Marketplace broker API" section, or the UI's Projects page.

## Iterating

After editing
`workflows/resource/configure/project-configure/kratix-guide-project-resource-pipeline/scripts/pipeline.py`:

```bash
make promise-build promise-load PROMISE_DIR=promises/project
kubectl --context kind-platform delete project checkout-service -n team-checkout
kubectl --context kind-platform apply -f promises/project/example-resource.yaml
```
