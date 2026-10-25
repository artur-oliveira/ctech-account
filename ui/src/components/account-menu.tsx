'use client'

import {useRouter} from 'next/navigation'
import {useTranslation} from 'react-i18next'
import {Settings, ShieldCheck} from 'lucide-react'
import {UserMenu} from '@aoctech/ui'
import {useAuthStore} from '@/store/auth'
import {logoutAPI} from '@/lib/mutations'
import {toLocale} from '@/providers/network-provider'
import type {User} from '@/lib/types'

/** Account's @aoctech/ui UserMenu: this user, its settings/admin links and the sign-out call. */
export function AccountMenu({user}: {user: User}) {
  const {t, i18n} = useTranslation()
  const router = useRouter()

  async function signOut() {
    try {
      await logoutAPI()
    } finally {
      useAuthStore.getState().clearAuth()
      router.push('/login')
    }
  }

  return (
    <UserMenu
      name={[user.first_name, user.last_name].filter(Boolean).join(' ')}
      email={user.email}
      locale={toLocale(i18n.language)}
      items={[
        {label: t('menu.settings'), href: '/account/profile', icon: <Settings aria-hidden />},
        ...(user.support_role ? [{label: t('menu.admin'), href: '/admin/support', icon: <ShieldCheck aria-hidden />}] : []),
      ]}
      onSignOut={() => void signOut()}
      labels={{signOut: t('menu.signOut')}}
    />
  )
}
