# P1-9: Implement Account Lockout After Failed Attempts in ctech-account

## Overview

Implement account lockout functionality to complement IP-based brute-force protection by tracking failed login attempts
per account and locking accounts after excessive failed attempts.

## Requirements

- Track failed login attempts per account (email/username)
- Configurable lockout threshold (default: 5 failed attempts)
- Configurable lockout duration (default: 15 minutes)
- Automatic unlock after lockout period expires
- Manual unlock capability for administrators/support
- Integration with existing login flow without user enumeration
- Secure error messaging that doesn't reveal account existence
- Metrics and monitoring for lockout events
- Protection against credential stuffing attacks
- Audit logging for lockout/unlock events
- Consideration for distributed systems (multiple API instances)

## Design Approach

### Dual Protection Strategy

Maintain existing IP-based rate limiting (5 failed logins/15min/IP) AND add account-based lockout for defense in depth.

### Implementation Location

- Failed attempt tracking: Valkey (for persistence and sharing across API instances)
- Lockout logic: Middleware layer (before authentication)
- Storage: Valkey with appropriate TTL for automatic expiration

## Data Model (Valkey Keys)

### Failed Attempts Counter

- Key: `auth:failed_attempts:{normalized_identifier}`
- Type: Integer
- TTL: Lockout duration (automatically clears after lockout period)
- Incremented on each failed login attempt
- Identifier: normalized email or username (lowercase, trimmed)

### Lockout Status

- Key: `auth:lockout:{normalized_identifier}`
- Type: String (timestamp of lockout expiration)
- TTL: Lockout duration
- Set when failed attempts threshold reached
- Present indicates account is currently locked

### Audit Trail (Optional)

- Key: `auth:lockout:audit:{timestamp}:{normalized_identifier}`
- Type: String (JSON with event details)
- TTL: 30 days (for compliance)
- Events: lockout_occurred, lockout_expired, manual_unlock

## API Changes

### Middleware Integration

Add account lockout check in authentication middleware before credential validation:

1. Normalize identifier (email/username)
2. Check if account is currently locked (Valkey GET lockout key)
3. If locked, return generic authentication error
4. If not locked, proceed with credential validation
5. On failed validation: increment failed attempts counter
6. On successful validation: reset failed attempts counter to zero

### Error Responses

- All authentication failures return identical generic message:
  "Invalid email or password"
- This prevents user enumeration via lockout timing differences
- Locked accounts and non-existent accounts both return same message
- Correct credentials on locked account: same generic message
- Correct credentials on unlocked account: successful login

## Configuration

### Lockout Settings (via environment or SSM)

- `ACCOUNT_LOCKOUT_ENABLED`: boolean (default: true)
- `ACCOUNT_LOCKOUT_THRESHOLD`: integer (default: 5)
- `ACCOUNT_LOCKOUT_DURATION_MINUTES`: integer (default: 15)
- `ACCOUNT_LOCKOUT_AUDIT_ENABLED`: boolean (default: true)

### Values Rationale

- Threshold of 5: balances security with usability (allows for typos)
- Duration of 15 minutes: sufficient to deter brute-force attacks
- Shorter durations frustrate legitimate users; longer increase support burden

## Implementation Details

### Normalization

- Email: lowercase, trim whitespace
- Username: lowercase, trim whitespace
- Consistent normalization prevents bypass via case/whitespace variations

### Valkey Operations

Use atomic operations where possible:

- INCR for failed attempts counter
- SET with NX and EX for lockout (avoids race conditions)
- GET for lockout status check
- DEL on successful login to reset counter
- EXPIRE to ensure keys don't persist indefinitely

### Middleware Implementation

```go
func AccountLockoutMiddleware() fiber.Handler {
return func (c *fiber.Ctx) error {
// Skip for non-auth endpoints
if !isAuthEndpoint(c.Path()) {
return c.Next()
}

// Extract identifier from request
identifier, err := extractIdentifier(c)
if err != nil {
// If we can't extract identifier, proceed (will fail later)
return c.Next()
}

normalized := normalizeIdentifier(identifier)

// Check lockout status
lockoutKey := fmt.Sprintf("auth:lockout:%s", normalized)
isLocked, err := valkeyClient.Exists(c.Context(), lockoutKey)
if err != nil {
// Fail closed on Valkey error
return apierror.ServiceUnauthorized("temporarily unavailable").Send(c)
}

if isLocked > 0 {
// Account is locked - return generic error to prevent enumeration
return apierror.InvalidCredentials().Send(c)
}

// Proceed with normal authentication flow
err = c.Next()

// Handle result after downstream handlers run
if err != nil {
// Check if this is a credential failure
if isCredentialError(err) {
// Increment failed attempts
attemptsKey := fmt.Sprintf("auth:failed_attempts:%s", normalized)
newCount, err := valkeyClient.Incr(c.Context(), attemptsKey)
if err != nil {
// Log error but don't block login attempt
slog.Warn("failed to increment auth attempts", "err", err)
} else {
// Check if threshold reached
if newCount >= lockoutThreshold {
// Set lockout
lockoutUntil := time.Now().Add(lockoutDuration)
lockoutKey := fmt.Sprintf("auth:lockout:%s", normalized)
valkeyClient.Set(c.Context(), lockoutKey, lockoutUntil.Format(time.RFC3339), lockoutDuration)

// Audit if enabled
if lockoutAuditEnabled {
auditLockout(normalized, lockoutUntil)
}
}
}
}
} else {
// Successful login - reset counter
attemptsKey := fmt.Sprintf("auth:failed_attempts:%s", normalized)
valkeyClient.Del(c.Context(), attemptsKey)

// Audit successful unlock if was previously locked
// (could check previous state, but simpler to audit on clear)
}

return err
}
}
```

## Security Considerations

### Prevention of User Enumeration

- Identical error messages for: wrong credentials, non-existent account, locked account
- Similar response timing (avoid early exits that leak information)
- Consistent HTTP status codes (401 for all auth failures)

### Protection Against Attacks

- **Brute Force**: Threshold and duration limit guessing attempts
- **Credential Stuffing**: Lockout prevents rapid testing of credential lists
- **Lockout Denial-of-Service**:
    - Legitimate users can wait out lockout period
    - Administrators can manually unlock
    - Consider implementing gradual lockout increases for persistent offenders
- **Timing Attacks**: Constant-time comparison for credential validation already in place

### Administrative Features

#### Manual Unlock Endpoint

- POST /v1.0/internal/unlock/{identifier}
- Requires admin or support role
- Removes lockout key and failed attempts counter
- Returns success/failure
- Audit logged

#### Lockout Status Check

- GET /v1.0/internal/lockout-status/{identifier}
- Requires admin or support role
- Returns: { is_locked: boolean, unlocks_at: timestamp?, failed_attempts: integer }
- Does not reveal whether account exists (returns same structure for non-existent)

### Audit Logging

Events to audit:

- Lockout threshold reached (with timestamp and identifier)
- Lockout expired (automatic)
- Manual unlock performed
- Failed login attempts (rate-limited to prevent log spam)
- Configuration changes

## Integration Points

### Middleware Chain

Insert after request ID and logger, before authentication middleware:

1. RequestID
2. Logger
3. AccountLockoutMiddleware (new)
4. RequireAuth/OptionalAuth middleware
5. Route handlers

### Configuration Loading

- Read lockout parameters from config.Load () alongside other settings
- Provide sensible defaults
- Allow override via environment variables/SSM

### Metrics and Monitoring

- Counter: account_lockout_total (labels: reason:threshold_reached,manual)
- Gauge: account_locked_accounts_current
- Histogram: account_lockout_duration_minutes
- Alert on: sudden increase in lockout events
- Dashboard: lockout events over time, top locked accounts

## Testing Plan

### Unit Tests

- Identifier normalization (email, username, edge cases)
- Lockout threshold logic (exactly at threshold, over threshold)
- Automatic expiration behavior
- Manual unlock functionality
- Error message consistency

### Integration Tests

- End-to-end lockout flow with failed attempts
- Successful login after lockout period
- Concurrent login attempts from multiple IPs
- Interaction with existing IP-based rate limiting
- Valkey failure scenarios (fail closed behavior)

### Security Tests

- User enumeration attempt via timing analysis
- Brute force attack simulation
- Credential stuffing attack with known breached passwords
- Lockout bypass attempts (race conditions, header manipulation)

## Cross-Project Impact

### ctech-wallet

- Relies on ctech-account for authentication
- No direct changes needed, inherits lockout protection
- May see reduction in fraudulent login attempts

### ctech-dfe

- Relies on ctech-account for authentication via JWT
- Inherits protection for API access
- No integration changes required

### ctech-ui

- May need to update error handling to show generic messages
- Should not attempt to distinguish lockout from bad credentials
- May add "try again later" messaging after multiple failures

### ctech-oauth-client

- No changes needed (uses JWT from ctech-account)
- Inherits protection indirectly

## Deployment Considerations

### Rollout Strategy

1. Deploy with feature flag disabled (default off)
2. Enable in staging for testing
3. Gradual rollout to production via percentage-based rollout
4. Monitor lockout events and legitimate user impact
5. Adjust threshold/duration based on observed data

### Backward Compatibility

- No breaking changes to existing APIs
- Feature flag allows easy rollback if issues discovered
- Does not affect existing successful login flows

### Monitoring During Rollout

- Track: new lockouts per hour, unique accounts locked
- Monitor: support tickets related to login issues
- Watch for: false positives (legitimate users locked out)
- Measure: reduction in suspicious login attempts

## Documentation Updates

- Update API documentation for new internal endpoints
- Update security documentation to describe lockout protection
- Add runbook procedures for investigating lockout events
- Include in troubleshooting guide for "account locked" scenarios
- Update CONFIGURATION.md with new lockout parameters