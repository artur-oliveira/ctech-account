'use client'

import { useState, type SyntheticEvent } from 'react'
import { useRouter } from 'next/navigation'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { fetchOrganizationMembers, fetchProfile } from '@/lib/queries'
import { removeMemberAPI, renameOrganizationAPI, transferOwnershipAPI } from '@/lib/mutations'
import { isAxiosError } from '@/lib/axios'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import type { Organization } from '@/lib/types'

export function SettingsTab({ organization }: { organization: Organization }) {
  const { t } = useTranslation()
  const isOwner = organization.role === 'owner'
  const canManage = isOwner || organization.role === 'admin'

  return (
    <div className="space-y-8">
      {canManage && <RenameSection organization={organization} />}

      <section className="space-y-4">
        <h2 className="text-base font-medium">{t('organizations.settings.dangerTitle')}</h2>
        {isOwner ? (
          <>
            <TransferSection organization={organization} />
            {/* Not a disabled "leave" button: a dead control with no reason
                given is a question the interface refuses to answer. */}
            <p className="max-w-prose text-sm text-muted-foreground">
              {t('organizations.settings.ownerCannotLeave')}
            </p>
          </>
        ) : (
          <LeaveSection organization={organization} />
        )}
      </section>
    </div>
  )
}

function RenameSection({ organization }: { organization: Organization }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()

  const { mutate, isPending, error } = useMutation({
    mutationFn: (displayName: string) => renameOrganizationAPI(organization.id, displayName),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['organization', organization.id] })
      queryClient.invalidateQueries({ queryKey: ['organizations'] })
      toast.success(t('toast.organizationRenamed'))
    },
    onError: (err) => {
      if (isAxiosError(err)) {
        toast.error(err.response?.data?.detail ?? t('toast.renameOrganizationFailed'))
      }
    },
  })

  const errorMsg = isAxiosError(error)
    ? (error.response?.data?.detail ?? t('toast.renameOrganizationFailed'))
    : null

  function handleSubmit(e: SyntheticEvent<HTMLFormElement>) {
    e.preventDefault()
    const fd = new FormData(e.currentTarget)
    mutate((fd.get('display_name') as string).trim())
  }

  return (
    <section className="space-y-4">
      <div>
        <h2 className="text-base font-medium">{t('organizations.settings.nameTitle')}</h2>
        <p className="mt-1 text-sm text-muted-foreground">
          {t('organizations.settings.nameDescription')}
        </p>
      </div>

      {errorMsg && (
        <Alert variant="destructive">
          <AlertDescription>{errorMsg}</AlertDescription>
        </Alert>
      )}

      {/* Keyed on the id so defaultValue resets once the query resolves — the
          same guard the profile form uses. */}
      <form key={organization.id} onSubmit={handleSubmit} className="flex max-w-md items-end gap-2">
        <div className="flex-1 space-y-1.5">
          <Label htmlFor="display_name">{t('organizations.create.nameLabel')}</Label>
          <Input
            id="display_name"
            name="display_name"
            required
            maxLength={120}
            defaultValue={organization.display_name}
          />
        </div>
        <Button type="submit" disabled={isPending}>
          {isPending ? t('organizations.settings.saving') : t('organizations.settings.save')}
        </Button>
      </form>
    </section>
  )
}

function TransferSection({ organization }: { organization: Organization }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [target, setTarget] = useState('')

  const { data: members = [] } = useQuery({
    queryKey: ['organization-members', organization.id],
    queryFn: () => fetchOrganizationMembers(organization.id),
  })

  // Never a free-text user id: the API requires an existing membership, and
  // typing an id is how an organization gets handed to a stranger.
  const candidates = members.filter((m) => m.role !== 'owner')

  const { mutate, isPending } = useMutation({
    mutationFn: (userId: string) => transferOwnershipAPI(organization.id, userId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['organization', organization.id] })
      queryClient.invalidateQueries({ queryKey: ['organization-members', organization.id] })
      queryClient.invalidateQueries({ queryKey: ['organizations'] })
      setTarget('')
      toast.success(t('toast.ownershipTransferred'))
    },
    onError: (err) => {
      if (isAxiosError(err)) toast.error(err.response?.data?.detail ?? t('toast.transferFailed'))
    },
  })

  return (
    <div className="rounded-xl border border-destructive/30 bg-destructive/5 p-4">
      <h3 className="text-sm font-medium">{t('organizations.settings.transfer')}</h3>
      <p className="mt-1 max-w-prose text-sm text-muted-foreground">
        {t('organizations.settings.transferDescription')}
      </p>

      {candidates.length === 0 ? (
        <p className="mt-4 text-sm text-muted-foreground">
          {t('organizations.settings.noOtherMembers')}
        </p>
      ) : (
        <div className="mt-4 flex flex-wrap items-end gap-2">
          <div className="space-y-1.5">
            <Label htmlFor="transfer-target">{t('organizations.settings.transferSelect')}</Label>
            <Select value={target} onValueChange={(v) => setTarget(v ?? '')}>
              <SelectTrigger id="transfer-target" className="w-72">
                <SelectValue placeholder={t('organizations.settings.transferSelect')} />
              </SelectTrigger>
              <SelectContent>
                {candidates.map((m) => (
                  <SelectItem key={m.user_id} value={m.user_id}>
                    {m.user_id}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <ConfirmDialog
            trigger={
              <Button variant="destructive" size="sm" disabled={!target || isPending}>
                {t('organizations.settings.transfer')}
              </Button>
            }
            title={t('organizations.settings.transferConfirmTitle', { name: target })}
            description={t('organizations.settings.transferConfirmBody')}
            onConfirm={() => mutate(target)}
          />
        </div>
      )}
    </div>
  )
}

function LeaveSection({ organization }: { organization: Organization }) {
  const { t } = useTranslation()
  const router = useRouter()
  const queryClient = useQueryClient()
  const { data: profile } = useQuery({ queryKey: ['profile'], queryFn: fetchProfile })

  const { mutate } = useMutation({
    mutationFn: (userId: string) => removeMemberAPI(organization.id, userId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['organizations'] })
      router.push('/account/organizations')
    },
    onError: (err) => {
      if (isAxiosError(err)) toast.error(err.response?.data?.detail ?? t('toast.leaveFailed'))
    },
  })

  return (
    <div className="rounded-xl border border-destructive/30 bg-destructive/5 p-4">
      <h3 className="text-sm font-medium">{t('organizations.settings.leave')}</h3>
      <p className="mt-1 max-w-prose text-sm text-muted-foreground">
        {t('organizations.settings.leaveDescription')}
      </p>
      <div className="mt-4">
        <ConfirmDialog
          trigger={
            <Button variant="destructive" size="sm">
              {t('organizations.settings.leave')}
            </Button>
          }
          title={t('organizations.settings.leaveConfirmTitle')}
          description={t('organizations.settings.leaveConfirmBody')}
          onConfirm={() => profile && mutate(profile.user_id)}
        />
      </div>
    </div>
  )
}
