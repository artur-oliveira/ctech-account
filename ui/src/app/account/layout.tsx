import { getProfile } from '@/lib/api'
import { AccountNav } from '@/components/account-nav'
import { UserMenu } from '@/components/user-menu'
import { Separator } from '@/components/ui/separator'

export default async function AccountLayout({ children }: { children: React.ReactNode }) {
  const user = await getProfile()

  if (!user) {
    return null
  }

  return (
    <div className="min-h-screen flex flex-col">
      <header className="sticky top-0 z-40 border-b bg-background/95 backdrop-blur supports-backdrop-filter:bg-background/60">
        <div className="mx-auto max-w-6xl flex h-14 items-center justify-between px-4">
          <a href="/account" className="font-semibold text-sm">
            arturocarvalho.com
          </a>
          <UserMenu user={user} />
        </div>
      </header>

      <div className="mx-auto max-w-6xl flex flex-1 w-full gap-8 px-4 py-8">
        <aside className="hidden md:block w-52 shrink-0">
          <AccountNav />
        </aside>

        <Separator orientation="vertical" className="hidden md:block h-auto" />

        <main className="flex-1 min-w-0">{children}</main>
      </div>
    </div>
  )
}
