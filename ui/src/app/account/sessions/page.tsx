'use client'

import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { fetchSessions } from '@/lib/queries'
import { formatDistanceToNow, formatDate } from '@/lib/format'
import { Card, CardContent } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { RevokeSessionButton, RevokeAllButton } from './session-actions'
import { MonitorSmartphone } from 'lucide-react'

export default function SessionsPage() {
  const { t } = useTranslation()
  const { data: sessions = [], isLoading } = useQuery({
    queryKey: ['sessions'],
    queryFn: fetchSessions,
  })

  if (isLoading) {
    return (
      <div className="space-y-3">
        {[...Array(3)].map((_, i) => (
          <div key={i} className="h-20 animate-pulse bg-muted rounded-lg" />
        ))}
      </div>
    )
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold">{t('sessions.title')}</h1>
          <p className="text-muted-foreground text-sm mt-1">{t('sessions.subtitle')}</p>
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
                    <Badge variant="secondary" className="text-xs shrink-0">{t('sessions.current')}</Badge>
                  )}
                </div>
                <p className="text-xs text-muted-foreground mt-0.5">
                  {session.ip_address} · {t('sessions.lastActive', { time: formatDistanceToNow(session.last_used_at) })}
                </p>
                <p className="text-xs text-muted-foreground">
                  {t('sessions.created', { date: formatDate(session.created_at) })}
                </p>
              </div>
              {!session.is_current && (
                <RevokeSessionButton sessionId={session.session_id} />
              )}
            </CardContent>
          </Card>
        ))}
        {sessions.length === 0 && (
          <p className="text-muted-foreground text-sm">{t('sessions.noSessions')}</p>
        )}
      </div>
    </div>
  )
}
