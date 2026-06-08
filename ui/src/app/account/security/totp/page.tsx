'use client'

import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { fetchTOTPSetup } from '@/lib/queries'
import { Card, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { TOTPConfirmForm } from './totp-confirm'

export default function TOTPSetupPage() {
  const { t } = useTranslation()
  const { data: setup, isLoading } = useQuery({
    queryKey: ['totp-setup'],
    queryFn: fetchTOTPSetup,
  })

  if (isLoading) {
    return <div className="h-64 animate-pulse bg-muted rounded-lg" />
  }

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold">{t('totp.setup.title')}</h1>
        <p className="text-muted-foreground text-sm mt-1">{t('totp.setup.subtitle')}</p>
      </div>

      {setup ? (
        <TOTPConfirmForm provisioningURI={setup.provisioning_uri} />
      ) : (
        <Card>
          <CardHeader>
            <CardTitle>{t('totp.setup.alreadyConfigured')}</CardTitle>
            <CardDescription>{t('totp.setup.alreadyDesc')}</CardDescription>
          </CardHeader>
        </Card>
      )}
    </div>
  )
}
