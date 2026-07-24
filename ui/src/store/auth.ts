import { create } from 'zustand'

interface AuthState {
  accessToken: string | null
  isInitialized: boolean
  setAccessToken: (token: string) => void
  clearAuth: () => void
  setInitialized: () => void
}

export const useAuthStore = create<AuthState>((set) => ({
  accessToken: null,
  isInitialized: false,
  setAccessToken: (token) => set({ accessToken: token }),
  clearAuth: () => set({ accessToken: null }),
  setInitialized: () => set({ isInitialized: true }),
}))
