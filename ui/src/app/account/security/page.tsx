import Link from 'next/link'
import { getPasskeys } from '@/lib/api'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Fingerprint, KeyRound, Lock } from 'lucide-react'
import { RemoveTOTPButton } from './security-actions'

export default async function SecurityPage() {
  const passkeys = await getPasskeys()

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold">Security</h1>
        <p className="text-muted-foreground text-sm mt-1">
          Manage your authentication methods and security settings.
        </p>
      </div>

      <Card>
        <CardHeader>
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-2">
              <KeyRound className="size-5" />
              <CardTitle className="text-base">Authenticator app (TOTP)</CardTitle>
            </div>
            <div className="flex gap-2">
              <Button render={<Link href="/account/security/totp" />} size="sm" variant="outline">
                Set up
              </Button>
              <RemoveTOTPButton />
            </div>
          </div>
          <CardDescription>
            Use a time-based one-time password (TOTP) app for extra login security.
          </CardDescription>
        </CardHeader>
      </Card>

      <Card>
        <CardHeader>
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-2">
              <Fingerprint className="size-5" />
              <CardTitle className="text-base">Passkeys</CardTitle>
            </div>
            <Button render={<Link href="/account/security/passkeys" />} size="sm" variant="outline">
              Manage
            </Button>
          </div>
          <CardDescription>
            Biometric or hardware-key login — no password required.
          </CardDescription>
        </CardHeader>
        {passkeys.length > 0 && (
          <CardContent>
            <p className="text-sm text-muted-foreground">
              {passkeys.length} passkey{passkeys.length !== 1 ? 's' : ''} registered
            </p>
          </CardContent>
        )}
      </Card>

      <Card>
        <CardHeader>
          <div className="flex items-center gap-2">
            <Lock className="size-5" />
            <CardTitle className="text-base">Password</CardTitle>
          </div>
          <CardDescription>Change your account password.</CardDescription>
        </CardHeader>
        <CardContent>
          <Button render={<Link href="/account/profile" />} variant="outline" size="sm">
            Change password
          </Button>
        </CardContent>
      </Card>
    </div>
  )
}
