import {describe, expect, it} from 'vitest'
import {readClientPrefill} from './prefill'

describe('readClientPrefill', () => {
  it('opens with exactly the given scopes, deduplicated', () => {
    expect(readClientPrefill(new URLSearchParams('new=1&scopes=billing:invoices:read,billing:usage:write,billing:invoices:read')))
      .toEqual({open: true, scopes: ['billing:invoices:read', 'billing:usage:write']})
  })
  it('drops anything that is not shaped like a scope', () => {
    expect(readClientPrefill(new URLSearchParams('new=1&scopes=openid,<script>,billing:products:read')).scopes)
      .toEqual(['billing:products:read'])
  })
  it('keeps the defaults when no usable scope is given', () => {
    expect(readClientPrefill(new URLSearchParams('new=1&scopes=,,'))).toEqual({open: true, scopes: null})
  })
  it('does nothing without new=1', () => {
    expect(readClientPrefill(new URLSearchParams('scopes=billing:invoices:read'))).toEqual({open: false, scopes: null})
  })
  it('caps the list at 32', () => {
    const many = Array.from({length: 40}, (_, i) => `billing:s${i}:read`).join(',')
    expect(readClientPrefill(new URLSearchParams(`new=1&scopes=${many}`)).scopes).toHaveLength(32)
  })
})
