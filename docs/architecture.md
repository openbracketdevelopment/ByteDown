# Architecture

## Boundaries

One Go executable has API, worker, migration, and internal processing commands.
The API uses net/http; PostgreSQL stores metadata, snapshots, leases, and output
manifests. pgx is the driver. govips wraps native libvips and its system codecs;
this is not a dependency-free static executable.

Each upload is streamed to a generated temporary path, bounded and validated in
a subprocess, then moved atomically into originals before a database transaction
accepts it. The original bytes never pass through an encoder. The resolved named
profile and pipeline version are immutable job inputs.

Workers claim with SKIP LOCKED, keep transactions short, heartbeat a lease, and
encode in a child process with an enforceable wall-clock deadline. Each attempt
has its own staging directory. A fencing token gates complete-manifest
publication. No partial asset is exposed. Deletion locks database state and
invalidates publication; a stale worker must discard its files.

## Processing contract

Only static decoded JPEG and PNG are accepted, independent of names/MIME types.
APNG is rejected. Dimensions and pixel count are checked before full decode.
Apply EXIF orientation, deliberately convert supported color configurations to
sRGB, then strip metadata on exports. Reject unsupported color configurations.
Every rendition comes from the same normalized source, never another rendition.
Resize without cropping or upscaling. Preserve transparency. PNG is lossless;
JPEG is lossy. Quality numbers are independent codec controls, not equivalent
perceptual quality. The format registry and encode/decode smoke probe are the
authority for runtime codec availability.

## Files and transactions

Filesystem rename and PostgreSQL commit are not one transaction. Files are
written before their database references; a crash may leave an orphan, never a
successful manifest referencing partial writes. Reconciliation removes abandoned
staging and unreferenced files after a safety interval. Failed database acceptance
removes the newly stored original when rejection is definite. An ambiguous
database commit leaves the original for reference-aware reconciliation rather
than risking deleting committed data. Completed variants use immutable opaque IDs.
Public caches can retain deleted bytes until cache expiration.

An original's `encoded_width`/`encoded_height` describe the untouched raster;
`width`/`height` describe its EXIF-oriented display dimensions. Generated output
dimensions are measured after normalization and resizing. Positive
`size_difference_percent` means a reduction relative to original upload bytes;
negative means the output grew. `includes_resizing` distinguishes resized output.

Back up the database and image volume together while writers are stopped.
Restoring only one is not a valid restore.

## Limits and operations

Default upload size is 20 MiB, decoded size 40 megapixels, maximum dimension
20,000, and one processing job per worker. Output count, aggregate output bytes,
and wall-clock duration are bounded. Native threading must be budgeted separately
from process concurrency. Compose resource limits are a second boundary.
Detailed configuration and precise enforcement are documented alongside the
implementation. Multiple worker replicas multiply concurrency.

Input validation uses a separate bounded child-process pool in each API process
(the same configured concurrency value); this is additional to worker children.
HTTP request lifetime is bounded by the processing timeout plus 30 seconds.
The encoder may allocate an output buffer before the byte limit can be checked.
The application does not implement a hard per-allocation native memory limit;
container memory limits, conservative pixel limits, and low concurrency matter.
Readiness rechecks database access and write access to all storage directories,
and relies on the real encode/decode probe performed at process startup for the
immutable executable/native-library installation.

Use a TLS reverse proxy, keep PostgreSQL private, and never log secrets or image
metadata. This release serves trusted applications, not hostile public uploaders.
