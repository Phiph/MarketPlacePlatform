import { useCallback, useEffect, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { toast } from 'sonner'
import { ArrowLeft, Loader2, TriangleAlert } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Skeleton } from '@/components/ui/skeleton'
import { Select, SelectContent, SelectGroup, SelectItem, SelectLabel, SelectTrigger, SelectValue } from '@/components/ui/select'
import { SchemaForm } from '@/components/SchemaForm'
import { RequestsTable } from '@/components/RequestsTable'
import { useAuth } from '@/lib/auth'
import { api, ApiError } from '@/lib/api'
import { usePolling } from '@/lib/use-polling'
import type { CatalogEntry, Environment, ResourceRequest } from '@/lib/types'

const NAME_PATTERN = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/

// The <Select>'s own value space can't represent "nothing chosen" with an
// empty string (Radix reserves that), so a sentinel stands in for "submit
// into the team's flat default namespace" - keeps behavior byte-for-byte
// unchanged from before targets existed.
const DEFAULT_TARGET = '__team-default__'

function encodeTarget(project: string, environment: string) {
  return `${project}::${environment}`
}

function decodeTarget(value: string): { project: string; environment: string } | null {
  if (value === DEFAULT_TARGET) return null
  const [project, environment] = value.split('::')
  return project && environment ? { project, environment } : null
}

export function ServiceDetailPage() {
  const { name = '' } = useParams()
  const navigate = useNavigate()
  const { session } = useAuth()

  const [entry, setEntry] = useState<CatalogEntry | null>(null)
  const [entryError, setEntryError] = useState<string | null>(null)

  const [requests, setRequests] = useState<ResourceRequest[] | null>(null)
  const [requestsError, setRequestsError] = useState<string | null>(null)
  const [deletingName, setDeletingName] = useState<string | null>(null)

  const [requestName, setRequestName] = useState('')
  const [spec, setSpec] = useState<Record<string, unknown>>({})
  const [submitting, setSubmitting] = useState(false)

  // project -> its environment names, for the target selector. Loaded once
  // from the team's full environment list (already scoped to the team by
  // the broker) rather than per-project, to avoid an N+1 fan-out.
  const [environmentsByProject, setEnvironmentsByProject] = useState<Record<string, string[]>>({})
  const [targetValue, setTargetValue] = useState(DEFAULT_TARGET)
  const target = decodeTarget(targetValue)

  useEffect(() => {
    if (!session) return
    api
      .listEnvironments(session.apiKey)
      .then((envs: Environment[]) => {
        const grouped: Record<string, string[]> = {}
        for (const env of envs) {
          const project = env.spec?.project
          if (!project) continue
          ;(grouped[project] ??= []).push(env.metadata.name)
        }
        setEnvironmentsByProject(grouped)
      })
      .catch(() => setEnvironmentsByProject({}))
  }, [session])

  const loadRequests = useCallback(() => {
    if (!session) return
    setRequestsError(null)
    const call = target
      ? api.listScopedRequests(session.apiKey, target.project, target.environment, name)
      : api.listRequests(session.apiKey, name)
    call.then(setRequests).catch((err) => setRequestsError(err instanceof Error ? err.message : 'Failed to load requests'))
  }, [session, name, target?.project, target?.environment])

  useEffect(() => {
    if (!session) return
    setEntryError(null)
    api
      .getPromise(session.apiKey, name)
      .then(setEntry)
      .catch((err) => setEntryError(err instanceof Error ? err.message : 'Failed to load service'))
  }, [session, name])

  // Provisioning happens asynchronously on the cluster, so poll for status
  // updates rather than requiring a manual refresh.
  usePolling(loadRequests, 5000, [session, name, targetValue])

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (!session) return

    if (!NAME_PATTERN.test(requestName)) {
      toast.error('Name must be lowercase alphanumeric with dashes, e.g. "my-database"')
      return
    }

    setSubmitting(true)
    try {
      if (target) {
        await api.submitScopedRequest(session.apiKey, target.project, target.environment, name, requestName, spec)
      } else {
        await api.submitRequest(session.apiKey, name, requestName, spec)
      }
      toast.success(`Request "${requestName}" submitted`)
      setRequestName('')
      setSpec({})
      loadRequests()
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        toast.error(`A request named "${requestName}" already exists`)
      } else {
        toast.error(err instanceof Error ? err.message : 'Failed to submit request')
      }
    } finally {
      setSubmitting(false)
    }
  }

  async function handleDelete(reqName: string) {
    if (!session) return
    setDeletingName(reqName)
    try {
      if (target) {
        await api.deleteScopedRequest(session.apiKey, target.project, target.environment, name, reqName)
      } else {
        await api.deleteRequest(session.apiKey, name, reqName)
      }
      toast.success(`Deleted "${reqName}"`)
      loadRequests()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Failed to delete request')
    } finally {
      setDeletingName(null)
    }
  }

  if (entryError) {
    return (
      <div className="space-y-4">
        <Button variant="ghost" size="sm" onClick={() => navigate('/catalog')}>
          <ArrowLeft className="size-4" /> Back to catalog
        </Button>
        <Alert variant="destructive">
          <TriangleAlert className="size-4" />
          <AlertTitle>Couldn't load this service</AlertTitle>
          <AlertDescription>{entryError}</AlertDescription>
        </Alert>
      </div>
    )
  }

  if (!entry) {
    return (
      <div className="space-y-4">
        <Skeleton className="h-8 w-64" />
        <Skeleton className="h-24 w-full" />
        <Skeleton className="h-64 w-full" />
      </div>
    )
  }

  const specSchema = entry.schema?.properties?.spec
  const projectNames = Object.keys(environmentsByProject).sort()

  return (
    <div className="space-y-6">
      <div>
        <Link to="/catalog">
          <Button variant="ghost" size="sm" className="-ml-2 mb-2">
            <ArrowLeft className="size-4" /> Back to catalog
          </Button>
        </Link>
        <div className="flex items-center gap-3">
          <h1 className="text-2xl font-semibold tracking-tight">{entry.displayName}</h1>
          <Badge variant="secondary">{entry.kind}</Badge>
        </div>
        <p className="mt-1 text-muted-foreground">{entry.description || 'No description provided.'}</p>
      </div>

      <div className="max-w-xs space-y-1.5">
        <Label htmlFor="target-select">Target</Label>
        <Select value={targetValue} onValueChange={setTargetValue}>
          <SelectTrigger id="target-select" className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={DEFAULT_TARGET}>Team default ({session?.team})</SelectItem>
            {projectNames.map((project) => (
              <SelectGroup key={project}>
                <SelectLabel>{project}</SelectLabel>
                {environmentsByProject[project].map((environment) => (
                  <SelectItem key={environment} value={encodeTarget(project, environment)}>
                    {project} / {environment}
                  </SelectItem>
                ))}
              </SelectGroup>
            ))}
          </SelectContent>
        </Select>
        <p className="text-xs text-muted-foreground">
          {target
            ? `Requests land in project-${target.project}-${target.environment}.`
            : `Requests land in ${session?.team}'s default namespace. Manage projects/environments from the Projects page.`}
        </p>
      </div>

      <Tabs defaultValue="request">
        <TabsList>
          <TabsTrigger value="request">New request</TabsTrigger>
          <TabsTrigger value="requests">
            Your requests {requests && requests.length > 0 && <Badge variant="secondary">{requests.length}</Badge>}
          </TabsTrigger>
        </TabsList>

        <TabsContent value="request">
          <Card>
            <CardHeader>
              <CardTitle className="text-base">Request {entry.displayName}</CardTitle>
              <CardDescription>
                Fill in the details below to submit a new request as {session?.team}
                {target ? ` into ${target.project} / ${target.environment}` : ''}.
              </CardDescription>
            </CardHeader>
            <CardContent>
              <form className="space-y-4" onSubmit={handleSubmit}>
                <div className="space-y-1.5">
                  <Label htmlFor="request-name">Request name</Label>
                  <Input
                    id="request-name"
                    placeholder="my-database"
                    value={requestName}
                    onChange={(e) => setRequestName(e.target.value)}
                    required
                  />
                  <p className="text-xs text-muted-foreground">Lowercase letters, numbers, and dashes only.</p>
                </div>

                <SchemaForm schema={specSchema} value={spec} onChange={setSpec} />

                <Button type="submit" disabled={submitting}>
                  {submitting && <Loader2 className="size-4 animate-spin" />}
                  Submit request
                </Button>
              </form>
            </CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="requests">
          {requestsError && (
            <Alert variant="destructive">
              <TriangleAlert className="size-4" />
              <AlertTitle>Couldn't load requests</AlertTitle>
              <AlertDescription>{requestsError}</AlertDescription>
            </Alert>
          )}

          {!requests && !requestsError && <Skeleton className="h-32 w-full" />}

          {requests && requests.length === 0 && (
            <div className="rounded-xl border border-dashed py-12 text-center text-sm text-muted-foreground">
              No requests yet for {entry.displayName}
              {target ? ` in ${target.project} / ${target.environment}` : ''}. Submit one from the "New request" tab.
            </div>
          )}

          {requests && requests.length > 0 && (
            <Card>
              <RequestsTable requests={requests} onDelete={handleDelete} deletingName={deletingName} />
            </Card>
          )}
        </TabsContent>
      </Tabs>
    </div>
  )
}
