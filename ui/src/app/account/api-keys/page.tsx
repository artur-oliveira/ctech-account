'use client'

import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { fetchAPIKeys } from '@/lib/queries'
import { formatDistanceToNow, formatDate } from '@/lib/format'
import { Card, CardContent } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { CreateAPIKeyDialog, RevokeAPIKeyButton } from './api-key-actions'
import { Key } from 'lucide-react'

export default function APIKeysPage() {
  const { t } = useTranslation()
  const { data: apiKeys = [], isLoading } = useQuery({
    queryKey: ['api-keys'],
    queryFn: fetchAPIKeys,
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

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold">{t('apiKeys.title')}</h1>
          <p className="text-muted-foreground text-sm mt-1">{t('apiKeys.subtitle')}</p>
        </div>
        <CreateAPIKeyDialog />
      </div>

      <div className="space-y-3">
        {apiKeys.map((key) => (
          <Card key={key.key_id}>
            <CardContent className="flex items-center gap-4 py-4">
              <Key className="size-5 shrink-0 text-muted-foreground" />
              <div className="flex-1 min-w-0">
                <div className="flex items-center gap-2 flex-wrap">
                  <p className="text-sm font-medium">{key.name}</p>
                  <code className="text-xs text-muted-foreground font-mono">{key.key_prefix}…</code>
                  {key.scopes.map((scope) => (
                    <Badge key={scope} variant="secondary" className="text-xs">{scope}</Badge>
                  ))}
                </div>
                <p className="text-xs text-muted-foreground mt-0.5">
                  {t('apiKeys.created', { date: formatDate(key.created_at) })}
                  {key.last_used_at && ` · ${t('apiKeys.lastUsed', { time: formatDistanceToNow(key.last_used_at) })}`}
                  {key.expires_at && ` · ${t('apiKeys.expires', { date: formatDate(key.expires_at) })}`}
                </p>
              </div>
              <RevokeAPIKeyButton keyId={key.key_id} />
            </CardContent>
          </Card>
        ))}
        {apiKeys.length === 0 && (
          <div className="text-center py-12 text-muted-foreground">
            <Key className="size-8 mx-auto mb-2 opacity-40" />
            <p className="text-sm">{t('apiKeys.noKeys')}</p>
          </div>
        )}
      </div>
    </div>
  )
}
