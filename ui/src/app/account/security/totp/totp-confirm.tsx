'use client'

import { useActionState, useEffect } from 'react'
import { useFormStatus } from 'react-dom'
import { QRCodeSVG } from 'qrcode.react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { confirmTOTP } from '@/lib/actions'
import { toast } from 'sonner'

function SubmitButton() {
  const { pending } = useFormStatus()
  return <Button type="submit" disabled={pending}>{pending ? 'Verifying…' : 'Activate'}</Button>
}

export function TOTPConfirmForm({ provisioningURI }: { provisioningURI: string }) {
  const [state, action] = useActionState(confirmTOTP, null)
  const backupCodes = state?.backup_codes as string[] | undefined

  useEffect(() => {
    if (state?.success && state.backup_codes) toast.success('TOTP activated. Save your backup codes.')
    if (state?.error) toast.error(state.error)
  }, [state])

  if (state?.success && backupCodes?.length) {
    return (
      <Card>
        <CardHeader>
          <CardTitle>TOTP activated</CardTitle>
          <CardDescription>
            Save these backup codes somewhere safe. Each can only be used once.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="grid grid-cols-2 gap-2">
            {backupCodes.map((code) => (
              <code key={code} className="rounded bg-muted px-2 py-1 text-sm font-mono text-center">
                {code}
              </code>
            ))}
          </div>
          <Button render={<a href="/account/security" />} variant="outline">
            Back to security
          </Button>
        </CardContent>
      </Card>
    )
  }

  return (
    <div className="space-y-4">
      <Card>
        <CardHeader>
          <CardTitle>Step 1 — Scan QR code</CardTitle>
          <CardDescription>
            Open your authenticator app and scan the code below.
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col items-center gap-4">
          <div className="rounded-lg border p-4 bg-white">
            <QRCodeSVG value={provisioningURI} size={180} />
          </div>
          <details className="text-xs text-muted-foreground w-full">
            <summary className="cursor-pointer">Can&apos;t scan? Enter manually</summary>
            <code className="mt-2 block break-all rounded bg-muted p-2">{provisioningURI}</code>
          </details>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Step 2 — Verify code</CardTitle>
          <CardDescription>
            Enter the 6-digit code from your authenticator app to confirm setup.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form action={action} className="space-y-4">
            {state?.error && (
              <Alert variant="destructive">
                <AlertDescription>{state.error}</AlertDescription>
              </Alert>
            )}
            <div className="space-y-1.5">
              <Label htmlFor="code">Verification code</Label>
              <Input
                id="code"
                name="code"
                type="text"
                inputMode="numeric"
                pattern="[0-9]{6}"
                maxLength={6}
                placeholder="000000"
                autoComplete="one-time-code"
                required
              />
            </div>
            <SubmitButton />
          </form>
        </CardContent>
      </Card>
    </div>
  )
}
