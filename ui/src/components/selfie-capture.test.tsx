import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { SelfieCapture } from './selfie-capture'

class FakeMediaRecorder {
  static isTypeSupported = vi.fn().mockReturnValue(true)
  ondataavailable: ((e: { data: Blob }) => void) | null = null
  onstop: (() => void) | null = null
  mimeType: string
  constructor(_stream: MediaStream, opts?: { mimeType?: string }) {
    this.mimeType = opts?.mimeType ?? 'video/webm'
    instances.push(this)
  }
  start = vi.fn()
  stop = vi.fn()
}

let instances: FakeMediaRecorder[] = []
const stopTrack = vi.fn()

function renderCapture() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <SelfieCapture uploadedTypes={[]} />
    </QueryClientProvider>,
  )
}

async function consentAndOpenCamera(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole('button', { name: 'Allow camera & start' }))
  await waitFor(() => expect(navigator.mediaDevices.getUserMedia).toHaveBeenCalled())
  await screen.findByRole('button', { name: 'Record' })
}

beforeEach(() => {
  instances = []
  stopTrack.mockClear()

  vi.stubGlobal('MediaRecorder', FakeMediaRecorder as unknown as typeof MediaRecorder)
  vi.stubGlobal('URL', { ...URL, createObjectURL: vi.fn(() => 'blob:fake'), revokeObjectURL: vi.fn() })
  vi.stubGlobal('matchMedia', vi.fn().mockReturnValue({ matches: true }))
  // jsdom has no Web Animations API.
  HTMLElement.prototype.getAnimations = vi.fn().mockReturnValue([])
  HTMLElement.prototype.animate = vi.fn().mockReturnValue({ cancel: vi.fn() }) as never

  vi.stubGlobal('navigator', {
    ...navigator,
    mediaDevices: {
      getUserMedia: vi.fn().mockResolvedValue({
        getTracks: () => [{ stop: stopTrack }],
      }),
    },
  })
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('SelfieCapture — MediaRecorder blob-leak guard', () => {
  it('creates a preview blob when onstop fires while still mounted', async () => {
    const user = userEvent.setup()
    renderCapture()
    await consentAndOpenCamera(user)

    await user.click(screen.getByRole('button', { name: 'Record' }))
    instances[0].onstop?.()

    expect(URL.createObjectURL).toHaveBeenCalledTimes(1)
    expect(await screen.findByText('Review your clip before sending')).toBeInTheDocument()
  })

  it('does not create a blob URL when onstop fires after the component unmounted', async () => {
    const user = userEvent.setup()
    const { unmount } = renderCapture()
    await consentAndOpenCamera(user)

    await user.click(screen.getByRole('button', { name: 'Record' }))
    const recorder = instances[0]

    // The browser can still fire onstop after cleanup already stopped the
    // tracks (unmount, pose switch, retry) — recordingActiveRef must gate it.
    unmount()
    expect(stopTrack).toHaveBeenCalled()

    recorder.onstop?.()

    expect(URL.createObjectURL).not.toHaveBeenCalled()
  })
})
