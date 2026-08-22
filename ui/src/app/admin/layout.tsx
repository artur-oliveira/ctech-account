'use client'

import { useEffect } from 'react'
import Link from 'next/link'
import Image from 'next/image'
import { useRouter } from 'next/navigation'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { useAuthStore } from '@/store/auth'
import { fetchProfile } from '@/lib/queries'
import { UserMenu } from '@/components/user-menu'
import { LanguageSwitcher } from '@/components/language-switcher'
import { QueryError } from '@/components/query-error'

/**
 * Gates every `/admin/*` route: requires a session (like `account/layout.tsx`)
 * and a non-empty `support_role` (like `account/layout.tsx` gates on
 * `terms_pending` — a second condition checked once the profile loads,
 * not per-page). A regular user is sent to `/account`, never shown an
 * "access denied" page, so the admin surface's existence isn't a permission
 * oracle for someone without the role.
 */
export default function AdminLayout({ children }: { children: React.ReactNode }) {
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

  useEffect(() => {
    if (user && !user.support_role) {
      router.push('/account')
    }
  }, [user, router])

  if (!isInitialized || !accessToken || (!user && !isError)) {
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

  if (!user || !user.support_role) {
    return (
      <div className="min-h-screen flex items-center justify-center">
        <p className="animate-pulse text-muted-foreground text-sm">{t('common.loading')}</p>
      </div>
    )
  }

  return (
    <div className="min-h-screen flex flex-col">
      <header className="sticky top-0 z-40 border-b bg-background/95 backdrop-blur supports-backdrop-filter:bg-background/60">
        <div className="mx-auto max-w-5xl flex h-14 items-center justify-between px-4">
          <Link href="/admin/support" className="flex items-center gap-2 font-semibold text-sm">
            <Image src="/app.svg" alt="" aria-hidden="true" width={28} height={28} />
            {t('support.admin.title')}
          </Link>
          <div className="flex items-center gap-2">
            <LanguageSwitcher />
            <UserMenu user={user} />
          </div>
        </div>
      </header>
      <main className="mx-auto w-full max-w-5xl flex-1 px-4 py-8">{children}</main>
    </div>
  )
}
