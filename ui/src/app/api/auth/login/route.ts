import { NextRequest, NextResponse } from 'next/server'
import { generatePKCE, generateState } from '@/lib/pkce'

const API_URL = process.env.API_URL ?? 'http://localhost:8080'
const CLIENT_ID = process.env.OAUTH_CLIENT_ID ?? 'accounts-ui'
const BASE_URL = process.env.BASE_URL ?? 'http://localhost:3000'
const REDIRECT_URI = `${BASE_URL}/api/auth/callback`

export async function POST(req: NextRequest) {
  const body = await req.json().catch(() => null)
  if (!body?.email || !body?.password) {
    return NextResponse.json({ detail: 'Email and password are required.' }, { status: 400 })
  }

  const loginRes = await fetch(`${API_URL}/v1.0/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email: body.email, password: body.password }),
  })

  if (!loginRes.ok) {
    const err = await loginRes.json().catch(() => ({ detail: 'Login failed.' }))
    return NextResponse.json(err, { status: loginRes.status })
  }

  const loginData = await loginRes.json()

  if (loginData.requires_mfa) {
    const res = NextResponse.json({ requires_mfa: true, mfa_methods: loginData.mfa_methods })
    res.cookies.set('ctech_mfa_token', loginData.mfa_token, {
      httpOnly: true,
      secure: process.env.NODE_ENV === 'production',
      sameSite: 'lax',
      maxAge: 300,
      path: '/',
    })
    return res
  }

  const sessionCookies = loginRes.headers.getSetCookie()
  const sessionHeader = sessionCookies.find((c) => c.startsWith('ctech_session='))
  const sessionValue = sessionHeader?.match(/ctech_session=([^;]+)/)?.[1]
  if (!sessionValue) {
    return NextResponse.json({ detail: 'Session cookie not returned.' }, { status: 500 })
  }

  const tokenRes = await exchangeToken(sessionValue)
  if (!tokenRes) {
    return NextResponse.json({ detail: 'Token exchange failed.' }, { status: 500 })
  }

  const continueURL = body.continue_url ?? '/account'
  const res = NextResponse.json({ ok: true, redirect: continueURL })
  setTokenCookies(res, tokenRes.access_token, tokenRes.refresh_token)
  return res
}

async function exchangeToken(
  sessionValue: string,
): Promise<{ access_token: string; refresh_token: string } | null> {
  const { codeVerifier, codeChallenge } = generatePKCE()
  const state = generateState()

  const authorizeURL = new URL(`${API_URL}/v1.0/authorize`)
  authorizeURL.searchParams.set('client_id', CLIENT_ID)
  authorizeURL.searchParams.set('redirect_uri', REDIRECT_URI)
  authorizeURL.searchParams.set('response_type', 'code')
  authorizeURL.searchParams.set('scope', 'openid profile email')
  authorizeURL.searchParams.set('state', state)
  authorizeURL.searchParams.set('code_challenge', codeChallenge)
  authorizeURL.searchParams.set('code_challenge_method', 'S256')

  const authorizeRes = await fetch(authorizeURL.toString(), {
    redirect: 'manual',
    headers: { Cookie: `ctech_session=${sessionValue}` },
  })

  const location = authorizeRes.headers.get('location')
  if (!location) return null

  const code = new URL(location, BASE_URL).searchParams.get('code')
  if (!code) return null

  const tokenRes = await fetch(`${API_URL}/v1.0/token`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    body: new URLSearchParams({
      grant_type: 'authorization_code',
      code,
      client_id: CLIENT_ID,
      redirect_uri: REDIRECT_URI,
      code_verifier: codeVerifier,
    }),
  })

  if (!tokenRes.ok) return null
  return tokenRes.json()
}

function setTokenCookies(
  res: NextResponse,
  accessToken: string,
  refreshToken: string,
) {
  const secure = process.env.NODE_ENV === 'production'
  res.cookies.set('ctech_at', accessToken, {
    httpOnly: true,
    secure,
    sameSite: 'lax',
    maxAge: 900,
    path: '/',
  })
  res.cookies.set('ctech_rt', refreshToken, {
    httpOnly: true,
    secure,
    sameSite: 'lax',
    maxAge: 90 * 24 * 60 * 60,
    path: '/',
  })
}

export { setTokenCookies, exchangeToken }
