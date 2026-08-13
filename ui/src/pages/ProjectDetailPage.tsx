import { useCallback, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { toast } from 'sonner'
import { ArrowLeft, Layers, Loader2, Plus, Trash2, TriangleAlert } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Skeleton } from '@/components/ui/skeleton'
import { StatusBadge } from '@/components/StatusBadge'
import { RequestsTable } from '@/components/RequestsTable'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog'
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
import { useAuth } from '@/lib/auth'
import { api } from '@/lib/api'
import { usePolling } from '@/lib/use-polling'
import type { Environment, JsonSchema, ResourceRequest } from '@/lib/types'

const NAME_PATTERN = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/

// One request row within an environment, tagged with which Promise it came
// from - same "fan out over every visible catalog entry" approach
// RequestsPage uses for the flat case, just scoped per environment instead.
interface EnvironmentRequest extends ResourceRequest {
  promiseName: string
  schema?: JsonSchema
}

export function ProjectDetailPage() {
  const { project = '' } = useParams()
  const { session } = useAuth()

  const [environments, setEnvironments] = useState<Environment[] | null>(null)
  const [error, setError] = useState<string | null>(null)

  // requestsByEnvironment[env] is undefined until that environment's fan-out
  // finishes (renders a skeleton), then an array (possibly empty).
  const [requestsByEnvironment, setRequestsByEnvironment] = useState<Record<string, EnvironmentRequest[]>>({})
  const [requestsError, setRequestsError] = useState<string | null>(null)
  const [deletingReqName, setDeletingReqName] = useState<string | null>(null)

  const [dialogOpen, setDialogOpen] = useState(false)
  const [name, setName] = useState('')
  const [creating, setCreating] = useState(false)

  const [pendingDelete, setPendingDelete] = useState<string | null>(null)
  const [deletingName, setDeletingName] = useState<string | null>(null)

  const load = useCallback(async () => {
    if (!session) return
    setError(null)
    let projectEnvs: Environment[] = []
    try {
      const allEnvs = await api.listEnvironments(session.apiKey)
      projectEnvs = allEnvs.filter((e) => e.spec?.project === project)
      setEnvironments(projectEnvs)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load environments')
      return
    }

    setRequestsError(null)
    try {
      const catalogEntries = await api.listPromises(session.apiKey)
      const grouped: Record<string, EnvironmentRequest[]> = {}
      await Promise.all(
        projectEnvs.map(async (env) => {
          const perPromise = await Promise.all(
            catalogEntries.map(async (entry) => {
              try {
                const reqs = await api.listScopedRequests(session.apiKey, project, env.metadata.name, entry.name)
                return reqs.map((r) => ({ ...r, promiseName: entry.name, schema: entry.schema }))
              } catch {
                return [] as EnvironmentRequest[]
              }
            }),
          )
          grouped[env.metadata.name] = perPromise.flat()
        }),
      )
      setRequestsByEnvironment(grouped)
    } catch (err) {
      setRequestsError(err instanceof Error ? err.message : 'Failed to load requests')
    }
  }, [session, project])

  // Provisioning an environment's namespace, and whatever's requested into
  // it, both happen asynchronously on the cluster, so poll for status the
  // same way ServiceDetailPage does.
  usePolling(() => void load(), 5000, [load])

  async function handleCreate(e: React.FormEvent) {
    e.preventDefault()
    if (!session) return

    if (!NAME_PATTERN.test(name)) {
      toast.error('Name must be lowercase alphanumeric with dashes, e.g. "dev"')
      return
    }

    setCreating(true)
    try {
      await api.createEnvironment(session.apiKey, name, project)
      toast.success(`Environment "${name}" created`)
      setDialogOpen(false)
      setName('')
      void load()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Failed to create environment')
    } finally {
      setCreating(false)
    }
  }

  async function handleDelete(envName: string) {
    if (!session) return
    setDeletingName(envName)
    try {
      await api.deleteEnvironment(session.apiKey, envName)
      toast.success(`Deleted "${envName}"`)
      void load()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Failed to delete environment')
    } finally {
      setDeletingName(null)
    }
  }

  async function handleUpdateRequest(environment: string, req: EnvironmentRequest, spec: Record<string, unknown>) {
    if (!session) return
    await api.updateScopedRequest(session.apiKey, project, environment, req.promiseName, req.metadata.name, spec)
    toast.success(`Updated "${req.metadata.name}"`)
    void load()
  }

  async function handleDeleteRequest(environment: string, reqName: string) {
    if (!session) return
    const match = requestsByEnvironment[environment]?.find((r) => r.metadata.name === reqName)
    if (!match) return
    setDeletingReqName(reqName)
    try {
      await api.deleteScopedRequest(session.apiKey, project, environment, match.promiseName, reqName)
      toast.success(`Deleted "${reqName}"`)
      void load()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Failed to delete request')
    } finally {
      setDeletingReqName(null)
    }
  }

  return (
    <div className="space-y-6">
      <div>
        <Link to="/projects">
          <Button variant="ghost" size="sm" className="-ml-2 mb-2">
            <ArrowLeft className="size-4" /> Back to projects
          </Button>
        </Link>
        <div className="flex items-start justify-between gap-4">
          <div>
            <h1 className="text-2xl font-semibold tracking-tight">{project}</h1>
            <p className="text-muted-foreground">
              Environments in this project, and everything requested into each of them.
            </p>
          </div>

          <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
            <DialogTrigger asChild>
              <Button>
                <Plus className="size-4" /> New environment
              </Button>
            </DialogTrigger>
            <DialogContent>
              <DialogHeader>
                <DialogTitle>New environment</DialogTitle>
                <DialogDescription>Provisions its own isolated namespace under {project}.</DialogDescription>
              </DialogHeader>
              <form className="space-y-4" onSubmit={handleCreate}>
                <div className="space-y-1.5">
                  <Label htmlFor="env-name">Name</Label>
                  <Input id="env-name" placeholder="dev" value={name} onChange={(e) => setName(e.target.value)} required />
                  <p className="text-xs text-muted-foreground">Lowercase letters, numbers, and dashes only.</p>
                </div>
                <DialogFooter>
                  <Button type="submit" disabled={creating}>
                    {creating && <Loader2 className="size-4 animate-spin" />}
                    Create environment
                  </Button>
                </DialogFooter>
              </form>
            </DialogContent>
          </Dialog>
        </div>
      </div>

      {error && (
        <Alert variant="destructive">
          <TriangleAlert className="size-4" />
          <AlertTitle>Couldn't load environments</AlertTitle>
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      {!environments && !error && <Skeleton className="h-48 w-full" />}

      {environments && environments.length === 0 && (
        <div className="flex flex-col items-center justify-center gap-2 rounded-xl border border-dashed py-16 text-center">
          <Layers className="size-8 text-muted-foreground" />
          <p className="font-medium">No environments yet</p>
          <p className="text-sm text-muted-foreground">Create one (e.g. "dev") to start submitting requests into it.</p>
        </div>
      )}

      {requestsError && (
        <Alert variant="destructive">
          <TriangleAlert className="size-4" />
          <AlertTitle>Couldn't load requests</AlertTitle>
          <AlertDescription>{requestsError}</AlertDescription>
        </Alert>
      )}

      {environments && environments.length > 0 && (
        <div className="space-y-4">
          {environments.map((env) => {
            const envRequests = requestsByEnvironment[env.metadata.name]
            return (
              <Card key={env.metadata.name}>
                <CardHeader className="flex-row items-center justify-between gap-4 space-y-0">
                  <div className="flex items-center gap-3">
                    <CardTitle className="font-mono text-base">{env.metadata.name}</CardTitle>
                    <StatusBadge status={env.status} />
                  </div>
                  <div className="flex items-center gap-3">
                    <span className="font-mono text-xs text-muted-foreground">
                      {typeof env.status?.namespace === 'string' ? env.status.namespace : '—'}
                    </span>
                    <Button
                      variant="ghost"
                      size="icon"
                      className="size-8 text-destructive hover:text-destructive"
                      disabled={deletingName === env.metadata.name}
                      onClick={() => setPendingDelete(env.metadata.name)}
                    >
                      <Trash2 className="size-4" />
                    </Button>
                  </div>
                </CardHeader>
                <CardContent className="p-0">
                  {!envRequests && <Skeleton className="m-4 h-16" />}

                  {envRequests && envRequests.length === 0 && (
                    <div className="border-t py-8 text-center text-sm text-muted-foreground">
                      Nothing requested into {env.metadata.name} yet. Submit one from the catalog and target{' '}
                      {project} / {env.metadata.name}.
                    </div>
                  )}

                  {envRequests && envRequests.length > 0 && (
                    <RequestsTable
                      requests={envRequests}
                      showKind
                      deletingName={deletingReqName}
                      onDelete={(reqName) => void handleDeleteRequest(env.metadata.name, reqName)}
                      schemaFor={(req) => (req as EnvironmentRequest).schema}
                      onSaveEdit={(req, spec) => handleUpdateRequest(env.metadata.name, req as EnvironmentRequest, spec)}
                    />
                  )}
                </CardContent>
              </Card>
            )
          })}
        </div>
      )}

      <AlertDialog open={pendingDelete !== null} onOpenChange={(open) => !open && setPendingDelete(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete "{pendingDelete}"?</AlertDialogTitle>
            <AlertDialogDescription>
              This tears down the environment's namespace and everything requested into it. This can't be undone.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              onClick={() => {
                if (pendingDelete) void handleDelete(pendingDelete)
                setPendingDelete(null)
              }}
            >
              Delete
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}
