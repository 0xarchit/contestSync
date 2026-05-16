package scheduler

import (
	"context"
	"log/slog"
	"time"

	"github.com/0xarchit/contestsync/internal/extractor"
	"github.com/0xarchit/contestsync/internal/sync"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/robfig/cron/v3"
)

type Scheduler struct {
	DB     *pgxpool.Pool
	Cron   *cron.Cron
	Syncer *sync.Syncer
}

func New(db *pgxpool.Pool, syncer *sync.Syncer) *Scheduler {
	return &Scheduler{
		DB:     db,
		Cron:   cron.New(),
		Syncer: syncer,
	}
}

func (s *Scheduler) Start() {
	s.Cron.AddFunc("@every 6h", func() {
		s.RunExtraction(context.Background())
	})
	s.Cron.AddFunc("@every 12h", func() {
		s.SyncAllUsers(context.Background())
	})
	s.Cron.AddFunc("@every 15m", func() {
		s.CleanupOAuthStates(context.Background())
	})
	s.Cron.Start()
}

func (s *Scheduler) CleanupOAuthStates(ctx context.Context) {
	_, err := s.DB.Exec(ctx, "DELETE FROM oauth_states WHERE created_at < NOW() - INTERVAL '10 minutes'")
	if err != nil {
		slog.Error("failed to cleanup oauth states", "error", err)
	}
}

func (s *Scheduler) SyncAllUsers(ctx context.Context) {
	slog.Info("starting global sync for all users")
	rows, err := s.DB.Query(ctx, "SELECT id FROM users")
	if err != nil {
		slog.Error("failed to fetch users for sync", "error", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var userID int
		rows.Scan(&userID)
		if err := s.Syncer.SyncUser(ctx, userID); err != nil {
			slog.Error("failed to sync user", "user_id", userID, "error", err)
		}
	}
}

func (s *Scheduler) RunExtraction(ctx context.Context) {
	for name, fetcher := range extractor.Fetchers {
		slog.Info("fetching contests", "platform", name)
		contests, err := fetcher()
		if err != nil {
			slog.Error("failed to fetch contests", "platform", name, "error", err)
			continue
		}

		for _, c := range contests {
			_, err := s.DB.Exec(ctx, `
				INSERT INTO contests (id, name, url, start_time, end_time, duration, platform)
				VALUES ($1, $2, $3, $4, $5, $6, $7)
				ON CONFLICT (id) DO UPDATE SET
					name = $2, url = $3, start_time = $4, end_time = $5, duration = $6, updated_at = NOW()
			`, c.ID, c.Name, c.URL, time.Unix(c.StartTime, 0), time.Unix(c.EndTime, 0), c.Duration, c.Platform)
			if err != nil {
				slog.Error("failed to save contest", "id", c.ID, "error", err)
			}
		}
	}
}
