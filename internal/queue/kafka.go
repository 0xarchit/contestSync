package queue

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/0xarchit/contestsync/config"
	"github.com/0xarchit/contestsync/internal/extractor"
	"github.com/0xarchit/contestsync/internal/sync"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/segmentio/kafka-go"
)

const (
	TopicExtraction = "extraction-tasks"
	TopicSync       = "sync-tasks"
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

	_ = ensureTopics(tlsConfig, broker)

	return &Queue{
		Producer:    writer,
		DB:          db,
		Syncer:      syncer,
		kafkaBroker: broker,
		kafkaTLS:    tlsConfig,
	}, nil
}

func ensureTopics(tlsConfig *tls.Config, broker string) error {
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
	topicConfigs := []kafka.TopicConfig{
		{Topic: TopicExtraction, NumPartitions: 1, ReplicationFactor: 3},
		{Topic: TopicSync, NumPartitions: 1, ReplicationFactor: 3},
	}
	err = conn.CreateTopics(topicConfigs...)
	if err != nil {
		topicConfigs1 := []kafka.TopicConfig{
			{Topic: TopicExtraction, NumPartitions: 1, ReplicationFactor: 1},
			{Topic: TopicSync, NumPartitions: 1, ReplicationFactor: 1},
		}
		err2 := conn.CreateTopics(topicConfigs1...)
		if err2 != nil {
			slog.Warn("could not auto-create kafka topics", "err", err2)
		}
	} else {
		slog.Info("successfully created kafka topics")
	}
	return nil
}

func (q *Queue) Health(ctx context.Context) error {
	if q.useInMemory {
		return nil
	}

	probe := fmt.Sprintf("health-probe-%d", time.Now().UnixNano())

	err := q.Producer.WriteMessages(ctx, kafka.Message{
		Topic: TopicExtraction,
		Key:   []byte("__health__"),
		Value: []byte(probe),
	})
	if err != nil {
		return fmt.Errorf("kafka publish failed: %w", err)
	}

	dialer := &kafka.Dialer{TLS: q.kafkaTLS}
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers:   []string{q.kafkaBroker},
		Topic:     TopicExtraction,
		GroupID:   "health-check-group",
		Dialer:    dialer,
		MaxWait:   2 * time.Second,
		MinBytes:  1,
		MaxBytes:  1024,
	})
	defer r.Close()

	readCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()

	for {
		m, err := r.ReadMessage(readCtx)
		if err != nil {
			return fmt.Errorf("kafka consume failed: %w", err)
		}
		if string(m.Value) == probe {
			return nil
		}
	}
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
	for {
		select {
		case platform := <-q.extractionCh:
			q.handleExtraction(ctx, platform)
		case <-ctx.Done():
			return
		}
	}
}

func (q *Queue) consumeSyncInMemory(ctx context.Context) {
	slog.Info("started in-memory sync consumer")
	for {
		select {
		case userID := <-q.syncCh:
			if err := q.Syncer.SyncUser(ctx, userID); err != nil {
				slog.Error("sync failed in in-memory consumer", "user_id", userID, "error", err)
			}
		case <-ctx.Done():
			return
		}
	}
}

func (q *Queue) consumeExtraction(ctx context.Context, cfg *config.Config) {
	tlsConfig, _ := createTLSConfig(cfg)
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

		q.handleExtraction(ctx, task.Platform)
	}
}

func (q *Queue) consumeSync(ctx context.Context, cfg *config.Config) {
	tlsConfig, _ := createTLSConfig(cfg)
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

		if err := q.Syncer.SyncUser(ctx, task.UserID); err != nil {
			slog.Error("sync failed in consumer", "user_id", task.UserID, "error", err)
		}
	}
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
}
