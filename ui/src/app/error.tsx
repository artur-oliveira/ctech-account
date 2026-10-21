'use client'

import {useEffect} from 'react'
import {useTranslation} from 'react-i18next'
import {Button, ErrorState} from '@aoctech/ui'
import {reportClientError} from '@/lib/client-logging'
import {toLocale} from '@/providers/network-provider'

export default function Error({error, reset}: {error: Error & {digest?: string}; reset: () => void}) {
  const {t, i18n} = useTranslation()
  useEffect(() => reportClientError('route', error), [error])
  return (
    <main className="min-h-screen flex items-center justify-center">
      <ErrorState status={500} locale={toLocale(i18n.language)} action={<Button onClick={reset}>{t('errors.retry')}</Button>}/>
    </main>
  )
}
