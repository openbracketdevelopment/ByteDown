#!/usr/bin/env bash
set -euo pipefail
RESULTS_DIR="${RESULTS_DIR:-examples/benchmark-results/$(date -u +%Y%m%dT%H%M%SZ)}"
export RESULTS_DIR
mkdir -p "$RESULTS_DIR"
chmod 700 "$RESULTS_DIR"
{
  date -u +%FT%TZ
  uname -srm
  command -v lscpu >/dev/null && lscpu | grep -E 'Architecture:|CPU\(s\):|Model name:' || true
  command -v docker >/dev/null && docker version --format '{{.Server.Version}}' || true
  printf 'WORKER_CONCURRENCY=%s VIPS_CONCURRENCY=%s\n' "${WORKER_CONCURRENCY:-1}" "${VIPS_CONCURRENCY:-1}"
  printf 'Client observations only; record server CPU/RAM, image digest and profile separately.\n'
} > "$RESULTS_DIR/environment.txt"
start="$(date +%s)"
bash "$(dirname -- "${BASH_SOURCE[0]}")/demo.sh" "$@" | tee "$RESULTS_DIR/summary.txt"
end="$(date +%s)"
printf 'observed_wall_seconds=%s\n' "$((end - start))" | tee -a "$RESULTS_DIR/summary.txt"
