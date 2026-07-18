'use client'

import Link from 'next/link'
import { useTranslation } from 'react-i18next'
import { Fingerprint, KeyRound, Layers, ShieldCheck } from 'lucide-react'
import { useAuthStore } from '@/store/auth'
import { Button } from '@/components/ui/button'

const FEATURE_ICONS = [Fingerprint, ShieldCheck, Layers, KeyRound] as const
const FEATURE_KEYS = ['passkeys', 'mfa', 'sso', 'oauth'] as const

export default function Home() {
  const { t } = useTranslation()
  const { isInitialized, accessToken } = useAuthStore()
  const authenticated = isInitialized && Boolean(accessToken)

  const ctaHref = authenticated ? '/account' : '/login'
  const ctaLabel = authenticated ? t('home.cta.dashboard') : t('home.cta.login')

  return (
    <div className="min-h-screen bg-background">
      <header className="mx-auto flex max-w-5xl items-center justify-between px-6 py-6">
        <div className="flex items-center gap-2.5">
          <div className="flex size-8 items-center justify-center rounded-lg bg-primary text-primary-foreground">
            <ShieldCheck size={16} />
          </div>
          <span className="font-semibold text-foreground">CTech Account</span>
        </div>
        <Button render={<Link href={ctaHref} />}>{ctaLabel}</Button>
      </header>

      <section className="mx-auto max-w-3xl px-6 py-16 text-center md:py-24">
        <p className="font-mono text-xs tracking-widest text-primary uppercase">{t('home.hero.eyebrow')}</p>
        <h1 className="mt-4 text-4xl font-bold leading-tight tracking-tight text-foreground md:text-5xl">
          {t('home.hero.title')}
        </h1>
        <p className="mx-auto mt-4 max-w-xl text-base leading-relaxed text-muted-foreground">
          {t('home.hero.subtitle')}
        </p>
        <div className="mt-8 flex justify-center">
          <Button size="lg" render={<Link href={ctaHref} />}>
            {ctaLabel}
          </Button>
        </div>
      </section>

      <section className="mx-auto max-w-5xl px-6 py-16">
        <h2 className="mb-8 text-center text-2xl font-bold text-foreground md:text-3xl">
          {t('home.features.title')}
        </h2>
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          {FEATURE_KEYS.map((key, i) => {
            const Icon = FEATURE_ICONS[i]
            return (
              <div key={key} className="rounded-xl bg-card p-5 ring-1 ring-foreground/10">
                <div className="flex size-10 items-center justify-center rounded-lg bg-primary/10 text-primary">
                  <Icon size={20} />
                </div>
                <p className="mt-3 font-semibold text-foreground">{t(`home.features.${key}.title`)}</p>
                <p className="mt-1.5 text-sm leading-relaxed text-muted-foreground">
                  {t(`home.features.${key}.body`)}
                </p>
              </div>
            )
          })}
        </div>
      </section>

      <footer className="border-t border-border">
        <div className="mx-auto flex max-w-5xl flex-col items-center gap-3 px-6 py-8 text-sm text-muted-foreground md:flex-row md:justify-between">
          <p>© {new Date().getFullYear()} A O CARVALHO TECH</p>
          <div className="flex items-center gap-4">
            <Link href="/terms" className="hover:text-foreground">
              {t('home.footer.terms')}
            </Link>
            <Link href="/privacy" className="hover:text-foreground">
              {t('home.footer.privacy')}
            </Link>
          </div>
        </div>
      </footer>
    </div>
  )
}
