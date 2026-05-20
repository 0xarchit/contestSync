package sync

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/0xarchit/contestsync/internal/auth"
	"github.com/0xarchit/contestsync/models"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/oauth2"
	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"
)

type Syncer struct {
	DB            *pgxpool.Pool
	AuthProvider  *auth.Provider
	SessionSecret []byte
}

func (s *Syncer) SyncUser(ctx context.Context, userID int) (retErr error) {
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
			rows.Scan(&cid)
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
		rows.Scan(&c.ID, &c.Name, &c.URL, &c.StartTime, &c.EndTime, &c.Platform)

		if syncedMap[c.ID] {
			continue
		}

		event := &calendar.Event{
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

		res, err := srv.Events.Insert(user.CalendarID, event).Context(ctx).Do()
		if err != nil {
			slog.Error("failed to insert event", "user_id", userID, "contest_id", c.ID, "error", err)
			continue
		}

		_, err = s.DB.Exec(ctx, "INSERT INTO synced_events (user_id, contest_id, google_event_id) VALUES ($1, $2, $3)", userID, c.ID, res.Id)
		if err != nil {
			slog.Error("failed to save synced event", "user_id", userID, "contest_id", c.ID, "error", err)
		}
	}

	return nil
}
