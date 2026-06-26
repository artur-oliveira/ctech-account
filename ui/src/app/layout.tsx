import type {Metadata} from 'next'
import {Geist, Geist_Mono} from 'next/font/google'
import {Toaster} from '@/components/ui/sonner'
import {QueryProvider} from '@/providers/query-provider'
import {I18nProvider} from '@/providers/i18n-provider'
import './globals.css'
import React from 'react'

const geistSans = Geist({
  variable: '--font-geist-sans',
  subsets: ['latin'],
})

const geistMono = Geist_Mono({
  variable: '--font-geist-mono',
  subsets: ['latin'],
})

export const metadata: Metadata = {
  metadataBase: new URL('https://accounts.aoctech.app'),

  title: {
    default: 'CTech Account',
    template: '%s | CTech Account',
  },

  description:
    'Plataforma de identidade e autenticação unificada do ecossistema CTech. Gerencie sessões, tokens, permissões e acesso a todos os produtos.',

  keywords: [
    'CTech Account',
    'auth',
    'authentication',
    'authorization',
    'OAuth2',
    'OpenID Connect',
    'OIDC',
    'SSO',
    'JWT',
    'PKCE',
    'identity provider',
    'IAM',
    'login',
    'security',
  ],

  authors: [
    {
      name: 'CTech',
    },
  ],

  openGraph: {
    title: 'CTech Account',
    description:
      'Sua identidade unificada para acessar todo o ecossistema CTech com segurança.',
    url: 'https://accounts.aoctech.app',
    siteName: 'CTech Account',
    locale: 'pt_BR',
    type: 'website',
    images: [
      {
        url: '/og-image.png',
        width: 1200,
        height: 630,
        alt: 'CTech Account - Identity Platform',
      },
    ],
  },

  twitter: {
    card: 'summary_large_image',
    title: 'CTech Account',
    description:
      'Autenticação e identidade unificada para todos os produtos CTech.',
    images: ['/og-image.png'],
  },

  robots: {
    index: false,
    follow: false,
  },

  manifest: '/site.webmanifest',
}
export default function RootLayout({
                                     children,
                                   }: {
  children: React.ReactNode
}) {
  return (
    <html
      lang="pt-BR"
      className={`${geistSans.variable} ${geistMono.variable} h-full`}
      suppressHydrationWarning
    >
    <body className="min-h-screen bg-background text-foreground antialiased">
    {/* subtle identity background layer */}
    <div
      className="fixed inset-0 -z-10 opacity-[0.6] bg-linear-to-b from-background via-background to-blue-50 dark:to-blue-950"/>

    <I18nProvider>
      <QueryProvider>{children}</QueryProvider>
    </I18nProvider>

    <Toaster/>
    </body>
    </html>
  )
}