'use client'

import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { fetchConsents } from '@/lib/queries'
import { revokeConsentAPI } from '@/lib/mutations'
import { isAxiosError } from '@/lib/axios'
import { formatDate } from '@/lib/format'
import { describeScope } from '@/lib/scope-description'
import { Card, CardContent } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { QueryError } from '@/components/query-error'
import { toast } from 'sonner'
import { Blocks } from 'lucide-react'

const CONSENTS_QUERY_KEY = ['consents']

function RevokeConsentButton({ clientId }: { clientId: string }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const { mutate } = useMutation({
    mutationFn: () => revokeConsentAPI(clientId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: CONSENTS_QUERY_KEY })
      toast.success(t('toast.consentRevoked'))
    },
    onError: (err) => {
      if (isAxiosError(err)) toast.error(err.response?.data?.detail ?? t('toast.consentRevokeFailed'))
    },
  })

  return (
    <ConfirmDialog
      variant="destructive"
      trigger={
        <Button variant="destructive" size="sm">
          {t('connectedApps.revoke')}
        </Button>
      }
      title={t('connectedApps.revokeTitle')}
      description={t('connectedApps.confirmRevoke')}
      onConfirm={() => mutate()}
    />
  )
}

export default function ConnectedAppsPage() {
  const { t } = useTranslation()
  const { data: consents = [], isLoading, isError, error, refetch } = useQuery({
    queryKey: CONSENTS_QUERY_KEY,
    queryFn: fetchConsents,
  })

  if (isLoading) {
    return (
      <div className="space-y-3">
        {[...Array(2)].map((_, i) => (
          <div key={i} className="h-20 animate-pulse bg-muted rounded-lg" />
        ))}
      </div>
    )
  }

  if (isError) {
    return <QueryError error={error} onRetry={() => refetch()} />
  }

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold">{t('connectedApps.title')}</h1>
        <p className="text-muted-foreground text-sm mt-1">{t('connectedApps.subtitle')}</p>
      </div>

      <div className="space-y-3">
        {consents.map((grant) => (
          <Card key={grant.client_id}>
            <CardContent className="py-4 space-y-2">
              <div className="flex items-center gap-3">
                <Blocks className="size-5 shrink-0 text-muted-foreground" />
                <div className="flex-1 min-w-0">
                  <p className="text-sm font-medium">{grant.client_name}</p>
                  <p className="text-xs text-muted-foreground">
                    {t('connectedApps.granted', { date: formatDate(grant.created_at) })}
                  </p>
                </div>
                <RevokeConsentButton clientId={grant.client_id} />
              </div>
              <ul className="pl-8 space-y-0.5">
                {grant.scopes.map((scope) => (
                  <li key={scope} className="text-xs text-muted-foreground">
                    {describeScope(scope, t)}{' '}
                    <code className="font-mono opacity-70">({scope})</code>
                  </li>
                ))}
              </ul>
            </CardContent>
          </Card>
        ))}
        {consents.length === 0 && (
          <div className="text-center py-12 text-muted-foreground">
            <Blocks className="size-8 mx-auto mb-2 opacity-40" />
            <p className="text-sm">{t('connectedApps.none')}</p>
          </div>
        )}
      </div>
    </div>
  )
}
