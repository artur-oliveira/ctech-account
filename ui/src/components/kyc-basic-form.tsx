'use client'

import { SyntheticEvent, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { submitBasicKYCAPI } from '@/lib/mutations'
import { isAxiosError } from '@/lib/axios'
import { CPF_DIGITS, KYC_MIN_AGE_YEARS } from '@/lib/constants'
import type { KYCStatus } from '@/lib/types'

function formatCPFInput(value: string): string {
  const digits = value.replace(/\D/g, '').slice(0, CPF_DIGITS)
  return digits
    .replace(/(\d{3})(\d)/, '$1.$2')
    .replace(/(\d{3})\.(\d{3})(\d)/, '$1.$2.$3')
    .replace(/(\d{3})\.(\d{3})\.(\d{3})(\d)/, '$1.$2.$3-$4')
}

function formatPhoneInput(value: string): string {
  const digits = value.replace(/\D/g, '').slice(0, 13)
  if (digits.length <= 2) return digits ? `+${digits}` : ''
  return `+${digits.slice(0, 2)} ${digits.slice(2)}`
}

function phoneDigitsToE164(value: string): string {
  return '+' + value.replace(/\D/g, '')
}

function problemSlug(err: unknown): string {
  if (isAxiosError(err)) {
    const type = err.response?.data?.type
    if (typeof type === 'string') return type.slice(type.lastIndexOf('/') + 1)
  }
  return ''
}

function isValidCPF(cpf: string): boolean {
  if (!/^\d{11}$/.test(cpf) || /^(\d)\1{10}$/.test(cpf)) return false
  const checkDigit = (pos: number) => {
    let sum = 0
    for (let i = 0; i < pos; i++) sum += Number(cpf[i]) * (pos + 1 - i)
    const d = 11 - (sum % 11)
    return d >= 10 ? 0 : d
  }
  return checkDigit(9) === Number(cpf[9]) && checkDigit(10) === Number(cpf[10])
}

function isEligibleAge(birthDate: string, years: number): boolean {
  const born = new Date(`${birthDate}T00:00:00Z`)
  if (Number.isNaN(born.getTime())) return false
  const eligibleFrom = new Date(born)
  eligibleFrom.setUTCFullYear(eligibleFrom.getUTCFullYear() + years)
  return new Date() >= eligibleFrom
}

/**
 * Basic KYC step: CPF/name/birthdate/phone. Resubmittable while pending phone
 * verification (a mistyped field doesn't need a separate "edit" mode), locked
 * once phone-verified (server returns kyc-already-verified past that point).
 */
export function KYCBasicForm({ status }: { status: KYCStatus }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [cpf, setCPF] = useState('')
  const [legalName, setLegalName] = useState(status.legal_name ?? '')
  const [birthDate, setBirthDate] = useState(status.birth_date ?? '')
  const [phone, setPhone] = useState('')
  const [cpfError, setCpfError] = useState(false)
  const [fieldErrors, setFieldErrors] = useState<Partial<Record<'legal_name' | 'birth_date' | 'phone', string>>>({})

  const { mutate: submit, isPending, error } = useMutation({
    mutationFn: submitBasicKYCAPI,
    onSuccess: (st) => queryClient.setQueryData(['kyc'], st),
  })

  function handleSubmit(e: SyntheticEvent<HTMLFormElement>) {
    e.preventDefault()
    const digits = cpf.replace(/\D/g, '')
    const cpfValid = isValidCPF(digits)
    setCpfError(!cpfValid)

    const errors: Partial<Record<'legal_name' | 'birth_date' | 'phone', string>> = {}
    if (legalName.trim().length < 3) errors.legal_name = t('identity.fieldRequired')
    if (!birthDate) errors.birth_date = t('identity.fieldRequired')
    else if (!isEligibleAge(birthDate, KYC_MIN_AGE_YEARS)) errors.birth_date = t('identity.underage')
    if (phone.replace(/\D/g, '').length < 10) errors.phone = t('identity.fieldRequired')
    setFieldErrors(errors)

    if (!cpfValid || Object.keys(errors).length > 0) return
    submit({ cpf: digits, legal_name: legalName, birth_date: birthDate, phone_number: phoneDigitsToE164(phone) })
  }

  const slugMessages: Record<string, string> = {
    'age-requirement-not-met': t('identity.underage'),
    'cpf-already-registered': t('identity.cpfTaken'),
    'kyc-already-verified': t('identity.alreadyVerified'),
    'kyc-phone-verification-unavailable': t('identity.phoneVerificationUnavailable'),
    'validation-failed': t('identity.invalidData'),
  }
  const errorMsg = error ? (slugMessages[problemSlug(error)] ?? t('identity.submitFailed')) : null
  const maxBirthDate = new Date().toISOString().slice(0, 10)

  return (
    <form onSubmit={handleSubmit} className="space-y-4" noValidate>
      {errorMsg && (
        <Alert variant="destructive">
          <AlertDescription>{errorMsg}</AlertDescription>
        </Alert>
      )}

      <div className="space-y-1.5">
        <Label htmlFor="cpf">{t('identity.cpf')}</Label>
        <Input
          id="cpf"
          required
          inputMode="numeric"
          placeholder="000.000.000-00"
          value={cpf}
          onChange={(e) => {
            setCPF(formatCPFInput(e.target.value))
            setCpfError(false)
          }}
          aria-invalid={cpfError}
          aria-describedby={cpfError ? 'cpf-error' : undefined}
          className="min-h-11"
        />
        {cpfError && (
          <p id="cpf-error" className="text-destructive text-sm">
            {t('identity.cpfInvalid')}
          </p>
        )}
      </div>

      <div className="space-y-1.5">
        <Label htmlFor="legal_name">{t('identity.legalName')}</Label>
        <Input
          id="legal_name"
          required
          minLength={3}
          value={legalName}
          onChange={(e) => setLegalName(e.target.value)}
          aria-invalid={!!fieldErrors.legal_name}
          className="min-h-11"
        />
        {fieldErrors.legal_name && <p className="text-destructive text-sm">{fieldErrors.legal_name}</p>}
      </div>

      <div className="space-y-1.5">
        <Label htmlFor="birth_date">{t('identity.birthDate')}</Label>
        <Input
          id="birth_date"
          type="date"
          required
          max={maxBirthDate}
          value={birthDate}
          onChange={(e) => setBirthDate(e.target.value)}
          aria-invalid={!!fieldErrors.birth_date}
          className="min-h-11"
        />
        {fieldErrors.birth_date && <p className="text-destructive text-sm">{fieldErrors.birth_date}</p>}
      </div>

      <div className="space-y-1.5">
        <Label htmlFor="phone">{t('identity.phoneNumber')}</Label>
        <Input
          id="phone"
          type="tel"
          required
          placeholder="+55 11987654321"
          value={phone}
          onChange={(e) => setPhone(formatPhoneInput(e.target.value))}
          aria-invalid={!!fieldErrors.phone}
          className="min-h-11"
        />
        {fieldErrors.phone && <p className="text-destructive text-sm">{fieldErrors.phone}</p>}
        <p className="text-muted-foreground text-xs">{t('identity.phoneNumberHint')}</p>
      </div>

      <Button type="submit" className="min-h-11" disabled={isPending}>
        {isPending ? t('identity.submitting') : t('identity.submit')}
      </Button>
    </form>
  )
}
