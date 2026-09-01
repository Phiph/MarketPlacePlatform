# MarketPlacePlatform

A demo platform showing platform engineers how a handful of CNCF ecosystem
projects compose into a working, self-service internal developer platform.
Kubernetes, Helm, Flux, Argo CD, cert-manager, and Capsule handle the
platform's plumbing; [Kratix](https://kratix.io) is the piece that turns a
Promise (a service definition) into a self-service API for the rest. See
[CNCF ecosystem in this demo](#cncf-ecosystem-in-this-demo) for what each
project is doing and why.

**TL;DR:** install [Docker](https://www.docker.com/products/docker-desktop),
clone this repo, then:

```bash
make up   # first run: ~10-20 minutes, mostly image pulls and Helm waits
make dev  # once up finishes: broker + UI at http://localhost:5173
```

Works on macOS, Linux, and Windows-via-WSL2. Read on for details, or jump to
[Windows (WSL2)](#windows-wsl2) if that's you.

## CNCF ecosystem in this demo

No single project here is the platform - each one owns a specific piece, and
the demo's job is to show how they fit together.

| Project | CNCF status | Role in this demo |
|---|---|---|
| [Kubernetes](https://kubernetes.io) | Graduated | The substrate everything else runs on (via [kind](https://kind.sigs.k8s.io) for local dev) |
| [containerd](https://containerd.io) | Graduated | Container runtime inside the kind nodes |
| [Helm](https://helm.sh) | Graduated | Installs Kratix and cert-manager |
| [Flux](https://fluxcd.io) | Graduated | GitOps delivery of Promise workloads to Destinations, and of the platform cluster's own infra (see [Platform infra](#platform-infra-fluxgitops)) |
| [Argo CD](https://argo-cd.readthedocs.io) | Graduated (part of the Argo project) | Per-team, read-only application status/visibility (see [Marketplace broker API](#marketplace-broker-api)) |
| [cert-manager](https://cert-manager.io) | Graduated | Issues the TLS/webhook certs Kratix's Helm chart requires |
| [Capsule](https://projectcapsule.dev) | Sandbox | Enforces the multi-tenant namespace/RBAC boundary between business units and teams |
| [Kratix](https://kratix.io) | - | The Promise mechanism: turns a resource request into a pipeline run and a set of Kubernetes objects |

Kratix isn't itself a CNCF project (unlike the others above); it's included
because it's the piece that ties requests to pipelines - not because it's
the centerpiece of the demo.

## Running the demo locally

Prerequisites: [Docker](https://www.docker.com/products/docker-desktop), and
on macOS, [Homebrew](https://brew.sh). Everything else (`kind`, `kubectl`,
`helm`, `yq`, `k9s`, `flux`, the `kratix` CLI) is installed/fetched
automatically - nothing is vendored into this repo. `make up` also runs a
`make doctor` preflight first, checking Docker's resources, disk space, and
that the local registry port is free, so a misconfigured machine fails in
seconds with a clear message instead of partway through the run.

```bash
make up
```

First run takes roughly **10-20 minutes** - mostly Docker image pulls and
Helm install waits across two clusters - and prints a `[step/9]` line before
each stage so you can see where it is. Re-running `make up` afterward is fast
since every step is idempotent and skips what already exists.

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

Once `make up` finishes, point `kubectl` at either cluster:

```bash
kubectl config use-context kind-platform
kubectl config use-context kind-worker
```

Run `make verify` any time to confirm the demo is actually healthy - it
checks both cluster contexts respond, Kratix's pods are `Running`, both
Destinations are `Ready`, and the broker can build and serve a real catalog.

### Windows (WSL2)

This Makefile needs bash, and `kind`'s own Windows support runs through
[WSL2](https://learn.microsoft.com/windows/wsl/install) - there's no native
cmd.exe/PowerShell path. To run this on Windows:

1. Install WSL2 and a Linux distro (Ubuntu is the default and best-tested):
   `wsl --install`.
2. Install [Docker Desktop](https://www.docker.com/products/docker-desktop)
   on Windows and enable **Settings > Resources > WSL Integration** for your
   distro - Docker Desktop's daemon is shared into WSL2, so you don't install
   Docker a second time inside the distro itself.
3. Open a shell in your WSL2 distro (`wsl` from a terminal, or the distro's
   Start Menu entry) and clone/work from there - not from a Windows path
   (`/mnt/c/...`), which is slow and can trip up file-watching tools.
4. From that WSL2 shell, everything below is identical to the Linux
   instructions - `make deps`/`make up` detect the Linux userland (WSL2
   reports itself as `Linux`, same as a native install) and install
   prerequisites the same way.

## Platform infra (Flux/GitOps)

[`clusters/platform/`](clusters/platform) is the platform cluster's own infra - cert-manager,
Kratix, and MinIO - declared as layered Flux objects instead of the `kubectl apply`/`helm
upgrade --install` calls the Makefile used to run directly. It's reconciled by the same Flux
`flux-platform` installs (see "How this works" below) - not a second instance, and not the
same thing as the Flux `kratix-worker`/`kratix-platform-destination` use to deliver Promise
workloads to Destinations.

```bash
make infra   # push clusters/platform as an OCI artifact + point Flux at it
```

`infra-push` bundles the folder into the local registry (`oci://localhost:5001/platform-infra`)
rather than requiring a git remote, which keeps the loop local: edit a file under
`clusters/platform/`, `make infra`, and Flux reconciles the change in place - no push to
`origin` needed. `infra-apply` applies [`hack/kratix/platform-infra-source.yaml`](hack/kratix/platform-infra-source.yaml)
(the `OCIRepository` + three `Kustomization`s pointing at it) and patches the tag to whatever
was just pushed. Swapping the `OCIRepository` for a `GitRepository` against a real git remote
later is a one-object change - `clusters/platform/`'s contents don't need to move.

`make up` runs `infra` itself as part of bringing up a fresh cluster (see "How this works"
below) - there's no imperative cert-manager/Kratix/MinIO install to hand off from anymore.
`clusters/platform/` has three subfolders, each its own `Kustomization`, chained with
`dependsOn` so Flux only moves on to the next layer once the previous one is fully `Ready`:

1. `cert-manager/` - the `jetstack` Helm repo + a `cert-manager` `HelmRelease` (required by
   the Kratix chart's webhooks)
2. `kratix/` - depends on `cert-manager` - the `syntasso` Helm repo + a `kratix`
   `HelmRelease` (the same values the old `kratix-platform` Makefile target passed via
   `-f hack/kratix/platform-values.yaml`, now inlined directly into the `HelmRelease`)
3. `minio/` - depends on `kratix` - the dev-only MinIO manifest, which needs the
   `kratix-platform-system` Namespace and `default/minio-credentials` Secret the Kratix
   chart creates (Helm refuses to adopt resources it didn't create, so this really does have
   to come after, not just conventionally)

`make verify` checks all three `Kustomization`s report `Ready=True` as part of confirming the
platform came up healthy.

**Migrating an existing cluster:** a cluster created before this chain existed can't adopt it via `make infra` - Flux's Helm release naming (`<namespace>-<release>`, e.g. `default-kratix`) differs from the old imperative `helm upgrade --install kratix kratix`'s bare `kratix` release name, so Flux would try to install a second, colliding release rather than take over the first. Recreate the cluster instead: `make down && make up` (or `make restart`).

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
make up          # creates the clusters AND provisions the full demo below - idempotent
make broker-run  # starts the broker on :8878, talking to kind-platform
```

`make up`'s last step is `demo-setup`, which installs every Promise the demo needs
(business-unit - pulling in Capsule as a dependency - team, project, environment; database
comes from `promise-demo` earlier in `up`), submits a BusinessUnit + Team request per
`broker/config/teams.yaml` (`make broker-provision-teams`), and seeds an example
`checkout-service` Project with a `dev` Environment under it - so there's always something to
browse, not just an empty catalog. Each piece is still its own target if you want to run part
of this by hand (e.g. re-provisioning teams after editing `teams.yaml`):

```bash
make promise-demo                                                # installs the database Promise + example request
make promise-build promise-load PROMISE_DIR=promises/business-unit   # installs Capsule + the business-unit Promise
kubectl --context kind-platform apply -f promises/business-unit/promise.yaml
make promise-build promise-load PROMISE_DIR=promises/team        # installs the team Promise
kubectl --context kind-platform apply -f promises/team/promise.yaml
make broker-provision-teams  # submits a BusinessUnit + Team request per broker/config/teams.yaml
make promise-build promise-load PROMISE_DIR=promises/project     # installs the project Promise
kubectl --context kind-platform apply -f promises/project/promise.yaml
make promise-build promise-load PROMISE_DIR=promises/environment # installs the environment Promise
kubectl --context kind-platform apply -f promises/environment/promise.yaml
kubectl --context kind-platform apply -f promises/project/example-resource.yaml     # example Project
kubectl --context kind-platform apply -f promises/environment/example-resource.yaml # example Environment
```

**Multi-tenancy** is two levels: **business units** and **teams** within them. Each team gets
its own namespace (`team-<name>`) that its requests live in - one team can't see or touch
another's, *even another team in the same business unit* - and that boundary is enforced by
Kubernetes RBAC, not application code. Concretely: a business unit is a Capsule
(https://projectcapsule.dev) `Tenant` (provisioned via the `business-unit` Promise,
`promises/business-unit/`), with resource quotas but deliberately **no owners** - Capsule's
normal owner model grants every owner every namespace in the Tenant, which would let sibling
teams see each other. Team-level isolation instead comes from a single, shared
`GlobalTenantResource` (installed once, alongside Capsule) that binds each team's own
Kubernetes Group to only its own namespace (created by the `team` Promise,
`promises/team/`, referencing its business unit). Every broker call runs impersonating that
team's own Group (`broker/internal/k8sclient/impersonate.go`), so it's the Kubernetes API
server that actually stops Team A from touching Team B's resources, not a namespace string the
broker computed. See `promises/business-unit/README.md` for how that's wired up, including a
couple of non-obvious gotchas (Flux/Capsule apply-ordering - across *both* Promises, not just
within one - and RBAC aggregation) discovered getting it working end-to-end.

A team can further self-serve two more levels underneath its own namespace: **projects**
(`promises/project/`) and **environments** (`promises/environment/`, `dev`/`staging`/`prod`/...
- each its own namespace, `project-<team>-<project>-<environment>`). This needed no changes to the
RBAC mechanism above - an environment's namespace carries the exact same
`marketplace.kratix.io/team` label a team's own namespace does, so the same
`GlobalTenantResource` grants access to it identically. See
`promises/environment/README.md` for the one place this layer *does* need broker-side care
(composing `spec.team`/`spec.businessUnit` itself rather than trusting the request body).

That same per-team boundary extends to Argo CD, ahead of a future per-request logs endpoint:
`promises/team`'s pipeline (`promises/team/README.md`) writes a read-only Argo CD `AppProject`
per team, alongside the `Namespace` it already writes, with a `viewer` role scoped to
`applications, get` and `logs, get` only - no `sync`/`delete` - and empty
`namespaceResourceWhitelist`/`clusterResourceWhitelist` as defense-in-depth, since Argo never
applies anything in this design. `demo-setup` (and so `make up`) runs a new `argo-provision-teams`
target right after `broker-provision-teams` to mint one Argo CD API token per team, scoped to
that team's own `AppProject`/`viewer` role, and stores it as a Secret (`argocd-team-token`, key
`token`) in the team's own namespace (`team-<name>`) - never a single shared credential, so a
broker routing bug can't leak another team's status/logs, the same reasoning that keeps the
Kubernetes-API boundary above per-team rather than broker-enforced. See
`docs/superpowers/specs/2026-08-14-container-workload-logs-design.md`'s "RBAC" section for the
full design, including why the `AppProject` lives in Argo's own `argocd` namespace rather than
the team's own.

Auth (API-key -> team) is still a **static demo-only** mapping
(`broker/config/teams.yaml`; ships with `payments` / `demo-key-payments`
and `checkout` / `demo-key-checkout`) - there's no real authn (no OIDC/
SSO), which is fine for a local kind cluster but is the first thing to
replace before this becomes anything but a demo. That's a separate concern
from the namespace/RBAC boundary above: a broker bug in resolving API
key->team would still only ever grant access to *some* team's resources, not
an arbitrary one.

**Endpoints** (all under `/api`, all requiring `Authorization: Bearer <key>`
except `/healthz`):

| Method | Path | What |
|---|---|---|
| GET | `/healthz` | Liveness, no auth |
| GET | `/api/promises` | List catalog-visible Promises (add `?all=true` to see hidden ones too) |
| GET | `/api/promises/{name}` | One Promise's full entry, including its request schema |
| GET | `/api/promises/{name}/versions` | List every known Promise revision for this Promise: `[{"version", "latest", "createdAt"}, ...]` |
| POST | `/api/promises/{name}/requests` | Submit a request in the caller's own `team-<name>` namespace: `{"name": "...", "spec": {...}}` |
| GET | `/api/promises/{name}/requests` | List the calling team's requests against this Promise |
| GET | `/api/promises/{name}/requests/{reqName}` | One request's current status |
| GET | `/api/promises/{name}/requests/{reqName}/version` | The request's current bound version: `{"boundVersion", "latestVersion", "upgradeAvailable"}` |
| POST | `/api/promises/{name}/requests/{reqName}/version` | Move the request to a different Promise revision (upgrade or rollback): `{"version": "..."}` |
| DELETE | `/api/promises/{name}/requests/{reqName}` | Delete a request |
| POST | `/api/environments` | Create an Environment under one of the caller's Projects: `{"name": "...", "project": "..."}` - see "Projects and Environments" below |
| POST | `/api/projects/{project}/environments/{environment}/promises/{name}/requests` | Same as the flat submit route above, but scoped into that project/environment's namespace instead |
| GET | `/api/projects/{project}/environments/{environment}/promises/{name}/requests` | Scoped equivalent of the flat list route |
| GET | `/api/projects/{project}/environments/{environment}/promises/{name}/requests/{reqName}` | Scoped equivalent of the flat get route |
| DELETE | `/api/projects/{project}/environments/{environment}/promises/{name}/requests/{reqName}` | Scoped equivalent of the flat delete route |

```bash
curl -H "Authorization: Bearer demo-key-payments" localhost:8878/api/promises

curl -X POST -H "Authorization: Bearer demo-key-payments" \
  -d '{"name":"my-db","spec":{"size":"1Gi"}}' \
  localhost:8878/api/promises/database/requests

curl -H "Authorization: Bearer demo-key-payments" localhost:8878/api/promises/database/requests

curl -H "Authorization: Bearer demo-key-payments" localhost:8878/api/promises/database/versions

curl -X POST -H "Authorization: Bearer demo-key-payments" \
  -d '{"version":"v0.2.0"}' \
  localhost:8878/api/promises/database/requests/my-db/version
```

### Projects and Environments

A `Project` (`promises/project/`) is just a name a team's Environments group under - create one
through the generic routes above, same as any other Promise request:

```bash
curl -X POST -H "Authorization: Bearer demo-key-checkout" \
  -d '{"name":"checkout-service","spec":{"description":"Checkout services"}}' \
  localhost:8878/api/promises/project/requests
```

An `Environment` (`promises/environment/`) needs its own dedicated endpoint instead, since
creating one has to set which team owns the resulting namespace - see
`promises/environment/README.md` for why that can't just be another field in the request body:

```bash
curl -X POST -H "Authorization: Bearer demo-key-checkout" \
  -d '{"name":"dev","project":"checkout-service"}' \
  localhost:8878/api/environments
```

Once it exists, submit requests into it with the scoped routes instead of the flat ones:

```bash
curl -X POST -H "Authorization: Bearer demo-key-checkout" \
  -d '{"name":"my-db","spec":{"size":"1Gi"}}' \
  localhost:8878/api/projects/checkout-service/environments/dev/promises/database/requests
```

That request lands in namespace `project-checkout-checkout-service-dev`, not `team-checkout` -
the flat routes remain the default for everything that doesn't opt into a project/environment.
Team is part of the namespace name, not just project/environment - see
`promises/environment/README.md`, "Why team is part of the namespace name", for why: without
it, two teams picking an identical project+environment name would collide on one real
namespace.

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
self-served through the generic catalog. This covers every Promise that
exposes a `spec.api` CRD (which every Promise in this repo does, and which
the catalog mechanism already requires to render a request schema) - a
dependency-only Promise with no `spec.api` isn't parsed into the catalog at
all, so it has no queryable evidence either.
`broker/internal/catalog/evidence_lint_test.go` fails `make broker-test` if
any checked-in Promise regresses on this.

## Marketplace UI

`ui/` is a [shadcn/ui](https://ui.shadcn.com) frontend (Vite + React +
TypeScript) for the broker: sign in with a team's API key, browse the
catalog, submit requests through a form generated from each Promise's
schema, and track their status. A **Projects** page lets a team manage its
own Projects and Environments, and the request form on each service's page
gains a target selector to submit into one of them instead of the team's
flat default namespace.

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

For a walkthrough of the end-to-end flow a team follows in the UI to request
a service - sign in, browse, target a namespace or a project/environment,
fill the schema-generated form, track status - see
[docs/requesting-a-service.md](docs/requesting-a-service.md).

## Other targets

```bash
make doctor              # preflight check - Docker resources, disk space, port conflicts
make verify              # confirm `make up` came up healthy (contexts, pods, Destinations, broker catalog)
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
make logs-flux-platform  # tail Flux on the platform cluster (the platform-cluster Destination, and where the cert-manager/kratix/minio infra chain reconciles - check here first if make up fails during "Reconciling platform infra")
make k9s-platform        # k9s on the platform cluster
make k9s-worker          # k9s on the worker cluster
make argo-ui              # port-forward the Argo CD UI to http://localhost:8080
make argo-admin-password  # print the Argo CD initial admin password
make restart             # delete and recreate both clusters from scratch
make down                # delete the clusters, keep the local registry
make destroy             # delete the clusters and the local registry
make help                # list all targets, grouped
```

## How this works

Nothing is cloned or vendored - `make up` runs `doctor` (the preflight checks
described above), then `deps` (installs whatever's missing for your OS), then
`registry-start`, then, in order:

1. `clusters` - two kind clusters (`hack/kind/{platform,worker}-config.yaml`: upstream
   [Kratix](https://github.com/syntasso/kratix)'s port-mappings plus a
   `containerdConfigPatches` block for the local registry)
2. `registry-configure` - wires `kind-registry` into both clusters' containerd
   ([kind's documented local-registry pattern](https://kind.sigs.k8s.io/docs/user/local-registry/))
3. `flux-platform` - installs Flux on the platform cluster (pinned to `FLUX_VERSION` - see the
   Makefile comment on that var for why not literal-latest), ahead of everything it's about
   to reconcile
4. `infra` - pushes [`clusters/platform/`](clusters/platform) as an OCI artifact to the local
   registry and points the just-installed Flux at it, then `make up` waits for the `minio`
   `Kustomization` to report Ready. This is what actually installs cert-manager, Kratix, and
   MinIO - see "Platform infra" above for the three-layer `dependsOn` chain. Kratix is
   configured (inlined into `clusters/platform/kratix/kratix-release.yaml`) to point at MinIO
   (not running yet - nothing needs it live until a Promise pipeline actually writes to it)
   and pre-register both `worker-1` and `platform-cluster` as `Destination`s.
5. `kratix-worker` - installs Flux on the worker cluster (`flux-worker`, pinned to
   `FLUX_VERSION`), then registers the worker cluster as a Destination via the companion
   `syntasso/kratix-destination` chart (`installFlux=false`, since Flux is already there),
   pointed at the platform's MinIO over the kind docker network (the two clusters are
   separate Docker containers, so this uses the platform node's container IP, not a
   Kubernetes Service DNS name)
6. `kratix-platform-destination` - same idea (`installFlux=false` `kratix-destination` chart;
   `flux-platform` is already installed by step 3, so this is idempotent), this time on the
   platform cluster itself, pointed at its own in-cluster MinIO Service (no docker-network IP
   needed) - this is what registers `kind-platform` itself as the `platform-cluster`
   Destination
7. `metrics-server` on both clusters

The `kratix-destination` chart is marked deprecated upstream (still published and
functional, just not where Syntasso is investing further) - it's used here for the
Destination-registration/Bucket/Kustomization wiring, which is far less bespoke code than
reimplementing that by hand, but *not* for installing Flux itself (`installFlux=false`):
Flux is installed explicitly by `flux-worker`/`flux-platform` so its version is under our
control rather than whatever the chart last pinned.

The `kratix` CLI (fetched to `bin/kratix`, git-ignored) is what scaffolds new
Promises - `promise-build`/`promise-load`/`promise-demo` are generic over
`PROMISE_DIR` (default `promises/database`), so they work for anything you
scaffold next, not just the checked-in example.
