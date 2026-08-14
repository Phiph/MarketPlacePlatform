import { useState } from 'react'
import { Loader2 } from 'lucide-react'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { ApiError } from '@/lib/api'
import type { PromiseRevision, RequestVersionInfo, ResourceRequest } from '@/lib/types'

// Unlike RequestEditDialog, this dialog does surface its own error toast on
// a failed move rather than leaving it to the caller: a 400 here specifically
// means "your spec doesn't fit the target version's schema" (see
// catalog.ValidateAgainstSchema in the broker) - a clear, actionable message
// the design spec's validation goal exists to produce, so swallowing it
// silently up to the caller would defeat that goal.
export function RequestVersionDialog({
  request,
  versionInfo,
  versions,
  onOpenChange,
  onSetVersion,
}: {
  request: ResourceRequest
  versionInfo: RequestVersionInfo | undefined
  versions: PromiseRevision[] | undefined
  onOpenChange: (open: boolean) => void
  onSetVersion: (version: string) => Promise<void>
}) {
  const [target, setTarget] = useState(versionInfo?.boundVersion ?? '')
  const [saving, setSaving] = useState(false)

  async function handleMove(e: React.FormEvent) {
    e.preventDefault()
    if (!target || target === versionInfo?.boundVersion) return
    setSaving(true)
    try {
      await onSetVersion(target)
      onOpenChange(false)
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : 'Failed to move version')
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open onOpenChange={onOpenChange}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle className="font-mono">{request.metadata.name}</DialogTitle>
          <DialogDescription>
            Currently bound to <span className="font-mono">{versionInfo?.boundVersion ?? '…'}</span>. Choose a version to
            move to - forward (upgrade) or back (rollback).
          </DialogDescription>
        </DialogHeader>

        <form className="space-y-4" onSubmit={handleMove}>
          <div className="space-y-1.5">
            <Label htmlFor="target-version">Version</Label>
            {versions === undefined ? (
              <div className="flex items-center gap-2 text-sm text-muted-foreground">
                <Loader2 className="size-4 animate-spin" /> Loading versions…
              </div>
            ) : (
              <Select value={target || undefined} onValueChange={setTarget}>
                <SelectTrigger id="target-version" className="w-full">
                  <SelectValue placeholder="Select…" />
                </SelectTrigger>
                <SelectContent>
                  {versions.map((v) => (
                    <SelectItem key={v.version} value={v.version}>
                      {v.version}
                      {v.latest ? ' (latest)' : ''}
                      {v.version === versionInfo?.boundVersion ? ' (current)' : ''}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            )}
          </div>
          <DialogFooter>
            <Button type="submit" disabled={saving || !target || target === versionInfo?.boundVersion}>
              {saving && <Loader2 className="size-4 animate-spin" />}
              Move to this version
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
