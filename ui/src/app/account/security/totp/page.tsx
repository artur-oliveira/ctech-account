import { getTOTPSetup } from '@/lib/api'
import { Card, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { TOTPConfirmForm } from './totp-confirm'

export default async function TOTPSetupPage() {
  const setup = await getTOTPSetup()

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold">Set up authenticator app</h1>
        <p className="text-muted-foreground text-sm mt-1">
          Scan the QR code with an authenticator app, then verify with a code.
        </p>
      </div>

      {setup ? (
        <TOTPConfirmForm provisioningURI={setup.provisioning_uri} />
      ) : (
        <Card>
          <CardHeader>
            <CardTitle>TOTP already configured</CardTitle>
            <CardDescription>
              An authenticator app is already set up for your account. Remove it from the security
              page to set up a new one.
            </CardDescription>
          </CardHeader>
        </Card>
      )}
    </div>
  )
}
