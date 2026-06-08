'use client'

import { useEffect } from 'react'
import { useRouter } from 'next/navigation'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { useAuthStore } from '@/store/auth'
import { fetchProfile } from '@/lib/queries'
import { AccountNav } from '@/components/account-nav'
import { UserMenu } from '@/components/user-menu'
import { Separator } from '@/components/ui/separator'

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

  const { data: user } = useQuery({
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

  if (!user) return null

  return (
    <div className="min-h-screen flex flex-col">
      <header className="sticky top-0 z-40 border-b bg-background/95 backdrop-blur supports-backdrop-filter:bg-background/60">
        <div className="mx-auto max-w-6xl flex h-14 items-center justify-between px-4">
          <a href="/account" className="font-semibold text-sm">
            {t('app.name')}
          </a>
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
    </div>
  )
}
