import { useCallback, useState } from 'react'
import { Link } from 'react-router-dom'
import { toast } from 'sonner'
import { ArrowRight, FolderKanban, Loader2, Plus, Trash2, TriangleAlert } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { Card, CardDescription, CardFooter, CardHeader, CardTitle } from '@/components/ui/card'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Skeleton } from '@/components/ui/skeleton'
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
import type { Project } from '@/lib/types'

const NAME_PATTERN = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/

export function ProjectsPage() {
  const { session } = useAuth()
  const [projects, setProjects] = useState<Project[] | null>(null)
  const [error, setError] = useState<string | null>(null)

  const [dialogOpen, setDialogOpen] = useState(false)
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [creating, setCreating] = useState(false)

  const [pendingDelete, setPendingDelete] = useState<string | null>(null)
  const [deletingName, setDeletingName] = useState<string | null>(null)

  const load = useCallback(() => {
    if (!session) return
    setError(null)
    api
      .listProjects(session.apiKey)
      .then(setProjects)
      .catch((err) => setError(err instanceof Error ? err.message : 'Failed to load projects'))
  }, [session])

  // Projects rarely change status, but polling keeps a freshly-created one
  // showing up without a manual refresh, same as every other list page.
  usePolling(load, 8000, [load])

  async function handleCreate(e: React.FormEvent) {
    e.preventDefault()
    if (!session) return

    if (!NAME_PATTERN.test(name)) {
      toast.error('Name must be lowercase alphanumeric with dashes, e.g. "checkout-service"')
      return
    }

    setCreating(true)
    try {
      await api.createProject(session.apiKey, name, description.trim() || undefined)
      toast.success(`Project "${name}" created`)
      setDialogOpen(false)
      setName('')
      setDescription('')
      load()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Failed to create project')
    } finally {
      setCreating(false)
    }
  }

  async function handleDelete(projectName: string) {
    if (!session) return
    setDeletingName(projectName)
    try {
      await api.deleteProject(session.apiKey, projectName)
      toast.success(`Deleted "${projectName}"`)
      load()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Failed to delete project')
    } finally {
      setDeletingName(null)
    }
  }

  return (
    <div className="space-y-6">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Projects</h1>
          <p className="text-muted-foreground">
            {session?.team}'s projects. Each one can have its own dev/staging/prod environments.
          </p>
        </div>

        <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
          <DialogTrigger asChild>
            <Button>
              <Plus className="size-4" /> New project
            </Button>
          </DialogTrigger>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>New project</DialogTitle>
              <DialogDescription>
                Projects group environments (dev, staging, prod, ...) under one name.
              </DialogDescription>
            </DialogHeader>
            <form className="space-y-4" onSubmit={handleCreate}>
              <div className="space-y-1.5">
                <Label htmlFor="project-name">Name</Label>
                <Input
                  id="project-name"
                  placeholder="checkout-service"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  required
                />
                <p className="text-xs text-muted-foreground">Lowercase letters, numbers, and dashes only.</p>
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="project-description">Description (optional)</Label>
                <Textarea
                  id="project-description"
                  placeholder="What this project is for"
                  value={description}
                  onChange={(e) => setDescription(e.target.value)}
                />
              </div>
              <DialogFooter>
                <Button type="submit" disabled={creating}>
                  {creating && <Loader2 className="size-4 animate-spin" />}
                  Create project
                </Button>
              </DialogFooter>
            </form>
          </DialogContent>
        </Dialog>
      </div>

      {error && (
        <Alert variant="destructive">
          <TriangleAlert className="size-4" />
          <AlertTitle>Couldn't load projects</AlertTitle>
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      {!projects && !error && (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {Array.from({ length: 3 }).map((_, i) => (
            <Skeleton key={i} className="h-40 rounded-xl" />
          ))}
        </div>
      )}

      {projects && projects.length === 0 && (
        <div className="flex flex-col items-center justify-center gap-2 rounded-xl border border-dashed py-16 text-center">
          <FolderKanban className="size-8 text-muted-foreground" />
          <p className="font-medium">No projects yet</p>
          <p className="text-sm text-muted-foreground">Create one to start grouping environments under it.</p>
        </div>
      )}

      {projects && projects.length > 0 && (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {projects.map((project) => (
            <Card key={project.metadata.name} className="flex h-full flex-col">
              <Link to={`/projects/${encodeURIComponent(project.metadata.name)}`}>
                <CardHeader>
                  <CardTitle className="text-base">{project.metadata.name}</CardTitle>
                  <CardDescription className="line-clamp-3">
                    {project.spec?.description || 'No description provided.'}
                  </CardDescription>
                </CardHeader>
              </Link>
              <CardFooter className="mt-auto justify-between">
                <Link
                  to={`/projects/${encodeURIComponent(project.metadata.name)}`}
                  className="inline-flex items-center gap-1 text-sm font-medium text-primary"
                >
                  Environments <ArrowRight className="size-3.5" />
                </Link>
                <Button
                  variant="ghost"
                  size="icon"
                  className="size-8 text-destructive hover:text-destructive"
                  disabled={deletingName === project.metadata.name}
                  onClick={() => setPendingDelete(project.metadata.name)}
                >
                  <Trash2 className="size-4" />
                </Button>
              </CardFooter>
            </Card>
          ))}
        </div>
      )}

      <AlertDialog open={pendingDelete !== null} onOpenChange={(open) => !open && setPendingDelete(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete "{pendingDelete}"?</AlertDialogTitle>
            <AlertDialogDescription>
              This deletes the project record. It doesn't delete its environments' namespaces - remove those first
              from the project's page.
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
