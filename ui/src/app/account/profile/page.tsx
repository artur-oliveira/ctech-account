'use client'

import { useActionState, useEffect } from 'react'
import { useFormStatus } from 'react-dom'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Separator } from '@/components/ui/separator'
import { updateProfile, changePassword } from '@/lib/actions'
import { toast } from 'sonner'

function SubmitButton({ label, pendingLabel }: { label: string; pendingLabel: string }) {
  const { pending } = useFormStatus()
  return <Button type="submit" disabled={pending}>{pending ? pendingLabel : label}</Button>
}

function ProfileForm() {
  const [state, action] = useActionState(updateProfile, null)

  useEffect(() => {
    if (state?.success) toast.success('Profile updated.')
    if (state?.error) toast.error(state.error)
  }, [state])

  return (
    <form action={action} className="space-y-4">
      {state?.error && (
        <Alert variant="destructive">
          <AlertDescription>{state.error}</AlertDescription>
        </Alert>
      )}
      <div className="grid grid-cols-2 gap-4">
        <div className="space-y-1.5">
          <Label htmlFor="first_name">First name</Label>
          <Input id="first_name" name="first_name" required />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="last_name">Last name</Label>
          <Input id="last_name" name="last_name" />
        </div>
      </div>
      <div className="space-y-1.5">
        <Label htmlFor="display_name">Display name</Label>
        <Input id="display_name" name="display_name" placeholder="Optional" />
      </div>
      <SubmitButton label="Save changes" pendingLabel="Saving…" />
    </form>
  )
}

function PasswordForm() {
  const [state, action] = useActionState(changePassword, null)

  useEffect(() => {
    if (state?.success) toast.success('Password changed.')
    if (state?.error) toast.error(state.error)
  }, [state])

  return (
    <form action={action} className="space-y-4">
      {state?.error && (
        <Alert variant="destructive">
          <AlertDescription>{state.error}</AlertDescription>
        </Alert>
      )}
      <div className="space-y-1.5">
        <Label htmlFor="current_password">Current password</Label>
        <Input id="current_password" name="current_password" type="password" required autoComplete="current-password" />
      </div>
      <div className="space-y-1.5">
        <Label htmlFor="new_password">New password</Label>
        <Input id="new_password" name="new_password" type="password" required minLength={8} autoComplete="new-password" />
      </div>
      <SubmitButton label="Change password" pendingLabel="Changing…" />
    </form>
  )
}

export default function ProfilePage() {
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold">Profile</h1>
        <p className="text-muted-foreground text-sm mt-1">Manage your personal information.</p>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Personal information</CardTitle>
          <CardDescription>Update your name and display name.</CardDescription>
        </CardHeader>
        <CardContent>
          <ProfileForm />
        </CardContent>
      </Card>

      <Separator />

      <Card>
        <CardHeader>
          <CardTitle>Password</CardTitle>
          <CardDescription>Change your account password.</CardDescription>
        </CardHeader>
        <CardContent>
          <PasswordForm />
        </CardContent>
      </Card>
    </div>
  )
}
