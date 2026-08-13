import type { CatalogEntry, ResourceRequest, Project, Environment, ApiErrorBody } from '@/lib/types'

// Same-origin default lets the UI be reverse-proxied alongside the broker;
// override for local dev against `make broker-run` (see .env.example).
const BASE_URL = import.meta.env.VITE_BROKER_URL ?? ''

export class ApiError extends Error {
  status: number

  constructor(status: number, message: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
  }
}

async function request<T>(apiKey: string, path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${BASE_URL}/api${path}`, {
    ...init,
    headers: {
      Authorization: `Bearer ${apiKey}`,
      ...(init?.body ? { 'Content-Type': 'application/json' } : {}),
      ...init?.headers,
    },
  })

  if (!res.ok) {
    let message = res.statusText
    try {
      const body = (await res.json()) as ApiErrorBody
      if (body.error) message = body.error
    } catch {
      // body wasn't JSON - fall back to statusText
    }
    throw new ApiError(res.status, message)
  }

  if (res.status === 204) return undefined as T
  return (await res.json()) as T
}

export const api = {
  listPromises: (apiKey: string, all = false) =>
    request<CatalogEntry[]>(apiKey, `/promises${all ? '?all=true' : ''}`),

  getPromise: (apiKey: string, name: string) =>
    request<CatalogEntry>(apiKey, `/promises/${encodeURIComponent(name)}`),

  submitRequest: (apiKey: string, promiseName: string, name: string, spec: Record<string, unknown>) =>
    request<ResourceRequest>(apiKey, `/promises/${encodeURIComponent(promiseName)}/requests`, {
      method: 'POST',
      body: JSON.stringify({ name, spec }),
    }),

  listRequests: (apiKey: string, promiseName: string) =>
    request<ResourceRequest[]>(apiKey, `/promises/${encodeURIComponent(promiseName)}/requests`),

  getRequest: (apiKey: string, promiseName: string, reqName: string) =>
    request<ResourceRequest>(
      apiKey,
      `/promises/${encodeURIComponent(promiseName)}/requests/${encodeURIComponent(reqName)}`,
    ),

  deleteRequest: (apiKey: string, promiseName: string, reqName: string) =>
    request<void>(apiKey, `/promises/${encodeURIComponent(promiseName)}/requests/${encodeURIComponent(reqName)}`, {
      method: 'DELETE',
    }),

  updateRequest: (apiKey: string, promiseName: string, reqName: string, spec: Record<string, unknown>) =>
    request<ResourceRequest>(
      apiKey,
      `/promises/${encodeURIComponent(promiseName)}/requests/${encodeURIComponent(reqName)}`,
      { method: 'PUT', body: JSON.stringify({ spec }) },
    ),

  // Projects carry no identity-sensitive fields (see promises/project/README.md),
  // so they ride the fully generic request routes above with a fixed promise name.
  listProjects: (apiKey: string) => request<Project[]>(apiKey, '/promises/project/requests'),

  getProject: (apiKey: string, name: string) =>
    request<Project>(apiKey, `/promises/project/requests/${encodeURIComponent(name)}`),

  createProject: (apiKey: string, name: string, description?: string) =>
    request<Project>(apiKey, '/promises/project/requests', {
      method: 'POST',
      body: JSON.stringify({ name, spec: description ? { description } : {} }),
    }),

  deleteProject: (apiKey: string, name: string) =>
    request<void>(apiKey, `/promises/project/requests/${encodeURIComponent(name)}`, { method: 'DELETE' }),

  // Get/list/delete are identity-insensitive and reuse the generic routes
  // too - only creation needs the dedicated endpoint below, since it's the
  // one place spec.team/spec.businessUnit have to be broker-composed rather
  // than caller-supplied (see promises/environment/README.md).
  listEnvironments: (apiKey: string) => request<Environment[]>(apiKey, '/promises/environment/requests'),

  getEnvironment: (apiKey: string, name: string) =>
    request<Environment>(apiKey, `/promises/environment/requests/${encodeURIComponent(name)}`),

  createEnvironment: (apiKey: string, name: string, project: string) =>
    request<Environment>(apiKey, '/environments', {
      method: 'POST',
      body: JSON.stringify({ name, project }),
    }),

  deleteEnvironment: (apiKey: string, name: string) =>
    request<void>(apiKey, `/promises/environment/requests/${encodeURIComponent(name)}`, { method: 'DELETE' }),

  // Scoped equivalents of submitRequest/listRequests/getRequest/deleteRequest
  // above, landing in a project's environment namespace instead of the
  // team's flat default one.
  submitScopedRequest: (
    apiKey: string,
    project: string,
    environment: string,
    promiseName: string,
    name: string,
    spec: Record<string, unknown>,
  ) =>
    request<ResourceRequest>(
      apiKey,
      `/projects/${encodeURIComponent(project)}/environments/${encodeURIComponent(environment)}/promises/${encodeURIComponent(promiseName)}/requests`,
      { method: 'POST', body: JSON.stringify({ name, spec }) },
    ),

  listScopedRequests: (apiKey: string, project: string, environment: string, promiseName: string) =>
    request<ResourceRequest[]>(
      apiKey,
      `/projects/${encodeURIComponent(project)}/environments/${encodeURIComponent(environment)}/promises/${encodeURIComponent(promiseName)}/requests`,
    ),

  getScopedRequest: (apiKey: string, project: string, environment: string, promiseName: string, reqName: string) =>
    request<ResourceRequest>(
      apiKey,
      `/projects/${encodeURIComponent(project)}/environments/${encodeURIComponent(environment)}/promises/${encodeURIComponent(promiseName)}/requests/${encodeURIComponent(reqName)}`,
    ),

  deleteScopedRequest: (apiKey: string, project: string, environment: string, promiseName: string, reqName: string) =>
    request<void>(
      apiKey,
      `/projects/${encodeURIComponent(project)}/environments/${encodeURIComponent(environment)}/promises/${encodeURIComponent(promiseName)}/requests/${encodeURIComponent(reqName)}`,
      { method: 'DELETE' },
    ),

  updateScopedRequest: (
    apiKey: string,
    project: string,
    environment: string,
    promiseName: string,
    reqName: string,
    spec: Record<string, unknown>,
  ) =>
    request<ResourceRequest>(
      apiKey,
      `/projects/${encodeURIComponent(project)}/environments/${encodeURIComponent(environment)}/promises/${encodeURIComponent(promiseName)}/requests/${encodeURIComponent(reqName)}`,
      { method: 'PUT', body: JSON.stringify({ spec }) },
    ),
}
