import { Badge } from '@/components/ui/badge'
import type { RequestVersionInfo } from '@/lib/types'
import { cn } from '@/lib/utils'

// Styled like StatusBadge (colored dot + label) - the established "compact
// evidence chip" idiom in this codebase. undefined means "not loaded yet"
// (the caller is still fetching it), not "no versioning" - every Promise in
// this repo carries a kratix.io/promise-version label, so there's no
// meaningful "not applicable" state to distinguish from "loading". An empty
// boundVersion (the broker can legitimately return
// {boundVersion:"",latestVersion:"",upgradeAvailable:false} when no
// revision is marked latest) degrades to the same placeholder rather than
// rendering a bare, label-less dot.
export function PromiseVersionBadge({ info }: { info: RequestVersionInfo | undefined }) {
  if (!info?.boundVersion) return <span className="text-sm text-muted-foreground">—</span>

  return (
    <Badge variant="outline" className="gap-1.5 font-normal">
      <span className={cn('size-1.5 rounded-full', info.upgradeAvailable ? 'bg-amber-500' : 'bg-emerald-500')} />
      <span className="font-mono">{info.boundVersion}</span>
      {info.upgradeAvailable && (
        <span className="text-muted-foreground">
          &rarr; {info.latestVersion} available
        </span>
      )}
    </Badge>
  )
}
