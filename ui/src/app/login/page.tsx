'use client'

import {Suspense, SyntheticEvent, useCallback, useEffect, useRef, useState} from 'react'
import {useRouter, useSearchParams} from 'next/navigation'
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

type AuthContinuation = {
  requires_mfa?: boolean
  mfa_token?: string
  mfa_methods?: string[]
}

function isPasskeyCancellation(error: unknown): boolean {
  return error instanceof DOMException && (error.name === 'AbortError' || error.name === 'NotAllowedError')
}

function LoginForm() {
  const {t} = useTranslation()
  const router = useRouter()
  const params = useSearchParams()
  const rawContinue = params.get('continue')
  const continueURL = sanitizeContinue(rawContinue)

  useRedirectIfAuthenticated(rawContinue)
  const [loading, setLoading] = useState(false)
  const [passkeyLoading, setPasskeyLoading] = useState(false)
  const [error, setError] = useState('')
  const [errorVariant, setErrorVariant] = useState<'default' | 'destructive'>('destructive')
  // Set when the API rejects a valid password because the address is unverified,
  // so we can offer to resend the link instead of just showing an error.
  const [unverifiedEmail, setUnverifiedEmail] = useState('')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const conditionalAbortRef = useRef<AbortController | null>(null)
  const authFlowStartedRef = useRef(false)

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

  const continueAfterAuthentication = useCallback(async (result: AuthContinuation) => {
    if (authFlowStartedRef.current) return
    authFlowStartedRef.current = true
    try {
      if (result.requires_mfa && result.mfa_token) {
        sessionStorage.setItem(MFA_TOKEN_KEY, result.mfa_token)
        sessionStorage.setItem(MFA_METHODS_KEY, JSON.stringify(result.mfa_methods ?? []))
        sessionStorage.setItem(CONTINUE_URL_KEY, continueURL)
        router.replace(`/login/mfa?continue=${encodeURIComponent(continueURL)}`)
        return
      }
      await startOAuthFlow(continueURL)
    } catch (continuationError) {
      authFlowStartedRef.current = false
      throw continuationError
    }
  }, [continueURL, router])

  useEffect(() => {
    if (typeof PublicKeyCredential === 'undefined') return
    const conditionalAPI = PublicKeyCredential as typeof PublicKeyCredential & {
      isConditionalMediationAvailable?: () => Promise<boolean>
    }
    if (!conditionalAPI.isConditionalMediationAvailable) return

    const controller = new AbortController()
    conditionalAbortRef.current = controller
    let active = true

    async function startConditionalPasskey() {
      try {
        if (!await conditionalAPI.isConditionalMediationAvailable?.() || !active) return
        const {session_token, options} = await beginPasskeyAuthAPI()
        if (!active) return
        const credential = await buildAssertionCredential(options, {
          mediation: 'conditional',
          signal: controller.signal,
        })
        if (!active) return

        setPasskeyLoading(true)
        const result = await completePasskeyAuthAPI(session_token, credential)
        await continueAfterAuthentication(result)
      } catch (conditionalError) {
        // Conditional mediation is progressive enhancement. Unsupported,
        // cancelled, rate-limited or offline initialization must leave the
        // password and explicit passkey controls fully usable.
        if (!active || isPasskeyCancellation(conditionalError)) return
        if (authFlowStartedRef.current) {
          authFlowStartedRef.current = false
        }
        setPasskeyLoading(false)
      }
    }

    void startConditionalPasskey()
    return () => {
      active = false
      controller.abort()
      if (conditionalAbortRef.current === controller) conditionalAbortRef.current = null
    }
  }, [continueAfterAuthentication])

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
    conditionalAbortRef.current?.abort()
    setError('')
    setErrorVariant('destructive')
    setUnverifiedEmail('')
    setLoading(true)

    try {
      const {data} = await api.post<AuthContinuation>('/v1.0/auth/login', {email, password})
      await continueAfterAuthentication(data)
    } catch (submitError) {
      if (isAxiosError(submitError)) {
        if (submitError.response?.status === HTTP_FORBIDDEN) {
          setUnverifiedEmail(email)
        }
        setError(submitError.response?.data?.detail ?? t('errors.loginFailed'))
      } else {
        setError(t('errors.network'))
      }
      setLoading(false)
    }
  }

  async function handlePasskeyLogin() {
    conditionalAbortRef.current?.abort()
    setError('')
    setErrorVariant('destructive')
    setPasskeyLoading(true)
    try {
      const {session_token, options} = await beginPasskeyAuthAPI()
      const credential = await buildAssertionCredential(options)
      const result = await completePasskeyAuthAPI(session_token, credential)
      await continueAfterAuthentication(result)
    } catch (passkeyError) {
      if (isPasskeyCancellation(passkeyError)) {
        setErrorVariant('default')
        setError(t('login.passkeyCancelled'))
      } else if (isAxiosError(passkeyError)) {
        setError(passkeyError.response?.data?.detail ?? t('errors.loginFailed'))
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
        <Alert variant={error ? errorVariant : 'destructive'}>
          <AlertDescription className="space-y-2">
            <span className="block">{displayError}</span>
            {unverifiedEmail && (
              <button
                type="button"
                onClick={handleResendVerification}
                className="inline-flex min-h-6 items-center font-medium underline underline-offset-4 max-sm:min-h-11"
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
            autoComplete="username webauthn"
            required
            placeholder={t('login.emailPlaceholder')}
            value={email}
            onChange={(event) => setEmail(event.target.value)}
            className="max-sm:h-11"
          />
        </div>

        <div className="space-y-1.5">
          <div className="flex items-center justify-between gap-3">
            <Label htmlFor="password">{t('common.password')}</Label>
            <Link
              href="/forgot-password"
              className="inline-flex min-h-6 items-center text-xs text-muted-foreground underline underline-offset-4 hover:text-foreground max-sm:min-h-11"
            >
              {t('login.forgotPassword')}
            </Link>
          </div>
          <Input
            id="password"
            name="password"
            type="password"
            autoComplete="current-password"
            required
            value={password}
            onChange={(event) => setPassword(event.target.value)}
            className="max-sm:h-11"
          />
        </div>

        <Button type="submit" className="w-full max-sm:min-h-11" disabled={loading || passkeyLoading}>
          {loading ? t('login.submitting') : t('login.submit')}
        </Button>
      </form>

      <div className="relative">
        <Separator/>
        <span className="absolute left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2 bg-card px-2 text-xs text-muted-foreground">
          {t('login.or')}
        </span>
      </div>

      <Button
        type="button"
        variant="outline"
        className="w-full max-sm:min-h-11"
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
                className="inline-flex items-center text-foreground underline underline-offset-4 hover:text-primary max-sm:min-h-11"
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
