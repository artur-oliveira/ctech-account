'use client'

import Link from 'next/link'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

export type LegalDocumentVersion = {
  version: string
  updatedAt: string
  href: string
}

export const TERMS_VERSION_HISTORY: LegalDocumentVersion[] = [
  {version: '3.0', updatedAt: '15 de julho de 2026', href: '/terms'},
  {version: '2.0', updatedAt: '12 de julho de 2026', href: '/terms/v2'},
  {version: '1.0', updatedAt: '10 de julho de 2026', href: '/terms/v1'},
]

export const PRIVACY_VERSION_HISTORY: LegalDocumentVersion[] = [
  {version: '3.0', updatedAt: '15 de julho de 2026', href: '/privacy'},
  {version: '2.0', updatedAt: '12 de julho de 2026', href: '/privacy/v2'},
  {version: '1.0', updatedAt: '10 de julho de 2026', href: '/privacy/v1'},
]

export const DFE_VERSION_HISTORY: LegalDocumentVersion[] = [
  {version: '2.0', updatedAt: '19 de julho de 2026', href: '/products/dfe'},
  {version: '1.0', updatedAt: '10 de julho de 2026', href: '/products/dfe/v1'},
]

export const WALLET_VERSION_HISTORY: LegalDocumentVersion[] = [
  {version: '2.0', updatedAt: '19 de julho de 2026', href: '/products/wallet'},
  {version: '1.0', updatedAt: '11 de julho de 2026', href: '/products/wallet/v1'},
]

export function LegalPageLayout({
  title,
  version,
  updatedAt,
  versionHistory,
  children,
}: {
  title: string
  version: string
  updatedAt: string
  versionHistory?: LegalDocumentVersion[]
  children: ReactNode
}) {
  const { t } = useTranslation()
  return (
    <div className="min-h-screen bg-muted/40">
      <div className="mx-auto max-w-3xl px-4 py-12">
        <nav className="flex flex-wrap gap-x-4 gap-y-2 text-sm text-muted-foreground">
          <Link href="/" className="underline underline-offset-4 hover:text-foreground">
            {t('legal.backToAccount')}
          </Link>
          <Link href="/legal" className="underline underline-offset-4 hover:text-foreground">
            Central Jurídica
          </Link>
        </nav>

        <h1 className="mt-4 text-2xl font-semibold tracking-tight">{title}</h1>
        <p className="mt-1 text-xs text-muted-foreground">Versão {version}</p>
        <p className="mt-1 text-sm text-muted-foreground">{t('legal.lastUpdated', { date: updatedAt })}</p>

        <article className="mt-8 space-y-8 text-sm leading-relaxed text-foreground/90">{children}</article>

        {versionHistory && versionHistory.length > 1 && (
          <aside className="mt-10 border-t pt-6" aria-labelledby="version-history-heading">
            <h2 id="version-history-heading" className="text-base font-semibold tracking-tight">
              Histórico de versões
            </h2>
            <ul className="mt-3 space-y-2 text-sm">
              {versionHistory.map((item, index) => {
                const isDisplayed = item.version === version
                const isEffective = index === 0

                return (
                  <li key={item.version} className="flex flex-wrap items-baseline gap-x-2">
                    {isDisplayed ? (
                      <span className="font-medium" aria-current="page">
                        Versão {item.version} ({isEffective ? 'vigente' : 'exibida'})
                      </span>
                    ) : (
                      <Link href={item.href} className="underline underline-offset-4 hover:text-foreground">
                        Versão {item.version}{isEffective ? ' (vigente)' : ''}
                      </Link>
                    )}
                    <span className="text-xs text-muted-foreground">{item.updatedAt}</span>
                  </li>
                )
              })}
            </ul>
          </aside>
        )}
      </div>
    </div>
  )
}

export function LegalSection({ heading, children }: { heading: string; children: ReactNode }) {
  return (
    <section className="space-y-3">
      <h2 className="text-lg font-semibold tracking-tight text-foreground">{heading}</h2>
      <div className="space-y-3">{children}</div>
    </section>
  )
}
