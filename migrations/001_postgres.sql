CREATE TABLE IF NOT EXISTS organizations (
    id text PRIMARY KEY,
    name text NOT NULL
);
CREATE TABLE IF NOT EXISTS users (
    id text PRIMARY KEY,
    tenant_id text NOT NULL REFERENCES organizations(id),
    email text NOT NULL UNIQUE,
    name text NOT NULL,
    password_hash text NOT NULL,
    role text NOT NULL CHECK (role IN ('analyst','manager')),
    UNIQUE (tenant_id,id)
);
CREATE TABLE IF NOT EXISTS sessions (
    token_hash text PRIMARY KEY,
    user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS sessions_expiry_idx ON sessions(expires_at);
CREATE TABLE IF NOT EXISTS ingestion_keys (
    token_hash text PRIMARY KEY,
    tenant_id text NOT NULL REFERENCES organizations(id),
    name text NOT NULL
);
CREATE TABLE IF NOT EXISTS workspaces (
    tenant_id text NOT NULL,
    user_id text NOT NULL,
    config jsonb NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id,user_id),
    FOREIGN KEY (tenant_id,user_id) REFERENCES users(tenant_id,id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS investigations (
    id text PRIMARY KEY,
    tenant_id text NOT NULL,
    saved_by text NOT NULL,
    title text NOT NULL,
    entity_id text NOT NULL,
    start_at timestamptz NOT NULL,
    end_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (end_at >= start_at),
    FOREIGN KEY (tenant_id,saved_by) REFERENCES users(tenant_id,id)
);
CREATE INDEX IF NOT EXISTS investigations_tenant_created_idx ON investigations(tenant_id,created_at DESC);
