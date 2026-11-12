-- micro git schema. Plain SQL, idempotent: safe to apply repeatedly.
--
-- Rules for editing this file:
--  * Never add BEGIN/COMMIT/ROLLBACK. The server runs it as one implicit
--    transaction after taking a transaction-scoped advisory lock and a
--    SET LOCAL lock_timeout; ending the transaction would release both.
--  * Re-applying must not take conflicting locks on tables that already
--    exist (the app is serving traffic), so ALTER TABLE and CREATE INDEX are
--    guarded by catalog checks instead of IF NOT EXISTS.

CREATE TABLE IF NOT EXISTS users (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    username      text        NOT NULL,
    password_hash text        NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);

DO $$ BEGIN IF to_regclass('users_username_lower_idx') IS NULL THEN CREATE UNIQUE INDEX users_username_lower_idx ON users (lower(username)); END IF; END $$;

CREATE TABLE IF NOT EXISTS repos (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    owner_id       bigint      NOT NULL REFERENCES users (id),
    name           text        NOT NULL,
    description    text        NOT NULL DEFAULT '',
    default_branch text        NOT NULL DEFAULT 'main',
    created_at     timestamptz NOT NULL DEFAULT now()
);

DO $$ BEGIN IF to_regclass('repos_owner_name_lower_idx') IS NULL THEN CREATE UNIQUE INDEX repos_owner_name_lower_idx ON repos (owner_id, lower(name)); END IF; END $$;
DO $$ BEGIN IF to_regclass('repos_created_idx') IS NULL THEN CREATE INDEX repos_created_idx ON repos (created_at DESC, id DESC); END IF; END $$;

CREATE TABLE IF NOT EXISTS objects (
    repo_id bigint   NOT NULL REFERENCES repos (id) ON DELETE CASCADE,
    oid     char(40) NOT NULL,
    type    text     NOT NULL CHECK (type IN ('blob', 'tree', 'commit')),
    data    bytea    NOT NULL,
    PRIMARY KEY (repo_id, oid)
);

CREATE TABLE IF NOT EXISTS refs (
    repo_id bigint   NOT NULL REFERENCES repos (id) ON DELETE CASCADE,
    name    text     NOT NULL,
    oid     char(40) NOT NULL,
    PRIMARY KEY (repo_id, name)
);

-- A ref can only point at an object that exists in the same repo.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'refs_object_fk' AND conrelid = 'refs'::regclass) THEN
        ALTER TABLE refs ADD CONSTRAINT refs_object_fk
            FOREIGN KEY (repo_id, oid) REFERENCES objects (repo_id, oid);
    END IF;
END $$;

-- Private repositories: hidden from everyone but the owner (404 for others).
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_attribute
                   WHERE attrelid = 'repos'::regclass AND attname = 'private' AND NOT attisdropped) THEN
        ALTER TABLE repos ADD COLUMN private boolean NOT NULL DEFAULT false;
    END IF;
END $$;
