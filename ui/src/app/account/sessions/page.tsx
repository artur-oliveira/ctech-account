import { getSessions } from '@/lib/api'
import { formatDistanceToNow, formatDate } from '@/lib/format'
import { Card, CardContent } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { RevokeSessionButton, RevokeAllButton } from './session-actions'
import { MonitorSmartphone } from 'lucide-react'

export default async function SessionsPage() {
  const sessions = await getSessions()

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold">Sessions</h1>
          <p className="text-muted-foreground text-sm mt-1">
            Devices currently signed in to your account.
          </p>
        </div>
        {sessions.length > 1 && <RevokeAllButton />}
      </div>

      <div className="space-y-3">
        {sessions.map((session) => (
          <Card key={session.session_id}>
            <CardContent className="flex items-center gap-4 py-4">
              <MonitorSmartphone className="size-8 shrink-0 text-muted-foreground" />
              <div className="flex-1 min-w-0">
                <div className="flex items-center gap-2">
                  <p className="text-sm font-medium truncate">{session.device_name}</p>
                  {session.is_current && (
                    <Badge variant="secondary" className="text-xs shrink-0">Current</Badge>
                  )}
                </div>
                <p className="text-xs text-muted-foreground mt-0.5">
                  {session.ip_address} · Last active {formatDistanceToNow(session.last_used_at)}
                </p>
                <p className="text-xs text-muted-foreground">
                  Created {formatDate(session.created_at)}
                </p>
              </div>
              {!session.is_current && (
                <RevokeSessionButton sessionId={session.session_id} />
              )}
            </CardContent>
          </Card>
        ))}
        {sessions.length === 0 && (
          <p className="text-muted-foreground text-sm">No active sessions found.</p>
        )}
      </div>
    </div>
  )
}
