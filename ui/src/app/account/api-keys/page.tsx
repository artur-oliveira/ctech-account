'use client'

import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { fetchAPIKeys } from '@/lib/queries'
import { formatDistanceToNow, formatDate } from '@/lib/format'
import { QueryError } from '@/components/query-error'
import { Badge } from '@/components/ui/badge'
import { ResponsiveDataList, type Column } from '@/components/responsive-data-list'
import type { APIKey } from '@/lib/types'
import { CreateAPIKeyDialog, RevokeAPIKeyButton } from './api-key-actions'
import { Key } from 'lucide-react'

export default function APIKeysPage() {
  const { t } = useTranslation()
  const { data: apiKeys = [], isLoading, isError, error, refetch } = useQuery({
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

  if (isError) {
    return <QueryError error={error} onRetry={() => refetch()} />
  }

  const columns: Column<APIKey>[] = [
    {
      key: 'name',
      header: t('apiKeys.name'),
      title: true,
      cell: (k) => (
        <div className="flex items-center gap-2 flex-wrap min-w-0">
          <span className="text-sm font-medium">{k.name}</span>
          <code className="text-xs text-muted-foreground font-mono">{k.key_prefix}…</code>
        </div>
      ),
    },
    {
      key: 'scopes',
      header: t('apiKeys.scopes'),
      cell: (k) => (
        <div className="flex flex-wrap gap-1">
          {k.scopes.map((scope) => (
            <Badge key={scope} variant="secondary" className="text-xs">{scope}</Badge>
          ))}
        </div>
      ),
    },
    {
      key: 'created',
      header: t('apiKeys.createdShort'),
      cell: (k) => (
        <span className="text-sm text-muted-foreground">{formatDate(k.created_at)}</span>
      ),
    },
    {
      key: 'lastUsed',
      header: t('apiKeys.lastUsedShort'),
      cell: (k) => (
        <span className="text-sm text-muted-foreground">
          {k.last_used_at ? formatDistanceToNow(k.last_used_at) : '—'}
        </span>
      ),
    },
    {
      key: 'expires',
      header: t('apiKeys.expiresShort'),
      cell: (k) => (
        <span className="text-sm text-muted-foreground">
          {k.expires_at ? formatDate(k.expires_at) : '—'}
        </span>
      ),
    },
  ]

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold">{t('apiKeys.title')}</h1>
          <p className="text-muted-foreground text-sm mt-1">{t('apiKeys.subtitle')}</p>
        </div>
        <CreateAPIKeyDialog />
      </div>

      <ResponsiveDataList
        rows={apiKeys}
        columns={columns}
        rowKey={(k) => k.key_id}
        actions={(k) => <RevokeAPIKeyButton keyId={k.key_id} />}
        empty={
          <div className="text-center py-12 text-muted-foreground">
            <Key className="size-8 mx-auto mb-2 opacity-40" />
            <p className="text-sm">{t('apiKeys.noKeys')}</p>
          </div>
        }
      />
    </div>
  )
}
