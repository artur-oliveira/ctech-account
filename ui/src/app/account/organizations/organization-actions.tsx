'use client'

import { useState, type SyntheticEvent } from 'react'
import { useRouter } from 'next/navigation'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { Plus } from 'lucide-react'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { createOrganizationAPI } from '@/lib/mutations'
import { isAxiosError } from '@/lib/axios'

/** Matches the server's `validate:"required,max=120"`. */
const MAX_NAME = 120

export function CreateOrganizationDialog() {
  const { t } = useTranslation()
  const router = useRouter()
  const queryClient = useQueryClient()
  const [open, setOpen] = useState(false)

  const { mutate, isPending, error, reset } = useMutation({
    mutationFn: (displayName: string) => createOrganizationAPI(displayName),
    onSuccess: (org) => {
      queryClient.invalidateQueries({ queryKey: ['organizations'] })
      setOpen(false)
      // Straight to the new organization: somebody who just made a workspace
      // wants to invite people into it, and a list of one is a step they did
      // not ask for.
      router.push(`/account/organizations/detail?id=${encodeURIComponent(org.id)}`)
    },
    onError: (err) => {
      if (isAxiosError(err)) {
        toast.error(err.response?.data?.detail ?? t('toast.createOrganizationFailed'))
      }
    },
  })

  const errorMsg = isAxiosError(error)
    ? (error.response?.data?.detail ?? t('toast.createOrganizationFailed'))
    : null

  function handleSubmit(e: SyntheticEvent<HTMLFormElement>) {
    e.preventDefault()
    const fd = new FormData(e.currentTarget)
    mutate((fd.get('display_name') as string).trim())
  }

  function handleOpenChange(next: boolean) {
    setOpen(next)
    if (!next) reset()
  }

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogTrigger
        render={
          <Button size="sm">
            <Plus className="size-4" />
            {t('organizations.create.action')}
          </Button>
        }
      />
      <DialogContent>
        <form onSubmit={handleSubmit} className="space-y-4">
          <DialogHeader>
            <DialogTitle>{t('organizations.create.title')}</DialogTitle>
            <DialogDescription>{t('organizations.create.description')}</DialogDescription>
          </DialogHeader>

          {errorMsg && (
            <Alert variant="destructive">
              <AlertDescription>{errorMsg}</AlertDescription>
            </Alert>
          )}

          <div className="space-y-1.5">
            <Label htmlFor="display_name">{t('organizations.create.nameLabel')}</Label>
            <Input
              id="display_name"
              name="display_name"
              required
              maxLength={MAX_NAME}
              autoComplete="organization"
              placeholder={t('organizations.create.namePlaceholder')}
              className="max-sm:h-11"
            />
          </div>

          <DialogFooter>
            <Button type="submit" disabled={isPending} className="max-sm:min-h-11">
              {isPending ? t('organizations.create.submitting') : t('organizations.create.submit')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
