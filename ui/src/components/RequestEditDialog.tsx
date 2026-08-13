import { useEffect, useState } from 'react'
import { Loader2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { SchemaForm } from '@/components/SchemaForm'
import type { JsonSchema, ResourceRequest } from '@/lib/types'

export function RequestEditDialog({
  request,
  schema,
  open,
  onOpenChange,
  onSave,
}: {
  request: ResourceRequest | null
  schema: JsonSchema | undefined
  open: boolean
  onOpenChange: (open: boolean) => void
  onSave: (spec: Record<string, unknown>) => Promise<void>
}) {
  const [spec, setSpec] = useState<Record<string, unknown>>({})
  const [saving, setSaving] = useState(false)

  // Reset to the request's current spec whenever the dialog opens for a
  // (possibly different) request - keyed off `open`/`request` rather than
  // every request prop change, so a mid-edit polling refresh elsewhere on
  // the page doesn't clobber what the user is typing.
  useEffect(() => {
    if (open) setSpec(request?.spec ?? {})
  }, [open, request])

  async function handleSave(e: React.FormEvent) {
    e.preventDefault()
    setSaving(true)
    try {
      await onSave(spec)
      onOpenChange(false)
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[80vh] max-w-2xl overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="font-mono">{request?.metadata.name}</DialogTitle>
          <DialogDescription>Edit this request's spec and save to apply the change.</DialogDescription>
        </DialogHeader>

        <form className="space-y-4" onSubmit={handleSave}>
          <SchemaForm schema={schema} value={spec} onChange={setSpec} />
          <DialogFooter>
            <Button type="submit" disabled={saving}>
              {saving && <Loader2 className="size-4 animate-spin" />}
              Save changes
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
