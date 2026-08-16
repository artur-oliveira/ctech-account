'use client'

import { useInfiniteQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { fetchActivity } from '@/lib/queries'
import { formatDistanceToNow } from '@/lib/format'
import { Button } from '@/components/ui/button'
import { ResponsiveDataList, type Column } from '@/components/responsive-data-list'
import type { ActivityEvent } from '@/lib/types'
import { QueryError } from '@/components/query-error'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'

type ActivityFilter = 'all' | 'sign-in' | 'security' | 'developer'

function matchesFilter(event: ActivityEvent, filter: ActivityFilter) {
  if (filter === 'all') return true
  if (filter === 'sign-in') return event.event_type.startsWith('login_') || event.event_type.startsWith('mfa_')
  if (filter === 'security') return event.event_type.startsWith('password_') || event.event_type.startsWith('passkey_') || event.event_type.startsWith('totp_') || event.event_type.startsWith('session_') || event.event_type.startsWith('stepup_')
  return event.event_type.startsWith('apikey_') || event.event_type.startsWith('oauth_client_') || event.event_type.startsWith('consent_')
}

export default function ActivityPage() {
  const { t, i18n } = useTranslation()
  const [filter, setFilter] = useState<ActivityFilter>('all')
  const { data, isLoading, isError, error, refetch, hasNextPage, isFetchingNextPage, fetchNextPage } = useInfiniteQuery({
    queryKey: ['activity'],
    queryFn: ({ pageParam }) => fetchActivity(pageParam),
    initialPageParam: '',
    getNextPageParam: (last) => (last.next_cursor ? last.next_cursor : undefined),
  })

  if (isLoading) {
    return (
      <div className="space-y-3">
        {[...Array(5)].map((_, i) => (
          <div key={i} className="h-16 animate-pulse bg-muted rounded-lg" />
        ))}
      </div>
    )
  }

  if (isError) {
    return <QueryError error={error} onRetry={() => refetch()} />
  }

  const events = (data?.pages.flatMap((p) => p.events) ?? []).filter((event) => matchesFilter(event, filter))

  const columns: Column<ActivityEvent>[] = [
    {
      key: 'event',
      header: t('activity.event'),
      title: true,
      cell: (e) => {
        const key = `activity.events.${e.event_type.replace(/_/g, '.')}`
        const label = i18n.exists(key) ? t(key) : e.event_type
        return <span className="text-sm font-medium">{label}</span>
      },
    },
    {
      key: 'detail',
      header: t('activity.detail'),
      cell: (e) => {
        const detail = e.metadata?.client_id || e.metadata?.device_name || e.metadata?.method || e.user_agent
        return (
          <span className="text-sm text-muted-foreground">
            {[e.ip, detail].filter(Boolean).join(' · ')}
          </span>
        )
      },
    },
    {
      key: 'time',
      header: t('activity.time'),
      align: 'right',
      cell: (e) => (
        <span className="text-sm text-muted-foreground">{formatDistanceToNow(e.created_at)}</span>
      ),
    },
  ]

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold">{t('activity.title')}</h1>
        <p className="text-muted-foreground text-sm mt-1">{t('activity.subtitle')}</p>
      </div>

      <div className="w-full sm:w-56">
        <Select value={filter} onValueChange={(value) => setFilter((value ?? 'all') as ActivityFilter)}>
          <SelectTrigger aria-label={t('activity.filterLabel')}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">{t('activity.filters.all')}</SelectItem>
            <SelectItem value="sign-in">{t('activity.filters.signIn')}</SelectItem>
            <SelectItem value="security">{t('activity.filters.security')}</SelectItem>
            <SelectItem value="developer">{t('activity.filters.developer')}</SelectItem>
          </SelectContent>
        </Select>
      </div>

      <ResponsiveDataList
        rows={events}
        columns={columns}
        rowKey={(e) => `${e.created_at}-${e.event_type}-${e.ip}-${e.user_agent}`}
        empty={<p className="text-muted-foreground text-sm">{t('activity.noEvents')}</p>}
      />

      {hasNextPage && (
        <Button variant="outline" onClick={() => fetchNextPage()} disabled={isFetchingNextPage}>
          {isFetchingNextPage ? t('activity.loading') : t('activity.loadMore')}
        </Button>
      )}
    </div>
  )
}
