'use client'

import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { removePasskeyAPI, beginPasskeyRegistrationAPI, completePasskeyRegistrationAPI } from '@/lib/mutations'
import { isAxiosError } from '@/lib/axios'
import { toast } from 'sonner'
import { Plus } from 'lucide-react'

function base64urlToArrayBuffer(base64url: string): ArrayBuffer {
  const base64 = base64url.replace(/-/g, '+').replace(/_/g, '/')
  const padded = base64.padEnd(base64.length + ((4 - (base64.length % 4)) % 4), '=')
  const binary = atob(padded)
  const bytes = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i)
  return bytes.buffer
}

function arrayBufferToBase64url(arr: ArrayBuffer): string {
  return btoa(String.fromCharCode(...new Uint8Array(arr)))
    .replace(/\+/g, '-')
    .replace(/\//g, '_')
    .replace(/=/g, '')
}

export function RegisterPasskeyButton() {
  const { t } = useTranslation()
  const [pending, setPending] = useState(false)
  const queryClient = useQueryClient()

  async function handleRegister() {
    setPending(true)
    try {
      const options = await beginPasskeyRegistrationAPI()

      const pk = options.publicKey as {
        challenge: string
        rp: PublicKeyCredentialRpEntity
        user: { id: string; name: string; displayName: string }
        pubKeyCredParams: PublicKeyCredentialParameters[]
        timeout?: number
        attestation?: AttestationConveyancePreference
        authenticatorSelection?: AuthenticatorSelectionCriteria
        excludeCredentials?: Array<{ id: string; type: string; transports?: string[] }>
      }

      const createOptions: CredentialCreationOptions = {
        publicKey: {
          ...pk,
          challenge: base64urlToArrayBuffer(pk.challenge),
          user: { ...pk.user, id: base64urlToArrayBuffer(pk.user.id) },
          excludeCredentials: pk.excludeCredentials?.map((c) => ({
            id: base64urlToArrayBuffer(c.id),
            type: c.type as PublicKeyCredentialType,
            transports: c.transports as AuthenticatorTransport[] | undefined,
          })),
        },
      }

      const credential = (await navigator.credentials.create(createOptions)) as PublicKeyCredential
      if (!credential) throw new Error('No credential returned')

      const response = credential.response as AuthenticatorAttestationResponse
      await completePasskeyRegistrationAPI({
        id: credential.id,
        rawId: arrayBufferToBase64url(credential.rawId),
        type: credential.type,
        response: {
          attestationObject: arrayBufferToBase64url(response.attestationObject),
          clientDataJSON: arrayBufferToBase64url(response.clientDataJSON),
        },
      })

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
