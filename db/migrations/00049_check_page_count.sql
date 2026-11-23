-- +goose Up
-- How many Pages the Project had when FinalizeCheck walked them (ADR 0010).
-- The Report reads a dense section by walking the Project's Pages in URL
-- order, so whether a section is dense is judged against that walk, not against
-- the Check's Fetches: a Project with many retired or ignored Pages walks
-- past them all. 0 for a Check that ended before this column: its Report falls
-- back to its Fetches.
ALTER TABLE checks ADD COLUMN count_pages integer NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE checks DROP COLUMN count_pages;
