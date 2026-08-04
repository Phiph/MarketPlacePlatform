import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription } from '@/components/ui/dialog'
import { StatusBadge } from '@/components/StatusBadge'
import type { ResourceRequest } from '@/lib/types'

export function RequestDetailDialog({
  request,
  open,
  onOpenChange,
}: {
  request: ResourceRequest | null
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[80vh] max-w-2xl overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2 font-mono">
            {request?.metadata.name}
            {request && <StatusBadge status={request.status} />}
          </DialogTitle>
          <DialogDescription>{request?.kind}</DialogDescription>
        </DialogHeader>

        {request && (
          <div className="space-y-4">
            <div>
              <h4 className="mb-1.5 text-sm font-medium">Spec</h4>
              <pre className="overflow-x-auto rounded-lg bg-muted p-3 text-xs">
                {JSON.stringify(request.spec ?? {}, null, 2)}
              </pre>
            </div>
            <div>
              <h4 className="mb-1.5 text-sm font-medium">Status</h4>
              <pre className="overflow-x-auto rounded-lg bg-muted p-3 text-xs">
                {JSON.stringify(request.status ?? {}, null, 2)}
              </pre>
            </div>
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
}
