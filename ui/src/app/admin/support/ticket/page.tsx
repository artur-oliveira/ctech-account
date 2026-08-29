'use client'

import {FormEvent, Suspense, useState} from 'react'
import Link from 'next/link'
import {ArrowLeft, Headset, Info, LockKeyhole, Radio, User} from 'lucide-react'
import {useMutation, useQuery} from '@tanstack/react-query'
import {useSearchParams} from 'next/navigation'
import {useTranslation} from 'react-i18next'
import {fetchAdminSupportTicket} from '@/lib/queries'
import {addAdminSupportInternalNoteAPI, replyAdminSupportTicketAPI, setAdminSupportEscalationAPI, setAdminSupportTicketStatusAPI} from '@/lib/mutations'
import {formatDate} from '@/lib/format'
import {QueryError} from '@/components/query-error'
import {Button} from '@/components/ui/button'
import {Badge} from '@/components/ui/badge'
import {Select, SelectContent, SelectItem, SelectTrigger, SelectValue} from '@/components/ui/select'
import type {SupportMessage} from '@/lib/types'
import {ConfirmDialog} from '@/components/confirm-dialog'
import {useSupportRealtime} from '@/lib/hooks/use-support-realtime'

const STATUS_VARIANT = {
  open: 'default',
  answered: 'secondary',
  closed: 'outline',
} as const

const ACTIVE_STATUS_VALUES = ['open', 'answered'] as const
const ESCALATION_VALUES = ['none', 'specialist', 'engineering'] as const

function MessageAuthorIcon({authorType}: { authorType: SupportMessage['author_type'] }) {
  if (authorType === 'agent') return <Headset className="size-4"/>
  if (authorType === 'system') return <Info className="size-4"/>
  return <User className="size-4"/>
}

function AdminTicketThread() {
  const {t} = useTranslation()
  const search = useSearchParams()
  const id = search.get('id') ?? ''
  const [body, setBody] = useState('')
  const [noteBody, setNoteBody] = useState('')
  const realtimeStatus = useSupportRealtime(id, '', true)

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
  const addNote = useMutation({
    mutationFn: () => addAdminSupportInternalNoteAPI(id, noteBody),
    onSuccess: () => { setNoteBody(''); q.refetch() },
  })
  const setEscalation = useMutation({
    mutationFn: (level: string) => setAdminSupportEscalationAPI(id, level),
    onSuccess: () => q.refetch(),
  })

  if (!id) return <p className="text-sm text-muted-foreground">{t('support.ticket.invalid')}</p>
  if (q.isLoading) return <p className="text-sm text-muted-foreground animate-pulse">{t('support.ticket.loading')}</p>
  if (q.isError || !q.data) return <QueryError error={q.error} onRetry={() => q.refetch()}/>

  const {ticket, messages, internal_notes} = q.data
  const isClosed = ticket.status === 'closed'

  function submitReply(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    reply.mutate()
  }

  function submitNote(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    addNote.mutate()
  }

  return (
    <div className="space-y-6">
      <Link href="/admin/support"
            className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground">
        <ArrowLeft className="size-4"/>
        {t('support.admin.back')}
      </Link>

      <div className="flex flex-wrap items-center justify-between gap-4">
        <div className="space-y-1">
          <p className="flex items-center gap-2 text-sm text-muted-foreground">
            <span>#{ticket.ticket_number}</span><span aria-hidden="true">·</span>
            <span className="inline-flex items-center gap-1.5"><Radio className="size-3.5"/>{t(`support.ticket.realtime.${realtimeStatus}`)}</span>
          </p>
          <h1
            className="text-2xl font-semibold">{ticket.subject_other || t(`support.categories.${ticket.subject_category}`)}</h1>
          <p className="text-sm capitalize text-muted-foreground">{t(`support.priority.${ticket.priority}`)}</p>
        </div>
        <div className="flex items-center gap-2">
          <Badge variant={STATUS_VARIANT[ticket.status]}>{t(`support.ticket.status.${ticket.status}`)}</Badge>
          {!isClosed && <Select value={ticket.status} onValueChange={(value) => value && setStatus.mutate(value)}>
            <SelectTrigger aria-label={t('support.admin.statusLabel')} className="w-40">
              <SelectValue>
                {ticket.status ? t(`support.ticket.status.${ticket.status}`) : ticket.status}
              </SelectValue>
            </SelectTrigger>
            <SelectContent>
              {ACTIVE_STATUS_VALUES.map((s) => (
                <SelectItem key={s} value={s}>
                  {t(`support.ticket.status.${s}`)}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>}
          {!isClosed && <ConfirmDialog
            trigger={<Button variant="destructive">{t('support.admin.close')}</Button>}
            title={t('support.admin.closeTitle')}
            description={t('support.admin.closeDescription')}
            confirmLabel={t('support.admin.closeConfirm')}
            onConfirm={() => setStatus.mutateAsync('closed')}
          />}
        </div>
      </div>

      <section className="space-y-3">
        {messages.map((m, i) => (
          <div key={`${m.created_at}-${i}`} className="rounded-xl bg-card p-4 ring-1 ring-foreground/10">
            <div className="mb-2 flex items-center gap-2 text-xs font-medium text-muted-foreground">
              <MessageAuthorIcon authorType={m.author_type}/>
              <span>{t(`support.ticket.author${m.author_type.charAt(0).toUpperCase()}${m.author_type.slice(1)}`)}</span>
              <span aria-hidden="true">·</span>
              <span>{formatDate(m.created_at)}</span>
            </div>
            <p className="whitespace-pre-wrap text-sm">{m.body}</p>
          </div>
        ))}
      </section>

      {!isClosed ? <div className="grid gap-5 lg:grid-cols-[minmax(0,1fr)_20rem]">
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
      <aside className="space-y-4 rounded-xl bg-muted/60 p-4" aria-label={t('support.admin.internalTools')}>
        <div className="space-y-2">
          <label className="text-sm font-medium" htmlFor="ticket-escalation">{t('support.admin.escalationLabel')}</label>
          <Select value={ticket.escalation_level || 'none'} onValueChange={(value) => value && setEscalation.mutate(value)}>
            <SelectTrigger id="ticket-escalation"><SelectValue>{t(`support.admin.escalation.${ticket.escalation_level || 'none'}`)}</SelectValue></SelectTrigger>
            <SelectContent>{ESCALATION_VALUES.map((level) => <SelectItem key={level} value={level}>{t(`support.admin.escalation.${level}`)}</SelectItem>)}</SelectContent>
          </Select>
        </div>
        <form onSubmit={submitNote} className="space-y-2">
          <label className="flex items-center gap-2 text-sm font-medium" htmlFor="internal-note"><LockKeyhole className="size-4"/>{t('support.admin.internalNote')}</label>
          <textarea id="internal-note" value={noteBody} onChange={(event) => setNoteBody(event.target.value)} minLength={3} maxLength={4000} required className="min-h-28 w-full rounded-lg border border-input bg-background p-3 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/70" placeholder={t('support.admin.internalNotePlaceholder')}/>
          <Button type="submit" variant="secondary" disabled={addNote.isPending}>{t('support.admin.saveNote')}</Button>
        </form>
        {internal_notes.length > 0 && <div className="space-y-2 border-t border-border pt-3">
          {internal_notes.map((note) => <div key={note.id} className="space-y-1"><p className="whitespace-pre-wrap text-sm">{note.body}</p><p className="text-xs text-muted-foreground">{formatDate(note.created_at)}</p></div>)}
        </div>}
      </aside>
      </div> : <div className="rounded-xl bg-muted p-4 text-sm text-muted-foreground">{t('support.admin.closedImmutable')}</div>}
    </div>
  )
}

export default function AdminTicketPage() {
  const {t} = useTranslation()
  return (
    <Suspense fallback={<p className="text-sm text-muted-foreground animate-pulse">{t('common.loading')}</p>}>
      <AdminTicketThread/>
    </Suspense>
  )
}
