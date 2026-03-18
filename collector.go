package main

import "context"

// DBMetric represents a single metric collected from a database.
type DBMetric struct {
	Name       string
	Value      float64
	Unit       string
	Attributes map[string]string
}

// Collector is the interface that each database collector must implement.
type Collector interface {
	// Type returns the database system type (e.g. "postgres", "redis").
	Type() string
	// Collect gathers metrics from the database. It should use the context
	// for timeout/cancellation and return collected metrics or an error.
	Collect(ctx context.Context) ([]DBMetric, error)
	// Close cleans up any resources (connections, pools) held by the collector.
	Close() error
}

// ConnectionConfig describes a single database connection to monitor.
type ConnectionConfig struct {
	Type string `json:"type"`
	DSN  string `json:"dsn"`
	Name string `json:"name"`
}
