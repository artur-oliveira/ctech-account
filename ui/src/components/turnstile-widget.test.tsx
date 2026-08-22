import { act, fireEvent, render } from '@testing-library/react'
import { afterEach, describe, expect, test, vi } from 'vitest'
import { TurnstileWidget } from './turnstile-widget'

type TurnstileOptions = {
  sitekey: string
  callback: (token: string) => void
  'error-callback': () => void
  'expired-callback': () => void
}

afterEach(() => {
  vi.unstubAllEnvs()
  document.getElementById('cloudflare-turnstile-script')?.remove()
  delete (window as typeof window & { turnstile?: unknown }).turnstile
})

describe('TurnstileWidget', () => {
  test('loads the Cloudflare script and forwards lifecycle callbacks', () => {
    vi.stubEnv('NEXT_PUBLIC_TURNSTILE_SITE_KEY', 'site-key')
    let options: TurnstileOptions | undefined
    const remove = vi.fn()
    const renderWidget = vi.fn((_element: HTMLElement, received: TurnstileOptions) => {
      options = received
      return 'widget-1'
    })
    ;(window as typeof window & { turnstile: unknown }).turnstile = { render: renderWidget, remove }
    const onToken = vi.fn()
    const onError = vi.fn()
    const onExpire = vi.fn()

    const view = render(<TurnstileWidget onToken={onToken} onError={onError} onExpire={onExpire} />)
    const script = document.getElementById('cloudflare-turnstile-script') as HTMLScriptElement
    expect(script.src).toContain('https://challenges.cloudflare.com/turnstile/v0/api.js')
    fireEvent.load(script)

    expect(renderWidget).toHaveBeenCalledOnce()
    expect(options?.sitekey).toBe('site-key')
    act(() => options?.callback('turnstile-token'))
    act(() => options?.['error-callback']())
    act(() => options?.['expired-callback']())
    expect(onToken).toHaveBeenCalledWith('turnstile-token')
    expect(onError).toHaveBeenCalledOnce()
    expect(onExpire).toHaveBeenCalledOnce()

    view.unmount()
    expect(remove).toHaveBeenCalledWith('widget-1')
  })

  test('reports a missing site key without loading Cloudflare', () => {
    vi.stubEnv('NEXT_PUBLIC_TURNSTILE_SITE_KEY', '')
    const onError = vi.fn()

    render(<TurnstileWidget onToken={vi.fn()} onError={onError} onExpire={vi.fn()} />)

    expect(onError).toHaveBeenCalledOnce()
    expect(document.getElementById('cloudflare-turnstile-script')).toBeNull()
  })

  test('does not recreate the provider iframe when parent callbacks change', () => {
    vi.stubEnv('NEXT_PUBLIC_TURNSTILE_SITE_KEY', 'site-key')
    const remove = vi.fn()
    const renderWidget = vi.fn(() => 'widget-1')
    ;(window as typeof window & { turnstile: unknown }).turnstile = { render: renderWidget, remove }

    const view = render(<TurnstileWidget onToken={vi.fn()} onError={vi.fn()} onExpire={vi.fn()} />)
    fireEvent.load(document.getElementById('cloudflare-turnstile-script')!)
    view.rerender(<TurnstileWidget onToken={vi.fn()} onError={vi.fn()} onExpire={vi.fn()} />)

    expect(renderWidget).toHaveBeenCalledOnce()
    expect(remove).not.toHaveBeenCalled()
  })
})
