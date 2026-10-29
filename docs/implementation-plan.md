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
