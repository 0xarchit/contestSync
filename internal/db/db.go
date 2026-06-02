package db

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync/atomic"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Pool struct {
	write   *pgxpool.Pool
	reads   []*pgxpool.Pool
	readIdx atomic.Uint64
}

func (p *Pool) WriteDB() *pgxpool.Pool {
	return p.write
}

func (p *Pool) ReadDB() *pgxpool.Pool {
	if len(p.reads) == 0 {
		return p.write
	}
	idx := p.readIdx.Add(1) - 1
	return p.reads[idx%uint64(len(p.reads))]
}

func (p *Pool) Close() {
	if p.write != nil {
		p.write.Close()
	}
	for _, r := range p.reads {
		r.Close()
	}
}

func newPool(ctx context.Context, dsn string, maxConns int32) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("unable to parse DSN: %w", err)
	}
	cfg.MaxConns = maxConns
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("unable to create pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("unable to reach database: %w", err)
	}
	return pool, nil
}

func Init(ctx context.Context, writeURL string, readURLsRaw string, writeLimit int, poolLimit int) (*Pool, error) {
	if writeURL == "" {
		return nil, fmt.Errorf("POSTGRES_DB environment variable is not set")
	}

	writeConns := int32(10)
	if writeLimit > 0 && writeLimit <= math.MaxInt32 {
		writeConns = int32(writeLimit)
	}

	readConns := int32(100)
	if poolLimit > 0 && poolLimit <= math.MaxInt32 {
		readConns = int32(poolLimit)
	}

	writePool, err := newPool(ctx, writeURL, writeConns)
	if err != nil {
		return nil, fmt.Errorf("write pool: %w", err)
	}

	p := &Pool{write: writePool}

	if readURLsRaw != "" {
		rawURLs := strings.Split(readURLsRaw, ",")
		for i, rawURL := range rawURLs {
			rawURL = strings.TrimSpace(rawURL)
			if rawURL == "" {
				continue
			}
			rPool, err := newPool(ctx, rawURL, readConns)
			if err != nil {
				slog.Warn("read replica pool failed to connect, skipping", "replica", i, "error", err)
				continue
			}
			p.reads = append(p.reads, rPool)
		}
		slog.Info("read replicas connected", "count", len(p.reads))
	}

	if len(p.reads) == 0 {
		slog.Info("no read replicas configured, using write pool for reads")
	}

	return p, nil
}
