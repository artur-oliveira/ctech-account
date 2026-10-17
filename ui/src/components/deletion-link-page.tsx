'use client'

import Link from 'next/link'
import {useMutation} from '@tanstack/react-query'
import {useTranslation} from 'react-i18next'
import {Alert, AlertDescription} from '@/components/ui/alert'
import {Button} from '@/components/ui/button'
import {Card, CardContent} from '@/components/ui/card'
import {useProductName} from '@/components/leave-products-card'
import {isAxiosError} from '@/lib/axios'
import {formatDate} from '@/lib/format'
import type {DeletionRequest} from '@/lib/types'
import {useAuthStore} from '@/store/auth'

type Props = {
  kind: 'confirm' | 'cancel'
  requestID: string
  token: string
  /** Set when the link is about leaving one product (service unlink): the account stays signed in. */
  service?: string
  action: (requestID: string, token: string) => Promise<DeletionRequest>
}

/**
 * The page an account-deletion e-mail link opens. It acts only when the user
 * presses the button (a POST): mail scanners that prefetch links must never
 * confirm or cancel a deletion.
 */
export function DeletionLinkPage({kind, requestID, token, service, action}: Props) {
  const {t} = useTranslation()
  const productName = useProductName()
  // Copy lives at deletion.<kind>.* for the account and deletion.leave.<kind>.* for one product.
  const ns = service ? `deletion.leave.${kind}` : `deletion.${kind}`
  const product = service ? productName(service) : ''
  const run = useMutation({
    mutationFn: () => action(requestID, token),
    onSuccess: (data) => {
      // Confirming an account deletion revokes every session; leaving one product revokes none.
      // The response says which it was; the URL's `service` only chose the copy before the click.
      if (kind === 'confirm' && data.scope !== 'service') useAuthStore.getState().clearAuth()
    },
  })
  const status = run.error && isAxiosError(run.error) ? (run.error as {response?: {status?: number}}).response?.status : undefined
  const invalid = !requestID || !token || status === 401 || status === 404
  const message = run.isSuccess
    ? kind === 'confirm'
      ? t(`${ns}.done`, {date: formatDate(run.data.grace_until ?? ''), product})
      : t(`${ns}.done`, {product})
    : invalid
      ? t(`${ns}.invalid`, {product})
      : status === 409
        ? t(service ? 'deletion.leave.cancel.tooLate' : 'deletion.cancel.tooLate')
        : run.isError
          ? t('errors.network')
          : ''

  return (
    <div className="min-h-screen flex items-center justify-center bg-muted/40 p-4">
      <div className="w-full max-w-md space-y-6">
        <div className="text-center space-y-1">
          <p className="text-sm font-medium text-muted-foreground">{t('app.name')}</p>
          <h1 className="text-2xl font-semibold tracking-tight">{t(`${ns}.title`, {product})}</h1>
          <p className="text-muted-foreground text-sm">{t(`${ns}.description`, {product})}</p>
        </div>
        <Card>
          <CardContent className="space-y-4">
            {message && (
              <Alert variant={run.isSuccess ? 'default' : 'destructive'}>
                <AlertDescription>{message}</AlertDescription>
              </Alert>
            )}
            {!run.isSuccess && !invalid && (
              <Button className="w-full" variant={kind === 'confirm' ? 'destructive' : 'default'}
                      disabled={run.isPending} onClick={() => run.mutate()}>
                {run.isPending ? t('common.loading') : t(`${ns}.button`, {product})}
              </Button>
            )}
            <p className="text-center text-sm text-muted-foreground">
              <Link href="/login" className="underline underline-offset-4 hover:text-foreground">{t('deletion.goToLogin')}</Link>
            </p>
          </CardContent>
        </Card>
      </div>
    </div>
  )
}
