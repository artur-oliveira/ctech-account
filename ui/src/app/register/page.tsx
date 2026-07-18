'use client'

import {SyntheticEvent, useState} from 'react'
import {useRouter} from 'next/navigation'
import Link from 'next/link'
import {useTranslation} from 'react-i18next'
import {Button} from '@/components/ui/button'
import {Input} from '@/components/ui/input'
import {Label} from '@/components/ui/label'
import {Card, CardContent} from '@/components/ui/card'
import {Alert, AlertDescription} from '@/components/ui/alert'
import {api, isAxiosError} from '@/lib/axios'
import {useRedirectIfAuthenticated} from '@/hooks/use-redirect-if-authenticated'
import {GoogleSignInButton} from '@/components/google-sign-in-button'
import {DEFAULT_REDIRECT} from '@/lib/safe-redirect'
import {Separator} from '@/components/ui/separator'

export default function RegisterPage() {
  const {t} = useTranslation()
  const router = useRouter()
  useRedirectIfAuthenticated()
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  async function handleSubmit(e: SyntheticEvent<HTMLFormElement>) {
    e.preventDefault()
    setError('')
    setLoading(true)
    const fd = new FormData(e.currentTarget)

    const password = fd.get('password') as string
    const confirm = fd.get('confirm_password') as string
    if (password !== confirm) {
      setError(t('errors.passwordMismatch'))
      setLoading(false)
      return
    }

    if (fd.get('accept_terms') !== 'on') {
      setError(t('register.acceptTermsRequired'))
      setLoading(false)
      return
    }

    try {
      await api.post('/v1.0/auth/register', {
        email: fd.get('email'),
        password,
        first_name: fd.get('first_name'),
        last_name: fd.get('last_name'),
        accept_terms: true,
      })

      // The API answers identically whether or not the address was already taken,
      // so the only correct next screen is "check your email".
      router.push('/register/verify')
    } catch (err) {
      if (isAxiosError(err)) {
        setError(err.response?.data?.detail ?? t('errors.registrationFailed'))
      } else {
        setError(t('errors.network'))
      }
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="min-h-screen flex items-center justify-center bg-muted/40 p-4">
      <div className="w-full max-w-md space-y-6">
        <div className="text-center space-y-1">
          <p className="text-sm font-medium text-muted-foreground">{t('app.name')}</p>
          <h1 className="text-2xl font-semibold tracking-tight">{t('register.title')}</h1>
          <p className="text-muted-foreground text-sm">{t('register.description')}</p>
        </div>

        <Card>
          <CardContent>
            <form onSubmit={handleSubmit} className="space-y-4">
              {error && (
                <Alert variant="destructive">
                  <AlertDescription>{error}</AlertDescription>
                </Alert>
              )}

              <div className="grid grid-cols-2 gap-3">
                <div className="space-y-1.5">
                  <Label htmlFor="first_name">{t('register.firstName')}</Label>
                  <Input id="first_name" name="first_name" required autoComplete="given-name"/>
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="last_name">{t('register.lastName')}</Label>
                  <Input id="last_name" name="last_name" autoComplete="family-name"/>
                </div>
              </div>

              <div className="space-y-1.5">
                <Label htmlFor="email">{t('common.email')}</Label>
                <Input id="email" name="email" type="email" autoComplete="email" required
                       placeholder={t('login.emailPlaceholder')}/>
              </div>

              <div className="space-y-1.5">
                <Label htmlFor="password">{t('common.password')}</Label>
                <Input id="password" name="password" type="password" autoComplete="new-password" required
                       minLength={8}/>
              </div>

              <div className="space-y-1.5">
                <Label htmlFor="confirm_password">{t('register.confirmPassword')}</Label>
                <Input id="confirm_password" name="confirm_password" type="password" autoComplete="new-password"
                       required minLength={8}/>
              </div>

              <div className="flex items-center gap-2">
                <input
                  id="accept_terms"
                  name="accept_terms"
                  type="checkbox"
                  required
                  className="mt-0.5 size-4 shrink-0 rounded border-input accent-primary"
                />
                <Label htmlFor="accept_terms" className="flex-1 items-center gap-2 text-sm font-normal text-muted-foreground">
                  {t('register.acceptTermsPrefix')}{' '}
                  <Link href="/terms" target="_blank"
                        className="text-foreground underline underline-offset-4 hover:text-primary">
                    {t('register.termsOfService')}
                  </Link>{' '}
                  {t('register.and')}{' '}
                  <Link href="/privacy" target="_blank"
                        className="text-foreground underline underline-offset-4 hover:text-primary">
                    {t('register.privacyPolicy')}
                  </Link>
                  .
                </Label>
              </div>

              <Button type="submit" className="w-full" disabled={loading}>
                {loading ? t('register.submitting') : t('register.submit')}
              </Button>
            </form>

            <div className="mt-4 space-y-4">
              <div className="flex items-center gap-3">
                <Separator className="flex-1"/>
                <span className="text-xs text-muted-foreground">{t('login.or')}</span>
                <Separator className="flex-1"/>
              </div>

              <GoogleSignInButton
                continueURL={DEFAULT_REDIRECT}
                label={t('register.google')}
                disabled={loading}
              />
            </div>

            <p className="mt-4 text-center text-sm text-muted-foreground">
              {t('register.alreadyHave')}{' '}
              <Link href="/login" className="text-foreground underline underline-offset-4 hover:text-primary">
                {t('register.signIn')}
              </Link>
            </p>
          </CardContent>
        </Card>
      </div>
    </div>
  )
}
