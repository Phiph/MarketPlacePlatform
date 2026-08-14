import { useState } from 'react'
import { Eye, Pencil, Tag, Trash2 } from 'lucide-react'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Button } from '@/components/ui/button'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { StatusBadge } from '@/components/StatusBadge'
import { PromiseVersionBadge } from '@/components/PromiseVersionBadge'
import { RequestDetailDialog } from '@/components/RequestDetailDialog'
import { RequestEditDialog } from '@/components/RequestEditDialog'
import { RequestVersionDialog } from '@/components/RequestVersionDialog'
import type { JsonSchema, PromiseRevision, RequestVersionInfo, ResourceRequest } from '@/lib/types'

interface RequestsTableProps {
  requests: ResourceRequest[]
  onDelete: (name: string) => void
  deletingName?: string | null
  showKind?: boolean
  schemaFor?: (req: ResourceRequest) => JsonSchema | undefined
  onSaveEdit?: (req: ResourceRequest, spec: Record<string, unknown>) => Promise<void>
  versionInfoFor?: (req: ResourceRequest) => RequestVersionInfo | undefined
  versionsFor?: (req: ResourceRequest) => PromiseRevision[] | undefined
  onSetVersion?: (req: ResourceRequest, version: string) => Promise<void>
}

export function RequestsTable({
  requests,
  onDelete,
  deletingName,
  showKind,
  schemaFor,
  onSaveEdit,
  versionInfoFor,
  versionsFor,
  onSetVersion,
}: RequestsTableProps) {
  const [selected, setSelected] = useState<ResourceRequest | null>(null)
  const [pendingDelete, setPendingDelete] = useState<string | null>(null)
  const [editing, setEditing] = useState<ResourceRequest | null>(null)
  const [managingVersion, setManagingVersion] = useState<ResourceRequest | null>(null)

  return (
    <>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Name</TableHead>
            {showKind && <TableHead>Service</TableHead>}
            <TableHead>Status</TableHead>
            {versionInfoFor && <TableHead>Version</TableHead>}
            <TableHead>Created</TableHead>
            <TableHead className="w-24 text-right">Actions</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {requests.map((req) => (
            <TableRow key={req.metadata.name} className="cursor-pointer" onClick={() => setSelected(req)}>
              <TableCell className="font-mono text-sm">{req.metadata.name}</TableCell>
              {showKind && <TableCell className="text-sm text-muted-foreground">{req.kind}</TableCell>}
              <TableCell>
                <StatusBadge status={req.status} />
              </TableCell>
              {versionInfoFor && (
                <TableCell>
                  <PromiseVersionBadge info={versionInfoFor(req)} />
                </TableCell>
              )}
              <TableCell className="text-sm text-muted-foreground">
                {req.metadata.creationTimestamp ? new Date(req.metadata.creationTimestamp).toLocaleString() : '—'}
              </TableCell>
              <TableCell className="text-right">
                <div className="flex justify-end gap-1" onClick={(e) => e.stopPropagation()}>
                  <Button variant="ghost" size="icon" className="size-8" onClick={() => setSelected(req)}>
                    <Eye className="size-4" />
                  </Button>
                  {onSaveEdit && (
                    <Button variant="ghost" size="icon" className="size-8" onClick={() => setEditing(req)}>
                      <Pencil className="size-4" />
                    </Button>
                  )}
                  {onSetVersion && (
                    <Button variant="ghost" size="icon" className="size-8" onClick={() => setManagingVersion(req)}>
                      <Tag className="size-4" />
                    </Button>
                  )}
                  <Button
                    variant="ghost"
                    size="icon"
                    className="size-8 text-destructive hover:text-destructive"
                    disabled={deletingName === req.metadata.name}
                    onClick={() => setPendingDelete(req.metadata.name)}
                  >
                    <Trash2 className="size-4" />
                  </Button>
                </div>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
      <RequestDetailDialog request={selected} open={selected !== null} onOpenChange={(open) => !open && setSelected(null)} />

      {onSaveEdit && editing && (
        <RequestEditDialog
          request={editing}
          schema={schemaFor?.(editing)}
          onOpenChange={(open) => !open && setEditing(null)}
          onSave={(spec) => onSaveEdit(editing, spec)}
        />
      )}

      {onSetVersion && managingVersion && (
        <RequestVersionDialog
          request={managingVersion}
          versionInfo={versionInfoFor?.(managingVersion)}
          versions={versionsFor?.(managingVersion)}
          onOpenChange={(open) => !open && setManagingVersion(null)}
          onSetVersion={(version) => onSetVersion(managingVersion, version)}
        />
      )}

      <AlertDialog open={pendingDelete !== null} onOpenChange={(open) => !open && setPendingDelete(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete "{pendingDelete}"?</AlertDialogTitle>
            <AlertDialogDescription>
              This permanently deletes the request and tears down whatever it provisioned. This can't be undone.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              onClick={() => {
                if (pendingDelete) onDelete(pendingDelete)
                setPendingDelete(null)
              }}
            >
              Delete
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}
