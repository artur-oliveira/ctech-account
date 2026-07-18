import { create } from 'zustand'

/**
 * Bridges the axios interceptor and the step-up dialog: a 403
 * step-up-required response calls request(), which opens the dialog and
 * returns a promise that settles when the user completes (or abandons)
 * the challenge. On success the interceptor silent-refreshes and retries
 * the original request.
 */
interface StepUpState {
  open: boolean
  enrollmentRequired: boolean
  pending: { resolve: () => void; reject: (err: unknown) => void } | null
  request: () => Promise<void>
  requireEnrollment: () => void
  succeed: () => void
  cancel: () => void
}

const ABORT_MESSAGE = 'step-up cancelled'

export const useStepUpStore = create<StepUpState>((set, get) => ({
  open: false,
  enrollmentRequired: false,
  pending: null,
  request: () =>
    new Promise<void>((resolve, reject) => {
      // A challenge is already open — fail the new request; the visible
      // dialog's retry covers the first one.
      if (get().pending) {
        reject(new Error(ABORT_MESSAGE))
        return
      }
      set({ open: true, enrollmentRequired: false, pending: { resolve, reject } })
    }),
  requireEnrollment: () => set({ enrollmentRequired: true }),
  succeed: () => {
    get().pending?.resolve()
    set({ open: false, enrollmentRequired: false, pending: null })
  },
  cancel: () => {
    get().pending?.reject(new Error(ABORT_MESSAGE))
    set({ open: false, enrollmentRequired: false, pending: null })
  },
}))
