# `database` Promise

A minimal, working Promise to learn the shape of Kratix Promises from — it
reproduces [Kratix's "Writing your first Promise" tutorial](https://docs.kratix.io/main/guides/writing-a-promise)
and was generated with the `kratix` CLI (`make cli` fetches it to `bin/kratix`).

Requesting a `Database` gets you a [Zalando Postgres Operator](https://github.com/zalando/postgres-operator)
`postgresql` custom resource, sized from `spec.size` on the request.

- `promise.yaml` - the `Database` CRD (`demo.kratix.io/v1alpha1`) plus two workflows:
  - **promise workflow** (`workflows/promise/configure/dependencies/configure-deps`) - installs the
    Postgres operator and its CRDs as static dependencies, once, when the Promise itself is installed
  - **resource workflow** (`workflows/resource/configure/database-configure`) - a Python pipeline that
    runs per-request, reading `spec.size` off the `Database` object and writing out a `postgresql` manifest
- `example-resource.yaml` - a sample `Database` request

## Try it

```bash
make promise-demo
```

This builds both pipeline images, `kind load`s them into the platform cluster (pipelines always run
there, regardless of which cluster the resulting workload lands on), installs the Promise, and requests
`example-resource.yaml`. Then:

```bash
kubectl --context kind-platform get databases.demo.kratix.io example-database -w
kubectl --context kind-worker get postgresqls
```

## Iterating

After editing `workflows/resource/configure/database-configure/kratix-guide-database-resource-pipeline/scripts/pipeline.py`:

```bash
make promise-build promise-load
kubectl --context kind-platform delete database example-database
kubectl --context kind-platform apply -f example-resource.yaml
```

## Starting your own Promise

```bash
bin/kratix init promise <name> --group <your-group> --kind <Kind>
bin/kratix update api --property <field>:<type>
bin/kratix add container resource/configure/<step-name> --image <name>:<tag> --language python
```

`make promise-build`, `make promise-load`, and `make promise-demo` all take a `PROMISE_DIR=` override,
so they work against whatever you scaffold next too - not just this example.
