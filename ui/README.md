# Marketplace UI

A [shadcn/ui](https://ui.shadcn.com)-based frontend for the [marketplace broker](../broker):
browse the catalog of Promises published by the platform team, submit requests
against them through a form generated from each Promise's JSON Schema, and
track the status of what you've requested.

Vite + React + TypeScript + Tailwind, client-side only - it talks directly to
the broker's `/api` over `fetch`, authenticating with the same per-team
bearer-token API key described in the [top-level README](../README.md#marketplace-broker-api).

## Running against the real broker

```bash
npm install
cp .env.example .env.local   # point VITE_BROKER_URL at the broker, if not localhost:8080
npm run dev
```

This assumes `make up` and `make broker-run` are already running (see the
top-level README). Sign in with one of the demo teams shown on the login
screen (`team-payments` / `demo-key-payments` or `team-checkout` /
`demo-key-checkout`), or your own team's key from `broker/config/teams.yaml`.

## Running against the mock broker

The real broker needs a live kind cluster. To iterate on the UI without one,
`mock/server.mjs` is a dependency-free Node script that speaks the same HTTP
contract (same routes, same auth header, same JSON shapes) backed by an
in-memory store instead of Kubernetes:

```bash
npm run mock-broker   # starts on :8080
npm run dev            # in another terminal
```

Newly submitted requests sit "Pending" for ~6s and then flip to "Ready" (or
"Failed", if the request name contains "fail") so you can see the UI's
status polling do something. This script is dev-only - it's not part of the
production build and doesn't touch Kubernetes.

## Structure

- `src/lib/api.ts` - typed fetch client for the broker's `/api/*` routes
- `src/lib/auth.tsx`, `src/lib/theme.tsx` - localStorage-backed session and
  light/dark theme context
- `src/components/SchemaForm.tsx` - renders a form from a Promise's `spec`
  JSON Schema (strings, enums, numbers, booleans, one level of nested
  objects; arrays and deeper nesting fall back to a raw-JSON field)
- `src/pages/` - Login, Catalog, Service detail (request form + this team's
  requests for that service), and an aggregated "My Requests" view across
  every service

## Other commands

```bash
npm run build   # type-check + production build
npm run lint    # oxlint
npm run preview # serve the production build locally
```
