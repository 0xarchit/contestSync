CREATE TYPE sync_status_type AS ENUM ('pending', 'syncing', 'success', 'failed');

CREATE TABLE users (
    id SERIAL PRIMARY KEY,
    google_id TEXT UNIQUE NOT NULL,
    email TEXT NOT NULL,
    refresh_token TEXT NOT NULL,
    calendar_id TEXT,
    use_dedicated BOOLEAN NOT NULL DEFAULT FALSE,
    platforms TEXT[] DEFAULT '{}' CHECK (platforms <@ ARRAY['leetcode', 'codeforces', 'codechef', 'atcoder', 'hackerrank', 'geeksforgeeks', 'code360']::text[]),
    last_sync_at TIMESTAMPTZ,
    sync_status sync_status_type DEFAULT 'pending',
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE contests (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    url TEXT NOT NULL,
    start_time TIMESTAMPTZ NOT NULL,
    end_time TIMESTAMPTZ NOT NULL,
    duration INTEGER NOT NULL,
    platform TEXT NOT NULL CHECK (platform IN ('leetcode', 'codeforces', 'codechef', 'atcoder', 'hackerrank', 'geeksforgeeks', 'code360')),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE synced_events (
    user_id INTEGER REFERENCES users(id) ON DELETE CASCADE,
    contest_id TEXT REFERENCES contests(id) ON DELETE CASCADE,
    google_event_id TEXT NOT NULL,
    synced_at TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (user_id, contest_id),
    CONSTRAINT uq_user_google_event UNIQUE (user_id, google_event_id)
);

CREATE TABLE oauth_states (
    state TEXT PRIMARY KEY,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_oauth_states_created ON oauth_states(created_at);
CREATE INDEX idx_contests_platform_start ON contests(platform, start_time);
CREATE INDEX idx_contests_start_time ON contests(start_time);
CREATE INDEX idx_synced_events_contest_id ON synced_events(contest_id);
CREATE INDEX idx_synced_events_user_id ON synced_events(user_id);
CREATE INDEX idx_users_google_id ON users(google_id);
