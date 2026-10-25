// standards:allow app wrapper over @aoctech/ui NetworkProvider (Next router, react-query refetch, mock fetch and i18n wiring); the shared provider is framework-free, so 0.11 liveness/start do not replace it
'use client'

import {usePathname, useRouter} from 'next/navigation'
import {useQueryClient} from '@tanstack/react-query'
import {useTranslation} from 'react-i18next'
import {NetworkProvider, type Locale} from '@aoctech/ui'
import {API_URL} from '@/lib/env'
import {USE_MOCK} from '@/lib/mock'

export const UNAVAILABLE_PATH = '/unavailable'
export const RETURN_KEY = 'ctech-account:return'

export function toLocale(language: string | undefined): Locale {
  return language?.startsWith('en') ? 'en' : 'pt-BR'
}

// Mock mode has no API to probe: always healthy.
const mockFetch = (async () => new Response(null, {status: 204})) as typeof fetch

/** One liveness probe for the app: banner while degraded, /unavailable after repeated failures, refetch on recovery. */
export function AppNetworkProvider({children}: {children: React.ReactNode}) {
  const queryClient = useQueryClient()
  const router = useRouter()
  const pathname = usePathname()
  const {t, i18n} = useTranslation()
  return (
    <NetworkProvider
      url={`${API_URL}/v1.0/health`}
      fetch={USE_MOCK ? mockFetch : undefined}
      locale={toLocale(i18n.language)}
      labels={{retry: t('errors.retry')}}
      banner={pathname !== UNAVAILABLE_PATH}
      onReconnect={() => void queryClient.refetchQueries({type: 'active'})}
      onUnavailable={() => {
        if (window.location.pathname === UNAVAILABLE_PATH) return
        try {
          sessionStorage.setItem(RETURN_KEY, window.location.pathname + window.location.search)
        } catch { /* storage blocked: recovery falls back to "/" */ }
        router.replace(UNAVAILABLE_PATH)
      }}
    >
      {children}
    </NetworkProvider>
  )
}
