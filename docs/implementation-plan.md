# First release implementation plan

1. Establish this product brief, architecture, and API contract before code.
2. Implement validated environment configuration and immutable named profiles.
3. Implement a small govips encoder registry, bounded input validation,
   normalization, independent outputs, and encode/decode capability probes.
4. Add versioned PostgreSQL migrations, idempotent acceptance, leased claims,
   fencing, retries, deletion coordination, and filesystem reconciliation.
5. Add authenticated net/http endpoints, consistent errors, downloads, and
   matching OpenAPI; wire API/worker/migrate/internal child commands.
6. Add pinned non-root Docker/Compose deployment, CI, generated fixtures,
   PostgreSQL integration tests, curl/server-JavaScript/Next.js examples,
   bounded demo, and measured benchmark command.
7. Exercise the service with real native encoders and PostgreSQL, review
   orientation/alpha/color handling, scan secrets and code, and report exact
   validation results and limitations. Do not equate scaffold presence with
   verified acceptance.

Prefer direct idiomatic Go and short transactions over broad frameworks.
Dependencies and native encoder APIs must be checked against upstream sources.
Future formats require application review, tests, quality/performance evaluation,
and client compatibility review; never automatically download codecs.

## Verification record — 2026-10-29

- Parent-owned command/API/configuration/authentication/storage tests and
  `go vet` passed; their tests also passed with `-race`.
- Processing owner ran `go test -race -cover ./internal/processing -count=1`
  and `go vet ./internal/processing`: passed, approximately 84% statement
  coverage. Structural tests cover orientation, ICC conversion, alpha,
  independent formats, limits, corrupt inputs, and deliberate rejections.
- Persistence owner ran PostgreSQL-backed
  `go test -race -count=1 ./internal/store ./internal/worker/...` and targeted
  vet: passed. Includes concurrent claiming, recovery, fencing, idempotency,
  deletion, timeouts, bounded stdout, retries, progress, and reconciliation.
- Deployment owner ran full `go test ./...` and `go vet ./...` inside the pinned
  Go 1.25.14/Debian 13 build image with PostgreSQL 17.11: passed. The non-root
  runtime's real JPEG/PNG/WebP/AVIF codec probe passed.
- Real Compose HTTP checks passed: JPEG/PNG upload, original byte equality,
  completed variants, measured durations, MIME/length/nosniff/cache headers,
  default private access, deletion, and bounded failure without partial manifests.
  The generated 124-byte PNG produced a 145-byte PNG copy, an 84-byte WebP, and a
  281-byte AVIF. These are tiny synthetic fixture observations, **not** a
  photographic compression benchmark or a throughput claim.
- Visually inspected the generated orientation/alpha/ICC review sheet; no
  obvious defects. This is not a comprehensive perceptual-quality evaluation.
- Secret scans passed before commits. Dependency advisory checks found no
  advisories for selected modules/actions at check time.
- Initial CodeQL analysis found an integer-narrowing issue (fixed with an upper
  bound check) and flagged SHA-256 API-key comparison as password hashing.
  The latter is a false positive: these are random bearer keys, not stored
  password hashes; fixed-size digests support constant-time comparison.
  C++, JavaScript, and Actions had no alerts. The requested final CodeQL rerun
  timed out after finishing Actions cleanly; a complete final scan is **not**
  claimed.

## Known limitations and next milestone

This is a first release for trusted applications, not a production-readiness
certification. Higher-precision/HDR PNG, untagged CMYK, and unsupported color
profiles are explicitly rejected. Native allocations and codec memory are not
bounded per allocation; use container limits and conservative concurrency.
Global throughput limits across multiple worker replicas, rate limiting, disk
quotas, and a canary/metrics-based worker health monitor remain operational work.
Deletion cannot revoke public caches or downloads already in progress.
APT security package revisions intentionally move within the pinned Debian
release; retain built image digests for exact deployments.

Recommended next milestone: broader photographic/graphics corpus evaluation,
resource measurements and operational hardening, followed by one explicitly
tested CMS adapter. Do not expand into dashboards, PDF/OCR, archives, or new
formats before that work.
