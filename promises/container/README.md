# `container` Promise

Runs a single container image as a Kubernetes `Deployment`, with an optional
`Service` when `spec.port` is set - the low-level workload primitive for
this marketplace. See
`docs/superpowers/specs/2026-08-14-container-promise-design.md` for the
full design, including why the API stays low-level (a future `Service`
compound Promise will bundle this with `database` behind a simpler API)
and the current worker-cluster tenancy limitation (next paragraph).

Marked `marketplace.kratix.io/visible: "true"`: self-served through the
catalog, like `database`.

**Operational evidence** (see the root `README.md`'s "Marketplace metadata
convention"): owner `platform-team`, lifecycle `experimental`, support
`#platform-eng`, policy `internal`.

- `promise.yaml` - the `Container` CRD (`demo.kratix.io/v1alpha1`), no
  promise-level dependency workflow (unlike `database`'s Postgres operator)
  - this Promise targets native `Deployment`/`Service` objects directly.
- `workflows/resource/configure/container-configure` - a Python pipeline
  that runs per-request, reading `spec.image`/`replicas`/`cpu`/`memory`/
  `port`/`env` and writing a `Deployment` manifest (plus a `Service`
  manifest when `spec.port` is set).
- `example-resource.yaml` - a sample `Container` request, `nginx:1.27`
  with a `port` and one `env` var set, so both the `Deployment` and
  `Service` code paths get exercised.

**Known limitation:** the `Deployment`/`Service` this pipeline writes
always land in the `default` namespace on the worker cluster
(`kind-worker`), regardless of which namespace the `Container` request
itself lives in on the platform cluster - the worker cluster has no
per-team/environment namespaces yet. Matches `database`'s existing
precedent; see the design doc's "Known limitation" section for why this
isn't fixed here. It also means no cpu/memory ceiling is enforced on this workload today -
see the design doc's "Known limitation" section for why.

## Try it

```bash
make promise-build promise-load PROMISE_DIR=promises/container
kubectl --context kind-platform apply -f promises/container/promise.yaml
kubectl --context kind-platform apply -f promises/container/example-resource.yaml
```

Then:

```bash
kubectl --context kind-platform get containers.demo.kratix.io example-container -w
kubectl --context kind-worker get deployments,services,pods -l app=example-container
```

## Iterating

After editing
`workflows/resource/configure/container-configure/kratix-guide-container-resource-pipeline/scripts/pipeline.py`:

```bash
make promise-build promise-load PROMISE_DIR=promises/container
kubectl --context kind-platform delete container example-container
kubectl --context kind-platform apply -f promises/container/example-resource.yaml
```
