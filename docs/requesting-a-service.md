# Requesting a service: the team user flow

This walks through the flow a team member follows in the [Marketplace
UI](../ui/) to browse the catalog and request a service, end to end. It's the
UI-level counterpart to the [broker API's endpoint table](../README.md#marketplace-broker-api)
in the top-level README - read that instead if you're integrating against
the API directly rather than clicking through the UI.

Verified locally against `make ui-mock` (`ui/mock/server.mjs`) + `npm run
dev`, which speaks the same HTTP contract as the real broker without needing
a live kind cluster.

## 1. Sign in

The UI's login screen (`/login`) asks for a **team name** and its **API
key** - the same static per-team bearer token described in
[`broker/config/teams.yaml`](../broker/config/teams.yaml). There's no real
authn behind this (no OIDC/SSO); it's a demo-only stand-in, called out
directly on the login screen itself ("Backed by the marketplace broker · not
real authentication").

For local dev, two demo teams are one click away via the buttons under
"demo teams": `payments` (`demo-key-payments`) and `checkout`
(`demo-key-checkout`). The session (team + key) persists in `localStorage`,
so reloading the page keeps you signed in until you explicitly sign out from
the account menu (top right).

## 2. Browse the catalog

Signing in lands on `/catalog` - the **Service Catalog**, one card per
Promise the platform team has published with `marketplace.kratix.io/visible:
"true"` (see the [metadata convention](../README.md#marketplace-metadata-convention)).
Each card shows the Promise's display name, its Kind, a short description,
and the CRD's API version. A search box filters by name/description for
catalogs with more than a couple of entries.

Clicking a card's "View" goes to that service's detail page
(`/catalog/{promise-name}`), which is where a request actually gets
submitted.

## 3. Choose a target

Every service detail page opens with a **Target** selector, defaulting to
"Team default (`{team}`)" - the team's own flat namespace
(`team-{team}`). If the team has created [Projects and
Environments](../README.md#projects-and-environments) (via the separate
**Projects** page), each `project / environment` pair also appears as a
target option. Switching targets changes only *where the request lands*
(the namespace), shown live as "Requests land in `{namespace}`" - the form
below and the request itself are identical either way.

This matters because project/environment namespaces are isolated the same
way a team's own namespace is (same Capsule `GlobalTenantResource`, same
`marketplace.kratix.io/team` label) - it's how one team keeps `dev` and
`staging` instances of the same service from colliding, or groups a
service's environments under a project without asking the platform team for
anything new.

## 4. Fill in the request form

The **New request** tab renders a form generated directly from the
Promise's CRD JSON Schema
([`src/components/SchemaForm.tsx`](../ui/src/components/SchemaForm.tsx)):

- **Request name** - a free-text field (lowercase, alphanumeric, dashes),
  becomes the resource's `metadata.name`.
- One field per schema property, typed to match: strings, `enum`s render as
  a select, numbers, booleans render as a switch, and one level of nested
  object properties. Required fields carry a red asterisk. Anything deeper
  (arrays, multiply-nested objects) falls back to a raw-JSON textarea rather
  than silently dropping the field.

Field help text comes from each property's schema `description`, so what
the form asks for should match what the Promise author documented, not a
hand-written form label that can drift from the schema over time.

## 5. Submit and track status

**Submit request** does a `POST` (or, for the scoped-target case, the
scoped-route equivalent) and immediately shows the new request under the
**Your requests** tab on the same page, with a toast confirming the
submission. From there:

- **Status** starts `Pending` and polls automatically
  ([`src/lib/use-polling.ts`](../ui/src/lib/use-polling.ts)) until the
  underlying resource reports `Ready` (or `Failed`) - no manual refresh
  needed.
- The eye icon opens a detail dialog with the full submitted `spec` and the
  resource's raw `status.conditions`, useful for seeing exactly what the
  Promise's pipeline reported without leaving the UI.
- The pencil icon re-opens the form pre-filled with the current spec for
  editing - submitting replaces the spec wholesale (not a merge), matching
  how [`resourceapi.Update`](../broker/internal/resourceapi/resource.go)
  works server-side.
- The tag icon shows/moves the request's bound Promise revision if the
  catalog has more than one (see `GET .../version` in the API table) -
  useful for upgrading a request pinned to an older schema once a newer one
  is available.
- The trash icon deletes the request.

## 6. Everything in one place: My Requests

`/requests` aggregates every request the signed-in team has made, across
every service and every project/environment target, in one table -
useful once a team has requested several different kinds of service and
doesn't want to check each service's own page individually. Same status
polling and row actions as the per-service view.

## Summary

```
Sign in (team + API key)
  → Catalog (browse visible Promises)
    → Service detail page
      → pick a Target (team default, or a project/environment)
      → fill the schema-generated form
      → Submit request
        → status polls Pending → Ready/Failed
        → view / edit / move version / delete from either the service page or My Requests
```
