import { useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { ArrowRight, PackageSearch, Search, TriangleAlert } from 'lucide-react'
import { Input } from '@/components/ui/input'
import { Card, CardDescription, CardFooter, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Skeleton } from '@/components/ui/skeleton'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { useAuth } from '@/lib/auth'
import { api } from '@/lib/api'
import type { CatalogEntry } from '@/lib/types'

export function CatalogPage() {
  const { session } = useAuth()
  const [entries, setEntries] = useState<CatalogEntry[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [query, setQuery] = useState('')

  useEffect(() => {
    if (!session) return
    let cancelled = false
    setError(null)
    api
      .listPromises(session.apiKey)
      .then((data) => !cancelled && setEntries(data))
      .catch((err) => !cancelled && setError(err instanceof Error ? err.message : 'Failed to load catalog'))
    return () => {
      cancelled = true
    }
  }, [session])

  const filtered = useMemo(() => {
    if (!entries) return null
    const q = query.trim().toLowerCase()
    if (!q) return entries
    return entries.filter(
      (e) => e.displayName.toLowerCase().includes(q) || e.description?.toLowerCase().includes(q) || e.name.toLowerCase().includes(q),
    )
  }, [entries, query])

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Service Catalog</h1>
        <p className="text-muted-foreground">Browse and request the services available to {session?.team}.</p>
      </div>

      <div className="relative max-w-sm">
        <Search className="absolute left-2.5 top-2.5 size-4 text-muted-foreground" />
        <Input
          placeholder="Search services…"
          className="pl-8"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
        />
      </div>

      {error && (
        <Alert variant="destructive">
          <TriangleAlert className="size-4" />
          <AlertTitle>Couldn't load the catalog</AlertTitle>
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      {!entries && !error && (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {Array.from({ length: 6 }).map((_, i) => (
            <Skeleton key={i} className="h-40 rounded-xl" />
          ))}
        </div>
      )}

      {filtered && filtered.length === 0 && (
        <div className="flex flex-col items-center justify-center gap-2 rounded-xl border border-dashed py-16 text-center">
          <PackageSearch className="size-8 text-muted-foreground" />
          <p className="font-medium">No services found</p>
          <p className="text-sm text-muted-foreground">
            {entries && entries.length > 0 ? 'Try a different search.' : 'Nothing has been published to the catalog yet.'}
          </p>
        </div>
      )}

      {filtered && filtered.length > 0 && (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {filtered.map((entry) => (
            <Link key={entry.name} to={`/catalog/${entry.name}`}>
              <Card className="h-full transition-colors hover:border-primary/50 hover:shadow-sm">
                <CardHeader>
                  <div className="flex items-start justify-between gap-2">
                    <CardTitle className="text-base">{entry.displayName}</CardTitle>
                    <Badge variant="secondary" className="shrink-0">
                      {entry.kind}
                    </Badge>
                  </div>
                  <CardDescription className="line-clamp-3">
                    {entry.description || 'No description provided.'}
                  </CardDescription>
                </CardHeader>
                <CardFooter className="mt-auto justify-between text-sm text-muted-foreground">
                  <span className="font-mono text-xs">{entry.version}</span>
                  <span className="inline-flex items-center gap-1 font-medium text-primary">
                    View <ArrowRight className="size-3.5" />
                  </span>
                </CardFooter>
              </Card>
            </Link>
          ))}
        </div>
      )}
    </div>
  )
}
