package models

import (
	"strconv"
	"time"
)

const (
	UserCacheTTL     = 24 * time.Hour
	ContestsCacheTTL = 12 * time.Hour
)

func UserCacheKey(userID int) string {
	return "cache:user:" + strconv.Itoa(userID)
}

func ContestsCacheKey(platform string) string {
	return "cache:contests:" + platform
}

type User struct {
	ID           int       `json:"id"`
	GoogleID     string    `json:"google_id"`
	Email        string    `json:"email"`
	RefreshToken string    `json:"-"`
	CalendarID   string    `json:"calendar_id"`
	UseDedicated bool      `json:"use_dedicated"`
	Platforms    []string  `json:"platforms"`
	CreatedAt    time.Time `json:"created_at"`
}

type CachedUser struct {
	ID           int      `json:"id"`
	GoogleID     string   `json:"google_id"`
	Email        string   `json:"email"`
	RefreshToken string   `json:"refresh_token"`
	CalendarID   string   `json:"calendar_id"`
	UseDedicated bool     `json:"use_dedicated"`
	Platforms    []string `json:"platforms"`
}

type Contest struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	URL       string    `json:"url"`
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time"`
	Duration  int       `json:"duration"`
	Platform  string    `json:"platform"`
	UpdatedAt time.Time `json:"updated_at"`
}

type UserPlatformPreference struct {
	UserID   int    `json:"user_id"`
	Platform string `json:"platform"`
}

type SyncedEvent struct {
	UserID        int       `json:"user_id"`
	ContestID     string    `json:"contest_id"`
	GoogleEventID string    `json:"google_event_id"`
	SyncedAt      time.Time `json:"synced_at"`
}
