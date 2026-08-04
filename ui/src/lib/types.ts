// Mirrors broker/internal/catalog.Entry (broker/internal/catalog/catalog.go).
export interface CatalogEntry {
  name: string
  displayName: string
  description?: string
  visible: boolean
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
