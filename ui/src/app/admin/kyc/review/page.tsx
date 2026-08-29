'use client'

import { Suspense, useEffect, useState } from 'react'
import Link from 'next/link'
import { useRouter, useSearchParams } from 'next/navigation'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from '@/components/ui/dialog'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { QueryError } from '@/components/query-error'
import { accessAdminKYCDocumentsAPI, decideAdminKYCReviewAPI } from '@/lib/mutations'
import { fetchAdminKYCReview, fetchProfile } from '@/lib/queries'
import { formatDate, formatDistanceToNow } from '@/lib/format'
import { hasSupportRole } from '@/lib/support-role'
import type { KYCRejectionCode } from '@/lib/types'
import { ArrowLeft, ExternalLink, Eye, FileCheck2, ShieldAlert, XCircle } from 'lucide-react'

function ReviewPageContent() {
  const { t } = useTranslation()
  const router = useRouter()
  const params = useSearchParams()
  const userId = params.get('id') ?? ''
  const queryClient = useQueryClient()
  const { data: profile } = useQuery({ queryKey: ['profile'], queryFn: fetchProfile })
  const allowed = !!profile && hasSupportRole(profile.support_role, 'manager')

  useEffect(() => {
    if (profile && !allowed) router.replace('/admin/support')
  }, [profile, allowed, router])

  const reviewQuery = useQuery({
    queryKey: ['admin', 'kyc', 'review', userId],
    queryFn: () => fetchAdminKYCReview(userId),
    enabled: allowed && !!userId,
  })
  const documents = useMutation({
    mutationFn: () => accessAdminKYCDocumentsAPI(userId),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['admin', 'kyc', 'review', userId] }),
    onError: () => toast.error(t('adminKyc.documentsError')),
  })
  const decision = useMutation({
    mutationFn: ({ value, reasonCode, details }: { value: 'approve' | 'reject'; reasonCode?: KYCRejectionCode; details?: string }) => decideAdminKYCReviewAPI(userId, value, reasonCode, details),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ['admin', 'kyc'] })
      toast.success(t('adminKyc.decisionSaved'))
      router.push('/admin/kyc')
    },
    onError: () => toast.error(t('adminKyc.decisionError')),
  })

  if (!userId) return <QueryError error={new Error(t('adminKyc.missingId'))} onRetry={() => router.push('/admin/kyc')} />
  if (!profile || !allowed || reviewQuery.isPending) {
    return <div className="h-72 animate-pulse rounded-lg bg-muted" aria-label={t('common.loading')} />
  }
  if (reviewQuery.isError) return <QueryError error={reviewQuery.error} onRetry={() => reviewQuery.refetch()} />

  const { review, audit_log: auditLog } = reviewQuery.data
  const pending = review.status === 'pending'

  return (
    <div className="space-y-7">
      <div>
        <Button variant="ghost" size="sm" className="-ml-2 mb-3" render={<Link href="/admin/kyc" />}>
          <ArrowLeft className="size-4" /> {t('adminKyc.back')}
        </Button>
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div>
            <h1 className="text-2xl font-semibold tracking-tight text-balance">{review.legal_name}</h1>
            <p className="mt-1 font-mono text-xs text-muted-foreground">{review.user_id}</p>
          </div>
          <Badge variant={review.status === 'rejected' ? 'destructive' : review.status === 'verified' ? 'default' : 'secondary'}>
            {t(`adminKyc.states.${review.status}`)}
          </Badge>
        </div>
      </div>

      <section aria-labelledby="identity-heading" className="space-y-3">
        <h2 id="identity-heading" className="text-base font-semibold">{t('adminKyc.identityData')}</h2>
        <dl className="grid gap-x-8 gap-y-3 border-y py-4 sm:grid-cols-2 lg:grid-cols-3">
          {[
            [t('adminKyc.cpf'), review.cpf], [t('adminKyc.birthDate'), formatDate(review.birth_date)],
            [t('adminKyc.phone'), review.phone_number], [t('adminKyc.submitted'), formatDate(review.submitted_at)],
            [t('adminKyc.risk'), String(review.risk_score)],
            [t('adminKyc.address'), `${review.address.street}, ${review.address.number} — ${review.address.city}/${review.address.state}`],
          ].map(([label, value]) => (
            <div key={label}>
              <dt className="text-xs font-medium text-muted-foreground">{label}</dt>
              <dd className="mt-0.5 text-sm break-words">{value}</dd>
            </div>
          ))}
        </dl>
        {review.risk_signals?.length > 0 && (
          <Alert>
            <ShieldAlert className="size-4" />
            <AlertTitle>{t('adminKyc.riskSignals')}</AlertTitle>
            <AlertDescription>{review.risk_signals.join(' · ')}</AlertDescription>
          </Alert>
        )}
      </section>

      <section aria-labelledby="documents-heading" className="space-y-3">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h2 id="documents-heading" className="text-base font-semibold">{t('adminKyc.documents')}</h2>
            <p className="mt-0.5 text-sm text-muted-foreground">{t('adminKyc.documentsPrivacy')}</p>
          </div>
          <Button variant="outline" onClick={() => documents.mutate()} disabled={documents.isPending || review.status === 'rejected'}>
            <Eye className="size-4" /> {documents.isPending ? t('common.loading') : t('adminKyc.accessDocuments')}
          </Button>
        </div>
        {documents.data && (
          <ul className="divide-y rounded-lg border">
            {documents.data.documents.map((document) => (
              <li key={document.id} className="flex items-center justify-between gap-3 px-3 py-3">
                <div className="min-w-0">
                  <p className="text-sm font-medium">{t(`adminKyc.documentTypes.${document.type}`)}</p>
                  <p className="text-xs text-muted-foreground">{formatDate(document.uploaded_at)}</p>
                </div>
                <Button variant="ghost" size="sm" render={<a href={document.url} target="_blank" rel="noopener noreferrer" />}>
                  {t('adminKyc.openDocument')} <ExternalLink className="size-4" />
                </Button>
              </li>
            ))}
          </ul>
        )}
      </section>

      {pending && (
        <section aria-labelledby="decision-heading" className="space-y-3 border-t pt-5">
          <div>
            <h2 id="decision-heading" className="text-base font-semibold">{t('adminKyc.decision')}</h2>
            <p className="mt-0.5 text-sm text-muted-foreground">{t('adminKyc.decisionHint')}</p>
          </div>
          <div className="flex flex-wrap gap-2">
            <ConfirmDialog
              trigger={<Button disabled={decision.isPending}><FileCheck2 className="size-4" /> {t('adminKyc.approve')}</Button>}
              title={t('adminKyc.approveTitle')}
              description={t('adminKyc.approveDescription')}
              confirmLabel={t('adminKyc.approve')}
              variant="default"
              onConfirm={() => decision.mutateAsync({ value: 'approve' }).catch(() => undefined)}
            />
            <RejectReviewButton pending={decision.isPending} onReject={(reasonCode, details) => decision.mutateAsync({ value: 'reject', reasonCode, details })} />
          </div>
        </section>
      )}

      <section aria-labelledby="audit-heading" className="space-y-3 border-t pt-5">
        <h2 id="audit-heading" className="text-base font-semibold">{t('adminKyc.audit')}</h2>
        {auditLog.length === 0 ? <p className="text-sm text-muted-foreground">{t('adminKyc.noAudit')}</p> : (
          <ol className="space-y-3">
            {auditLog.map((event, index) => (
              <li key={`${event.created_at}-${index}`} className="flex gap-3 text-sm">
                <span className="mt-1.5 size-2 shrink-0 rounded-full bg-primary" />
                <div>
                  <p><span className="font-medium">{event.actor_name || event.actor_id || t('adminKyc.system')}</span> {t(`adminKyc.events.${event.event_type}`)}</p>
                  <p className="text-xs text-muted-foreground">{formatDistanceToNow(event.created_at)} · {event.actor_role || '—'}</p>
                  {event.reason_code && <p className="mt-1 text-muted-foreground">{t(`adminKyc.rejectionReasons.${event.reason_code}`)}{event.details ? ` — ${event.details}` : ''}</p>}
                </div>
              </li>
            ))}
          </ol>
        )}
      </section>
    </div>
  )
}

const REJECTION_CODES: KYCRejectionCode[] = ['document_unreadable', 'document_incomplete', 'document_mismatch', 'selfie_mismatch', 'data_mismatch', 'suspected_fraud', 'other']

function RejectReviewButton({ pending, onReject }: { pending: boolean; onReject: (reasonCode: KYCRejectionCode, details: string) => Promise<void> }) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [reasonCode, setReasonCode] = useState<KYCRejectionCode | null>(null)
  const [details, setDetails] = useState('')
  const [error, setError] = useState('')

  async function submit() {
    const normalized = details.trim()
    if (!reasonCode || (reasonCode === 'other' && !normalized)) {
      setError(t('adminKyc.rejectionReasonRequired'))
      return
    }
    setError('')
    try {
      await onReject(reasonCode, normalized)
      setOpen(false)
    } catch {
      // The mutation's onError owns user-visible feedback; keep the dialog
      // open so the reviewer can retry without re-entering the reason.
    }
  }

  return (
    <Dialog open={open} onOpenChange={(next) => !pending && setOpen(next)}>
      <DialogTrigger render={<Button variant="destructive" disabled={pending}><XCircle className="size-4" /> {t('adminKyc.reject')}</Button>} />
      <DialogContent showCloseButton={false}>
        <DialogHeader>
          <DialogTitle>{t('adminKyc.rejectTitle')}</DialogTitle>
          <DialogDescription>{t('adminKyc.rejectDescription')}</DialogDescription>
        </DialogHeader>
        <div className="space-y-1.5">
          <label htmlFor="kyc-rejection-code" className="text-sm font-medium">{t('adminKyc.rejectionReason')}</label>
          <Select value={reasonCode} onValueChange={(value) => setReasonCode(value as KYCRejectionCode)}>
            <SelectTrigger id="kyc-rejection-code" className="w-full" aria-invalid={!!error && !reasonCode}>
              <SelectValue placeholder={t('adminKyc.rejectionReasonPlaceholder')} />
            </SelectTrigger>
            <SelectContent align="start">
              {REJECTION_CODES.map((code) => <SelectItem key={code} value={code}>{t(`adminKyc.rejectionReasons.${code}`)}</SelectItem>)}
            </SelectContent>
          </Select>
          <label htmlFor="kyc-rejection-details" className="text-sm font-medium">{t('adminKyc.rejectionDetails')}</label>
          <textarea
            id="kyc-rejection-details"
            value={details}
            maxLength={255}
            onChange={(event) => setDetails(event.target.value)}
            rows={4}
            aria-invalid={!!error}
            aria-describedby={error ? 'kyc-rejection-error' : undefined}
            className="w-full resize-y rounded-lg border border-input bg-transparent px-2.5 py-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/70 aria-invalid:border-destructive"
          />
          <p className="text-right text-xs text-muted-foreground">{details.length}/255</p>
          {error && <p id="kyc-rejection-error" className="text-sm text-destructive">{error}</p>}
        </div>
        <DialogFooter showCloseButton={false}>
          <DialogClose render={<Button variant="outline" disabled={pending}>{t('dialog.cancel')}</Button>} />
          <Button variant="destructive" onClick={submit} disabled={pending}>{pending ? t('dialog.processing') : t('adminKyc.reject')}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

export default function AdminKYCReviewPage() {
  return <Suspense fallback={<div className="h-72 animate-pulse rounded-lg bg-muted" />}><ReviewPageContent /></Suspense>
}
