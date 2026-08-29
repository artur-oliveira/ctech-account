'use client'

import { Suspense } from 'react'
import Link from 'next/link'
import { useSearchParams } from 'next/navigation'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { ArrowLeft } from 'lucide-react'
import { fetchOrganization } from '@/lib/queries'
import { isAxiosError } from '@/lib/axios'
import { QueryError } from '@/components/query-error'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { OrganizationRoleBadge } from '@/components/organization-role-badge'
import { MembersTab } from './members-tab'
import { CompaniesTab } from './companies-tab'
import { InvitationsTab } from './invitations-tab'
import { SettingsTab } from './settings-tab'

/**
 * The id travels as a query parameter because production is a static export
 * (`next.config.ts`: `output: 'export'`), which has no dynamic segments —
 * the same shape `/admin/kyc/review` uses.
 */
export default function OrganizationDetailPage() {
  return (
    <Suspense fallback={<div className="h-40 animate-pulse rounded-lg bg-muted" />}>
      <OrganizationDetail />
    </Suspense>
  )
}

function OrganizationDetail() {
  const { t } = useTranslation()
  const id = useSearchParams().get('id') ?? ''

  const { data: organization, isLoading, isError, error, refetch } = useQuery({
    queryKey: ['organization', id],
    queryFn: () => fetchOrganization(id),
    enabled: id !== '',
    retry: false,
  })

  // A 403 answers both "you are not in this organization" and "there is no such
  // organization" — the server refuses to say which, so this screen must not
  // either. A missing id lands in the same place: an id nobody supplied is an
  // organization nobody can be a member of.
  const denied = id === '' || (isAxiosError(error) && error.response?.status === 403)

  if (denied) {
    return (
      <div className="space-y-6">
        <BackLink />
        <Alert>
          <AlertDescription>{t('organizations.detail.noAccess')}</AlertDescription>
        </Alert>
      </div>
    )
  }

  if (isLoading || !organization) {
    return (
      <div className="space-y-6">
        <BackLink />
        <div className="h-40 animate-pulse rounded-lg bg-muted" />
      </div>
    )
  }

  if (isError) {
    return <QueryError error={error} onRetry={() => refetch()} />
  }

  const canManage = organization.role === 'admin' || organization.role === 'owner'

  return (
    <div className="space-y-6">
      <div className="space-y-3">
        <BackLink />
        <div className="flex flex-wrap items-center gap-3">
          <h1 className="text-2xl font-semibold">{organization.display_name}</h1>
          <OrganizationRoleBadge role={organization.role} />
        </div>
      </div>

      <Tabs defaultValue="members">
        <TabsList>
          <TabsTrigger value="members">{t('organizations.detail.members')}</TabsTrigger>
          <TabsTrigger value="companies">{t('organizations.detail.companies')}</TabsTrigger>
          {/* A viewer or member does not get the tab: the pending list is a list
              of addresses of people who have not joined yet. */}
          {canManage && (
            <TabsTrigger value="invitations">{t('organizations.detail.invitations')}</TabsTrigger>
          )}
          <TabsTrigger value="settings">{t('organizations.detail.settings')}</TabsTrigger>
        </TabsList>

        <TabsContent value="members" className="mt-6">
          <MembersTab organization={organization} />
        </TabsContent>

        <TabsContent value="companies" className="mt-6">
          <CompaniesTab organization={organization} />
        </TabsContent>
        {canManage && (
          <TabsContent value="invitations" className="mt-6">
            <InvitationsTab organization={organization} />
          </TabsContent>
        )}
        <TabsContent value="settings" className="mt-6">
          <SettingsTab organization={organization} />
        </TabsContent>
      </Tabs>
    </div>
  )
}

function BackLink() {
  const { t } = useTranslation()
  return (
    <Link
      href="/account/organizations"
      className="inline-flex items-center gap-1.5 text-sm text-muted-foreground transition-colors hover:text-foreground"
    >
      <ArrowLeft className="size-3.5" />
      {t('organizations.detail.back')}
    </Link>
  )
}
