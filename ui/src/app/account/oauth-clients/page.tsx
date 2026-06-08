'use client'

import { useTranslation } from 'react-i18next'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { AppWindow } from 'lucide-react'

export default function OAuthClientsPage() {
  const { t } = useTranslation()
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold">{t('oauthClients.title')}</h1>
        <p className="text-muted-foreground text-sm mt-1">{t('oauthClients.subtitle')}</p>
      </div>

      <Card>
        <CardHeader>
          <div className="flex items-center gap-2">
            <AppWindow className="size-5" />
            <CardTitle className="text-base">{t('oauthClients.management')}</CardTitle>
            <Badge variant="secondary" className="text-xs">{t('oauthClients.comingSoon')}</Badge>
          </div>
          <CardDescription>{t('oauthClients.description')}</CardDescription>
        </CardHeader>
        <CardContent>
          <p className="text-sm text-muted-foreground">{t('oauthClients.body')}</p>
        </CardContent>
      </Card>
    </div>
  )
}
