'use client'

import { useEffect } from 'react'
import { I18nextProvider, useTranslation } from 'react-i18next'
import i18n from '@/lib/i18n'

function LanguageSync() {
  const { i18n: instance } = useTranslation()
  useEffect(() => {
    document.documentElement.lang = instance.language
  }, [instance.language])
  return null
}

export function I18nProvider({ children }: { children: React.ReactNode }) {
  return (
    <I18nextProvider i18n={i18n}>
      <LanguageSync />
      {children}
    </I18nextProvider>
  )
}
