-- +goose Up
-- A Project's own Noise rules; the built-in ones live in code. StartCheck
-- copies them (with the built-ins) onto the Check, so editing these never
-- touches a Check that already started.
CREATE TABLE noise_rules (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    project_id bigint NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    kind text NOT NULL CHECK (kind IN ('header', 'body_mask')),
    value text NOT NULL CHECK (value <> ''),
    position integer NOT NULL
);

CREATE INDEX noise_rules_project_idx ON noise_rules (project_id, position);

-- +goose Down
DROP TABLE noise_rules;
