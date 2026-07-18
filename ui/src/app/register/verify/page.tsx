'use client'

import Link from 'next/link'
import { useTranslation } from 'react-i18next'
import { Card, CardContent } from '@/components/ui/card'
import { Button } from '@/components/ui/button'

export default function VerifyEmailPage() {
  const { t } = useTranslation()
  return (
    <div className="min-h-screen flex items-center justify-center bg-muted/40 p-4">
      <div className="w-full max-w-md space-y-6">
        <div className="text-center space-y-1">
          <p className="text-sm font-medium text-muted-foreground">{t('app.name')}</p>
          <h1 className="text-2xl font-semibold tracking-tight">{t('verify.title')}</h1>
          <p className="text-muted-foreground text-sm">{t('verify.description')}</p>
        </div>

        <Card>
          <CardContent className="space-y-4">
            <p className="text-sm text-muted-foreground">{t('verify.body')}</p>
            <Button render={<Link href="/login" />} variant="outline" className="w-full">
              {t('verify.back')}
            </Button>
          </CardContent>
        </Card>
      </div>
    </div>
  )
}
