import { useState } from 'react'
import { Loader2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { SchemaForm } from '@/components/SchemaForm'
import type { JsonSchema, ResourceRequest } from '@/lib/types'

// The caller only mounts this component while there's a request to edit
// (see RequestsTable) - a fresh instance per request, rather than one
// long-lived instance toggled via an `open` prop. That's what lets `spec`
// seed synchronously from `request.spec` on the initial render: seeding it
// via a useEffect after mount instead made the Select start out
// uncontrolled (spec still {}) and then flip to controlled once the effect
// ran, which Radix's Select doesn't reliably pick up - the dropdown stuck
// on "Select…" even once the underlying state was correct.
export function RequestEditDialog({
  request,
  schema,
  onOpenChange,
  onSave,
}: {
  request: ResourceRequest
  schema: JsonSchema | undefined
  onOpenChange: (open: boolean) => void
  onSave: (spec: Record<string, unknown>) => Promise<void>
}) {
  const [spec, setSpec] = useState<Record<string, unknown>>(request.spec ?? {})
  const [saving, setSaving] = useState(false)

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
    <Dialog open onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[80vh] max-w-2xl overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="font-mono">{request.metadata.name}</DialogTitle>
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
