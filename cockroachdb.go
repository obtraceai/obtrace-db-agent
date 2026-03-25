package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// CockroachDBCollector collects metrics from a CockroachDB node by scraping
// the _status/vars Prometheus endpoint.
type CockroachDBCollector struct {
	endpoint string
	name     string
	instance string
	client   *http.Client
}

func NewCockroachDBCollector(dsn, name, instance string) (*CockroachDBCollector, error) {
	endpoint := strings.TrimRight(dsn, "/")
	client := &http.Client{Timeout: 10 * time.Second}

	// Verify connectivity
	resp, err := client.Get(endpoint + "/_status/vars")
	if err != nil {
		return nil, fmt.Errorf("connect to cockroachdb: %w", err)
	}
	resp.Body.Close()

	return &CockroachDBCollector{
		endpoint: endpoint,
		name:     name,
		instance: instance,
		client:   client,
	}, nil
}

func (c *CockroachDBCollector) Type() string { return "cockroachdb" }

func (c *CockroachDBCollector) Close() error {
	c.client.CloseIdleConnections()
	return nil
}

func (c *CockroachDBCollector) baseAttrs() map[string]string {
	return map[string]string{
		"db.system":   "cockroachdb",
		"db.name":     c.name,
		"db.instance": c.instance,
	}
}

func (c *CockroachDBCollector) Collect(ctx context.Context) ([]Metric, error) {
	url := c.endpoint + "/_status/vars"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("GET %s returned %d: %s", url, resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	promMetrics := parseCRDBPrometheus(string(body))
	attrs := c.baseAttrs()
	var metrics []Metric

	type metricMapping struct {
		promName string
		name     string
		unit     string
	}

	mappings := []metricMapping{
		{promName: "sql_query_count", name: "db.cockroachdb.sql.query_count", unit: "{queries}"},
		{promName: "sql_failure_count", name: "db.cockroachdb.sql.failure_count", unit: "{queries}"},
		{promName: "sql_service_latency_p99", name: "db.cockroachdb.sql.latency_p99", unit: "ns"},
		{promName: "ranges", name: "db.cockroachdb.ranges.total", unit: "{ranges}"},
		{promName: "ranges_unavailable", name: "db.cockroachdb.ranges.unavailable", unit: "{ranges}"},
		{promName: "liveness_livenodes", name: "db.cockroachdb.liveness.live_nodes", unit: "{nodes}"},
		{promName: "capacity_used", name: "db.cockroachdb.capacity.used_bytes", unit: "By"},
		{promName: "capacity_available", name: "db.cockroachdb.capacity.available_bytes", unit: "By"},
	}

	for _, m := range mappings {
		if val, ok := promMetrics[m.promName]; ok {
			metrics = append(metrics, Metric{
				Name: m.name, Value: val, Unit: m.unit, Attributes: attrs,
			})
		} else {
			log.Printf("DEBUG: cockroachdb %s metric %s not found", c.name, m.promName)
		}
	}

	return metrics, nil
}

// parseCRDBPrometheus parses Prometheus text exposition format, handling CockroachDB's
// specific metric naming. For gauge/counter metrics without labels, extracts the value.
// For histogram metrics, looks for specific quantile labels.
func parseCRDBPrometheus(body string) map[string]float64 {
	result := make(map[string]float64)
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		var metricName, valueStr string

		if idx := strings.IndexByte(line, '{'); idx >= 0 {
			metricName = line[:idx]

			// Check for p99 quantile label
			if strings.Contains(line, `quantile="0.99"`) {
				// Store as metricname_p99
				endIdx := strings.LastIndexByte(line, '}')
				if endIdx >= 0 && endIdx+1 < len(line) {
					valueStr = strings.TrimSpace(line[endIdx+1:])
					if f, err := strconv.ParseFloat(valueStr, 64); err == nil {
						result[metricName+"_p99"] = f
					}
				}
			}

			// Also store the raw line value for non-histogram metrics
			endIdx := strings.LastIndexByte(line, '}')
			if endIdx >= 0 && endIdx+1 < len(line) {
				valueStr = strings.TrimSpace(line[endIdx+1:])
			}
		} else {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				metricName = parts[0]
				valueStr = parts[1]
			}
		}

		if metricName == "" || valueStr == "" {
			continue
		}

		// Only store first occurrence for simple metrics (no labels)
		if _, exists := result[metricName]; !exists {
			if f, err := strconv.ParseFloat(valueStr, 64); err == nil {
				result[metricName] = f
			}
		}
	}
	return result
}
