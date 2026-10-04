CREATE TABLE IF NOT EXISTS releases (
    id                    BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    game_id               UUID NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    channel               TEXT NOT NULL DEFAULT 'stable',
    version               TEXT NOT NULL,
    changelog             TEXT NOT NULL DEFAULT '',
    status                TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'published', 'deprecated', 'revoked')),
    mandatory             BOOLEAN NOT NULL DEFAULT FALSE,
    min_supported_version TEXT NOT NULL DEFAULT '',
    rollout_percent       INT NOT NULL DEFAULT 100 CHECK (rollout_percent BETWEEN 0 AND 100),
    published_at          TIMESTAMPTZ,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (game_id, channel, version)
);

CREATE INDEX IF NOT EXISTS idx_releases_lookup ON releases (game_id, channel, status);

CREATE TABLE IF NOT EXISTS artifacts (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    release_id  BIGINT NOT NULL REFERENCES releases(id) ON DELETE CASCADE,
    platform    TEXT NOT NULL CHECK (platform IN ('windows', 'macos', 'linux', 'android', 'ios', 'web', 'any')),
    arch        TEXT NOT NULL DEFAULT 'any' CHECK (arch IN ('amd64', 'arm64', 'any')),
    filename    TEXT NOT NULL,
    size        BIGINT NOT NULL,
    storage_key TEXT NOT NULL,
    sha256      TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'ready')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (release_id, platform, arch)
);

CREATE TABLE IF NOT EXISTS update_checks_daily (
    game_id      UUID NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    date         DATE NOT NULL,
    channel      TEXT NOT NULL,
    from_version TEXT NOT NULL,
    checks       BIGINT NOT NULL DEFAULT 0,
    updates      BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (game_id, date, channel, from_version)
);
