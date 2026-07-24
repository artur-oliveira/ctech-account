'use client'

import Link from 'next/link'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { fetchProfile, fetchSessions } from '@/lib/queries'
import { formatDistanceToNow } from '@/lib/format'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { QueryError } from '@/components/query-error'

export default function DashboardPage() {
  const { t } = useTranslation()
  const { data: user, isError: profileError, error: profileErr, refetch: refetchProfile } = useQuery({ queryKey: ['profile'], queryFn: fetchProfile })
  const { data: sessions = [], isError: sessionsError, error: sessionsErr, refetch: refetchSessions } = useQuery({ queryKey: ['sessions'], queryFn: fetchSessions })

  if (profileError || sessionsError) {
    return <QueryError error={profileErr ?? sessionsErr} onRetry={() => { refetchProfile(); refetchSessions() }} />
  }

  if (!user) return null

  const currentSession = sessions.find((s) => s.is_current)

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">
          {t('dashboard.welcome', { name: user.display_name ?? user.first_name })}
        </h1>
        <p className="text-muted-foreground text-sm mt-1">
          {user.email}
          {!user.email_verified && (
            <Badge variant="secondary" className="ml-2 text-xs">{t('dashboard.unverified')}</Badge>
          )}
        </p>
      </div>

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        <Card>
          <CardHeader className="pb-2">
            <CardDescription>{t('dashboard.activeSessions')}</CardDescription>
            <CardTitle className="text-2xl font-semibold tracking-tight">{sessions.length}</CardTitle>
          </CardHeader>
          <CardContent>
            <Link href="/account/sessions" className="text-sm text-primary hover:underline">
              {t('dashboard.manageSessions')}
            </Link>
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="pb-2">
            <CardDescription>{t('dashboard.currentSession')}</CardDescription>
            <CardTitle className="text-base truncate">
              {currentSession?.device_name ?? t('dashboard.unknownDevice')}
            </CardTitle>
          </CardHeader>
          <CardContent>
            <p className="text-sm text-muted-foreground">
              {currentSession ? formatDistanceToNow(currentSession.last_used_at) : '—'}
            </p>
          </CardContent>
        </Card>

        <Card className="sm:col-span-2 lg:col-span-1">
          <CardHeader className="pb-2">
            <CardDescription>{t('dashboard.accountCreated')}</CardDescription>
            <CardTitle className="text-base">
              {formatDistanceToNow(user.created_at)}
            </CardTitle>
          </CardHeader>
          <CardContent>
            <Link href="/account/security" className="text-sm text-primary hover:underline">
              {t('dashboard.securitySettings')}
            </Link>
          </CardContent>
        </Card>
      </div>
    </div>
  )
}
