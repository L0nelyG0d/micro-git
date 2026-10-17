-- micro git schema. Plain SQL, idempotent: safe to apply repeatedly.

CREATE TABLE IF NOT EXISTS users (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    username      text        NOT NULL,
    password_hash text        NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS users_username_lower_idx ON users (lower(username));

CREATE TABLE IF NOT EXISTS repos (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    owner_id       bigint      NOT NULL REFERENCES users (id),
    name           text        NOT NULL,
    description    text        NOT NULL DEFAULT '',
    default_branch text        NOT NULL DEFAULT 'main',
    created_at     timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS repos_owner_name_lower_idx ON repos (owner_id, lower(name));
CREATE INDEX IF NOT EXISTS repos_created_idx ON repos (created_at DESC, id DESC);

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
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'refs_object_fk') THEN
        ALTER TABLE refs ADD CONSTRAINT refs_object_fk
            FOREIGN KEY (repo_id, oid) REFERENCES objects (repo_id, oid);
    END IF;
END $$;
