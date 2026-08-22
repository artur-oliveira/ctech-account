'use client'

import {Suspense, useMemo, useState, SyntheticEvent} from 'react'
import {useRouter, useSearchParams} from 'next/navigation'
import Link from 'next/link'
import {useTranslation} from 'react-i18next'
import {toast} from 'sonner'
import {Button} from '@/components/ui/button'
import {Label} from '@/components/ui/label'
import {OTPInput} from '@/components/ui/otp-input'
import {Card, CardContent} from '@/components/ui/card'
import {Alert, AlertDescription} from '@/components/ui/alert'
import {isAxiosError} from '@/lib/axios'
import {startOAuthFlow} from '@/lib/auth-flow'
import {mfaChallengeAPI} from '@/lib/mutations'
import {sanitizeContinue} from '@/lib/safe-redirect'
import {isTOTPMFAMethod, MFA_INVALID_TOKEN_PROBLEM, MFA_METHODS_KEY, MFA_TOKEN_KEY} from '@/lib/constants'
import {useSessionItem} from '@/hooks/use-session-item'
import {reportClientError} from '@/lib/client-logging'

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
  const router = useRouter()
  const params = useSearchParams()
  const continueURL = sanitizeContinue(params.get('continue'))
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [code, setCode] = useState('')
  // null while prerendering and hydrating — see useSessionItem.
  const rawMethods = useSessionItem(MFA_METHODS_KEY)
  const methods = useMemo(() => parseMethods(rawMethods), [rawMethods])

  const hasTOTP = methods?.some(isTOTPMFAMethod) ?? false

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
      reportClientError('mfa-login', err)
      if (isAxiosError(err)) {
        const problemType = err.response?.data?.type
        // mfa_token is dead (expired, or invalidated after too many wrong
        // attempts) — retrying can never succeed, so restart the login flow
        // instead of leaving the user stuck resubmitting a dead token.
        if (typeof problemType === 'string' && problemType.endsWith(MFA_INVALID_TOKEN_PROBLEM)) {
          sessionStorage.removeItem(MFA_TOKEN_KEY)
          sessionStorage.removeItem(MFA_METHODS_KEY)
          toast.error(t('errors.sessionExpired'))
          router.replace('/login')
          return
        }
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
  if (!hasTOTP) {
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

      <form onSubmit={handleTOTP} className="space-y-4">
        <div className="space-y-1.5">
          <Label htmlFor="code">{t('mfa.code')}</Label>
          <OTPInput
            id="code"
            value={code}
            onChange={setCode}
            disabled={loading}
            className="justify-center"
          />
        </div>
        <Button type="submit" className="w-full"
                disabled={loading || code.length < MFA_CODE_LENGTH}>
          {loading ? t('mfa.submitting') : t('mfa.submit')}
        </Button>
      </form>
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
