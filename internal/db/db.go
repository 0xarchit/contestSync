package db

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

func Init(ctx context.Context, databaseURL string, caCert []byte, connectionLimit int) (*pgxpool.Pool, error) {
	if databaseURL == "" {
		return nil, fmt.Errorf("POSTGRES_DB environment variable is not set")
	}

	finalDSN := databaseURL

	if len(caCert) > 0 {
		tempDir := os.TempDir()
		certPath := filepath.Join(tempDir, "aiven-ca.pem")
		
		if err := os.WriteFile(certPath, caCert, 0600); err != nil {
			return nil, fmt.Errorf("failed to write CA certificate: %w", err)
		}

		// Inject sslrootcert into connection string
		if strings.Contains(finalDSN, "?") {
			finalDSN += "&sslrootcert=" + certPath
		} else {
			finalDSN += "?sslrootcert=" + certPath
		}
	}

	config, err := pgxpool.ParseConfig(finalDSN)
	if err != nil {
		return nil, fmt.Errorf("unable to parse database DSN: %w", err)
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
