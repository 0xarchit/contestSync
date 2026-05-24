package sync

import (
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/base32"
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

	var user models.User
	var encryptedRefreshToken string
	var calendarID sql.NullString
	var platforms []string

	err := s.DB.QueryRow(ctx, "SELECT id, email, refresh_token, calendar_id, use_dedicated, platforms FROM users WHERE id = $1", userID).Scan(&user.ID, &user.Email, &encryptedRefreshToken, &calendarID, &user.UseDedicated, &platforms)
	if err != nil {
		return err
	}

	s.DB.Exec(ctx, "UPDATE users SET sync_status = 'syncing' WHERE id = $1", userID)

	defer func() {
		status := "success"
		if retErr != nil {
			status = "failed"
		}
		s.DB.Exec(context.Background(), "UPDATE users SET sync_status = $1, last_sync_at = NOW() WHERE id = $2", status, userID)
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
		if calendarID.Valid && calendarID.String != "" && calendarID.String != "primary" {
			user.CalendarID = calendarID.String
		} else {
			newCal := &calendar.Calendar{
				Summary:  "ContestSync",
				TimeZone: timezone,
			}
			createdCal, err := srv.Calendars.Insert(newCal).Context(ctx).Do()
			if err == nil {
				user.CalendarID = createdCal.Id
				s.DB.Exec(ctx, "UPDATE users SET calendar_id = $1 WHERE id = $2", createdCal.Id, userID)
			} else {
				slog.Error("failed to create dedicated calendar", "user_id", userID, "error", err)
			}
		}
	}

	syncedMap := make(map[string]bool)
	rows, err := s.DB.Query(ctx, "SELECT contest_id FROM synced_events WHERE user_id = $1", userID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var cid string
			if err := rows.Scan(&cid); err != nil {
				slog.Error("failed to scan contest ID", "error", err)
				continue
			}
			syncedMap[cid] = true
		}
	}

	if len(platforms) == 0 {
		return nil
	}

	rows, err = s.DB.Query(ctx, "SELECT id, name, url, start_time, end_time, platform FROM contests WHERE platform = ANY($1) AND start_time > NOW()", platforms)
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

		if syncedMap[c.ID] {
			continue
		}

		hasher := md5.New()
		hasher.Write([]byte(fmt.Sprintf("%d_%s", userID, c.ID)))
		detID := strings.ToLower(base32.HexEncoding.WithPadding(base32.NoPadding).EncodeToString(hasher.Sum(nil)))

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
				}
				continue
			}
			slog.Error("failed to insert event", "user_id", userID, "contest_id", c.ID, "error", err)
			continue
		}

		_, err = s.DB.Exec(ctx, "INSERT INTO synced_events (user_id, contest_id, google_event_id) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING", userID, c.ID, res.Id)
		if err != nil {
			slog.Error("failed to save synced event", "user_id", userID, "contest_id", c.ID, "error", err)
		}
	}

	return nil
}
