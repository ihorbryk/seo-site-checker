-- +goose Up
-- What happened to a Page in a Check: first seen, orphaned for the first time,
-- retired after three orphaned Checks, or found again after retiring.
CREATE TABLE page_events (
    check_id bigint NOT NULL REFERENCES checks (id) ON DELETE CASCADE,
    page_id bigint NOT NULL REFERENCES pages (id) ON DELETE CASCADE,
    kind text NOT NULL CHECK (kind IN ('new', 'vanished', 'retired', 'reactivated')),
    PRIMARY KEY (check_id, page_id, kind)
);

CREATE INDEX page_events_page_idx ON page_events (page_id);

-- Report counts, cached by FinalizeCheck. New counts new and reactivated Pages.
ALTER TABLE checks
    ADD COLUMN count_new integer NOT NULL DEFAULT 0,
    ADD COLUMN count_vanished integer NOT NULL DEFAULT 0,
    ADD COLUMN count_retired integer NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE checks
    DROP COLUMN count_new,
    DROP COLUMN count_vanished,
    DROP COLUMN count_retired;
DROP TABLE page_events;
