package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ClickHouseCollector collects metrics from a ClickHouse instance using the
// HTTP API (port 8123). No external driver dependency is needed.
type ClickHouseCollector struct {
	endpoint string // http://host:8123
	name     string
	instance string
	client   *http.Client
	user     string
	password string
}

func NewClickHouseCollector(dsn, name, instance string) (*ClickHouseCollector, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse clickhouse DSN: %w", err)
	}

	endpoint := fmt.Sprintf("%s://%s", u.Scheme, u.Host)
	user := ""
	password := ""
	if u.User != nil {
		user = u.User.Username()
		password, _ = u.User.Password()
	}

	client := &http.Client{Timeout: 10 * time.Second}

	// Verify connectivity
	testURL := endpoint + "/?query=" + url.QueryEscape("SELECT 1 FORMAT JSON")
	req, err := http.NewRequest(http.MethodGet, testURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create test request: %w", err)
	}
	if user != "" {
		req.SetBasicAuth(user, password)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connect to clickhouse: %w", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("clickhouse returned %d on connectivity test", resp.StatusCode)
	}

	return &ClickHouseCollector{
		endpoint: endpoint,
		name:     name,
		instance: instance,
		client:   client,
		user:     user,
		password: password,
	}, nil
}

func (c *ClickHouseCollector) Type() string { return "clickhouse" }

func (c *ClickHouseCollector) Close() error {
	c.client.CloseIdleConnections()
	return nil
}

func (c *ClickHouseCollector) baseAttrs() map[string]string {
	return map[string]string{
		"db.system":   "clickhouse",
		"db.name":     c.name,
		"db.instance": c.instance,
	}
}

func (c *ClickHouseCollector) Collect(ctx context.Context) ([]Metric, error) {
	var metrics []Metric

	m, err := c.collectSystemMetrics(ctx)
	if err != nil {
		log.Printf("WARN: clickhouse %s system.metrics: %v", c.name, err)
	} else {
		metrics = append(metrics, m...)
	}

	m, err = c.collectSystemEvents(ctx)
	if err != nil {
		log.Printf("WARN: clickhouse %s system.events: %v", c.name, err)
	} else {
		metrics = append(metrics, m...)
	}

	m, err = c.collectSystemParts(ctx)
	if err != nil {
		log.Printf("WARN: clickhouse %s system.parts: %v", c.name, err)
	} else {
		metrics = append(metrics, m...)
	}

	return metrics, nil
}

// chQueryResult represents a ClickHouse JSON format result.
type chQueryResult struct {
	Data []map[string]interface{} `json:"data"`
}

func (c *ClickHouseCollector) query(ctx context.Context, sql string) (*chQueryResult, error) {
	reqURL := c.endpoint + "/?query=" + url.QueryEscape(sql+" FORMAT JSON")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	if c.user != "" {
		req.SetBasicAuth(c.user, c.password)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("query clickhouse: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("query returned %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var result chQueryResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse JSON: %w", err)
	}

	return &result, nil
}

func (c *ClickHouseCollector) collectSystemMetrics(ctx context.Context) ([]Metric, error) {
	sql := `SELECT metric, value FROM system.metrics WHERE metric IN ('Query', 'HTTPConnection', 'TCPConnection', 'MemoryTracking')`

	result, err := c.query(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("system.metrics: %w", err)
	}

	attrs := c.baseAttrs()
	var metrics []Metric

	nameMap := map[string]struct {
		name string
		unit string
	}{
		"Query":          {name: "db.clickhouse.queries.active", unit: "{queries}"},
		"HTTPConnection": {name: "db.clickhouse.connections.http", unit: "{connections}"},
		"TCPConnection":  {name: "db.clickhouse.connections.tcp", unit: "{connections}"},
		"MemoryTracking": {name: "db.clickhouse.memory.tracked_bytes", unit: "By"},
	}

	for _, row := range result.Data {
		metricName, _ := row["metric"].(string)
		if mapping, ok := nameMap[metricName]; ok {
			if val := chToFloat64(row["value"]); val != 0 || metricName != "" {
				metrics = append(metrics, Metric{
					Name: mapping.name, Value: chToFloat64(row["value"]), Unit: mapping.unit, Attributes: attrs,
				})
			}
		}
	}

	return metrics, nil
}

func (c *ClickHouseCollector) collectSystemEvents(ctx context.Context) ([]Metric, error) {
	sql := `SELECT event, value FROM system.events WHERE event IN ('Query', 'FailedQuery', 'Merge', 'InsertedRows', 'InsertedBytes', 'SelectedRows', 'SelectedBytes')`

	result, err := c.query(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("system.events: %w", err)
	}

	attrs := c.baseAttrs()
	var metrics []Metric

	nameMap := map[string]struct {
		name string
		unit string
	}{
		"Query":         {name: "db.clickhouse.queries.total", unit: "{queries}"},
		"FailedQuery":   {name: "db.clickhouse.queries.failed", unit: "{queries}"},
		"Merge":         {name: "db.clickhouse.merges.total", unit: "{merges}"},
		"InsertedRows":  {name: "db.clickhouse.inserted_rows", unit: "{rows}"},
		"InsertedBytes": {name: "db.clickhouse.inserted_bytes", unit: "By"},
		"SelectedRows":  {name: "db.clickhouse.selected_rows", unit: "{rows}"},
		"SelectedBytes": {name: "db.clickhouse.selected_bytes", unit: "By"},
	}

	for _, row := range result.Data {
		eventName, _ := row["event"].(string)
		if mapping, ok := nameMap[eventName]; ok {
			metrics = append(metrics, Metric{
				Name: mapping.name, Value: chToFloat64(row["value"]), Unit: mapping.unit, Attributes: attrs,
			})
		}
	}

	return metrics, nil
}

func (c *ClickHouseCollector) collectSystemParts(ctx context.Context) ([]Metric, error) {
	sql := `SELECT count() AS active_parts, sum(bytes_on_disk) AS total_bytes FROM system.parts WHERE active = 1`

	result, err := c.query(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("system.parts: %w", err)
	}

	if len(result.Data) == 0 {
		return nil, nil
	}

	attrs := c.baseAttrs()
	row := result.Data[0]

	return []Metric{
		{Name: "db.clickhouse.parts.active", Value: chToFloat64(row["active_parts"]), Unit: "{parts}", Attributes: attrs},
		{Name: "db.clickhouse.parts.total_bytes", Value: chToFloat64(row["total_bytes"]), Unit: "By", Attributes: attrs},
	}, nil
}

// chToFloat64 converts a ClickHouse JSON value (which may be string or number) to float64.
func chToFloat64(v interface{}) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case json.Number:
		f, _ := n.Float64()
		return f
	case string:
		// ClickHouse HTTP JSON format often returns numbers as strings
		f := 0.0
		fmt.Sscanf(strings.TrimSpace(n), "%f", &f)
		return f
	default:
		return 0
	}
}
