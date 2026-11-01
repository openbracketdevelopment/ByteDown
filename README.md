# ByteDown

**Smaller files. More possibilities.**

Self-hosted, asynchronous JPEG/PNG optimization with preserved originals,
optimized original-format copies, and responsive WebP/AVIF outputs. Go, libvips,
PostgreSQL, and local disk; MIT-licensed. For a single operator and trusted
server-side applications, not a hosted multi-tenant platform.

Smaller is measured, not guaranteed. A successful job may contain an output
larger than its input. Quality numbers are codec-specific, and resizing is
reported separately from same-size compression. No universal savings or
production-readiness claim is made.

## API reference

Open **http://127.0.0.1:8080/api-docs** for the browsable developer reference,
with keyboard-accessible cURL/Node/Python examples, copy buttons, endpoint details,
authentication, limits, and error guidance. `/openapi.yaml` remains the downloadable
machine-readable contract. Both are served locally without third-party scripts.
The refined **Byte Fold** logo combines a folded lowercase b and a negative-space
down arrow; its source is `web/public/logo.svg`.

JPEG exports with a recognized, length-prefixed OnePlus metadata trailer are
supported. Arbitrary trailing bytes and multi-picture JPEG remain rejected; the
original camera file is retained byte-for-byte. Validation failures now report
safe, actionable reasons for dimension, color, precision, and structural problems.

## Web interface

ByteDown includes a Svelte/TypeScript image workshop and an original SVG logo:
two image tiles form a **B** with a downward arrow. The Go API serves the compiled
assets; production requires no Node server. All optimization stays in Go/libvips,
and existing bearer API clients work independently of the interface.

For a new installation, create credentials once (do not overwrite an existing `.env`):

```bash
(umask 077; printf 'POSTGRES_PASSWORD=%s\nAPI_KEY=%s\n' \
  "$(openssl rand -hex 32)" "$(openssl rand -hex 32)" > .env)
docker build -t bytedown:local .
bash examples/set-operator-password.sh
docker compose up --build --wait --wait-timeout 240 api
docker compose up -d worker
```

Open **http://127.0.0.1:8080** and enter the operator password. For an existing
installation, keep `.env`, build the image, run the password setup script, and
start API/worker with the commands above. The script prompts invisibly, hashes
the password in the container, and writes a single-quoted hash to `.env` so Compose
preserves its dollar signs. It requires Bash and Docker. Passwords must have at
least twelve characters. There is no registration or password recovery; rerun
the setup script to change the password, then recreate the API container.

The UI accepts up to ten JPG/PNG files and uploads two at a time. Limits come
from `/v1/capabilities` (default 20 MiB, 40 megapixels, 20,000 pixels per dimension).
Server validation rejects corrupt, animated, or unsupported inputs individually.
Select files, then click **Optimize pending**. Job progress reports actual output
counts. Primary cards compare matching source dimensions; **More sizes** contains
responsive variants. Missing outputs and size increases are shown explicitly.
Previews load on demand, preserve aspect ratio, and show a transparency grid.
The source remains available separately as **Untouched original**.

Individual downloads use the operator cookie. ZIPs contain all generated outputs
for completed selected images, including responsive sizes, but never originals.
Archives stream from the server with limits of ten images, 160 entries and
512 MiB. ID-based folders and output IDs avoid collisions; the backend does not
persist uploaded filenames, so ZIP names use IDs and dimensions. Browser individual
downloads use the local filename when available.

**Files remain on this server until explicitly deleted.** Clear finished removes
items only from the browser view. Delete from server requires confirmation and
uses the existing deletion API, including active-job publication protection.
There is no automatic expiry. Recent job identifiers and display filenames are
kept in sessionStorage; credentials are never stored there. Unsubmitted files
cannot be restored after a reload. Interrupted uploads can be retried with their
original idempotency key while the selected File is still available.

Operator sessions use HttpOnly, SameSite=Strict cookies and CSRF tokens; they
expire after eight hours or an API restart. HTTPS sets Secure cookies. At a TLS
reverse proxy, preserve Host and **overwrite** X-Forwarded-Proto with the actual
scheme; keep the API's upstream port private. Session state is in memory, so run
one API instance or use sticky sessions. Login attempts are globally limited to
one per second, with at most 128 active sessions. The administrative bearer key
stays server-side. `PUBLIC_VARIANTS` retains its existing meaning; originals stay
private. A public multiuser demo needs additional quotas, isolation, per-client
rate limits, and retention controls.

Set `UI_ENABLED=false` in `.env` and recreate the API to disable all browser UI
and session routes while preserving the headless API. `API_PORT` optionally
changes the host port (default 8080).

Frontend development (Node 22.15+):

```bash
cd web
npm ci
npm run dev       # http://127.0.0.1:5173; proxies API/session requests to :8080
npm run check
npm test
npm run build
```

Run the Docker API/worker alongside the Vite development server. Its proxy
rewrites Host/Origin consistently for the backend's same-origin checks. Static
assets are embedded at Go build time: for a native build, run `npm run build` in
`web/` first, then `go build ./cmd/bytedown`. Optional build-time
`VITE_BATCH_LIMIT=1..10` and `VITE_UPLOAD_CONCURRENCY=1..2` lower client batch/upload
limits. Docker builds the frontend automatically. Run full isolated acceptance
checks without disturbing an existing instance using:

```bash
BYTEDOWN_TEST_PORT=18089 bash examples/test-live.sh
```

This runs Go/native/database tests, checks browser sessions/CSRF and ZIPs, and
exercises the bearer API independently. Only its disposable Compose project's
volumes are removed. `node examples/test-examples.mjs` checks example contracts.

## Start locally

Requires Docker Engine with Compose v2, OpenSSL, and Bash. The demo also needs
`curl`, `jq`, `base64`, `file`, and `cmp`.

```bash
cp .env.example .env
chmod 600 .env
printf 'POSTGRES_PASSWORD=%s\nAPI_KEY=%s\n' \
  "$(openssl rand -hex 32)" "$(openssl rand -hex 32)" > .env
docker compose up --build --wait --wait-timeout 240 api
docker compose up -d worker
docker compose ps -a
curl --fail http://127.0.0.1:8080/health/ready
set -a; . ./.env; set +a
bash examples/demo.sh
# Or use your own representative JPEG/PNG:
bash examples/demo.sh path/to/photo.jpg
```

Compose starts PostgreSQL, runs the one-shot migration, then starts API and
worker from the **same non-root image**. API binds only `127.0.0.1:8080`;
PostgreSQL has no published port. Named `database` and `images` volumes persist
across `docker compose down`. Never use `down -v` on data you want to keep.
The first command waits for database health, successful one-shot migration,
and API readiness before the demo. Wait targets only API and its dependencies:
the worker intentionally has no container healthcheck.

The sample is an original, generated 64×48 PNG, intentionally not a meaningful
compression benchmark. The demo uploads profile `web`, polls for at most 120
seconds (individual HTTP calls are also bounded), downloads every variant,
checks byte counts, file signatures and Content-Type, and compares the stored
original byte-for-byte. It writes JSON and files to `examples/results/`;
server-side decoding tests are separate from these client checks.

```bash
node examples/upload.mjs path/to/photo.jpg  # Node.js 20+, trusted server only
bash examples/benchmark.sh path/to/photo.jpg
docker compose logs --tail=100 -f api worker
docker compose run --rm --no-deps api probe
docker compose down                       # retains data
```

The benchmark records observed wall time, job duration, actual output sizes,
and client environment, not fabricated savings. Record server hardware,
container digest, input corpus and resolved profile alongside the results.

## API and integration

Management, originals, and (by default) variants require a bearer API key.
`GET /health/live` and `GET /health/ready` are health endpoints.
`POST /v1/images` accepts multipart `file` and `profile`, returning
`image_id`, `job_id`, and `status_url`. Poll the job, then fetch
`GET /v1/images/{image_id}` for the manifest. Uploads enqueue work; they do not
perform synchronous optimization.

The manifest reports original format/bytes, orientation-normalized `width` and
`height`, stored-file `encoded_width` and `encoded_height`, `original_url`, and
`outputs` with format/dimensions/bytes/URL, `smaller`,
`size_difference_percent`, and `includes_resizing`. Job progress uses
`generated`/`planned` counts and measured `duration_ms`, not invented percentages.
Original downloads remain byte-identical despite the normalized display
dimensions. See the [API contract](docs/api.md) and [OpenAPI schema](docs/openapi.yaml).

Keep the key on your backend. [Next.js examples](examples/integrations.txt) include a
session-protected upload route and responsive native `<picture>` component
that avoids Next image re-encoding. Public media is **opt-in**:
`PUBLIC_VARIANTS=true` makes variant URLs accessible without the key; originals
stay private. Opaque URLs are not authorization. Browser CORS is disabled by
default; enabling it does not make exposing the key safe.

## Configuration and limits

`.env.example` lists the Compose settings. Standalone commands require
`DATABASE_URL`, an `API_KEY` of at least **32 bytes**, and writable storage.
Use URL-safe hex database passwords with the supplied Compose URL.

| Variable | Default |
|---|---|
| `STORAGE_DIR` / `LISTEN_ADDR` | `/data` / `:8080` |
| `PUBLIC_VARIANTS` / `CORS_ORIGINS` | `false` / empty |
| `WORKER_CONCURRENCY` | `1` (independent child slots per API and per worker container) |
| `VIPS_CONCURRENCY` | `1` (libvips concurrency per native child; valid range `1..16`) |
| `MAX_UPLOAD_BYTES` | `20971520` (20 MiB) |
| `MAX_PIXELS` / `MAX_DIMENSION` | `40000000` / `20000` |
| `MAX_OUTPUT_COUNT` | `16` |
| `MAX_OUTPUT_BYTES` | `104857600` (100 MiB) |
| `PROCESS_TIMEOUT` | `120s` |
| `PROFILES_JSON` | empty selects built-in `web` |

Query `/v1/capabilities` with authentication for resolved profiles and encoder
availability. JPEG/PNG inputs only; animation, SVG, GIF, remote URLs, dynamic
transforms, PDF and video are not supported. Sixteen-bit PNG and PNG
`cICP`/`mDCV`/`cLLI` metadata are rejected rather than silently quantized;
supported lower-bit palette/grayscale PNG expands losslessly to 8-bit channels.
Resource limits do not turn an
image decoder into an isolation boundary. Budget memory/disk and use an edge
proxy with request limits. See [operations](docs/operations.txt) for TLS, worker
monitoring, backups, restore, upgrades and destructive development reset.

`WORKER_CONCURRENCY` applies independently to each worker's processing slots
**and** each API container's isolated upload-validation slots. With one of each,
the default therefore permits one validation child plus one processing child,
not one native process service-wide. Every replica adds its own slots, and each
native child uses `VIPS_CONCURRENCY`; account for both multipliers when sizing.

## Development and verification

For native development, build the frontend with `cd web && npm ci && npm run build` first.
Go `1.25` with CGO, a C compiler, `pkg-config`, and **libvips ≥8.14** are needed.
govips `v2.20.0`'s [pinned go.mod][govips-module] requires Go 1.25.0; its
[README requirements][govips] still list an older Go minimum. The module
requirement is authoritative for this build. Debian 13 provides
libvips 8.16.1, compatible with that minimum ([development package][vips-dev]).
Debian 12's older native library is not the supported build environment here.

```bash
# On Debian 13, as a user allowed to install packages:
sudo apt-get update
sudo apt-get install -y build-essential pkg-config libvips-dev liblcms2-dev libheif-plugin-aomenc libheif-plugin-aomdec
go test ./...
go vet ./...
# Against a dedicated, disposable PostgreSQL test database:
TEST_DATABASE_URL='postgres://bytedown@localhost:5432/bytedown_test?sslmode=disable' go test ./...
docker build -t bytedown:local .
docker run --rm bytedown:local probe
node examples/test-examples.mjs
bash examples/test-live.sh # full native checks + isolated Compose acceptance; port 8080 must be free
```

Use your database's password/TLS settings in `TEST_DATABASE_URL`; it must never
point at production. Tests requiring it can be skipped when it is absent.
CI supplies a disposable PostgreSQL 17 service, runs `go test ./...` and
`go vet ./...` with native dependencies, then builds/probes the runtime image.
The example test is an HTTP-contract mock test, not a live-service acceptance
test. CI configuration is not evidence that a particular run has passed.

Base images and the checkout action are pinned to verified upstream digests/
commit SHAs. Apt packages intentionally receive current Debian security updates,
so builds are not byte-for-byte reproducible. Upgrade pins regularly.

[Operations](docs/operations.txt) · [Architecture](docs/architecture.md) ·
[Third-party notices](docs/third-party-licenses.txt) · [License](LICENSE)

[govips]: https://github.com/davidbyttow/govips/blob/v2.20.0/README.md#requirements
[govips-module]: https://github.com/davidbyttow/govips/blob/v2.20.0/go.mod
[vips-dev]: https://packages.debian.org/trixie/libvips-dev
