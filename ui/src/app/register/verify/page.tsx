'use client'

import Link from 'next/link'
import { useTranslation } from 'react-i18next'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'

export default function VerifyEmailPage() {
  const { t } = useTranslation()
  return (
    <div className="min-h-screen flex items-center justify-center bg-muted/40 p-4">
      <div className="w-full max-w-md">
        <Card>
          <CardHeader>
            <CardTitle>{t('verify.title')}</CardTitle>
            <CardDescription>{t('verify.description')}</CardDescription>
          </CardHeader>
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
