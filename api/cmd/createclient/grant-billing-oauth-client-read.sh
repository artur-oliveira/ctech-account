#!/usr/bin/env bash
# Grants internal:account:oauth-client-read to ctech-billing's Account client in
# one environment. Kept for the runbook that names it; the work is
# grant-billing-scope.sh, which takes any internal scope.
#
#   AWS_PROFILE=ctech api/cmd/createclient/grant-billing-oauth-client-read.sh prod
set -euo pipefail

ENV="${1:?usage: $0 <dev|stage|prod>}"
exec "$(dirname "$0")/grant-billing-scope.sh" "$ENV" internal:account:oauth-client-read
