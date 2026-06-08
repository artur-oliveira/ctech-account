import { NextRequest, NextResponse } from 'next/server'
import { cookies } from 'next/headers'

const API_URL = process.env.API_URL ?? 'http://localhost:8080'

export async function POST(_req: NextRequest) {
  const cookieStore = await cookies()
  const at = cookieStore.get('ctech_at')?.value

  if (at) {
    await fetch(`${API_URL}/v1.0/auth/logout`, {
      method: 'POST',
      headers: { Authorization: `Bearer ${at}` },
    }).catch(() => {})
  }

  const res = NextResponse.json({ ok: true })
  res.cookies.delete('ctech_at')
  res.cookies.delete('ctech_rt')
  res.cookies.delete('ctech_mfa_token')
  return res
}
