# P1-1: Implement Refresh Token Rotation in ctech-account

## Overview

Improve refresh token rotation to give each rotated token a fresh TTL, limiting the window of opportunity if a refresh
token is leaked.

## Current Implementation Analysis

The current `RotateClientToken` method in `internal/domain/session/service.go` correctly:

- Validates the presented refresh token
- Checks client ownership and token expiration
- Generates a new refresh token hash
- Updates the stored hash with conditional update to prevent race conditions
- Leaves a consumed token marker to detect replay attacks

However, it sets the new refresh token's expiration time to match the session's expiration time
(`ExpiresAt: sess.ExpiresAt`), rather than giving it a fresh TTL. This means:

- If a session has 90 days TTL and a refresh token is used on day 89
- After rotation, the new refresh token only has 1 day of validity (until session expires)
- Users near the end of their session window must re-authenticate frequently despite active refresh token usage

## Requirements

- Modify refresh token rotation to give each rotated token a fresh TTL
- Maintain all existing security properties (token reuse detection, client binding, etc.)
- Ensure rotated refresh tokens have same TTL as initial issue (90 days)
- Preserve backward compatibility with existing token validation
- Update all relevant tests to reflect new behavior

## Design Approach

### Change in `internal/domain/session/service.go`

In the `RotateClientToken` method, modify the refresh token creation to use a fresh expiration time:

```go
// Current code (line ~129):
ExpiresAt:        sess.ExpiresAt,

// Changed to:
ExpiresAt:        now.Add(SessionTTL).Unix(),
```

This ensures each rotated refresh token gets a full 90-day window from rotation time.

### Security Considerations

- **Token Reuse Protection**: Unchanged - consumed token marker still prevents replays
- **Client Binding**: Unchanged - token still bound to issuing client
- **Session Binding**: Unchanged - token still tied to parent session
- **Rotation Atomicity**: Unchanged - conditional update prevents race conditions
- **Window of Opportunity**: Reduced - leaked tokens now valid for full 90 days from theft, not just until session
  expiry

## API Changes

None - this is an internal implementation change with no API modifications.

## Implementation Details

### Modified Function

```go
func (s *Service) RotateClientToken(ctx context.Context, rawToken, clientID string) (*Session, string, []string, error) {
// ... existing validation code ...

newRaw, newHash, err := crypto.GenerateRefreshToken()
if err != nil {
return nil, "", nil, fmt.Errorf("generating new refresh token: %w", err)
}
// Capture the superseded hash before the rotation so the consumed-token
// marker below points at the old token, not the freshly written one.
oldHash := t.RefreshTokenHash
if err := s.repo.UpdateRefreshTokenHash(ctx, t.UserID(), t.SessionID, t.ClientID, newHash, oldHash); err != nil {
if errors.Is(err, ErrTokenReuse) {
return nil, "", nil, ErrTokenReuse
}
return nil, "", nil, fmt.Errorf("rotating refresh token: %w", err)
}
// Leave a marker linking the superseded hash to its session so a replay of
// the old token (token-reuse attempt) can revoke the now-compromised grant.
// Best-effort: a marker write failure must not fail the rotation, and reuse
// is still reported as such even if the marker is missing.
_ = s.repo.PutConsumedToken(ctx, t.UserID(), t.SessionID, t.ClientID, oldHash, sess.ExpiresAt)
return sess, newRaw, t.Scopes, nil
}
```

Note: The `RefreshToken` struct creation in `IssueClientToken` already uses fresh TTL:

```go
ExpiresAt:        now.Add(SessionTTL).Unix(),
```

So we only need to change the expiration time in `RotateClientToken`.

### Database Schema

No changes required - uses existing refresh token storage.

## Testing Plan

### Unit Tests

- TestRefreshTokenRotation_GetsFreshTTL: Verify rotated token has full SessionTTL from rotation time
- TestRefreshTokenRotation_PreservesSecurityProperties: Confirm reuse detection, client binding still work
- TestRefreshTokenRotation_EdgeCases: Test near session expiry, concurrent rotations

### Integration Tests

- TestRefreshTokenFlow_WithRotation: End-to-end flow issuing, using, and rotating refresh tokens
- TestRefreshTokenSecurity_ReplayAfterRotation: Confirm old tokens are properly invalidated
- TestRefreshToken_Binding: Verify tokens remain bound to correct client/session

### Specific Test Updates

Update existing tests that assume current behavior:

- `TestRotateClientToken_SingleUseHappyPath`: May need adjustment for new expiration time
- Any tests checking token expiration times

## Cross-Project Impact

### ctech-dfe

- Relies on ctech-account for JWT validation
- No direct impact - continues to validate access tokens as before
- Indirect benefit: reduced frequency of forced re-authentications improves user experience

### ctech-wallet

- Relies on ctech-account for authentication
- No integration changes required
- Improved reliability for long-running wallet sessions

### ctech-ui

- May see reduced login prompts for long-lived sessions
- No code changes required
- Improved user experience for SPA silent refresh flows

### ctech-oauth-client

- No changes needed (uses JWT from ctech-account)
- Inherits improvement indirectly

## Deployment Considerations

### Rollout Strategy

1. Deploy with feature flag (optional) or directly as backward compatible change
2. Monitor token validity periods in logs/metrics
3. Verify no increase in authentication failures
4. Check that refresh token rotation works as expected in staging

### Backward Compatibility

- All existing valid refresh tokens continue to work
- No changes to token validation logic
- Only affects newly rotated tokens' expiration times
- Safe to roll out without downtime

### Monitoring During Rollout

- Track: average refresh token lifetime, rotation frequency
- Monitor: authentication success rates, silent refresh success rates
- Watch for: any increase in "invalid token" errors (should decrease)
- Measure: reduction in forced re-authentications near session expiry

## Documentation Updates

- Update internal documentation describing refresh token behavior
- Add comments to `RotateClientToken` explaining fresh TTL behavior
- No API documentation changes needed (no public API changes)