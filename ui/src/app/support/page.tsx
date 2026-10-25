'use client'

import {SyntheticEvent, useState} from 'react'
import {useMutation} from '@tanstack/react-query'
import {useRouter} from 'next/navigation'
import {useTranslation} from 'react-i18next'
import {createSupportTicketAPI} from '@/lib/mutations'
import {useAuthStore} from '@/store/auth'
import {buildSupportSubject, findSupportCategory, SUPPORT_CATEGORIES} from '@/lib/support-catalog'
import {TurnstileWidget} from '@/components/turnstile-widget'
import {Button, Skeleton, Input, Select, Label, Alert, AlertDescription} from '@aoctech/ui'

const PRIORITY_VALUES = ['low', 'medium', 'high', 'urgent', 'critical'] as const

export default function SupportPage() {
  const {t} = useTranslation()
  const router = useRouter()
  const isInitialized = useAuthStore((s) => s.isInitialized)
  const isAuthenticated = useAuthStore((s) => Boolean(s.accessToken))
  const [token, setToken] = useState('')
  const [error, setError] = useState('')
  const [category, setCategory] = useState('account')
  const [subcategory, setSubcategory] = useState('')
  const [subjectOther, setSubjectOther] = useState('')
  const [priority, setPriority] = useState('low')
  const [turnstileResetSignal, setTurnstileResetSignal] = useState(0)
  const selectedCategory = findSupportCategory(category)
  const isOtherCategory = category === 'other'

  const create = useMutation({
    mutationFn: createSupportTicketAPI,
    onSuccess: (r) =>
      router.push(`/support/ticket?id=${encodeURIComponent(r.ticket_id)}${r.anonymous_token ? `&token=${encodeURIComponent(r.anonymous_token)}` : ''}`),
    onError: () => {
      setError(t('support.genericError'))
      setToken('')
      setTurnstileResetSignal((value) => value + 1)
    },
  })

  function submit(e: SyntheticEvent<HTMLFormElement>) {
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
      {!isInitialized ? (
        <div className="space-y-3" aria-hidden="true">
          {[...Array(4)].map((_, i) => (
            <Skeleton key={i} className="h-16 rounded-lg"/>
          ))}
        </div>
      ) : (
      <form onSubmit={submit} className="space-y-4">
        <div>
          <Label htmlFor="category">{t('support.categoryLabel')}</Label>
          <Select
            id="category"
            className="mt-1"
            value={category}
            onValueChange={(value) => {
              setCategory(value)
              setSubcategory('')
            }}
            options={SUPPORT_CATEGORIES.map((c) => ({value: c.value, label: t(`support.categories.${c.value}`)}))}
          />
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
            <Select
              id="subcategory"
              className="mt-1"
              value={subcategory}
              onValueChange={setSubcategory}
              placeholder={t('support.subcategoryPlaceholder')}
              options={(selectedCategory?.subcategories ?? []).map((s) => ({value: s.value, label: t(`support.subcategories.${s.value}`)}))}
            />
          </div>
        )}

        {!isAuthenticated && (
          <div>
            <Label htmlFor="email">{t('support.emailLabel')}</Label>
            <Input id="email" name="email" type="email" required className="mt-1"/>
          </div>
        )}

        <div>
          <Label htmlFor="priority">{t('support.priorityLabel')}</Label>
          <Select
            id="priority"
            className="mt-1"
            value={priority}
            onValueChange={setPriority}
            options={PRIORITY_VALUES.map((p) => ({value: p, label: t(`support.priority.${p}`)}))}
          />
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

        <TurnstileWidget
          onToken={setToken}
          onError={() => setError(t('support.captchaError'))}
          onExpire={() => setToken('')}
          resetSignal={turnstileResetSignal}
        />

        {error && (
          <Alert tone="danger">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}

        <Button type="submit" disabled={create.isPending}>
          {create.isPending ? t('support.submitting') : t('support.submit')}
        </Button>
      </form>
      )}
    </main>
  )
}
