package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"

	_ "modernc.org/sqlite"
)

// SQLiteCollector collects metrics from a SQLite database file using PRAGMA
// queries. It uses the pure-Go modernc.org/sqlite driver (no CGO required).
type SQLiteCollector struct {
	db       *sql.DB
	name     string
	instance string
}

func NewSQLiteCollector(dsn, name, instance string) (*SQLiteCollector, error) {
	// Open in read-only mode with WAL mode support
	connStr := fmt.Sprintf("file:%s?mode=ro", dsn)
	db, err := sql.Open("sqlite", connStr)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	return &SQLiteCollector{
		db:       db,
		name:     name,
		instance: instance,
	}, nil
}

func (s *SQLiteCollector) Type() string { return "sqlite" }

func (s *SQLiteCollector) Close() error {
	return s.db.Close()
}

func (s *SQLiteCollector) baseAttrs() map[string]string {
	return map[string]string{
		"db.system":   "sqlite",
		"db.name":     s.name,
		"db.instance": s.instance,
	}
}

func (s *SQLiteCollector) Collect(ctx context.Context) ([]Metric, error) {
	attrs := s.baseAttrs()
	var metrics []Metric

	// Page count
	var pageCount int64
	if err := s.db.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pageCount); err != nil {
		log.Printf("WARN: sqlite %s PRAGMA page_count: %v", s.name, err)
	}

	// Page size
	var pageSize int64
	if err := s.db.QueryRowContext(ctx, "PRAGMA page_size").Scan(&pageSize); err != nil {
		log.Printf("WARN: sqlite %s PRAGMA page_size: %v", s.name, err)
	}

	// Database size
	if pageCount > 0 && pageSize > 0 {
		dbSize := float64(pageCount) * float64(pageSize)
		metrics = append(metrics, Metric{
			Name: "db.sqlite.database_size_bytes", Value: dbSize, Unit: "By", Attributes: attrs,
		})
	}

	metrics = append(metrics, Metric{
		Name: "db.sqlite.page_size", Value: float64(pageSize), Unit: "By", Attributes: attrs,
	})

	// Cache size
	var cacheSize int64
	if err := s.db.QueryRowContext(ctx, "PRAGMA cache_size").Scan(&cacheSize); err != nil {
		log.Printf("WARN: sqlite %s PRAGMA cache_size: %v", s.name, err)
	} else {
		// cache_size can be negative (meaning KB), we report the absolute value as pages
		if cacheSize < 0 {
			// Negative means size in KiB; convert to approximate page count
			cacheSize = (-cacheSize * 1024) / pageSize
		}
		metrics = append(metrics, Metric{
			Name: "db.sqlite.cache_size_pages", Value: float64(cacheSize), Unit: "{pages}", Attributes: attrs,
		})
	}

	// WAL checkpoint (only available in WAL mode)
	var walPages, checkpointed int64
	err := s.db.QueryRowContext(ctx, "PRAGMA wal_checkpoint(PASSIVE)").Scan(
		new(int64), // busy flag
		&walPages,
		&checkpointed,
	)
	if err != nil {
		// Not in WAL mode or WAL not available; this is expected for journal mode
		log.Printf("DEBUG: sqlite %s PRAGMA wal_checkpoint: %v", s.name, err)
	} else {
		metrics = append(metrics, Metric{
			Name: "db.sqlite.wal_checkpoint_count", Value: float64(checkpointed), Unit: "{checkpoints}", Attributes: attrs,
		})
	}

	return metrics, nil
}
