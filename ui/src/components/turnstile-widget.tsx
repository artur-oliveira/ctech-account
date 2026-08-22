'use client'

import { useEffect, useRef } from 'react'

const TURNSTILE_SCRIPT_ID = 'cloudflare-turnstile-script'
const TURNSTILE_SCRIPT_URL = 'https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit'

type TurnstileAPI = {
  render: (element: HTMLElement, options: Record<string, unknown>) => string
  remove: (widgetId: string) => void
}

function turnstileAPI() {
  return (window as typeof window & { turnstile?: TurnstileAPI }).turnstile
}

// TurnstileWidget loads the provider script once and yields each short-lived
// token to its form. The token is intentionally held only by the consuming
// component until it submits its request.
export function TurnstileWidget({
  onToken,
  onError,
  onExpire,
}: {
  onToken: (token: string) => void
  onError: () => void
  onExpire: () => void
}) {
  const containerRef = useRef<HTMLDivElement>(null)
  const widgetRef = useRef('')
  const siteKey = process.env.NEXT_PUBLIC_TURNSTILE_SITE_KEY ?? ''

  useEffect(() => {
    if (!siteKey) {
      onError()
      return undefined
    }

    let cancelled = false
    const render = () => {
      const api = turnstileAPI()
      if (cancelled || !api || !containerRef.current || widgetRef.current) return

      widgetRef.current = api.render(containerRef.current, {
        sitekey: siteKey,
        callback: onToken,
        'error-callback': onError,
        'expired-callback': onExpire,
      })
    }
    const existing = document.getElementById(TURNSTILE_SCRIPT_ID) as HTMLScriptElement | null
    if (existing) {
      if (turnstileAPI()) render()
      else existing.addEventListener('load', render, { once: true })
    } else {
      const script = document.createElement('script')
      script.id = TURNSTILE_SCRIPT_ID
      script.src = TURNSTILE_SCRIPT_URL
      script.async = true
      script.defer = true
      script.addEventListener('load', render, { once: true })
      script.addEventListener('error', onError, { once: true })
      document.head.appendChild(script)
    }

    return () => {
      cancelled = true
      if (widgetRef.current && turnstileAPI()) turnstileAPI()?.remove(widgetRef.current)
      widgetRef.current = ''
    }
  }, [onError, onExpire, onToken, siteKey])

  return <div ref={containerRef} aria-label="Cloudflare Turnstile verification" />
}
