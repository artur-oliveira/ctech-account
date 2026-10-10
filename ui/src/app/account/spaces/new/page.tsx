'use client'

import {Suspense, useState, type SyntheticEvent} from 'react'
import Link from 'next/link'
import {useRouter, useSearchParams} from 'next/navigation'
import {useMutation, useQuery, useQueryClient} from '@tanstack/react-query'
import {useTranslation} from 'react-i18next'
import {Users} from 'lucide-react'
import {fetchHandoff} from '@/lib/queries'
import {createOrganizationAPI} from '@/lib/mutations'
import {isAxiosError} from '@/lib/axios'
import {planProblemOf, planURL} from '@/lib/plan-problem'
import {cn} from '@/lib/utils'
import {Button, buttonVariants} from '@/components/ui/button'
import {Input} from '@/components/ui/input'
import {Label} from '@/components/ui/label'
import {Alert, AlertDescription} from '@/components/ui/alert'

/** Matches the server's `validate:"required,max=120"`. */
const MAX_NAME = 120

/**
 * Creating a space, with an optional return trip — the organization handoff's
 * rules, with the name as the only question. Without handoff parameters this
 * is simply the create screen, which is what makes it safe to link to.
 */
export default function NewSpacePage() {
  return (
    <Suspense fallback={<div className="h-64 animate-pulse rounded-xl bg-muted"/>}>
      <NewSpace/>
    </Suspense>
  )
}

function NewSpace() {
  const {t} = useTranslation()
  const router = useRouter()
  const queryClient = useQueryClient()
  const params = useSearchParams()
  const clientID = params.get('client_id') ?? ''
  const rawReturnTo = params.get('return_to') ?? ''
  const state = params.get('state') ?? ''
  const isHandoff = !!clientID && !!rawReturnTo

  const [displayName, setDisplayName] = useState('')

  // The server decides whether this handoff is legitimate and what the product
  // is called — the same check the organization handoff uses.
  const {data: handoff, isLoading, isError} = useQuery({
    queryKey: ['handoff', clientID, rawReturnTo],
    queryFn: () => fetchHandoff(clientID, rawReturnTo, state),
    enabled: isHandoff,
    retry: false,
  })

  /**
   * Leaves through the URL the server echoed back, never the raw parameter.
   * `replace`, so Back in the product does not land on a create form whose
   * work is already done.
   */
  function leave(result: {organization_id: string} | 'cancelled') {
    if (!handoff) return
    const url = new URL(handoff.return_to)
    if (result === 'cancelled') {
      url.searchParams.set('cancelled', '1')
    } else {
      url.searchParams.set('organization_id', result.organization_id)
    }
    if (state) url.searchParams.set('state', state)
    window.location.replace(url.toString())
  }

  const {mutate, isPending, error} = useMutation({
    mutationFn: () => createOrganizationAPI(displayName.trim(), 'personal'),
    onSuccess: (space) => {
      void queryClient.invalidateQueries({queryKey: ['spaces']})
      if (isHandoff && handoff) {
        leave({organization_id: space.id})
        return
      }
      router.push('/account/spaces')
    },
  })

  const plan = planProblemOf(error)
  const errorMsg = plan?.kind === 'unavailable'
    ? t('spaces.plan.unavailable')
    : isAxiosError(error)
      // Never the server's detail: it is written for organizations.
      ? t('spaces.new.failed')
      : (error?.message ?? null)

  if (isHandoff && isLoading) {
    return <div className="h-64 animate-pulse rounded-xl bg-muted"/>
  }

  // A misconfigured integration strands nobody: the person is told, and given
  // the way to do this from their own account instead.
  if (isHandoff && (isError || !handoff)) {
    return (
      <div className="mx-auto max-w-md space-y-4 py-8 text-center">
        <Users className="mx-auto size-8 text-muted-foreground opacity-40"/>
        <h1 className="text-lg font-semibold">{t('spaces.new.handoffInvalidTitle')}</h1>
        <p className="text-sm text-muted-foreground">{t('spaces.new.handoffInvalidBody')}</p>
        <Link href="/account/spaces" className={cn(buttonVariants({variant: 'outline'}), 'max-sm:min-h-11')}>
          {t('spaces.new.goToSpaces')}
        </Link>
      </div>
    )
  }

  // Over the plan: the form has nothing left to offer. Say why, offer the
  // plans, and give the product its way back (spec § 7).
  if (plan?.kind === 'limit') {
    const plansHref = planURL()
    return (
      <div className="mx-auto max-w-md space-y-4 py-8">
        <Alert>
          <AlertDescription>
            {t('spaces.plan.spacesLimit', {count: plan.limit ?? 0, used: plan.used ?? 0})}
          </AlertDescription>
        </Alert>
        <div className="flex flex-wrap items-center gap-2">
          {plansHref && (
            <a href={plansHref} className={cn(buttonVariants(), 'max-sm:min-h-11')}>
              {t('spaces.plan.seePlans')}
            </a>
          )}
          {isHandoff ? (
            <Button type="button" variant="ghost" onClick={() => leave('cancelled')} className="max-sm:min-h-11">
              {t('spaces.plan.back')}
            </Button>
          ) : (
            <Link href="/account/spaces" className={cn(buttonVariants({variant: 'ghost'}), 'max-sm:min-h-11')}>
              {t('spaces.plan.back')}
            </Link>
          )}
        </div>
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
        <h1 className="text-xl font-semibold tracking-tight">{t('spaces.new.title')}</h1>
        <p className="text-sm text-muted-foreground">{t('spaces.new.body')}</p>
      </div>

      {/* Somebody who tapped a button in another product and landed on a
          different domain needs to be told why, or it reads as a bug — or a
          phish. */}
      {handoff && (
        <Alert>
          <AlertDescription>
            {t('spaces.new.handoffBanner', {product: handoff.client_name})}
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
          <Label htmlFor="space-name">{t('spaces.new.name')}</Label>
          <Input
            id="space-name"
            value={displayName}
            onChange={(e) => setDisplayName(e.target.value)}
            required
            maxLength={MAX_NAME}
            autoFocus
            autoComplete="off"
            placeholder={t('spaces.new.placeholder')}
            className="max-sm:h-11"
          />
        </div>

        <div className="flex items-center gap-2 pt-2">
          <Button type="submit" disabled={isPending} className="max-sm:min-h-11">
            {isPending ? t('common.saving') : t('spaces.new.submit')}
          </Button>
          {/* A real action, not a back button: the product that sent them has
              to be told, or it cannot put the person back where they were. */}
          {isHandoff ? (
            <Button type="button" variant="ghost" onClick={() => leave('cancelled')} className="max-sm:min-h-11">
              {t('common.cancel')}
            </Button>
          ) : (
            <Link href="/account/spaces" className={cn(buttonVariants({variant: 'ghost'}), 'max-sm:min-h-11')}>
              {t('common.cancel')}
            </Link>
          )}
        </div>
      </form>
    </div>
  )
}
