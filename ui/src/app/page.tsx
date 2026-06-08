import { cookies } from 'next/headers'
import { redirect } from 'next/navigation'

export default async function RootPage() {
  const store = await cookies()
  if (store.get('ctech_at')) {
    redirect('/account')
  }
  redirect('/login')
}
