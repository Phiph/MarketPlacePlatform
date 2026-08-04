import { useCallback, useState } from 'react'
import { Link } from 'react-router-dom'
import { toast } from 'sonner'
import { Inbox, TriangleAlert } from 'lucide-react'
import { Card } from '@/components/ui/card'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Skeleton } from '@/components/ui/skeleton'
import { RequestsTable } from '@/components/RequestsTable'
import { useAuth } from '@/lib/auth'
import { api } from '@/lib/api'
import { usePolling } from '@/lib/use-polling'
import type { CatalogEntry, ResourceRequest } from '@/lib/types'

// One request row, tagged with which Promise it belongs to - the broker has
// no "all my requests" endpoint, so this fans out a listRequests call per
// visible catalog entry and merges the results.
interface TaggedRequest extends ResourceRequest {
  promiseName: string
}

export function RequestsPage() {
  const { session } = useAuth()
  const [requests, setRequests] = useState<TaggedRequest[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [deletingName, setDeletingName] = useState<string | null>(null)

  const load = useCallback(async () => {
    if (!session) return
    setError(null)
    try {
      const entries: CatalogEntry[] = await api.listPromises(session.apiKey)
      const results = await Promise.all(
        entries.map(async (entry) => {
          try {
            const reqs = await api.listRequests(session.apiKey, entry.name)
            return reqs.map((r) => ({ ...r, promiseName: entry.name }))
          } catch {
            return []
          }
        }),
      )
      setRequests(results.flat())
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load requests')
    }
  }, [session])

  // Provisioning happens asynchronously on the cluster, so poll for status
  // updates rather than requiring a manual refresh.
  usePolling(() => void load(), 6000, [load])

  async function handleDelete(promiseName: string, reqName: string) {
    if (!session) return
    setDeletingName(reqName)
    try {
      await api.deleteRequest(session.apiKey, promiseName, reqName)
      toast.success(`Deleted "${reqName}"`)
      void load()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Failed to delete request')
    } finally {
      setDeletingName(null)
    }
  }

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">My Requests</h1>
        <p className="text-muted-foreground">Everything {session?.team} has requested across the catalog.</p>
      </div>

      {error && (
        <Alert variant="destructive">
          <TriangleAlert className="size-4" />
          <AlertTitle>Couldn't load requests</AlertTitle>
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      {!requests && !error && <Skeleton className="h-64 w-full" />}

      {requests && requests.length === 0 && (
        <div className="flex flex-col items-center justify-center gap-2 rounded-xl border border-dashed py-16 text-center">
          <Inbox className="size-8 text-muted-foreground" />
          <p className="font-medium">No requests yet</p>
          <p className="text-sm text-muted-foreground">
            Browse the{' '}
            <Link to="/catalog" className="text-primary underline underline-offset-2">
              catalog
            </Link>{' '}
            to request a service.
          </p>
        </div>
      )}

      {requests && requests.length > 0 && (
        <Card>
          <RequestsTable
            requests={requests}
            showKind
            deletingName={deletingName}
            onDelete={(reqName) => {
              const match = requests.find((r) => r.metadata.name === reqName)
              if (match) void handleDelete(match.promiseName, reqName)
            }}
          />
        </Card>
      )}
    </div>
  )
}
