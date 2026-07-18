'use client'

import Link from 'next/link'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

export function LegalPageLayout({
  title,
  updatedAt,
  children,
}: {
  title: string
  updatedAt: string
  children: ReactNode
}) {
  const { t } = useTranslation()
  return (
    <div className="min-h-screen bg-muted/40">
      <div className="mx-auto max-w-3xl px-4 py-12">
        <Link href="/" className="text-sm text-muted-foreground underline underline-offset-4 hover:text-foreground">
          {t('legal.backToAccount')}
        </Link>

        <h1 className="mt-4 text-2xl font-semibold tracking-tight">{title}</h1>
        <p className="mt-1 text-sm text-muted-foreground">{t('legal.lastUpdated', { date: updatedAt })}</p>

        <article className="mt-8 space-y-8 text-sm leading-relaxed text-foreground/90">{children}</article>
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
