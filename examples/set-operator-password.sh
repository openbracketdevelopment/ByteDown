#!/usr/bin/env bash
# Run from the repository root after docker build -t bytedown:local .
set -euo pipefail
[[ -f .env ]] || { echo 'Create .env with POSTGRES_PASSWORD and API_KEY first.' >&2; exit 1; }
IFS= read -r -s -p 'Operator password (at least 12 characters): ' operator_password
printf '\n'
IFS= read -r -s -p 'Confirm password: ' confirmation
printf '\n'
[[ "$operator_password" == "$confirmation" ]] || { echo 'Passwords do not match.' >&2; exit 1; }
password_hash="$(printf '%s' "$operator_password" | docker run --rm -i --entrypoint hashpassword bytedown:local)"
unset operator_password confirmation
[[ "$password_hash" == 'pbkdf2-sha256$600000$'* ]] || { echo 'Could not generate password hash.' >&2; exit 1; }
umask 077
temporary="$(mktemp .env.operator.XXXXXX)"
trap 'rm -f "$temporary"' EXIT
awk '!/^OPERATOR_PASSWORD_HASH=/' .env > "$temporary"
printf "OPERATOR_PASSWORD_HASH='%s'\n" "$password_hash" >> "$temporary"
mv "$temporary" .env
printf 'Operator password configured. Restart the API with: docker compose up -d api\n'
