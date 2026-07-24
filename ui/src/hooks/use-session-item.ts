'use client'

import { useSyncExternalStore } from 'react'

// sessionStorage is written before navigation and never mutated while the page
// is mounted, so there is nothing to subscribe to.
const noopSubscribe = () => () => {}

/**
 * Reads a sessionStorage key without breaking hydration.
 *
 * Returns null during the prerender and the hydrating render (there is no
 * sessionStorage on the server), then the real value — '' when the key is
 * absent. Callers must treat null as "not read yet" and render the same markup
 * the server produced.
 */
export function useSessionItem(key: string): string | null {
  return useSyncExternalStore(
    noopSubscribe,
    () => sessionStorage.getItem(key) ?? '',
    () => null,
  )
}
