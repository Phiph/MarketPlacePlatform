import type { ResourceStatus } from '@/lib/types'

export type RequestPhase = 'ready' | 'failed' | 'pending' | 'unknown'

// Kratix (like most Kubernetes controllers) reports progress via
// status.conditions, a list of {type, status, reason, message}. There's no
// single canonical "is it done" field, so this looks for the conventional
// Ready/Succeeded condition types and falls back to "pending" if the
// resource has a status but no recognizable condition yet.
export function requestPhase(status?: ResourceStatus): RequestPhase {
  const conditions = status?.conditions
  if (!conditions || conditions.length === 0) return status ? 'pending' : 'unknown'

  const byType = new Map(conditions.map((c) => [c.type, c]))
  const primary =
    byType.get('Ready') ?? byType.get('Succeeded') ?? byType.get('ConfigureWorkflowCompleted') ?? conditions[0]

  if (!primary) return 'pending'
  if (primary.status === 'True') return 'ready'
  if (primary.status === 'False' && primary.reason?.toLowerCase().includes('fail')) return 'failed'
  return 'pending'
}

export const phaseLabel: Record<RequestPhase, string> = {
  ready: 'Ready',
  failed: 'Failed',
  pending: 'Pending',
  unknown: 'Unknown',
}
