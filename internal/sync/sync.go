package sync

import (
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/0xarchit/contestsync/internal/auth"
	"github.com/0xarchit/contestsync/models"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"golang.org/x/oauth2"
	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

type Syncer struct {
	DB            *pgxpool.Pool
	ReadDB        *pgxpool.Pool
	AuthProvider  *auth.Provider
	SessionSecret []byte
	Valkey        *redis.Client
	syncingUsers  sync.Map
}

func (s *Syncer) SyncUser(ctx context.Context, userID int) (retErr error) {
	if s.Valkey != nil {
		lockKey := fmt.Sprintf("lock:sync:%d", userID)
		ok, err := s.Valkey.SetNX(ctx, lockKey, "1", 5*time.Minute).Result()
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("user already syncing")
		}
		defer func() {
			if err := s.Valkey.Del(context.Background(), lockKey).Err(); err != nil {
				slog.Error("failed to release valkey lock", "user_id", userID, "error", err)
			}
		}()
	} else {
		if _, loaded := s.syncingUsers.LoadOrStore(userID, true); loaded {
			return fmt.Errorf("user already syncing")
		}
		defer s.syncingUsers.Delete(userID)
	}

	var cachedUser models.CachedUser
	cacheKey := models.UserCacheKey(userID)
	cacheFound := false

	if s.Valkey != nil {
		cachedVal, err := s.Valkey.Get(ctx, cacheKey).Result()
		if err == nil {
			if err := json.Unmarshal([]byte(cachedVal), &cachedUser); err == nil {
				cacheFound = true
			}
		}
	}

	if !cacheFound {
		var dbCalendarID sql.NullString
		var dbEncryptedRefreshToken string
		dbPool := s.ReadDB
		if dbPool == nil {
			dbPool = s.DB
		}
		err := dbPool.QueryRow(ctx, "SELECT id, google_id, email, calendar_id, use_dedicated, platforms, refresh_token FROM users WHERE id = $1", userID).Scan(
			&cachedUser.ID, &cachedUser.GoogleID, &cachedUser.Email, &dbCalendarID, &cachedUser.UseDedicated, &cachedUser.Platforms, &dbEncryptedRefreshToken,
		)
		if err != nil {
			return err
		}
		cachedUser.CalendarID = ""
		if dbCalendarID.Valid {
			cachedUser.CalendarID = dbCalendarID.String
		}
		cachedUser.RefreshToken = dbEncryptedRefreshToken

		if s.Valkey != nil {
			if serialized, err := json.Marshal(cachedUser); err == nil {
				if err := s.Valkey.Set(ctx, cacheKey, string(serialized), models.UserCacheTTL).Err(); err != nil {
					slog.Error("failed to write user cache in sync", "user_id", userID, "error", err)
				}
			}
		}
	}

	var user models.User
	user.ID = cachedUser.ID
	user.GoogleID = cachedUser.GoogleID
	user.Email = cachedUser.Email
	user.CalendarID = cachedUser.CalendarID
	user.UseDedicated = cachedUser.UseDedicated
	encryptedRefreshToken := cachedUser.RefreshToken
	platforms := cachedUser.Platforms

	s.DB.Exec(ctx, "UPDATE users SET sync_status = 'syncing' WHERE id = $1", userID)

	defer func() {
		status := "success"
		if retErr != nil {
			status = "failed"
		}
		s.DB.Exec(context.Background(), "UPDATE users SET sync_status = $1, last_sync_at = NOW() WHERE id = $2", status, userID)
		if s.Valkey != nil {
			cacheKey := fmt.Sprintf("user:last_sync_at:%d", userID)
			if err := s.Valkey.Set(context.Background(), cacheKey, time.Now().Format(time.RFC3339), 1*time.Hour).Err(); err != nil {
				slog.Error("failed to update last sync time cache", "user_id", userID, "error", err)
			}
		}
	}()

	refreshToken, err := auth.DecryptToken(encryptedRefreshToken, s.SessionSecret)
	if err != nil {
		return err
	}

	tokenSource := s.AuthProvider.Config.TokenSource(ctx, &oauth2.Token{RefreshToken: refreshToken})
	srv, err := calendar.NewService(ctx, option.WithTokenSource(tokenSource))
	if err != nil {
		return err
	}

	cal, err := srv.Calendars.Get("primary").Do()
	if err != nil {
		return err
	}
	timezone := "UTC"
	if cal.TimeZone != "" {
		timezone = cal.TimeZone
	}

	user.CalendarID = "primary"
	if user.UseDedicated {
		if cachedUser.CalendarID != "" && cachedUser.CalendarID != "primary" {
			user.CalendarID = cachedUser.CalendarID
		} else {
			newCal := &calendar.Calendar{
				Summary:  "ContestSync",
				TimeZone: timezone,
			}
			createdCal, err := srv.Calendars.Insert(newCal).Context(ctx).Do()
			if err == nil {
				user.CalendarID = createdCal.Id
				s.DB.Exec(ctx, "UPDATE users SET calendar_id = $1 WHERE id = $2", createdCal.Id, userID)
				if s.Valkey != nil {
					cacheKey := models.UserCacheKey(userID)
					if err := s.Valkey.Del(ctx, cacheKey).Err(); err != nil {
						slog.Error("failed to invalidate user cache after calendar creation", "user_id", userID, "error", err)
					}
				}
			} else {
				slog.Error("failed to create dedicated calendar", "user_id", userID, "error", err)
			}
		}
	}

	syncedMap := make(map[string]bool)
	var syncedIDs []string
	cacheKeySE := models.SyncedEventsCacheKey(userID)
	cacheFoundSE := false

	if s.Valkey != nil {
		val, err := s.Valkey.Get(ctx, cacheKeySE).Result()
		if err == nil {
			if err := json.Unmarshal([]byte(val), &syncedIDs); err == nil {
				cacheFoundSE = true
			}
		}
	}

	if cacheFoundSE {
		for _, cid := range syncedIDs {
			syncedMap[cid] = true
		}
	} else {
		dbPoolRead := s.ReadDB
		if dbPoolRead == nil {
			dbPoolRead = s.DB
		}
		rows, err := dbPoolRead.Query(ctx, "SELECT contest_id FROM synced_events WHERE user_id = $1", userID)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var cid string
				if err := rows.Scan(&cid); err != nil {
					slog.Error("failed to scan contest ID", "error", err)
					continue
				}
				syncedMap[cid] = true
				syncedIDs = append(syncedIDs, cid)
			}
			rows.Close()

			if s.Valkey != nil {
				if serialized, err := json.Marshal(syncedIDs); err == nil {
					if err := s.Valkey.Set(ctx, cacheKeySE, string(serialized), models.SyncedEventsCacheTTL).Err(); err != nil {
						slog.Error("failed to write synced events cache", "user_id", userID, "error", err)
					}
				}
			}
		}
	}

	if len(platforms) == 0 {
		return nil
	}

	var contests []models.Contest
	if s.Valkey != nil {
		var missedPlatforms []string
		for _, p := range platforms {
			cacheKey := models.ContestsCacheKey(p)
			val, err := s.Valkey.Get(ctx, cacheKey).Result()
			if err == nil {
				var platformContests []models.Contest
				if err := json.Unmarshal([]byte(val), &platformContests); err == nil {
					contests = append(contests, platformContests...)
					continue
				}
			}
			missedPlatforms = append(missedPlatforms, p)
		}

		if len(missedPlatforms) > 0 {
			dbPoolRead := s.ReadDB
			if dbPoolRead == nil {
				dbPoolRead = s.DB
			}
			dbRows, err := dbPoolRead.Query(ctx, "SELECT id, name, url, start_time, end_time, platform FROM contests WHERE platform = ANY($1) AND start_time > NOW()", missedPlatforms)
			if err != nil {
				return err
			}
			platformMap := make(map[string][]models.Contest)
			for _, p := range missedPlatforms {
				platformMap[p] = []models.Contest{}
			}
			for dbRows.Next() {
				var c models.Contest
				if err := dbRows.Scan(&c.ID, &c.Name, &c.URL, &c.StartTime, &c.EndTime, &c.Platform); err != nil {
					slog.Error("failed to scan contest row", "error", err)
					continue
				}
				platformMap[c.Platform] = append(platformMap[c.Platform], c)
			}
			dbRows.Close()

			for _, p := range missedPlatforms {
				platformContests := platformMap[p]
				cacheKey := models.ContestsCacheKey(p)
				if serialized, err := json.Marshal(platformContests); err == nil {
					if err := s.Valkey.Set(ctx, cacheKey, string(serialized), models.ContestsCacheTTL).Err(); err != nil {
						slog.Error("failed to write contests cache", "key", cacheKey, "error", err)
					}
				}
				contests = append(contests, platformContests...)
			}
		}
	} else {
		dbPoolRead := s.ReadDB
		if dbPoolRead == nil {
			dbPoolRead = s.DB
		}
		rows, err := dbPoolRead.Query(ctx, "SELECT id, name, url, start_time, end_time, platform FROM contests WHERE platform = ANY($1) AND start_time > NOW()", platforms)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var c models.Contest
			if err := rows.Scan(&c.ID, &c.Name, &c.URL, &c.StartTime, &c.EndTime, &c.Platform); err != nil {
				slog.Error("failed to scan contest row", "error", err)
				continue
			}
			contests = append(contests, c)
		}
		rows.Close()
	}

	anySynced := false
	for _, c := range contests {
		if syncedMap[c.ID] {
			continue
		}

		detID := GenerateDeterministicEventID(userID, c.ID)

		event := &calendar.Event{
			Id:          detID,
			Summary:     "[" + c.Platform + "] " + c.Name,
			Description: "Platform: " + c.Platform + "\nURL: " + c.URL + "\n\nSynced by ContestSync",
			Location:    c.URL,
			Start: &calendar.EventDateTime{
				DateTime: c.StartTime.Format(time.RFC3339),
				TimeZone: timezone,
			},
			End: &calendar.EventDateTime{
				DateTime: c.EndTime.Format(time.RFC3339),
				TimeZone: timezone,
			},
			Reminders: &calendar.EventReminders{
				UseDefault: false,
				Overrides: []*calendar.EventReminder{
					{Method: "popup", Minutes: 30},
					{Method: "email", Minutes: 60},
				},
				ForceSendFields: []string{"UseDefault"},
			},
			Transparency: "opaque",
		}

		var res *calendar.Event
		var err error
		for attempt := 1; attempt <= 3; attempt++ {
			res, err = srv.Events.Insert(user.CalendarID, event).Context(ctx).Do()
			if err == nil {
				break
			}
			if gErr, ok := err.(*googleapi.Error); ok && gErr.Code == http.StatusConflict {
				break
			}
			if attempt < 3 {
				time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
			}
		}

		if err != nil {
			if gErr, ok := err.(*googleapi.Error); ok && gErr.Code == http.StatusConflict {
				_, err = s.DB.Exec(ctx, "INSERT INTO synced_events (user_id, contest_id, google_event_id) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING", userID, c.ID, detID)
				if err != nil {
					slog.Error("failed to reconcile synced event on conflict", "user_id", userID, "contest_id", c.ID, "error", err)
				} else {
					anySynced = true
				}
				continue
			}
			slog.Error("failed to insert event", "user_id", userID, "contest_id", c.ID, "error", err)
			continue
		}

		_, err = s.DB.Exec(ctx, "INSERT INTO synced_events (user_id, contest_id, google_event_id) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING", userID, c.ID, res.Id)
		if err != nil {
			slog.Error("failed to save synced event", "user_id", userID, "contest_id", c.ID, "error", err)
		} else {
			anySynced = true
		}
	}

	if anySynced && s.Valkey != nil {
		cacheKeySE := models.SyncedEventsCacheKey(userID)
		if err := s.Valkey.Del(ctx, cacheKeySE).Err(); err != nil {
			slog.Error("failed to invalidate synced events cache", "user_id", userID, "error", err)
		}
	}

	return nil
}

func GenerateDeterministicEventID(userID int, contestID string) string {
	hasher := md5.New()
	hasher.Write([]byte(fmt.Sprintf("%d_%s", userID, contestID)))
	return strings.ToLower(base32.HexEncoding.WithPadding(base32.NoPadding).EncodeToString(hasher.Sum(nil)))
}
