'use client'

import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Link from 'next/link'
import { usePathname } from 'next/navigation'
import { IdCard, LayoutDashboard, Menu, Shield, Users, X } from 'lucide-react'
import { AccountNav } from '@/components/account-nav'
import { cn } from '@/lib/utils'

/**
 * Mobile bottom navigation for primary account destinations. The Menu panel
 * reuses AccountNav wholesale so every account destination stays reachable.
 */
export function AccountMobileNav() {
  const { t } = useTranslation()
  const pathname = usePathname()
  const [open, setOpen] = useState(false)
  const panelRef = useRef<HTMLDivElement>(null)
  const triggerRef = useRef<HTMLButtonElement>(null)

  // Close on Escape and on outside click — the disclosure is not a modal, so
  // it must not trap the user. Return focus to the trigger on close so
  // keyboard/screen-reader users land where they expect.
  useEffect(() => {
    if (!open) return
    function onPointerDown(e: PointerEvent) {
      if (
        panelRef.current &&
        !panelRef.current.contains(e.target as Node) &&
        !triggerRef.current?.contains(e.target as Node)
      ) {
        setOpen(false)
      }
    }
    function onKeyDown(e: KeyboardEvent) {
      if (e.key === 'Escape') {
        setOpen(false)
        triggerRef.current?.focus()
      }
    }
    document.addEventListener('pointerdown', onPointerDown)
    document.addEventListener('keydown', onKeyDown)
    return () => {
      document.removeEventListener('pointerdown', onPointerDown)
      document.removeEventListener('keydown', onKeyDown)
    }
  }, [open])

  const primaryLinks = [
    { href: '/account', label: t('nav.dashboard'), icon: LayoutDashboard, exact: true },
    { href: '/account/identity', label: t('nav.identity'), icon: IdCard },
    { href: '/account/spaces', label: t('nav.spaces'), icon: Users },
    { href: '/account/security', label: t('nav.security'), icon: Shield },
  ]

  return (
    <div className="md:hidden">
      {open && (
        <>
          <button aria-label={t('nav.closeMenu')} onClick={() => { setOpen(false); triggerRef.current?.focus() }} className="fixed inset-0 z-40 bg-black/20" />
          <div
            ref={panelRef}
            id="account-mobile-nav"
            onClick={() => setOpen(false)}
            className="fixed inset-x-0 bottom-[calc(4rem+env(safe-area-inset-bottom))] z-50 max-h-[min(70dvh,36rem)] overflow-y-auto border-t bg-background px-4 py-3 shadow-lg"
          >
            <AccountNav />
          </div>
        </>
      )}
      <nav aria-label={t('nav.accountNavigation')} className="fixed inset-x-0 bottom-0 z-40 border-t bg-background/95 px-1 pt-1 backdrop-blur supports-backdrop-filter:bg-background/80 pb-[max(0.25rem,env(safe-area-inset-bottom))]">
        <div className="mx-auto grid max-w-lg grid-cols-5">
          {primaryLinks.map(({ href, label, icon: Icon, exact }) => {
            const active = exact ? pathname === href : pathname.startsWith(href)
            return (
              <Link key={href} href={href} aria-current={active ? 'page' : undefined} className={cn('flex min-h-12 flex-col items-center justify-center gap-0.5 rounded-md text-xs font-medium leading-tight', active ? 'text-primary' : 'text-muted-foreground')}>
                <Icon className="size-5" />
                <span className="max-w-full truncate">{label}</span>
              </Link>
            )
          })}
          <button ref={triggerRef} type="button" aria-expanded={open} aria-controls="account-mobile-nav" onClick={() => setOpen((value) => !value)} className="flex min-h-12 flex-col items-center justify-center gap-0.5 rounded-md text-xs font-medium leading-tight text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
            {open ? <X className="size-5" /> : <Menu className="size-5" />}
            <span>{t('nav.menu')}</span>
          </button>
        </div>
      </nav>
    </div>
  )
}
