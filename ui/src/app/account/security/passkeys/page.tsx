import { getPasskeys } from '@/lib/api'
import { formatDistanceToNow, formatDate } from '@/lib/format'
import { Card, CardContent } from '@/components/ui/card'
import { RegisterPasskeyButton, RemovePasskeyButton } from './passkey-actions'
import { Fingerprint } from 'lucide-react'

export default async function PasskeysPage() {
  const passkeys = await getPasskeys()

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold">Passkeys</h1>
          <p className="text-muted-foreground text-sm mt-1">
            Passwordless login using biometrics or hardware security keys.
          </p>
        </div>
        <RegisterPasskeyButton />
      </div>

      <div className="space-y-3">
        {passkeys.map((passkey) => (
          <Card key={passkey.id}>
            <CardContent className="flex items-center gap-4 py-4">
              <Fingerprint className="size-6 shrink-0 text-muted-foreground" />
              <div className="flex-1 min-w-0">
                <p className="text-sm font-medium">{passkey.name || 'Passkey'}</p>
                <p className="text-xs text-muted-foreground mt-0.5">
                  Added {formatDate(passkey.created_at)}
                  {passkey.last_used_at && ` · Last used ${formatDistanceToNow(passkey.last_used_at)}`}
                </p>
              </div>
              <RemovePasskeyButton passkeyId={passkey.id} />
            </CardContent>
          </Card>
        ))}
        {passkeys.length === 0 && (
          <div className="text-center py-12 text-muted-foreground">
            <Fingerprint className="size-8 mx-auto mb-2 opacity-40" />
            <p className="text-sm">No passkeys registered.</p>
          </div>
        )}
      </div>
    </div>
  )
}
