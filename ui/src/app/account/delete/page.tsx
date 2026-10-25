'use client'

import {useState, type SyntheticEvent} from 'react'
import {useMutation, useQuery, useQueryClient} from '@tanstack/react-query'
import {useTranslation} from 'react-i18next'
import {Alert, AlertDescription} from '@/components/ui/alert'
import {Button, Input} from '@aoctech/ui'
import {Card, CardContent, CardHeader, CardTitle} from '@/components/ui/card'
import {Label} from '@/components/ui/label'
import {isAxiosError} from '@/lib/axios'
import {DELETION_BLOCKED_PROBLEM, DELETION_PHRASE} from '@/lib/constants'
import {formatDate} from '@/lib/format'
import {requestDeletionAPI} from '@/lib/mutations'
import {fetchDeletionRequest, fetchProfile} from '@/lib/queries'
import type {DeletionBlocker, ProblemDetail} from '@/lib/types'

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

export default function DeleteAccountPage() {
  const {t} = useTranslation()
  const queryClient = useQueryClient()
  const [phrase, setPhrase] = useState('')
  const profile = useQuery({queryKey: ['profile'], queryFn: fetchProfile})
  const current = useQuery({queryKey: ['deletion'], queryFn: fetchDeletionRequest})
  const request = useMutation({
    mutationFn: ({password}: {password?: string}) => requestDeletionAPI(DELETION_PHRASE, password),
    onSuccess: () => queryClient.invalidateQueries({queryKey: ['deletion']}),
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
    ? status === 401 ? t('deletion.errors.password')
      : status === 409 ? t('deletion.errors.conflict')
        : status === 429 ? t('deletion.errors.tooMany')
          : status === 503 ? t('deletion.errors.unavailable')
            : t('errors.network')
    : ''
  // A pending request to leave one product is shown by the profile's "Leave a CTech product" card, not here.
  const sent = request.data ?? (current.data?.state === 'awaiting_confirmation' && current.data.scope !== 'service' ? current.data : null)

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold">{t('deletion.title')}</h1>
        <p className="text-muted-foreground text-sm mt-1">{t('deletion.subtitle')}</p>
      </div>

      <Alert>
        <AlertDescription>
          {t('deletion.exportHint')}{' '}
          <a href="/account/export" className="underline underline-offset-4">{t('deletion.exportLink')}</a>
        </AlertDescription>
      </Alert>

      {current.data?.legal_hold && (
        <Alert><AlertDescription>{t('deletion.legalHold')}</AlertDescription></Alert>
      )}
      {sent && (
        <Alert>
          <AlertDescription>
            {request.data
              ? t('deletion.checkEmail', {date: formatDate(sent.confirm_by ?? '')})
              : t('deletion.awaitingConfirmation', {date: formatDate(sent.confirm_by ?? '')})}
          </AlertDescription>
        </Alert>
      )}

      <Card>
        <CardHeader><CardTitle>{t('deletion.whatHappens')}</CardTitle></CardHeader>
        <CardContent className="space-y-4 text-sm">
          <ol className="list-decimal pl-5 space-y-1">
            {(t('deletion.steps', {returnObjects: true}) as string[]).map((s) => <li key={s}>{s}</li>)}
          </ol>
          <p className="font-medium">{t('deletion.whatIsKept')}</p>
          <ul className="list-disc pl-5 space-y-1">
            {(t('deletion.kept', {returnObjects: true}) as string[]).map((s) => <li key={s}>{s}</li>)}
          </ul>
          <Alert><AlertDescription>{t('deletion.soleOrgsWarning')}</AlertDescription></Alert>
        </CardContent>
      </Card>

      {blockers.length > 0 && <BlockerList blockers={blockers}/>}
      {errorText && <Alert variant="destructive"><AlertDescription>{errorText}</AlertDescription></Alert>}

      {!request.isSuccess && (
        <form onSubmit={handleSubmit} className="space-y-4">
          <div className="space-y-1.5">
            <Label htmlFor="phrase">{t('deletion.phraseLabel', {phrase: DELETION_PHRASE})}</Label>
            <Input id="phrase" value={phrase} onChange={(e) => setPhrase(e.target.value)} autoComplete="off"/>
          </div>
          {profile.data?.has_password ? (
            <div className="space-y-1.5">
              <Label htmlFor="password">{t('deletion.passwordLabel')}</Label>
              <Input id="password" name="password" type="password" autoComplete="current-password" required/>
            </div>
          ) : profile.data ? (
            <p className="text-sm text-muted-foreground">{t('deletion.noPasswordHint')}</p>
          ) : null}
          <Button type="submit" variant="danger" disabled={phrase !== DELETION_PHRASE || request.isPending || !profile.data}>
            {request.isPending ? t('common.loading') : t('deletion.submit')}
          </Button>
        </form>
      )}
    </div>
  )
}
