'use client'

import { useTransition } from 'react'
import { Button } from '@/components/ui/button'
import { removeTOTP } from '@/lib/actions'
import { toast } from 'sonner'

export function RemoveTOTPButton() {
  const [pending, startTransition] = useTransition()

  function handleRemove() {
    if (!confirm('Remove TOTP authentication? Your account will be less secure.')) return
    startTransition(async () => {
      const result = await removeTOTP()
      if (result.error) toast.error(result.error)
      else toast.success('TOTP removed.')
    })
  }

  return (
    <Button variant="destructive" size="sm" onClick={handleRemove} disabled={pending}>
      {pending ? 'Removing…' : 'Remove'}
    </Button>
  )
}
