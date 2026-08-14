// Mirrors broker/internal/catalog.Entry (broker/internal/catalog/catalog.go).
export interface CatalogEntry {
  name: string
  displayName: string
  description?: string
  visible: boolean
  // The Promise's current kratix.io/promise-version label - a different
  // axis from `version` below (the CRD *schema* version, e.g. v1alpha1).
  // See catalog.go's own doc comment on Entry.PromiseVersion.
  promiseVersion?: string
  group: string
  version: string
  kind: string
  plural: string
  scope: string
  schema?: JsonSchema
  status?: Record<string, unknown>
}

// A (subset of a) JSON Schema / OpenAPI v3 schema, as embedded in a
// Promise's spec.api CRD. Only the parts the request-form builder needs.
export interface JsonSchema {
  type?: string
  properties?: Record<string, JsonSchema>
  required?: string[]
  enum?: (string | number)[]
  description?: string
  default?: unknown
  items?: JsonSchema
  format?: string
  minimum?: number
  maximum?: number
}

// Mirrors broker/internal/catalog.Revision (broker/internal/catalog/revisions.go).
export interface PromiseRevision {
  version: string
  latest: boolean
  createdAt: string
}

// Mirrors the requestVersionInfo response shape both GET and POST
// .../requests/{reqName}/version return (broker/internal/api/server.go).
export interface RequestVersionInfo {
  boundVersion: string
  latestVersion: string
  upgradeAvailable: boolean
}

// A submitted request: an instance of a Promise's custom resource. Kubernetes
// objects are heterogeneous, so this stays loose beyond the fields every
// request is guaranteed to have.
export interface ResourceRequest {
  apiVersion: string
  kind: string
  metadata: {
    name: string
    namespace?: string
    creationTimestamp?: string
    [key: string]: unknown
  }
  spec?: Record<string, unknown>
  status?: ResourceStatus
}

export interface Condition {
  type: string
  status: string
  reason?: string
  message?: string
  lastTransitionTime?: string
}

export interface ResourceStatus {
  conditions?: Condition[]
  message?: string
  [key: string]: unknown
}

export interface ApiErrorBody {
  error: string
}

// A team's Project - an instance of the `project` Promise's custom
// resource. See broker/../promises/project/.
export interface Project extends ResourceRequest {
  spec?: {
    description?: string
  }
}

// One environment (dev/staging/prod/...) of a Project - an instance of the
// `environment` Promise's custom resource. spec.team/spec.businessUnit are
// broker-owned (see promises/environment/README.md); the UI never sets them
// itself, POST /api/environments composes them server-side.
export interface Environment extends ResourceRequest {
  spec?: {
    project: string
    team: string
    businessUnit: string
  }
}
