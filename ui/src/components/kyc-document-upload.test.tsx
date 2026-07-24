import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { describe, expect, it, vi, beforeEach } from 'vitest'
import { toast } from 'sonner'
import { KYCDocumentUpload } from './kyc-document-upload'
import { uploadKYCDocumentAPI } from '@/lib/mutations'
import type { KYCStatus } from '@/lib/types'

vi.mock('@/lib/mutations', () => ({
  uploadKYCDocumentAPI: vi.fn(),
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn() },
}))

function renderUpload(uploadedTypes: KYCStatus['documents'] extends undefined ? never[] : never) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <KYCDocumentUpload uploadedTypes={uploadedTypes} />
    </QueryClientProvider>,
  )
}

const FAKE_STATUS = { state: 'awaiting_files', level: 'basic' } as unknown as KYCStatus

describe('KYCDocumentUpload', () => {
  beforeEach(() => {
    vi.mocked(uploadKYCDocumentAPI).mockReset()
  })

  it('shows the plain upload-success toast for a document type not yet uploaded', async () => {
    const user = userEvent.setup()
    vi.mocked(uploadKYCDocumentAPI).mockResolvedValue(FAKE_STATUS)
    renderUpload([] as never)

    const file = new File(['x'], 'id.jpg', { type: 'image/jpeg' })
    const input = screen.getByLabelText(/ID.*front/i)
    await user.upload(input, file)
    await user.click(screen.getByRole('button', { name: /upload this photo/i }))

    await waitFor(() => expect(toast.success).toHaveBeenCalledWith('Document uploaded.'))
    expect(toast.success).not.toHaveBeenCalledWith('Document replaced.')
  })

  it('shows the distinct replaced toast when re-uploading an already-uploaded type', async () => {
    const user = userEvent.setup()
    vi.mocked(uploadKYCDocumentAPI).mockResolvedValue(FAKE_STATUS)
    // id_front is the default selected document_type — pre-mark it uploaded.
    renderUpload(['id_front'] as never)

    const file = new File(['x'], 'id.jpg', { type: 'image/jpeg' })
    const input = screen.getByLabelText(/ID.*front/i)
    await user.upload(input, file)
    await user.click(screen.getByRole('button', { name: /upload this photo/i }))

    await waitFor(() => expect(toast.success).toHaveBeenCalledWith('Document replaced.'))
  })

  it('captures the replacing flag at confirm time, not from state read after the mutation settles', async () => {
    const user = userEvent.setup()
    let resolveUpload!: (status: KYCStatus) => void
    vi.mocked(uploadKYCDocumentAPI).mockImplementation(
      () => new Promise((resolve) => { resolveUpload = resolve }),
    )
    renderUpload(['id_front'] as never)

    const file = new File(['x'], 'id.jpg', { type: 'image/jpeg' })
    const input = screen.getByLabelText(/ID.*front/i)
    await user.upload(input, file)
    await user.click(screen.getByRole('button', { name: /upload this photo/i }))

    // Regression: wasReplacingRef must be captured before the mutation settles —
    // reading `isReplacing` inside onSuccess would see whatever `uploadedTypes`
    // is by then, which can differ from what it was at confirm time.
    resolveUpload(FAKE_STATUS)
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith('Document replaced.'))
  })
})
