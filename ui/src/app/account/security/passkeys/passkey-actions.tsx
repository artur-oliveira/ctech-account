'use client'

import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { removePasskeyAPI, beginPasskeyRegistrationAPI, completePasskeyRegistrationAPI } from '@/lib/mutations'
import { buildRegistrationCredential } from '@/lib/webauthn'
import { isAxiosError } from '@/lib/axios'
import { toast } from 'sonner'
import { Plus } from 'lucide-react'

export function RegisterPasskeyButton() {
  const { t } = useTranslation()
  const [pending, setPending] = useState(false)
  const queryClient = useQueryClient()

  async function handleRegister() {
    setPending(true)
    try {
      const name = t('passkeys.defaultName')
      const { session_token, options } = await beginPasskeyRegistrationAPI(name)
      const credential = await buildRegistrationCredential(options)
      await completePasskeyRegistrationAPI(session_token, name, credential)
      queryClient.invalidateQueries({ queryKey: ['passkeys'] })
      toast.success(t('toast.passkeyRegistered'))
    } catch (err) {
      if (isAxiosError(err)) toast.error(err.response?.data?.detail ?? t('toast.removePasskeyFailed'))
      else toast.error(t('toast.passkeyRegistrationCancelled'))
    } finally {
      setPending(false)
    }
  }

  return (
    <Button size="sm" onClick={handleRegister} disabled={pending}>
      <Plus className="size-4" />
      {pending ? t('passkeys.registering') : t('passkeys.add')}
    </Button>
  )
}

export function RemovePasskeyButton({ passkeyId }: { passkeyId: string }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const { mutate, isPending } = useMutation({
    mutationFn: () => removePasskeyAPI(passkeyId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['passkeys'] })
      toast.success(t('toast.passkeyRemoved'))
    },
    onError: (err) => {
      if (isAxiosError(err)) toast.error(err.response?.data?.detail ?? t('toast.removePasskeyFailed'))
    },
  })

  function handleRemove() {
    if (!confirm(t('passkeys.confirmRemove'))) return
    mutate()
  }

  return (
    <Button variant="destructive" size="sm" onClick={handleRemove} disabled={isPending}>
      {isPending ? t('passkeys.removing') : t('passkeys.remove')}
    </Button>
  )
}
