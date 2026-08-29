'use client'

import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { Users } from 'lucide-react'
import { toast } from 'sonner'
import { fetchOrganizationMembers } from '@/lib/queries'
import { removeMemberAPI, setMemberRoleAPI } from '@/lib/mutations'
import { formatDate } from '@/lib/format'
import { isAxiosError } from '@/lib/axios'
import { QueryError } from '@/components/query-error'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { ResponsiveDataList, type Column } from '@/components/responsive-data-list'
import { OrganizationRoleBadge } from '@/components/organization-role-badge'
import { Button } from '@/components/ui/button'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { GRANTABLE_ROLES, type Organization, type OrganizationMember, type OrganizationRole } from '@/lib/types'

export function MembersTab({ organization }: { organization: Organization }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const canManage = organization.role === 'admin' || organization.role === 'owner'

  const { data: members = [], isLoading, isError, error, refetch } = useQuery({
    queryKey: ['organization-members', organization.id],
    queryFn: () => fetchOrganizationMembers(organization.id),
  })

  function invalidate() {
    queryClient.invalidateQueries({ queryKey: ['organization-members', organization.id] })
    queryClient.invalidateQueries({ queryKey: ['organization', organization.id] })
    queryClient.invalidateQueries({ queryKey: ['organizations'] })
  }

  const roleMutation = useMutation({
    mutationFn: ({ userId, role }: { userId: string; role: OrganizationRole }) =>
      setMemberRoleAPI(organization.id, userId, role),
    onSuccess: () => {
      invalidate()
      toast.success(t('toast.roleChanged'))
    },
    onError: (err) => {
      if (isAxiosError(err)) toast.error(err.response?.data?.detail ?? t('toast.setRoleFailed'))
    },
  })

  const removeMutation = useMutation({
    mutationFn: (userId: string) => removeMemberAPI(organization.id, userId),
    onSuccess: () => {
      invalidate()
      toast.success(t('toast.memberRemoved'))
    },
    onError: (err) => {
      if (isAxiosError(err)) toast.error(err.response?.data?.detail ?? t('toast.removeMemberFailed'))
    },
  })

  if (isLoading) {
    return (
      <div className="space-y-3">
        {[...Array(3)].map((_, i) => (
          <div key={i} className="h-14 animate-pulse rounded-lg bg-muted" />
        ))}
      </div>
    )
  }

  if (isError) return <QueryError error={error} onRetry={() => refetch()} />

  const columns: Column<OrganizationMember>[] = [
    {
      key: 'user',
      header: t('organizations.members.user'),
      title: true,
      cell: (m) => <code className="font-mono text-sm">{m.user_id}</code>,
    },
    {
      key: 'role',
      header: t('organizations.role'),
      cell: (m) =>
        // The owner's row shows the role as text, never a control: the API
        // refuses to re-role or remove an owner, and a control that always
        // fails is worse than no control.
        canManage && m.role !== 'owner' ? (
          <Select
            value={m.role}
            onValueChange={(role) =>
              roleMutation.mutate({ userId: m.user_id, role: role as OrganizationRole })
            }
            disabled={roleMutation.isPending}
          >
            <SelectTrigger className="w-36" aria-label={t('organizations.members.changeRole')}>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {GRANTABLE_ROLES.map((role) => (
                <SelectItem key={role} value={role}>
                  {t(`organizations.roles.${role}`)}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        ) : (
          <OrganizationRoleBadge role={m.role} />
        ),
    },
    {
      key: 'joined',
      header: t('organizations.joined'),
      cell: (m) => <span className="text-sm text-muted-foreground">{formatDate(m.created_at)}</span>,
    },
  ]

  return (
    <ResponsiveDataList
      rows={members}
      columns={columns}
      rowKey={(m) => m.user_id}
      actions={
        canManage
          ? (m) =>
              m.role === 'owner' ? null : (
                <ConfirmDialog
                  trigger={
                    <Button variant="ghost" size="sm" className="text-destructive">
                      {t('organizations.members.remove')}
                    </Button>
                  }
                  title={t('organizations.members.removeTitle')}
                  description={t('organizations.members.removeBody')}
                  onConfirm={() => removeMutation.mutateAsync(m.user_id)}
                />
              )
          : undefined
      }
      empty={
        <div className="py-12 text-center text-muted-foreground">
          <Users className="mx-auto mb-2 size-8 opacity-40" />
          <p className="text-sm">{t('organizations.members.empty')}</p>
        </div>
      }
    />
  )
}
