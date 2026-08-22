'use client'

import Link from 'next/link'
import { useInfiniteQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { fetchMySupportTickets } from '@/lib/queries'
import { formatDistanceToNow } from '@/lib/format'
import { QueryError } from '@/components/query-error'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { ResponsiveDataList, type Column } from '@/components/responsive-data-list'
import type { SupportTicket } from '@/lib/types'

const STATUS_VARIANT = {
  open: 'default',
  answered: 'secondary',
  closed: 'outline',
} as const

export default function MySupportTicketsPage() {
  const { t } = useTranslation()
  const { data, isLoading, isError, error, refetch, hasNextPage, isFetchingNextPage, fetchNextPage } = useInfiniteQuery({
    queryKey: ['my-support-tickets'],
    queryFn: ({ pageParam }) => fetchMySupportTickets(pageParam),
    initialPageParam: '',
    getNextPageParam: (last) => (last.next_cursor ? last.next_cursor : undefined),
  })

  const tickets = data?.pages.flatMap((p) => p.tickets) ?? []

  const columns: Column<SupportTicket>[] = [
    {
      key: 'subject',
      header: t('support.categoryLabel'),
      title: true,
      cell: (ticket) => (
        <Link href={`/support/ticket?id=${encodeURIComponent(ticket.id.replace('TICKET_', ''))}`} className="text-sm font-medium hover:underline">
          #{ticket.ticket_number} · {ticket.subject_other || t(`support.categories.${ticket.subject_category}`)}
        </Link>
      ),
    },
    {
      key: 'priority',
      header: t('support.mine.priority'),
      cell: (ticket) => <span className="text-sm capitalize">{t(`support.priority.${ticket.priority}`)}</span>,
    },
    {
      key: 'status',
      header: t('support.admin.statusLabel'),
      cell: (ticket) => <Badge variant={STATUS_VARIANT[ticket.status]}>{t(`support.ticket.status.${ticket.status}`)}</Badge>,
    },
    {
      key: 'lastMessage',
      header: t('activity.time'),
      align: 'right',
      cell: (ticket) => <span className="text-sm text-muted-foreground">{formatDistanceToNow(ticket.last_message_at)}</span>,
    },
  ]

  if (isLoading) {
    return (
      <div className="space-y-3">
        {[...Array(3)].map((_, i) => (
          <div key={i} className="h-16 animate-pulse rounded-lg bg-muted" />
        ))}
      </div>
    )
  }

  if (isError) {
    return <QueryError error={error} onRetry={() => refetch()} />
  }

  return (
    <div className="space-y-6">
      <div className="flex items-end justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold">{t('support.mine.title')}</h1>
          <p className="text-sm text-muted-foreground">{t('support.mine.subtitle')}</p>
        </div>
        <Button render={<Link href="/support" />}>{t('support.mine.newTicket')}</Button>
      </div>

      <ResponsiveDataList
        rows={tickets}
        columns={columns}
        rowKey={(ticket) => ticket.id}
        empty={
          <div className="space-y-3 rounded-lg border border-dashed p-8 text-center">
            <p className="text-sm text-muted-foreground">{t('support.mine.empty')}</p>
            <Button variant="outline" render={<Link href="/support" />}>
              {t('support.mine.emptyCta')}
            </Button>
          </div>
        }
      />

      {hasNextPage && (
        <Button variant="outline" onClick={() => fetchNextPage()} disabled={isFetchingNextPage}>
          {isFetchingNextPage ? t('support.mine.loading') : t('support.mine.loadMore')}
        </Button>
      )}
    </div>
  )
}
