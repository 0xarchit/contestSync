ALTER TABLE users ADD COLUMN IF NOT EXISTS has_calendar_access BOOLEAN NOT NULL DEFAULT FALSE;
UPDATE users SET has_calendar_access = TRUE WHERE refresh_token IS NOT NULL AND refresh_token != '';
