import { Badge } from '@/components/ui/badge'
import { requestPhase, phaseLabel, type RequestPhase } from '@/lib/status'
import type { ResourceStatus } from '@/lib/types'
import { cn } from '@/lib/utils'

const dotColor: Record<RequestPhase, string> = {
  ready: 'bg-emerald-500',
  failed: 'bg-red-500',
  pending: 'bg-amber-500',
  unknown: 'bg-muted-foreground',
}

export function StatusBadge({ status }: { status?: ResourceStatus }) {
  const phase = requestPhase(status)
  return (
    <Badge variant="outline" className="gap-1.5 font-normal">
      <span className={cn('size-1.5 rounded-full', dotColor[phase])} />
      {phaseLabel[phase]}
    </Badge>
  )
}
