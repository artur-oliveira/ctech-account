'use client'

import {Suspense, useMemo, useState, SyntheticEvent} from 'react'
import {useSearchParams} from 'next/navigation'
import Link from 'next/link'
import {useTranslation} from 'react-i18next'
import {Button} from '@/components/ui/button'
import {Label} from '@/components/ui/label'
import {OTPInput} from '@/components/ui/otp-input'
import {Card, CardContent} from '@/components/ui/card'
import {Alert, AlertDescription} from '@/components/ui/alert'
import {Separator} from '@/components/ui/separator'
import {isAxiosError} from '@/lib/axios'
import {startOAuthFlow} from '@/lib/auth-flow'
import {beginMFAPasskeyAPI, completeMFAPasskeyAPI, mfaChallengeAPI} from '@/lib/mutations'
import {buildAssertionCredential} from '@/lib/webauthn'
import {sanitizeContinue} from '@/lib/safe-redirect'
import {MFA_METHODS_KEY, MFA_METHOD_PASSKEY, MFA_METHOD_TOTP, MFA_TOKEN_KEY} from '@/lib/constants'
import {useSessionItem} from '@/hooks/use-session-item'
import {Fingerprint} from 'lucide-react'

const MFA_CODE_LENGTH = 6

function MFASkeleton() {
  return <div className="h-32 animate-pulse bg-muted rounded"/>
}

// null (still hydrating) propagates; anything unparseable means no methods.
function parseMethods(raw: string | null): string[] | null {
  if (raw === null) return null
  try {
    const parsed: unknown = JSON.parse(raw || '[]')
    return Array.isArray(parsed) ? parsed.filter((m): m is string => typeof m === 'string') : []
  } catch {
    return []
  }
}

function MFAForm() {
  const {t} = useTranslation()
  const params = useSearchParams()
  const continueURL = sanitizeContinue(params.get('continue'))
  const [loading, setLoading] = useState(false)
  const [passkeyLoading, setPasskeyLoading] = useState(false)
  const [error, setError] = useState('')
  const [code, setCode] = useState('')
  // null while prerendering and hydrating — see useSessionItem.
  const rawMethods = useSessionItem(MFA_METHODS_KEY)
  const methods = useMemo(() => parseMethods(rawMethods), [rawMethods])

  const hasPasskey = methods?.includes(MFA_METHOD_PASSKEY) ?? false
  const hasTOTP = methods?.includes(MFA_METHOD_TOTP) ?? false

  async function handlePasskey() {
    setError('')
    setPasskeyLoading(true)
    const mfaToken = sessionStorage.getItem(MFA_TOKEN_KEY)
    if (!mfaToken) {
      setError(t('errors.sessionExpired'))
      setPasskeyLoading(false)
      return
    }
    try {
      const {session_token, options} = await beginMFAPasskeyAPI(mfaToken)
      const credential = await buildAssertionCredential(options)
      await completeMFAPasskeyAPI(mfaToken, session_token, credential)
      sessionStorage.removeItem(MFA_TOKEN_KEY)
      sessionStorage.removeItem(MFA_METHODS_KEY)
      await startOAuthFlow(continueURL)
    } catch (err) {
      if (isAxiosError(err)) {
        setError(err.response?.data?.detail ?? t('errors.mfaFailed'))
      } else {
        setError(t('errors.network'))
      }
      setPasskeyLoading(false)
    }
  }

  async function handleTOTP(e: SyntheticEvent<HTMLFormElement>) {
    e.preventDefault()
    if (code.length < MFA_CODE_LENGTH) return
    setError('')
    setLoading(true)
    const mfaToken = sessionStorage.getItem(MFA_TOKEN_KEY)
    if (!mfaToken) {
      setError(t('errors.sessionExpired'))
      setLoading(false)
      return
    }
    try {
      await mfaChallengeAPI(mfaToken, code)
      sessionStorage.removeItem(MFA_TOKEN_KEY)
      sessionStorage.removeItem(MFA_METHODS_KEY)
      await startOAuthFlow(continueURL)
    } catch (err) {
      if (isAxiosError(err)) {
        setError(err.response?.data?.detail ?? t('errors.mfaFailed'))
      } else {
        setError(t('errors.network'))
      }
      setLoading(false)
    }
  }

  if (methods === null) return <MFASkeleton/>

  // Landing here without a challenge in sessionStorage (direct navigation, reload
  // after the token was consumed) would otherwise render an empty card.
  if (!hasPasskey && !hasTOTP) {
    return (
      <div className="space-y-4">
        <Alert variant="destructive">
          <AlertDescription>{t('errors.sessionExpired')}</AlertDescription>
        </Alert>
        <Button className="w-full" render={<Link href="/login"/>}>{t('mfa.backToLogin')}</Button>
      </div>
    )
  }

  return (
    <div className="space-y-4">
      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      {hasPasskey && (
        <Button
          type="button"
          className="w-full"
          onClick={handlePasskey}
          disabled={loading || passkeyLoading}
        >
          <Fingerprint className="size-4"/>
          {passkeyLoading ? t('mfa.passkeyPending') : t('mfa.passkey')}
        </Button>
      )}

      {hasPasskey && hasTOTP && (
        <div className="relative">
          <Separator/>
          <span
            className="absolute left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2 bg-card px-2 text-xs text-muted-foreground">
            {t('login.or')}
          </span>
        </div>
      )}

      {hasTOTP && (
        <form onSubmit={handleTOTP} className="space-y-4">
          <div className="space-y-1.5">
            <Label htmlFor="code">{t('mfa.code')}</Label>
            <OTPInput
              id="code"
              value={code}
              onChange={setCode}
              disabled={loading || passkeyLoading}
              className="justify-center"
            />
          </div>
          <Button type="submit" variant={hasPasskey ? 'outline' : 'default'} className="w-full"
                  disabled={loading || passkeyLoading || code.length < MFA_CODE_LENGTH}>
            {loading ? t('mfa.submitting') : t('mfa.submit')}
          </Button>
        </form>
      )}
    </div>
  )
}

export default function MFAPage() {
  const {t} = useTranslation()
  return (
    <div className="min-h-screen flex items-center justify-center bg-muted/40 p-4">
      <div className="w-full max-w-80 space-y-6">
        <div className="text-center space-y-1">
          <p className="text-sm font-medium text-muted-foreground">{t('app.name')}</p>
          <h1 className="text-2xl font-semibold tracking-tight">{t('mfa.title')}</h1>
          <p className="text-muted-foreground text-sm">{t('mfa.description')}</p>
        </div>

        <Card>
          <CardContent>
            <Suspense fallback={<MFASkeleton/>}>
              <MFAForm/>
            </Suspense>
          </CardContent>
        </Card>
      </div>
    </div>
  )
}
