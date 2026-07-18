'use client'

import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Languages } from 'lucide-react'

/**
 * Manual locale toggle (pt-BR ⇄ en). The i18n provider already detects the
 * browser language on first load and persists the choice; this just lets the user
 * switch without clearing storage.
 */
export function LanguageSwitcher() {
  const { i18n, t } = useTranslation()
  const current = i18n.language?.startsWith('en') ? 'en' : 'pt-BR'

  function toggle() {
    void i18n.changeLanguage(current === 'en' ? 'pt-BR' : 'en')
  }

  const nextLabel = current === 'en' ? 'PT' : 'EN'

  return (
    <Button
      variant="ghost"
      size="sm"
      onClick={toggle}
      aria-label={`${t('common.language')}: ${nextLabel}`}
      className="gap-1.5"
    >
      <Languages className="size-4" />
      <span className="text-xs font-medium uppercase">{nextLabel}</span>
    </Button>
  )
}
