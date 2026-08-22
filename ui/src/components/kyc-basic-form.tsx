'use client'

import { type ComponentProps, SyntheticEvent, useMemo, useRef, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { AsYouType, getCountries, getCountryCallingCode, isValidPhoneNumber, validatePhoneNumberLength, type CountryCode } from 'libphonenumber-js/min'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Combobox } from '@base-ui/react/combobox'
import { ChevronDown } from 'lucide-react'
import { submitBasicKYCAPI } from '@/lib/mutations'
import { isAxiosError } from '@/lib/axios'
import { CPF_DIGITS, KYC_ADDRESS_LIMITS, KYC_LEGAL_NAME_MAX_LENGTH, KYC_MIN_AGE_YEARS } from '@/lib/constants'
import type { KYCAddress, KYCStatus } from '@/lib/types'
import { lookupViaCEP } from '@/lib/viacep'
import { reportClientError } from '@/lib/client-logging'

function formatCPFInput(value: string): string {
  const digits = value.replace(/\D/g, '').slice(0, CPF_DIGITS)
  return digits
    .replace(/(\d{3})(\d)/, '$1.$2')
    .replace(/(\d{3})\.(\d{3})(\d)/, '$1.$2.$3')
    .replace(/(\d{3})\.(\d{3})\.(\d{3})(\d)/, '$1.$2.$3-$4')
}

type PhoneCountry = Exclude<CountryCode, 'AC'>

const DEFAULT_PHONE_COUNTRY: PhoneCountry = 'BR'
const PHONE_COUNTRIES: PhoneCountry[] = getCountries().filter((country): country is PhoneCountry => country !== 'AC')

function formatPhoneInput(value: string, country: PhoneCountry): string {
  let digits = value.replace(/\D/g, '')
  while (digits && validatePhoneNumberLength(digits, country) === 'TOO_LONG') {
    digits = digits.slice(0, -1)
  }
  return new AsYouType(country).input(digits)
}

function phoneToE164(value: string, country: PhoneCountry): string {
  return `+${getCountryCallingCode(country)}${value.replace(/\D/g, '')}`
}

function countryFlagPath(country: PhoneCountry): string {
  return `/country-flags/${country.toLowerCase()}.svg`
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
 * Basic KYC step: CPF/name/birthdate/phone/address. A successful submission
 * grants Basic KYC directly; the phone remains contact data, not an SMS proof.
 */
export function KYCBasicForm({ status }: { status: KYCStatus }) {
  const { t, i18n } = useTranslation()
  const language = i18n.resolvedLanguage ?? i18n.language
  const queryClient = useQueryClient()
  const [cpf, setCPF] = useState('')
  const [legalName, setLegalName] = useState(status.legal_name ?? '')
  const [birthDate, setBirthDate] = useState(status.birth_date ?? '')
  const [phoneCountry, setPhoneCountry] = useState<PhoneCountry>(DEFAULT_PHONE_COUNTRY)
  const [phone, setPhone] = useState('')
  const [address, setAddress] = useState<KYCAddress>({ zip_code: '', street: '', number: '', complement: '', district: '', city: '', state: '' })
  const [cpfError, setCpfError] = useState(false)
  const [fieldErrors, setFieldErrors] = useState<Partial<Record<'legal_name' | 'birth_date' | 'phone' | keyof KYCAddress, string>>>({})
  const [zipLookupStatus, setZipLookupStatus] = useState<'idle' | 'loading' | 'found' | 'not_found' | 'unavailable'>('idle')
  const zipLookupController = useRef<AbortController | null>(null)

  const { mutate: submit, isPending, error } = useMutation({
    mutationFn: submitBasicKYCAPI,
    onSuccess: (st) => queryClient.setQueryData(['kyc'], st),
  })

  function handleSubmit(e: SyntheticEvent<HTMLFormElement>) {
    e.preventDefault()
    const digits = cpf.replace(/\D/g, '')
    const cpfValid = isValidCPF(digits)
    setCpfError(!cpfValid)

    const errors: Partial<Record<'legal_name' | 'birth_date' | 'phone' | keyof KYCAddress, string>> = {}
    if (legalName.trim().length < 3) errors.legal_name = t('identity.fieldRequired')
    if (!birthDate) errors.birth_date = t('identity.fieldRequired')
    else if (!isEligibleAge(birthDate, KYC_MIN_AGE_YEARS)) errors.birth_date = t('identity.underage')
    if (!phone) errors.phone = t('identity.fieldRequired')
    else if (!isValidPhoneNumber(phone, phoneCountry)) errors.phone = t('identity.phoneInvalid')
    if (!/^\d{8}$/.test(address.zip_code)) errors.zip_code = t('identity.zipCodeInvalid')
    for (const field of ['street', 'number', 'district', 'city'] as const) {
      if (!address[field].trim()) errors[field] = t('identity.fieldRequired')
    }
    if (address.state.length !== KYC_ADDRESS_LIMITS.state) errors.state = t('identity.stateInvalid')
    setFieldErrors(errors)

    if (!cpfValid || Object.keys(errors).length > 0) return
    submit({ cpf: digits, legal_name: legalName, birth_date: birthDate, phone_number: phoneToE164(phone, phoneCountry), address })
  }

  const slugMessages: Record<string, string> = {
    'age-requirement-not-met': t('identity.underage'),
    'cpf-already-registered': t('identity.cpfTaken'),
    'kyc-already-verified': t('identity.alreadyVerified'),
    'validation-failed': t('identity.invalidData'),
  }
  const errorMsg = error ? (slugMessages[problemSlug(error)] ?? t('identity.submitFailed')) : null
  const countryNames = useMemo(() => {
    const names = new Intl.DisplayNames([language], { type: 'region' })
    return new Map(PHONE_COUNTRIES.map((country) => [country, names.of(country) ?? country]))
  }, [language])
  const sortedPhoneCountries = useMemo(
    () => [...PHONE_COUNTRIES].sort((a, b) => {
      if (a === DEFAULT_PHONE_COUNTRY) return -1
      if (b === DEFAULT_PHONE_COUNTRY) return 1
      return (countryNames.get(a) ?? a).localeCompare(countryNames.get(b) ?? b, language)
    }),
    [countryNames, language],
  )
  function updateAddress(field: keyof KYCAddress, value: string) {
    const cleaned = field === 'zip_code'
      ? value.replace(/\D/g, '').slice(0, KYC_ADDRESS_LIMITS.zipCode)
      : field === 'state'
        ? value.toUpperCase().replace(/[^A-Z]/g, '').slice(0, KYC_ADDRESS_LIMITS.state)
        : value.slice(0, KYC_ADDRESS_LIMITS[field])
    setAddress((current) => ({ ...current, [field]: cleaned }))
    setFieldErrors((errors) => ({ ...errors, [field]: undefined }))
  }
  async function lookupAddress(zipCode: string) {
    zipLookupController.current?.abort()
    if (zipCode.length !== KYC_ADDRESS_LIMITS.zipCode) {
      setZipLookupStatus('idle')
      return
    }

    const controller = new AbortController()
    zipLookupController.current = controller
    setZipLookupStatus('loading')
    try {
      const result = await lookupViaCEP(zipCode, controller.signal)
      if (controller.signal.aborted) return
      setZipLookupStatus(result.status)
      if (result.status !== 'found') return

      setAddress((current) => ({ ...current, ...result.address }))
      setFieldErrors((errors) => ({ ...errors, street: undefined, district: undefined, city: undefined, state: undefined }))
    } catch (error) {
      if (error instanceof DOMException && error.name === 'AbortError') return
      reportClientError('kyc-address', error)
      setZipLookupStatus('unavailable')
    }
  }
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
          autoComplete="off"
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
          maxLength={KYC_LEGAL_NAME_MAX_LENGTH}
          onChange={(e) => setLegalName(e.target.value.slice(0, KYC_LEGAL_NAME_MAX_LENGTH))}
          aria-invalid={!!fieldErrors.legal_name}
          autoComplete="name"
        />
        {fieldErrors.legal_name && <p className="text-destructive text-sm">{fieldErrors.legal_name}</p>}
      </div>

      <div className="space-y-1.5">
        <Label htmlFor="birth_date">{t('identity.birthDate')}</Label>
        <Input
          id="birth_date"
          type="date"
          required
          max={new Date().toISOString().slice(0, 10)}
          value={birthDate}
          onChange={(event) => setBirthDate(event.target.value)}
          aria-invalid={!!fieldErrors.birth_date}
          aria-describedby={fieldErrors.birth_date ? 'birth-date-error' : undefined}
          autoComplete="bday"
        />
        {fieldErrors.birth_date && <p id="birth-date-error" className="text-destructive text-sm">{fieldErrors.birth_date}</p>}
      </div>

      <div className="space-y-1.5">
        <div className="grid gap-3 sm:grid-cols-[minmax(13rem,0.8fr)_minmax(0,1.2fr)]">
          <div className="space-y-1.5">
            <Label htmlFor="phone-country">{t('identity.phoneCountry')}</Label>
            <Combobox.Root<PhoneCountry>
              items={sortedPhoneCountries}
              value={phoneCountry}
              onValueChange={(country) => {
                if (!country) return
                setPhoneCountry(country)
                setPhone(formatPhoneInput(phone, country))
              }}
              itemToStringLabel={(country) => `${countryNames.get(country)} +${getCountryCallingCode(country)}`}
              itemToStringValue={(country) => country}
              autoHighlight
              locale={language}
            >
              <div className="relative">
                {/* Static local SVGs are part of the selector affordance; next/image adds no benefit to this static export. */}
                {/* eslint-disable-next-line @next/next/no-img-element */}
                <img src={countryFlagPath(phoneCountry)} alt="" className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 rounded-sm" />
                <Combobox.Input
                  id="phone-country"
                  aria-label={t('identity.phoneCountry')}
                  className="h-8 w-full rounded-lg border border-input bg-transparent py-1 pr-9 pl-9 text-sm outline-none placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/70"
                  placeholder={t('identity.phoneCountrySearch')}
                />
                <Combobox.Icon render={<ChevronDown className="pointer-events-none absolute top-1/2 right-3 size-4 -translate-y-1/2 text-muted-foreground" />} />
              </div>
              <Combobox.Portal>
                <Combobox.Positioner side="bottom" align="start" sideOffset={4} className="z-50 w-(--anchor-width)">
                  <Combobox.Popup className="overflow-hidden rounded-lg bg-popover text-popover-foreground ring-1 ring-foreground/10 data-open:animate-in data-open:fade-in-0 data-open:zoom-in-95 data-closed:animate-out data-closed:fade-out-0 data-closed:zoom-out-95">
                    <Combobox.List className="max-h-72 overflow-y-auto p-1">
                      {(country: PhoneCountry) => (
                        <Combobox.Item key={country} value={country} className="flex cursor-default items-center gap-2 rounded-md px-2 py-2 text-sm outline-none data-highlighted:bg-accent data-highlighted:text-accent-foreground data-selected:font-medium">
                          {/* eslint-disable-next-line @next/next/no-img-element */}
                          <img src={countryFlagPath(country)} alt="" className="size-4 shrink-0 rounded-sm" />
                          <span className="min-w-0 flex-1 truncate">{countryNames.get(country)}</span>
                          <span className="shrink-0 text-muted-foreground">+{getCountryCallingCode(country)}</span>
                        </Combobox.Item>
                      )}
                    </Combobox.List>
                    <Combobox.Empty className="px-3 py-3 text-sm text-muted-foreground">{t('identity.phoneCountryEmpty')}</Combobox.Empty>
                  </Combobox.Popup>
                </Combobox.Positioner>
              </Combobox.Portal>
            </Combobox.Root>
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="phone">{t('identity.phoneNumber')}</Label>
            <Input
              id="phone"
              type="tel"
              required
              inputMode="tel"
              autoComplete="tel-national"
              placeholder={t('identity.phonePlaceholder')}
              value={phone}
              onChange={(e) => {
                setPhone(formatPhoneInput(e.target.value, phoneCountry))
                setFieldErrors((errors) => ({ ...errors, phone: undefined }))
              }}
              aria-invalid={!!fieldErrors.phone}
              aria-describedby={fieldErrors.phone ? 'phone-error' : undefined}
            />
          </div>
        </div>
        {fieldErrors.phone && <p id="phone-error" className="text-destructive text-sm">{fieldErrors.phone}</p>}
      </div>

      <fieldset className="space-y-3 rounded-xl p-4 ring-1 ring-foreground/10">
        <legend className="px-1 text-sm font-medium">{t('identity.address')}</legend>
        <p className="text-muted-foreground text-sm">{t('identity.addressHint')}</p>
        <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_minmax(8rem,0.45fr)]">
          <AddressField id="zip_code" label={t('identity.zipCode')} value={address.zip_code} onChange={(value) => { updateAddress('zip_code', value); void lookupAddress(value.replace(/\D/g, '').slice(0, KYC_ADDRESS_LIMITS.zipCode)) }} error={fieldErrors.zip_code} inputMode="numeric" autoComplete="postal-code" placeholder="00000-000" />
          <AddressField id="number" label={t('identity.addressNumber')} value={address.number} onChange={(value) => updateAddress('number', value)} error={fieldErrors.number} maxLength={KYC_ADDRESS_LIMITS.number} />
        </div>
        <p className="min-h-5 text-sm text-muted-foreground" aria-live="polite">
          {zipLookupStatus === 'loading' && t('identity.zipLookupLoading')}
          {zipLookupStatus === 'found' && t('identity.zipLookupFound')}
          {zipLookupStatus === 'not_found' && t('identity.zipLookupNotFound')}
          {zipLookupStatus === 'unavailable' && t('identity.zipLookupUnavailable')}
        </p>
        <AddressField id="street" label={t('identity.street')} value={address.street} onChange={(value) => updateAddress('street', value)} error={fieldErrors.street} maxLength={KYC_ADDRESS_LIMITS.street} />
        <AddressField id="complement" label={t('identity.complement')} value={address.complement ?? ''} onChange={(value) => updateAddress('complement', value)} error={fieldErrors.complement} maxLength={KYC_ADDRESS_LIMITS.complement} required={false} />
        <div className="grid gap-3 sm:grid-cols-2">
          <AddressField id="district" label={t('identity.district')} value={address.district} onChange={(value) => updateAddress('district', value)} error={fieldErrors.district} maxLength={KYC_ADDRESS_LIMITS.district} />
          <AddressField id="city" label={t('identity.city')} value={address.city} onChange={(value) => updateAddress('city', value)} error={fieldErrors.city} maxLength={KYC_ADDRESS_LIMITS.city} />
        </div>
        <AddressField id="state" label={t('identity.state')} value={address.state} onChange={(value) => updateAddress('state', value)} error={fieldErrors.state} maxLength={KYC_ADDRESS_LIMITS.state} autoComplete="address-level1" />
      </fieldset>

      <Button type="submit" disabled={isPending}>
        {isPending ? t('identity.submitting') : t('identity.submit')}
      </Button>
    </form>
  )
}

function AddressField({ id, label, value, onChange, error, required = true, ...inputProps }: { id: string; label: string; value: string; onChange: (value: string) => void; error?: string; required?: boolean } & Omit<ComponentProps<typeof Input>, 'id' | 'value' | 'onChange' | 'required'>) {
  return (
    <div className="space-y-1.5">
      <Label htmlFor={id}>{label}</Label>
      <Input id={id} value={value} onChange={(event) => onChange(event.target.value)} required={required} aria-invalid={!!error} aria-describedby={error ? `${id}-error` : undefined} {...inputProps} />
      {error && <p id={`${id}-error`} className="text-destructive text-sm">{error}</p>}
    </div>
  )
}
