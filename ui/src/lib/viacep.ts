import type { Address } from './types'
import { ZIP_CODE_DIGITS } from './constants'

const VIACEP_URL = 'https://viacep.com.br/ws'

/** Shape ViaCEP answers with. `erro` is present (and true) for unknown CEPs. */
interface ViaCEPResponse {
  erro?: boolean | string
  logradouro?: string
  bairro?: string
  localidade?: string
  uf?: string
}

/** Strips everything but digits and caps at the CEP length. */
export function normalizeZipCode(value: string): string {
  return value.replace(/\D/g, '').slice(0, ZIP_CODE_DIGITS)
}

/** Renders digits as 00000-000 while typing. */
export function formatZipCodeInput(value: string): string {
  return normalizeZipCode(value).replace(/(\d{5})(\d)/, '$1-$2')
}

/**
 * Looks a CEP up on ViaCEP (public API, called straight from the browser).
 * Returns null when the CEP is unknown or the service is unreachable — the
 * caller keeps the address fields editable either way.
 */
export async function lookupZipCode(zipCode: string): Promise<Partial<Address> | null> {
  const digits = normalizeZipCode(zipCode)
  if (digits.length !== ZIP_CODE_DIGITS) return null

  try {
    const res = await fetch(`${VIACEP_URL}/${digits}/json/`)
    if (!res.ok) return null

    const data: ViaCEPResponse = await res.json()
    if (data.erro) return null

    return {
      zip_code: digits,
      street: data.logradouro ?? '',
      district: data.bairro ?? '',
      city: data.localidade ?? '',
      state: data.uf ?? '',
    }
  } catch {
    return null
  }
}
