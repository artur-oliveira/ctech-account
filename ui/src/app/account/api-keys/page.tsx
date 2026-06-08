import { getAPIKeys } from '@/lib/api'
import { formatDistanceToNow, formatDate } from '@/lib/format'
import { Card, CardContent } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { CreateAPIKeyDialog, RevokeAPIKeyButton } from './api-key-actions'
import { Key } from 'lucide-react'

export default async function APIKeysPage() {
  const apiKeys = await getAPIKeys()

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold">API Keys</h1>
          <p className="text-muted-foreground text-sm mt-1">
            Long-lived tokens for programmatic API access.
          </p>
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
                  Created {formatDate(key.created_at)}
                  {key.last_used_at && ` · Last used ${formatDistanceToNow(key.last_used_at)}`}
                  {key.expires_at && ` · Expires ${formatDate(key.expires_at)}`}
                </p>
              </div>
              <RevokeAPIKeyButton keyId={key.key_id} />
            </CardContent>
          </Card>
        ))}
        {apiKeys.length === 0 && (
          <div className="text-center py-12 text-muted-foreground">
            <Key className="size-8 mx-auto mb-2 opacity-40" />
            <p className="text-sm">No API keys yet.</p>
          </div>
        )}
      </div>
    </div>
  )
}
