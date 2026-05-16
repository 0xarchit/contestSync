package sync

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/0xarchit/contestsync/internal/auth"
	"github.com/0xarchit/contestsync/models"
	"golang.org/x/oauth2"
	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"
)

type Syncer struct {
	DB            *pgxpool.Pool
	AuthProvider  *auth.Provider
	SessionSecret []byte
}

func (s *Syncer) SyncUser(ctx context.Context, userID int) error {
	var user models.User
	var encryptedRefreshToken string
	var calendarID sql.NullString

	err := s.DB.QueryRow(ctx, "SELECT id, email, refresh_token, calendar_id FROM users WHERE id = $1", userID).Scan(&user.ID, &user.Email, &encryptedRefreshToken, &calendarID)
	if err != nil {
		return err
	}

	user.CalendarID = "primary"
	if calendarID.Valid && calendarID.String != "" {
		user.CalendarID = calendarID.String
	}


	refreshToken, err := auth.DecryptToken(encryptedRefreshToken, s.SessionSecret)
	if err != nil {
		return err
	}

	tokenSource := s.AuthProvider.Config.TokenSource(ctx, &oauth2.Token{RefreshToken: refreshToken})
	srv, err := calendar.NewService(ctx, option.WithTokenSource(tokenSource))
	if err != nil {
		return err
	}

	// 1. Get User's Timezone from primary calendar
	cal, err := srv.Calendars.Get("primary").Do()
	timezone := "UTC"
	if err == nil && cal.TimeZone != "" {
		timezone = cal.TimeZone
	}

	// Get user preferences
	rows, err := s.DB.Query(ctx, "SELECT platform FROM user_platform_preferences WHERE user_id = $1", userID)
	if err != nil {
		return err
	}
	defer rows.Close()

	var platforms []string
	for rows.Next() {
		var p string
		rows.Scan(&p)
		platforms = append(platforms, p)
	}

	if len(platforms) == 0 {
		return nil // No platforms selected
	}

	// 2. Get contests for these platforms
	rows, err = s.DB.Query(ctx, "SELECT id, name, url, start_time, end_time, platform FROM contests WHERE platform = ANY($1) AND start_time > NOW()", platforms)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var c models.Contest
		rows.Scan(&c.ID, &c.Name, &c.URL, &c.StartTime, &c.EndTime, &c.Platform)

		// Check if already synced
		var existingEventID string
		err = s.DB.QueryRow(ctx, "SELECT google_event_id FROM synced_events WHERE user_id = $1 AND contest_id = $2", userID, c.ID).Scan(&existingEventID)
		if err == nil {
			continue // Already synced
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
			Transparency: "opaque", // Mark as 'Busy'
		}

		calendarID := "primary"
		if user.CalendarID != "" {
			calendarID = user.CalendarID
		}

		res, err := srv.Events.Insert(calendarID, event).Context(ctx).Do()
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
