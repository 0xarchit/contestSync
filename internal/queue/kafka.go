package queue

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"log/slog"
	stdSync "sync"
	"time"

	"github.com/0xarchit/contestsync/config"
	"github.com/0xarchit/contestsync/internal/extractor"
	"github.com/0xarchit/contestsync/internal/sync"
	"github.com/0xarchit/contestsync/models"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/segmentio/kafka-go"
)

const (
	TopicExtraction = "extraction-tasks"
	TopicSync       = "sync-tasks"
	TopicHealth     = "health-check-tasks"
)

type TaskType string

const (
	TaskExtraction TaskType = "extraction"
	TaskSync       TaskType = "sync"
)

type ExtractionTask struct {
	Platform string `json:"platform"`
}

type SyncTask struct {
	UserID int `json:"user_id"`
}

type Queue struct {
	Producer     *kafka.Writer
	DB           *pgxpool.Pool
	Syncer       *sync.Syncer
	useInMemory  bool
	extractionCh chan string
	syncCh       chan int
	kafkaBroker  string
	kafkaTLS     *tls.Config
	wg           stdSync.WaitGroup
}

func New(cfg *config.Config, db *pgxpool.Pool, syncer *sync.Syncer) (*Queue, error) {
	if cfg.KafkaHost == "" {
		slog.Info("Kafka host not configured; falling back to in-memory queue")
		return &Queue{
			DB:           db,
			Syncer:       syncer,
			useInMemory:  true,
			extractionCh: make(chan string, 100),
			syncCh:       make(chan int, 1000),
		}, nil
	}

	tlsConfig, err := createTLSConfig(cfg)
	if err != nil {
		return nil, err
	}

	broker := fmt.Sprintf("%s:%s", cfg.KafkaHost, cfg.KafkaPort)

	writer := &kafka.Writer{
		Addr:     kafka.TCP(broker),
		Balancer: &kafka.LeastBytes{},
		Transport: &kafka.Transport{
			TLS: tlsConfig,
		},
		Async: false,
	}

	if err := ensureTopics(cfg, tlsConfig, broker); err != nil {
		return nil, fmt.Errorf("failed to ensure kafka topics: %w", err)
	}

	return &Queue{
		Producer:    writer,
		DB:          db,
		Syncer:      syncer,
		kafkaBroker: broker,
		kafkaTLS:    tlsConfig,
	}, nil
}

func ensureTopics(cfg *config.Config, tlsConfig *tls.Config, broker string) error {
	dialer := &kafka.Dialer{
		Timeout:   10 * time.Second,
		DualStack: true,
		TLS:       tlsConfig,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, err := dialer.DialContext(ctx, "tcp", broker)
	if err != nil {
		return err
	}
	defer conn.Close()
	partitions, err := conn.ReadPartitions()
	if err != nil {
		return err
	}
	existing := make(map[string]bool)
	for _, p := range partitions {
		existing[p.Topic] = true
	}

	var toCreate []kafka.TopicConfig
	if !existing[TopicExtraction] {
		toCreate = append(toCreate, kafka.TopicConfig{Topic: TopicExtraction, NumPartitions: 1, ReplicationFactor: cfg.KafkaReplicationFactor})
	}
	if !existing[TopicSync] {
		toCreate = append(toCreate, kafka.TopicConfig{Topic: TopicSync, NumPartitions: cfg.KafkaPartitions, ReplicationFactor: cfg.KafkaReplicationFactor})
	}
	if !existing[TopicHealth] {
		toCreate = append(toCreate, kafka.TopicConfig{Topic: TopicHealth, NumPartitions: 1, ReplicationFactor: cfg.KafkaReplicationFactor})
	}

	if len(toCreate) > 0 {
		if err := conn.CreateTopics(toCreate...); err != nil {
			slog.Error("failed to create missing kafka topics", "error", err)
		} else {
			slog.Info("successfully created missing kafka topics")
		}
	}
	return nil
}

func (q *Queue) Health(ctx context.Context) error {
	if q.useInMemory {
		return nil
	}
	dialer := &kafka.Dialer{
		Timeout: 10 * time.Second,
		TLS:     q.kafkaTLS,
	}
	conn, err := dialer.DialLeader(ctx, "tcp", q.kafkaBroker, TopicHealth, 0)
	if err != nil {
		return fmt.Errorf("failed to dial leader: %w", err)
	}
	defer conn.Close()
	_, lastOffset, err := conn.ReadOffsets()
	if err != nil {
		return fmt.Errorf("failed to read offsets: %w", err)
	}
	probe := fmt.Sprintf("health-probe-%d", time.Now().UnixNano())
	_, err = conn.WriteMessages(kafka.Message{
		Value: []byte(probe),
	})
	if err != nil {
		return fmt.Errorf("failed to write messages: %w", err)
	}
	_, err = conn.Seek(lastOffset, kafka.SeekAbsolute)
	if err != nil {
		return fmt.Errorf("failed to seek offset: %w", err)
	}
	m, err := conn.ReadMessage(1024)
	if err != nil {
		return fmt.Errorf("failed to read message: %w", err)
	}
	if string(m.Value) != probe {
		return fmt.Errorf("read unexpected message: expected %s, got %s", probe, string(m.Value))
	}
	return nil
}

func createTLSConfig(cfg *config.Config) (*tls.Config, error) {
	if len(cfg.KafkaAccessCert) == 0 || len(cfg.KafkaAccessKey) == 0 || len(cfg.KafkaCACert) == 0 {
		return nil, nil
	}

	cert, err := tls.X509KeyPair(cfg.KafkaAccessCert, cfg.KafkaAccessKey)
	if err != nil {
		return nil, fmt.Errorf("failed to load client cert/key: %w", err)
	}

	caCertPool := x509.NewCertPool()
	if ok := caCertPool.AppendCertsFromPEM(cfg.KafkaCACert); !ok {
		return nil, fmt.Errorf("failed to append CA cert")
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      caCertPool,
		MinVersion:   tls.VersionTLS12,
	}, nil
}

func (q *Queue) PublishExtractionTask(ctx context.Context, platform string) error {
	if q.useInMemory {
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
	val, _ := json.Marshal(task)
	return q.Producer.WriteMessages(ctx, kafka.Message{
		Topic: TopicExtraction,
		Key:   []byte(platform),
		Value: val,
	})
}

func (q *Queue) PublishSyncTask(ctx context.Context, userID int) error {
	if q.useInMemory {
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
	val, _ := json.Marshal(task)
	return q.Producer.WriteMessages(ctx, kafka.Message{
		Topic: TopicSync,
		Key:   []byte(fmt.Sprintf("%d", userID)),
		Value: val,
	})
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
	go q.consumeExtraction(ctx, cfg)
	go q.consumeSync(ctx, cfg)
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

func (q *Queue) consumeExtraction(ctx context.Context, cfg *config.Config) {
	tlsConfig, err := createTLSConfig(cfg)
	if err != nil {
		slog.Error("failed to create TLS config", "error", err)
		return
	}
	broker := fmt.Sprintf("%s:%s", cfg.KafkaHost, cfg.KafkaPort)

	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers: []string{broker},
		Topic:   TopicExtraction,
		GroupID: "extraction-group",
		Dialer: &kafka.Dialer{
			TLS: tlsConfig,
		},
		MaxWait: 5 * time.Second,
	})
	defer r.Close()

	slog.Info("started extraction consumer")
	sem := make(chan struct{}, 3)

	for {
		m, err := r.ReadMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Error("kafka extraction reader error", "error", err)
			continue
		}

		var task ExtractionTask
		if err := json.Unmarshal(m.Value, &task); err != nil {
			slog.Error("failed to unmarshal extraction task", "error", err)
			continue
		}

		sem <- struct{}{}
		q.wg.Add(1)
		go func(plat string) {
			defer q.wg.Done()
			defer func() { <-sem }()
			q.handleExtraction(ctx, plat)
		}(task.Platform)
	}
}

func (q *Queue) consumeSync(ctx context.Context, cfg *config.Config) {
	tlsConfig, err := createTLSConfig(cfg)
	if err != nil {
		slog.Error("failed to create TLS config", "error", err)
		return
	}
	broker := fmt.Sprintf("%s:%s", cfg.KafkaHost, cfg.KafkaPort)

	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers: []string{broker},
		Topic:   TopicSync,
		GroupID: "sync-group",
		Dialer: &kafka.Dialer{
			TLS: tlsConfig,
		},
		MaxWait: 5 * time.Second,
	})
	defer r.Close()

	slog.Info("started sync consumer")
	sem := make(chan struct{}, 10)

	for {
		m, err := r.ReadMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Error("kafka sync reader error", "error", err)
			continue
		}

		var task SyncTask
		if err := json.Unmarshal(m.Value, &task); err != nil {
			slog.Error("failed to unmarshal sync task", "error", err)
			continue
		}

		sem <- struct{}{}
		q.wg.Add(1)
		go func(uid int) {
			defer q.wg.Done()
			defer func() { <-sem }()
			if err := q.Syncer.SyncUser(ctx, uid); err != nil {
				slog.Error("sync failed in consumer", "user_id", uid, "error", err)
			}
		}(task.UserID)
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
		return
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
}

func (q *Queue) Close() error {
	if q.Producer != nil {
		return q.Producer.Close()
	}
	return nil
}
