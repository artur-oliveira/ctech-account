import {isAxiosError} from 'axios'

const reportedErrors = new WeakSet<object>()

function markReported(error: unknown): boolean {
  if ((typeof error !== 'object' || error === null) && typeof error !== 'function') return false
  if (reportedErrors.has(error)) return true
  reportedErrors.add(error)
  return false
}

function safePath(rawURL: string | undefined): string | undefined {
  return rawURL?.split('?', 1)[0]
}

/** Logs a rejected API call without exposing request bodies, headers, cookies, or tokens. */
export function reportAPIError(error: unknown): void {
  if (!isAxiosError(error)) {
    reportClientError('api', error)
    return
  }
  if (markReported(error)) return
  console.error('[api] request failed', {
    method: error.config?.method?.toUpperCase(),
    path: safePath(error.config?.url),
    status: error.response?.status,
    request_id: error.response?.headers?.['x-request-id'],
    code: error.code,
    message: error.message,
  })
}

/** Logs browser-only failures in a deliberately sanitized shape. */
export function reportClientError(scope: string, error: unknown): void {
  if (markReported(error)) return
  const safeError = error instanceof Error
    ? {name: error.name, message: error.message}
    : {type: typeof error}
  console.error(`[${scope}] client operation failed`, safeError)
}
