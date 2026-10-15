#!/usr/bin/env bash
# Creates the wallet's client for ctech-billing (client_credentials) in one
# environment and stores its id and secret in the wallet's SSM namespace, read by
# the wallet API and its reconcile Lambda only.
# Runbook: ctech-wallet docs/runbooks/billing-rail-cutover.md § Checklist 2.
#
#   AWS_PROFILE=ctech api/cmd/createclient/create-wallet-billing-client.sh prod
#
# Run only after ctech-billing's scope manifest (with the payout scopes) is
# published in that environment. The secret goes straight to SSM and is printed
# only if the SSM write fails after the client was created (main.go's one
# recovery copy). The script refuses to run when either parameter already exists;
# re-running fails at create (the client exists): that is the guard.
set -euo pipefail
ENV="${1:?usage: $0 <dev|stage|prod>}"
case "$ENV" in dev | stage | prod) ;; *)
  echo "unknown environment: $ENV" >&2
  exit 2
  ;;
esac
for p in billing-client-id billing-client-secret; do
  if aws ssm get-parameter --region us-east-1 --name "/ctech-wallet/$ENV/$p" --query Parameter.Name --output text >/dev/null 2>&1; then
    echo "/ctech-wallet/$ENV/$p already exists; refusing (delete it first if the client is being recreated)" >&2
    exit 1
  fi
done
cd "$(dirname "$0")/../.."
unset VALKEY_URL
AWS_REGION=us-east-1 TABLE_PREFIX="$ENV" go run ./cmd/createclient \
  -client-id "wallet-billing" \
  -name "CTech Wallet (ctech-billing rail)" \
  -scopes "billing:customers:read,billing:customers:write,billing:invoices:read,billing:invoices:write,internal:billing:payouts-read,internal:billing:payouts-write" \
  -ssm-path-client "/ctech-wallet/$ENV/billing-client-id" \
  -ssm-path-secret "/ctech-wallet/$ENV/billing-client-secret"
aws ssm get-parameter --region us-east-1 --name "/ctech-wallet/$ENV/billing-client-id" --query Parameter.Value --output text
