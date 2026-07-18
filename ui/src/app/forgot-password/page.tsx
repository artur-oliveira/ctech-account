'use client'

import { useState, SyntheticEvent } from 'react'
import Link from 'next/link'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Card, CardContent } from '@/components/ui/card'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { forgotPasswordAPI } from '@/lib/mutations'
import { useRedirectIfAuthenticated } from '@/hooks/use-redirect-if-authenticated'

export default function ForgotPasswordPage() {
  const { t } = useTranslation()
  useRedirectIfAuthenticated()
  const [loading, setLoading] = useState(false)
  const [sent, setSent] = useState(false)
  const [error, setError] = useState('')

  async function handleSubmit(e: SyntheticEvent<HTMLFormElement>) {
    e.preventDefault()
    setError('')
    setLoading(true)
    const fd = new FormData(e.currentTarget)
    try {
      await forgotPasswordAPI(fd.get('email') as string)
      setSent(true)
    } catch {
      setError(t('errors.network'))
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="min-h-screen flex items-center justify-center bg-muted/40 p-4">
      <div className="w-full max-w-md space-y-6">
        <div className="text-center space-y-1">
          <p className="text-sm font-medium text-muted-foreground">{t('app.name')}</p>
          <h1 className="text-2xl font-semibold tracking-tight">{t('forgotPassword.title')}</h1>
          <p className="text-muted-foreground text-sm">{t('forgotPassword.description')}</p>
        </div>

        <Card>
          <CardContent className="space-y-4">
            {sent ? (
              <Alert>
                <AlertDescription>{t('forgotPassword.sent')}</AlertDescription>
              </Alert>
            ) : (
              <>
                {error && (
                  <Alert variant="destructive">
                    <AlertDescription>{error}</AlertDescription>
                  </Alert>
                )}
                <form onSubmit={handleSubmit} className="space-y-4">
                  <div className="space-y-1.5">
                    <Label htmlFor="email">{t('common.email')}</Label>
                    <Input id="email" name="email" type="email" autoComplete="email" required />
                  </div>
                  <Button type="submit" className="w-full" disabled={loading}>
                    {loading ? t('common.loading') : t('forgotPassword.submit')}
                  </Button>
                </form>
              </>
            )}

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
