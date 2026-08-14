'use client'

import { SyntheticEvent, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { removePasskeyAPI, beginPasskeyRegistrationAPI, completePasskeyRegistrationAPI } from '@/lib/mutations'
import { buildRegistrationCredential } from '@/lib/webauthn'
import { isAxiosError } from '@/lib/axios'
import { toast } from 'sonner'
import { Plus } from 'lucide-react'

export function RegisterPasskeyButton() {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const queryClient = useQueryClient()

  const { mutate, isPending, reset } = useMutation({
    mutationFn: async (name: string) => {
      const { session_token, options } = await beginPasskeyRegistrationAPI(name)
      const credential = await buildRegistrationCredential(options)
      await completePasskeyRegistrationAPI(session_token, name, credential)
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['passkeys'] })
      toast.success(t('toast.passkeyRegistered'))
      setOpen(false)
    },
    onError: (err) => {
      if (isAxiosError(err)) toast.error(err.response?.data?.detail ?? t('toast.passkeyRegistrationFailed'))
      else toast.error(t('toast.passkeyRegistrationCancelled'))
    },
  })

  function handleSubmit(event: SyntheticEvent<HTMLFormElement>) {
    event.preventDefault()
    const name = String(new FormData(event.currentTarget).get('name') ?? '').trim()
    if (name) mutate(name)
  }

  function handleOpenChange(nextOpen: boolean) {
    if (isPending) return
    setOpen(nextOpen)
    if (!nextOpen) reset()
  }

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogTrigger
        render={
          <Button size="sm">
            <Plus className="size-4" />
            {t('passkeys.add')}
          </Button>
        }
      />
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('passkeys.addTitle')}</DialogTitle>
          <DialogDescription>{t('passkeys.addDescription')}</DialogDescription>
        </DialogHeader>
        <form onSubmit={handleSubmit} className="space-y-4">
          <div className="space-y-1.5">
            <Label htmlFor="passkey-name">{t('passkeys.name')}</Label>
            <Input
              id="passkey-name"
              name="name"
              required
              maxLength={100}
              autoComplete="off"
              placeholder={t('passkeys.namePlaceholder')}
              autoFocus
            />
          </div>
          <DialogFooter showCloseButton>
            <Button type="submit" disabled={isPending}>
              {isPending ? t('passkeys.registering') : t('passkeys.continue')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

export function RemovePasskeyButton({ passkeyId }: { passkeyId: string }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const { mutate } = useMutation({
    mutationFn: () => removePasskeyAPI(passkeyId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['passkeys'] })
      toast.success(t('toast.passkeyRemoved'))
    },
    onError: (err) => {
      if (isAxiosError(err)) toast.error(err.response?.data?.detail ?? t('toast.removePasskeyFailed'))
    },
  })

  return (
    <ConfirmDialog
      variant="destructive"
      trigger={
        <Button variant="destructive" size="sm">
          {t('passkeys.remove')}
        </Button>
      }
      title={t('passkeys.removeTitle')}
      description={t('passkeys.confirmRemove')}
      onConfirm={() => mutate()}
    />
  )
}
