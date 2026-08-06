import { useCallback, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { toast } from 'sonner'
import { ArrowLeft, Layers, Loader2, Plus, Trash2, TriangleAlert } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Card, CardContent } from '@/components/ui/card'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Skeleton } from '@/components/ui/skeleton'
import { StatusBadge } from '@/components/StatusBadge'
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
import type { Environment } from '@/lib/types'

const NAME_PATTERN = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/

export function ProjectDetailPage() {
  const { project = '' } = useParams()
  const { session } = useAuth()

  const [environments, setEnvironments] = useState<Environment[] | null>(null)
  const [error, setError] = useState<string | null>(null)

  const [dialogOpen, setDialogOpen] = useState(false)
  const [name, setName] = useState('')
  const [creating, setCreating] = useState(false)

  const [pendingDelete, setPendingDelete] = useState<string | null>(null)
  const [deletingName, setDeletingName] = useState<string | null>(null)

  const load = useCallback(() => {
    if (!session) return
    setError(null)
    api
      .listEnvironments(session.apiKey)
      .then((all) => setEnvironments(all.filter((e) => e.spec?.project === project)))
      .catch((err) => setError(err instanceof Error ? err.message : 'Failed to load environments'))
  }, [session, project])

  // Provisioning an environment's namespace happens asynchronously on the
  // cluster, so poll for status the same way ServiceDetailPage does.
  usePolling(load, 5000, [load])

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
      load()
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
      load()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Failed to delete environment')
    } finally {
      setDeletingName(null)
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
              Environments in this project. Requests submitted from the catalog can target any of them.
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

      {environments && environments.length > 0 && (
        <Card>
          <CardContent className="divide-y p-0">
            {environments.map((env) => (
              <div key={env.metadata.name} className="flex items-center justify-between gap-4 px-4 py-3">
                <div className="flex items-center gap-3">
                  <span className="font-mono text-sm">{env.metadata.name}</span>
                  <StatusBadge status={env.status} />
                </div>
                <div className="flex items-center gap-4">
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
              </div>
            ))}
          </CardContent>
        </Card>
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
