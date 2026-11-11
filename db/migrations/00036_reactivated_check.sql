-- +goose Up
-- The Check whose discovery brought a retired Page back into the pool. The
-- lane sets it (the Page must be fetched in that Check); FinalizeCheck turns
-- it into a `reactivated` page event, since page_events is written only there.
ALTER TABLE pages ADD COLUMN reactivated_check_id bigint REFERENCES checks (id) ON DELETE SET NULL;

-- +goose Down
ALTER TABLE pages DROP COLUMN reactivated_check_id;
