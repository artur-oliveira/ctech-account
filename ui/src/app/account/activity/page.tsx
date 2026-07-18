'use client'

import { useInfiniteQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { fetchActivity } from '@/lib/queries'
import { formatDistanceToNow } from '@/lib/format'
import { Button } from '@/components/ui/button'
import { ResponsiveDataList, type Column } from '@/components/responsive-data-list'
import type { ActivityEvent } from '@/lib/types'
import { QueryError } from '@/components/query-error'

export default function ActivityPage() {
  const { t, i18n } = useTranslation()
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

  const events = data?.pages.flatMap((p) => p.events) ?? []

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
        const detail = e.metadata?.client_id || e.metadata?.device_name || e.metadata?.method
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

      <ResponsiveDataList
        rows={events}
        columns={columns}
        rowKey={(e) => `${e.created_at}-${e.event_type}-${e.ip}`}
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
