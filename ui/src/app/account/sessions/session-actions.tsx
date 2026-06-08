'use client'

import { useTransition } from 'react'
import { Button } from '@/components/ui/button'
import { revokeSession, revokeAllSessions } from '@/lib/actions'
import { toast } from 'sonner'

export function RevokeSessionButton({ sessionId }: { sessionId: string }) {
  const [pending, startTransition] = useTransition()

  function handleRevoke() {
    startTransition(async () => {
      const result = await revokeSession(sessionId)
      if (result.error) toast.error(result.error)
      else toast.success('Session revoked.')
    })
  }

  return (
    <Button variant="outline" size="sm" onClick={handleRevoke} disabled={pending}>
      {pending ? 'Revoking…' : 'Revoke'}
    </Button>
  )
}

export function RevokeAllButton() {
  const [pending, startTransition] = useTransition()

  function handleRevokeAll() {
    startTransition(async () => {
      const result = await revokeAllSessions()
      if (result.error) toast.error(result.error)
      else toast.success('All other sessions revoked.')
    })
  }

  return (
    <Button variant="destructive" size="sm" onClick={handleRevokeAll} disabled={pending}>
      {pending ? 'Revoking…' : 'Revoke all others'}
    </Button>
  )
}
