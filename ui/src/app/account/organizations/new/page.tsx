'use client'

import {Suspense, useState, type SyntheticEvent} from 'react'
import Link from 'next/link'
import {useRouter, useSearchParams} from 'next/navigation'
import {useMutation, useQuery, useQueryClient} from '@tanstack/react-query'
import {useTranslation} from 'react-i18next'
import {Building2} from 'lucide-react'
import {fetchHandoff, lookupTaxID} from '@/lib/queries'
import {createOrganizationAPI, registerCompanyAPI} from '@/lib/mutations'
import {isAxiosError} from '@/lib/axios'
import {cn} from '@/lib/utils'
import {Button, buttonVariants} from '@/components/ui/button'
import {Input} from '@/components/ui/input'
import {Label} from '@/components/ui/label'
import {Alert, AlertDescription} from '@/components/ui/alert'

/**
 * Creating an organization, with an optional return trip.
 *
 * A route of its own rather than the dialog on /account/organizations, because
 * a handoff has to carry `return_to` somewhere and a dialog has no URL. Without
 * handoff parameters this is simply the create screen, which is what makes it
 * safe to link to directly.
 */
export default function NewOrganizationPage() {
  return (
    <Suspense fallback={<div className="h-64 animate-pulse rounded-xl bg-muted"/>}>
      <NewOrganization/>
    </Suspense>
  )
}

function NewOrganization() {
  const {t} = useTranslation()
  const router = useRouter()
  const queryClient = useQueryClient()
  const params = useSearchParams()
  const clientID = params.get('client_id') ?? ''
  const rawReturnTo = params.get('return_to') ?? ''
  const state = params.get('state') ?? ''
  const isHandoff = !!clientID && !!rawReturnTo

  const [displayName, setDisplayName] = useState('')
  const [taxID, setTaxID] = useState('')
  const [legalName, setLegalName] = useState('')
  const [tradeName, setTradeName] = useState('')

  // The server decides whether this handoff is legitimate and what the product
  // is called. A banner reading client_name off the query string is a banner
  // anybody can make say whatever they like.
  const {data: handoff, isLoading, isError} = useQuery({
    queryKey: ['handoff', clientID, rawReturnTo],
    queryFn: () => fetchHandoff(clientID, rawReturnTo, state),
    enabled: isHandoff,
    retry: false,
  })

  /**
   * Leaves for the product that sent us, through the URL the *server* echoed
   * back — never the raw query parameter. Whatever was validated is what the
   * browser follows, or the two can differ.
   */
  function leave(result: {organization_id: string; company_id: string} | 'cancelled') {
    if (!handoff) return
    const url = new URL(handoff.return_to)
    if (result === 'cancelled') {
      url.searchParams.set('cancelled', '1')
    } else {
      url.searchParams.set('organization_id', result.organization_id)
      url.searchParams.set('company_id', result.company_id)
    }
    if (state) url.searchParams.set('state', state)
    window.location.assign(url.toString())
  }

  const {mutate, isPending, error} = useMutation({
    mutationFn: async () => {
      const org = await createOrganizationAPI(displayName.trim())
      // The company second, and only when one was typed: a handoff always
      // collects it, and a direct visit does not have to.
      if (!taxID.trim()) return {organizationID: org.id, companyID: ''}
      const company = await registerCompanyAPI(org.id, {
        tax_id: taxID,
        legal_name: legalName.trim(),
        trade_name: tradeName.trim(),
      })
      return {organizationID: org.id, companyID: company.id}
    },
    onSuccess: ({organizationID, companyID}) => {
      void queryClient.invalidateQueries({queryKey: ['organizations']})
      if (isHandoff && handoff) {
        leave({organization_id: organizationID, company_id: companyID})
        return
      }
      router.push(`/account/organizations/detail?id=${encodeURIComponent(organizationID)}`)
    },
  })

  // A miss changes nothing and says nothing: a register not knowing a CNPJ says
  // nothing about whether it is real.
  async function fillFromRegister() {
    if (!taxID.trim() || legalName.trim()) return
    const names = await lookupTaxID(taxID)
    if (!names) return
    setLegalName(names.legal_name)
    if (names.trade_name) setTradeName(names.trade_name)
  }

  const errorMsg = isAxiosError(error)
    ? (error.response?.data?.detail ?? t('organizations.new.failed'))
    : null

  if (isHandoff && isLoading) {
    return <div className="h-64 animate-pulse rounded-xl bg-muted"/>
  }

  // A misconfigured integration strands nobody: the person is told, and given
  // the way to do this from their own account instead.
  if (isHandoff && (isError || !handoff)) {
    return (
      <div className="mx-auto max-w-md space-y-4 py-8 text-center">
        <Building2 className="mx-auto size-8 text-muted-foreground opacity-40"/>
        <h1 className="text-lg font-semibold">{t('organizations.new.handoffInvalidTitle')}</h1>
        <p className="text-sm text-muted-foreground">{t('organizations.new.handoffInvalidBody')}</p>
        <Link href="/account/organizations" className={cn(buttonVariants({variant: 'outline'}))}>
          {t('organizations.new.goToOrganizations')}
        </Link>
      </div>
    )
  }

  function handleSubmit(e: SyntheticEvent<HTMLFormElement>) {
    e.preventDefault()
    mutate()
  }

  return (
    <div className="mx-auto max-w-md space-y-6 py-4">
      <div className="space-y-1">
        <h1 className="text-xl font-semibold tracking-tight">{t('organizations.new.title')}</h1>
        <p className="text-sm text-muted-foreground">{t('organizations.new.body')}</p>
      </div>

      {/* Somebody who tapped a button in another product and landed on a
          different domain needs to be told why, or it reads as a bug — or a
          phish. */}
      {handoff && (
        <Alert>
          <AlertDescription>
            {t('organizations.new.handoffBanner', {product: handoff.client_name})}
          </AlertDescription>
        </Alert>
      )}

      <form onSubmit={handleSubmit} className="space-y-4">
        {errorMsg && (
          <Alert variant="destructive">
            <AlertDescription>{errorMsg}</AlertDescription>
          </Alert>
        )}

        <div className="space-y-2">
          <Label htmlFor="display-name">{t('organizations.new.name')}</Label>
          <Input
            id="display-name"
            value={displayName}
            onChange={(e) => setDisplayName(e.target.value)}
            required
            maxLength={120}
            autoFocus
          />
        </div>

        <div className="space-y-2">
          <Label htmlFor="new-tax-id">
            {isHandoff ? t('organizations.companies.taxId') : t('organizations.new.taxIdOptional')}
          </Label>
          <Input
            id="new-tax-id"
            value={taxID}
            onChange={(e) => setTaxID(e.target.value)}
            onBlur={() => void fillFromRegister()}
            required={isHandoff}
            maxLength={32}
            autoComplete="off"
          />
        </div>

        {!!taxID.trim() && (
          <>
            <div className="space-y-2">
              <Label htmlFor="new-legal-name">{t('organizations.companies.legalName')}</Label>
              <Input
                id="new-legal-name"
                value={legalName}
                onChange={(e) => setLegalName(e.target.value)}
                required
                maxLength={200}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="new-trade-name">{t('organizations.companies.tradeNameOptional')}</Label>
              <Input
                id="new-trade-name"
                value={tradeName}
                onChange={(e) => setTradeName(e.target.value)}
                maxLength={200}
              />
            </div>
          </>
        )}

        <div className="flex items-center gap-2 pt-2">
          <Button type="submit" disabled={isPending}>
            {isPending ? t('common.saving') : t('organizations.new.submit')}
          </Button>
          {/* A real action, not a back button: the product that sent them has
              to be told, or it cannot put the person back where they were. */}
          {isHandoff ? (
            <Button type="button" variant="ghost" onClick={() => leave('cancelled')}>
              {t('common.cancel')}
            </Button>
          ) : (
            <Link href="/account/organizations" className={cn(buttonVariants({variant: 'ghost'}))}>
              {t('common.cancel')}
            </Link>
          )}
        </div>
      </form>
    </div>
  )
}
