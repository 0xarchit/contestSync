package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/0xarchit/contestsync/internal/extractor"
	"github.com/0xarchit/contestsync/internal/queue"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/robfig/cron/v3"
)

type Scheduler struct {
	ReadDB  *pgxpool.Pool
	WriteDB *pgxpool.Pool
	Cron    *cron.Cron
	Queue   *queue.Queue
	OnEvent func(event, details string)
}

func New(readDB *pgxpool.Pool, writeDB *pgxpool.Pool, q *queue.Queue) *Scheduler {
	return &Scheduler{
		ReadDB:  readDB,
		WriteDB: writeDB,
		Cron:    cron.New(),
		Queue:   q,
	}
}

func (s *Scheduler) Start() {
	s.Cron.AddFunc("@every 30m", func() {
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

	go s.RunExtraction(context.Background())
}

func (s *Scheduler) PruneOldData(ctx context.Context) {
	slog.Info("starting data pruning task")
	res, err := s.WriteDB.Exec(ctx, "DELETE FROM contests WHERE end_time < NOW()")
	if err != nil {
		slog.Error("failed to prune old contests", "error", err)
	} else {
		slog.Info("pruned old contests", "count", res.RowsAffected())
	}

	if s.Queue != nil {
		for _, platform := range extractor.Platforms {
			if err := s.Queue.InvalidateContestsCache(ctx, platform); err != nil {
				slog.Error("failed to invalidate contests cache on prune", "platform", platform, "error", err)
			}
		}
	}
}

func (s *Scheduler) CleanupOAuthStates(ctx context.Context) {
	_, err := s.WriteDB.Exec(ctx, "DELETE FROM oauth_states WHERE created_at < NOW() - INTERVAL '10 minutes'")
	if err != nil {
		slog.Error("failed to cleanup oauth states", "error", err)
	}
}

func (s *Scheduler) SyncAllUsers(ctx context.Context) {
	slog.Info("queuing global sync for all users")
	if s.OnEvent != nil {
		s.OnEvent("CRON_SYNC_ALL_USERS", "Triggered global user synchronization sync task queuing.")
	}
	limit := 500
	offset := 0
	for {
		rows, err := s.ReadDB.Query(ctx, "SELECT id FROM users ORDER BY id LIMIT $1 OFFSET $2", limit, offset)
		if err != nil {
			slog.Error("failed to fetch users for sync", "error", err)
			return
		}
		count := 0
		for rows.Next() {
			count++
			var userID int
			if err := rows.Scan(&userID); err != nil {
				slog.Error("failed to scan user ID", "error", err)
				continue
			}
			if err := s.Queue.PublishSyncTask(ctx, userID); err != nil {
				slog.Error("failed to queue sync task", "user_id", userID, "error", err)
			}
		}
		rows.Close()
		if count < limit {
			break
		}
		offset += limit
	}
}

func (s *Scheduler) RunExtraction(ctx context.Context) {
	var platforms []string
	for name := range extractor.Fetchers {
		platforms = append(platforms, name)
		slog.Info("queuing extraction task", "platform", name)
		if err := s.Queue.PublishExtractionTask(ctx, name); err != nil {
			slog.Error("failed to queue extraction task", "platform", name, "error", err)
		}
	}
	if s.OnEvent != nil {
		s.OnEvent("CRON_CONTEST_EXTRACTION", fmt.Sprintf("Triggered contest extraction tasks queuing for: %s", strings.Join(platforms, ", ")))
	}
}

func (s *Scheduler) Stop() {
	s.Cron.Stop()
}
