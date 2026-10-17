'use client'

import {Suspense, useState, type SyntheticEvent} from 'react'
import {useSearchParams} from 'next/navigation'
import {useMutation, useQuery, useQueryClient} from '@tanstack/react-query'
import {useTranslation} from 'react-i18next'
import {Alert, AlertDescription} from '@/components/ui/alert'
import {Button} from '@/components/ui/button'
import {Card, CardContent, CardHeader, CardTitle} from '@/components/ui/card'
import {Input} from '@/components/ui/input'
import {Label} from '@/components/ui/label'
import {useProductName} from '@/components/leave-products-card'
import {isAxiosError} from '@/lib/axios'
import {DELETION_BLOCKED_PROBLEM, servicePhrase} from '@/lib/constants'
import {formatDate} from '@/lib/format'
import {requestServiceUnlinkAPI} from '@/lib/mutations'
import {fetchProfile} from '@/lib/queries'
import type {DeletionBlocker, ProblemDetail} from '@/lib/types'

const UNKNOWN_SERVICE_PROBLEM = 'unknown-service'

function problemOf(err: unknown): ProblemDetail | undefined {
  return isAxiosError(err) ? (err as {response?: {data?: ProblemDetail}}).response?.data : undefined
}

function statusOf(err: unknown): number | undefined {
  return isAxiosError(err) ? (err as {response?: {status?: number}}).response?.status : undefined
}

function BlockerList({blockers}: {blockers: DeletionBlocker[]}) {
  const {t} = useTranslation()
  return (
    <Alert variant="destructive">
      <AlertDescription className="space-y-2">
        <p className="font-medium">{t('deletion.blockedTitle')}</p>
        <ul className="list-disc pl-5 space-y-1">
          {blockers.map((b) => (
            <li key={b.code + (b.action_url ?? '')}>
              {t(`deletion.blockers.${b.code}`, {defaultValue: t('deletion.blockers.unknown', {code: b.code})})}
              {b.action_url && (
                <> <a href={b.action_url} className="underline underline-offset-4">{t('deletion.blockerAction')}</a></>
              )}
            </li>
          ))}
        </ul>
      </AlertDescription>
    </Alert>
  )
}

function LeaveProduct() {
  const {t} = useTranslation()
  const productName = useProductName()
  const service = useSearchParams().get('service') ?? ''
  const product = productName(service)
  const phrase = servicePhrase(service)
  const queryClient = useQueryClient()
  const [typed, setTyped] = useState('')
  const profile = useQuery({queryKey: ['profile'], queryFn: fetchProfile})
  const request = useMutation({
    mutationFn: ({password}: {password?: string}) => requestServiceUnlinkAPI(service, phrase, password),
    onSuccess: () => Promise.all([
      queryClient.invalidateQueries({queryKey: ['deletion']}),
      queryClient.invalidateQueries({queryKey: ['deletion-services']}),
    ]),
  })

  function handleSubmit(e: SyntheticEvent<HTMLFormElement>) {
    e.preventDefault()
    const password = new FormData(e.currentTarget).get('password')
    request.mutate({password: typeof password === 'string' && password !== '' ? password : undefined})
  }

  const problem = problemOf(request.error)
  const status = statusOf(request.error)
  const blockers = problem?.type?.endsWith(DELETION_BLOCKED_PROBLEM) ? problem.blockers ?? [] : []
  const errorText = request.isError && blockers.length === 0
    ? problem?.type?.endsWith(UNKNOWN_SERVICE_PROBLEM) ? t('deletion.leave.unknown')
      : status === 401 ? t('deletion.errors.password')
        : status === 409 ? t('deletion.errors.conflict')
          : status === 429 ? t('deletion.errors.tooMany')
            : status === 503 ? t('deletion.errors.unavailable')
              : t('errors.network')
    : ''

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold">{t('deletion.leave.title', {product})}</h1>
        <p className="text-muted-foreground text-sm mt-1">{t('deletion.leave.subtitle', {product})}</p>
      </div>

      <Alert>
        <AlertDescription>
          {t('deletion.exportHint')}{' '}
          <a href="/account/export" className="underline underline-offset-4">{t('deletion.exportLink')}</a>
        </AlertDescription>
      </Alert>

      {request.data && (
        <Alert><AlertDescription>{t('deletion.checkEmail', {date: formatDate(request.data.confirm_by ?? '')})}</AlertDescription></Alert>
      )}

      <Card>
        <CardHeader><CardTitle>{t('deletion.whatHappens')}</CardTitle></CardHeader>
        <CardContent className="space-y-4 text-sm">
          <ol className="list-decimal pl-5 space-y-1">
            {(t('deletion.leave.steps', {returnObjects: true, product}) as string[]).map((s) => <li key={s}>{s}</li>)}
          </ol>
        </CardContent>
      </Card>

      {blockers.length > 0 && <BlockerList blockers={blockers}/>}
      {errorText && <Alert variant="destructive"><AlertDescription>{errorText}</AlertDescription></Alert>}

      {!request.isSuccess && (
        <form onSubmit={handleSubmit} className="space-y-4">
          <div className="space-y-1.5">
            <Label htmlFor="phrase">{t('deletion.leave.phraseLabel', {phrase})}</Label>
            <Input id="phrase" value={typed} onChange={(e) => setTyped(e.target.value)} autoComplete="off"/>
          </div>
          {profile.data?.has_password ? (
            <div className="space-y-1.5">
              <Label htmlFor="password">{t('deletion.passwordLabel')}</Label>
              <Input id="password" name="password" type="password" autoComplete="current-password" required/>
            </div>
          ) : profile.data ? (
            <p className="text-sm text-muted-foreground">{t('deletion.noPasswordHint')}</p>
          ) : null}
          <Button type="submit" variant="destructive" disabled={!service || typed !== phrase || request.isPending || !profile.data}>
            {request.isPending ? t('common.loading') : t('deletion.leave.submit', {product})}
          </Button>
        </form>
      )}
    </div>
  )
}

export default function LeaveProductPage() {
  return <Suspense fallback={null}><LeaveProduct/></Suspense>
}
