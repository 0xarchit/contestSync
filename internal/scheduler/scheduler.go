package scheduler

import (
	"context"
	"log/slog"

	"github.com/0xarchit/contestsync/internal/extractor"
	"github.com/0xarchit/contestsync/internal/queue"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/robfig/cron/v3"
)

type Scheduler struct {
	DB    *pgxpool.Pool
	Cron  *cron.Cron
	Queue *queue.Queue
}

func New(db *pgxpool.Pool, q *queue.Queue) *Scheduler {
	return &Scheduler{
		DB:    db,
		Cron:  cron.New(),
		Queue: q,
	}
}

func (s *Scheduler) Start() {
	s.Cron.AddFunc("@daily", func() {
		s.RunExtraction(context.Background())
	})
	s.Cron.AddFunc("@daily", func() {
		s.SyncAllUsers(context.Background())
	})
	s.Cron.AddFunc("@daily", func() {
		s.PruneOldData(context.Background())
	})
	s.Cron.AddFunc("@every 15m", func() {
		s.CleanupOAuthStates(context.Background())
	})
	s.Cron.Start()
}

func (s *Scheduler) PruneOldData(ctx context.Context) {
	slog.Info("starting data pruning task")
	// Delete contests older than 30 days
	res, err := s.DB.Exec(ctx, "DELETE FROM contests WHERE end_time < NOW() - INTERVAL '30 days'")
	if err != nil {
		slog.Error("failed to prune old contests", "error", err)
	} else {
		slog.Info("pruned old contests", "count", res.RowsAffected())
	}

	// synced_events will be pruned via ON DELETE CASCADE on contest_id
}

func (s *Scheduler) CleanupOAuthStates(ctx context.Context) {
	_, err := s.DB.Exec(ctx, "DELETE FROM oauth_states WHERE created_at < NOW() - INTERVAL '10 minutes'")
	if err != nil {
		slog.Error("failed to cleanup oauth states", "error", err)
	}
}

func (s *Scheduler) SyncAllUsers(ctx context.Context) {
	slog.Info("queuing global sync for all users")
	rows, err := s.DB.Query(ctx, "SELECT id FROM users")
	if err != nil {
		slog.Error("failed to fetch users for sync", "error", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var userID int
		if err := rows.Scan(&userID); err != nil {
			slog.Error("failed to scan user ID", "error", err)
			continue
		}
		if err := s.Queue.PublishSyncTask(ctx, userID); err != nil {
			slog.Error("failed to queue sync task", "user_id", userID, "error", err)
		}
	}
}

func (s *Scheduler) RunExtraction(ctx context.Context) {
	for name := range extractor.Fetchers {
		slog.Info("queuing extraction task", "platform", name)
		if err := s.Queue.PublishExtractionTask(ctx, name); err != nil {
			slog.Error("failed to queue extraction task", "platform", name, "error", err)
		}
	}
}

func (s *Scheduler) Stop() {
	s.Cron.Stop()
}
