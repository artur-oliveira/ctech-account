'use client'

import { useEffect } from 'react'
import { useRouter } from 'next/navigation'
import { useAuthStore } from '@/store/auth'

export default function RootPage() {
  const router = useRouter()
  const { accessToken, isInitialized } = useAuthStore()

  useEffect(() => {
    if (!isInitialized) return
    router.replace(accessToken ? '/account' : '/login')
  }, [isInitialized, accessToken, router])

  return null
}
