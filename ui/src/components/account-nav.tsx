'use client'

import Link from 'next/link'
import { usePathname } from 'next/navigation'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import {
  LayoutDashboard,
  User,
  Shield,
  MonitorSmartphone,
  Key,
  AppWindow,
  Blocks,
  ChevronRight,
  Activity,
  IdCard,
} from 'lucide-react'

export function AccountNav() {
  const { t } = useTranslation()
  const pathname = usePathname()

  const navItems = [
    { href: '/account', label: t('nav.dashboard'), icon: LayoutDashboard, exact: true },
    { href: '/account/profile', label: t('nav.profile'), icon: User },
    {
      href: '/account/security',
      label: t('nav.security'),
      icon: Shield,
      children: [
        { href: '/account/security/totp', label: t('nav.authenticator') },
        { href: '/account/security/passkeys', label: t('nav.passkeys') },
      ],
    },
    { href: '/account/identity', label: t('nav.identity'), icon: IdCard },
    { href: '/account/sessions', label: t('nav.sessions'), icon: MonitorSmartphone },
    { href: '/account/activity', label: t('nav.activity'), icon: Activity },
    { href: '/account/api-keys', label: t('nav.apiKeys'), icon: Key },
    { href: '/account/oauth-clients', label: t('nav.oauthClients'), icon: AppWindow },
    { href: '/account/connected-apps', label: t('nav.connectedApps'), icon: Blocks },
  ]

  return (
    <nav className="space-y-0.5">
      {navItems.map((item) => {
        const active = item.exact ? pathname === item.href : pathname.startsWith(item.href)
        const Icon = item.icon
        return (
          <div key={item.href}>
            <Link
              href={item.href}
              className={cn(
                'flex items-center gap-2.5 rounded-md px-3 py-2 max-md:min-h-11 text-sm font-medium transition-colors min-w-0',
                // DESIGN.md §238: active = cobalt text + cobalt-tinted well
                // (never solid fill — keeps cobalt under the ≤10% rule).
                active
                  ? 'bg-primary/10 text-primary'
                  : 'text-muted-foreground hover:bg-muted hover:text-foreground',
              )}
            >
              <Icon className="size-4 shrink-0" />
              <span className="truncate">{item.label}</span>
              {item.children && <ChevronRight className="ml-auto size-3.5 shrink-0" />}
            </Link>
            {item.children && active && (
              <div className="ml-6 mt-0.5 space-y-0.5">
                {item.children.map((child) => (
                  <Link
                    key={child.href}
                    href={child.href}
                    className={cn(
                      'block rounded-md px-3 py-1.5 text-sm transition-colors',
                      pathname === child.href
                        ? 'text-primary font-medium'
                        : 'text-muted-foreground hover:text-foreground',
                    )}
                  >
                    {child.label}
                  </Link>
                ))}
              </div>
            )}
          </div>
        )
      })}
    </nav>
  )
}
