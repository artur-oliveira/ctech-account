'use client'

import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { removeTOTPAPI, unlinkGoogleAPI } from '@/lib/mutations'
import { isAxiosError } from '@/lib/axios'
import { toast } from 'sonner'

export function RemoveTOTPButton() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const { mutate } = useMutation({
    mutationFn: removeTOTPAPI,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['totp-setup'] })
      queryClient.invalidateQueries({ queryKey: ['totp-status'] })
      toast.success(t('toast.totpRemoved'))
    },
    onError: (err) => {
      if (isAxiosError(err)) toast.error(err.response?.data?.detail ?? t('toast.removeTOTPFailed'))
    },
  })

  return (
    <ConfirmDialog
      variant="destructive"
      trigger={
        <Button variant="destructive" size="sm">
          {t('security.totp.remove')}
        </Button>
      }
      title={t('security.totp.removeTitle')}
      description={t('security.totp.confirmRemove')}
      onConfirm={() => mutate()}
    />
  )
}

/**
 * Removes the bound Google identity. Step-up is enforced server-side (the
 * axios interceptor opens the MFA challenge and retries); passwordless
 * accounts get a 409 surfaced as a toast instead of unlinking themselves out
 * of a login method.
 */
export function UnlinkGoogleButton() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const { mutate } = useMutation({
    mutationFn: unlinkGoogleAPI,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['profile'] })
      toast.success(t('toast.googleUnlinked'))
    },
    onError: (err) => {
      if (isAxiosError(err)) toast.error(err.response?.data?.detail ?? t('toast.googleUnlinkFailed'))
    },
  })

  return (
    <ConfirmDialog
      variant="destructive"
      trigger={
        <Button variant="destructive" size="sm">
          {t('security.google.unlink')}
        </Button>
      }
      title={t('security.google.unlinkTitle')}
      description={t('security.google.confirmUnlink')}
      onConfirm={() => mutate()}
    />
  )
}
