'use client'

import { SyntheticEvent, useEffect, useRef, useState } from 'react'
import Link from 'next/link'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Card, CardContent, CardDescription, CardHeader } from '@/components/ui/card'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Separator } from '@/components/ui/separator'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { cn } from '@/lib/utils'
import { KYCDocumentUpload } from '@/components/kyc-document-upload'
import { SelfieCapture } from '@/components/selfie-capture'
import { CheckCircle2, Circle, Clock, FileSearch, Info, XCircle } from 'lucide-react'
import { fetchKYC, fetchPasskeys, fetchTOTPStatus } from '@/lib/queries'
import { submitKYCAPI } from '@/lib/mutations'
import { isAxiosError } from '@/lib/axios'
import { formatDate } from '@/lib/format'
import { formatZipCodeInput, lookupZipCode, normalizeZipCode } from '@/lib/viacep'
import {
  BRAZILIAN_STATES,
  CPF_DIGITS,
  KYC_MIN_AGE_YEARS,
  REQUIRED_DOC_TYPES,
  SUPPORT_EMAIL,
  ZIP_CODE_DIGITS,
} from '@/lib/constants'
import type { Address, KYCDocumentType, KYCStatus } from '@/lib/types'
import { toast } from 'sonner'
import { QueryError } from '@/components/query-error'

// formatCPFInput renders digits as 000.000.000-00 while typing.
function formatCPFInput(value: string): string {
  const digits = value.replace(/\D/g, '').slice(0, CPF_DIGITS)
  return digits
    .replace(/(\d{3})(\d)/, '$1.$2')
    .replace(/(\d{3})\.(\d{3})(\d)/, '$1.$2.$3')
    .replace(/(\d{3})\.(\d{3})\.(\d{3})(\d)/, '$1.$2.$3-$4')
}

// problemSlug extracts the RFC 7807 type slug for error mapping.
function problemSlug(err: unknown): string {
  if (isAxiosError(err)) {
    const type = err.response?.data?.type
    if (typeof type === 'string') return type.slice(type.lastIndexOf('/') + 1)
  }
  return ''
}

// isValidCPF mirrors internal/domain/kyc/cpf.go's mod-11 checksum so a
// mistyped CPF is caught before review, not after a server round trip.
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

// isEligibleAge mirrors internal/domain/kyc/service.go's isAtLeast (date
// arithmetic so the birthday itself counts) — a client-side pre-check only,
// so an underage submitter is blocked before reaching the "cannot be
// changed" confirm step instead of after it. The server remains authoritative.
function isEligibleAge(birthDate: string, years: number): boolean {
  const born = new Date(`${birthDate}T00:00:00Z`)
  if (Number.isNaN(born.getTime())) return false
  const eligibleFrom = new Date(born)
  eligibleFrom.setUTCFullYear(eligibleFrom.getUTCFullYear() + years)
  return new Date() >= eligibleFrom
}

const REQUIRED_ADDRESS_FIELDS: (keyof Address)[] = ['zip_code', 'street', 'number', 'district', 'city', 'state']

const READ_ONLY_LOCK_CLASS = 'read-only:bg-muted read-only:cursor-default'

const EMPTY_ADDRESS: Address = {
  zip_code: '',
  street: '',
  number: '',
  complement: '',
  district: '',
  city: '',
  state: '',
}

// Checklist/history contexts need a noun label ("Liveness — up"), not the
// imperative camera instruction ("Look up, then start recording") that
// SelfieCapture shows while the pose is actually being recorded.
const DOCUMENT_LABEL_KEY: Record<KYCDocumentType, string> = {
  id_front: 'identity.documentIdFront',
  id_back: 'identity.documentIdBack',
  selfie_up: 'identity.livenessUp',
  selfie_down: 'identity.livenessDown',
  selfie_left: 'identity.livenessLeft',
  selfie_right: 'identity.livenessRight',
}

function ProgressChecklist({ uploadedTypes }: { uploadedTypes: KYCDocumentType[] }) {
  const { t } = useTranslation()
  const done = REQUIRED_DOC_TYPES.filter((docType) => uploadedTypes.includes(docType)).length

  return (
    <div className="space-y-2">
      <p className="text-sm font-medium">{t('identity.progressLabel', { done, total: REQUIRED_DOC_TYPES.length })}</p>
      <ul className="grid grid-cols-2 gap-x-4 gap-y-1.5 sm:grid-cols-3">
        {REQUIRED_DOC_TYPES.map((docType) => {
          const isDone = uploadedTypes.includes(docType)
          return (
            <li key={docType} className="flex items-center gap-1.5 text-sm">
              {isDone ? (
                <CheckCircle2 className="text-primary size-4 shrink-0" />
              ) : (
                <Circle className="text-muted-foreground size-4 shrink-0" />
              )}
              <span className={isDone ? 'text-foreground' : 'text-muted-foreground'}>{t(DOCUMENT_LABEL_KEY[docType])}</span>
            </li>
          )
        })}
      </ul>
    </div>
  )
}

function StateBadge({ status }: { status: KYCStatus }) {
  const { t } = useTranslation()
  switch (status.state) {
    case 'verified':
      return (
        <Badge variant="default">
          <CheckCircle2 className="size-3.5" />
          {t('identity.levelVerified')}
        </Badge>
      )
    case 'awaiting_files':
      return (
        <Badge variant="secondary">
          <Clock className="size-3.5" />
          {t('identity.levelAwaitingFiles')}
        </Badge>
      )
    case 'under_review':
      return (
        <Badge variant="secondary">
          <FileSearch className="size-3.5" />
          {t('identity.levelUnderReview')}
        </Badge>
      )
    case 'rejected':
      return (
        <Badge variant="destructive">
          <XCircle className="size-3.5" />
          {t('identity.levelRejected')}
        </Badge>
      )
    default:
      return <Badge variant="outline">{t('identity.levelNone')}</Badge>
  }
}

function FieldError({ id, message }: { id: string; message?: string }) {
  if (!message) return null
  return (
    <p id={id} className="text-destructive text-sm">
      {message}
    </p>
  )
}

function AddressFields({
  address,
  onChange,
  errors,
}: {
  address: Address
  onChange: (updater: (prev: Address) => Address) => void
  errors?: Partial<Record<keyof Address, string>>
}) {
  const { t } = useTranslation()
  const [zipNotFound, setZipNotFound] = useState(false)
  // Success is silent otherwise: fields just change under a screen-reader
  // user with no cue. Keyed by zip so a repeat lookup re-announces even when
  // the resulting message text would otherwise be identical.
  const [zipFound, setZipFound] = useState('')
  // Guards against an in-flight lookup resolving after a newer one — without
  // it, editing the CEP again before the first request settles could let a
  // slower, stale response overwrite the address with outdated data.
  const latestZipRef = useRef('')

  // ViaCEP fills street/district/city/state as soon as the CEP is complete; the
  // user still types number and complement. Every update goes through the
  // functional form so an in-flight lookup can never overwrite edits the user
  // made to other fields while awaiting the response.
  async function handleZipChange(value: string) {
    const zip = normalizeZipCode(value)
    onChange((prev) => ({ ...prev, zip_code: zip }))
    if (zip.length < ZIP_CODE_DIGITS) return

    latestZipRef.current = zip
    const found = await lookupZipCode(zip)
    if (latestZipRef.current !== zip) return
    setZipNotFound(!found)
    setZipFound(found ? zip : '')
    if (found) onChange((prev) => ({ ...prev, ...found }))
  }

  return (
    <div className="space-y-4">
      <h2 className="text-base font-semibold">{t('identity.stepAddressHeading')}</h2>

      {zipNotFound && (
        <Alert>
          <Info className="size-4" />
          <AlertDescription>{t('identity.zipNotFound')}</AlertDescription>
        </Alert>
      )}
      <p aria-live="polite" className="sr-only">
        {zipFound ? t('identity.zipFound', { zip: formatZipCodeInput(zipFound) }) : ''}
      </p>

      <div className="grid grid-cols-2 gap-3">
        <div className="space-y-1.5">
          <Label htmlFor="zip_code">{t('identity.zipCode')}</Label>
          <Input
            id="zip_code"
            required
            inputMode="numeric"
            placeholder="00000-000"
            value={formatZipCodeInput(address.zip_code)}
            onChange={(e) => void handleZipChange(e.target.value)}
            aria-invalid={!!errors?.zip_code}
            aria-describedby={errors?.zip_code ? 'zip_code-error' : undefined}
            className="min-h-11"
          />
          <FieldError id="zip_code-error" message={errors?.zip_code} />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="number">{t('identity.number')}</Label>
          <Input
            id="number"
            required
            value={address.number}
            onChange={(e) => onChange((prev) => ({ ...prev, number: e.target.value }))}
            aria-invalid={!!errors?.number}
            aria-describedby={errors?.number ? 'number-error' : undefined}
            className="min-h-11"
          />
          <FieldError id="number-error" message={errors?.number} />
        </div>
      </div>

      <div className="space-y-1.5">
        <Label htmlFor="street">{t('identity.street')}</Label>
        <Input
          id="street"
          required
          value={address.street}
          onChange={(e) => onChange((prev) => ({ ...prev, street: e.target.value }))}
          aria-invalid={!!errors?.street}
          aria-describedby={errors?.street ? 'street-error' : undefined}
          className="min-h-11"
        />
        <FieldError id="street-error" message={errors?.street} />
      </div>

      <div className="space-y-1.5">
        <Label htmlFor="complement">{t('identity.complement')}</Label>
        <Input
          id="complement"
          value={address.complement ?? ''}
          onChange={(e) => onChange((prev) => ({ ...prev, complement: e.target.value }))}
          className="min-h-11"
        />
      </div>

      <div className="space-y-1.5">
        <Label htmlFor="district">{t('identity.district')}</Label>
        <Input
          id="district"
          required
          value={address.district}
          onChange={(e) => onChange((prev) => ({ ...prev, district: e.target.value }))}
          aria-invalid={!!errors?.district}
          aria-describedby={errors?.district ? 'district-error' : undefined}
          className="min-h-11"
        />
        <FieldError id="district-error" message={errors?.district} />
      </div>

      <div className="grid grid-cols-[1fr_6rem] gap-3">
        <div className="space-y-1.5">
          <Label htmlFor="city">{t('identity.city')}</Label>
          <Input
            id="city"
            required
            value={address.city}
            onChange={(e) => onChange((prev) => ({ ...prev, city: e.target.value }))}
            aria-invalid={!!errors?.city}
            aria-describedby={errors?.city ? 'city-error' : undefined}
            className="min-h-11"
          />
          <FieldError id="city-error" message={errors?.city} />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="state">{t('identity.state')}</Label>
          <Select value={address.state} onValueChange={(value) => onChange((prev) => ({ ...prev, state: value as string }))}>
            <SelectTrigger
              id="state"
              className="w-full min-h-11"
              aria-invalid={!!errors?.state}
              aria-describedby={errors?.state ? 'state-error' : undefined}
            >
              <SelectValue placeholder={t('identity.selectState')} />
            </SelectTrigger>
            <SelectContent>
              {BRAZILIAN_STATES.map((uf) => (
                <SelectItem key={uf} value={uf}>
                  {uf}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <FieldError id="state-error" message={errors?.state} />
        </div>
      </div>
    </div>
  )
}

function IdentityForm({ status, docsComplete }: { status: KYCStatus; docsComplete: boolean }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [cpf, setCPF] = useState('')
  const [legalName, setLegalName] = useState(status.legal_name ?? '')
  const [birthDate, setBirthDate] = useState(status.birth_date ?? '')
  const [address, setAddress] = useState<Address>(status.address ?? EMPTY_ADDRESS)
  const [reviewing, setReviewing] = useState(false)
  const [cpfError, setCpfError] = useState(false)
  const [fieldErrors, setFieldErrors] = useState<Partial<Record<keyof Address | 'legal_name' | 'birth_date', string>>>({})

  const { mutateAsync: submit, isPending, error } = useMutation({
    mutationFn: submitKYCAPI,
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['kyc'] })
      toast.success(t('identity.submittedOn', { date: formatDate(new Date().toISOString()) }))
    },
    onError: () => setReviewing(false),
  })

  function handleReview(e: SyntheticEvent<HTMLFormElement>) {
    e.preventDefault()
    const cpfValid = isValidCPF(cpf.replace(/\D/g, ''))
    setCpfError(!cpfValid)

    const errors: Partial<Record<keyof Address | 'legal_name' | 'birth_date', string>> = {}
    if (legalName.trim().length < 3) errors.legal_name = t('identity.fieldRequired')
    if (!birthDate) errors.birth_date = t('identity.fieldRequired')
    else if (!isEligibleAge(birthDate, KYC_MIN_AGE_YEARS)) errors.birth_date = t('identity.underage')
    for (const field of REQUIRED_ADDRESS_FIELDS) {
      if (!address[field]?.trim()) errors[field] = t('identity.fieldRequired')
    }
    setFieldErrors(errors)

    if (cpfValid && Object.keys(errors).length === 0) setReviewing(true)
  }

  async function handleConfirm() {
    try {
      // useMutation's onError above already resets `reviewing` and the `error`
      // it returns already drives the Alert below — swallow here so the
      // rejection doesn't surface as an unhandled promise from ConfirmDialog's
      // onClick handler.
      await submit({
        cpf: cpf.replace(/\D/g, ''),
        legal_name: legalName,
        birth_date: birthDate,
        address: { ...address, zip_code: normalizeZipCode(address.zip_code) },
      })
    } catch {
      // handled via onError / the `error` state above
    }
  }

  const slugMessages: Record<string, string> = {
    'age-requirement-not-met': t('identity.underage'),
    'cpf-already-registered': t('identity.cpfTaken'),
    'kyc-already-verified': t('identity.alreadyVerified'),
    'kyc-submission-locked': t('identity.submissionLocked'),
    'validation-failed': t('identity.invalidData'),
  }
  // Falls back to a generic failure message, not `invalidData` — that copy is
  // reserved for the `validation-failed` slug and would misreport a network
  // or server error as a problem with what the user typed.
  const errorMsg = error ? (slugMessages[problemSlug(error)] ?? t('identity.submitFailed')) : null
  const maxBirthDate = new Date().toISOString().slice(0, 10)

  return (
    <form onSubmit={handleReview} className="space-y-4" noValidate>
      {errorMsg && (
        <Alert variant="destructive">
          <AlertDescription>{errorMsg}</AlertDescription>
        </Alert>
      )}

      {reviewing ? (
        <Alert>
          <AlertDescription>{t('identity.reviewNote')}</AlertDescription>
        </Alert>
      ) : (
        <Alert>
          <Info className="size-4" />
          <AlertDescription>{t('identity.detailsPrivacyNote')}</AlertDescription>
        </Alert>
      )}

      <h2 className="text-base font-semibold">{t('identity.stepDetails')}</h2>

      {reviewing ? (
        <ReviewSummary cpf={cpf} legalName={legalName} birthDate={birthDate} address={address} />
      ) : (
        <>
          <div className="space-y-1.5">
            <Label htmlFor="cpf">{t('identity.cpf')}</Label>
            <Input
              id="cpf"
              name="cpf"
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
              name="legal_name"
              required
              minLength={3}
              value={legalName}
              onChange={(e) => setLegalName(e.target.value)}
              aria-invalid={!!fieldErrors.legal_name}
              aria-describedby={fieldErrors.legal_name ? 'legal_name-error' : undefined}
              className="min-h-11"
            />
            <FieldError id="legal_name-error" message={fieldErrors.legal_name} />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="birth_date">{t('identity.birthDate')}</Label>
            <Input
              id="birth_date"
              name="birth_date"
              type="date"
              required
              max={maxBirthDate}
              value={birthDate}
              onChange={(e) => setBirthDate(e.target.value)}
              aria-invalid={!!fieldErrors.birth_date}
              aria-describedby={fieldErrors.birth_date ? 'birth_date-error' : undefined}
              className="min-h-11"
            />
            <FieldError id="birth_date-error" message={fieldErrors.birth_date} />
          </div>

          <Separator />

          <AddressFields address={address} onChange={setAddress} errors={fieldErrors} />
        </>
      )}

      {!docsComplete && <p className="text-muted-foreground text-sm">{t('identity.finishChecklistNote')}</p>}

      {reviewing ? (
        <div className="flex items-center gap-2">
          <Button type="button" variant="outline" className="min-h-11" onClick={() => setReviewing(false)} disabled={isPending}>
            {t('identity.editDetails')}
          </Button>
          <ConfirmDialog
            variant="default"
            trigger={
              <Button type="button" className="min-h-11" disabled={isPending || !docsComplete}>
                {isPending ? t('identity.submitting') : t('identity.confirmSubmit')}
              </Button>
            }
            title={t('identity.confirmSubmitTitle')}
            description={t('identity.confirmSubmitDescription')}
            confirmLabel={t('identity.confirmSubmit')}
            onConfirm={handleConfirm}
          />
        </div>
      ) : (
        <Button type="submit" className="min-h-11" disabled={!docsComplete}>
          {t('identity.reviewCta')}
        </Button>
      )}
    </form>
  )
}

function ReadOnlySummaryField({ id, label, value }: { id: string; label: string; value: string }) {
  return (
    <div className="space-y-1.5">
      <Label htmlFor={id}>{label}</Label>
      <Input id={id} value={value} readOnly className={cn('min-h-11', READ_ONLY_LOCK_CLASS)} />
    </div>
  )
}

function AddressSummary({ id, address }: { id: string; address: Address }) {
  const { t } = useTranslation()
  return (
    <div className="space-y-1.5">
      <span id={`${id}-label`} className="text-sm leading-none font-medium">{t('identity.address')}</span>
      <p
        aria-labelledby={`${id}-label`}
        className="bg-muted min-h-8 w-full rounded-lg border border-input px-2.5 py-1.5 text-sm text-balance md:text-pretty"
      >
        {formatAddress(address)}
      </p>
    </div>
  )
}

/**
 * Read-only view of a submission the user can no longer change: the masked CPF
 * is what tells them verification is already under way.
 */
function SubmittedDetails({ status }: { status: KYCStatus }) {
  const { t } = useTranslation()
  return (
    <div className="space-y-4">
      <ReadOnlySummaryField id="submitted-cpf" label={t('identity.cpf')} value={status.cpf_masked ?? ''} />
      <ReadOnlySummaryField id="submitted-legal-name" label={t('identity.legalName')} value={status.legal_name ?? ''} />
      <ReadOnlySummaryField id="submitted-birth-date" label={t('identity.birthDate')} value={status.birth_date ?? ''} />
      {status.address && <AddressSummary id="submitted-address" address={status.address} />}
    </div>
  )
}

/**
 * Confirmation summary shown in place of the editable form once the user
 * reaches the review step — reads like SubmittedDetails instead of a
 * grayed-out form, since this is the last moment before an irreversible submit.
 */
function ReviewSummary({
  cpf,
  legalName,
  birthDate,
  address,
}: {
  cpf: string
  legalName: string
  birthDate: string
  address: Address
}) {
  const { t } = useTranslation()
  return (
    <div className="space-y-4">
      <ReadOnlySummaryField id="review-cpf" label={t('identity.cpf')} value={cpf} />
      <ReadOnlySummaryField id="review-legal-name" label={t('identity.legalName')} value={legalName} />
      <ReadOnlySummaryField id="review-birth-date" label={t('identity.birthDate')} value={birthDate} />
      <AddressSummary id="review-address" address={address} />
    </div>
  )
}

function formatAddress(a: Address): string {
  const line = [a.street, a.number, a.complement].filter(Boolean).join(', ')
  return `${line} — ${a.district}, ${a.city}/${a.state} — ${formatZipCodeInput(a.zip_code)}`
}

const ID_DOC_TYPES: KYCDocumentType[] = ['id_front', 'id_back']
const SELFIE_TYPES: KYCDocumentType[] = ['selfie_up', 'selfie_down', 'selfie_left', 'selfie_right']

type KYCStep = 1 | 2 | 3

function StepNav({
  activeStep,
  maxReachable,
  onSelect,
}: {
  activeStep: KYCStep
  maxReachable: KYCStep
  onSelect: (step: KYCStep) => void
}) {
  const { t } = useTranslation()
  const steps: { step: KYCStep; labelKey: string; done: boolean }[] = [
    { step: 1, labelKey: 'identity.stepDocuments', done: maxReachable > 1 },
    { step: 2, labelKey: 'identity.stepSelfie', done: maxReachable > 2 },
    { step: 3, labelKey: 'identity.stepDetails', done: false },
  ]

  return (
    <nav aria-label={t('identity.stepNavLabel')}>
      <ol className="flex flex-wrap items-center gap-x-6 gap-y-2">
        {steps.map(({ step, labelKey, done }) => {
          const reachable = step <= maxReachable
          // `title` only reaches sighted mouse users — a keyboard/screen-reader
          // user gets nothing from it, so the lock reason also needs an
          // aria-describedby pointing at real (if visually hidden) text.
          const lockedId = `step-locked-${step}`
          return (
            <li key={step}>
              <button
                type="button"
                onClick={() => reachable && onSelect(step)}
                disabled={!reachable}
                title={!reachable ? t('identity.stepLocked') : undefined}
                aria-current={step === activeStep ? 'step' : undefined}
                aria-describedby={!reachable ? lockedId : undefined}
                className={`-m-2.5 flex min-h-11 items-center gap-1.5 p-2.5 text-sm ${
                  step === activeStep
                    ? 'text-foreground font-medium'
                    : reachable
                      ? 'text-muted-foreground hover:text-foreground cursor-pointer'
                      : 'text-muted-foreground/50 cursor-not-allowed'
                }`}
              >
                {!reachable && (
                  <span id={lockedId} className="sr-only">
                    {t('identity.stepLocked')}
                  </span>
                )}
                {done ? (
                  <CheckCircle2 className="text-primary size-4 shrink-0" />
                ) : (
                  <Circle className="size-4 shrink-0" />
                )}
                {t(labelKey)}
              </button>
            </li>
          )
        })}
      </ol>
    </nav>
  )
}

/**
 * Docs, selfie, and details used to render on one long scroll — the highest
 * cognitive-load finding from the design critique. Gate them behind a
 * 3-step flow instead, reusing MFARequired's precondition-gate pattern.
 * `manualStep` only overrides the data-driven default while it stays
 * reachable, so a background upload never yanks the user off a step they
 * navigated back to on purpose.
 */
function KYCStepFlow({
  status,
  uploadedTypes,
  docsComplete,
}: {
  status: KYCStatus
  uploadedTypes: KYCDocumentType[]
  docsComplete: boolean
}) {
  const { t } = useTranslation()
  const [manualStep, setManualStep] = useState<KYCStep | null>(null)

  const idDocsDone = ID_DOC_TYPES.every((type) => uploadedTypes.includes(type))
  const selfiesDone = SELFIE_TYPES.every((type) => uploadedTypes.includes(type))
  const maxReachable: KYCStep = !idDocsDone ? 1 : !selfiesDone ? 2 : 3
  const activeStep = manualStep && manualStep <= maxReachable ? manualStep : maxReachable

  // Screen-reader/keyboard users get no other cue that step-nav swapped the
  // panel's content; move focus to the new panel so it's announced.
  const panelRef = useRef<HTMLDivElement>(null)
  useEffect(() => {
    panelRef.current?.focus()
  }, [activeStep])

  return (
    <div className="space-y-4">
      {activeStep !== 3 && (
        <>
          <ProgressChecklist uploadedTypes={uploadedTypes} />
          <Separator />
        </>
      )}

      <StepNav activeStep={activeStep} maxReachable={maxReachable} onSelect={setManualStep} />

      <div ref={panelRef} tabIndex={-1} className="space-y-4 outline-none">
        {/* Hidden, not unmounted, once reachable: unmounting on every StepNav
            click would wipe an in-progress upload/recording preview, silently
            discarding it on an ordinary back-tap (same reasoning as step 3
            below). */}
        <div className={activeStep === 1 ? undefined : 'hidden'}>
          <div className="space-y-3">
            <h2 className="text-base font-semibold">{t('identity.stepDocuments')}</h2>
            <Alert>
              <Info className="size-4" />
              <AlertDescription>{t('identity.documentsPrivacyNote')}</AlertDescription>
            </Alert>
            {status.state !== 'rejected' && <DocumentList status={status} />}
            <KYCDocumentUpload uploadedTypes={uploadedTypes} />
          </div>
        </div>

        {maxReachable >= 2 && (
          <div className={activeStep === 2 ? undefined : 'hidden'}>
            <div className="space-y-3">
              <h2 className="text-base font-semibold">{t('identity.stepSelfie')}</h2>
              <SelfieCapture uploadedTypes={uploadedTypes} />
            </div>
          </div>
        )}

        {/* Hidden, not unmounted, once reachable: unmounting on every StepNav
            click would wipe IdentityForm's typed CPF/name/address/reviewing
            state, destroying the highest-effort input in the flow on an
            ordinary back-tap. */}
        {maxReachable === 3 && (
          <div className={activeStep === 3 ? undefined : 'hidden'}>
            <IdentityForm status={status} docsComplete={docsComplete} />
          </div>
        )}
      </div>
    </div>
  )
}

function DocumentList({ status }: { status: KYCStatus }) {
  const { t } = useTranslation()
  if (!status.documents?.length) return null

  return (
    <div className="space-y-1.5">
      <ul className="text-muted-foreground space-y-1 text-sm">
        {status.documents.map((doc) => (
          <li key={doc.id}>
            {t(DOCUMENT_LABEL_KEY[doc.type])} — {t('identity.uploadedOn', { date: formatDate(doc.uploaded_at) })}
          </li>
        ))}
      </ul>
    </div>
  )
}

/**
 * Every write route under /account/kyc sits behind step-up, which a user with
 * no enrolled MFA method can never satisfy. Say so up front instead of letting
 * them fill the whole form and hit a 403 they cannot clear.
 */
function MFARequired() {
  const { t } = useTranslation()
  return (
    <Alert>
      <Info className="size-4" />
      <AlertTitle>{t('identity.mfaRequiredTitle')}</AlertTitle>
      <AlertDescription className="space-y-3">
        <p>{t('identity.mfaRequired')}</p>
        <Button render={<Link href="/account/security" />} className="min-h-11">{t('identity.mfaRequiredCta')}</Button>
      </AlertDescription>
    </Alert>
  )
}

function cardDescriptionKey(state: KYCStatus['state']): string {
  switch (state) {
    case 'awaiting_files':
      return 'identity.awaitingFiles'
    case 'under_review':
      return 'identity.underReview'
    case 'rejected':
      return 'identity.rejected'
    case 'verified':
      return 'identity.levelVerified'
    default:
      return 'identity.notVerifiedCta'
  }
}

export default function IdentityPage() {
  const { t } = useTranslation()
  const { data: status, isError: kycError, error: kycErr, refetch: refetchKYC } = useQuery({ queryKey: ['kyc'], queryFn: fetchKYC })
  const { data: totp, isError: totpError, error: totpErr, refetch: refetchTOTP } = useQuery({ queryKey: ['totp'], queryFn: fetchTOTPStatus })
  const { data: passkeys, isError: passkeysError, error: passkeysErr, refetch: refetchPasskeys } = useQuery({ queryKey: ['passkeys'], queryFn: fetchPasskeys })

  if (kycError || totpError || passkeysError) {
    return (
      <QueryError
        error={kycErr ?? totpErr ?? passkeysErr}
        onRetry={() => { void refetchKYC(); void refetchTOTP(); void refetchPasskeys() }}
      />
    )
  }

  const mfaLoaded = totp !== undefined && passkeys !== undefined
  const hasMFA = (totp?.enabled ?? false) || (passkeys?.length ?? 0) > 0
  // ponytail: rejected submissions must redo every document per identity.rejected copy —
  // ignore stale `documents` client-side so the step flow resets to step 1. Real fix is
  // the backend clearing the document list on rejection; verify that contract too.
  const uploadedTypes = status?.state === 'rejected' ? [] : (status?.documents?.map((d) => d.type) ?? [])
  const docsComplete = REQUIRED_DOC_TYPES.every((docType) => uploadedTypes.includes(docType))

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold">{t('identity.title')}</h1>
          <p className="text-muted-foreground text-sm mt-1">{t('identity.subtitle')}</p>
        </div>
        {status && <StateBadge status={status} />}
      </div>

      <Card>
        <CardHeader>
          <CardDescription>{status ? t(cardDescriptionKey(status.state)) : null}</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          {status?.state === 'rejected' && (
            <Alert variant="destructive">
              <XCircle className="size-4" />
              <AlertDescription className="space-y-2">
                {status.rejection_reason && <p>{t('identity.rejectionReason', { reason: status.rejection_reason })}</p>}
                <p>{t('identity.rejectionGuidance')}</p>
                <a href={`mailto:${SUPPORT_EMAIL}`} className="underline underline-offset-4">
                  {t('identity.contactSupport')}
                </a>
              </AlertDescription>
            </Alert>
          )}

          {status?.state === 'verified' && (
            <>
              <Alert>
                <CheckCircle2 className="size-4" />
                <AlertDescription>
                  {t('identity.verifiedOn', { date: formatDate(status.verified_at ?? null) })} —{' '}
                  {t('identity.lockedNote')}
                </AlertDescription>
              </Alert>
              <SubmittedDetails status={status} />
            </>
          )}

          {status?.state === 'under_review' && (
            <>
              <SubmittedDetails status={status} />
              <DocumentList status={status} />
              <p className="text-muted-foreground text-sm">{t('identity.underReviewNote')}</p>
              {status.expires_at && (
                <p className="text-muted-foreground text-sm">
                  {t('identity.expiresOn', { date: formatDate(status.expires_at) })}
                </p>
              )}
            </>
          )}

          {status &&
            (status.state === 'not_started' || status.state === 'awaiting_files' || status.state === 'rejected') && (
              <>
                {mfaLoaded && !hasMFA ? (
                  <MFARequired />
                ) : (
                  <KYCStepFlow status={status} uploadedTypes={uploadedTypes} docsComplete={docsComplete} />
                )}
              </>
            )}
        </CardContent>
      </Card>
    </div>
  )
}
