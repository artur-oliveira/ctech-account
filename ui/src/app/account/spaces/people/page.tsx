'use client'

import { Suspense, useEffect } from 'react'
import Link from 'next/link'
import { useRouter, useSearchParams } from 'next/navigation'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { ArrowLeft } from 'lucide-react'
import { fetchHandoff, fetchOrganization } from '@/lib/queries'
import { isAxiosError } from '@/lib/axios'
import { isPersonal } from '@/lib/types'
import { QueryError } from '@/components/query-error'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { OrganizationRoleBadge } from '@/components/organization-role-badge'
import { MembersTab } from '@/app/account/organizations/detail/members-tab'
import { InvitationsTab } from '@/app/account/organizations/detail/invitations-tab'
import { SettingsTab } from '@/app/account/organizations/detail/settings-tab'

/**
 * The people of one space: roster, invitations, access levels, transfer.
 *
 * `?id=` rather than `/account/spaces/{id}/people`: production is a static
 * export (`next.config.ts`: `output: 'export'`), which has no dynamic
 * segments — the same shape `/account/organizations/detail` uses. A product
 * handing somebody here passes `client_id`, `return_to` and `state` beside it.
 *
 * The tabs are the organization page's own, which read the workspace's kind:
 * one implementation of invitations and roles, not two drifting apart.
 */
export default function SpacePeoplePage() {
  return (
    <Suspense fallback={<div className="h-40 animate-pulse rounded-lg bg-muted" />}>
      <SpacePeople />
    </Suspense>
  )
}

function SpacePeople() {
  const { t } = useTranslation()
  const router = useRouter()
  const params = useSearchParams()
  const id = params.get('id') ?? ''
  const clientID = params.get('client_id') ?? ''
  const rawReturnTo = params.get('return_to') ?? ''
  const state = params.get('state') ?? ''
  const isHandoff = !!clientID && !!rawReturnTo

  const { data: space, isLoading, isError, error, refetch } = useQuery({
    queryKey: ['organization', id],
    queryFn: () => fetchOrganization(id),
    enabled: id !== '',
    retry: false,
  })

  // The way back is offered only once the server has vouched for it. A
  // refused handoff hides the link and nothing else: the people are still the
  // person's to manage.
  const { data: handoff } = useQuery({
    queryKey: ['handoff', clientID, rawReturnTo],
    queryFn: () => fetchHandoff(clientID, rawReturnTo, state),
    enabled: isHandoff,
    retry: false,
  })

  // An organization's id typed into this URL belongs on the organization's
  // page, which has its companies and its own words.
  const organization = !!space && !isPersonal(space)
  useEffect(() => {
    if (organization) router.replace(`/account/organizations/detail?id=${encodeURIComponent(id)}`)
  }, [organization, id, router])

  // The server answers 403 for "not a member" and for "no such space" alike,
  // and this screen does not tell them apart either.
  const status = isAxiosError(error) ? error.response?.status : undefined
  const denied = id === '' || status === 403 || status === 404

  if (denied) {
    return (
      <div className="space-y-6">
        <BackLink />
        <Alert>
          <AlertDescription>{t('spaces.detail.noAccess')}</AlertDescription>
        </Alert>
      </div>
    )
  }

  if (isError) {
    return <QueryError error={error} onRetry={() => refetch()} />
  }

  if (isLoading || !space || organization) {
    return (
      <div className="space-y-6">
        <BackLink />
        <div className="h-40 animate-pulse rounded-lg bg-muted" />
      </div>
    )
  }

  const isOwner = space.role === 'owner'
  // Built from the URL the server echoed, never the raw parameter, with the
  // state handed back untouched.
  const returnURL = handoff ? new URL(handoff.return_to) : null
  if (returnURL && state) returnURL.searchParams.set('state', state)

  return (
    <div className="space-y-6">
      <div className="space-y-3">
        <div className="flex flex-wrap items-center gap-x-6 gap-y-2">
          {returnURL && handoff && (
            <a
              href={returnURL.toString()}
              className="inline-flex items-center gap-1.5 text-sm font-medium text-primary hover:underline max-sm:min-h-11"
            >
              <ArrowLeft className="size-3.5" />
              {t('spaces.detail.backTo', { product: handoff.client_name })}
            </a>
          )}
          <BackLink />
        </div>
        <div className="flex min-w-0 flex-wrap items-center gap-3">
          <h1 className="min-w-0 text-balance text-2xl font-semibold [overflow-wrap:anywhere]">
            {space.display_name}
          </h1>
          <OrganizationRoleBadge role={space.role} kind="personal" />
        </div>
        {/* Said once, at the top, instead of leaving a column with nothing to
            press and no reason given. */}
        {!isOwner && (
          <p className="max-w-prose text-sm text-muted-foreground">{t('spaces.detail.readOnly')}</p>
        )}
      </div>

      <Tabs defaultValue="members">
        <TabsList className="min-h-11 w-full max-w-full justify-start overflow-x-auto overflow-y-hidden md:min-h-0">
          <TabsTrigger value="members" className="min-h-11 shrink-0 px-3 md:min-h-0">{t('spaces.detail.members')}</TabsTrigger>
          {/* Pending invitations are addresses of people who have not joined
              yet, and only the owner invites — so only the owner sees them. */}
          {isOwner && (
            <TabsTrigger value="invitations" className="min-h-11 shrink-0 px-3 md:min-h-0">{t('spaces.detail.invitations')}</TabsTrigger>
          )}
          <TabsTrigger value="settings" className="min-h-11 shrink-0 px-3 md:min-h-0">{t('spaces.detail.settings')}</TabsTrigger>
        </TabsList>

        <TabsContent value="members" className="mt-6">
          <MembersTab organization={space} />
        </TabsContent>
        {isOwner && (
          <TabsContent value="invitations" className="mt-6">
            <InvitationsTab organization={space} />
          </TabsContent>
        )}
        <TabsContent value="settings" className="mt-6">
          <SettingsTab organization={space} />
        </TabsContent>
      </Tabs>
    </div>
  )
}

function BackLink() {
  const { t } = useTranslation()
  return (
    <Link
      href="/account/spaces"
      className="inline-flex items-center gap-1.5 text-sm text-muted-foreground transition-colors hover:text-foreground max-sm:min-h-11"
    >
      <ArrowLeft className="size-3.5" />
      {t('spaces.detail.back')}
    </Link>
  )
}
