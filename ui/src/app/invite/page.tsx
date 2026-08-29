'use client'

import { Suspense, useState } from 'react'
import Link from 'next/link'
import { useRouter, useSearchParams } from 'next/navigation'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { MailOpen } from 'lucide-react'
import { toast } from 'sonner'
import { useAuthStore } from '@/store/auth'
import { acceptInvitationAPI, resendVerificationAPI } from '@/lib/mutations'
import { fetchProfile } from '@/lib/queries'
import { isAxiosError } from '@/lib/axios'
import { Button, buttonVariants } from '@/components/ui/button'
import { cn } from '@/lib/utils'
import { Alert, AlertDescription } from '@/components/ui/alert'

/**
 * Invitation acceptance.
 *
 * Deliberately outside the account shell: the recipient may not have an account
 * yet, and that layout bounces anyone without a token to /login, which would
 * lose the invitation.
 */
export default function InvitePage() {
  return (
    <main className="mx-auto flex min-h-dvh max-w-md flex-col justify-center px-4 py-12">
      <Suspense fallback={<div className="h-48 animate-pulse rounded-xl bg-muted" />}>
        <Invite />
      </Suspense>
    </main>
  )
}

type Failure = 'unverified' | 'already' | 'invalid' | null

function Invite() {
  const { t } = useTranslation()
  const router = useRouter()
  const queryClient = useQueryClient()
  const token = useSearchParams().get('token') ?? ''
  const { accessToken, isInitialized } = useAuthStore()
  const [failure, setFailure] = useState<Failure>(null)

  const { data: profile } = useQuery({
    queryKey: ['profile'],
    queryFn: fetchProfile,
    enabled: !!accessToken,
  })

  const { mutate, isPending, isSuccess } = useMutation({
    mutationFn: () => acceptInvitationAPI(token),
    onSuccess: (membership) => {
      setFailure(null)
      queryClient.invalidateQueries({ queryKey: ['organizations'] })
      // Straight into the organization rather than a success card: the page
      // they land on already carries its name in the header, and a
      // confirmation that flashes past on its way somewhere else is a step
      // nobody reads.
      router.push(
        `/account/organizations/detail?id=${encodeURIComponent(membership.organization_id)}`,
      )
    },
    onError: (err) => {
      // Three answers, and the page says something different for each. It
      // branches on the RFC 7807 `type`, never on `detail`, which is prose that
      // gets rewritten.
      if (!isAxiosError(err)) return setFailure('invalid')
      const status = err.response?.status
      const type = err.response?.data?.type ?? ''
      if (type.endsWith('/email-not-verified')) return setFailure('unverified')
      if (status === 409) return setFailure('already')
      // Unknown, expired, already used and addressed to somebody else arrive
      // alike on purpose. The page must not guess which happened.
      setFailure('invalid')
    },
  })

  const resend = useMutation({
    mutationFn: (email: string) => resendVerificationAPI(email),
    onSuccess: () => toast.success(t('organizations.invite.resent')),
  })

  if (!token) {
    return (
      <Card>
        <Alert variant="destructive">
          <AlertDescription>{t('organizations.invite.missingToken')}</AlertDescription>
        </Alert>
      </Card>
    )
  }

  // Signed out: nothing about the organization is shown — not its name, not who
  // sent it. There is no unauthenticated endpoint that would reveal it, and
  // adding one would turn a leaked link into a read capability for the
  // workspace's name.
  if (isInitialized && !accessToken) {
    const back = `/invite?token=${encodeURIComponent(token)}`
    const to = (path: string) => `${path}?continue=${encodeURIComponent(back)}`
    return (
      <Card>
        <p className="text-sm text-muted-foreground">{t('organizations.invite.signedOut')}</p>
        {/* Styled links, not Buttons rendering links: Base UI stamps
            role="button" on whatever it renders, which would strip the link
            semantics a screen reader needs to announce these as navigation. */}
        <div className="mt-6 flex flex-col gap-2 sm:flex-row">
          <Link href={to('/login')} className={cn(buttonVariants(), 'flex-1')}>
            {t('organizations.invite.signIn')}
          </Link>
          <Link
            href={to('/register')}
            className={cn(buttonVariants({ variant: 'outline' }), 'flex-1')}
          >
            {t('organizations.invite.register')}
          </Link>
        </div>
      </Card>
    )
  }

  if (!isInitialized) {
    return <div className="h-48 animate-pulse rounded-xl bg-muted" />
  }

  // The redirect is in flight; showing the accept button again would invite a
  // second POST on a token that is already spent.
  if (isSuccess) {
    return <div className="h-48 animate-pulse rounded-xl bg-muted" />
  }

  return (
    <Card>
      {failure === 'unverified' && (
        <div className="space-y-3">
          <Alert>
            <AlertDescription>{t('organizations.invite.unverified')}</AlertDescription>
          </Alert>
          <Button
            variant="outline"
            className="w-full"
            disabled={!profile || resend.isPending}
            onClick={() => profile && resend.mutate(profile.email)}
          >
            {t('organizations.invite.resend')}
          </Button>
        </div>
      )}

      {failure === 'invalid' && (
        <Alert variant="destructive">
          <AlertDescription>{t('organizations.invite.invalid')}</AlertDescription>
        </Alert>
      )}

      {failure === 'already' && (
        <div className="space-y-3">
          <Alert>
            <AlertDescription>{t('organizations.invite.already')}</AlertDescription>
          </Alert>
          <Link
            href="/account/organizations"
            className={cn(buttonVariants({ variant: 'outline' }), 'w-full')}
          >
            {t('organizations.invite.open')}
          </Link>
        </div>
      )}

      {failure !== 'already' && (
        <Button className="mt-6 w-full" disabled={isPending} onClick={() => mutate()}>
          {isPending
            ? t('organizations.invite.accepting')
            : failure
              ? t('organizations.invite.retry')
              : t('organizations.invite.accept')}
        </Button>
      )}
    </Card>
  )
}

function Card({ children }: { children: React.ReactNode }) {
  const { t } = useTranslation()
  return (
    <div className="rounded-xl border bg-card p-6">
      <div className="flex items-center gap-3">
        <MailOpen className="size-5 text-primary" />
        <h1 className="text-lg font-semibold">{t('organizations.invite.title')}</h1>
      </div>
      <div className="mt-4">{children}</div>
    </div>
  )
}
