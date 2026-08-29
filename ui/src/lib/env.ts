// Empty means same-origin: CloudFront forwards /v1.0/* to the ALB in deployed
// environments, and `next dev` proxies it locally (next.config.ts). Either way
// the browser never makes a cross-origin request, so CORS never applies and the
// auth cookies stay first-party.
export const API_URL = process.env.NEXT_PUBLIC_API_URL ?? ''
// Kept explicit in deployed builds because CSP connect-src is scheme-exact:
// an https:// API origin does not authorize its wss:// counterpart.
export const WS_URL = process.env.NEXT_PUBLIC_WS_URL ?? ''
export const CLIENT_ID = process.env.NEXT_PUBLIC_OAUTH_CLIENT_ID ?? 'accounts'
