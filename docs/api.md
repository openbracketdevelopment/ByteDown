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
OpenAPI specification for exact implemented fields and response codes.
