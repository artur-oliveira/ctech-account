'use client'

import {useEffect} from 'react'
import {Button, ErrorState} from '@aoctech/ui'
import {reportClientError} from '@/lib/client-logging'
import './globals.css'

// Replaces the root layout, so no providers (i18n, query) exist here: the language comes from the browser.
export default function GlobalError({error, reset}: {error: Error & {digest?: string}; reset: () => void}) {
  const en = typeof navigator !== 'undefined' && navigator.language.startsWith('en')
  useEffect(() => reportClientError('global', error), [error])
  return (
    <html lang={en ? 'en' : 'pt-BR'}>
    <body className="min-h-screen bg-background text-foreground antialiased">
    <main className="min-h-screen flex items-center justify-center">
      <ErrorState status={500} locale={en ? 'en' : 'pt-BR'} action={<Button onClick={reset}>{en ? 'Try again' : 'Tentar novamente'}</Button>}/>
    </main>
    </body>
    </html>
  )
}
