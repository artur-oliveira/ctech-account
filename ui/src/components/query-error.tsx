'use client'

import { useTranslation } from 'react-i18next'
import { AlertTriangle } from 'lucide-react'
import { isAxiosError } from '@/lib/axios'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'

type QueryErrorProps = {
  error: unknown
  onRetry: () => void
  className?: string
}

/**
 * Shared error surface for react-query data pages. Without this, a failed
 * fetch falls through to `data = []` and silently renders the empty state.
 * Resolves the server's RFC 7807 `detail` when present, otherwise a generic
 * message, and offers a single retry.
 */
export function QueryError({ error, onRetry, className }: QueryErrorProps) {
  const { t } = useTranslation()
  const message = isAxiosError(error)
    ? (error.response?.data?.detail ?? t('errors.network'))
    : t('errors.loadFailed')

  return (
    <Alert variant="destructive" className={className}>
      <AlertDescription className="flex items-center gap-3">
        <AlertTriangle className="size-4 shrink-0" />
        <span className="flex-1 min-w-0">{message}</span>
        <Button type="button" size="sm" variant="outline" onClick={onRetry} className="shrink-0">
          {t('errors.retry')}
        </Button>
      </AlertDescription>
    </Alert>
  )
}
