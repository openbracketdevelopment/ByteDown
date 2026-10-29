#!/usr/bin/env bash
set -euo pipefail
for tool in curl jq base64 file cmp; do
  command -v "$tool" >/dev/null || { echo "Missing tool: $tool" >&2; exit 1; }
done
: "${API_KEY:?Export API_KEY from your .env}"
authorization="$(printf '%s %s' Bearer "$API_KEY")"
BASE_URL="${BASE_URL:-http://127.0.0.1:8080}"
BASE_URL="${BASE_URL%/}"
RESULTS_DIR="${RESULTS_DIR:-examples/results}"
mkdir -p "$RESULTS_DIR"
chmod 700 "$RESULTS_DIR"
script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
if [[ $# -gt 0 ]]; then
  input="$1"
else
  input="$RESULTS_DIR/sample.png"
  base64 --decode "$script_dir/fixture.png.base64" > "$input"
fi
request() {
  local path="$1"
  shift
  [[ "$path" == /* && "$path" != //* ]] || { echo "Refusing non-local API path" >&2; return 1; }
  curl --silent --show-error --fail-with-body --connect-timeout 5 --max-time "${CURL_MAX_TIME:-30}" \
    -H "Authorization: $authorization" "$@" "$BASE_URL$path"
}
request /v1/images -F "file=@$input" -F profile=web > "$RESULTS_DIR/upload.json"
jq -e '.image_id and .job_id and .status_url' "$RESULTS_DIR/upload.json" >/dev/null
image_id="$(jq -er .image_id "$RESULTS_DIR/upload.json")"
status_url="$(jq -er .status_url "$RESULTS_DIR/upload.json")"
deadline=$((SECONDS + 120))
while :; do
  remaining=$((deadline - SECONDS))
  (( remaining > 0 )) || { echo "Job did not finish within 120s" >&2; exit 1; }
  (( remaining <= 30 )) || remaining=30
  CURL_MAX_TIME="$remaining" request "$status_url" > "$RESULTS_DIR/job.json"
  state="$(jq -er .status "$RESULTS_DIR/job.json")"
  case "$state" in
    succeeded) break ;;
    failed) cat "$RESULTS_DIR/job.json" >&2; exit 1 ;;
    queued|processing) ;;
    *) echo "Unexpected job state: $state" >&2; exit 1 ;;
  esac
  (( SECONDS < deadline )) || { echo "Job did not finish within 120s" >&2; exit 1; }
  sleep 1
done
request "/v1/images/$image_id" > "$RESULTS_DIR/manifest.json"
jq -e '
  .original.width > 0 and .original.height > 0 and .original.bytes > 0
  and (.outputs | length > 0)
  and all(.outputs[]; .width > 0 and .height > 0 and .bytes > 0)
' "$RESULTS_DIR/manifest.json" >/dev/null
request "$(jq -er .original_url "$RESULTS_DIR/manifest.json")" -o "$RESULTS_DIR/original"
cmp "$input" "$RESULTS_DIR/original"
index=0
while IFS= read -r output; do
  index=$((index + 1))
  format="$(jq -er .format <<<"$output")"
  case "$format" in
    jpeg|jpg) expected_mime=image/jpeg ;;
    png) expected_mime=image/png ;;
    webp) expected_mime=image/webp ;;
    avif) expected_mime=image/avif ;;
    *) echo "Unexpected output format: $format" >&2; exit 1 ;;
  esac
  destination="$RESULTS_DIR/variant-$index.$format"
  request "$(jq -er .url <<<"$output")" -D "$destination.headers" -o "$destination"
  expected_bytes="$(jq -er .bytes <<<"$output")"
  actual_bytes="$(wc -c < "$destination" | tr -d '[:space:]')"
  [[ "$actual_bytes" == "$expected_bytes" ]] || { echo "Byte count mismatch" >&2; exit 1; }
  actual_mime="$(file --brief --mime-type "$destination")"
  [[ "$actual_mime" == "$expected_mime" ]] || { echo "File signature mismatch: $actual_mime" >&2; exit 1; }
  grep -Eiq "^content-type:[[:space:]]*$expected_mime([;[:space:]]|$)" "$destination.headers" \
    || { echo "Content-Type mismatch" >&2; exit 1; }
done < <(jq -c '.outputs[]' "$RESULTS_DIR/manifest.json")
jq '{status, generated, planned, duration_ms}' "$RESULTS_DIR/job.json"
jq '{image_id: (.image_id // .id), original, original_url,
  outputs: [.outputs[] | {id, format, width, height, bytes, smaller,
    size_difference_percent, includes_resizing, url}]}' "$RESULTS_DIR/manifest.json"
printf 'Verified original equality and %s variant sizes/signatures/headers. Results: %s\n' "$index" "$RESULTS_DIR"
