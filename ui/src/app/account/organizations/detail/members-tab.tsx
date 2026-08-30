'use client'

import {useMutation, useQuery, useQueryClient} from '@tanstack/react-query'
import {useTranslation} from 'react-i18next'
import {Users} from 'lucide-react'
import {toast} from 'sonner'
import {fetchOrganizationMembers, fetchProfile} from '@/lib/queries'
import {removeMemberAPI, setMemberRoleAPI} from '@/lib/mutations'
import {formatDate} from '@/lib/format'
import {isAxiosError} from '@/lib/axios'
import {QueryError} from '@/components/query-error'
import {ConfirmDialog} from '@/components/confirm-dialog'
import {type Column, ResponsiveDataList} from '@/components/responsive-data-list'
import {OrganizationRoleBadge} from '@/components/organization-role-badge'
import {Button} from '@/components/ui/button'
import {Select, SelectContent, SelectItem, SelectTrigger, SelectValue} from '@/components/ui/select'
import {
  assignableRoles,
  canManageMember,
  type Organization,
  type OrganizationMember,
  type OrganizationRole,
} from '@/lib/types'

export function MembersTab({organization}: { organization: Organization }) {
  const {t} = useTranslation()
  const queryClient = useQueryClient()
  const {data: profile} = useQuery({queryKey: ['profile'], queryFn: fetchProfile})
  // What this caller may hand out. Empty below admin, and never their own rank.
  const options = assignableRoles(organization.role)
  const canActOn = (m: OrganizationMember) =>
    !!profile && canManageMember(organization.role, profile.user_id, m)

  const {data: members = [], isLoading, isError, error, refetch} = useQuery({
    queryKey: ['organization-members', organization.id],
    queryFn: () => fetchOrganizationMembers(organization.id),
  })

  function invalidate() {
    void queryClient.invalidateQueries({queryKey: ['organization-members', organization.id]})
    void queryClient.invalidateQueries({queryKey: ['organization', organization.id]})
    void queryClient.invalidateQueries({queryKey: ['organizations']})
  }

  const roleMutation = useMutation({
    mutationFn: ({userId, role}: { userId: string; role: OrganizationRole }) =>
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
          <div key={i} className="h-14 animate-pulse rounded-lg bg-muted"/>
        ))}
      </div>
    )
  }

  if (isError) return <QueryError error={error} onRetry={() => refetch()}/>

  const columns: Column<OrganizationMember>[] = [
    {
      key: 'user',
      header: t('organizations.members.user'),
      title: true,
      // The name is what a colleague recognizes; the id is what support asks
      // for. Both, with the name leading — and the id alone on a row that
      // predates stored names.
      cell: (m) =>
        m.name ? (
          <div className="min-w-0">
            <span className="block truncate text-sm font-medium">{m.name}</span>
            <code className="block truncate font-mono text-xs text-muted-foreground">
              {m.user_id}
            </code>
          </div>
        ) : (
          <code className="font-mono text-sm">{m.user_id}</code>
        ),
    },
    {
      key: 'role',
      header: t('organizations.role'),
      cell: (m) =>
        // A control appears only where it can succeed. Your own row, a peer's
        // row and the owner's row all show the role as text: the server
        // refuses each of those, and a control that always fails is worse than
        // no control.
        canActOn(m) && options.length > 0 ? (
          <Select
            value={m.role}
            onValueChange={(role) =>
              roleMutation.mutate({userId: m.user_id, role: role as OrganizationRole})
            }
            disabled={roleMutation.isPending}
          >
            <SelectTrigger className="w-36 max-sm:h-11" aria-label={t('organizations.members.changeRole')}>
              <SelectValue>
                {m.role ? t(`organizations.roles.${m.role}`) : m.role}
              </SelectValue>
            </SelectTrigger>
            <SelectContent>
              {options.map((role) => (
                <SelectItem key={role} value={role}>
                  {t(`organizations.roles.${role}`)}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        ) : (
          <OrganizationRoleBadge role={m.role}/>
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
        options.length > 0
          ? (m) =>
            !canActOn(m) ? null : (
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
          <Users className="mx-auto mb-2 size-8 opacity-40"/>
          <p className="text-sm">{t('organizations.members.empty')}</p>
        </div>
      }
    />
  )
}
