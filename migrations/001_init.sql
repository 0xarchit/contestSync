CREATE TABLE users (
    id            SERIAL PRIMARY KEY,
    google_id     TEXT UNIQUE NOT NULL,
    email         TEXT NOT NULL,
    refresh_token TEXT NOT NULL,
    calendar_id   TEXT,
    created_at    TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE user_platform_preferences (
    user_id  INTEGER REFERENCES users(id) ON DELETE CASCADE,
    platform TEXT NOT NULL,
    PRIMARY KEY (user_id, platform)
);

CREATE TABLE contests (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    url        TEXT NOT NULL,
    start_time TIMESTAMPTZ NOT NULL,
    end_time   TIMESTAMPTZ NOT NULL,
    duration   INTEGER NOT NULL,
    platform   TEXT NOT NULL,
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE synced_events (
    user_id         INTEGER REFERENCES users(id) ON DELETE CASCADE,
    contest_id      TEXT REFERENCES contests(id),
    google_event_id TEXT NOT NULL,
    synced_at       TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (user_id, contest_id)
);

CREATE TABLE oauth_states (
    state      TEXT PRIMARY KEY,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_oauth_states_created ON oauth_states(created_at);
