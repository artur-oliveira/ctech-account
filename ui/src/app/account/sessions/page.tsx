'use client'

import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { fetchSessions } from '@/lib/queries'
import { formatDistanceToNow, formatDate } from '@/lib/format'
import { QueryError } from '@/components/query-error'
import { Badge } from '@/components/ui/badge'
import { ResponsiveDataList, type Column } from '@/components/responsive-data-list'
import type { Session } from '@/lib/types'
import { RevokeSessionButton, RevokeAllButton } from './session-actions'

export default function SessionsPage() {
  const { t } = useTranslation()
  const { data: sessions = [], isLoading, isError, error, refetch } = useQuery({
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

  if (isError) {
    return <QueryError error={error} onRetry={() => refetch()} />
  }

  const columns: Column<Session>[] = [
    {
      key: 'device',
      header: t('sessions.device'),
      title: true,
      cell: (s) => (
        <div className="flex items-center gap-2 min-w-0">
          <span className="text-sm font-medium truncate">{s.device_name}</span>
          {s.is_current && (
            <Badge variant="secondary" className="text-xs shrink-0">{t('sessions.current')}</Badge>
          )}
        </div>
      ),
    },
    {
      key: 'ip',
      header: t('sessions.ip'),
      cell: (s) => <span className="text-sm">{s.ip_address}</span>,
    },
    {
      key: 'lastActive',
      header: t('sessions.lastActiveShort'),
      cell: (s) => (
        <span className="text-sm text-muted-foreground">
          {formatDistanceToNow(s.last_used_at)}
        </span>
      ),
    },
    {
      key: 'created',
      header: t('sessions.createdShort'),
      cell: (s) => (
        <span className="text-sm text-muted-foreground">{formatDate(s.created_at)}</span>
      ),
    },
  ]

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold">{t('sessions.title')}</h1>
          <p className="text-muted-foreground text-sm mt-1">{t('sessions.subtitle')}</p>
        </div>
        {sessions.length > 1 && <RevokeAllButton />}
      </div>

      <ResponsiveDataList
        rows={sessions}
        columns={columns}
        rowKey={(s) => s.session_id}
        actions={(s) =>
          !s.is_current ? <RevokeSessionButton sessionId={s.session_id} /> : null
        }
        empty={<p className="text-muted-foreground text-sm">{t('sessions.noSessions')}</p>}
      />
    </div>
  )
}
