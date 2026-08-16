package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	stdSync "sync"
	"time"

	"github.com/0xarchit/contestsync/config"
	"github.com/0xarchit/contestsync/internal/extractor"
	"github.com/0xarchit/contestsync/internal/observability"
	"github.com/0xarchit/contestsync/internal/sync"
	"github.com/0xarchit/contestsync/models"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	amqp "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const (
	QueueExtraction = "extraction-tasks"
	QueueSync       = "sync-tasks"
)

type ExtractionTask struct {
	Platform string `json:"platform"`
}

type SyncTask struct {
	UserID int `json:"user_id"`
}

type Queue struct {
	cfg             *config.Config
	AMQPConn        *amqp.Connection
	AMQPChannel     *amqp.Channel
	DB              *pgxpool.Pool
	Syncer          *sync.Syncer
	OTel            *observability.OTelMetrics
	useInMemory     bool
	extractionCh    chan string
	syncCh          chan int
	wg              stdSync.WaitGroup
	mu              stdSync.RWMutex
	OnTelegramEvent func(event, details string)
}

func New(cfg *config.Config, db *pgxpool.Pool, syncer *sync.Syncer) (*Queue, error) {
	q := &Queue{
		cfg:          cfg,
		DB:           db,
		Syncer:       syncer,
		extractionCh: make(chan string, 100),
		syncCh:       make(chan int, 1000),
	}

	if cfg.AMQPURL == "" {
		slog.Info("AMQP URL not configured; running in-memory queue")
		q.useInMemory = true
		return q, nil
	}

	if err := q.connectAMQP(); err != nil {
		slog.Error("failed initial connection to AMQP broker, falling back to in-memory queue", "error", err)
		q.useInMemory = true
		return q, nil
	}

	return q, nil
}

func (q *Queue) connectAMQP() error {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.AMQPChannel != nil {
		_ = q.AMQPChannel.Close()
	}
	if q.AMQPConn != nil {
		_ = q.AMQPConn.Close()
	}

	amqpCfg := amqp.Config{
		Heartbeat: 10 * time.Second,
		Locale:    "en_US",
	}

	conn, err := amqp.DialConfig(q.cfg.AMQPURL, amqpCfg)
	if err != nil {
		return fmt.Errorf("failed to dial AMQP broker: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("failed to open AMQP channel: %w", err)
	}

	_, err = ch.QueueDeclare(QueueExtraction, true, false, false, false, nil)
	if err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return fmt.Errorf("failed to declare extraction queue: %w", err)
	}

	_, err = ch.QueueDeclare(QueueSync, true, false, false, false, nil)
	if err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return fmt.Errorf("failed to declare sync queue: %w", err)
	}

	q.AMQPConn = conn
	q.AMQPChannel = ch
	slog.Info("successfully connected to CloudAMQP LavinMQ broker")
	return nil
}

func (q *Queue) Health(ctx context.Context) error {
	if q.useInMemory {
		return nil
	}
	q.mu.RLock()
	defer q.mu.RUnlock()
	if q.AMQPConn == nil || q.AMQPConn.IsClosed() {
		return fmt.Errorf("AMQP connection is closed")
	}
	return nil
}

func (q *Queue) PublishExtractionTask(ctx context.Context, platform string) error {
	q.mu.RLock()
	inMemory := q.useInMemory || q.AMQPChannel == nil || q.AMQPConn == nil || q.AMQPConn.IsClosed()
	ch := q.AMQPChannel
	q.mu.RUnlock()

	if inMemory {
		select {
		case q.extractionCh <- platform:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		default:
			return fmt.Errorf("in-memory extraction queue is full")
		}
	}

	task := ExtractionTask{Platform: platform}
	val, err := json.Marshal(task)
	if err != nil {
		return err
	}

	return ch.PublishWithContext(
		ctx,
		"",
		QueueExtraction,
		false,
		false,
		amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent,
			Body:         val,
		},
	)
}

func (q *Queue) PublishSyncTask(ctx context.Context, userID int) error {
	q.mu.RLock()
	inMemory := q.useInMemory || q.AMQPChannel == nil || q.AMQPConn == nil || q.AMQPConn.IsClosed()
	ch := q.AMQPChannel
	q.mu.RUnlock()

	if inMemory {
		select {
		case q.syncCh <- userID:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		default:
			return fmt.Errorf("in-memory sync queue is full")
		}
	}

	task := SyncTask{UserID: userID}
	val, err := json.Marshal(task)
	if err != nil {
		return err
	}

	return ch.PublishWithContext(
		ctx,
		"",
		QueueSync,
		false,
		false,
		amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent,
			Body:         val,
		},
	)
}

func (q *Queue) InvalidateContestsCache(ctx context.Context, platform string) error {
	if q.Syncer != nil && q.Syncer.Valkey != nil {
		cacheKey := models.ContestsCacheKey(platform)
		return q.Syncer.Valkey.Del(ctx, cacheKey).Err()
	}
	return nil
}

func (q *Queue) StartConsumers(ctx context.Context, cfg *config.Config) {
	if q.useInMemory {
		go q.consumeExtractionInMemory(ctx)
		go q.consumeSyncInMemory(ctx)
		return
	}

	go q.runSelfHealingExtractionConsumer(ctx)
	go q.runSelfHealingSyncConsumer(ctx)
}

func (q *Queue) runSelfHealingExtractionConsumer(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		q.mu.RLock()
		conn := q.AMQPConn
		q.mu.RUnlock()

		if conn == nil || conn.IsClosed() {
			slog.Warn("AMQP connection closed, attempting reconnect before extraction consumer restart...")
			if err := q.reconnectWithBackoff(ctx); err != nil {
				return
			}
		}

		q.mu.RLock()
		ch := q.AMQPChannel
		q.mu.RUnlock()

		if ch == nil {
			time.Sleep(2 * time.Second)
			continue
		}

		msgs, err := ch.Consume(
			QueueExtraction,
			"extraction-consumer",
			false,
			false,
			false,
			false,
			nil,
		)
		if err != nil {
			slog.Warn("failed to start AMQP extraction consumer, reconnecting...", "error", err)
			_ = q.reconnectWithBackoff(ctx)
			continue
		}

		slog.Info("started AMQP extraction consumer")
		sem := make(chan struct{}, 3)
		active := true

		for active {
			select {
			case msg, ok := <-msgs:
				if !ok {
					slog.Warn("AMQP extraction consumer channel closed, restarting consumer...")
					active = false
					break
				}

				var task ExtractionTask
				if err := json.Unmarshal(msg.Body, &task); err != nil {
					slog.Error("failed to unmarshal extraction task", "error", err)
					_ = msg.Nack(false, false)
					continue
				}

				sem <- struct{}{}
				q.wg.Add(1)
				go func(d amqp.Delivery, plat string) {
					defer q.wg.Done()
					defer func() { <-sem }()
					q.handleExtraction(ctx, plat)
					_ = d.Ack(false)
				}(msg, task.Platform)

			case <-ctx.Done():
				return
			}
		}

		time.Sleep(1 * time.Second)
	}
}

func (q *Queue) runSelfHealingSyncConsumer(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		q.mu.RLock()
		conn := q.AMQPConn
		q.mu.RUnlock()

		if conn == nil || conn.IsClosed() {
			slog.Warn("AMQP connection closed, attempting reconnect before sync consumer restart...")
			if err := q.reconnectWithBackoff(ctx); err != nil {
				return
			}
		}

		q.mu.RLock()
		ch := q.AMQPChannel
		q.mu.RUnlock()

		if ch == nil {
			time.Sleep(2 * time.Second)
			continue
		}

		msgs, err := ch.Consume(
			QueueSync,
			"sync-consumer",
			false,
			false,
			false,
			false,
			nil,
		)
		if err != nil {
			slog.Warn("failed to start AMQP sync consumer, reconnecting...", "error", err)
			_ = q.reconnectWithBackoff(ctx)
			continue
		}

		slog.Info("started AMQP sync consumer")
		sem := make(chan struct{}, 10)
		active := true

		for active {
			select {
			case msg, ok := <-msgs:
				if !ok {
					slog.Warn("AMQP sync consumer channel closed, restarting consumer...")
					active = false
					break
				}

				var task SyncTask
				if err := json.Unmarshal(msg.Body, &task); err != nil {
					slog.Error("failed to unmarshal sync task", "error", err)
					_ = msg.Nack(false, false)
					continue
				}

				sem <- struct{}{}
				q.wg.Add(1)
				go func(d amqp.Delivery, uid int) {
					defer q.wg.Done()
					defer func() { <-sem }()
					if err := q.Syncer.SyncUser(ctx, uid); err != nil {
						slog.Error("sync failed in consumer", "user_id", uid, "error", err)
					}
					_ = d.Ack(false)
				}(msg, task.UserID)

			case <-ctx.Done():
				return
			}
		}

		time.Sleep(1 * time.Second)
	}
}

func (q *Queue) reconnectWithBackoff(ctx context.Context) error {
	backoff := 1 * time.Second
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		slog.Info("attempting to reconnect to AMQP broker...", "backoff", backoff)
		if err := q.connectAMQP(); err == nil {
			slog.Info("reconnection to AMQP broker successful")
			return nil
		} else {
			slog.Warn("AMQP reconnection attempt failed", "error", err)
		}

		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return ctx.Err()
		}

		backoff *= 2
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
	}
}

func (q *Queue) consumeExtractionInMemory(ctx context.Context) {
	slog.Info("started in-memory extraction consumer")
	sem := make(chan struct{}, 3)
	for {
		select {
		case platform := <-q.extractionCh:
			sem <- struct{}{}
			q.wg.Add(1)
			go func(plat string) {
				defer q.wg.Done()
				defer func() { <-sem }()
				q.handleExtraction(ctx, plat)
			}(platform)
		case <-ctx.Done():
			return
		}
	}
}

func (q *Queue) consumeSyncInMemory(ctx context.Context) {
	slog.Info("started in-memory sync consumer")
	sem := make(chan struct{}, 10)
	for {
		select {
		case userID := <-q.syncCh:
			sem <- struct{}{}
			q.wg.Add(1)
			go func(uid int) {
				defer q.wg.Done()
				defer func() { <-sem }()
				if err := q.Syncer.SyncUser(ctx, uid); err != nil {
					slog.Error("sync failed in in-memory consumer", "user_id", uid, "error", err)
				}
			}(userID)
		case <-ctx.Done():
			return
		}
	}
}

func (q *Queue) Drain() {
	q.wg.Wait()
}

func (q *Queue) handleExtraction(ctx context.Context, platform string) {
	fetcher, ok := extractor.Fetchers[platform]
	if !ok {
		slog.Error("unknown platform in extraction task", "platform", platform)
		return
	}

	slog.Info("processing extraction task", "platform", platform)
	contests, err := fetcher()
	if err != nil {
		slog.Error("failed to fetch contests in consumer", "platform", platform, "error", err)
		return
	}

	if len(contests) == 0 {
		slog.Info("scraper returned 0 upcoming contests, leaving existing database records intact", "platform", platform)
		return
	}

	newIDs := make([]string, 0, len(contests))
	for _, c := range contests {
		newIDs = append(newIDs, c.ID)
	}

	_, err = q.DB.Exec(ctx, "DELETE FROM contests WHERE platform = $1 AND start_time > NOW() AND id != ALL($2)", platform, newIDs)
	if err != nil {
		slog.Error("failed to prune obsolete upcoming contests in consumer", "platform", platform, "error", err)
	}

	batch := &pgx.Batch{}
	for _, c := range contests {
		batch.Queue(`
			INSERT INTO contests (id, name, url, start_time, end_time, duration, platform)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (id) DO UPDATE SET
				name = $2, url = $3, start_time = $4, end_time = $5, duration = $6, updated_at = NOW()
		`, c.ID, c.Name, c.URL, time.Unix(c.StartTime, 0), time.Unix(c.EndTime, 0), c.Duration, c.Platform)
	}

	br := q.DB.SendBatch(ctx, batch)
	defer br.Close()

	for range contests {
		if _, err := br.Exec(); err != nil {
			slog.Error("failed to save batch contest in consumer", "error", err)
		}
	}

	if q.Syncer != nil && q.Syncer.Valkey != nil {
		cacheKey := models.ContestsCacheKey(platform)
		if err := q.Syncer.Valkey.Del(ctx, cacheKey).Err(); err != nil {
			slog.Error("failed to invalidate valkey cache", "platform", platform, "error", err)
		}
	}

	if q.OTel != nil && q.OTel.ExtractionCounter != nil && len(contests) > 0 {
		q.OTel.ExtractionCounter.Add(ctx, int64(len(contests)), metric.WithAttributes(attribute.String("platform", platform)))
	}

	q.logDatabaseContestsTelemetry(ctx)
}

func (q *Queue) logDatabaseContestsTelemetry(ctx context.Context) {
	rows, err := q.DB.Query(ctx, "SELECT platform, COUNT(*) FROM contests WHERE start_time > NOW() GROUP BY platform")
	if err != nil {
		slog.Error("failed to query contest counts telemetry", "error", err)
		return
	}
	defer rows.Close()

	var platformCounts []any
	var summaryText strings.Builder
	totalContests := 0
	for rows.Next() {
		var plat string
		var count int
		if err := rows.Scan(&plat, &count); err == nil {
			platformCounts = append(platformCounts, plat, count)
			fmt.Fprintf(&summaryText, "• <b>%s</b>: %d contest(s)\n", plat, count)
			totalContests += count
		}
	}
	if err := rows.Err(); err != nil {
		slog.Error("error iterating contest counts telemetry rows", "error", err)
		return
	}
	slog.Info("database contest metrics updated", platformCounts...)

	if q.OnTelegramEvent != nil && totalContests > 0 {
		msg := fmt.Sprintf("📊 <b>[CONTEST EXTRACTION METRICS]</b>\n\nTotal Upcoming Contests: <b>%d</b>\n\n%s", totalContests, summaryText.String())
		q.OnTelegramEvent("CONTEST_EXTRACTION_SUCCESS", msg)
	}
}

func (q *Queue) Close() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.AMQPChannel != nil {
		_ = q.AMQPChannel.Close()
		q.AMQPChannel = nil
	}
	if q.AMQPConn != nil {
		err := q.AMQPConn.Close()
		q.AMQPConn = nil
		return err
	}
	return nil
}
