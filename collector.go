package main

import "context"

type Metric struct {
	Name       string
	Value      float64
	Unit       string
	Attributes map[string]string
}

type Collector interface {
	Type() string
	Collect(ctx context.Context) ([]Metric, error)
	Close() error
}

type ConnectionConfig struct {
	Type string `json:"type"`
	DSN  string `json:"dsn"`
	Name string `json:"name"`
}
