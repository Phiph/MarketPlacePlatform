import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { toast } from 'sonner'
import { Store } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from '@/components/ui/card'
import { Separator } from '@/components/ui/separator'
import { useAuth } from '@/lib/auth'
import { api, ApiError } from '@/lib/api'

// Demo-only shortcuts matching broker/config/teams.yaml, documented in the
// repo README - not a real secret, just a convenience for local dev.
const DEMO_TEAMS = [
  { team: 'team-payments', apiKey: 'demo-key-payments' },
  { team: 'team-checkout', apiKey: 'demo-key-checkout' },
]

export function LoginPage() {
  const { login } = useAuth()
  const navigate = useNavigate()
  const [team, setTeam] = useState('')
  const [apiKey, setApiKey] = useState('')
  const [loading, setLoading] = useState(false)

  async function signIn(team: string, apiKey: string) {
    if (!team.trim() || !apiKey.trim()) {
      toast.error('Enter both a team name and an API key')
      return
    }
    setLoading(true)
    try {
      await api.listPromises(apiKey)
      login({ team: team.trim(), apiKey: apiKey.trim() })
      navigate('/catalog')
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        toast.error('That API key was rejected by the broker')
      } else {
        toast.error(err instanceof Error ? err.message : 'Could not reach the broker')
      }
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="flex min-h-svh items-center justify-center bg-muted/30 p-4">
      <Card className="w-full max-w-sm">
        <CardHeader className="items-center text-center">
          <div className="mb-2 flex size-10 items-center justify-center rounded-lg bg-primary text-primary-foreground">
            <Store className="size-5" />
          </div>
          <CardTitle className="text-xl">Service Marketplace</CardTitle>
          <CardDescription>Sign in with your team's API key to browse and request services.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <form
            className="space-y-4"
            onSubmit={(e) => {
              e.preventDefault()
              void signIn(team, apiKey)
            }}
          >
            <div className="space-y-2">
              <Label htmlFor="team">Team</Label>
              <Input
                id="team"
                placeholder="team-payments"
                value={team}
                onChange={(e) => setTeam(e.target.value)}
                autoComplete="username"
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="apiKey">API key</Label>
              <Input
                id="apiKey"
                type="password"
                placeholder="••••••••••••"
                value={apiKey}
                onChange={(e) => setApiKey(e.target.value)}
                autoComplete="current-password"
              />
            </div>
            <Button type="submit" className="w-full" disabled={loading}>
              {loading ? 'Signing in…' : 'Sign in'}
            </Button>
          </form>

          <div className="flex items-center gap-3">
            <Separator className="flex-1" />
            <span className="text-xs text-muted-foreground">demo teams</span>
            <Separator className="flex-1" />
          </div>

          <div className="grid grid-cols-2 gap-2">
            {DEMO_TEAMS.map((demo) => (
              <Button
                key={demo.team}
                type="button"
                variant="outline"
                size="sm"
                disabled={loading}
                onClick={() => {
                  setTeam(demo.team)
                  setApiKey(demo.apiKey)
                  void signIn(demo.team, demo.apiKey)
                }}
              >
                {demo.team}
              </Button>
            ))}
          </div>
        </CardContent>
        <CardFooter className="justify-center">
          <p className="text-xs text-muted-foreground">Backed by the marketplace broker · not real authentication</p>
        </CardFooter>
      </Card>
    </div>
  )
}
