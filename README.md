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

## Other targets

```bash
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
4. `minio` - a small, vendored, dev-only MinIO manifest (`hack/kind/minio-install.yaml`,
   copied from Kratix's own `config/samples/minio-install.yaml` since it's a handful of
   lines, not something worth fetching from a mutable branch on every run)
5. `kratix-platform` - installs Kratix itself via its published Helm chart
   (`helm install kratix syntasso/kratix`, repo `https://syntasso.github.io/helm-charts`),
   configured (`hack/kratix/platform-values.yaml`) to point at the MinIO above and
   pre-register both `worker-1` and `platform-cluster` as `Destination`s
6. `kratix-worker` - registers the worker cluster via the companion
   `syntasso/kratix-destination` chart, which installs Flux and points it at the
   platform's MinIO (over the kind docker network - the two clusters are separate
   Docker containers, so this uses the platform node's container IP, not a Kubernetes
   Service DNS name)
7. `kratix-platform-destination` - the same `kratix-destination` chart again, this
   time installed *on* the platform cluster, pointed at its own in-cluster MinIO
   Service (no docker-network IP needed, since it's all one cluster) - this is what
   registers `kind-platform` itself as the `platform-cluster` Destination
8. `metrics-server` on both clusters

The `kratix-destination` chart is marked deprecated upstream (still published and
functional, just not where Syntasso is investing further) - it's used here because it's
far less bespoke code than reimplementing Flux + Bucket/Kustomization wiring by hand. If
that chart is ever pulled, that wiring would need to be hand-rolled instead.

The `kratix` CLI (fetched to `bin/kratix`, git-ignored) is what scaffolds new
Promises - `promise-build`/`promise-load`/`promise-demo` are generic over
`PROMISE_DIR` (default `promises/database`), so they work for anything you
scaffold next, not just the checked-in example.
