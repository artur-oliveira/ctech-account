'use client'

import { useState, SyntheticEvent } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { ConfirmDialog } from '@/components/confirm-dialog'
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
import {
  createOAuthClientAPI,
  updateOAuthClientAPI,
  deleteOAuthClientAPI,
  regenerateOAuthClientSecretAPI,
  type OAuthClientPayload,
} from '@/lib/mutations'
import { isAxiosError } from '@/lib/axios'
import { ScopePicker } from '@/components/scope-picker'
import type { OAuthClient } from '@/lib/types'
import { toast } from 'sonner'
import { Plus, Copy, RefreshCw, Trash2, Pencil } from 'lucide-react'

const OAUTH_CLIENTS_QUERY_KEY = ['oauth-clients']

/** Splits whitespace/comma/newline separated user input into clean values. */
function splitList(value: string): string[] {
  return value
    .split(/[\s,]+/)
    .map((s) => s.trim())
    .filter(Boolean)
}

function readPayload(e: SyntheticEvent<HTMLFormElement>, scopes: string[]): OAuthClientPayload {
  const fd = new FormData(e.currentTarget)
  const audience = splitList((fd.get('audience') as string) ?? '')
  return {
    name: fd.get('name') as string,
    redirect_uris: splitList((fd.get('redirect_uris') as string) ?? ''),
    allowed_scopes: scopes,
    ...(audience.length > 0 ? { audience } : {}),
  }
}

/** Default selection for new clients: standard OIDC sign-in scopes. */
const DEFAULT_CLIENT_SCOPES = ['openid', 'profile', 'email']

function SecretReveal({ secret, onDone }: { secret: string; onDone: () => void }) {
  const { t } = useTranslation()

  function handleCopy() {
    navigator.clipboard.writeText(secret)
    toast.success(t('toast.secretCopied'))
  }

  return (
    <div className="space-y-4">
      <Alert>
        <AlertDescription className="text-xs">{t('oauthClients.dialog.secretWarning')}</AlertDescription>
      </Alert>
      <div className="flex gap-2">
        <Input readOnly value={secret} className="font-mono text-xs min-w-0" />
        <Button variant="outline" size="icon" aria-label={t('common.copy')} onClick={handleCopy}>
          <Copy className="size-4" />
        </Button>
      </div>
      <DialogFooter>
        <Button onClick={onDone}>{t('oauthClients.dialog.done')}</Button>
      </DialogFooter>
    </div>
  )
}

function ClientFormFields({
  client,
  scopes,
  onScopesChange,
}: {
  client?: OAuthClient
  scopes: string[]
  onScopesChange: (scopes: string[]) => void
}) {
  const { t } = useTranslation()
  return (
    <>
      <div className="space-y-1.5">
        <Label htmlFor="name">{t('oauthClients.dialog.name')}</Label>
        <Input id="name" name="name" required maxLength={100} defaultValue={client?.name} />
      </div>
      <div className="space-y-1.5">
        <Label htmlFor="redirect_uris">{t('oauthClients.dialog.redirectURIs')}</Label>
        <Input
          id="redirect_uris"
          name="redirect_uris"
          required
          placeholder="https://app.example.com/callback"
          defaultValue={client?.redirect_uris.join(' ')}
        />
        <p className="text-xs text-muted-foreground">{t('oauthClients.dialog.redirectURIsHint')}</p>
      </div>
      <div className="space-y-1.5">
        <Label>{t('oauthClients.dialog.scopes')}</Label>
        <ScopePicker value={scopes} onChange={onScopesChange} includeIdentity />
        <p className="text-xs text-muted-foreground">{t('oauthClients.dialog.scopesHint')}</p>
      </div>
      <div className="space-y-1.5">
        <Label htmlFor="audience">{t('oauthClients.dialog.audience')}</Label>
        <Input
          id="audience"
          name="audience"
          placeholder="https://dfe.aoctech.app"
          defaultValue={client?.audience?.join(' ') ?? ''}
        />
        <p className="text-xs text-muted-foreground">{t('oauthClients.dialog.audienceHint')}</p>
      </div>
    </>
  )
}

export function CreateOAuthClientDialog() {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [createdSecret, setCreatedSecret] = useState<string | null>(null)
  const [scopes, setScopes] = useState<string[]>(DEFAULT_CLIENT_SCOPES)
  const queryClient = useQueryClient()

  const { mutate, isPending, reset } = useMutation({
    mutationFn: createOAuthClientAPI,
    onSuccess: (data) => {
      queryClient.invalidateQueries({ queryKey: OAUTH_CLIENTS_QUERY_KEY })
      if (data.client_secret) {
        setCreatedSecret(data.client_secret)
      } else {
        toast.success(t('toast.clientCreated'))
        handleClose()
      }
    },
    onError: (err) => {
      if (isAxiosError(err)) toast.error(err.response?.data?.detail ?? t('toast.clientSaveFailed'))
    },
  })

  function handleSubmit(e: SyntheticEvent<HTMLFormElement>) {
    e.preventDefault()
    const fd = new FormData(e.currentTarget)
    mutate({
      ...readPayload(e, scopes),
      client_type: (fd.get('client_type') as 'public' | 'confidential') ?? 'confidential',
    })
  }

  function handleClose() {
    setOpen(false)
    setCreatedSecret(null)
    setScopes(DEFAULT_CLIENT_SCOPES)
    reset()
  }

  return (
    <Dialog open={open} onOpenChange={(v) => { if (!v) handleClose(); else setOpen(true) }}>
      <DialogTrigger
        render={
          <Button size="sm">
            <Plus className="size-4" />
            {t('oauthClients.newClient')}
          </Button>
        }
      />
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('oauthClients.dialog.createTitle')}</DialogTitle>
          <DialogDescription>{t('oauthClients.dialog.createDescription')}</DialogDescription>
        </DialogHeader>

        {createdSecret ? (
          <SecretReveal secret={createdSecret} onDone={handleClose} />
        ) : (
          <form onSubmit={handleSubmit} className="space-y-4">
            <ClientFormFields scopes={scopes} onScopesChange={setScopes} />
            <fieldset className="space-y-2 border-0">
              <legend className="text-sm leading-none font-medium select-none">
                {t('oauthClients.dialog.clientType')}
              </legend>
              <div className="flex gap-4 text-sm">
                <label className="flex items-center gap-1.5 cursor-pointer">
                  <input type="radio" name="client_type" value="confidential" defaultChecked />
                  {t('oauthClients.dialog.confidential')}
                </label>
                <label className="flex items-center gap-1.5 cursor-pointer">
                  <input type="radio" name="client_type" value="public" />
                  {t('oauthClients.dialog.public')}
                </label>
              </div>
              <p className="text-xs text-muted-foreground">{t('oauthClients.dialog.clientTypeHint')}</p>
            </fieldset>
            <DialogFooter showCloseButton>
              <Button type="submit" disabled={isPending}>
                {isPending ? t('oauthClients.dialog.creating') : t('oauthClients.dialog.create')}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  )
}

export function EditOAuthClientDialog({ client }: { client: OAuthClient }) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [scopes, setScopes] = useState<string[]>(client.allowed_scopes)
  const queryClient = useQueryClient()

  const { mutate, isPending } = useMutation({
    mutationFn: (payload: OAuthClientPayload) => updateOAuthClientAPI(client.client_id, payload),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: OAUTH_CLIENTS_QUERY_KEY })
      toast.success(t('toast.clientUpdated'))
      setOpen(false)
    },
    onError: (err) => {
      if (isAxiosError(err)) toast.error(err.response?.data?.detail ?? t('toast.clientSaveFailed'))
    },
  })

  function handleSubmit(e: SyntheticEvent<HTMLFormElement>) {
    e.preventDefault()
    mutate(readPayload(e, scopes))
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger
        render={
          <Button variant="outline" size="sm">
            <Pencil className="size-4" />
            {t('oauthClients.edit')}
          </Button>
        }
      />
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('oauthClients.dialog.editTitle')}</DialogTitle>
          <DialogDescription>{client.client_id}</DialogDescription>
        </DialogHeader>
        <form onSubmit={handleSubmit} className="space-y-4">
          <ClientFormFields client={client} scopes={scopes} onScopesChange={setScopes} />
          <DialogFooter showCloseButton>
            <Button type="submit" disabled={isPending}>
              {isPending ? t('oauthClients.dialog.saving') : t('oauthClients.dialog.save')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

export function RegenerateSecretButton({ clientId }: { clientId: string }) {
  const { t } = useTranslation()
  const [secret, setSecret] = useState<string | null>(null)

  const { mutate } = useMutation({
    mutationFn: () => regenerateOAuthClientSecretAPI(clientId),
    onSuccess: (data) => setSecret(data.client_secret),
    onError: (err) => {
      if (isAxiosError(err)) toast.error(err.response?.data?.detail ?? t('toast.clientSaveFailed'))
    },
  })

  return (
    <>
      <ConfirmDialog
        variant="destructive"
        trigger={
          <Button variant="outline" size="sm">
            <RefreshCw className="size-4" />
            {t('oauthClients.regenerate')}
          </Button>
        }
        title={t('oauthClients.regenerateTitle')}
        description={t('oauthClients.confirmRegenerate')}
        onConfirm={() => mutate()}
      />
      <Dialog open={secret !== null} onOpenChange={(v) => { if (!v) setSecret(null) }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('oauthClients.dialog.newSecretTitle')}</DialogTitle>
          </DialogHeader>
          {secret && <SecretReveal secret={secret} onDone={() => setSecret(null)} />}
        </DialogContent>
      </Dialog>
    </>
  )
}

export function DeleteOAuthClientButton({ clientId }: { clientId: string }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const { mutate } = useMutation({
    mutationFn: () => deleteOAuthClientAPI(clientId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: OAUTH_CLIENTS_QUERY_KEY })
      toast.success(t('toast.clientDeleted'))
    },
    onError: (err) => {
      if (isAxiosError(err)) toast.error(err.response?.data?.detail ?? t('toast.clientDeleteFailed'))
    },
  })

  return (
    <ConfirmDialog
      variant="destructive"
      trigger={
        <Button variant="destructive" size="sm">
          <Trash2 className="size-4" />
          {t('oauthClients.delete')}
        </Button>
      }
      title={t('oauthClients.deleteTitle')}
      description={t('oauthClients.confirmDelete')}
      onConfirm={() => mutate()}
    />
  )
}
