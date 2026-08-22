'use client'

import { FormEvent, Suspense, useState } from 'react'
import Link from 'next/link'
import { ArrowLeft, User, Headset, Info } from 'lucide-react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { useSearchParams } from 'next/navigation'
import { useTranslation } from 'react-i18next'
import { fetchAdminSupportTicket } from '@/lib/queries'
import { replyAdminSupportTicketAPI, setAdminSupportTicketStatusAPI } from '@/lib/mutations'
import { formatDate } from '@/lib/format'
import { QueryError } from '@/components/query-error'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import type { SupportMessage } from '@/lib/types'

const STATUS_VARIANT = {
  open: 'default',
  answered: 'secondary',
  closed: 'outline',
} as const

const STATUS_VALUES = ['open', 'answered', 'closed'] as const

function MessageAuthorIcon({ authorType }: { authorType: SupportMessage['author_type'] }) {
  if (authorType === 'agent') return <Headset className="size-4" />
  if (authorType === 'system') return <Info className="size-4" />
  return <User className="size-4" />
}

function AdminTicketThread() {
  const { t } = useTranslation()
  const search = useSearchParams()
  const id = search.get('id') ?? ''
  const [body, setBody] = useState('')

  const q = useQuery({
    queryKey: ['admin-support-ticket', id],
    queryFn: () => fetchAdminSupportTicket(id),
    enabled: Boolean(id),
  })

  const reply = useMutation({
    mutationFn: () => replyAdminSupportTicketAPI(id, body),
    onSuccess: () => {
      setBody('')
      q.refetch()
    },
  })

  const setStatus = useMutation({
    mutationFn: (status: string) => setAdminSupportTicketStatusAPI(id, status),
    onSuccess: () => q.refetch(),
  })

  if (!id) return <p className="text-sm text-muted-foreground">{t('support.ticket.invalid')}</p>
  if (q.isLoading) return <p className="text-sm text-muted-foreground animate-pulse">{t('support.ticket.loading')}</p>
  if (q.isError || !q.data) return <QueryError error={q.error} onRetry={() => q.refetch()} />

  const { ticket, messages } = q.data

  function submitReply(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    reply.mutate()
  }

  return (
    <div className="space-y-6">
      <Link href="/admin/support" className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground">
        <ArrowLeft className="size-4" />
        {t('support.admin.back')}
      </Link>

      <div className="flex flex-wrap items-center justify-between gap-4">
        <div className="space-y-1">
          <p className="text-sm text-muted-foreground">#{ticket.ticket_number}</p>
          <h1 className="text-2xl font-semibold">{ticket.subject_other || t(`support.categories.${ticket.subject_category}`)}</h1>
          <p className="text-sm capitalize text-muted-foreground">{t(`support.priority.${ticket.priority}`)}</p>
        </div>
        <div className="flex items-center gap-2">
          <Badge variant={STATUS_VARIANT[ticket.status]}>{t(`support.ticket.status.${ticket.status}`)}</Badge>
          <Select value={ticket.status} onValueChange={(value) => value && setStatus.mutate(value)}>
            <SelectTrigger aria-label={t('support.admin.statusLabel')} className="w-40">
              <SelectValue />
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
      </div>

      <section className="space-y-3">
        {messages.map((m, i) => (
          <div key={`${m.created_at}-${i}`} className="rounded-xl bg-card p-4 ring-1 ring-foreground/10">
            <div className="mb-2 flex items-center gap-2 text-xs font-medium text-muted-foreground">
              <MessageAuthorIcon authorType={m.author_type} />
              <span>{t(`support.ticket.author${m.author_type.charAt(0).toUpperCase()}${m.author_type.slice(1)}`)}</span>
              <span aria-hidden="true">·</span>
              <span>{formatDate(m.created_at)}</span>
            </div>
            <p className="whitespace-pre-wrap text-sm">{m.body}</p>
          </div>
        ))}
      </section>

      <form onSubmit={submitReply} className="space-y-2">
        <textarea
          value={body}
          onChange={(e) => setBody(e.target.value)}
          minLength={15}
          maxLength={4000}
          required
          className="min-h-32 w-full rounded-lg border border-input bg-transparent p-3 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/70"
          placeholder={t('support.admin.replyPlaceholder')}
        />
        <Button type="submit" disabled={reply.isPending}>
          {reply.isPending ? t('support.admin.replying') : t('support.admin.reply')}
        </Button>
      </form>
    </div>
  )
}

export default function AdminTicketPage() {
  const { t } = useTranslation()
  return (
    <Suspense fallback={<p className="text-sm text-muted-foreground animate-pulse">{t('common.loading')}</p>}>
      <AdminTicketThread />
    </Suspense>
  )
}
