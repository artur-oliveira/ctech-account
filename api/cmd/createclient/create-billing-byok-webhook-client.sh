#!/usr/bin/env bash
# Creates ctech-billing's BYOK webhook Lambda client (client_credentials, scope
# internal:billing:byok-notify) in one environment and stores its id and secret
# in billing's SSM namespace, read only by that Lambda.
# Runbook: ctech-billing docs/runbooks/byok.md § Webhook client.
#
#   AWS_PROFILE=ctech api/cmd/createclient/create-billing-byok-webhook-client.sh prod
#
# Run only after ctech-billing's scope manifest with internal:billing:byok-notify
# is published in that environment. Never prints the secret (it goes straight
# to SSM). Re-running fails at create (the client exists): that is the guard.
set -euo pipefail
ENV="${1:?usage: $0 <dev|stage|prod>}"
case "$ENV" in dev | stage | prod) ;; *)
  echo "unknown environment: $ENV" >&2
  exit 2
  ;;
esac
cd "$(dirname "$0")/../.."
unset VALKEY_URL
AWS_REGION=us-east-1 TABLE_PREFIX="$ENV" go run ./cmd/createclient \
  -client-id "billing-byok-webhook-$ENV" \
  -name "CTech Billing BYOK webhook ($ENV)" \
  -scopes "internal:billing:byok-notify" \
  -ssm-path-client "/ctech-billing/$ENV/byok-webhook/client-id" \
  -ssm-path-secret "/ctech-billing/$ENV/byok-webhook/client-secret"
