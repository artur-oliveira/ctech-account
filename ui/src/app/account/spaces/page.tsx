'use client'

import Link from 'next/link'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { Plus, Users } from 'lucide-react'
import { fetchSpaces } from '@/lib/queries'
import { formatDate } from '@/lib/format'
import { cn } from '@/lib/utils'
import { QueryError } from '@/components/query-error'
import { ResponsiveDataList, type Column } from '@/components/responsive-data-list'
import { OrganizationRoleBadge } from '@/components/organization-role-badge'
import { buttonVariants } from '@/components/ui/button'
import type { Organization } from '@/lib/types'

/**
 * The person's spaces — personal workspaces, the same record as an
 * organization with a different kind. Its own section rather than a filter on
 * Organizações: a household budget is not a company, and a list that mixes the
 * two makes both read wrong.
 */
export default function SpacesPage() {
  const { t } = useTranslation()
  const { data: spaces = [], isLoading, isError, error, refetch } = useQuery({
    queryKey: ['spaces'],
    queryFn: fetchSpaces,
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

  if (spaces.length === 0) {
    return (
      <div className="space-y-6">
        <Header />
        <div className="rounded-xl border bg-card px-6 py-12 text-center">
          <Users className="mx-auto size-8 opacity-40" />
          <h2 className="mt-4 text-base font-medium">{t('spaces.empty.title')}</h2>
          <p className="mx-auto mt-2 max-w-prose text-sm text-muted-foreground">
            {t('spaces.empty.body')}
          </p>
          <div className="mt-6 flex justify-center">
            <NewSpaceLink />
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
      cell: (space) => (
        <Link
          href={`/account/spaces/people?id=${encodeURIComponent(space.id)}`}
          className="block max-w-96 truncate text-sm font-medium text-primary hover:underline max-md:flex max-md:min-h-11 max-md:items-center"
          title={space.display_name}
        >
          {space.display_name}
        </Link>
      ),
    },
    {
      key: 'role',
      header: t('spaces.role'),
      cell: (space) => <OrganizationRoleBadge role={space.role} kind="personal" />,
    },
    {
      key: 'joined',
      header: t('organizations.joined'),
      cell: (space) => (
        <span className="text-sm text-muted-foreground">{formatDate(space.joined_at)}</span>
      ),
    },
  ]

  return (
    <div className="space-y-6">
      <Header action={<NewSpaceLink />} />
      <ResponsiveDataList rows={spaces} columns={columns} rowKey={(space) => space.id} />
    </div>
  )
}

/**
 * A link, not a dialog: /account/spaces/new is the one create screen, the
 * same one a product hands people to, so there is one form to keep right.
 */
function NewSpaceLink() {
  const { t } = useTranslation()
  return (
    <Link href="/account/spaces/new" className={cn(buttonVariants({ size: 'sm' }), 'max-sm:min-h-11')}>
      <Plus className="size-4" />
      {t('spaces.create')}
    </Link>
  )
}

function Header({ action }: { action?: React.ReactNode }) {
  const { t } = useTranslation()
  return (
    <div className="flex flex-col items-start gap-4 sm:flex-row sm:items-center sm:justify-between">
      <div className="min-w-0">
        <h1 className="text-2xl font-semibold">{t('spaces.title')}</h1>
        <p className="mt-1 text-sm text-muted-foreground">{t('spaces.subtitle')}</p>
      </div>
      {action}
    </div>
  )
}
