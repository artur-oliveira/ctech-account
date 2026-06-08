'use client'

import { useState, FormEvent } from 'react'
import { useMutation } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { QRCodeSVG } from 'qrcode.react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { confirmTOTPAPI } from '@/lib/mutations'
import { isAxiosError } from '@/lib/axios'
import { toast } from 'sonner'

export function TOTPConfirmForm({ provisioningURI }: { provisioningURI: string }) {
  const { t } = useTranslation()
  const [backupCodes, setBackupCodes] = useState<string[] | null>(null)

  const { mutate, isPending, error } = useMutation({
    mutationFn: confirmTOTPAPI,
    onSuccess: (data) => {
      setBackupCodes(data.backup_codes)
      toast.success(t('toast.totpActivated'))
    },
    onError: (err) => {
      if (isAxiosError(err)) toast.error(err.response?.data?.detail ?? t('errors.invalidCode'))
    },
  })

  function handleSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    const fd = new FormData(e.currentTarget)
    mutate(fd.get('code') as string)
  }

  const errorMsg = isAxiosError(error) ? (error.response?.data?.detail ?? t('errors.invalidCode')) : null

  if (backupCodes?.length) {
    return (
      <Card>
        <CardHeader>
          <CardTitle>{t('totp.backup.title')}</CardTitle>
          <CardDescription>{t('totp.backup.description')}</CardDescription>
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
            {t('totp.backup.back')}
          </Button>
        </CardContent>
      </Card>
    )
  }

  return (
    <div className="space-y-4">
      <Card>
        <CardHeader>
          <CardTitle>{t('totp.setup.step1Title')}</CardTitle>
          <CardDescription>{t('totp.setup.step1Desc')}</CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col items-center gap-4">
          <div className="rounded-lg border p-4 bg-white">
            <QRCodeSVG value={provisioningURI} size={180} />
          </div>
          <details className="text-xs text-muted-foreground w-full">
            <summary className="cursor-pointer">{t('totp.setup.cantScan')}</summary>
            <code className="mt-2 block break-all rounded bg-muted p-2">{provisioningURI}</code>
          </details>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>{t('totp.setup.step2Title')}</CardTitle>
          <CardDescription>{t('totp.setup.step2Desc')}</CardDescription>
        </CardHeader>
        <CardContent>
          <form onSubmit={handleSubmit} className="space-y-4">
            {errorMsg && (
              <Alert variant="destructive">
                <AlertDescription>{errorMsg}</AlertDescription>
              </Alert>
            )}
            <div className="space-y-1.5">
              <Label htmlFor="code">{t('totp.setup.code')}</Label>
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
            <Button type="submit" disabled={isPending}>
              {isPending ? t('totp.setup.activating') : t('totp.setup.activate')}
            </Button>
          </form>
        </CardContent>
      </Card>
    </div>
  )
}
