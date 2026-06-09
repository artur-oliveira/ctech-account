'use client'

import { useState, SyntheticEvent } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { createAPIKeyAPI, revokeAPIKeyAPI } from '@/lib/mutations'
import { isAxiosError } from '@/lib/axios'
import { toast } from 'sonner'
import { Plus, Copy } from 'lucide-react'

const SCOPES = ['read', 'write', 'admin']

export function CreateAPIKeyDialog() {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [createdKey, setCreatedKey] = useState<string | null>(null)
  const queryClient = useQueryClient()

  const { mutate, isPending, reset } = useMutation({
    mutationFn: createAPIKeyAPI,
    onSuccess: (data) => {
      const key = data.raw_key ?? data.key
      if (key) setCreatedKey(key as string)
      queryClient.invalidateQueries({ queryKey: ['api-keys'] })
    },
    onError: (err) => {
      if (isAxiosError(err)) toast.error(err.response?.data?.detail ?? t('toast.createKeyFailed'))
    },
  })

  function handleSubmit(e: SyntheticEvent<HTMLFormElement>) {
    e.preventDefault()
    const fd = new FormData(e.currentTarget)
    const scopes = fd.getAll('scopes') as string[]
    mutate({
      name: fd.get('name') as string,
      scopes: scopes.length > 0 ? scopes : ['read'],
      expires_in_days: parseInt(fd.get('expires_in_days') as string) || 0,
    })
  }

  function handleClose() {
    setOpen(false)
    setCreatedKey(null)
    reset()
  }

  function handleCopy() {
    if (createdKey) {
      navigator.clipboard.writeText(createdKey)
      toast.success(t('toast.keyCopied'))
    }
  }

  return (
    <Dialog open={open} onOpenChange={(v) => { if (!v) handleClose(); else setOpen(true) }}>
      <DialogTrigger
        render={
          <Button size="sm">
            <Plus className="size-4" />
            {t('apiKeys.newKey')}
          </Button>
        }
      />
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('apiKeys.dialog.title')}</DialogTitle>
          <DialogDescription>{t('apiKeys.dialog.description')}</DialogDescription>
        </DialogHeader>

        {createdKey ? (
          <div className="space-y-4">
            <Alert>
              <AlertDescription className="text-xs">
                {t('apiKeys.dialog.copyWarning')}
              </AlertDescription>
            </Alert>
            <div className="flex gap-2">
              <Input readOnly value={createdKey} className="font-mono text-xs" />
              <Button variant="outline" size="icon" onClick={handleCopy}>
                <Copy className="size-4" />
              </Button>
            </div>
            <DialogFooter>
              <Button onClick={handleClose}>{t('apiKeys.dialog.done')}</Button>
            </DialogFooter>
          </div>
        ) : (
          <form onSubmit={handleSubmit} className="space-y-4">
            <div className="space-y-1.5">
              <Label htmlFor="name">{t('apiKeys.dialog.name')}</Label>
              <Input id="name" name="name" required placeholder={t('apiKeys.dialog.namePlaceholder')} />
            </div>
            <div className="space-y-2">
              <Label>{t('apiKeys.dialog.scopes')}</Label>
              <div className="flex gap-4">
                {SCOPES.map((scope) => (
                  <label key={scope} className="flex items-center gap-1.5 cursor-pointer text-sm">
                    <input
                      type="checkbox"
                      name="scopes"
                      value={scope}
                      defaultChecked={scope === 'read'}
                    />
                    {scope}
                  </label>
                ))}
              </div>
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="expires_in_days">{t('apiKeys.dialog.expiry')}</Label>
              <Input
                id="expires_in_days"
                name="expires_in_days"
                type="number"
                min={0}
                max={365}
                defaultValue={0}
              />
            </div>
            <DialogFooter showCloseButton>
              <Button type="submit" disabled={isPending}>
                {isPending ? t('apiKeys.dialog.creating') : t('apiKeys.dialog.create')}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  )
}

export function RevokeAPIKeyButton({ keyId }: { keyId: string }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const { mutate, isPending } = useMutation({
    mutationFn: () => revokeAPIKeyAPI(keyId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['api-keys'] })
      toast.success(t('toast.keyRevoked'))
    },
    onError: (err) => {
      if (isAxiosError(err)) toast.error(err.response?.data?.detail ?? t('toast.revokeKeyFailed'))
    },
  })

  function handleRevoke() {
    if (!confirm(t('apiKeys.confirmRevoke'))) return
    mutate()
  }

  return (
    <Button variant="destructive" size="sm" onClick={handleRevoke} disabled={isPending}>
      {isPending ? t('apiKeys.revoking') : t('apiKeys.revoke')}
    </Button>
  )
}
