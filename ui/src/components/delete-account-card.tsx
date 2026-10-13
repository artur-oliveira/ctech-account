'use client'

import Link from 'next/link'
import {useTranslation} from 'react-i18next'
import {Button} from '@/components/ui/button'
import {Card, CardContent, CardDescription, CardHeader, CardTitle} from '@/components/ui/card'

/** Profile entry point to account deletion: a quiet, separate danger zone. */
export function DeleteAccountCard() {
  const {t} = useTranslation()
  return (
    <Card className="border-destructive/40">
      <CardHeader>
        <CardTitle>{t('deletion.cardTitle')}</CardTitle>
        <CardDescription>{t('deletion.cardDesc')}</CardDescription>
      </CardHeader>
      <CardContent>
        <Button variant="destructive" render={<Link href="/account/delete"/>}>{t('deletion.cardAction')}</Button>
      </CardContent>
    </Card>
  )
}
