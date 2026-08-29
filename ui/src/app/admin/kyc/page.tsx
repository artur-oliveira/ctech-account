'use client'

import { useEffect, useState } from 'react'
import Link from 'next/link'
import { useRouter } from 'next/navigation'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { QueryError } from '@/components/query-error'
import { ResponsiveDataList, type Column } from '@/components/responsive-data-list'
import { fetchAdminKYCReviews, fetchProfile } from '@/lib/queries'
import { formatDate, formatDistanceToNow } from '@/lib/format'
import { hasSupportRole } from '@/lib/support-role'
import type { AdminKYCReviewSummary, KYCReviewQueue } from '@/lib/types'
import { ArrowRight, ClipboardCheck } from 'lucide-react'

export default function AdminKYCPage() {
  const { t } = useTranslation()
  const router = useRouter()
  const [queue, setQueue] = useState<KYCReviewQueue>('pending')
  const { data: profile } = useQuery({ queryKey: ['profile'], queryFn: fetchProfile })
  const allowed = !!profile && hasSupportRole(profile.support_role, 'manager')

  useEffect(() => {
    if (profile && !allowed) router.replace('/admin/support')
  }, [profile, allowed, router])

  const reviews = useQuery({
    queryKey: ['admin', 'kyc', 'reviews', queue],
    queryFn: () => fetchAdminKYCReviews(queue),
    enabled: allowed,
  })

  if (!profile || !allowed) {
    return <p className="animate-pulse text-sm text-muted-foreground">{t('common.loading')}</p>
  }

  const columns: Column<AdminKYCReviewSummary>[] = [
    {
      key: 'person', header: t('adminKyc.person'), title: true,
      cell: (review) => (
        <div className="min-w-0">
          <p className="truncate font-medium">{review.legal_name}</p>
          <p className="truncate font-mono text-xs text-muted-foreground">{review.user_id}</p>
        </div>
      ),
    },
    {
      key: 'status', header: t('adminKyc.status'),
      cell: (review) => (
        <Badge variant={review.status === 'rejected' ? 'destructive' : review.status === 'verified' ? 'default' : 'secondary'}>
          {t(`adminKyc.states.${review.status}`)}
        </Badge>
      ),
    },
    {
      key: 'submitted', header: t('adminKyc.submitted'), className: 'w-40',
      cell: (review) => <span title={formatDate(review.submitted_at)}>{formatDistanceToNow(review.submitted_at)}</span>,
    },
    {
      key: 'reviewer', header: t('adminKyc.reviewer'),
      cell: (review) => review.reviewed_by_name || '—',
    },
  ]

  return (
    <div className="space-y-6">
      <div className="flex items-start gap-3">
        <span className="mt-0.5 inline-flex size-9 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary">
          <ClipboardCheck className="size-5" />
        </span>
        <div>
          <h1 className="text-2xl font-semibold tracking-tight text-balance">{t('adminKyc.title')}</h1>
          <p className="mt-1 max-w-2xl text-sm text-muted-foreground text-pretty">{t('adminKyc.description')}</p>
        </div>
      </div>

      <Tabs value={queue} onValueChange={(value) => setQueue(value as KYCReviewQueue)}>
        <TabsList aria-label={t('adminKyc.queues')}>
          <TabsTrigger value="pending">{t('adminKyc.pending')}</TabsTrigger>
          <TabsTrigger value="completed">{t('adminKyc.completed')}</TabsTrigger>
        </TabsList>
      </Tabs>

      {reviews.isPending ? (
        <div className="space-y-2" aria-label={t('common.loading')}>
          {[0, 1, 2].map((item) => <div key={item} className="h-14 animate-pulse rounded-lg bg-muted" />)}
        </div>
      ) : reviews.isError ? (
        <QueryError error={reviews.error} onRetry={() => reviews.refetch()} />
      ) : (
        <ResponsiveDataList
          rows={reviews.data ?? []}
          columns={columns}
          rowKey={(review) => review.user_id}
          actions={(review) => (
            <Button variant="ghost" size="sm" render={<Link href={`/admin/kyc/review?id=${encodeURIComponent(review.user_id)}`} />}>
              {t('adminKyc.open')} <ArrowRight className="size-4" />
            </Button>
          )}
          empty={(
            <div className="rounded-lg border border-dashed px-5 py-10 text-center">
              <p className="font-medium">{queue === 'pending' ? t('adminKyc.emptyPending') : t('adminKyc.emptyCompleted')}</p>
              <p className="mt-1 text-sm text-muted-foreground">{t('adminKyc.emptyHint')}</p>
            </div>
          )}
        />
      )}
    </div>
  )
}
