'use client'

import { useState } from 'react'
import { useRouter } from 'next/navigation'
import { Avatar, AvatarFallback } from '@/components/ui/avatar'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { LogOut, Settings } from 'lucide-react'
import type { User } from '@/lib/types'

export function UserMenu({ user }: { user: User }) {
  const router = useRouter()
  const [loading, setLoading] = useState(false)

  const initials = [user.first_name[0], user.last_name?.[0]]
    .filter(Boolean)
    .join('')
    .toUpperCase()

  async function handleLogout() {
    setLoading(true)
    await fetch('/api/auth/logout', { method: 'POST' })
    router.push('/login')
  }

  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={
          <button className="flex items-center gap-2 rounded-lg p-1.5 hover:bg-muted transition-colors outline-none" />
        }
      >
        <Avatar className="size-7">
          <AvatarFallback className="text-xs">{initials}</AvatarFallback>
        </Avatar>
        <div className="hidden sm:block text-left">
          <p className="text-sm font-medium leading-none">
            {user.first_name} {user.last_name}
          </p>
          <p className="text-xs text-muted-foreground mt-0.5">{user.email}</p>
        </div>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-52">
        <div className="px-2 py-1.5">
          <p className="text-sm font-medium">
            {user.first_name} {user.last_name}
          </p>
          <p className="text-xs text-muted-foreground">{user.email}</p>
        </div>
        <DropdownMenuSeparator />
        <DropdownMenuItem render={<a href="/account/profile" />}>
          <Settings className="size-4" />
          Settings
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem
          onClick={handleLogout}
          disabled={loading}
          variant="destructive"
        >
          <LogOut className="size-4" />
          {loading ? 'Signing out…' : 'Sign out'}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
