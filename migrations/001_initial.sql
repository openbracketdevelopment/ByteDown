CREATE TABLE images (
    id text PRIMARY KEY,
    input jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE jobs (
    id text PRIMARY KEY,
    image_id text NOT NULL UNIQUE REFERENCES images(id) ON DELETE CASCADE,
    status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','processing','succeeded','failed')),
    profile jsonb NOT NULL,
    pipeline text NOT NULL,
    planned integer NOT NULL,
    generated integer NOT NULL DEFAULT 0,
    error_code text NOT NULL DEFAULT '',
    error_message text NOT NULL DEFAULT '',
    duration_ms bigint NOT NULL DEFAULT 0,
    token text,
    lease_until timestamptz,
    attempt integer NOT NULL DEFAULT 0,
    available_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX jobs_claim ON jobs(status, available_at, lease_until);
CREATE TABLE outputs (
    id text PRIMARY KEY,
    image_id text NOT NULL REFERENCES images(id) ON DELETE CASCADE,
    path text NOT NULL UNIQUE,
    metadata jsonb NOT NULL
);
CREATE INDEX outputs_image ON outputs(image_id);
CREATE TABLE idempotency (
    key text PRIMARY KEY,
    hash text NOT NULL,
    image_id text NOT NULL REFERENCES images(id) ON DELETE CASCADE
);
