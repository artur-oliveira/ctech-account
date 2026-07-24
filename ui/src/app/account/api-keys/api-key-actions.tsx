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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { createAPIKeyAPI, revokeAPIKeyAPI } from '@/lib/mutations'
import { ScopePicker } from '@/components/scope-picker'
import { isAxiosError } from '@/lib/axios'
import { toast } from 'sonner'
import { Plus, Copy } from 'lucide-react'

/** Read-only profile access — mirrors the API default. */
const DEFAULT_API_KEY_SCOPE = 'account:profile:read'

/** Fixed expiry choices (days); 0 = never expires. */
const EXPIRY_OPTIONS = [30, 90, 180, 365, 0] as const

export function CreateAPIKeyDialog() {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [createdKey, setCreatedKey] = useState<string | null>(null)
  const [scopes, setScopes] = useState<string[]>([DEFAULT_API_KEY_SCOPE])
  const [expiry, setExpiry] = useState<string>('90')
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
    mutate({
      name: fd.get('name') as string,
      scopes: scopes.length > 0 ? scopes : [DEFAULT_API_KEY_SCOPE],
      expires_in_days: parseInt(expiry) || 0,
    })
  }

  function handleClose() {
    setOpen(false)
    setCreatedKey(null)
    setScopes([DEFAULT_API_KEY_SCOPE])
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
              <Input readOnly value={createdKey} className="font-mono text-xs min-w-0" />
              <Button variant="outline" size="icon" aria-label={t('common.copy')} onClick={handleCopy}>
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
            <div className="space-y-1.5">
              <Label>{t('apiKeys.dialog.scopes')}</Label>
              <ScopePicker value={scopes} onChange={setScopes} />
              <p className="text-xs text-muted-foreground">{t('apiKeys.dialog.scopesHint')}</p>
            </div>
            <div className="space-y-1.5">
              <Label>{t('apiKeys.dialog.expiry')}</Label>
              <Select value={expiry} onValueChange={(v) => setExpiry(v ?? '90')}>
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {EXPIRY_OPTIONS.map((days) => (
                    <SelectItem key={days} value={String(days)}>
                      {days === 0 ? t('apiKeys.dialog.expiryNever') : t('apiKeys.dialog.expiryDays', { days })}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
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
  const { mutate } = useMutation({
    mutationFn: () => revokeAPIKeyAPI(keyId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['api-keys'] })
      toast.success(t('toast.keyRevoked'))
    },
    onError: (err) => {
      if (isAxiosError(err)) toast.error(err.response?.data?.detail ?? t('toast.revokeKeyFailed'))
    },
  })

  return (
    <ConfirmDialog
      variant="destructive"
      trigger={
        <Button variant="destructive" size="sm">
          {t('apiKeys.revoke')}
        </Button>
      }
      title={t('apiKeys.revokeTitle')}
      description={t('apiKeys.confirmRevoke')}
      onConfirm={() => mutate()}
    />
  )
}
