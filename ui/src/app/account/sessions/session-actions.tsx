'use client'

import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { revokeSessionAPI, revokeAllSessionsAPI } from '@/lib/mutations'
import { isAxiosError } from '@/lib/axios'
import { toast } from 'sonner'

export function RevokeSessionButton({ sessionId }: { sessionId: string }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const { mutate, isPending } = useMutation({
    mutationFn: () => revokeSessionAPI(sessionId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['sessions'] })
      toast.success(t('toast.sessionRevoked'))
    },
    onError: (err) => {
      if (isAxiosError(err)) toast.error(err.response?.data?.detail ?? t('toast.revokeSessionFailed'))
    },
  })

  return (
    <Button variant="outline" size="sm" onClick={() => mutate()} disabled={isPending}>
      {isPending ? t('sessions.revoking') : t('sessions.revoke')}
    </Button>
  )
}

export function RevokeAllButton() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const { mutate, isPending } = useMutation({
    mutationFn: revokeAllSessionsAPI,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['sessions'] })
      toast.success(t('toast.allSessionsRevoked'))
    },
    onError: (err) => {
      if (isAxiosError(err)) toast.error(err.response?.data?.detail ?? t('toast.revokeAllSessionsFailed'))
    },
  })

  return (
    <Button variant="destructive" size="sm" onClick={() => mutate()} disabled={isPending}>
      {isPending ? t('sessions.revoking') : t('sessions.revokeAll')}
    </Button>
  )
}
