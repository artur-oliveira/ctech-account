'use client'

import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { fetchOAuthClients } from '@/lib/queries'
import { formatDate } from '@/lib/format'
import { QueryError } from '@/components/query-error'
import { Badge } from '@/components/ui/badge'
import { ResponsiveDataList, type Column } from '@/components/responsive-data-list'
import type { OAuthClient } from '@/lib/types'
import {
  CreateOAuthClientDialog,
  EditOAuthClientDialog,
  RegenerateSecretButton,
  DeleteOAuthClientButton,
} from './oauth-client-actions'
import { AppWindow } from 'lucide-react'
import { toast } from 'sonner'

export default function OAuthClientsPage() {
  const { t } = useTranslation()
  const { data: clients = [], isLoading, isError, error, refetch } = useQuery({
    queryKey: ['oauth-clients'],
    queryFn: fetchOAuthClients,
  })

  if (isLoading) {
    return (
      <div className="space-y-3">
        {[...Array(2)].map((_, i) => (
          <div key={i} className="h-24 animate-pulse bg-muted rounded-lg" />
        ))}
      </div>
    )
  }

  if (isError) {
    return <QueryError error={error} onRetry={() => refetch()} />
  }

  function handleCopyID(clientId: string) {
    navigator.clipboard.writeText(clientId)
    toast.success(t('toast.clientIDCopied'))
  }

  const columns: Column<OAuthClient>[] = [
    {
      key: 'name',
      header: t('oauthClients.name'),
      title: true,
      cell: (c) => (
        <div className="flex items-center gap-2 flex-wrap min-w-0">
          <span className="text-sm font-medium">{c.name}</span>
          <Badge variant="secondary" className="text-xs">
            {t(`oauthClients.type.${c.client_type}`)}
          </Badge>
          <button
            type="button"
            onClick={() => handleCopyID(c.client_id)}
            className="rounded text-xs text-muted-foreground font-mono outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/70"
            title={t('oauthClients.copyID')}
          >
            {c.client_id}
          </button>
        </div>
      ),
    },
    {
      key: 'redirect',
      header: t('oauthClients.redirectShort'),
      cell: (c) => (
        <span className="text-xs text-muted-foreground break-all">
          {c.redirect_uris.join(' · ')}
        </span>
      ),
    },
    {
      key: 'scopes',
      header: t('oauthClients.scopes'),
      cell: (c) => (
        <div className="flex flex-wrap gap-1">
          {c.allowed_scopes.map((scope) => (
            <Badge key={scope} variant="outline" className="text-xs font-mono">{scope}</Badge>
          ))}
        </div>
      ),
    },
    {
      key: 'created',
      header: t('oauthClients.createdShort'),
      cell: (c) => (
        <span className="text-sm text-muted-foreground">{formatDate(c.created_at)}</span>
      ),
    },
  ]

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold">{t('oauthClients.title')}</h1>
          <p className="text-muted-foreground text-sm mt-1">{t('oauthClients.subtitle')}</p>
        </div>
        <CreateOAuthClientDialog />
      </div>

      <ResponsiveDataList
        rows={clients}
        columns={columns}
        rowKey={(c) => c.client_id}
        actions={(c) => (
          <>
            <EditOAuthClientDialog client={c} />
            {c.client_type === 'confidential' && (
              <RegenerateSecretButton clientId={c.client_id} />
            )}
            <DeleteOAuthClientButton clientId={c.client_id} />
          </>
        )}
        empty={
          <div className="text-center py-12 text-muted-foreground">
            <AppWindow className="size-8 mx-auto mb-2 opacity-40" />
            <p className="text-sm">{t('oauthClients.noClients')}</p>
          </div>
        }
      />
    </div>
  )
}
