-- +goose Up
-- Every Project belongs to one User. An existing install's Projects pass to a
-- locked bootstrap User named by BOOTSTRAP_EMAIL (needed only when Projects exist).
ALTER TABLE projects ADD COLUMN owner_id bigint;

-- +goose ENVSUB ON
-- Dollar-quoted so an apostrophe in the address stays data. goose's variable
-- substitution turns "$$" into "$", so the tags are written doubled; the
-- padding spaces are trimmed below.
SELECT set_config('seo.bootstrap_email', $$bootstrap$$ ${BOOTSTRAP_EMAIL:-} $$bootstrap$$, true);
-- +goose ENVSUB OFF

-- +goose StatementBegin
DO $$
DECLARE
    bootstrap text := lower(trim(current_setting('seo.bootstrap_email')));
    owner bigint;
BEGIN
    IF EXISTS (SELECT 1 FROM projects) THEN
        IF bootstrap = '' THEN
            RAISE EXCEPTION 'Projects exist but BOOTSTRAP_EMAIL is not set: set it to the email of the User who should own them';
        END IF;
        INSERT INTO users (email, password_hash) VALUES (bootstrap, '!')
        ON CONFLICT (email) DO UPDATE SET email = EXCLUDED.email
        RETURNING id INTO owner;
        UPDATE projects SET owner_id = owner;
    END IF;
END
$$;
-- +goose StatementEnd

ALTER TABLE projects
    ALTER COLUMN owner_id SET NOT NULL,
    ADD CONSTRAINT projects_owner_id_fkey FOREIGN KEY (owner_id) REFERENCES users (id) ON DELETE RESTRICT,
    DROP CONSTRAINT projects_domain_key,
    ADD CONSTRAINT projects_owner_id_domain_key UNIQUE (owner_id, domain);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION projects_domain_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.domain IS DISTINCT FROM OLD.domain THEN
        RAISE EXCEPTION 'a Project''s domain cannot be changed';
    END IF;
    IF NEW.owner_id IS DISTINCT FROM OLD.owner_id THEN
        RAISE EXCEPTION 'a Project''s owner cannot be changed';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- Best effort: restoring UNIQUE (domain) fails if two Users own the same domain.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION projects_domain_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.domain IS DISTINCT FROM OLD.domain THEN
        RAISE EXCEPTION 'a Project''s domain cannot be changed';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

ALTER TABLE projects
    DROP CONSTRAINT projects_owner_id_domain_key,
    DROP CONSTRAINT projects_owner_id_fkey,
    DROP COLUMN owner_id,
    ADD CONSTRAINT projects_domain_key UNIQUE (domain);
