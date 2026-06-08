import { NextRequest, NextResponse } from 'next/server'
import { cookies } from 'next/headers'
import { setTokenCookies } from '@/app/api/auth/login/route'

const API_URL = process.env.API_URL ?? 'http://localhost:8080'

export async function POST(req: NextRequest) {
  const body = await req.json().catch(() => null)
  if (!body?.code) {
    return NextResponse.json({ detail: 'TOTP code is required.' }, { status: 400 })
  }

  const cookieStore = await cookies()
  const mfaToken = cookieStore.get('ctech_mfa_token')?.value
  if (!mfaToken) {
    return NextResponse.json({ detail: 'MFA session expired. Please log in again.' }, { status: 401 })
  }

  const challengeRes = await fetch(`${API_URL}/v1.0/auth/mfa/challenge`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ mfa_token: mfaToken, code: body.code }),
  })

  if (!challengeRes.ok) {
    const err = await challengeRes.json().catch(() => ({ detail: 'MFA challenge failed.' }))
    return NextResponse.json(err, { status: challengeRes.status })
  }

  const sessionCookies = challengeRes.headers.getSetCookie()
  const sessionHeader = sessionCookies.find((c) => c.startsWith('ctech_session='))
  const sessionValue = sessionHeader?.match(/ctech_session=([^;]+)/)?.[1]
  if (!sessionValue) {
    return NextResponse.json({ detail: 'Session not returned after MFA.' }, { status: 500 })
  }

  const { exchangeToken } = await import('@/app/api/auth/login/route')
  const tokenRes = await exchangeToken(sessionValue)
  if (!tokenRes) {
    return NextResponse.json({ detail: 'Token exchange failed after MFA.' }, { status: 500 })
  }

  const continueURL = body.continue_url ?? '/account'
  const res = NextResponse.json({ ok: true, redirect: continueURL })
  setTokenCookies(res, tokenRes.access_token, tokenRes.refresh_token)
  res.cookies.delete('ctech_mfa_token')
  return res
}
