'use client'

import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { fetchPasskeys } from '@/lib/queries'
import { formatDistanceToNow, formatDate } from '@/lib/format'
import { QueryError } from '@/components/query-error'
import { Card, CardContent } from '@/components/ui/card'
import { RegisterPasskeyButton, RemovePasskeyButton } from './passkey-actions'
import { Fingerprint } from 'lucide-react'

export default function PasskeysPage() {
  const { t } = useTranslation()
  const { data: passkeys = [], isLoading, isError, error, refetch } = useQuery({
    queryKey: ['passkeys'],
    queryFn: fetchPasskeys,
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
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold">{t('passkeys.title')}</h1>
          <p className="text-muted-foreground text-sm mt-1">{t('passkeys.subtitle')}</p>
        </div>
        <RegisterPasskeyButton />
      </div>

      <div className="space-y-3">
        {passkeys.map((passkey) => (
          <Card key={passkey.id}>
            <CardContent className="flex items-center gap-4 py-4">
              <Fingerprint className="size-6 shrink-0 text-muted-foreground" />
              <div className="flex-1 min-w-0">
                <p className="text-sm font-medium">{passkey.name || t('passkeys.defaultName')}</p>
                <p className="text-xs text-muted-foreground mt-0.5">
                  {t('passkeys.added', { date: formatDate(passkey.created_at) })}
                  {passkey.last_used_at && ` · ${t('passkeys.lastUsed', { time: formatDistanceToNow(passkey.last_used_at) })}`}
                </p>
              </div>
              <RemovePasskeyButton passkeyId={passkey.id} />
            </CardContent>
          </Card>
        ))}
        {passkeys.length === 0 && (
          <div className="text-center py-12 text-muted-foreground">
            <Fingerprint className="size-8 mx-auto mb-2 opacity-40" />
            <p className="text-sm">{t('passkeys.noPasskeys')}</p>
          </div>
        )}
      </div>
    </div>
  )
}
