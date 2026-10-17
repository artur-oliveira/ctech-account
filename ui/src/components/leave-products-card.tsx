'use client'

import Link from 'next/link'
import {useQuery} from '@tanstack/react-query'
import {useTranslation} from 'react-i18next'
import {Button} from '@/components/ui/button'
import {Card, CardContent, CardDescription, CardHeader, CardTitle} from '@/components/ui/card'
import {formatDate} from '@/lib/format'
import {fetchDeletionRequest, fetchLeavableServices} from '@/lib/queries'

/** Product display name; an id the locales do not know reads "CTech <id>". */
export function useProductName() {
  const {t} = useTranslation()
  return (service: string) => t(`deletion.services.${service}`, {defaultValue: `CTech ${service}`})
}

/**
 * Profile entry point to leaving one CTech product (spec
 * docs/specs/2026-10-17-service-unlink.md § 8). Renders nothing until the API
 * lists at least one product, so it stays dark where no product is enabled.
 */
export function LeaveProductsCard() {
  const {t} = useTranslation()
  const productName = useProductName()
  const services = useQuery({queryKey: ['deletion-services'], queryFn: fetchLeavableServices})
  const current = useQuery({queryKey: ['deletion'], queryFn: fetchDeletionRequest})

  if (!services.data || services.data.length === 0) return null
  const leaving = current.data?.scope === 'service' && current.data.service ? current.data : null

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('deletion.leave.cardTitle')}</CardTitle>
        <CardDescription>{t('deletion.leave.cardDesc')}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        {leaving && (
          <p className="text-sm">
            {t('deletion.leave.leaving', {product: productName(leaving.service ?? ''), date: formatDate(leaving.grace_until ?? leaving.confirm_by ?? '')})}
          </p>
        )}
        <ul className="divide-y">
          {services.data.map((s) => (
            <li key={s.service} className="flex items-center justify-between gap-3 py-2">
              <span className="font-medium">{productName(s.service)}</span>
              {s.can_leave
                ? <Button variant="outline" render={<Link href={`/account/leave?service=${encodeURIComponent(s.service)}`}/>}>{t('deletion.leave.cardAction')}</Button>
                : null}
            </li>
          ))}
        </ul>
        {services.data.some((s) => !s.can_leave) && !leaving && (
          <p className="text-sm text-muted-foreground">{t('deletion.leave.busy')}</p>
        )}
      </CardContent>
    </Card>
  )
}
