# Operations

This is a single-operator deployment using PostgreSQL and a local shared image
volume. It is not a production-readiness certification. Run the application,
integration tests and representative media checks in your own environment
before exposing it.

## Images and native dependencies

The same `/usr/local/bin/bytedown` image supports `api` (default), `worker`,
`migrate`, `probe`, and `healthcheck`. It runs UID/GID **10001:10001**, drops all
capabilities, uses a read-only root filesystem in Compose, and writes persistent
data only to `/data`. Named image volumes inherit the image directory ownership;
for an existing bind mount, explicitly give UID/GID 10001 write permission.
Do not work around permissions by running the service as root.

Pinned official multi-platform image manifest digests, resolved from Docker
Registry v2:

| Image | Manifest digest |
|---|---|
| `golang:1.25.14-trixie` | `sha256:2c4c60ef415fbfa5e90300722293bef36c5e63fae17570ce18f580af933dbd73` |
| `debian:trixie-slim` | `sha256:f6b08b09a05725f9431502d3b5714f5c4576e82c1a84fa1debe0c24a7e0c3ec4` |
| `postgres:17.11-trixie` | `sha256:31ba9c94a03ada9988f2bb493a22a75eef3a185884533e5cdbcd498ea835b08f` |

Sources: [official Go image][go-image], [Debian image][debian-image],
[PostgreSQL image][pg-image], [Registry manifest API][registry].
The Go module targets Go 1.25; the image pins a Go 1.25 patch.
[govips 2.20.0 requires Go ≥1.23 and libvips ≥8.14][govips].
Trixie's [libvips-dev][vips-dev] and [libvips42t64][vips-runtime] supply
8.16.1 (security package revisions can advance). Both stages use the same
Debian release to avoid native ABI mismatch. Apt security revisions are not
pinned: preserve the final built image digest for reproducible deployments.
The `probe` command checks actual native encoders, rather than assuming a
package name proves AVIF/WebP availability.
AVIF requires libheif codec plugins: both Docker stages explicitly install
[`libheif-plugin-aomenc`](https://packages.debian.org/trixie/libheif-plugin-aomenc)
and [`libheif-plugin-aomdec`](https://packages.debian.org/trixie/libheif-plugin-aomdec).
Installing libvips alone with `--no-install-recommends` does not provide an AVIF
encoder. `liblcms2-dev` is explicitly installed for the native color-management
bridge at build time; its shared runtime dependency is supplied by libvips.

## Startup and monitoring

`docker compose up --build -d` waits for `pg_isready`, runs migrations to
completion, then starts API and worker. `pg_isready` only proves PostgreSQL is
accepting connections, not that the application schema is usable.

```bash
docker compose ps -a
docker compose logs --tail=100 migrate api worker db
curl --fail http://127.0.0.1:8080/health/live
curl --fail http://127.0.0.1:8080/health/ready
docker compose run --rm --no-deps api probe
docker stats --no-stream
```

The API container healthcheck invokes `bytedown healthcheck` against HTTP
readiness. API readiness is **not worker liveness**. Worker healthcheck is
deliberately disabled: rerunning a codec probe is expensive and cannot prove a
worker is claiming jobs. Monitor oldest queued-job age, stuck processing jobs,
failures, process restarts, storage capacity and a periodic end-to-end canary.
Compose restart policies restart exited processes, not merely unhealthy ones.
Logs rotate at 10 MiB ×3 per service. Collect them externally if retention is
required, and never enable logging of API keys or sensitive upload bodies.

## Limits, disk and shutdown

Start at one worker and one libvips thread; concurrency multiplies native memory
pressure. Compose caps API at 512 MiB/1 CPU, worker at 2 GiB/2 CPUs, database at
512 MiB/1 CPU, and migration at 256 MiB/0.5 CPU. These are starting limits, not
capacity promises. Workloads near the 40-million-pixel ceiling may need tuning
or lower limits; cgroup OOM termination is possible. Runtime scratch storage is
a bounded 256 MiB container tmpfs. Docker's tmpfs is runtime scratch, not a backup.

Defaults bound uploads (20 MiB), pixels (40 million), either dimension (20,000),
output count (16), total output bytes (100 MiB), and process duration (120s).
Check `/v1/capabilities` for resolved profiles. Restrict HTTP request size and
rate at the edge as well. Pixel limits do not guarantee bounded decoder CPU.
Disk must hold originals, all variants, in-progress writes and database WAL.
Watch filesystem bytes and inodes; no automatic volume quota is supplied.

Worker gets 150 seconds on shutdown, exceeding the default process timeout;
increase it if increasing `PROCESS_TIMEOUT`. API gets 30 seconds. On restart,
durable jobs can be retried; verify lease recovery with the integration tests
and your deployment rather than interpreting a container restart as success.

## TLS, secrets and public media

Keep the localhost API binding behind a maintained TLS reverse proxy. Configure
upload/body limits, request timeouts, rate limiting, trusted forwarding headers,
and private access to management endpoints. Use a private Docker network to an
edge container if desired; do not publish PostgreSQL. Cross-host database
connections need TLS and verified certificates, not `sslmode=disable`.

Generate two independent random values with `openssl rand -hex 32`, store
them only in a permission-0600 `.env` or your deployment secret store, and keep
`.env` out of Git and build contexts. Compose interpolation places secrets in
container environment/configuration, accessible to Docker administrators;
this is not a secret-vault integration. Never share unredacted `compose config`.
Hex passwords avoid URI escaping problems in the supplied database URL.

The bearer API key must be at least 32 bytes. Rotate by changing the deployed
key, recreating API/worker, and updating trusted clients; there is no
overlapping-key rotation protocol. Changing `POSTGRES_PASSWORD` in `.env`
does **not** change an initialized database user's password. Use a controlled
PostgreSQL credential rotation, then recreate clients.

`PUBLIC_VARIANTS=false` is the safe default. Public variants are not suitable
for sensitive content, even with unguessable IDs. Originals always stay
authenticated. CORS is not authentication. Configure public caching only for
intentionally public completed variants and account for deletion/cache purge.

## Consistent backup and restore

Back up **both PostgreSQL and image files** as one consistency point. Database
alone is insufficient. Here is a quiesced local Compose example; ensure enough
space, restrict backup access, and encrypt/copy backups off-host separately.
It uses the already-built runtime's `tar` and overrides the normal entrypoint.

```bash
mkdir -p backups
chmod 700 backups
docker compose stop api worker
docker compose exec -T db pg_dump -U bytedown -d bytedown -Fc > backups/database.dump
docker compose run --rm --no-deps --entrypoint tar api -C /data -czf - . > backups/images.tar.gz
chmod 600 backups/database.dump backups/images.tar.gz
docker compose start api worker
```

Check command exit statuses before considering a backup complete. Do not
overwrite your only backup; timestamp/version the files in actual operations.
The commands above use fixed names for clarity. They do not back up `.env`;
escrow secrets separately. To rehearse restore into a **separate, empty**
Compose project (copy the matching source/image and `.env` first):

```bash
# Use the same -p name consistently; the new project's volumes must be empty.
docker compose -p bytedown-restore up -d --wait db
docker compose -p bytedown-restore exec -T db pg_restore \
  -U bytedown -d bytedown --exit-on-error < backups/database.dump
docker compose -p bytedown-restore run --rm --no-deps --entrypoint tar api \
  -C /data -xzf - < backups/images.tar.gz
# No conflicting API instance can bind localhost:8080 during this rehearsal.
docker compose -p bytedown-restore up -d
curl --fail http://127.0.0.1:8080/health/ready
```

Verify a known original's hash, manifest and variants, not just readiness.
Restore archive ownership as UID 10001. A database dump from a newer schema
needs a compatible application image. Schedule and test restores; volume
existence alone is not a recovery strategy.

## Upgrades and development reset

Before upgrading: read changes, back up both stores, retain the previous image
digest and configuration, test migration/encoder probe on restored data, and
budget downtime. Stop API/worker, build/pull the tested image, run
`docker compose run --rm migrate`, then `docker compose up -d --force-recreate`.
Never assume old code can read a migrated schema; rollback may require a full
matched database/files restore. PostgreSQL major upgrades require an explicit
`pg_dump`/restore or `pg_upgrade` plan; changing an image tag is not that plan.

For **disposable development data only**:

```bash
docker compose down -v   # DESTRUCTIVE: deletes database and all uploaded images
docker compose up --build -d
```

[go-image]: https://hub.docker.com/_/golang
[debian-image]: https://hub.docker.com/_/debian
[pg-image]: https://hub.docker.com/_/postgres
[registry]: https://distribution.github.io/distribution/spec/api/#pulling-an-image-manifest
[govips]: https://github.com/davidbyttow/govips/blob/v2.20.0/README.md#requirements
[vips-dev]: https://packages.debian.org/trixie/libvips-dev
[vips-runtime]: https://packages.debian.org/trixie/libvips42t64
