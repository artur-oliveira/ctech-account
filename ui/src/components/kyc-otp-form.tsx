'use client'

import { SyntheticEvent, useEffect, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { resendKYCCodeAPI, verifyPhoneKYCAPI } from '@/lib/mutations'
import { isAxiosError } from '@/lib/axios'
import { OTP_CODE_LENGTH, OTP_RESEND_COOLDOWN_SECONDS } from '@/lib/constants'
import type { KYCStatus } from '@/lib/types'

function problemSlug(err: unknown): string {
  if (isAxiosError(err)) {
    const type = err.response?.data?.type
    if (typeof type === 'string') return type.slice(type.lastIndexOf('/') + 1)
  }
  return ''
}

/** Phone verification step: enter the SMS code, or request a fresh one after a cooldown. */
export function KYCOtpForm({ status }: { status: KYCStatus }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [code, setCode] = useState('')
  const [cooldown, setCooldown] = useState(OTP_RESEND_COOLDOWN_SECONDS)

  useEffect(() => {
    if (cooldown <= 0) return
    const id = setInterval(() => setCooldown((n) => Math.max(0, n - 1)), 1000)
    return () => clearInterval(id)
  }, [cooldown])

  const { mutate: verify, isPending: verifying, error: verifyError } = useMutation({
    mutationFn: verifyPhoneKYCAPI,
    onSuccess: (st) => queryClient.setQueryData(['kyc'], st),
  })

  const { mutate: resend, isPending: resending, error: resendError } = useMutation({
    mutationFn: resendKYCCodeAPI,
    onSuccess: (st) => {
      queryClient.setQueryData(['kyc'], st)
      setCooldown(OTP_RESEND_COOLDOWN_SECONDS)
      setCode('')
    },
  })

  function handleSubmit(e: SyntheticEvent<HTMLFormElement>) {
    e.preventDefault()
    verify(code)
  }

  const slugMessages: Record<string, string> = {
    'kyc-invalid-code': t('identity.otpInvalidCode'),
    'kyc-resend-cooldown': t('identity.otpResendCooldownError'),
    'kyc-phone-verification-unavailable': t('identity.phoneVerificationUnavailable'),
  }
  const activeError = verifyError ?? resendError
  const errorMsg = activeError ? (slugMessages[problemSlug(activeError)] ?? t('identity.submitFailed')) : null

  return (
    <div className="space-y-4">
      <p className="text-muted-foreground text-sm">{t('identity.otpDescription', { phone: status.phone_masked ?? '' })}</p>

      {errorMsg && (
        <Alert variant="destructive">
          <AlertDescription>{errorMsg}</AlertDescription>
        </Alert>
      )}

      <form onSubmit={handleSubmit} className="space-y-3" noValidate>
        <div className="space-y-1.5">
          <Label htmlFor="otp_code">{t('identity.otpCodeLabel')}</Label>
          <Input
            id="otp_code"
            inputMode="numeric"
            required
            maxLength={OTP_CODE_LENGTH}
            value={code}
            onChange={(e) => setCode(e.target.value.replace(/\D/g, '').slice(0, OTP_CODE_LENGTH))}
            className="min-h-11 tracking-widest"
          />
        </div>
        <div className="flex items-center gap-2">
          <Button type="submit" className="min-h-11" disabled={verifying || code.length !== OTP_CODE_LENGTH}>
            {verifying ? t('identity.submitting') : t('identity.otpSubmit')}
          </Button>
          <Button
            type="button"
            variant="outline"
            className="min-h-11"
            disabled={resending || cooldown > 0}
            onClick={() => resend()}
          >
            {cooldown > 0 ? t('identity.otpResendCooldown', { seconds: cooldown }) : t('identity.otpResend')}
          </Button>
        </div>
      </form>
    </div>
  )
}
