#!/usr/bin/env bash
# Grants one internal scope to ctech-billing's Account client in one environment,
# then shows the client's allowed_scopes (read-only check).
# Runbook: docs/plans/2026-10-10-oauth-client-internal-route.md § Deploy, steps 2-3.
#
#   AWS_PROFILE=ctech api/cmd/createclient/grant-billing-scope.sh prod internal:account:company-actor
#
# Run it only after the ctech-account deploy that publishes the scope (and any
# route behind it) is live in that environment. It is add-only and idempotent: a
# re-run prints "no change: every scope was already granted". VALKEY_URL is left
# unset on purpose, so the scope catalog is read straight from DynamoDB (no stale
# cache). It prints the client_id (not a secret) and never reads or prints any
# secret.
set -euo pipefail

ENV="${1:?usage: $0 <dev|stage|prod> <internal:account:...>}"
SCOPE="${2:?usage: $0 <dev|stage|prod> <internal:account:...>}"
case "$ENV" in dev | stage | prod) ;; *)
  echo "unknown environment: $ENV" >&2
  exit 2
  ;;
esac
# Exactly one scope: createclient -scopes splits on commas, so a list here would
# grant more than this runner says it does.
if [[ ! "$SCOPE" =~ ^internal:account:[a-z][a-z-]*$ ]]; then
  echo "not exactly one internal account scope: $SCOPE" >&2
  exit 2
fi

REGION=us-east-1
PARAM="/ctech-billing/$ENV/billing/account-client-id"

CLIENT_ID=$(aws ssm get-parameter --region "$REGION" --name "$PARAM" \
  --query Parameter.Value --output text)
if [[ -z "$CLIENT_ID" || "$CLIENT_ID" == "None" ]]; then
  echo "no client id at $PARAM" >&2
  exit 1
fi

cd "$(dirname "$0")/../.."
unset VALKEY_URL
AWS_REGION="$REGION" TABLE_PREFIX="$ENV" go run ./cmd/createclient -grant \
  -client-id "$CLIENT_ID" -scopes "$SCOPE"

echo "allowed_scopes of $CLIENT_ID in ${ENV}_account_oauth_clients:"
aws dynamodb get-item --region "$REGION" --table-name "${ENV}_account_oauth_clients" \
  --key "{\"pk\":{\"S\":\"CLIENT_$CLIENT_ID\"}}" \
  --projection-expression allowed_scopes --output json
