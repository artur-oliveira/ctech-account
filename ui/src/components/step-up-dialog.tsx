'use client'

import { useState } from 'react'
import Link from 'next/link'
import { useTranslation } from 'react-i18next'
import { isAxiosError } from '@/lib/axios'
import { oauthClient } from '@/lib/oauth-client'
import { stepUpTOTPAPI, beginStepUpPasskeyAPI, completeStepUpPasskeyAPI } from '@/lib/mutations'
import { buildAssertionCredential } from '@/lib/webauthn'
import { useStepUpStore } from '@/store/step-up'
import { useAuthStore } from '@/store/auth'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { OTPInput } from '@/components/ui/otp-input'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Fingerprint } from 'lucide-react'

const OTP_LENGTH = 6
const HTTP_FORBIDDEN = 403
const HTTP_TOO_MANY = 429
const ENROLLMENT_PROBLEM = 'mfa-enrollment-required'

/**
 * Re-authentication dialog opened by the axios interceptor when the API
 * answers 403 step-up-required. On success it silent-refreshes (the fresh
 * last_mfa_at claim only exists on a new token) and resolves the pending
 * request so the interceptor retries it.
 */
export function StepUpDialog() {
  const { t } = useTranslation()
  const { open, enrollmentRequired, succeed, cancel, requireEnrollment } = useStepUpStore()
  const [code, setCode] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const reset = () => {
    setCode('')
    setError('')
    setBusy(false)
  }

  const finish = async () => {
    // A fresh last_mfa_at claim only lands on a newly issued token.
    const result = await oauthClient.refresh()
    if (!result) throw new Error('Failed to refresh token after step-up')
    useAuthStore.getState().setAccessToken(result.accessToken)
    reset()
    succeed()
  }

  const handleFailure = (err: unknown) => {
    setCode('')
    if (isAxiosError(err) && err.response?.status === HTTP_TOO_MANY) {
      setError(t('stepUp.rateLimited'))
      return
    }
    const problem = isAxiosError(err) ? (err.response?.data as { type?: string } | undefined) : undefined
    if (err && problem?.type?.endsWith(ENROLLMENT_PROBLEM)) {
      requireEnrollment()
      return
    }
    if (isAxiosError(err) && err.response?.status === HTTP_FORBIDDEN) {
      requireEnrollment()
      return
    }
    setError(t('stepUp.invalidCode'))
  }

  const submitTOTP = async (value: string) => {
    setBusy(true)
    setError('')
    try {
      await stepUpTOTPAPI(value)
      await finish()
    } catch (err) {
      handleFailure(err)
    } finally {
      setBusy(false)
    }
  }

  const submitPasskey = async () => {
    setBusy(true)
    setError('')
    try {
      const { session_token, options } = await beginStepUpPasskeyAPI()
      const credential = await buildAssertionCredential(options)
      await completeStepUpPasskeyAPI(session_token, credential)
      await finish()
    } catch (err) {
      handleFailure(err)
    } finally {
      setBusy(false)
    }
  }

  const onCodeChange = (value: string) => {
    setCode(value)
    if (value.length === OTP_LENGTH && !busy) void submitTOTP(value)
  }

  const webAuthnAvailable = typeof window !== 'undefined' && !!window.PublicKeyCredential

  return (
    <Dialog
      open={open}
      onOpenChange={(isOpen) => {
        if (!isOpen) {
          reset()
          cancel()
        }
      }}
    >
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t('stepUp.title')}</DialogTitle>
          <DialogDescription>
            {enrollmentRequired ? t('stepUp.enrollDescription') : t('stepUp.description')}
          </DialogDescription>
        </DialogHeader>

        {enrollmentRequired ? (
          <Button render={<Link href="/account/security" onClick={() => cancel()} />}>
            {t('stepUp.enrollCta')}
          </Button>
        ) : (
          <div className="space-y-4">
            <OTPInput value={code} onChange={onCodeChange} disabled={busy} />
            {error && (
              <Alert variant="destructive">
                <AlertDescription>{error}</AlertDescription>
              </Alert>
            )}
            {webAuthnAvailable && (
              <Button variant="outline" className="w-full" onClick={submitPasskey} disabled={busy}>
                <Fingerprint className="size-4" />
                {t('stepUp.usePasskey')}
              </Button>
            )}
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
}
