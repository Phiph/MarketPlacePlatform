# MarketPlacePlatform

A demo platform showing platform engineers how to build their own enterprise
internal developer platform, built on [Kratix](https://kratix.io).

## Running Kratix locally

Prerequisites: [Docker](https://www.docker.com/products/docker-desktop) and
[Homebrew](https://brew.sh). Everything else (`kind`, `kubectl`, `helm`, `yq`,
`k9s`, the `kratix` CLI) is installed/fetched automatically - nothing is
vendored into this repo.

```bash
make up
```

This creates two local [kind](https://kind.sigs.k8s.io) clusters and the
supporting dev tooling:

- **`kind-platform`** - runs the Kratix controller and a MinIO state store, and is
  *also* registered as a Destination (`platform-cluster`) - some Promises (e.g.
  compound Promises) require the platform cluster to be a valid Destination
- **`kind-worker`** - runs Flux, registered with Kratix as a `Destination` named
  `worker-1`; this is where scheduled workloads normally land

`platform-cluster` has `strictMatchLabels: true` and its own `environment: platform`
label, so it's opt-in only - Promises with no `destinationSelectors` (like the
`database` starter) keep landing on `worker-1` alone, same as before. A Promise
has to explicitly select `environment: platform` to land on the platform cluster.
- **a local image registry** (`localhost:5001`, wired into both clusters'
  containerd) - for images you build locally and want to run in either
  cluster without pushing anywhere
- **metrics-server** on both clusters, for `make top`

The first run takes a few minutes while images pull and Flux reconciles.
`make up` is idempotent - re-running it skips clusters that already exist.

Once `make up` finishes, point `kubectl` at either cluster:

```bash
kubectl config use-context kind-platform
kubectl config use-context kind-worker
```

## Platform infra (Flux/GitOps)

[`clusters/platform/`](clusters/platform) is the platform cluster's own infra, declared
as Flux objects (`HelmRepository`/`HelmRelease` for cert-manager, so far) instead of the
`kubectl apply`/`helm upgrade --install` calls the Makefile used to run directly. It's
reconciled by the same Flux `flux-platform` installs (see "How this works" below) - not a
second instance, and not the same thing as the Flux `kratix-worker`/`kratix-platform-destination`
use to deliver Promise workloads to Destinations.

```bash
make infra   # push clusters/platform as an OCI artifact + point Flux at it
```

`infra-push` bundles the folder into the local registry (`oci://localhost:5001/platform-infra`)
rather than requiring a git remote, which keeps the loop local: edit a file under
`clusters/platform/`, `make infra`, and Flux reconciles the change in place - no push to
`origin` needed. `infra-apply` applies [`hack/kratix/platform-infra-source.yaml`](hack/kratix/platform-infra-source.yaml)
(the `OCIRepository` + `Kustomization` pointing at it) and patches the tag to whatever was
just pushed. Swapping the `OCIRepository` for a `GitRepository` against a real git remote
later is a one-object change - `clusters/platform/`'s contents don't need to move.

This is additive, not yet wired into `make up`: cert-manager is still installed
imperatively by the `cert-manager` target before Flux exists on the platform cluster (Flux
itself is installed later, by `kratix-platform-destination`'s `flux-platform` prerequisite),
so `make infra` is something you run *after* `make up` to have Flux take over an
already-running cert-manager. Folding this into `up` so a fresh cluster is GitOps-managed
from the start would mean bootstrapping Flux before cert-manager instead of after - not
done here yet.

**Running `make infra` against a cluster that still has the imperative cert-manager doesn't
cleanly replace it** - the upstream release manifest and the Helm chart name their
resources differently (`cert-manager-*` vs `cert-manager-cert-manager-*`), so Helm doesn't
see a conflict and you end up with two full cert-manager installs (two sets of webhooks)
running side by side. To hand off cleanly, remove the imperative one first - but *not* via
`kubectl delete -f <the release manifest>`: that manifest also owns the `cert-manager`
Namespace object, so deleting it deletes the whole namespace, including whatever Flux
already put there. Delete the old Deployments/Services/webhook configs by name instead, or
accept the brief gap and let the `HelmRelease`'s `install.createNamespace: true` recreate
the namespace (`kubectl annotate helmrelease cert-manager -n flux-system
reconcile.fluxcd.io/requestedAt="$(date -u +%Y-%m-%dT%H:%M:%SZ)" --overwrite` to force an
immediate retry rather than waiting for the next interval).

## Building a Promise

```bash
make promise-demo
```

Builds, loads, and installs the [starter `database` Promise](promises/database),
then submits its example request - see [promises/database/README.md](promises/database/README.md)
for what it does and how to iterate on it, and for how to scaffold your own
Promise from scratch with `bin/kratix init promise`.

**Two different image loops, for two different things:**
- **Promise pipeline images** (the containers Kratix runs to fulfil a request) always run on the
  platform cluster, so `make promise-build` + `make promise-load` (`kind load docker-image`) is enough -
  no registry needed. This is what `make promise-demo` uses.
- **Workload images** (something a Promise deploys, e.g. a demo app) may need to run on the worker
  cluster. For those, use the local registry: `docker build -t localhost:5001/<name>:<tag> .`, then
  `docker push localhost:5001/<name>:<tag>`, and reference that same tag in the manifest your pipeline emits.

## Marketplace broker API

Kubernetes/Kratix is the supply side - Promises, and the CRDs/workflows they
define. The **broker** (`broker/`, a small Go service) is the beginning of
the demand side: a multi-tenant HTTP facade in front of the platform cluster,
so a future marketplace UI (or anything else) has a friendly contract to
build against instead of talking to `kubectl`/the Kubernetes API directly.

```bash
make up            # if you haven't already
make promise-demo   # installs the database Promise, so there's something to browse
make broker-run      # starts the broker on :8878, talking to kind-platform
```

**Multi-tenancy**: callers are "teams". Each team gets its own namespace
(`team-<name>`) that its requests live in - one team can't see or touch
another's. Auth is a **static demo-only** API-key -> team mapping
(`broker/config/teams.yaml`; ships with `team-payments` / `demo-key-payments`
and `team-checkout` / `demo-key-checkout`) - there's no real authn (no OIDC/
SSO), which is fine for a local kind cluster but is the first thing to
replace before this becomes anything but a demo.

**Endpoints** (all under `/api`, all requiring `Authorization: Bearer <key>`
except `/healthz`):

| Method | Path | What |
|---|---|---|
| GET | `/healthz` | Liveness, no auth |
| GET | `/api/promises` | List catalog-visible Promises (add `?all=true` to see hidden ones too) |
| GET | `/api/promises/{name}` | One Promise's full entry, including its request schema |
| POST | `/api/promises/{name}/requests` | Submit a request: `{"name": "...", "spec": {...}}` |
| GET | `/api/promises/{name}/requests` | List the calling team's requests against this Promise |
| GET | `/api/promises/{name}/requests/{reqName}` | One request's current status |
| DELETE | `/api/promises/{name}/requests/{reqName}` | Delete a request |

```bash
curl -H "Authorization: Bearer demo-key-payments" localhost:8878/api/promises

curl -X POST -H "Authorization: Bearer demo-key-payments" \
  -d '{"name":"my-db","spec":{"size":"1Gi"}}' \
  localhost:8878/api/promises/database/requests

curl -H "Authorization: Bearer demo-key-payments" localhost:8878/api/promises/database/requests
```

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

## Marketplace UI

`ui/` is a [shadcn/ui](https://ui.shadcn.com) frontend (Vite + React +
TypeScript) for the broker: sign in with a team's API key, browse the
catalog, submit requests through a form generated from each Promise's
schema, and track their status.

```bash
make ui-install   # once
make dev           # runs the broker and the UI dev server together, opens on localhost:5173
```

The UI dev server proxies `/api` straight to the broker, so there's no `.env`
to set up and no CORS to think about - `make dev` just works. Run
`make broker-run` and `make ui-dev` in separate terminals instead if you want
their output apart (e.g. to restart one without the other).

No live cluster? `make ui-mock` runs a dependency-free mock of the broker
(same routes, same auth, an in-memory store) so the UI can be developed and
demoed on its own. See [ui/README.md](ui/README.md) for details.

## Other targets

```bash
make dev                 # run the broker + UI dev server together
make broker-run          # run the marketplace broker API against kind-platform
make broker-build        # build the broker binary (bin/broker)
make broker-test         # run the broker's Go tests
make ui-dev               # run the marketplace UI dev server alone
make ui-mock               # run the UI's mock broker (no cluster needed)
make status             # pod/destination health on both clusters
make top                 # CPU/memory per pod on both clusters
make logs-platform       # tail the Kratix controller
make logs-flux-worker    # tail Flux on the worker cluster
make logs-flux-platform  # tail Flux on the platform cluster (the platform-cluster Destination)
make k9s-platform        # k9s on the platform cluster
make k9s-worker          # k9s on the worker cluster
make restart             # delete and recreate both clusters from scratch
make down                # delete the clusters, keep the local registry
make destroy             # delete the clusters and the local registry
make help                # list all targets, grouped
```

## How this works

Nothing is cloned or vendored - `make up` runs, in order:

1. `clusters` - two kind clusters (`hack/kind/{platform,worker}-config.yaml`: upstream
   [Kratix](https://github.com/syntasso/kratix)'s port-mappings plus a
   `containerdConfigPatches` block for the local registry)
2. `registry-configure` - wires `kind-registry` into both clusters' containerd
   ([kind's documented local-registry pattern](https://kind.sigs.k8s.io/docs/user/local-registry/))
3. `cert-manager` - the upstream release manifest (required by the Kratix chart's webhooks)
4. `kratix-platform` - installs Kratix itself via its published Helm chart
   (`helm install kratix syntasso/kratix`, repo `https://syntasso.github.io/helm-charts`),
   configured (`hack/kratix/platform-values.yaml`) to point at MinIO (not running yet -
   nothing needs it live until a Promise pipeline actually writes to it) and pre-register
   both `worker-1` and `platform-cluster` as `Destination`s. This is also what creates the
   `kratix-platform-system` namespace and the `default/minio-credentials` Secret (via
   `additionalResources` in the values file) - the Helm chart is the one source of truth
   for both, which is why `minio` (next) has to run after it, not before: Helm refuses to
   adopt a namespace or Secret that already exists without its ownership metadata.
5. `minio` - a small, vendored, dev-only MinIO manifest (`hack/kind/minio-install.yaml`,
   copied from Kratix's own `config/samples/minio-install.yaml` since it's a handful of
   lines, not something worth fetching from a mutable branch on every run) - deploys into
   the namespace `kratix-platform` created, using the Secret it created
6. `kratix-worker` - installs Flux on the worker cluster (`flux-worker`, pinned to
   `FLUX_VERSION` - see the Makefile comment on that var for why not literal-latest), then
   registers the worker cluster as a Destination via the companion
   `syntasso/kratix-destination` chart (`installFlux=false`, since Flux is already there),
   pointed at the platform's MinIO over the kind docker network (the two clusters are
   separate Docker containers, so this uses the platform node's container IP, not a
   Kubernetes Service DNS name)
7. `kratix-platform-destination` - same idea (`flux-platform` + the `kratix-destination`
   chart with `installFlux=false`), this time on the platform cluster itself, pointed at
   its own in-cluster MinIO Service (no docker-network IP needed) - this is what registers
   `kind-platform` itself as the `platform-cluster` Destination
8. `metrics-server` on both clusters

The `kratix-destination` chart is marked deprecated upstream (still published and
functional, just not where Syntasso is investing further) - it's used here for the
Destination-registration/Bucket/Kustomization wiring, which is far less bespoke code than
reimplementing that by hand, but *not* for installing Flux itself (`installFlux=false`):
Flux is installed explicitly by `flux-worker`/`flux-platform` so its version is under our
control rather than whatever the chart last pinned. See the ["target folder as a Flux
target"](#platform-infra-fluxgitops) section below for how the platform cluster's own
infra (starting with cert-manager) is reconciled through that same Flux instance.

The `kratix` CLI (fetched to `bin/kratix`, git-ignored) is what scaffolds new
Promises - `promise-build`/`promise-load`/`promise-demo` are generic over
`PROMISE_DIR` (default `promises/database`), so they work for anything you
scaffold next, not just the checked-in example.
