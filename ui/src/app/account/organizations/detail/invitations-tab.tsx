'use client'

import { useState, type SyntheticEvent } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { Copy, MailPlus, Send } from 'lucide-react'
import { toast } from 'sonner'
import { fetchOrganizationInvitations } from '@/lib/queries'
import { inviteMemberAPI, revokeInvitationAPI } from '@/lib/mutations'
import { fetchCompanies } from '@/lib/queries'
import { formatDate } from '@/lib/format'
import { isAxiosError } from '@/lib/axios'
import { QueryError } from '@/components/query-error'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { ResponsiveDataList, type Column } from '@/components/responsive-data-list'
import { OrganizationRoleBadge } from '@/components/organization-role-badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Alert, AlertDescription } from '@/components/ui/alert'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import {
  assignableRoles,
  formatTaxID,
  type Organization,
  type OrganizationInvitation,
  type OrganizationRole,
} from '@/lib/types'

export function InvitationsTab({ organization }: { organization: Organization }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()

  const { data: invitations = [], isLoading, isError, error, refetch } = useQuery({
    queryKey: ['organization-invitations', organization.id],
    queryFn: () => fetchOrganizationInvitations(organization.id),
  })

  const revokeMutation = useMutation({
    mutationFn: (email: string) => revokeInvitationAPI(organization.id, email),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['organization-invitations', organization.id] })
      toast.success(t('toast.invitationRevoked'))
    },
    onError: (err) => {
      if (isAxiosError(err)) {
        toast.error(err.response?.data?.detail ?? t('toast.revokeInvitationFailed'))
      }
    },
  })

  if (isLoading) {
    return (
      <div className="space-y-3">
        {[...Array(2)].map((_, i) => (
          <div key={i} className="h-14 animate-pulse rounded-lg bg-muted" />
        ))}
      </div>
    )
  }

  if (isError) return <QueryError error={error} onRetry={() => refetch()} />

  const columns: Column<OrganizationInvitation>[] = [
    {
      key: 'email',
      header: t('organizations.invitations.email'),
      title: true,
      cell: (inv) => <span className="text-sm font-medium">{inv.email}</span>,
    },
    {
      key: 'role',
      header: t('organizations.role'),
      cell: (inv) => <OrganizationRoleBadge role={inv.role} />,
    },
    {
      key: 'expires',
      header: t('organizations.invitations.expires'),
      cell: (inv) => (
        <span className="text-sm text-muted-foreground">{formatDate(inv.expires_at)}</span>
      ),
    },
  ]

  return (
    <div className="space-y-4">
      <div className="flex justify-end">
        <InviteDialog organization={organization} />
      </div>
      <ResponsiveDataList
        rows={invitations}
        columns={columns}
        rowKey={(inv) => inv.email}
        actions={(inv) => (
          <ConfirmDialog
            trigger={
              <Button variant="ghost" size="sm" className="text-destructive">
                {t('organizations.invitations.revoke')}
              </Button>
            }
            title={t('organizations.invitations.revokeTitle')}
            description={t('organizations.invitations.revokeBody')}
            onConfirm={() => revokeMutation.mutateAsync(inv.email)}
          />
        )}
        empty={
          <div className="py-12 text-center text-muted-foreground">
            <Send className="mx-auto mb-2 size-8 opacity-40" />
            <p className="text-sm">{t('organizations.invitations.empty')}</p>
          </div>
        }
      />
    </div>
  )
}

function InviteDialog({ organization }: { organization: Organization }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [open, setOpen] = useState(false)
  const [role, setRole] = useState<OrganizationRole>('member')
  // Inviting is granting, so the list stops below the caller's own rank —
  // exactly what SetRole offers, and exactly what the server accepts.
  const options = assignableRoles(organization.role)
  // Held in state, never re-fetchable: the server returns the token once and
  // stores only its hash.
  const [link, setLink] = useState<string | null>(null)
  // Which companies this invitation also grants reach to. Empty is valid and
  // common — a bookkeeper who only reads invoices — so the form never demands
  // one; it says what empty means instead.
  const [companyIDs, setCompanyIDs] = useState<string[]>([])
  const { data: companies = [] } = useQuery({
    queryKey: ['companies', organization.id],
    queryFn: () => fetchCompanies(organization.id),
  })

  const { mutate, isPending, error, reset } = useMutation({
    mutationFn: ({ email, role }: { email: string; role: OrganizationRole }) =>
      inviteMemberAPI(organization.id, email, role, companyIDs),
    onSuccess: (data) => {
      // The full URL, not the bare token: a token alone is something the
      // recipient cannot act on.
      setLink(`${window.location.origin}/invite?token=${encodeURIComponent(data.token)}`)
      queryClient.invalidateQueries({ queryKey: ['organization-invitations', organization.id] })
    },
    onError: (err) => {
      if (isAxiosError(err)) toast.error(err.response?.data?.detail ?? t('toast.inviteFailed'))
    },
  })

  const errorMsg = isAxiosError(error)
    ? (error.response?.data?.detail ?? t('toast.inviteFailed'))
    : null

  function handleSubmit(e: SyntheticEvent<HTMLFormElement>) {
    e.preventDefault()
    const fd = new FormData(e.currentTarget)
    mutate({ email: (fd.get('email') as string).trim(), role })
  }

  function handleOpenChange(next: boolean) {
    setOpen(next)
    if (!next) {
      setLink(null)
      setRole('member')
      setCompanyIDs([])
      reset()
    }
  }

  async function handleCopy() {
    if (!link) return
    await navigator.clipboard.writeText(link)
    toast.success(t('toast.inviteLinkCopied'))
  }

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogTrigger
        render={
          <Button size="sm">
            <MailPlus className="size-4" />
            {t('organizations.invitations.invite')}
          </Button>
        }
      />
      <DialogContent>
        {link ? (
          <div className="space-y-4">
            <DialogHeader>
              <DialogTitle>{t('organizations.invitations.linkTitle')}</DialogTitle>
              <DialogDescription>
                {t('organizations.invitations.inviteDescription')}
              </DialogDescription>
            </DialogHeader>

            {/* Said before the dialog can be closed, not after. */}
            <Alert>
              <AlertDescription>{t('organizations.invitations.linkWarning')}</AlertDescription>
            </Alert>

            <div className="flex items-center gap-2">
              <Input readOnly value={link} className="font-mono text-xs" onFocus={(e) => e.currentTarget.select()} />
              <Button type="button" variant="outline" size="sm" onClick={handleCopy}>
                <Copy className="size-4" />
                {t('organizations.invitations.copy')}
              </Button>
            </div>

            <DialogFooter>
              <Button type="button" onClick={() => handleOpenChange(false)}>
                {t('organizations.invitations.done')}
              </Button>
            </DialogFooter>
          </div>
        ) : (
          <form onSubmit={handleSubmit} className="space-y-4">
            <DialogHeader>
              <DialogTitle>{t('organizations.invitations.inviteTitle')}</DialogTitle>
              <DialogDescription>
                {t('organizations.invitations.inviteDescription')}
              </DialogDescription>
            </DialogHeader>

            {errorMsg && (
              <Alert variant="destructive">
                <AlertDescription>{errorMsg}</AlertDescription>
              </Alert>
            )}

            <div className="space-y-1.5">
              <Label htmlFor="email">{t('organizations.invitations.emailLabel')}</Label>
              <Input id="email" name="email" type="email" required autoComplete="email" />
            </div>

            <div className="space-y-1.5">
              <Label htmlFor="invite-role">{t('organizations.invitations.roleLabel')}</Label>
              {/* Owner is absent, and so is the caller's own rank: the API
                  rejects both, and offering a choice that always fails is
                  asking a question with a wrong answer on it. */}
              <Select value={role} onValueChange={(v) => setRole(v as OrganizationRole)}>
                <SelectTrigger id="invite-role">
                  <SelectValue>
                    {role ? t(`organizations.roles.${role}`) : role}
                  </SelectValue>
                </SelectTrigger>
                <SelectContent>
                  {options.map((r) => (
                    <SelectItem key={r} value={r}>
                      {t(`organizations.roles.${r}`)}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>

            {companies.length > 0 && (
              <div className="space-y-1.5">
                <Label>{t('organizations.invitations.companiesLabel')}</Label>
                {/* Checkboxes, not a Select: choosing several is the case this
                    exists for — an accountant picking five of forty — and a
                    multi-select hides how many are ticked behind a summary. */}
                <div className="max-h-44 space-y-1 overflow-y-auto rounded-md border p-2">
                  {companies.map((c) => (
                    <label key={c.id} className="flex items-center gap-2 rounded px-1.5 py-1 text-sm hover:bg-muted">
                      <input
                        type="checkbox"
                        className="size-4 accent-primary"
                        checked={companyIDs.includes(c.id)}
                        onChange={(e) =>
                          setCompanyIDs((prev) =>
                            e.target.checked ? [...prev, c.id] : prev.filter((id) => id !== c.id),
                          )
                        }
                      />
                      <span className="min-w-0 truncate">{c.legal_name}</span>
                      <code className="ml-auto shrink-0 font-mono text-xs text-muted-foreground">
                        {formatTaxID(c.tax_id, c.tax_id_kind)}
                      </code>
                    </label>
                  ))}
                </div>
                {/* Said out loud rather than left implied: somebody invited
                    with no company joins and can act for nothing, and silence
                    there is a person who cannot work with nothing on screen
                    explaining why. */}
                {companyIDs.length === 0 && (
                  <p className="text-xs text-muted-foreground">
                    {t('organizations.invitations.noCompaniesHint')}
                  </p>
                )}
              </div>
            )}

            <DialogFooter>
              <Button type="submit" disabled={isPending}>
                {isPending
                  ? t('organizations.invitations.submitting')
                  : t('organizations.invitations.submit')}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  )
}
