import { getProfile, getSessions } from '@/lib/api'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { formatDistanceToNow } from '@/lib/format'

export default async function DashboardPage() {
  const [user, sessions] = await Promise.all([getProfile(), getSessions()])

  if (!user) return null

  const currentSession = sessions.find((s) => s.is_current)

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold">
          Welcome back, {user.display_name ?? user.first_name}
        </h1>
        <p className="text-muted-foreground text-sm mt-1">
          {user.email}
          {!user.email_verified && (
            <Badge variant="secondary" className="ml-2 text-xs">Unverified</Badge>
          )}
        </p>
      </div>

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        <Card>
          <CardHeader className="pb-2">
            <CardDescription>Active sessions</CardDescription>
            <CardTitle className="text-3xl">{sessions.length}</CardTitle>
          </CardHeader>
          <CardContent>
            <a href="/account/sessions" className="text-sm text-primary hover:underline">
              Manage sessions →
            </a>
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="pb-2">
            <CardDescription>Current session</CardDescription>
            <CardTitle className="text-base truncate">
              {currentSession?.device_name ?? 'Unknown device'}
            </CardTitle>
          </CardHeader>
          <CardContent>
            <p className="text-sm text-muted-foreground">
              {currentSession ? formatDistanceToNow(currentSession.last_used_at) : '—'}
            </p>
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="pb-2">
            <CardDescription>Account created</CardDescription>
            <CardTitle className="text-base">
              {formatDistanceToNow(user.created_at)}
            </CardTitle>
          </CardHeader>
          <CardContent>
            <a href="/account/security" className="text-sm text-primary hover:underline">
              Security settings →
            </a>
          </CardContent>
        </Card>
      </div>
    </div>
  )
}
