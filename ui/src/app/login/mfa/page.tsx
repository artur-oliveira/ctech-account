'use client'

import { Suspense, useState, FormEvent } from 'react'
import { useRouter, useSearchParams } from 'next/navigation'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { toast } from 'sonner'

function MFAForm() {
  const router = useRouter()
  const params = useSearchParams()
  const continueURL = params.get('continue') ?? '/account'

  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  async function handleSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    setError('')
    setLoading(true)
    const fd = new FormData(e.currentTarget)

    try {
      const res = await fetch('/api/auth/mfa', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          code: fd.get('code'),
          continue_url: continueURL,
        }),
      })

      const data = await res.json()

      if (!res.ok) {
        setError(data.detail ?? 'MFA verification failed.')
        return
      }

      toast.success('Logged in successfully.')
      router.push(data.redirect ?? '/account')
    } catch {
      setError('Network error. Please try again.')
    } finally {
      setLoading(false)
    }
  }

  return (
    <form onSubmit={handleSubmit} className="space-y-4">
      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      <div className="space-y-1.5">
        <Label htmlFor="code">Verification code</Label>
        <Input
          id="code"
          name="code"
          type="text"
          inputMode="numeric"
          pattern="[0-9]{6}"
          maxLength={6}
          autoComplete="one-time-code"
          placeholder="000000"
          required
        />
      </div>

      <Button type="submit" className="w-full" disabled={loading}>
        {loading ? 'Verifying…' : 'Verify'}
      </Button>
    </form>
  )
}

export default function MFAPage() {
  return (
    <div className="min-h-screen flex items-center justify-center bg-muted/40 p-4">
      <div className="w-full max-w-md space-y-6">
        <div className="text-center">
          <h1 className="text-2xl font-bold tracking-tight">arturocarvalho.com</h1>
          <p className="text-muted-foreground text-sm mt-1">Account portal</p>
        </div>

        <Card>
          <CardHeader>
            <CardTitle>Two-factor authentication</CardTitle>
            <CardDescription>Enter the 6-digit code from your authenticator app.</CardDescription>
          </CardHeader>
          <CardContent>
            <Suspense fallback={<div className="h-32 animate-pulse bg-muted rounded" />}>
              <MFAForm />
            </Suspense>
          </CardContent>
        </Card>
      </div>
    </div>
  )
}
