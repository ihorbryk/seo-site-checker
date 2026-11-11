-- +goose Up
-- One Diff summary per Snapshot that has a baseline (the Page's previous
-- Snapshot, pinned when the lane ran). Empty Diffs get a row too.
CREATE TABLE diffs (
    snapshot_id bigint PRIMARY KEY REFERENCES snapshots (id) ON DELETE CASCADE,
    baseline_id bigint NOT NULL REFERENCES snapshots (id) ON DELETE CASCADE,
    status_changed boolean NOT NULL,
    headers_changed boolean NOT NULL,
    seo_changed boolean NOT NULL,
    body_changed boolean NOT NULL,
    -- {field: {before: [...], after: [...]}}
    seo_changes jsonb NOT NULL DEFAULT '{}',
    tier text NOT NULL CHECK (tier IN ('critical', 'seo', 'headers', 'body', 'empty'))
);

CREATE INDEX diffs_baseline_idx ON diffs (baseline_id);

-- +goose Down
DROP TABLE diffs;
