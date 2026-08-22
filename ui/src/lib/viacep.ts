import { VIACEP_API_BASE_URL } from '@/lib/constants'
import type { KYCAddress, ViaCEPResponse } from '@/lib/types'
import { reportClientError } from '@/lib/client-logging'

type ViaCEPAddress = Pick<KYCAddress, 'street' | 'district' | 'city' | 'state'>

export type ViaCEPLookupResult =
  | { status: 'found'; address: ViaCEPAddress }
  | { status: 'not_found' | 'unavailable' }

function isViaCEPResponse(value: unknown): value is ViaCEPResponse {
  return typeof value === 'object' && value !== null
}

/**
 * Looks up a Brazilian CEP to reduce address-entry work. This is deliberately
 * separate from the authenticated account API: ViaCEP is a public assistance
 * service and its result is never trusted in place of KYC API validation.
 */
export async function lookupViaCEP(zipCode: string, signal: AbortSignal): Promise<ViaCEPLookupResult> {
  try {
    const response = await fetch(`${VIACEP_API_BASE_URL}/${zipCode}/json/`, { signal })
    if (!response.ok) return { status: 'unavailable' }

    const data: unknown = await response.json()
    if (!isViaCEPResponse(data)) return { status: 'unavailable' }
    if (data.erro) return { status: 'not_found' }

    return {
      status: 'found',
      address: {
        street: data.logradouro ?? '',
        district: data.bairro ?? '',
        city: data.localidade ?? '',
        state: data.uf ?? '',
      },
    }
  } catch (error) {
    if (error instanceof DOMException && error.name === 'AbortError') throw error
    reportClientError('viacep', error)
    return { status: 'unavailable' }
  }
}
