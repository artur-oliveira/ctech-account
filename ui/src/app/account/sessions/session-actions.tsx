'use client'

import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/confirm-dialog'
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
    <ConfirmDialog
      variant="destructive"
      trigger={
        <Button variant="outline" size="sm">
          {isPending ? t('sessions.revoking') : t('sessions.revoke')}
        </Button>
      }
      title={t('sessions.revokeTitle')}
      description={t('sessions.confirmRevoke')}
      onConfirm={() => mutate()}
    />
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
    <ConfirmDialog
      variant="destructive"
      trigger={
        <Button variant="destructive" size="sm">
          {isPending ? t('sessions.revoking') : t('sessions.revokeAll')}
        </Button>
      }
      title={t('sessions.revokeAllTitle')}
      description={t('sessions.confirmRevokeAll')}
      onConfirm={() => mutate()}
    />
  )
}
