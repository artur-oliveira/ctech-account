'use client'

import { Suspense, useState, SyntheticEvent } from 'react'
import { useSearchParams, useRouter } from 'next/navigation'
import Link from 'next/link'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { resetPasswordAPI } from '@/lib/mutations'

function ResetPasswordForm() {
  const { t } = useTranslation()
  const params = useSearchParams()
  const router = useRouter()
  const token = params.get('token') ?? ''
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  if (!token) {
    return (
      <Alert variant="destructive">
        <AlertDescription>{t('resetPassword.invalidLink')}</AlertDescription>
      </Alert>
    )
  }

  async function handleSubmit(e: SyntheticEvent<HTMLFormElement>) {
    e.preventDefault()
    setError('')
    const fd = new FormData(e.currentTarget)
    const pw = fd.get('password') as string
    const pw2 = fd.get('confirm') as string
    if (pw !== pw2) {
      setError(t('resetPassword.mismatch'))
      return
    }
    setLoading(true)
    try {
      await resetPasswordAPI(token, pw)
      router.push('/login?reset=1')
    } catch (err: unknown) {
      const detail = (err as { response?: { data?: { detail?: string } } })?.response?.data?.detail
      setError(detail ?? t('errors.network'))
      setLoading(false)
    }
  }

  return (
    <form onSubmit={handleSubmit} className="space-y-4">
      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
      <div className="space-y-1.5">
        <Label htmlFor="password">{t('resetPassword.newPassword')}</Label>
        <Input id="password" name="password" type="password" autoComplete="new-password" required minLength={8} />
      </div>
      <div className="space-y-1.5">
        <Label htmlFor="confirm">{t('resetPassword.confirmPassword')}</Label>
        <Input id="confirm" name="confirm" type="password" autoComplete="new-password" required minLength={8} />
      </div>
      <Button type="submit" className="w-full" disabled={loading}>
        {loading ? t('common.loading') : t('resetPassword.submit')}
      </Button>
    </form>
  )
}

export default function ResetPasswordPage() {
  const { t } = useTranslation()
  return (
    <div className="min-h-screen flex items-center justify-center bg-muted/40 p-4">
      <div className="w-full max-w-md space-y-6">
        <div className="text-center">
          <h1 className="text-2xl font-bold tracking-tight">{t('app.name')}</h1>
        </div>
        <Card>
          <CardHeader>
            <CardTitle>{t('resetPassword.title')}</CardTitle>
            <CardDescription>{t('resetPassword.description')}</CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <Suspense fallback={<div className="h-32 animate-pulse bg-muted rounded" />}>
              <ResetPasswordForm />
            </Suspense>
            <p className="text-center text-sm text-muted-foreground">
              <Link href="/login" className="underline underline-offset-4 hover:text-foreground">
                {t('forgotPassword.backToLogin')}
              </Link>
            </p>
          </CardContent>
        </Card>
      </div>
    </div>
  )
}
