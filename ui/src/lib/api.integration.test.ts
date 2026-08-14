import { describe, expect, it } from 'vitest'
import { api, ApiError } from './api'

// Runs against the real broker binary (see vitest.globalSetup.ts and
// broker/cmd/broker/main.go's BROKER_FAKE_K8S mode) instead of a mocked
// fetch: a hand-rolled mock only proves it matches what we told it to
// return, not what the broker actually returns. This exercises real HTTP
// status codes, JSON shapes, and error messages produced by the same
// handler code the production broker runs (backed by an in-memory fake
// Kubernetes API instead of a real cluster, so no kind cluster is needed).
const API_KEY = 'demo-key-payments' // team "payments", per broker/config/teams.yaml

// Same base-URL construction as api.ts's request() - see vitest.globalSetup.ts
// (fixed port, matches .env.test's VITE_BROKER_URL) and BROKER_CORS_ORIGIN=''
// so no browser-only preflight is involved here.
const BASE_URL = `${import.meta.env.VITE_BROKER_URL ?? ''}/api`

function uniqueName() {
  return `ui-it-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`
}

// Raw fetch, not the api.ts helpers: these three routes
// (GET .../versions, GET/POST .../requests/{reqName}/version) don't have
// TypeScript wrappers yet (a separate, already-planned follow-up), and this
// test's job is to prove the broker's HTTP contract - route registration,
// auth, JSON shape - independent of any client-side wrapper.
async function rawGet(path: string) {
  const res = await fetch(`${BASE_URL}${path}`, { headers: { Authorization: `Bearer ${API_KEY}` } })
  return { status: res.status, body: await res.json() }
}

async function rawPost(path: string, body: unknown) {
  const res = await fetch(`${BASE_URL}${path}`, {
    method: 'POST',
    headers: { Authorization: `Bearer ${API_KEY}`, 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  return { status: res.status, body: await res.json() }
}

describe('api (real broker, fake K8s backend)', () => {
  it('lists the seeded database Promise', async () => {
    const promises = await api.listPromises(API_KEY)
    expect(promises.map((p) => p.name)).toContain('database')
  })

  it('submits then edits a request, and the edit replaces the spec wholesale', async () => {
    const name = uniqueName()

    const created = await api.submitRequest(API_KEY, 'database', name, { size: '10Gi', highAvailability: true })
    expect(created.spec).toEqual({ size: '10Gi', highAvailability: true })

    const updated = await api.updateRequest(API_KEY, 'database', name, { size: '50Gi' })
    expect(updated.spec).toEqual({ size: '50Gi' })

    const fetched = await api.getRequest(API_KEY, 'database', name)
    expect(fetched.spec).toEqual({ size: '50Gi' })
  })

  it('surfaces a real 404 ApiError when editing a request that does not exist', async () => {
    await expect(api.updateRequest(API_KEY, 'database', uniqueName(), { size: '10Gi' })).rejects.toMatchObject({
      status: 404,
    } satisfies Partial<ApiError>)
  })

  it('surfaces a real 401 ApiError for an invalid API key', async () => {
    await expect(api.listPromises('not-a-real-key')).rejects.toMatchObject({ status: 401 } satisfies Partial<ApiError>)
  })

  it('lists both revisions of the seeded database Promise, with v0.2.0 marked latest', async () => {
    const { status, body } = await rawGet('/promises/database/versions')
    expect(status).toBe(200)
    expect(body).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ version: 'v0.1.0', latest: false }),
        expect.objectContaining({ version: 'v0.2.0', latest: true }),
      ]),
    )
  })

  // These three routes (GET .../versions, GET/POST .../requests/{reqName}/version)
  // are covered by Go handler-level unit tests too, but only over HTTP does a
  // route-registration typo or path collision actually show up - see the
  // seeded fixture in broker/cmd/broker/fake_seed.go: example-database in
  // team-payments, pinned to v0.1.0 via its ResourceBinding, with v0.2.0
  // (adds optional highAvailability) as latest. Sequenced in one `it` (not
  // three) because the third assertion mutates that shared fixture's
  // binding - fine here since BROKER_FAKE_K8S state is in-memory and reset
  // on every broker process restart, so a rerun always starts from the same
  // seeded state regardless of this test's internal order.
  it('reports and then moves example-database off its pinned v0.1.0 binding', async () => {
    const before = await rawGet('/promises/database/requests/example-database/version')
    expect(before.status).toBe(200)
    expect(before.body).toEqual({ boundVersion: 'v0.1.0', latestVersion: 'v0.2.0', upgradeAvailable: true })

    const moved = await rawPost('/promises/database/requests/example-database/version', { version: 'v0.2.0' })
    expect(moved.status).toBe(200)
    expect(moved.body).toEqual({ boundVersion: 'v0.2.0', latestVersion: 'v0.2.0', upgradeAvailable: false })

    const after = await rawGet('/promises/database/requests/example-database/version')
    expect(after.status).toBe(200)
    expect(after.body).toEqual({ boundVersion: 'v0.2.0', latestVersion: 'v0.2.0', upgradeAvailable: false })
  })
})
