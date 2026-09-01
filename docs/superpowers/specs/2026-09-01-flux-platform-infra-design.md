# Flux-Managed Platform Infra (cert-manager, Kratix, MinIO)

## Problem

`clusters/platform/` today only covers cert-manager, and even that is
inert by default: `README.md`'s "Platform infra (Flux/GitOps)" section
says outright it's "additive, not yet wired into `make up`." The real
bootstrap path (`make up`) still installs cert-manager, Kratix, and MinIO
imperatively (`cert-manager`, `kratix-platform`, `minio` Makefile
targets - `kubectl apply`/`helm upgrade --install` run directly), before
Flux exists on the platform cluster at all (`flux-platform` isn't
installed until step 7, `kratix-platform-destination`). `make infra` is a
separate step you run *after* `make up`, to hand an already-running
cert-manager off to Flux - and the README documents in detail why doing
that against a live imperative install is fiddly (colliding resource
names, a `HelmRelease` that also owns the `cert-manager` Namespace).

These three components are core platform dependencies, not one-off setup
commands - they belong under the same GitOps reconciliation as everything
else in `clusters/platform/`, present and healthy from the moment a
fresh cluster comes up, not bolted on afterward.

## Goals

- cert-manager, Kratix, and MinIO are declared as Flux objects in
  `clusters/platform/`, reconciled by the platform cluster's own Flux
  instance from a fresh `make up` - no imperative install step, no
  after-the-fact hand-off.
- Strict ordering (cert-manager ready before Kratix installs; Kratix's
  namespace/Secret exist before MinIO applies) is expressed declaratively
  via Flux `Kustomization.spec.dependsOn`, not via Makefile step order.
- `make up` remains a single idempotent command; a fresh cluster ends up
  in the same state as before, just GitOps-managed throughout instead of
  imperative-then-handed-off.

## Non-goals

- **Destination registration (`kratix-worker`, `kratix-platform-destination`)
  stays imperative.** `kratix-worker` resolves the platform cluster's
  Docker network IP at runtime (`docker inspect ... kind ...
  IPAddress`) to reach MinIO's NodePort cross-cluster - a value that
  doesn't exist until the clusters are running and changes across
  recreations. That's a poor fit for a static Flux manifest committed to
  `clusters/platform/`. Both targets are left as Helm-CLI steps, run
  after the Flux infra chain settles.
- **No change to `flux-worker` or the worker cluster.** Only the platform
  cluster's infra bootstrap changes.
- **No git-remote GitOps.** `clusters/platform/` continues to be
  reconciled from a local OCI artifact (`make infra-push`/`infra-apply`),
  same as today - swapping the `OCIRepository` for a `GitRepository`
  against a real remote remains a separate, later change (already noted
  as a one-object swap in the existing `infra-apply` comments).
- **No version pin added for the Kratix Helm chart.** The current
  imperative `helm upgrade --install kratix` has no explicit
  `--version`; the new `HelmRelease` preserves that behavior (floating
  to latest) rather than introducing a pin as part of this change.

## Approach

Split `clusters/platform/` into three subfolders, each its own Flux
`Kustomization` sourced from the same OCI artifact, chained by
`dependsOn` so each layer only applies once the previous one is fully
`Ready`:

```
clusters/platform/
  cert-manager/   # unchanged: cert-manager-repo.yaml + cert-manager-release.yaml
  kratix/         # new: HelmRepository (syntasso) + HelmRelease, values inlined
                  # from today's hack/kratix/platform-values.yaml
  minio/          # moved from hack/kind/minio-install.yaml, content unchanged
```

`hack/kratix/platform-infra-source.yaml` grows from one `Kustomization`
into three, each pointing at its own subfolder path within the same
`platform-infra` `OCIRepository`:

- `cert-manager` - no `dependsOn`, applies first.
- `kratix` - `dependsOn: [{name: cert-manager}]`.
- `minio` - `dependsOn: [{name: kratix}]`.

All three keep `wait: true` (already the pattern in the current single
`Kustomization`), so `dependsOn` blocking is meaningful: Flux won't mark
a layer `Ready` - and therefore won't let the next layer's `dependsOn`
proceed - until every resource it applied (including `HelmRelease`s and
the MinIO bucket-creation `Job`) is itself healthy per kstatus.

This ordering is required, not incidental: `minio/`'s manifest
deliberately omits the `kratix-platform-system` Namespace and the
`default/minio-credentials` Secret, because the Kratix Helm chart already
creates both (via its `additionalResources` values) and Helm refuses to
adopt resources it didn't create - already noted in
`hack/kind/minio-install.yaml`'s own comments. Kratix's chart in turn
needs cert-manager's CRDs/webhooks present before its own
Certificate/webhook resources can be admitted.

### Why not one Kustomization for the whole folder?

Considered and rejected. A single `Kustomization` with `wait: true` waits
for *all* applied resources to become healthy before reporting Ready,
but does not guarantee the *order* resources within it get applied and
reconciled in - kubectl-apply ordering is not dependency-aware, so
Kratix's `HelmRelease` could be created (and start installing) before
cert-manager's `HelmRelease` has actually finished issuing CRDs/webhook
certs, causing a race. Splitting into per-layer `Kustomization`s and
using `dependsOn` makes the ordering explicit and enforced by Flux
itself, matching how the user asked for this to be modeled.

## Architecture

```
make up
  -> clusters                (kind: platform + worker)
  -> registry-configure       (wire local registry into both clusters)
  -> flux-platform             (moved earlier: Flux must exist before infra can be pushed)
  -> infra                    (push clusters/platform as OCI artifact, apply the 3-layer Kustomization chain)
       cert-manager Kustomization         (no dependsOn)
         -> kratix Kustomization           (dependsOn: cert-manager)
              -> minio Kustomization        (dependsOn: kratix)
  -> wait for kustomization/minio Ready   (replaces old separate waits for cert-manager/kratix-platform/minio)
  -> kratix-worker             (unchanged: Helm CLI, registers worker-1 Destination)
  -> kratix-platform-destination (unchanged: Helm CLI, registers platform-cluster Destination)
  -> metrics-server
  -> argo-register-worker
  -> demo-setup
```

## Components

| Piece | Where | Change |
|---|---|---|
| cert-manager manifests | `clusters/platform/cert-manager/` | Moved from `clusters/platform/` directly into a subfolder; content unchanged |
| Kratix HelmRelease | `clusters/platform/kratix/` (new) | New `HelmRepository` (`https://syntasso.github.io/helm-charts`) + `HelmRelease`, values inlined from `hack/kratix/platform-values.yaml` (`stateStores: []`, `destinations: []`, `additionalResources`: minio-credentials Secret, default BucketStateStore, worker-1 + platform-cluster Destinations) |
| MinIO manifests | `clusters/platform/minio/` (moved from `hack/kind/minio-install.yaml`) | Content unchanged; comment explaining the Namespace/Secret-ownership constraint updated to describe Flux `dependsOn` ordering instead of Makefile step order |
| Flux Kustomization chain | `hack/kratix/platform-infra-source.yaml` | One `Kustomization` becomes three (`cert-manager`, `kratix`, `minio`), each `path`-scoped to its subfolder, chained via `dependsOn`, all `wait: true` |
| `Makefile` | `cert-manager`, and the imperative halves of `kratix-platform`/`minio` targets | Removed. `CERT_MANAGER_VERSION` var removed (no longer referenced) |
| `Makefile` | `up` target | Reordered: `flux-platform` moves to run right after `clusters`/`registry-configure`; `infra` (push+apply) plus a `kubectl wait --for=condition=Ready kustomization/minio` replace the old cert-manager/kratix-platform/minio steps |
| `Makefile` | `verify` target | Add checks that the `cert-manager`, `kratix`, and `minio` Kustomizations are each `Ready=True`, ahead of the existing pod/Destination checks |
| `README.md` | "Platform infra (Flux/GitOps)" section | Rewritten: no more "additive, not yet wired into `make up`" caveat, no more dual-install collision warning (nothing imperative left to collide with) |
| `README.md` | "How this works" numbered list | Renumbered/reworded to describe the three-layer Flux chain in place of the old imperative cert-manager/Kratix/MinIO steps |

No changes needed to: `flux-worker`, `kratix-worker`,
`kratix-platform-destination`, `metrics-server`, `argo-register-worker`,
`demo-setup`, the broker, or any Promise.

## Error handling

- `make up` follows the repo's existing style for failure points - a
  `kubectl wait ... || { echo "FAIL: ..."; exit 1; }` after applying the
  infra chain: `kubectl wait --for=condition=Ready kustomization/minio -n
  flux-system --timeout=5m || { echo "FAIL: platform infra
  (cert-manager/kratix/minio) didn't reconcile - check 'flux get
  kustomizations -n flux-system' and 'flux get helmreleases -n
  flux-system'"; exit 1; }`. No new retry/rollback logic is introduced -
  matches every other failure point in `up` today, where the user fixes
  the underlying issue and re-runs `make up` (idempotent).
- If a layer's `HelmRelease` fails to install (e.g. a bad chart version),
  its owning `Kustomization` reports `Ready=False` with the underlying
  error in `.status.conditions`, and `dependsOn` keeps the next layer
  from even attempting to apply - failures don't cascade into confusing
  partial states in the next layer.

## Testing

- **`make up` end-to-end** (manual, local kind clusters): run against a
  fresh set of clusters (`make down && make up` if clusters already
  exist), confirm it completes without the old separate cert-manager/
  Kratix/MinIO steps and without requiring a follow-up `make infra`.
- **`make verify`:** extended checks (`cert-manager`/`kratix`/`minio`
  Kustomizations `Ready=True`) should pass on a healthy cluster and fail
  clearly if one is run against a cluster where the chain never
  reconciled.
- **Failure-path check (manual):** temporarily break one layer (e.g. a
  bad Kratix chart version in `clusters/platform/kratix/`), run `make
  infra`, and confirm the `kratix` Kustomization reports `Ready=False`
  and the `minio` Kustomization never attempts to apply (still
  `Ready=Unknown`/pending on its `dependsOn`).
- **`make infra` standalone still works:** after `make up`, hand-edit a
  file under `clusters/platform/` and run `make infra` alone to confirm
  the existing "edit, push, reconcile" local loop is unaffected.

## Open follow-ups (not built here)

- Pinning the Kratix Helm chart version in the new `HelmRelease` (see
  Non-goals) - currently floats to latest, matching today's behavior,
  but the repo pins `FLUX_VERSION` and cert-manager's chart version
  elsewhere for a documented reason (avoiding CLI/version drift); Kratix
  staying unpinned is an inconsistency worth revisiting separately. It's
  also a slightly different risk now than under the old imperative flow:
  under continuous Flux reconciliation (`interval: 1h`) an upstream
  republish of the `kratix` chart tag can now auto-upgrade the running
  controller unattended, whereas before it only re-resolved when someone
  ran `make up`/`kratix-platform` by hand.
- Swapping the `OCIRepository` source for a `GitRepository` against a
  real git remote, once one exists for this repo - already a one-object
  change per the existing `infra-apply` comments, unaffected by this
  design.
