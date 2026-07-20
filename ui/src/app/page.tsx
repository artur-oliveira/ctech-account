'use client'

import Link from 'next/link'
import { useTranslation } from 'react-i18next'
import { ShieldCheck } from 'lucide-react'
import { useAuthStore } from '@/store/auth'
import { Button } from '@/components/ui/button'
import { LanguageSwitcher } from '@/components/language-switcher'

export default function Home() {
  const { t } = useTranslation()
  const { isInitialized, accessToken } = useAuthStore()
  const authenticated = isInitialized && Boolean(accessToken)

  const ctaHref = authenticated ? '/account' : '/login'
  const ctaLabel = authenticated ? t('home.cta.dashboard') : t('home.cta.login')

  return (
    <div className="flex min-h-screen flex-col bg-background">
      <header className="mx-auto flex w-full max-w-3xl justify-end px-6 py-5">
        <LanguageSwitcher />
      </header>

      <main className="flex flex-1 items-center px-6 py-12">
        <section className="mx-auto w-full max-w-md">
          <div className="flex items-center gap-2.5">
            <div className="flex size-9 items-center justify-center rounded-lg bg-primary text-primary-foreground">
              <ShieldCheck aria-hidden="true" size={18} />
            </div>
            <span className="font-semibold text-foreground">CTech Account</span>
          </div>
          <h1 className="mt-8 text-2xl font-semibold tracking-tight text-foreground">
            {t('home.title')}
          </h1>
          <p className="mt-3 max-w-prose text-base leading-relaxed text-muted-foreground">
            {t('home.description')}
          </p>
          <Button
            size="lg"
            className="mt-8 h-auto min-h-9 w-full whitespace-normal py-2 sm:w-auto"
            render={<Link href={ctaHref} />}
          >
            {ctaLabel}
          </Button>
        </section>
      </main>

      <footer className="mx-auto flex w-full max-w-3xl flex-col gap-3 px-6 py-6 text-sm text-muted-foreground sm:flex-row sm:items-center sm:justify-between">
        <p>© {new Date().getFullYear()} A O CARVALHO TECH</p>
        <div className="flex flex-wrap gap-x-4 gap-y-2">
          <Link href="/terms" className="hover:text-foreground">
            {t('home.footer.terms')}
          </Link>
          <Link href="/privacy" className="hover:text-foreground">
            {t('home.footer.privacy')}
          </Link>
        </div>
      </footer>
    </div>
  )
}
