'use client'

import Link from 'next/link'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { fetchPasskeys } from '@/lib/queries'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Fingerprint, KeyRound, Lock } from 'lucide-react'
import { RemoveTOTPButton } from './security-actions'

export default function SecurityPage() {
  const { t } = useTranslation()
  const { data: passkeys = [] } = useQuery({
    queryKey: ['passkeys'],
    queryFn: fetchPasskeys,
  })

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold">{t('security.title')}</h1>
        <p className="text-muted-foreground text-sm mt-1">{t('security.subtitle')}</p>
      </div>

      <Card>
        <CardHeader>
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-2">
              <KeyRound className="size-5" />
              <CardTitle className="text-base">{t('security.totp.title')}</CardTitle>
            </div>
            <div className="flex gap-2">
              <Button render={<Link href="/account/security/totp" />} size="sm" variant="outline">
                {t('security.totp.setup')}
              </Button>
              <RemoveTOTPButton />
            </div>
          </div>
          <CardDescription>{t('security.totp.description')}</CardDescription>
        </CardHeader>
      </Card>

      <Card>
        <CardHeader>
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-2">
              <Fingerprint className="size-5" />
              <CardTitle className="text-base">{t('security.passkeys.title')}</CardTitle>
            </div>
            <Button render={<Link href="/account/security/passkeys" />} size="sm" variant="outline">
              {t('security.passkeys.manage')}
            </Button>
          </div>
          <CardDescription>{t('security.passkeys.description')}</CardDescription>
        </CardHeader>
        {passkeys.length > 0 && (
          <CardContent>
            <p className="text-sm text-muted-foreground">
              {t('security.passkeys.count_one', { count: passkeys.length })}
            </p>
          </CardContent>
        )}
      </Card>

      <Card>
        <CardHeader>
          <div className="flex items-center gap-2">
            <Lock className="size-5" />
            <CardTitle className="text-base">{t('security.password.title')}</CardTitle>
          </div>
          <CardDescription>{t('security.password.description')}</CardDescription>
        </CardHeader>
        <CardContent>
          <Button render={<Link href="/account/profile" />} variant="outline" size="sm">
            {t('security.password.change')}
          </Button>
        </CardContent>
      </Card>
    </div>
  )
}
