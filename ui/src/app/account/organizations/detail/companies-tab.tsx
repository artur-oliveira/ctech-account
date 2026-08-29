'use client'

import {useState, type SyntheticEvent} from 'react'
import {useMutation, useQuery, useQueryClient} from '@tanstack/react-query'
import {useTranslation} from 'react-i18next'
import {Building2, Plus} from 'lucide-react'
import {toast} from 'sonner'
import {fetchCompanies, lookupTaxID} from '@/lib/queries'
import {registerCompanyAPI} from '@/lib/mutations'
import {formatDate} from '@/lib/format'
import {isAxiosError} from '@/lib/axios'
import {QueryError} from '@/components/query-error'
import {type Column, ResponsiveDataList} from '@/components/responsive-data-list'
import {Button} from '@/components/ui/button'
import {Input} from '@/components/ui/input'
import {Label} from '@/components/ui/label'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog'
import {Alert, AlertDescription} from '@/components/ui/alert'
import {assignableRoles, type Company, formatTaxID, type Organization} from '@/lib/types'

export function CompaniesTab({organization}: { organization: Organization }) {
  const {t} = useTranslation()
  // The same predicate the members tab uses for "may manage": admin or owner.
  // Reused rather than re-derived, so the two screens cannot disagree.
  const mayManage = assignableRoles(organization.role).length > 0

  const {data: companies = [], isLoading, isError, error, refetch} = useQuery({
    queryKey: ['companies', organization.id],
    queryFn: () => fetchCompanies(organization.id),
  })

  if (isLoading) {
    return (
      <div className="space-y-3">
        {[...Array(3)].map((_, i) => (
          <div key={i} className="h-14 animate-pulse rounded-lg bg-muted"/>
        ))}
      </div>
    )
  }

  if (isError) return <QueryError error={error} onRetry={() => refetch()}/>

  const columns: Column<Company>[] = [
    {
      key: 'name',
      header: t('organizations.companies.company'),
      title: true,
      // The legal name is what a person recognizes; the tax id is what
      // identifies it. Both, with the name leading — the same hierarchy the
      // members tab gives a person's name over their id.
      cell: (c) => (
        <div className="min-w-0">
          <span className="block truncate text-sm font-medium">{c.legal_name}</span>
          <code className="block truncate font-mono text-xs text-muted-foreground">
            {formatTaxID(c.tax_id, c.tax_id_kind)}
          </code>
        </div>
      ),
    },
    {
      key: 'trade',
      header: t('organizations.companies.tradeName'),
      cell: (c) => <span className="text-sm text-muted-foreground">{c.trade_name || '—'}</span>,
    },
    {
      key: 'added',
      header: t('organizations.companies.added'),
      cell: (c) => <span className="text-sm text-muted-foreground">{formatDate(c.created_at)}</span>,
    },
  ]

  return (
    <div className="space-y-4">
      {mayManage && (
        <div className="flex justify-end">
          <RegisterCompanyDialog organizationID={organization.id}/>
        </div>
      )}
      <ResponsiveDataList
        rows={companies}
        columns={columns}
        rowKey={(c) => c.id}
        empty={
          <div className="py-12 text-center text-muted-foreground">
            <Building2 className="mx-auto mb-2 size-8 opacity-40"/>
            <p className="text-sm">{t('organizations.companies.empty')}</p>
          </div>
        }
      />
    </div>
  )
}

function RegisterCompanyDialog({organizationID}: { organizationID: string }) {
  const {t} = useTranslation()
  const queryClient = useQueryClient()
  const [open, setOpen] = useState(false)
  const [taxID, setTaxID] = useState('')
  const [legalName, setLegalName] = useState('')
  const [tradeName, setTradeName] = useState('')

  const {mutate, isPending, error, reset} = useMutation({
    mutationFn: () =>
      registerCompanyAPI(organizationID, {
        // Sent as typed. The server canonicalizes — a mask stripped here and
        // there is two implementations of one rule, and they drift.
        tax_id: taxID,
        legal_name: legalName.trim(),
        trade_name: tradeName.trim(),
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({queryKey: ['companies', organizationID]})
      handleOpenChange(false)
      toast.success(t('toast.companyAdded'))
    },
  })

  const errorMsg = isAxiosError(error)
    ? (error.response?.data?.detail ?? t('toast.addCompanyFailed'))
    : null

  // Fired on blur, not on every keystroke: one request per completed field
  // instead of one per character, and nothing at all while somebody is still
  // typing. A miss changes nothing and says nothing — the register not knowing
  // a CNPJ says nothing about whether it is real.
  async function fillFromRegister() {
    if (!taxID.trim() || legalName.trim()) return
    const names = await lookupTaxID(organizationID, taxID)
    if (!names) return
    setLegalName(names.legal_name)
    if (names.trade_name) setTradeName(names.trade_name)
  }

  function handleOpenChange(next: boolean) {
    setOpen(next)
    if (!next) {
      setTaxID('')
      setLegalName('')
      setTradeName('')
      reset()
    }
  }

  function handleSubmit(e: SyntheticEvent<HTMLFormElement>) {
    e.preventDefault()
    mutate()
  }

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogTrigger
        render={
          <Button size="sm">
            <Plus className="size-4"/>
            {t('organizations.companies.add')}
          </Button>
        }
      />
      <DialogContent>
        <form onSubmit={handleSubmit}>
          <DialogHeader>
            <DialogTitle>{t('organizations.companies.addTitle')}</DialogTitle>
            <DialogDescription>{t('organizations.companies.addBody')}</DialogDescription>
          </DialogHeader>
          <div className="space-y-4 py-4">
            {errorMsg && (
              <Alert variant="destructive">
                <AlertDescription>{errorMsg}</AlertDescription>
              </Alert>
            )}
            <div className="space-y-2">
              <Label htmlFor="company-tax-id">{t('organizations.companies.taxId')}</Label>
              <Input
                id="company-tax-id"
                name="tax_id"
                value={taxID}
                onChange={(e) => setTaxID(e.target.value)}
                onBlur={() => void fillFromRegister()}
                required
                maxLength={32}
                autoComplete="off"
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="company-legal-name">{t('organizations.companies.legalName')}</Label>
              <Input
                id="company-legal-name"
                name="legal_name"
                value={legalName}
                onChange={(e) => setLegalName(e.target.value)}
                required
                maxLength={200}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="company-trade-name">{t('organizations.companies.tradeNameOptional')}</Label>
              <Input
                id="company-trade-name"
                name="trade_name"
                value={tradeName}
                onChange={(e) => setTradeName(e.target.value)}
                maxLength={200}
              />
            </div>
          </div>
          <DialogFooter>
            <Button type="submit" disabled={isPending}>
              {isPending ? t('common.saving') : t('organizations.companies.add')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
