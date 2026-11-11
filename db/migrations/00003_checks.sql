-- +goose Up
CREATE TABLE checks (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    project_id bigint NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    trigger text NOT NULL CHECK (trigger IN ('scheduled', 'manual')),
    state text NOT NULL DEFAULT 'queued'
        CHECK (state IN ('queued', 'running', 'cancelling', 'finished', 'cancelled')),
    created_at timestamptz NOT NULL DEFAULT now(),
    started_at timestamptz,
    finished_at timestamptz,
    -- Request settings and Noise rules as they were when the Check started.
    settings jsonb NOT NULL DEFAULT '{}',
    noise_rules jsonb NOT NULL DEFAULT '[]',
    -- Running lanes; changed only under this row's lock (ADR 0003).
    lanes integer NOT NULL DEFAULT 0 CHECK (lanes >= 0),
    orphan_counting_skipped boolean NOT NULL DEFAULT false,
    -- Report counts, cached by FinalizeCheck.
    count_snapshots integer NOT NULL DEFAULT 0,
    count_failures integer NOT NULL DEFAULT 0,
    count_skipped integer NOT NULL DEFAULT 0
);

CREATE INDEX checks_project_created_idx ON checks (project_id, created_at DESC);

-- At most one active Check per Project.
CREATE UNIQUE INDEX checks_one_active_idx ON checks (project_id)
    WHERE state IN ('queued', 'running', 'cancelling');

CREATE TABLE fetches (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    check_id bigint NOT NULL REFERENCES checks (id) ON DELETE CASCADE,
    page_id bigint NOT NULL REFERENCES pages (id) ON DELETE CASCADE,
    state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'in_flight', 'done')),
    -- The River job (lane) that claimed this Fetch.
    job_id bigint,
    outcome text CHECK (outcome IN ('snapshot', 'failure', 'skipped')),
    skip_reason text CHECK (skip_reason IN ('robots_disallowed', 'robots_unreachable', 'cancelled')),
    failure_kind text CHECK (failure_kind IN ('timeout', 'dns', 'tls', 'connection', 'body_too_large', 'internal')),
    message text,
    started_at timestamptz,
    duration_ms integer,
    UNIQUE (check_id, page_id)
);

CREATE INDEX fetches_pending_idx ON fetches (check_id) WHERE state = 'pending';
CREATE INDEX fetches_job_idx ON fetches (job_id) WHERE state = 'in_flight';
CREATE INDEX fetches_page_check_idx ON fetches (page_id, check_id);

CREATE TABLE bodies (
    project_id bigint NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    sha256 bytea NOT NULL,
    content bytea NOT NULL,
    size integer NOT NULL,
    PRIMARY KEY (project_id, sha256)
);

ALTER TABLE bodies ALTER COLUMN content SET COMPRESSION lz4;

CREATE TABLE snapshots (
    id bigint PRIMARY KEY REFERENCES fetches (id) ON DELETE CASCADE,
    page_id bigint NOT NULL REFERENCES pages (id) ON DELETE CASCADE,
    check_id bigint NOT NULL REFERENCES checks (id) ON DELETE CASCADE,
    fetched_at timestamptz NOT NULL,
    status integer NOT NULL,
    -- Ordered [name, value] pairs as received.
    headers jsonb NOT NULL,
    body_sha256 bytea,
    seo_fields jsonb NOT NULL DEFAULT '{}'
);

CREATE INDEX snapshots_page_fetched_idx ON snapshots (page_id, fetched_at DESC);
CREATE INDEX snapshots_check_idx ON snapshots (check_id);

-- +goose Down
DROP TABLE snapshots;
DROP TABLE bodies;
DROP TABLE fetches;
DROP TABLE checks;
