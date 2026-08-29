'use client'

import { useState } from 'react'
import Link from 'next/link'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { fetchAdminSupportMetrics, fetchAdminSupportTickets } from '@/lib/queries'
import { formatDistanceToNow } from '@/lib/format'
import { QueryError } from '@/components/query-error'
import { Badge } from '@/components/ui/badge'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { ResponsiveDataList, type Column } from '@/components/responsive-data-list'
import type { SupportTicket } from '@/lib/types'

const STATUS_VARIANT = {
  open: 'default',
  answered: 'secondary',
  closed: 'outline',
} as const

const STATUS_VALUES = ['open', 'answered', 'closed'] as const

export default function AdminSupportPage() {
  const { t } = useTranslation()
  const [status, setStatus] = useState<(typeof STATUS_VALUES)[number]>('open')
  const q = useQuery({
    queryKey: ['admin-support', status],
    queryFn: () => fetchAdminSupportTickets(status),
  })
  const metrics = useQuery({queryKey: ['admin-support-metrics'], queryFn: fetchAdminSupportMetrics})

  const periodLabel = (period: string) => period === 'all' ? t('support.admin.metrics.all')
    : period.startsWith('day#') ? t('support.admin.metrics.day')
      : period.startsWith('month#') ? t('support.admin.metrics.month') : t('support.admin.metrics.year')
  const duration = (seconds: number) => seconds < 3600
    ? t('support.admin.metrics.minutes', {count: Math.round(seconds / 60)})
    : t('support.admin.metrics.hours', {count: Math.round(seconds / 3600)})

  const columns: Column<SupportTicket>[] = [
    {
      key: 'subject',
      header: t('support.categoryLabel'),
      title: true,
      cell: (ticket) => (
        <Link href={`/admin/support/ticket?id=${encodeURIComponent(ticket.id.replace('TICKET_', ''))}`} className="text-sm font-medium hover:underline">
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
      key: 'escalation',
      header: t('support.admin.escalationLabel'),
      cell: (ticket) => ticket.escalation_level && ticket.escalation_level !== 'none'
        ? <Badge variant="outline">{t(`support.admin.escalation.${ticket.escalation_level}`)}</Badge>
        : <span className="text-sm text-muted-foreground">—</span>,
    },
    {
      key: 'lastMessage',
      header: t('activity.time'),
      align: 'right',
      cell: (ticket) => <span className="text-sm text-muted-foreground">{formatDistanceToNow(ticket.last_message_at)}</span>,
    },
  ]

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold">{t('support.admin.title')}</h1>
          <p className="text-sm text-muted-foreground">{t('support.admin.subtitle')}</p>
        </div>
        <Select value={status} onValueChange={(value) => setStatus((value ?? 'open') as (typeof STATUS_VALUES)[number])}>
          <SelectTrigger aria-label={t('support.admin.statusFilter')} className="w-40">
            <SelectValue>
              {status ? t(`support.ticket.status.${status}`) : status}
            </SelectValue>
          </SelectTrigger>
          <SelectContent>
            {STATUS_VALUES.map((s) => (
              <SelectItem key={s} value={s}>
                {t(`support.ticket.status.${s}`)}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>

      {metrics.data?.buckets?.length ? (
        <section aria-labelledby="support-metrics-title" className="overflow-hidden rounded-xl bg-card ring-1 ring-foreground/10">
          <div className="border-b border-border px-4 py-3">
            <h2 id="support-metrics-title" className="font-medium">{t('support.admin.metrics.title')}</h2>
            <p className="text-sm text-muted-foreground">{t('support.admin.metrics.subtitle')}</p>
          </div>
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead className="text-left text-muted-foreground">
                <tr><th className="px-4 py-2 font-medium">{t('support.admin.metrics.period')}</th><th className="px-4 py-2 font-medium">{t('support.admin.metrics.created')}</th><th className="px-4 py-2 font-medium">{t('support.admin.metrics.resolved')}</th><th className="px-4 py-2 font-medium">{t('support.admin.metrics.average')}</th><th className="px-4 py-2 font-medium">{t('support.admin.metrics.leadingProduct')}</th></tr>
              </thead>
              <tbody>
                {metrics.data.buckets.map((bucket) => {
                  const leading = Object.entries(bucket.tickets_by_product).sort((a, b) => b[1] - a[1])[0]
                  return <tr key={bucket.period} className="border-t border-border/70">
                    <td className="px-4 py-2.5 font-medium">{periodLabel(bucket.period)}</td>
                    <td className="px-4 py-2.5 tabular-nums">{bucket.created_count}</td>
                    <td className="px-4 py-2.5 tabular-nums">{bucket.resolved_count}</td>
                    <td className="px-4 py-2.5 tabular-nums">{duration(bucket.average_resolution_seconds)}</td>
                    <td className="px-4 py-2.5">{leading && leading[1] > 0 ? `${t(`support.categories.${leading[0]}`)} · ${leading[1]}` : '—'}</td>
                  </tr>
                })}
              </tbody>
            </table>
          </div>
        </section>
      ) : null}

      {q.isLoading ? (
        <div className="space-y-3">
          {[...Array(3)].map((_, i) => (
            <div key={i} className="h-16 animate-pulse rounded-lg bg-muted" />
          ))}
        </div>
      ) : q.isError ? (
        <QueryError error={q.error} onRetry={() => q.refetch()} />
      ) : (
        <ResponsiveDataList
          rows={q.data?.tickets ?? []}
          columns={columns}
          rowKey={(ticket) => ticket.id}
          empty={<div className="rounded-lg border border-dashed p-8 text-center text-sm text-muted-foreground">{t('support.admin.empty')}</div>}
        />
      )}
    </div>
  )
}
