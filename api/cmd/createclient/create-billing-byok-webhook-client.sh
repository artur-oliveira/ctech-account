#!/usr/bin/env bash
# Creates ctech-billing's BYOK webhook Lambda client (client_credentials, scope
# internal:billing:byok-notify) in one environment and stores its id and secret
# in billing's SSM namespace, read only by that Lambda.
# Runbook: ctech-billing docs/runbooks/byok.md § Webhook client.
#
#   AWS_PROFILE=ctech api/cmd/createclient/create-billing-byok-webhook-client.sh prod
#
# Run only after ctech-billing's scope manifest with internal:billing:byok-notify
# is published in that environment. The secret goes straight to SSM; it is
# printed only if the SSM write fails after the client was created (main.go's
# one recovery copy: store it, then clear the scrollback). To make that path
# unlikely, the script refuses to run when either parameter already exists.
# Re-running fails at create (the client exists): that is the guard.
# Recovery if only the client-id write failed (the id is deterministic):
#   aws ssm put-parameter --name /ctech-billing/$ENV/byok-webhook/client-id --type String --value billing-byok-webhook-$ENV
set -euo pipefail
ENV="${1:?usage: $0 <dev|stage|prod>}"
case "$ENV" in dev | stage | prod) ;; *)
  echo "unknown environment: $ENV" >&2
  exit 2
  ;;
esac
for p in client-id client-secret; do
  if aws ssm get-parameter --region us-east-1 --name "/ctech-billing/$ENV/byok-webhook/$p" --query Parameter.Name --output text >/dev/null 2>&1; then
    echo "/ctech-billing/$ENV/byok-webhook/$p already exists; refusing (delete it first if the client is being recreated)" >&2
    exit 1
  fi
done
cd "$(dirname "$0")/../.."
unset VALKEY_URL
AWS_REGION=us-east-1 TABLE_PREFIX="$ENV" go run ./cmd/createclient \
  -client-id "billing-byok-webhook-$ENV" \
  -name "CTech Billing BYOK webhook ($ENV)" \
  -scopes "internal:billing:byok-notify" \
  -ssm-path-client "/ctech-billing/$ENV/byok-webhook/client-id" \
  -ssm-path-secret "/ctech-billing/$ENV/byok-webhook/client-secret"
aws ssm get-parameter --region us-east-1 --name "/ctech-billing/$ENV/byok-webhook/client-id" --query Parameter.Value --output text
