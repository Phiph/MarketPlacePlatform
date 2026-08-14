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

function uniqueName() {
  return `ui-it-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`
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
})
