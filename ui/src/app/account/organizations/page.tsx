'use client'

import Link from 'next/link'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { Building2 } from 'lucide-react'
import { fetchOrganizations } from '@/lib/queries'
import { formatDate } from '@/lib/format'
import { QueryError } from '@/components/query-error'
import { ResponsiveDataList, type Column } from '@/components/responsive-data-list'
import { OrganizationRoleBadge } from '@/components/organization-role-badge'
import type { Organization } from '@/lib/types'
import { CreateOrganizationDialog } from './organization-actions'

export default function OrganizationsPage() {
  const { t } = useTranslation()
  const { data: organizations = [], isLoading, isError, error, refetch } = useQuery({
    queryKey: ['organizations'],
    queryFn: fetchOrganizations,
  })

  if (isLoading) {
    return (
      <div className="space-y-3">
        {[...Array(2)].map((_, i) => (
          <div key={i} className="h-20 animate-pulse bg-muted rounded-lg" />
        ))}
      </div>
    )
  }

  if (isError) {
    return <QueryError error={error} onRetry={() => refetch()} />
  }

  // Belonging to none is not an empty table — it is the state every new account
  // starts in, and the screen has to teach what an organization is for and hand
  // over the one action that exists. A "no rows" message here would be the same
  // dead end the billing console showed.
  if (organizations.length === 0) {
    return (
      <div className="space-y-6">
        <Header />
        <div className="rounded-xl border bg-card px-6 py-12 text-center">
          <Building2 className="mx-auto size-8 opacity-40" />
          <h2 className="mt-4 text-base font-medium">{t('organizations.empty.title')}</h2>
          <p className="mx-auto mt-2 max-w-prose text-sm text-muted-foreground">
            {t('organizations.empty.body')}
          </p>
          <div className="mt-6 flex justify-center">
            <CreateOrganizationDialog />
          </div>
        </div>
      </div>
    )
  }

  const columns: Column<Organization>[] = [
    {
      key: 'name',
      header: t('organizations.name'),
      title: true,
      cell: (org) => (
        <Link
          href={`/account/organizations/detail?id=${encodeURIComponent(org.id)}`}
          className="block max-w-96 truncate text-sm font-medium text-primary hover:underline max-md:flex max-md:min-h-11 max-md:items-center"
          title={org.display_name}
        >
          {org.display_name}
        </Link>
      ),
    },
    {
      key: 'role',
      header: t('organizations.role'),
      cell: (org) => <OrganizationRoleBadge role={org.role} />,
    },
    {
      key: 'joined',
      header: t('organizations.joined'),
      cell: (org) => (
        <span className="text-sm text-muted-foreground">{formatDate(org.joined_at)}</span>
      ),
    },
  ]

  return (
    <div className="space-y-6">
      <Header action={<CreateOrganizationDialog />} />
      {/* No actions column: everything an organization can do lives on its own
          page. A destructive control in a list row is a misclick waiting for a
          name it resembles. */}
      <ResponsiveDataList rows={organizations} columns={columns} rowKey={(org) => org.id} />
    </div>
  )
}

function Header({ action }: { action?: React.ReactNode }) {
  const { t } = useTranslation()
  return (
    <div className="flex flex-col items-start gap-4 sm:flex-row sm:items-center sm:justify-between">
      <div className="min-w-0">
        <h1 className="text-2xl font-semibold">{t('organizations.title')}</h1>
        <p className="mt-1 text-sm text-muted-foreground">{t('organizations.subtitle')}</p>
      </div>
      {action}
    </div>
  )
}
