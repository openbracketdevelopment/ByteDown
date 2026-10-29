# Integration examples

Run commands from the repository root. Export `API_KEY` and optionally
`BASE_URL` (default `http://127.0.0.1:8080`). Never publish the key, store it in
CMS frontend settings, or use `NEXT_PUBLIC_*`.

```bash
set -a; . ./.env; set +a
bash examples/demo.sh                      # generated 64×48 PNG
bash examples/demo.sh path/to/photo.png
node examples/upload.mjs path/to/photo.jpg # Node 20+, no npm dependencies
bash examples/benchmark.sh path/to/photo.jpg
node examples/test-examples.mjs            # mock HTTP contract, not encoder acceptance
bash examples/test-live.sh                 # full native Go checks + disposable live Compose test
```

`demo.sh` verifies original equality, downloaded lengths, Content-Type and
format magic. It does not claim perceptual equivalence or fully decode the
downloads; native Go tests must cover decoding. Use representative photographs,
alpha PNGs and synthetic graphics for actual quality/size measurements.
`RESULTS_DIR` overrides the output location. Results contain private media and
URLs; do not commit them. Delete local results after evaluation.
The live test requires localhost port 8080 to be free and OpenSSL. It builds
images, runs full Go tests/vet against its disposable PostgreSQL, tests real
PNG/JPEG processing, private access, deletion and a deliberately failed job,
then destroys only its uniquely named project's containers and volumes.
It writes evidence to `examples/live-results/`; the tiny owned fixtures are
correctness cases, not a representative compression benchmark.

## Next.js App Router (scoped snippets)

These are **integration snippets**, not a standalone Next application or a
dependency/version recommendation:

1. Copy `next/route.ts` to your existing app's `app/api/images/route.ts`.
   Adapt `@/auth` to your existing authentication provider; it deliberately
   refuses anonymous users and cross-origin requests. Add per-user quotas,
   rate limits, proxy body limits and application ownership records. The
   example selects only `web`, not a caller-controlled processing profile.
2. Set server-only `BYTEDOWN_URL` (reachable by your Next server) and
   `BYTEDOWN_API_KEY`. The key stays in outbound server requests. Upload with
   browser `fetch("/api/images", {method:"POST", body:formData})`; do not set a
   multipart Content-Type manually.
3. This small route polls server-side for up to 120 seconds. Many serverless
   platforms time out sooner: use a durable application-side job, return 202,
   and add an authenticated, owner-scoped status route instead of deploying a
   long-lived request there. Network exceptions propagate to your app's error
   handling; redact secrets from logs. Do not retry uploads blindly without
   an application idempotency strategy.
4. Persist the returned image ID and manifest under the authenticated owner.
   Never implement an arbitrary-path or arbitrary-image proxy that allows any
   logged-in user to access every image.
5. Copy `OptimizedPicture.tsx`, pass a completed manifest, meaningful `alt`,
   actual layout `sizes`, and an externally reachable `mediaOrigin`. Native
   `<picture>` emits AVIF/WebP `srcset` plus an **optimized JPEG/PNG** fallback,
   dimensions and lazy loading. It intentionally does not use `next/image`,
   avoiding a second optimizer pass; if adapting it to `next/image`, explicitly
   use `unoptimized`.

Default variants are private. A browser cannot authenticate `<img>` using the
service's bearer header. Either explicitly opt into `PUBLIC_VARIANTS=true` for
non-sensitive media, or rewrite URLs to your own authenticated media endpoint
that checks session **and image ownership**, then streams service bytes.
Do not expose original URLs in public pages. Configure public cache/TLS at the
edge, not by sharing credentials. A completed manifest is metadata, not proof
that a browser can reach internal Docker hostnames.

## CMS mapping (conceptual; no plugin included)

A Craft/Payload/other CMS adapter would upload the original from a trusted
backend hook, store `image_id`/`job_id`, poll asynchronously, then map variant
dimensions and URLs into asset metadata. Publishing should wait for success or
retain a known-good existing asset. Keep the CMS source asset until backup and
deletion policies are agreed; propagate deletions intentionally and tolerate
partial failures. Use public variants only for public assets, and an
owner-aware proxy for private assets. No CMS plugin, webhook or S3 integration
is implemented by these examples.
