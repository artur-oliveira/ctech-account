'use client'

import { useTransition } from 'react'
import { Button } from '@/components/ui/button'
import { removePasskey, beginPasskeyRegistration, completePasskeyRegistration } from '@/lib/actions'
import { toast } from 'sonner'
import { Plus } from 'lucide-react'

function base64urlToUint8Array(base64url: string): ArrayBuffer {
  const base64 = base64url.replace(/-/g, '+').replace(/_/g, '/')
  const padded = base64.padEnd(base64.length + ((4 - (base64.length % 4)) % 4), '=')
  const binary = atob(padded)
  const bytes = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i++) {
    bytes[i] = binary.charCodeAt(i)
  }
  return bytes.buffer
}

function uint8ArrayToBase64url(arr: ArrayBuffer): string {
  return btoa(String.fromCharCode(...new Uint8Array(arr)))
    .replace(/\+/g, '-')
    .replace(/\//g, '_')
    .replace(/=/g, '')
}

export function RegisterPasskeyButton() {
  const [pending, startTransition] = useTransition()

  function handleRegister() {
    startTransition(async () => {
      const begin = await beginPasskeyRegistration()
      if (begin.error || !begin.options) {
        toast.error(begin.error ?? 'Failed to start passkey registration.')
        return
      }

      const opts = begin.options as {
        publicKey: {
          challenge: string
          rp: PublicKeyCredentialRpEntity
          user: { id: string; name: string; displayName: string }
          pubKeyCredParams: PublicKeyCredentialParameters[]
          timeout?: number
          attestation?: AttestationConveyancePreference
          authenticatorSelection?: AuthenticatorSelectionCriteria
          excludeCredentials?: Array<{ id: string; type: string; transports?: string[] }>
        }
      }

      const pk = opts.publicKey
      const createOptions: CredentialCreationOptions = {
        publicKey: {
          ...pk,
          challenge: base64urlToUint8Array(pk.challenge),
          user: {
            ...pk.user,
            id: base64urlToUint8Array(pk.user.id),
          },
          excludeCredentials: pk.excludeCredentials?.map((c) => ({
            id: base64urlToUint8Array(c.id),
            type: c.type as PublicKeyCredentialType,
            transports: c.transports as AuthenticatorTransport[] | undefined,
          })),
        },
      }

      let credential: PublicKeyCredential
      try {
        credential = (await navigator.credentials.create(createOptions)) as PublicKeyCredential
        if (!credential) throw new Error('No credential returned')
      } catch {
        toast.error('Passkey creation cancelled or failed.')
        return
      }

      const response = credential.response as AuthenticatorAttestationResponse
      const credentialJSON = {
        id: credential.id,
        rawId: uint8ArrayToBase64url(credential.rawId),
        type: credential.type,
        response: {
          attestationObject: uint8ArrayToBase64url(response.attestationObject),
          clientDataJSON: uint8ArrayToBase64url(response.clientDataJSON),
        },
      }

      const result = await completePasskeyRegistration(credentialJSON)
      if (result.error) toast.error(result.error)
      else toast.success('Passkey registered successfully.')
    })
  }

  return (
    <Button size="sm" onClick={handleRegister} disabled={pending}>
      <Plus className="size-4" />
      {pending ? 'Registering…' : 'Add passkey'}
    </Button>
  )
}

export function RemovePasskeyButton({ passkeyId }: { passkeyId: string }) {
  const [pending, startTransition] = useTransition()

  function handleRemove() {
    if (!confirm('Remove this passkey?')) return
    startTransition(async () => {
      const result = await removePasskey(passkeyId)
      if (result.error) toast.error(result.error)
      else toast.success('Passkey removed.')
    })
  }

  return (
    <Button variant="destructive" size="sm" onClick={handleRemove} disabled={pending}>
      {pending ? 'Removing…' : 'Remove'}
    </Button>
  )
}
