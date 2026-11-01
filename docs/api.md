# HTTP API

Management endpoints require the `Authorization` header with scheme `Bearer`
and the server API key.
Errors use `{"error":{"code":"...","message":"..."}}`. Never put the key in
browser code or a `NEXT_PUBLIC` environment variable. CORS is disabled by default.

| Method | Path | Purpose |
|---|---|---|
| GET | /health/live | Process liveness |
| GET | /health/ready | Sanitized database, storage, and encoder readiness |
| GET | /v1/capabilities | Versions, profiles, formats, limits |
| POST | /v1/images | Multipart `file` and `profile`; 202 accepted |
| GET | /v1/jobs/{id} | State, generated/planned counts, sanitized failures |
| GET | /v1/images/{id} | Metadata and completed manifest |
| GET | /v1/images | Paginated list, optional status filter |
| GET | /v1/images/{id}/original | Private original bytes |
| GET | /media/{id} | Completed variant; private by default |
| DELETE | /v1/images/{id} | Invalidate job/publication and remove stored files |

`Idempotency-Key` associates a content hash and resolved profile with one image.
Identical retries return existing IDs; conflicting reuse returns 409. Content
hash alone does not deduplicate separate uploads. Multipart limits apply even
without Content-Length. Upload requests do not encode variants.

States are `queued`, `processing`, `succeeded`, `failed`. Counts are not invented
percentages. Output sizes include resizing when dimensions change. The original
is retained even if the full-size copy grows. Durations are measured, and numeric
quality settings must not be compared between codecs.

Public variants require intentional `PUBLIC_VARIANTS=true`. Opaque URLs are not
access control. Originals always require authentication. See the accompanying
[OpenAPI specification](openapi.yaml) for implemented fields and response codes.

## Profiles

The default `web` profile targets widths 480, 960, 1440, and 1920, includes the
oriented source width, and requests WebP plus AVIF. Larger widths are skipped and
the list is sorted and deduplicated. A full-size original-format copy is always
required, independent of the configured variant formats.

Operators can replace the profile map with `PROFILES_JSON`; callers select only
a map key through the multipart `profile` field:

```json
{"web":{"widths":[480,960,1440,1920],"formats":["webp","avif"],"include_source":true,"jpeg_quality":82,"webp_quality":80,"webp_effort":4,"avif_quality":50,"avif_effort":4}}
```

In a Compose `.env`, put that JSON on a single `PROFILES_JSON=` line. Invalid
widths, formats, qualities, or effort settings fail startup. Jobs snapshot this
resolved configuration and the pipeline version, not just the profile name.
An idempotent retry after a profile change conflicts rather than silently
submitting different work. Quality 82 for JPEG, 80 for WebP, and 50 for AVIF
are independent codec settings, not claims of equivalent appearance.

## Response semantics

Positive `size_difference_percent` means percentage reduction relative to the
uploaded bytes, negative means growth. `includes_resizing` is true if the output
dimensions differ from the oriented input. The metadata includes original
encoded dimensions as well as EXIF-oriented display dimensions; the original
download still has its original metadata and bytes.

List responses use `images`, `limit`, and `offset`. Default limit is 50, maximum
100. Offset pagination can shift under concurrent changes. Job `duration_ms` is
the measured duration of its last attempt, not queue waiting time.
`generated` observes nonempty staged output files on worker heartbeats and is
finalized at successful publication. It can reset on retry; it is not permission
to download partial results. The image's output manifest remains empty until
every required output is committed successfully.
Required formats are smoke-tested during API/worker startup. Readiness relies
on that startup result and rechecks database and writable storage access.

Deletion removes image/job/output/idempotency records transactionally and
invalidates active worker publication. Referenced files are removed best-effort;
crash leftovers and abandoned attempts are reclaimed by age-gated worker
reconciliation. The deleted idempotency key can subsequently be reused.
Already-started downloads may finish, and public caches may retain variants for
the advertised 86,400-second cache lifetime. Originals never become public.

## Optional operator browser client

With `UI_ENABLED=true`, `/` serves the static Svelte client. Configure
`OPERATOR_PASSWORD_HASH` using `examples/set-operator-password.sh`. Passwords use
PBKDF2-HMAC-SHA256 (600,000 iterations, random 128-bit salt), implemented with the
Go standard cryptography library. `POST /ui/login` accepts JSON `password` and
returns a CSRF token; `GET /ui/session` retrieves that token for a live session;
`POST /ui/logout` revokes it. Login requires a same-origin Origin header.
Sessions expire after eight hours and are lost on API restart. The cookie is
HttpOnly, SameSite=Strict, and Secure under HTTPS. Session mutations require both
an exact Origin match and `X-CSRF-Token`. No API key is sent to the browser.
Bearer API clients continue using their existing authentication independently.

`GET /v1/archive?images=<id>,<id>` streams a stored-entry ZIP of all generated
outputs of the selected completed images; originals are excluded. It requires
bearer or operator-session authorization. Limits: ten unique image IDs, 160
entries, 512 MiB uncompressed bytes. Unfinished images return 409; invalid IDs
return 400; missing files return 404. Files are opened and checked before sending
headers. A storage failure during streaming can truncate the archive. Entries
use generated image/output IDs and dimensions, so filenames cannot collide or
traverse directories. Original upload names are not persisted by the API.

### Camera metadata and validation failures

JPEG supports a recognized OnePlus metadata trailer after EOI (length-prefixed,
OnePlus signature, maximum 64 KiB, no additional JPEG raster). Originals retain
that trailer. Arbitrary trailers and multi-picture JPEG remain unsupported.
Validation errors use fixed safe messages and specific codes such as
`image_dimensions`, `unsupported_color_profile`, `unsupported_precision`,
`unsupported_jpeg_structure`, or `invalid_image`. An interrupted validator returns
503 `validation_unavailable`, rather than classifying a server failure as a bad
image. No raw decoder diagnostics or metadata are returned to clients.
