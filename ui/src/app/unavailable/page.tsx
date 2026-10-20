'use client'

import {useRouter} from 'next/navigation'
import {useTranslation} from 'react-i18next'
import {UnavailableScreen} from '@aoctech/ui'
import {RETURN_KEY, toLocale} from '@/providers/network-provider'

/** The existing return route is read once the API answers again; only same-origin paths are honored. */
function returnPath(): string {
  try {
    const saved = sessionStorage.getItem(RETURN_KEY)
    sessionStorage.removeItem(RETURN_KEY)
    if (saved && saved.startsWith('/') && !saved.startsWith('//') && !saved.startsWith('/unavailable')) return saved
  } catch { /* ignore */ }
  return '/'
}

export default function UnavailablePage() {
  const router = useRouter()
  const {t, i18n} = useTranslation()
  return (
    <main className="min-h-screen flex items-center justify-center">
      <UnavailableScreen
        locale={toLocale(i18n.language)}
        labels={{retry: t('errors.retry')}}
        onRecovered={() => router.replace(returnPath())}
      />
    </main>
  )
}
