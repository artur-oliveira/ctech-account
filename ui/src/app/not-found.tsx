'use client'

import Link from 'next/link'
import {useAuthStore} from '@/store/auth'
import {useTranslation} from "react-i18next";
import {Button} from "@/components/ui/button";

export default function NotFound() {
  const {isInitialized, accessToken} = useAuthStore()
  const user = isInitialized && accessToken !== null && accessToken !== undefined;
  const {t} = useTranslation()
  
  
  return (
    <div className="min-h-screen bg-background flex items-center justify-center px-4">
      <div className="text-center">
        <p className="text-7xl font-bold text-foreground/10 select-none">404</p>
        <h1 className="mt-4 text-xl font-semibold text-foreground">{t('notFound.header')}</h1>
        <p className="mt-2 text-sm text-muted-foreground">
          {t('notFound.description')}
        </p>
        <Button className="mt-6" render={<Link href={user ? "/account" : "/login"}/>}>
          {user ? t('notFound.backToAccount') : t('notFound.backToLogin')}
        </Button>
      </div>
    </div>
  )
}
