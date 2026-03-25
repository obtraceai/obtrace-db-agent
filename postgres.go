package main

import (
	"context"
	"fmt"
	"log"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresCollector collects metrics from a PostgreSQL database.
type PostgresCollector struct {
	pool     *pgxpool.Pool
	name     string
	instance string
	dbName   string
}

func NewPostgresCollector(dsn, name, instance string) (*PostgresCollector, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse postgres DSN: %w", err)
	}
	cfg.MaxConns = 2
	cfg.MinConns = 1

	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		return nil, fmt.Errorf("connect to postgres: %w", err)
	}

	dbName := cfg.ConnConfig.Database

	return &PostgresCollector{
		pool:     pool,
		name:     name,
		instance: instance,
		dbName:   dbName,
	}, nil
}

func (p *PostgresCollector) Type() string { return "postgres" }

func (p *PostgresCollector) Close() error {
	p.pool.Close()
	return nil
}

func (p *PostgresCollector) baseAttrs() map[string]string {
	return map[string]string{
		"db.system":   "postgres",
		"db.name":     p.name,
		"db.instance": p.instance,
	}
}

func (p *PostgresCollector) Collect(ctx context.Context) ([]Metric, error) {
	var metrics []Metric

	// Collect server version for instance metadata
	var version string
	_ = p.pool.QueryRow(ctx, "SHOW server_version").Scan(&version)
	metrics = append(metrics, Metric{
		Name:  "db.instance.info",
		Value: 1,
		Unit:  "1",
		Attributes: map[string]string{
			"db.system":   "postgres",
			"db.version":  version,
			"db.name":     p.name,
			"db.instance": p.instance,
		},
	})

	m, err := p.collectStatDatabase(ctx)
	if err != nil {
		log.Printf("WARN: postgres %s pg_stat_database: %v", p.name, err)
	} else {
		metrics = append(metrics, m...)
	}

	m, err = p.collectStatActivity(ctx)
	if err != nil {
		log.Printf("WARN: postgres %s pg_stat_activity: %v", p.name, err)
	} else {
		metrics = append(metrics, m...)
	}

	m, err = p.collectDatabaseSize(ctx)
	if err != nil {
		log.Printf("WARN: postgres %s pg_database_size: %v", p.name, err)
	} else {
		metrics = append(metrics, m...)
	}

	m, err = p.collectUserTables(ctx)
	if err != nil {
		log.Printf("WARN: postgres %s pg_stat_user_tables: %v", p.name, err)
	} else {
		metrics = append(metrics, m...)
	}

	m, err = p.collectStatStatements(ctx)
	if err != nil {
		// This is expected if pg_stat_statements is not installed
		log.Printf("DEBUG: postgres %s pg_stat_statements not available: %v", p.name, err)
	} else {
		metrics = append(metrics, m...)
	}

	return metrics, nil
}

func (p *PostgresCollector) collectStatDatabase(ctx context.Context) ([]Metric, error) {
	query := `
		SELECT
			numbackends,
			xact_commit,
			xact_rollback,
			blks_read,
			blks_hit,
			tup_returned,
			tup_fetched,
			tup_inserted,
			tup_updated,
			tup_deleted,
			deadlocks,
			temp_bytes
		FROM pg_stat_database
		WHERE datname = $1
	`

	var numbackends, xactCommit, xactRollback, blksRead, blksHit int64
	var tupReturned, tupFetched, tupInserted, tupUpdated, tupDeleted int64
	var deadlocks, tempBytes int64

	err := p.pool.QueryRow(ctx, query, p.dbName).Scan(
		&numbackends, &xactCommit, &xactRollback, &blksRead, &blksHit,
		&tupReturned, &tupFetched, &tupInserted, &tupUpdated, &tupDeleted,
		&deadlocks, &tempBytes,
	)
	if err != nil {
		return nil, fmt.Errorf("query pg_stat_database: %w", err)
	}

	attrs := p.baseAttrs()

	// Compute cache hit ratio
	var cacheHitRatio float64
	if blksHit+blksRead > 0 {
		cacheHitRatio = float64(blksHit) / float64(blksHit+blksRead)
	}

	return []Metric{
		{Name: "db.postgres.connections.active", Value: float64(numbackends), Unit: "{connections}", Attributes: attrs},
		{Name: "db.postgres.transactions.committed", Value: float64(xactCommit), Unit: "{transactions}", Attributes: attrs},
		{Name: "db.postgres.transactions.rolledback", Value: float64(xactRollback), Unit: "{transactions}", Attributes: attrs},
		{Name: "db.postgres.blocks.read", Value: float64(blksRead), Unit: "{blocks}", Attributes: attrs},
		{Name: "db.postgres.blocks.hit", Value: float64(blksHit), Unit: "{blocks}", Attributes: attrs},
		{Name: "db.postgres.cache_hit_ratio", Value: cacheHitRatio, Unit: "1", Attributes: attrs},
		{Name: "db.postgres.rows.returned", Value: float64(tupReturned), Unit: "{rows}", Attributes: attrs},
		{Name: "db.postgres.rows.fetched", Value: float64(tupFetched), Unit: "{rows}", Attributes: attrs},
		{Name: "db.postgres.rows.inserted", Value: float64(tupInserted), Unit: "{rows}", Attributes: attrs},
		{Name: "db.postgres.rows.updated", Value: float64(tupUpdated), Unit: "{rows}", Attributes: attrs},
		{Name: "db.postgres.rows.deleted", Value: float64(tupDeleted), Unit: "{rows}", Attributes: attrs},
		{Name: "db.postgres.deadlocks", Value: float64(deadlocks), Unit: "{deadlocks}", Attributes: attrs},
		{Name: "db.postgres.temp_bytes", Value: float64(tempBytes), Unit: "By", Attributes: attrs},
	}, nil
}

func (p *PostgresCollector) collectDatabaseSize(ctx context.Context) ([]Metric, error) {
	var sizeBytes int64
	err := p.pool.QueryRow(ctx, "SELECT pg_database_size($1)", p.dbName).Scan(&sizeBytes)
	if err != nil {
		return nil, fmt.Errorf("query pg_database_size: %w", err)
	}

	attrs := p.baseAttrs()
	return []Metric{
		{Name: "db.postgres.database_size_bytes", Value: float64(sizeBytes), Unit: "By", Attributes: attrs},
	}, nil
}

func (p *PostgresCollector) collectStatActivity(ctx context.Context) ([]Metric, error) {
	query := `
		SELECT
			COALESCE(SUM(CASE WHEN state = 'idle' THEN 1 ELSE 0 END), 0) AS idle,
			COALESCE(SUM(CASE WHEN state = 'active' THEN 1 ELSE 0 END), 0) AS active,
			COALESCE(SUM(CASE WHEN wait_event_type IS NOT NULL THEN 1 ELSE 0 END), 0) AS waiting,
			COALESCE(MAX(EXTRACT(EPOCH FROM (now() - query_start))::float8) FILTER (WHERE state = 'active'), 0) AS longest_query_s
		FROM pg_stat_activity
		WHERE datname = $1
	`

	var idle, active, waiting int64
	var longestQueryS float64

	err := p.pool.QueryRow(ctx, query, p.dbName).Scan(&idle, &active, &waiting, &longestQueryS)
	if err != nil {
		return nil, fmt.Errorf("query pg_stat_activity: %w", err)
	}

	attrs := p.baseAttrs()
	return []Metric{
		{Name: "db.postgres.connections.idle", Value: float64(idle), Unit: "{connections}", Attributes: attrs},
		{Name: "db.postgres.connections.active", Value: float64(active), Unit: "{connections}", Attributes: attrs},
		{Name: "db.postgres.connections.waiting", Value: float64(waiting), Unit: "{connections}", Attributes: attrs},
		{Name: "db.postgres.longest_query_seconds", Value: longestQueryS, Unit: "s", Attributes: attrs},
	}, nil
}

func (p *PostgresCollector) collectUserTables(ctx context.Context) ([]Metric, error) {
	query := `
		SELECT
			schemaname || '.' || relname AS table_name,
			COALESCE(seq_scan, 0),
			COALESCE(idx_scan, 0),
			COALESCE(n_dead_tup, 0),
			COALESCE(n_live_tup, 0)
		FROM pg_stat_user_tables
		ORDER BY seq_scan DESC NULLS LAST
		LIMIT 10
	`

	rows, err := p.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query pg_stat_user_tables: %w", err)
	}
	defer rows.Close()

	var metrics []Metric
	for rows.Next() {
		var tableName string
		var seqScan, idxScan, deadTup, liveTup int64
		if err := rows.Scan(&tableName, &seqScan, &idxScan, &deadTup, &liveTup); err != nil {
			continue
		}
		attrs := p.baseAttrs()
		attrs["db.sql.table"] = tableName

		metrics = append(metrics,
			Metric{Name: "db.postgres.table.seq_scan", Value: float64(seqScan), Unit: "{scans}", Attributes: attrs},
			Metric{Name: "db.postgres.table.idx_scan", Value: float64(idxScan), Unit: "{scans}", Attributes: attrs},
			Metric{Name: "db.postgres.table.dead_tuples", Value: float64(deadTup), Unit: "{tuples}", Attributes: attrs},
			Metric{Name: "db.postgres.table.live_tuples", Value: float64(liveTup), Unit: "{tuples}", Attributes: attrs},
		)
	}

	return metrics, nil
}

func (p *PostgresCollector) collectStatStatements(ctx context.Context) ([]Metric, error) {
	// Check if pg_stat_statements is available
	var extExists bool
	err := p.pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_extension WHERE extname = 'pg_stat_statements')").Scan(&extExists)
	if err != nil || !extExists {
		return nil, fmt.Errorf("pg_stat_statements extension not available")
	}

	query := `
		SELECT
			queryid::text,
			LEFT(query, 100) AS query_text,
			mean_exec_time,
			calls
		FROM pg_stat_statements
		ORDER BY mean_exec_time DESC
		LIMIT 10
	`

	rows, err := p.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query pg_stat_statements: %w", err)
	}
	defer rows.Close()

	var metrics []Metric
	for rows.Next() {
		var queryID, queryText string
		var meanExecTime float64
		var calls int64

		if err := rows.Scan(&queryID, &queryText, &meanExecTime, &calls); err != nil {
			if err == pgx.ErrNoRows {
				break
			}
			continue
		}

		attrs := p.baseAttrs()
		attrs["db.statement"] = queryText
		attrs["db.query_id"] = queryID

		metrics = append(metrics,
			Metric{Name: "db.postgres.query.avg_time_ms", Value: meanExecTime, Unit: "ms", Attributes: attrs},
			Metric{Name: "db.postgres.query.calls", Value: float64(calls), Unit: "{calls}", Attributes: attrs},
		)
	}

	return metrics, nil
}
