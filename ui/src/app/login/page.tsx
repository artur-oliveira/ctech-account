'use client'

import {Suspense, useState, SyntheticEvent} from 'react'
import {useSearchParams} from 'next/navigation'
import Link from 'next/link'
import {useTranslation} from 'react-i18next'
import {Button} from '@/components/ui/button'
import {Input} from '@/components/ui/input'
import {Label} from '@/components/ui/label'
import {Card, CardContent} from '@/components/ui/card'
import {Alert, AlertDescription} from '@/components/ui/alert'
import {Separator} from '@/components/ui/separator'
import {toast} from 'sonner'
import {api, isAxiosError} from '@/lib/axios'
import {startOAuthFlow} from '@/lib/auth-flow'
import {beginPasskeyAuthAPI, completePasskeyAuthAPI, resendVerificationAPI} from '@/lib/mutations'
import {buildAssertionCredential} from '@/lib/webauthn'
import {sanitizeContinue} from '@/lib/safe-redirect'
import {CONTINUE_URL_KEY, MFA_METHODS_KEY, MFA_TOKEN_KEY} from '@/lib/constants'
import {useRedirectIfAuthenticated} from '@/hooks/use-redirect-if-authenticated'
import {GoogleSignInButton} from '@/components/google-sign-in-button'
import {Fingerprint} from 'lucide-react'

/** The API answers 403 when the password is correct but the email is unverified. */
const HTTP_FORBIDDEN = 403

function LoginForm() {
  const {t} = useTranslation()
  const params = useSearchParams()
  const rawContinue = params.get('continue')
  const continueURL = sanitizeContinue(rawContinue)

  useRedirectIfAuthenticated(rawContinue)
  const [loading, setLoading] = useState(false)
  const [passkeyLoading, setPasskeyLoading] = useState(false)
  const [error, setError] = useState('')
  // Set when the API rejects a valid password because the address is unverified,
  // so we can offer to resend the link instead of just showing an error.
  const [unverifiedEmail, setUnverifiedEmail] = useState('')

  // OAuth errors the API reports by redirecting back with ?error=… (e.g. a
  // cancelled Google sign-in, or a Google account whose email the provider
  // never verified — which the backend refuses to trust). Derived, not stored,
  // so a form-submission error still takes precedence when present.
  const oauthErrorMessages: Record<string, string> = {
    google_denied: t('errors.googleDenied'),
    google_email_unverified: t('errors.googleEmailUnverified'),
  }
  const oauthErrorCode = params.get('error')
  const oauthError = oauthErrorCode
    ? (oauthErrorMessages[oauthErrorCode] ?? t('errors.loginFailed'))
    : ''

  async function handleResendVerification() {
    if (!unverifiedEmail) return
    try {
      await resendVerificationAPI(unverifiedEmail)
      toast.success(t('login.verificationResent'))
    } catch {
      toast.error(t('errors.network'))
    }
  }

  async function handleSubmit(e: SyntheticEvent<HTMLFormElement>) {
    e.preventDefault()
    setError('')
    setUnverifiedEmail('')
    setLoading(true)
    const fd = new FormData(e.currentTarget)
    const email = fd.get('email') as string

    try {
      const {data} = await api.post<{
        requires_mfa: boolean
        mfa_token?: string
        mfa_methods?: string[]
      }>('/v1.0/auth/login', {
        email,
        password: fd.get('password'),
      })

      if (data.requires_mfa && data.mfa_token) {
        sessionStorage.setItem(MFA_TOKEN_KEY, data.mfa_token)
        sessionStorage.setItem(MFA_METHODS_KEY, JSON.stringify(data.mfa_methods ?? []))
        sessionStorage.setItem(CONTINUE_URL_KEY, continueURL)
        window.location.href = `/login/mfa?continue=${encodeURIComponent(continueURL)}`
        return
      }

      await startOAuthFlow(continueURL)
    } catch (err) {
      if (isAxiosError(err)) {
        if (err.response?.status === HTTP_FORBIDDEN) {
          setUnverifiedEmail(email)
        }
        setError(err.response?.data?.detail ?? t('errors.loginFailed'))
      } else {
        setError(t('errors.network'))
      }
      setLoading(false)
    }
  }

  async function handlePasskeyLogin() {
    setError('')
    setPasskeyLoading(true)
    try {
      const {session_token, options} = await beginPasskeyAuthAPI()
      const credential = await buildAssertionCredential(options)
      const result = await completePasskeyAuthAPI(session_token, credential)
      if (result?.requires_mfa && result?.mfa_token) {
        sessionStorage.setItem(MFA_TOKEN_KEY, result.mfa_token)
        sessionStorage.setItem(MFA_METHODS_KEY, JSON.stringify(result.mfa_methods ?? []))
        sessionStorage.setItem(CONTINUE_URL_KEY, continueURL)
        window.location.href = `/login/mfa?continue=${encodeURIComponent(continueURL)}`
        return
      }
      await startOAuthFlow(continueURL)
    } catch (err) {
      if (isAxiosError(err)) {
        setError(err.response?.data?.detail ?? t('errors.loginFailed'))
      } else {
        setError(t('errors.network'))
      }
      setPasskeyLoading(false)
    }
  }

  const displayError = error || oauthError

  return (
    <div className="space-y-4">
      {displayError && (
        <Alert variant="destructive">
          <AlertDescription className="space-y-2">
            <span className="block">{displayError}</span>
            {unverifiedEmail && (
              <button
                type="button"
                onClick={handleResendVerification}
                className="underline underline-offset-4 font-medium"
              >
                {t('login.resendVerification')}
              </button>
            )}
          </AlertDescription>
        </Alert>
      )}

      <form onSubmit={handleSubmit} className="space-y-4">
        <div className="space-y-1.5">
          <Label htmlFor="email">{t('common.email')}</Label>
          <Input
            id="email"
            name="email"
            type="email"
            autoComplete="email"
            required
            placeholder={t('login.emailPlaceholder')}
          />
        </div>

        <div className="space-y-1.5">
          <div className="flex items-center justify-between">
            <Label htmlFor="password">{t('common.password')}</Label>
            <Link href="/forgot-password"
                  className="text-xs text-muted-foreground hover:text-foreground underline underline-offset-4">
              {t('login.forgotPassword')}
            </Link>
          </div>
          <Input
            id="password"
            name="password"
            type="password"
            autoComplete="current-password"
            required
          />
        </div>

        <Button type="submit" className="w-full" disabled={loading || passkeyLoading}>
          {loading ? t('login.submitting') : t('login.submit')}
        </Button>
      </form>

      <div className="relative">
        <Separator/>
        <span
          className="absolute left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2 bg-card px-2 text-xs text-muted-foreground">
          {t('login.or')}
        </span>
      </div>

      <Button
        type="button"
        variant="outline"
        className="w-full"
        onClick={handlePasskeyLogin}
        disabled={loading || passkeyLoading}
      >
        <Fingerprint className="size-4"/>
        {passkeyLoading ? t('login.passkeyPending') : t('login.passkey')}
      </Button>

      <GoogleSignInButton
        continueURL={continueURL}
        label={t('login.google')}
        disabled={loading || passkeyLoading}
      />
    </div>
  )
}

export default function LoginPage() {
  const {t} = useTranslation()
  return (
    <div className="min-h-screen flex items-center justify-center bg-muted/40 p-4">
      <div className="w-full max-w-md space-y-6">
        <div className="text-center space-y-1">
          <p className="text-sm font-medium text-muted-foreground">{t('app.name')}</p>
          <h1 className="text-2xl font-semibold tracking-tight">{t('login.title')}</h1>
          <p className="text-muted-foreground text-sm">{t('login.description')}</p>
        </div>

        <Card>
          <CardContent>
            <Suspense fallback={<div className="h-40 animate-pulse bg-muted rounded"/>}>
              <LoginForm/>
            </Suspense>

            <p className="mt-4 text-center text-sm text-muted-foreground">
              {t('login.noAccount')}{' '}
              <Link
                href="/register"
                className="text-foreground underline underline-offset-4 hover:text-primary"
              >
                {t('login.createOne')}
              </Link>
            </p>
          </CardContent>
        </Card>
      </div>
    </div>
  )
}
