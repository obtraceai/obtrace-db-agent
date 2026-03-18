package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strconv"

	_ "github.com/go-sql-driver/mysql"
)

// MySQLCollector collects metrics from a MySQL database.
type MySQLCollector struct {
	db       *sql.DB
	name     string
	instance string
}

func NewMySQLCollector(dsn, name, instance string) (*MySQLCollector, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("open mysql: %w", err)
	}
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(1)

	if err := db.PingContext(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping mysql: %w", err)
	}

	return &MySQLCollector{
		db:       db,
		name:     name,
		instance: instance,
	}, nil
}

func (m *MySQLCollector) Type() string { return "mysql" }

func (m *MySQLCollector) Close() error {
	return m.db.Close()
}

func (m *MySQLCollector) baseAttrs() map[string]string {
	return map[string]string{
		"db.system":   "mysql",
		"db.name":     m.name,
		"db.instance": m.instance,
	}
}

func (m *MySQLCollector) Collect(ctx context.Context) ([]DBMetric, error) {
	var allMetrics []DBMetric

	// Collect server version for instance metadata
	var version string
	if err := m.db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&version); err == nil {
		allMetrics = append(allMetrics, DBMetric{
			Name:  "db.instance.info",
			Value: 1,
			Unit:  "1",
			Attributes: map[string]string{
				"db.system":   "mysql",
				"db.version":  version,
				"db.name":     m.name,
				"db.instance": m.instance,
			},
		})
	}

	metrics, err := m.collectGlobalStatus(ctx)
	if err != nil {
		log.Printf("WARN: mysql %s SHOW GLOBAL STATUS: %v", m.name, err)
		return allMetrics, err
	}
	allMetrics = append(allMetrics, metrics...)
	return allMetrics, nil
}

func (m *MySQLCollector) collectGlobalStatus(ctx context.Context) ([]DBMetric, error) {
	rows, err := m.db.QueryContext(ctx, "SHOW GLOBAL STATUS")
	if err != nil {
		return nil, fmt.Errorf("SHOW GLOBAL STATUS: %w", err)
	}
	defer rows.Close()

	status := make(map[string]string)
	for rows.Next() {
		var name, value string
		if err := rows.Scan(&name, &value); err != nil {
			continue
		}
		status[name] = value
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate status rows: %w", err)
	}

	attrs := m.baseAttrs()
	var metrics []DBMetric

	addGauge := func(metricName, statusKey, unit string) {
		if v, ok := status[statusKey]; ok {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				metrics = append(metrics, DBMetric{Name: metricName, Value: f, Unit: unit, Attributes: attrs})
			}
		}
	}

	addGauge("db.mysql.connections.current", "Threads_connected", "{connections}")
	addGauge("db.mysql.connections.running", "Threads_running", "{connections}")
	addGauge("db.mysql.queries.total", "Questions", "{queries}")
	addGauge("db.mysql.queries.slow", "Slow_queries", "{queries}")
	addGauge("db.mysql.bytes.sent", "Bytes_sent", "By")
	addGauge("db.mysql.bytes.received", "Bytes_received", "By")
	addGauge("db.mysql.innodb.row_lock_waits", "Innodb_row_lock_waits", "{waits}")
	addGauge("db.mysql.innodb.row_lock_time_ms", "Innodb_row_lock_time", "ms")
	addGauge("db.mysql.connections.aborted", "Aborted_connects", "{connections}")
	addGauge("db.mysql.table_locks.waited", "Table_locks_waited", "{locks}")

	// Compute InnoDB buffer pool hit ratio
	readRequests, rOK := parseStatusFloat(status, "Innodb_buffer_pool_read_requests")
	reads, dOK := parseStatusFloat(status, "Innodb_buffer_pool_reads")
	if rOK && dOK && readRequests > 0 {
		hitRatio := (readRequests - reads) / readRequests
		metrics = append(metrics, DBMetric{
			Name:       "db.mysql.innodb.buffer_pool_hit_ratio",
			Value:      hitRatio,
			Unit:       "1",
			Attributes: attrs,
		})
	}

	return metrics, nil
}

func parseStatusFloat(status map[string]string, key string) (float64, bool) {
	v, ok := status[key]
	if !ok {
		return 0, false
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}
