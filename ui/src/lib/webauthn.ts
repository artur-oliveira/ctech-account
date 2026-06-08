export function base64urlToArrayBuffer(base64url: string): ArrayBuffer {
  const base64 = base64url.replace(/-/g, '+').replace(/_/g, '/')
  const padded = base64.padEnd(base64.length + ((4 - (base64.length % 4)) % 4), '=')
  const binary = atob(padded)
  const bytes = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i)
  return bytes.buffer
}

export function arrayBufferToBase64url(arr: ArrayBuffer): string {
  return btoa(String.fromCharCode(...new Uint8Array(arr)))
    .replace(/\+/g, '-')
    .replace(/\//g, '_')
    .replace(/=/g, '')
}

export async function buildRegistrationCredential(optionsJSON: string) {
  const pk = JSON.parse(optionsJSON).publicKey as {
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
  return {
    id: credential.id,
    rawId: arrayBufferToBase64url(credential.rawId),
    type: credential.type,
    response: {
      attestationObject: arrayBufferToBase64url(response.attestationObject),
      clientDataJSON: arrayBufferToBase64url(response.clientDataJSON),
    },
  }
}

export async function buildAssertionCredential(optionsJSON: string) {
  const pk = JSON.parse(optionsJSON).publicKey as {
    challenge: string
    timeout?: number
    rpId?: string
    allowCredentials?: Array<{ id: string; type: string; transports?: string[] }>
    userVerification?: UserVerificationRequirement
  }

  const getOptions: CredentialRequestOptions = {
    publicKey: {
      ...pk,
      challenge: base64urlToArrayBuffer(pk.challenge),
      allowCredentials: pk.allowCredentials?.map((c) => ({
        id: base64urlToArrayBuffer(c.id),
        type: c.type as PublicKeyCredentialType,
        transports: c.transports as AuthenticatorTransport[] | undefined,
      })),
    },
  }

  const credential = (await navigator.credentials.get(getOptions)) as PublicKeyCredential
  if (!credential) throw new Error('No credential returned')

  const response = credential.response as AuthenticatorAssertionResponse
  return {
    id: credential.id,
    rawId: arrayBufferToBase64url(credential.rawId),
    type: credential.type,
    response: {
      authenticatorData: arrayBufferToBase64url(response.authenticatorData),
      clientDataJSON: arrayBufferToBase64url(response.clientDataJSON),
      signature: arrayBufferToBase64url(response.signature),
      userHandle: response.userHandle ? arrayBufferToBase64url(response.userHandle) : null,
    },
  }
}
