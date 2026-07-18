'use client'

import Link from 'next/link'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { fetchPasskeys, fetchTOTPStatus, fetchProfile } from '@/lib/queries'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Fingerprint, KeyRound, Lock, Globe } from 'lucide-react'
import { RemoveTOTPButton, UnlinkGoogleButton } from './security-actions'
import { GoogleSignInButton } from '@/components/google-sign-in-button'
import { QueryError } from '@/components/query-error'

export default function SecurityPage() {
  const { t } = useTranslation()
  const { data: passkeys = [], isError: passkeysError, error: passkeysErr, refetch: refetchPasskeys } = useQuery({
    queryKey: ['passkeys'],
    queryFn: fetchPasskeys,
  })
  const { data: totpStatus, isError: totpError, error: totpErr, refetch: refetchTOTP } = useQuery({
    queryKey: ['totp-status'],
    queryFn: fetchTOTPStatus,
  })
  const { data: profile, isError: profileError, error: profileErr, refetch: refetchProfile } = useQuery({
    queryKey: ['profile'],
    queryFn: fetchProfile,
  })

  if (passkeysError || totpError || profileError) {
    return (
      <QueryError
        error={passkeysErr ?? totpErr ?? profileErr}
        onRetry={() => { refetchPasskeys(); refetchTOTP(); refetchProfile() }}
      />
    )
  }

  const totpEnabled = totpStatus?.enabled ?? false
  const googleLinked = profile?.google_linked ?? false
  const hasPassword = profile?.has_password ?? false

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold">{t('security.title')}</h1>
        <p className="text-muted-foreground text-sm mt-1">{t('security.subtitle')}</p>
        <p className="text-muted-foreground text-sm mt-2">{t('security.helpIntro')}</p>
      </div>

      <Card>
        <CardHeader>
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-2">
              <Globe className="size-5" />
              <CardTitle className="text-base">{t('security.google.title')}</CardTitle>
              {googleLinked && (
                <Badge variant="secondary">{t('security.google.linked')}</Badge>
              )}
            </div>
            <div className="flex gap-2">
              {!googleLinked && (
                <GoogleSignInButton continueURL="/account/security" label={t('security.google.link')} />
              )}
              {googleLinked && hasPassword && <UnlinkGoogleButton />}
            </div>
          </div>
          <CardDescription>{t('security.google.description')}</CardDescription>
        </CardHeader>
        {googleLinked && !hasPassword && (
          <CardContent>
            <p className="text-sm text-muted-foreground">{t('security.google.unlinkNeedsPassword')}</p>
          </CardContent>
        )}
      </Card>

      <Card>
        <CardHeader>
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-2">
              <KeyRound className="size-5" />
              <CardTitle className="text-base">{t('security.totp.title')}</CardTitle>
              {totpEnabled && (
                <Badge variant="secondary">{t('security.totp.enabled')}</Badge>
              )}
            </div>
            <div className="flex gap-2">
              {!totpEnabled && (
                <Button render={<Link href="/account/security/totp" />} size="sm" variant="outline">
                  {t('security.totp.setup')}
                </Button>
              )}
              {totpEnabled && <RemoveTOTPButton />}
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
