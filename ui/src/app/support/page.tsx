'use client'

import { FormEvent, useState } from 'react'
import { useMutation } from '@tanstack/react-query'
import { useRouter } from 'next/navigation'
import { useTranslation } from 'react-i18next'
import { createSupportTicketAPI } from '@/lib/mutations'
import { useAuthStore } from '@/store/auth'
import { SUPPORT_CATEGORIES, buildSupportSubject, findSupportCategory } from '@/lib/support-catalog'
import { TurnstileWidget } from '@/components/turnstile-widget'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'

const PRIORITY_VALUES = ['low', 'medium', 'high', 'urgent', 'critical'] as const

export default function SupportPage() {
  const { t } = useTranslation()
  const router = useRouter()
  const isAuthenticated = useAuthStore((s) => Boolean(s.accessToken))
  const [token, setToken] = useState('')
  const [error, setError] = useState('')
  const [category, setCategory] = useState('account')
  const [subcategory, setSubcategory] = useState('')
  const [subjectOther, setSubjectOther] = useState('')
  const [priority, setPriority] = useState('low')
  const selectedCategory = findSupportCategory(category)
  const isOtherCategory = category === 'other'

  const create = useMutation({
    mutationFn: createSupportTicketAPI,
    onSuccess: (r) =>
      router.push(`/support/ticket?id=${encodeURIComponent(r.ticket_id)}${r.anonymous_token ? `&token=${encodeURIComponent(r.anonymous_token)}` : ''}`),
    onError: () => setError(t('support.genericError')),
  })

  function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    const f = new FormData(e.currentTarget)
    if (!token) {
      setError(t('support.captchaRequired'))
      return
    }
    if (!isOtherCategory && !subcategory) {
      setError(t('support.subcategoryRequired'))
      return
    }
    setError('')
    create.mutate({
      subject_category: category,
      subject_other: buildSupportSubject(category, subcategory, subjectOther),
      priority,
      body: String(f.get('body') || ''),
      email: isAuthenticated ? undefined : String(f.get('email') || ''),
      turnstile_token: token,
    })
  }

  return (
    <main className="mx-auto max-w-xl space-y-6 px-4 py-12">
      <div>
        <h1 className="text-2xl font-semibold">{t('support.title')}</h1>
        <p className="mt-1 text-sm text-muted-foreground">{t('support.subtitle')}</p>
      </div>
      <form onSubmit={submit} className="space-y-4">
        <div>
          <Label htmlFor="category">{t('support.categoryLabel')}</Label>
          <Select
            value={category}
            onValueChange={(value) => {
              setCategory((value ?? 'account') as string)
              setSubcategory('')
            }}
          >
            <SelectTrigger id="category" className="mt-1 w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {SUPPORT_CATEGORIES.map((c) => (
                <SelectItem key={c.value} value={c.value}>
                  {t(`support.categories.${c.value}`)}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>

        {isOtherCategory ? (
          <div>
            <Label htmlFor="subject_other">{t('support.otherSubjectLabel')}</Label>
            <Input
              id="subject_other"
              value={subjectOther}
              onChange={(e) => setSubjectOther(e.target.value)}
              minLength={3}
              maxLength={120}
              required
              className="mt-1"
            />
          </div>
        ) : (
          <div>
            <Label htmlFor="subcategory">{t('support.subcategoryLabel')}</Label>
            <Select value={subcategory} onValueChange={(value) => setSubcategory((value ?? '') as string)}>
              <SelectTrigger id="subcategory" className="mt-1 w-full">
                <SelectValue placeholder={t('support.subcategoryPlaceholder')} />
              </SelectTrigger>
              <SelectContent>
                {selectedCategory?.subcategories.map((s) => (
                  <SelectItem key={s.value} value={s.value}>
                    {t(`support.subcategories.${s.value}`)}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        )}

        {!isAuthenticated && (
          <div>
            <Label htmlFor="email">{t('support.emailLabel')}</Label>
            <Input id="email" name="email" type="email" required className="mt-1" />
          </div>
        )}

        <div>
          <Label htmlFor="priority">{t('support.priorityLabel')}</Label>
          <Select value={priority} onValueChange={(value) => setPriority((value ?? 'low') as string)}>
            <SelectTrigger id="priority" className="mt-1 w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {PRIORITY_VALUES.map((p) => (
                <SelectItem key={p} value={p}>
                  {t(`support.priority.${p}`)}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>

        <div>
          <Label htmlFor="body">{t('support.bodyLabel')}</Label>
          <textarea
            id="body"
            name="body"
            required
            minLength={15}
            maxLength={4000}
            className="mt-1 min-h-40 w-full rounded-lg border border-input bg-transparent p-3 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/70"
          />
        </div>

        <TurnstileWidget onToken={setToken} onError={() => setError(t('support.captchaError'))} onExpire={() => setToken('')} />

        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}

        <Button type="submit" disabled={create.isPending}>
          {create.isPending ? t('support.submitting') : t('support.submit')}
        </Button>
      </form>
    </main>
  )
}
