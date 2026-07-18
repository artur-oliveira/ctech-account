'use client'

import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Menu, X } from 'lucide-react'
import { AccountNav } from '@/components/account-nav'

/**
 * Mobile-only disclosure for the account section nav. The desktop sidebar is
 * hidden below `md`, so this is the only way to reach account sub-pages on
 * small screens. Reuses AccountNav wholesale to avoid duplicating navItems.
 */
export function AccountMobileNav() {
  const { t } = useTranslation()
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

  return (
    <div className="md:hidden">
      <button
        ref={triggerRef}
        type="button"
        aria-expanded={open}
        aria-controls="account-mobile-nav"
        onClick={() => setOpen((o) => !o)}
        className="inline-flex size-9 items-center justify-center rounded-lg text-foreground hover:bg-muted outline-none focus-visible:ring-3 focus-visible:ring-ring/70"
      >
        {open ? <X className="size-5" /> : <Menu className="size-5" />}
        <span className="sr-only">{t('nav.menu')}</span>
      </button>

      {open && (
        <>
          <div aria-hidden="true" className="fixed inset-0 z-30 bg-black/10 backdrop-blur-xs" />
          <div
            ref={panelRef}
            id="account-mobile-nav"
            onClick={() => setOpen(false)}
            className="absolute inset-x-0 top-14 z-40 border-b bg-background p-3 ring-1 ring-foreground/10"
          >
            <AccountNav />
          </div>
        </>
      )}
    </div>
  )
}
