'use client'

import {SyntheticEvent} from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Separator } from '@/components/ui/separator'
import { updateProfileAPI, changePasswordAPI } from '@/lib/mutations'
import { fetchProfile } from '@/lib/queries'
import { isAxiosError } from '@/lib/axios'
import { toast } from 'sonner'

function ProfileForm() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const { data: profile } = useQuery({ queryKey: ['profile'], queryFn: fetchProfile })
  const { mutate: update, isPending, error } = useMutation({
    mutationFn: updateProfileAPI,
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['profile'] })
      toast.success(t('toast.profileUpdated'))
    },
    onError: (err) => {
      if (isAxiosError(err)) toast.error(err.response?.data?.detail ?? t('errors.updateFailed'))
    },
  })

  function handleSubmit(e: SyntheticEvent<HTMLFormElement>) {
    e.preventDefault()
    const fd = new FormData(e.currentTarget)
    update({
      first_name: fd.get('first_name') as string,
      last_name: (fd.get('last_name') as string) || '',
      display_name: (fd.get('display_name') as string) || undefined,
    })
  }

  const errorMsg = isAxiosError(error) ? (error.response?.data?.detail ?? t('errors.updateFailed')) : null

  return (
    <form key={profile?.user_id} onSubmit={handleSubmit} className="space-y-4">
      {errorMsg && (
        <Alert variant="destructive">
          <AlertDescription>{errorMsg}</AlertDescription>
        </Alert>
      )}
      <div className="grid grid-cols-2 gap-4">
        <div className="space-y-1.5">
          <Label htmlFor="first_name">{t('profile.firstName')}</Label>
          <Input id="first_name" name="first_name" required defaultValue={profile?.first_name ?? ''} />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="last_name">{t('profile.lastName')}</Label>
          <Input id="last_name" name="last_name" defaultValue={profile?.last_name ?? ''} />
        </div>
      </div>
      <div className="space-y-1.5">
        <Label htmlFor="display_name">{t('profile.displayName')}</Label>
        <Input id="display_name" name="display_name" placeholder={t('profile.displayNamePlaceholder')} defaultValue={profile?.display_name ?? ''} />
      </div>
      <Button type="submit" disabled={isPending}>{isPending ? t('profile.saving') : t('profile.save')}</Button>
    </form>
  )
}

function PasswordForm() {
  const { t } = useTranslation()
  const { mutate: changePassword, isPending, error } = useMutation({
    mutationFn: changePasswordAPI,
    onSuccess: () => toast.success(t('toast.passwordChanged')),
    onError: (err) => {
      if (isAxiosError(err)) toast.error(err.response?.data?.detail ?? t('errors.passwordChangeFailed'))
    },
  })

  function handleSubmit(e: SyntheticEvent<HTMLFormElement>) {
    e.preventDefault()
    const fd = new FormData(e.currentTarget)
    changePassword({
      current_password: fd.get('current_password') as string,
      new_password: fd.get('new_password') as string,
    })
  }

  const errorMsg = isAxiosError(error) ? (error.response?.data?.detail ?? t('errors.passwordChangeFailed')) : null

  return (
    <form onSubmit={handleSubmit} className="space-y-4">
      {errorMsg && (
        <Alert variant="destructive">
          <AlertDescription>{errorMsg}</AlertDescription>
        </Alert>
      )}
      <div className="space-y-1.5">
        <Label htmlFor="current_password">{t('profile.currentPassword')}</Label>
        <Input id="current_password" name="current_password" type="password" required autoComplete="current-password" />
      </div>
      <div className="space-y-1.5">
        <Label htmlFor="new_password">{t('profile.newPassword')}</Label>
        <Input id="new_password" name="new_password" type="password" required minLength={8} autoComplete="new-password" />
      </div>
      <Button type="submit" disabled={isPending}>{isPending ? t('profile.changing') : t('profile.changePassword')}</Button>
    </form>
  )
}

export default function ProfilePage() {
  const { t } = useTranslation()
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold">{t('profile.title')}</h1>
        <p className="text-muted-foreground text-sm mt-1">{t('profile.subtitle')}</p>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>{t('profile.personalInfo')}</CardTitle>
          <CardDescription>{t('profile.personalInfoDesc')}</CardDescription>
        </CardHeader>
        <CardContent>
          <ProfileForm />
        </CardContent>
      </Card>

      <Separator />

      <Card>
        <CardHeader>
          <CardTitle>{t('profile.passwordSection')}</CardTitle>
          <CardDescription>{t('profile.passwordSectionDesc')}</CardDescription>
        </CardHeader>
        <CardContent>
          <PasswordForm />
        </CardContent>
      </Card>
    </div>
  )
}
