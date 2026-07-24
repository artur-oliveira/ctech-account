'use client'

import { useEffect } from 'react'
import Link from 'next/link'
import { useRouter } from 'next/navigation'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { useAuthStore } from '@/store/auth'
import { fetchProfile } from '@/lib/queries'
import { AccountNav } from '@/components/account-nav'
import { AccountMobileNav } from '@/components/account-mobile-nav'
import { StepUpDialog } from '@/components/step-up-dialog'
import { TermsGate } from '@/components/terms-gate'
import { UserMenu } from '@/components/user-menu'
import { LanguageSwitcher } from '@/components/language-switcher'
import { Separator } from '@/components/ui/separator'
import { QueryError } from '@/components/query-error'

export default function AccountLayout({ children }: { children: React.ReactNode }) {
  const { t } = useTranslation()
  const router = useRouter()
  const { accessToken, isInitialized } = useAuthStore()

  useEffect(() => {
    if (isInitialized && !accessToken) {
      const path = window.location.pathname + window.location.search
      router.push(`/login?continue=${encodeURIComponent(path)}`)
    }
  }, [isInitialized, accessToken, router])

  const { data: user, isError, error, refetch } = useQuery({
    queryKey: ['profile'],
    queryFn: fetchProfile,
    enabled: !!accessToken,
  })

  if (!isInitialized || !accessToken) {
    return (
      <div className="min-h-screen flex items-center justify-center">
        <p className="animate-pulse text-muted-foreground text-sm">{t('common.loading')}</p>
      </div>
    )
  }

  if (isError) {
    return (
      <div className="min-h-screen flex items-center justify-center p-4">
        <QueryError error={error} onRetry={() => refetch()} className="max-w-md w-full" />
      </div>
    )
  }

  if (!user) {
    return (
      <div className="min-h-screen flex items-center justify-center">
        <p className="animate-pulse text-muted-foreground text-sm">{t('common.loading')}</p>
      </div>
    )
  }

  // A ToS/Privacy version bump blocks the account area until it is accepted.
  if (user.terms_pending.tos || user.terms_pending.privacy) {
    return <TermsGate pending={user.terms_pending} />
  }

  return (
    <div className="min-h-screen flex flex-col">
      <header className="sticky top-0 z-40 border-b bg-background/95 backdrop-blur supports-backdrop-filter:bg-background/60">
        <div className="mx-auto max-w-6xl flex h-14 items-center justify-between px-4">
          <AccountMobileNav />
          <Link href="/account" className="font-semibold text-sm">
            {t('app.name')}
          </Link>
          <LanguageSwitcher />
          <UserMenu user={user} />
        </div>
      </header>

      <div className="mx-auto max-w-6xl flex flex-1 w-full gap-8 px-4 py-8">
        <aside className="hidden md:block w-52 shrink-0">
          <AccountNav />
        </aside>

        <Separator orientation="vertical" className="hidden md:block h-auto" />

        <main className="flex-1 min-w-0">{children}</main>
      </div>

      <StepUpDialog />
    </div>
  )
}
