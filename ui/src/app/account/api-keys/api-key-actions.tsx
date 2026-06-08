'use client'

import { useState, useTransition, useActionState, useEffect } from 'react'
import { useFormStatus } from 'react-dom'
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
import { createAPIKey, revokeAPIKey } from '@/lib/actions'
import { toast } from 'sonner'
import { Plus, Copy } from 'lucide-react'

const SCOPES = ['read', 'write', 'admin']

function CreateSubmitButton() {
  const { pending } = useFormStatus()
  return (
    <Button type="submit" disabled={pending}>
      {pending ? 'Creating…' : 'Create key'}
    </Button>
  )
}

export function CreateAPIKeyDialog() {
  const [open, setOpen] = useState(false)
  const [state, action] = useActionState(createAPIKey, null)
  const createdKey = state?.success && state?.key ? (state.key as string) : null

  useEffect(() => {
    if (state?.error) toast.error(state.error)
  }, [state])

  function handleCopy() {
    if (createdKey) {
      navigator.clipboard.writeText(createdKey)
      toast.success('Copied to clipboard.')
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(v) => {
        setOpen(v)
      }}
    >
      <DialogTrigger
        render={
          <Button size="sm">
            <Plus className="size-4" />
            New key
          </Button>
        }
      />
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Create API key</DialogTitle>
          <DialogDescription>
            Give your key a name and select the permissions it needs.
          </DialogDescription>
        </DialogHeader>

        {createdKey ? (
          <div className="space-y-4">
            <Alert>
              <AlertDescription className="text-xs">
                Copy this key now — you won&apos;t be able to see it again.
              </AlertDescription>
            </Alert>
            <div className="flex gap-2">
              <Input readOnly value={createdKey} className="font-mono text-xs" />
              <Button variant="outline" size="icon" onClick={handleCopy}>
                <Copy className="size-4" />
              </Button>
            </div>
            <DialogFooter>
              <Button onClick={() => { setOpen(false) }}>Done</Button>
            </DialogFooter>
          </div>
        ) : (
          <form action={action} className="space-y-4">
            {state?.error && (
              <Alert variant="destructive">
                <AlertDescription>{state.error}</AlertDescription>
              </Alert>
            )}
            <div className="space-y-1.5">
              <Label htmlFor="name">Key name</Label>
              <Input id="name" name="name" required placeholder="e.g. CI/CD pipeline" />
            </div>
            <div className="space-y-2">
              <Label>Scopes</Label>
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
              <Label htmlFor="expires_in_days">Expiry (days, 0 = no expiry)</Label>
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
              <CreateSubmitButton />
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  )
}

export function RevokeAPIKeyButton({ keyId }: { keyId: string }) {
  const [pending, startTransition] = useTransition()

  function handleRevoke() {
    if (!confirm('Revoke this API key? This cannot be undone.')) return
    startTransition(async () => {
      const result = await revokeAPIKey(keyId)
      if (result.error) toast.error(result.error)
      else toast.success('API key revoked.')
    })
  }

  return (
    <Button variant="destructive" size="sm" onClick={handleRevoke} disabled={pending}>
      {pending ? 'Revoking…' : 'Revoke'}
    </Button>
  )
}
