#!/usr/bin/env bash
# Creates and destroys only its uniquely named disposable Compose project.
set -euo pipefail
for tool in docker curl jq base64 file cmp openssl; do
  command -v "$tool" >/dev/null || { echo "Missing tool: $tool" >&2; exit 1; }
done
project="bytedown-livecheck-$$"
export API_KEY="$(openssl rand -hex 32)" POSTGRES_PASSWORD="$(openssl rand -hex 32)"
export PUBLIC_VARIANTS=false PROFILES_JSON= MAX_OUTPUT_BYTES=104857600 PROCESS_TIMEOUT=120s
export RESULTS_DIR="${RESULTS_DIR:-examples/live-results}"
export BASE_URL=http://127.0.0.1:8080
authorization="$(printf '%s %s' Bearer "$API_KEY")"
mkdir -p "$RESULTS_DIR"
chmod 700 "$RESULTS_DIR"
compose() { docker compose -p "$project" "$@"; }
curl() { command curl --connect-timeout 5 --max-time 30 "$@"; }
cleanup() {
  result=$?
  if (( result != 0 )); then compose logs --tail=80 api worker migrate >&2 || true; fi
  compose down -v --remove-orphans >&2 || true
  exit "$result"
}
trap cleanup EXIT
docker version --format '{{.Client.Version}} / {{.Server.Version}}' > "$RESULTS_DIR/docker-version.txt"
uname -srm > "$RESULTS_DIR/environment.txt"
if [[ "${SKIP_GO_CHECKS:-false}" != true ]]; then
  docker build --quiet --target build -t bytedown:ci-native-check .
fi
compose up -d --wait db
test_url="postgres://bytedown:${POSTGRES_PASSWORD}@db:5432/bytedown?sslmode=disable"
if [[ "${SKIP_GO_CHECKS:-false}" != true ]]; then
  docker run --rm --network "${project}_default" \
    -e TEST_DATABASE_URL="$test_url" -e VIPS_CONCURRENCY=1 \
    bytedown:ci-native-check sh -c 'go test ./... && go vet ./...' \
    | tee "$RESULTS_DIR/go-checks.txt"
fi
compose build --quiet
docker run --rm bytedown:local probe
compose up -d --wait --wait-timeout 90 api
compose up -d worker
compose ps -a > "$RESULTS_DIR/compose-status.txt"
docker image inspect bytedown:local --format '{{.Id}} uid={{.Config.User}}' > "$RESULTS_DIR/image.txt"
curl --silent --show-error --fail "$BASE_URL/health/ready" > "$RESULTS_DIR/readiness.json"
code="$(curl --silent --output /dev/null --write-out '%{http_code}' "$BASE_URL/v1/images")"
[[ "$code" == 401 ]] || { echo "Unauthenticated management endpoint was not rejected" >&2; exit 1; }
root_results="$RESULTS_DIR"
for format in png jpg; do
  base64 --decode "examples/fixture.$format.base64" > "$root_results/input.$format"
  RESULTS_DIR="$root_results/$format" bash examples/demo.sh "$root_results/input.$format"
  jq -e '.status == "succeeded" and .generated == .planned and .duration_ms >= 0' \
    "$root_results/$format/job.json" >/dev/null
  manifest="$root_results/$format/manifest.json"
  for headers in "$root_results/$format"/*.headers; do
    grep -Eiq '^x-content-type-options:[[:space:]]*nosniff[[:space:]]*$' "$headers"
    grep -Eiq '^cache-control:[[:space:]]*private,' "$headers"
  done
  image_id="$(jq -er .id "$manifest")"
  variant_url="$(jq -er '.outputs[0].url' "$manifest")"
  code="$(curl --silent --output /dev/null --write-out '%{http_code}' "$BASE_URL$variant_url")"
  [[ "$code" == 401 ]] || { echo "Default variant was not private" >&2; exit 1; }
  code="$(curl --silent --output /dev/null --write-out '%{http_code}' \
    -H "Authorization: $authorization" -X DELETE "$BASE_URL/v1/images/$image_id")"
  [[ "$code" == 204 ]] || { echo "Delete failed: $code" >&2; exit 1; }
  for path in "/v1/images/$image_id" "/v1/images/$image_id/original" "$variant_url"; do
    code="$(curl --silent --output /dev/null --write-out '%{http_code}' \
      -H "Authorization: $authorization" "$BASE_URL$path")"
    [[ "$code" == 404 ]] || { echo "Deleted resource remains accessible: $path ($code)" >&2; exit 1; }
  done
done
# The API accepts a valid upload, but the worker's deliberately impossible
# output-byte limit must fail the job without publishing partial variants.
MAX_OUTPUT_BYTES=1 PROCESS_TIMEOUT=10s compose up -d --no-deps --force-recreate worker
curl --silent --show-error --fail -H "Authorization: $authorization" \
  -F "file=@$root_results/input.png" -F profile=web "$BASE_URL/v1/images" > "$root_results/failed-upload.json"
status_url="$(jq -er .status_url "$root_results/failed-upload.json")"
deadline=$((SECONDS + 30))
while :; do
  (( SECONDS < deadline )) || { echo "Failed-processing test exceeded 30 seconds" >&2; exit 1; }
  curl --silent --show-error --fail --max-time 2 -H "Authorization: $authorization" \
    "$BASE_URL$status_url" > "$root_results/failed-job.json"
  status="$(jq -er .status "$root_results/failed-job.json")"
  [[ "$status" != succeeded ]] || { echo "Output byte limit was not enforced" >&2; exit 1; }
  [[ "$status" != failed ]] || break
  sleep 1
done
jq -e '.status == "failed" and .generated == 0 and (.error_code | length > 0)' "$root_results/failed-job.json" >/dev/null
image_id="$(jq -er .image_id "$root_results/failed-upload.json")"
curl --silent --show-error --fail -H "Authorization: $authorization" \
  "$BASE_URL/v1/images/$image_id" > "$root_results/failed-manifest.json"
jq -e '(.outputs // [] | length) == 0' "$root_results/failed-manifest.json" >/dev/null
curl --silent --show-error --fail -H "Authorization: $authorization" \
  "$BASE_URL/v1/images/$image_id/original" > "$root_results/failed-original.png"
cmp "$root_results/input.png" "$root_results/failed-original.png"
printf 'PASS: native probe, Compose health, PNG/JPEG originals/variants, private access, deletion, bounded failed processing.\n'
