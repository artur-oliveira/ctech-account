'use client'

import { FormEvent, Suspense, useState } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { useSearchParams } from 'next/navigation'
import { useTranslation } from 'react-i18next'
import { User, Headset, Info } from 'lucide-react'
import { fetchSupportTicket } from '@/lib/queries'
import { replySupportTicketAPI, submitSupportTicketNPSAPI } from '@/lib/mutations'
import { formatDate } from '@/lib/format'
import { QueryError } from '@/components/query-error'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import type { SupportMessage } from '@/lib/types'

const STATUS_VARIANT = {
  open: 'default',
  answered: 'secondary',
  closed: 'outline',
} as const

function MessageAuthorIcon({ authorType }: { authorType: SupportMessage['author_type'] }) {
  if (authorType === 'agent') return <Headset className="size-4" />
  if (authorType === 'system') return <Info className="size-4" />
  return <User className="size-4" />
}

function TicketThread() {
  const { t } = useTranslation()
  const search = useSearchParams()
  const id = search.get('id') ?? ''
  const token = search.get('token') ?? ''
  const [body, setBody] = useState('')
  const [npsScore, setNpsScore] = useState(0)
  const [npsMessage, setNpsMessage] = useState('')
  const [npsError, setNpsError] = useState('')

  const q = useQuery({
    queryKey: ['support-ticket', id, token],
    queryFn: () => fetchSupportTicket(id, token),
    enabled: Boolean(id),
  })

  const reply = useMutation({
    mutationFn: () => replySupportTicketAPI(id, body, token),
    onSuccess: () => {
      setBody('')
      q.refetch()
    },
  })

  const nps = useMutation({
    mutationFn: () => submitSupportTicketNPSAPI(id, npsScore, npsMessage, token),
    onSuccess: () => {
      setNpsError('')
      q.refetch()
    },
    onError: () => setNpsError(t('support.genericError')),
  })

  if (!id) return <main className="p-8 text-sm text-muted-foreground">{t('support.ticket.invalid')}</main>
  if (q.isLoading) return <main className="p-8 text-sm text-muted-foreground animate-pulse">{t('support.ticket.loading')}</main>
  if (q.isError || !q.data) {
    return (
      <main className="mx-auto max-w-2xl p-8">
        <QueryError error={q.error} onRetry={() => q.refetch()} />
      </main>
    )
  }

  const { ticket, messages } = q.data
  const isClosed = ticket.status === 'closed'
  const hasNpsScore = typeof ticket.nps_score === 'number' && ticket.nps_score > 0
  const npsRequiresMessage = npsScore > 0 && npsScore <= 3

  function submitReply(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    reply.mutate()
  }

  function submitNPS(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    if (!npsScore) return
    if (npsRequiresMessage && npsMessage.trim().length < 15) {
      setNpsError(t('support.ticket.nps.messageRequired'))
      return
    }
    nps.mutate()
  }

  return (
    <main className="mx-auto max-w-2xl space-y-6 px-4 py-12">
      <div className="space-y-1">
        <p className="text-sm text-muted-foreground">#{ticket.ticket_number}</p>
        <div className="flex items-center gap-2">
          <h1 className="text-2xl font-semibold">{ticket.subject_other || t(`support.categories.${ticket.subject_category}`)}</h1>
          <Badge variant={STATUS_VARIANT[ticket.status]}>{t(`support.ticket.status.${ticket.status}`)}</Badge>
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

      {!isClosed && (
        <form onSubmit={submitReply} className="space-y-2">
          <textarea
            value={body}
            onChange={(e) => setBody(e.target.value)}
            minLength={15}
            maxLength={4000}
            required
            className="min-h-32 w-full rounded-lg border border-input bg-transparent p-3 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/70"
            placeholder={t('support.ticket.replyPlaceholder')}
          />
          <Button type="submit" disabled={reply.isPending}>
            {reply.isPending ? t('support.ticket.replying') : t('support.ticket.reply')}
          </Button>
        </form>
      )}

      {isClosed && !hasNpsScore && (
        <form onSubmit={submitNPS} className="space-y-3 rounded-xl bg-card p-4 ring-1 ring-foreground/10">
          <div>
            <h2 className="text-base font-medium">{t('support.ticket.nps.title')}</h2>
            <p className="text-sm text-muted-foreground">{t('support.ticket.nps.subtitle')}</p>
          </div>
          <div className="flex gap-2" role="radiogroup" aria-label={t('support.ticket.nps.title')}>
            {[1, 2, 3, 4, 5].map((score) => (
              <button
                key={score}
                type="button"
                role="radio"
                aria-checked={npsScore === score}
                onClick={() => setNpsScore(score)}
                className={`flex size-10 items-center justify-center rounded-lg text-sm font-medium ring-1 transition-colors ${
                  npsScore === score ? 'bg-primary text-primary-foreground ring-primary' : 'bg-transparent text-foreground ring-foreground/10 hover:bg-muted'
                }`}
              >
                {score}
              </button>
            ))}
          </div>
          {npsRequiresMessage && (
            <textarea
              value={npsMessage}
              onChange={(e) => setNpsMessage(e.target.value)}
              minLength={15}
              maxLength={1000}
              required
              className="min-h-24 w-full rounded-lg border border-input bg-transparent p-3 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/70"
              placeholder={t('support.ticket.nps.messagePlaceholder')}
            />
          )}
          {npsError && <p role="alert" className="text-sm text-destructive">{npsError}</p>}
          <Button type="submit" disabled={!npsScore || nps.isPending}>
            {nps.isPending ? t('support.ticket.nps.submitting') : t('support.ticket.nps.submit')}
          </Button>
        </form>
      )}

      {isClosed && hasNpsScore && <p className="text-sm text-muted-foreground">{t('support.ticket.nps.thanks')}</p>}
    </main>
  )
}

export default function TicketPage() {
  const { t } = useTranslation()
  return (
    <Suspense fallback={<main className="p-8 text-sm text-muted-foreground animate-pulse">{t('common.loading')}</main>}>
      <TicketThread />
    </Suspense>
  )
}
