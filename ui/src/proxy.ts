import { NextRequest, NextResponse } from 'next/server'

export function proxy(req: NextRequest) {
  const { pathname, searchParams } = req.nextUrl

  if (pathname.startsWith('/account')) {
    const at = req.cookies.get('ctech_at')
    if (!at) {
      const continueURL = encodeURIComponent(pathname + (searchParams.toString() ? `?${searchParams}` : ''))
      return NextResponse.redirect(new URL(`/login?continue=${continueURL}`, req.url))
    }
  }

  return NextResponse.next()
}

export const config = {
  matcher: ['/account/:path*'],
}
