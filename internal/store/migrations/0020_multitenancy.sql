-- Add a tenant hierarchy without rewriting or deleting any existing data.
-- The fixed legacy IDs make the cutover deterministic for scripts and support
-- tooling while every pre-existing game/client is assigned to one workspace.
CREATE TABLE IF NOT EXISTS organizations (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slug       TEXT UNIQUE NOT NULL,
    name       TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS workspaces (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    slug            TEXT NOT NULL,
    name            TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (organization_id, slug)
);

INSERT INTO organizations (id, slug, name)
VALUES ('00000000-0000-4000-8000-000000000001', 'legacy', 'Legacy organization')
ON CONFLICT DO NOTHING;

INSERT INTO workspaces (id, organization_id, slug, name)
VALUES (
    '00000000-0000-4000-8000-000000000002',
    '00000000-0000-4000-8000-000000000001',
    'legacy',
    'Legacy workspace'
)
ON CONFLICT DO NOTHING;

ALTER TABLE games ADD COLUMN IF NOT EXISTS workspace_id UUID REFERENCES workspaces(id);
UPDATE games
SET workspace_id = '00000000-0000-4000-8000-000000000002'
WHERE workspace_id IS NULL;
ALTER TABLE games ALTER COLUMN workspace_id SET NOT NULL;
-- Keep the default for one mixed-version rollout window. New code always
-- supplies workspace_id; an older binary can still create only in legacy.
ALTER TABLE games ALTER COLUMN workspace_id SET DEFAULT '00000000-0000-4000-8000-000000000002';
CREATE INDEX IF NOT EXISTS idx_games_workspace ON games (workspace_id, created_at DESC);

-- OAuth clients are console-created resources too. Existing clients follow
-- the same lossless legacy assignment as games.
ALTER TABLE oauth_clients ADD COLUMN IF NOT EXISTS workspace_id UUID REFERENCES workspaces(id);
UPDATE oauth_clients
SET workspace_id = '00000000-0000-4000-8000-000000000002'
WHERE workspace_id IS NULL;
ALTER TABLE oauth_clients ALTER COLUMN workspace_id SET NOT NULL;
ALTER TABLE oauth_clients ALTER COLUMN workspace_id SET DEFAULT '00000000-0000-4000-8000-000000000002';
CREATE INDEX IF NOT EXISTS idx_oauth_clients_workspace ON oauth_clients (workspace_id, created_at DESC);

-- A console identity either represents the configured bootstrap operator or
-- an existing bcrypt-authenticated Zekumo account. Tenant onboarding never
-- creates or shares a password.
CREATE TABLE IF NOT EXISTS console_identities (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    kind       TEXT NOT NULL CHECK (kind IN ('bootstrap', 'account')),
    username   TEXT NOT NULL,
    account_id UUID UNIQUE REFERENCES accounts(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (kind, username),
    CHECK ((kind = 'account') = (account_id IS NOT NULL))
);

CREATE TABLE IF NOT EXISTS organization_memberships (
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    identity_id     UUID NOT NULL REFERENCES console_identities(id) ON DELETE CASCADE,
    role            TEXT NOT NULL CHECK (role IN ('owner', 'admin', 'editor', 'viewer')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, identity_id)
);

CREATE TABLE IF NOT EXISTS workspace_memberships (
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    identity_id  UUID NOT NULL REFERENCES console_identities(id) ON DELETE CASCADE,
    role         TEXT NOT NULL CHECK (role IN ('owner', 'admin', 'editor', 'viewer')),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, identity_id)
);
CREATE INDEX IF NOT EXISTS idx_workspace_members_identity
    ON workspace_memberships (identity_id, workspace_id);
