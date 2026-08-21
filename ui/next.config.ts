import type { NextConfig } from "next";

const isProduction = process.env.NODE_ENV === 'production';

// Where `next dev` forwards the API to. Deployed environments do NOT do this any
// more — the export is served by Cloudflare and the browser calls the API host
// directly, with CORS. The dev rewrite stays only so local work needs no CORS
// configuration; it is the one place where dev and prod differ on purpose.
const DEV_API_ORIGIN = process.env.DEV_API_ORIGIN || 'http://localhost:8001';

// rewrites() is unsupported by `output: 'export'` and only ever runs under
// `next dev`. Keeping the two mutually exclusive is what lets the dev server
// proxy the API while the production build stays a pure static export.
const nextConfig: NextConfig = {
  allowedDevOrigins: ['127.0.0.1', 'localhost'],
  ...(isProduction
    ? {output: 'export' as const}
    : {
      async rewrites() {
        return [
          {source: '/v1.0/:path*', destination: `${DEV_API_ORIGIN}/v1.0/:path*`},
          {source: '/.well-known/:path*', destination: `${DEV_API_ORIGIN}/.well-known/:path*`},
        ];
      },
    }),
};

export default nextConfig;
