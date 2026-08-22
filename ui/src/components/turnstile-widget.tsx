'use client'

import { useEffect, useRef } from 'react'

const TURNSTILE_SCRIPT_ID = 'cloudflare-turnstile-script'
const TURNSTILE_SCRIPT_URL = 'https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit'

type TurnstileAPI = {
	 render: (element: HTMLElement, options: Record<string, unknown>) => string
	 reset: (widgetId: string) => void
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
  resetSignal = 0,
}: {
  onToken: (token: string) => void
  onError: () => void
  onExpire: () => void
  // Increase after a rejected submit: Turnstile tokens are single-use.
  resetSignal?: number
}) {
  const containerRef = useRef<HTMLDivElement>(null)
  const widgetRef = useRef('')
  const onTokenRef = useRef(onToken)
  const onErrorRef = useRef(onError)
  const onExpireRef = useRef(onExpire)
  const siteKey = process.env.NEXT_PUBLIC_TURNSTILE_SITE_KEY ?? ''

  // Parent forms naturally rerender as their fields change. Keep the provider
  // iframe alive across those renders, while always delivering callbacks to
  // the latest form state.
  useEffect(() => {
    onTokenRef.current = onToken
    onErrorRef.current = onError
    onExpireRef.current = onExpire
  }, [onError, onExpire, onToken])

  useEffect(() => {
    if (!siteKey) {
      onErrorRef.current()
      return undefined
    }

    let cancelled = false
    const render = () => {
      const api = turnstileAPI()
      if (cancelled || !api || !containerRef.current || widgetRef.current) return

      widgetRef.current = api.render(containerRef.current, {
        sitekey: siteKey,
		action: 'support_ticket',
        callback: (token: string) => onTokenRef.current(token),
        'error-callback': () => onErrorRef.current(),
        'expired-callback': () => onExpireRef.current(),
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
      script.addEventListener('error', () => onErrorRef.current(), { once: true })
      document.head.appendChild(script)
    }

    return () => {
      cancelled = true
      if (widgetRef.current && turnstileAPI()) turnstileAPI()?.remove(widgetRef.current)
      widgetRef.current = ''
    }
  }, [siteKey])

	useEffect(() => {
		if (resetSignal > 0 && widgetRef.current && turnstileAPI()) {
			turnstileAPI()?.reset(widgetRef.current)
		}
	}, [resetSignal])

  return <div ref={containerRef} aria-label="Cloudflare Turnstile verification" />
}
