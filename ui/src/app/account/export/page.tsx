'use client'

import {type SyntheticEvent} from 'react'
import {useMutation, useQuery, useQueryClient} from '@tanstack/react-query'
import {useTranslation} from 'react-i18next'
import {Button, Input, Alert, AlertDescription, Label} from '@aoctech/ui'
import {Card, CardContent, CardHeader, CardTitle} from '@/components/ui/card'
import {isAxiosError} from '@/lib/axios'
import {formatDate} from '@/lib/format'
import {discardDataExportAPI, requestDataExportAPI} from '@/lib/mutations'
import {fetchDataExport, fetchProfile} from '@/lib/queries'

const POLL_MS = 5000
const KIB = 1024

function statusOf(err: unknown): number | undefined {
  return isAxiosError(err) ? (err as {response?: {status?: number}}).response?.status : undefined
}

function size(bytes: number): string {
  return bytes < KIB * KIB ? `${Math.max(1, Math.round(bytes / KIB))} KB` : `${(bytes / KIB / KIB).toFixed(1)} MB`
}

export default function DataExportPage() {
  const {t} = useTranslation()
  const queryClient = useQueryClient()
  const profile = useQuery({queryKey: ['profile'], queryFn: fetchProfile})
  const current = useQuery({
    queryKey: ['data-export'],
    queryFn: fetchDataExport,
    refetchInterval: (q) => (q.state.data?.state === 'building' || q.state.data?.parts.some((p) => p.status === 'dispatched') ? POLL_MS : false),
  })
  const refresh = () => queryClient.invalidateQueries({queryKey: ['data-export']})
  const request = useMutation({
    mutationFn: ({password}: {password?: string}) => requestDataExportAPI(password),
    onSuccess: refresh,
  })
  const discard = useMutation({mutationFn: discardDataExportAPI, onSuccess: refresh})

  function handleSubmit(e: SyntheticEvent<HTMLFormElement>) {
    e.preventDefault()
    const password = new FormData(e.currentTarget).get('password')
    request.mutate({password: typeof password === 'string' && password !== '' ? password : undefined})
  }

  const status = statusOf(request.error)
  const errorText = request.isError
    ? status === 401 ? t('dataExport.errors.password')
      : status === 403 ? t('dataExport.errors.stepUp')
        : status === 429 ? t('dataExport.errors.tooSoon')
          : status === 409 ? t('dataExport.errors.conflict')
            : t('errors.network')
    : ''
  const serviceName = (service: string) => t(`dataExport.services.${service}`, {defaultValue: service})
  const data = current.data
  const canRequest = !data || data.state === 'failed' || data.state === 'expired' || data.state === 'ready'

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold">{t('dataExport.title')}</h1>
        <p className="text-muted-foreground text-sm mt-1">{t('dataExport.subtitle')}</p>
      </div>

      <Card>
        <CardContent className="space-y-1 pt-6 text-sm">
          <p>{t('dataExport.scope')}</p>
          <p className="text-muted-foreground">{t('dataExport.notIncluded')}</p>
        </CardContent>
      </Card>

      {data?.state === 'building' && <Alert><AlertDescription>{t('dataExport.building')}</AlertDescription></Alert>}
      {data?.state === 'failed' && <Alert tone="danger"><AlertDescription>{t('dataExport.failed')}</AlertDescription></Alert>}
      {data?.state === 'expired' && <Alert><AlertDescription>{t('dataExport.expired')}</AlertDescription></Alert>}
      {errorText && <Alert tone="danger"><AlertDescription>{errorText}</AlertDescription></Alert>}

      {data?.state === 'ready' && (
        <Card>
          <CardHeader><CardTitle>{t('dataExport.ready')}</CardTitle></CardHeader>
          <CardContent className="space-y-3 text-sm">
            {data.expires_at && <p className="text-muted-foreground">{t('dataExport.expiresAt', {date: formatDate(data.expires_at)})}</p>}
            {data.parts.filter((p) => p.status === 'dispatched' || p.status === 'failed').map((p) => (
              <p key={p.service} className={p.status === 'failed' ? 'text-destructive' : 'text-muted-foreground'}>
                {t(p.status === 'failed' ? 'dataExport.partFailed' : 'dataExport.partPending', {service: serviceName(p.service)})}
              </p>
            ))}
            <div className="flex flex-wrap gap-2">
              {data.links.map((l) => (
                <Button key={l.service} render={<a href={l.url} download/>}>
                  {t('dataExport.download', {service: serviceName(l.service), size: size(l.size)})}
                </Button>
              ))}
              <Button variant="outline" disabled={discard.isPending} onClick={() => discard.mutate()}>
                {discard.isPending ? t('dataExport.discarding') : t('dataExport.discard')}
              </Button>
            </div>
          </CardContent>
        </Card>
      )}

      {canRequest && data?.state !== 'ready' && (
        <form onSubmit={handleSubmit} className="space-y-4">
          {profile.data?.has_password ? (
            <div className="space-y-1.5">
              <Label htmlFor="password">{t('dataExport.passwordLabel')}</Label>
              <Input id="password" name="password" type="password" autoComplete="current-password" required/>
            </div>
          ) : profile.data ? (
            <p className="text-sm text-muted-foreground">{t('dataExport.noPasswordHint')}</p>
          ) : null}
          <Button type="submit" disabled={request.isPending || !profile.data}>
            {request.isPending ? t('dataExport.requesting') : t('dataExport.request')}
          </Button>
        </form>
      )}
    </div>
  )
}
