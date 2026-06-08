'use client'

import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { removeTOTPAPI } from '@/lib/mutations'
import { isAxiosError } from '@/lib/axios'
import { toast } from 'sonner'

export function RemoveTOTPButton() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const { mutate, isPending } = useMutation({
    mutationFn: removeTOTPAPI,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['totp-setup'] })
      toast.success(t('toast.totpRemoved'))
    },
    onError: (err) => {
      if (isAxiosError(err)) toast.error(err.response?.data?.detail ?? t('toast.removeTOTPFailed'))
    },
  })

  function handleRemove() {
    if (!confirm(t('security.totp.confirmRemove'))) return
    mutate()
  }

  return (
    <Button variant="destructive" size="sm" onClick={handleRemove} disabled={isPending}>
      {isPending ? t('security.totp.removing') : t('security.totp.remove')}
    </Button>
  )
}
