-- +goose Up
-- The robots.txt a Check used, fetched once by StartCheck when "respect
-- robots.txt" is on; all NULL when it is off. The body is the first 500 KiB.
-- robots_status is the last response's status (NULL after a transport error,
-- which sets robots_failure_kind instead).
ALTER TABLE checks
    ADD COLUMN robots_status integer,
    ADD COLUMN robots_body bytea,
    ADD COLUMN robots_fetched_at timestamptz,
    ADD COLUMN robots_failure_kind text
        CHECK (robots_failure_kind IN ('timeout', 'dns', 'tls', 'connection'));

-- +goose Down
ALTER TABLE checks
    DROP COLUMN robots_failure_kind,
    DROP COLUMN robots_fetched_at,
    DROP COLUMN robots_body,
    DROP COLUMN robots_status;
