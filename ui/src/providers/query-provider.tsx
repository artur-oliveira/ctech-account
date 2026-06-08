'use client'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useEffect, useRef } from 'react'
import axios from 'axios'
import { useAuthStore } from '@/store/auth'
import { API_URL, CLIENT_ID } from '@/lib/axios'

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 30_000,
      retry: 1,
      refetchOnWindowFocus: false,
    },
  },
})

export function QueryProvider({ children }: { children: React.ReactNode }) {
  return (
    <QueryClientProvider client={queryClient}>
      <AuthInitializer />
      {children}
    </QueryClientProvider>
  )
}

function AuthInitializer() {
  const initialized = useRef(false)

  useEffect(() => {
    if (initialized.current) return
    initialized.current = true

    const store = useAuthStore.getState()
    const params = new URLSearchParams({ grant_type: 'refresh_token', client_id: CLIENT_ID })

    axios
      .post(`${API_URL}/v1.0/token`, params, {
        withCredentials: true,
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      })
      .then(({ data }) => store.setAccessToken(data.access_token))
      .catch(() => store.clearAuth())
      .finally(() => store.setInitialized())
  }, [])

  return null
}
