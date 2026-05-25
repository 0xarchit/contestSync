package db

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

func Init(ctx context.Context, databaseURL string, caCert []byte, connectionLimit int) (*pgxpool.Pool, error) {
	if databaseURL == "" {
		return nil, fmt.Errorf("POSTGRES_DB environment variable is not set")
	}

	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("unable to parse database DSN: %w", err)
	}

	if len(caCert) > 0 {
		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA certificate PEM")
		}
		if config.ConnConfig.TLSConfig == nil {
			config.ConnConfig.TLSConfig = &tls.Config{}
		}
		config.ConnConfig.TLSConfig.RootCAs = caCertPool
	}

	maxConns := int32(10)
	if connectionLimit > 0 && connectionLimit <= 2147483647 {
		maxConns = int32(connectionLimit)
	}
	config.MaxConns = maxConns

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("unable to create connection pool: %w", err)
	}

	// Test connection
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("unable to connect to database: %w", err)
	}

	return pool, nil
}
