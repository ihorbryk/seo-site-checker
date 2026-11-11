-- +goose Up
CREATE TABLE projects (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    domain text NOT NULL UNIQUE,
    schedule text NOT NULL DEFAULT 'daily'
        CHECK (schedule IN ('manual', 'hourly', 'daily', 'weekly')),
    user_agent text NOT NULL DEFAULT 'SEOSiteChecker/0.1',
    timeout_s integer NOT NULL DEFAULT 30 CHECK (timeout_s BETWEEN 1 AND 120),
    max_parallel integer NOT NULL DEFAULT 2 CHECK (max_parallel BETWEEN 1 AND 10),
    respect_robots boolean NOT NULL DEFAULT false,
    custom_headers jsonb NOT NULL DEFAULT '[]',
    exclude_patterns text[] NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now()
);

-- +goose StatementBegin
CREATE FUNCTION projects_domain_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.domain IS DISTINCT FROM OLD.domain THEN
        RAISE EXCEPTION 'a Project''s domain cannot be changed';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER projects_domain_immutable BEFORE UPDATE ON projects
    FOR EACH ROW EXECUTE FUNCTION projects_domain_immutable();

CREATE TABLE pages (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    project_id bigint NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    url text NOT NULL,
    state text NOT NULL DEFAULT 'active'
        CHECK (state IN ('active', 'retired', 'ignored')),
    origin text NOT NULL CHECK (origin IN ('seed', 'discovered', 'manual')),
    orphan_streak integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (project_id, url)
);

CREATE INDEX pages_project_state_idx ON pages (project_id, state);

-- +goose Down
DROP TABLE pages;
DROP TABLE projects;
DROP FUNCTION projects_domain_immutable();
