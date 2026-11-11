-- +goose Up
-- Outgoing on-domain links of a Snapshot whose target is a Page.
CREATE TABLE links (
    snapshot_id bigint NOT NULL REFERENCES snapshots (id) ON DELETE CASCADE,
    target_page_id bigint NOT NULL REFERENCES pages (id) ON DELETE CASCADE,
    source text NOT NULL CHECK (source IN ('a', 'canonical', 'hreflang', 'redirect')),
    PRIMARY KEY (snapshot_id, target_page_id, source)
);

CREATE INDEX links_target_idx ON links (target_page_id);

-- URLs refused by the sanity limits during a Check.
CREATE TABLE dropped_urls (
    check_id bigint NOT NULL REFERENCES checks (id) ON DELETE CASCADE,
    url text NOT NULL,
    reason text NOT NULL CHECK (reason IN ('too_long', 'repeated_segment')),
    PRIMARY KEY (check_id, url)
);

-- +goose Down
DROP TABLE dropped_urls;
DROP TABLE links;
