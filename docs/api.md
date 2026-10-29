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
| GET | /v1/jobs/{id} | State, completed/planned counts, sanitized failures |
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
Required formats are smoke-tested during API/worker startup. Readiness relies
on that startup result and rechecks database and writable storage access.
