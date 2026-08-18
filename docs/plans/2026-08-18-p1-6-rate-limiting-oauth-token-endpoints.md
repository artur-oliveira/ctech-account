# P1-6: Enhance Rate Limiting for OAuth Token Endpoints in ctech-account

## Overview

Enhance the rate limiting protection for OAuth token endpoints (`/v1.0/token` and `/v1.0/revoke`) to prevent abuse while
maintaining usability for legitimate clients. The current implementation provides IP-based brute-force protection but
lacks throughput limiting and grant-type-specific controls.

## Current Implementation Analysis

The OAuth token endpoints currently have rate limiting applied via `tokenLimiter` in `cmd/api/main.go`:

- IP-based limiter (`prefix: "token"`)
- Limits: 5 failed requests per 15 minutes (`FailedLoginMax`, `FailedLoginWindow`)
- CountOnlyFailures: true (only counts 4xx/5xx responses)
- FailClosed: true (denies requests when Valkey is unavailable)

This provides brute-force protection but doesn't prevent:

- Legitimate clients making excessive token requests (e.g., misconfigured clients refreshing too frequently)
- Abuse that succeeds (2xx responses don't count toward limit)
- Different abuse patterns by grant type (authorization_code vs refresh_token vs client_credentials)

## Requirements

- Maintain existing brute-force protection (IP-based, failed-attempts-only)
- Add throughput limiting to prevent excessive legitimate usage
- Consider grant-type-specific limits where appropriate
- Preserve backward compatibility - existing valid usage patterns should continue working
- Use existing Valkey-backed rate limiting infrastructure
- Follow existing patterns and constants from `middleware/ratelimit.go`

## Design Approach

### Enhance tokenLimiter configuration

Add a second rate limiter for throughput protection alongside the existing brute-force protector:

1. **Keep existing brute-force protector** (unchanged):
    - IP-based, counts only failures
    - 5 failures / 15 minutes
    - Protects against credential guessing attacks

2. **Add throughput limiter** (new):
    - IP-based, counts all requests
    - Higher limit to allow legitimate usage
    - Prevents abuse from excessive successful requests
    - Different window for burst tolerance

### Implementation Details

#### Modified File: `cmd/api/main.go`

Update the token limiter section to include both brute-force and throughput protection:

```go
// Rate limiting (Valkey-backed; no-op when Valkey is disabled).
// Brute-force guard: count only failed responses per client IP.
ipKey := utils.IP
authLimiter := middleware.RateLimit(middleware.RateLimitConfig{
Cache: valkeyClient, Prefix: "login", Max: middleware.FailedLoginMax,
Window: middleware.FailedLoginWindow, KeyFunc: ipKey, CountOnlyFailures: true, FailClosed: true,
})
pwResetLimiter := middleware.RateLimit(middleware.RateLimitConfig{
Cache: valkeyClient, Prefix: "pwreset", Max: middleware.FailedLoginMax,
Window: middleware.FailedLoginWindow, KeyFunc: ipKey, CountOnlyFailures: true, FailClosed: true,
})
tokenLimiterBruteForce := middleware.RateLimit(middleware.RateLimitConfig{
Cache: valkeyClient, Prefix: "token_brute", Max: middleware.FailedLoginMax,
Window: middleware.FailedLoginWindow, KeyFunc: ipKey, CountOnlyFailures: true, FailClosed: true,
})
tokenLimiterThroughput := middleware.RateLimit(middleware.RateLimitConfig{
Cache: valkeyClient, Prefix: "token_throughput", Max: 50, // 50 requests
Window: time.Minute, KeyFunc: ipKey, CountOnlyFailures: false, FailClosed: true,
})
// ... existing limiters ...

v1.Use("/token", tokenLimiterBruteForce, tokenLimiterThroughput)
v1.Use("/revoke", tokenLimiterBruteForce, tokenLimiterThroughput)
```

### Security Considerations

- **Brute-force protection preserved**: Existing failure-only counting maintains protection against credential stuffing
- **Throughput limiting**: Prevents resource exhaustion from excessive valid requests
- **FailClosed**: Both limiters deny requests when Valkey is unavailable (secure default)
- **No user enumeration**: Rate limiting doesn't reveal account existence
- **Consistent error messages**: All limiters return standardized 429 responses

### Rate Limit Values Rationale

- **Brute-force (5/15min)**: Matches login endpoints, prevents credential guessing
- **Throughput (50/min)**:
    - Allows bursty legitimate usage (mobile apps, web clients)
    - 50/min = ~0.83 requests/second sustained
    - Prevents abuse while allowing normal token refresh flows
    - 1-minute window accommodates burst behavior better than 15-minute window

## API Changes

None - rate limiting is internal middleware behavior with no API contract changes.

## Implementation Details

### Modified File: `cmd/api/main.go`

Replace the existing tokenLimiter section (lines 319-322) with:

```go
// Rate limiting (Valkey-backed; no-op when Valkey is disabled).
// Brute-force guard: count only failed responses per client IP.
// Throughput guard: count all requests per client IP to prevent abuse.
ipKey := utils.IP
authLimiter := middleware.RateLimit(middleware.RateLimitConfig{
Cache: valkeyClient, Prefix: "login", Max: middleware.FailedLoginMax,
Window: middleware.FailedLoginWindow, KeyFunc: ipKey, CountOnlyFailures: true, FailClosed: true,
})
pwResetLimiter := middleware.RateLimit(middleware.RateLimitConfig{
Cache: valkeyClient, Prefix: "pwreset", Max: middleware.FailedLoginMax,
Window: middleware.FailedLoginWindow, KeyFunc: ipKey, CountOnlyFailures: true, FailClosed: true,
})
tokenLimiterBruteForce := middleware.RateLimit(middleware.RateLimitConfig{
Cache: valkeyClient, Prefix: "token_brute", Max: middleware.FailedLoginMax,
Window: middleware.FailedLoginWindow, KeyFunc: ipKey, CountOnlyFailures: true, FailClosed: true,
})
tokenLimiterThroughput := middleware.RateLimit(middleware.RateLimitConfig{
Cache: valkeyClient, Prefix: "token_throughput", Max: 50,
Window: time.Minute, KeyFunc: ipKey, CountOnlyFailures: false, FailClosed: true,
})
// ... existing limiters for pwReset, passkey, google ...
v1.Use("/token", tokenLimiterBruteForce, tokenLimiterThroughput)
v1.Use("/revoke", tokenLimiterBruteForce, tokenLimiterThroughput)
```

## Testing Plan

### Unit Tests

- TestTokenEndpoint_BruteForceProtection: Verify 5 failed attempts triggers limiting
- TestTokenEndpoint_ThroughputLimiting: Verify 50 requests/minute triggers limiting
- TestTokenEndpoint_MixedSuccessFailure: Verify only failures count for brute-force limiter
- TestTokenEndpoint_SkipRateLimitCount: Verify benign failures don't consume brute-force budget
- TestTokenEndpoint_FailClosed: Verify requests blocked when Valkey unavailable

### Integration Tests

- TestTokenEndpoint_RateLimit_Integ: End-to-end rate limiting on token endpoint
- TestTokenEndpoint_GrantTypeLimits: Verify limits apply across all grant types
- TestTokenEndpoint_LegacyUsage: Verify existing valid usage patterns still work

### Specific Test Updates

Update existing tests in `oauth_security_test.go` that may be affected by enhanced limiting.

## Cross-Project Impact

### ctech-dfe

- Uses ctech-account for JWT validation
- Benefits from more robust token endpoint protection
- Reduced risk of token endpoint downtime due to abuse

### ctech-wallet

- Relies on ctech-account for authentication
- Inherits improved rate limiting protection
- More reliable authentication during attack scenarios

### ctech-ui

- May see fewer rate-related errors during development/testing
- No direct changes needed

## Deployment Considerations

### Rollout Strategy

1. Deploy with monitoring to observe impact on legitimate traffic
2. Verify no increase in 429 responses for normal usage patterns
3. Monitor Valkey key usage for new token_* prefixes
4. Adjust limits if needed based on observed metrics

### Backward Compatibility

- All existing valid token requests continue to work
- Only changes behavior for abusive/excessive request patterns
- No changes to token validation or response formats
- Safe to roll out without downtime

### Monitoring During Rollout

- Track: rate limited requests per endpoint, by IP
- Monitor: authentication success rates, token issuance rates
- Watch for: false positives (legitimate clients limited)
- Measure: reduction in abusive traffic patterns

## Documentation Updates

- Update API documentation to mention rate limiting on token endpoints
- Add notes to SECURITY.md about enhanced token endpoint protection
- Update runbook for investigating rate limiting events