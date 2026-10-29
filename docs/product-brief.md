# ByteDown

**Smaller files. More possibilities.**

ByteDown is an MIT-licensed, self-hosted image optimization service for one
operator and multiple trusted server-side applications. It preserves JPEG/PNG
uploads and asynchronously produces an optimized original-format copy and
responsive WebP/AVIF variants. It is not a production-readiness guarantee.
Smaller files are an outcome to measure, not a promise.

The first release includes bearer authentication, named profiles, durable
PostgreSQL jobs, local storage, a versioned API, Docker deployment, tests, and a
bounded command-line demo. Originals are private; variants are private unless
the operator explicitly enables public access. No browser receives the API key.

Acceptance means originals remain byte-identical; required outputs are all
decodable before publication; job state survives restarts; conflicting
idempotency keys fail; errors and size increases are honestly reported.

Future milestones: operational hardening and broader image-quality evaluation,
Craft/Payload integrations, S3-compatible storage, completion webhooks, and
reviewed additional image formats. Accounts, billing, dashboards, remote imports,
dynamic transforms, GIF/SVG, video, Kubernetes, and microservices are out of scope.
PDF, OCR, and archives would be separate modules, not image pipeline extensions.
