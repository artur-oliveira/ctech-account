import { NextRequest, NextResponse } from 'next/server'
import { cookies } from 'next/headers'
import { setTokenCookies } from '@/app/api/auth/login/route'

const API_URL = process.env.API_URL ?? 'http://localhost:8080'
const CLIENT_ID = process.env.OAUTH_CLIENT_ID ?? 'accounts-ui'

export async function POST(_req: NextRequest) {
  const cookieStore = await cookies()
  const rt = cookieStore.get('ctech_rt')?.value
  if (!rt) {
    return NextResponse.json({ detail: 'No refresh token.' }, { status: 401 })
  }

  const tokenRes = await fetch(`${API_URL}/v1.0/token`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    body: new URLSearchParams({
      grant_type: 'refresh_token',
      refresh_token: rt,
      client_id: CLIENT_ID,
    }),
  })

  if (!tokenRes.ok) {
    const res = NextResponse.json({ detail: 'Token refresh failed.' }, { status: 401 })
    res.cookies.delete('ctech_at')
    res.cookies.delete('ctech_rt')
    return res
  }

  const data = await tokenRes.json()
  const res = NextResponse.json({ ok: true })
  setTokenCookies(res, data.access_token, data.refresh_token)
  return res
}
