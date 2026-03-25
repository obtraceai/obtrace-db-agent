package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// CassandraCollector collects metrics from a Cassandra node via Jolokia JMX
// or a Prometheus-format metrics endpoint.
type CassandraCollector struct {
	endpoint string
	name     string
	instance string
	mode     string // "jolokia" or "prometheus"
	client   *http.Client
}

func NewCassandraCollector(dsn, name, instance string) (*CassandraCollector, error) {
	mode := "jolokia"
	if strings.Contains(dsn, "/metrics") || strings.Contains(dsn, ":9103") {
		mode = "prometheus"
	}

	client := &http.Client{Timeout: 10 * time.Second}

	// Verify connectivity
	resp, err := client.Get(dsn)
	if err != nil {
		return nil, fmt.Errorf("connect to cassandra metrics endpoint: %w", err)
	}
	resp.Body.Close()

	return &CassandraCollector{
		endpoint: dsn,
		name:     name,
		instance: instance,
		mode:     mode,
		client:   client,
	}, nil
}

func (c *CassandraCollector) Type() string { return "cassandra" }

func (c *CassandraCollector) Close() error {
	c.client.CloseIdleConnections()
	return nil
}

func (c *CassandraCollector) baseAttrs() map[string]string {
	return map[string]string{
		"db.system":   "cassandra",
		"db.name":     c.name,
		"db.instance": c.instance,
	}
}

func (c *CassandraCollector) Collect(ctx context.Context) ([]Metric, error) {
	if c.mode == "prometheus" {
		return c.collectPrometheus(ctx)
	}
	return c.collectJolokia(ctx)
}

// collectJolokia queries the Jolokia JMX HTTP endpoint for Cassandra metrics.
func (c *CassandraCollector) collectJolokia(ctx context.Context) ([]Metric, error) {
	attrs := c.baseAttrs()
	var metrics []Metric

	// Read latency
	val, err := c.queryJolokiaMBean(ctx,
		"org.apache.cassandra.metrics:type=ClientRequest,scope=Read,name=Latency",
		"Mean")
	if err != nil {
		log.Printf("WARN: cassandra %s read latency: %v", c.name, err)
	} else {
		// Jolokia returns latency in microseconds; convert to ms
		metrics = append(metrics, Metric{
			Name: "db.cassandra.read_latency_ms", Value: val / 1000.0, Unit: "ms", Attributes: attrs,
		})
	}

	// Write latency
	val, err = c.queryJolokiaMBean(ctx,
		"org.apache.cassandra.metrics:type=ClientRequest,scope=Write,name=Latency",
		"Mean")
	if err != nil {
		log.Printf("WARN: cassandra %s write latency: %v", c.name, err)
	} else {
		metrics = append(metrics, Metric{
			Name: "db.cassandra.write_latency_ms", Value: val / 1000.0, Unit: "ms", Attributes: attrs,
		})
	}

	// Pending compactions
	val, err = c.queryJolokiaMBean(ctx,
		"org.apache.cassandra.metrics:type=Compaction,name=PendingTasks",
		"Value")
	if err != nil {
		log.Printf("WARN: cassandra %s pending compactions: %v", c.name, err)
	} else {
		metrics = append(metrics, Metric{
			Name: "db.cassandra.compactions_pending", Value: val, Unit: "{compactions}", Attributes: attrs,
		})
	}

	// Tombstones scanned
	val, err = c.queryJolokiaMBean(ctx,
		"org.apache.cassandra.metrics:type=Table,name=TombstoneScannedHistogram",
		"Mean")
	if err != nil {
		log.Printf("WARN: cassandra %s tombstones: %v", c.name, err)
	} else {
		metrics = append(metrics, Metric{
			Name: "db.cassandra.tombstones_scanned", Value: val, Unit: "{tombstones}", Attributes: attrs,
		})
	}

	// Connected native clients
	val, err = c.queryJolokiaMBean(ctx,
		"org.apache.cassandra.metrics:type=Client,name=connectedNativeClients",
		"Value")
	if err != nil {
		log.Printf("WARN: cassandra %s connections: %v", c.name, err)
	} else {
		metrics = append(metrics, Metric{
			Name: "db.cassandra.connections.active", Value: val, Unit: "{connections}", Attributes: attrs,
		})
	}

	// Storage load (total bytes)
	val, err = c.queryJolokiaMBean(ctx,
		"org.apache.cassandra.db:type=StorageService",
		"Load")
	if err != nil {
		log.Printf("WARN: cassandra %s storage load: %v", c.name, err)
	} else {
		metrics = append(metrics, Metric{
			Name: "db.cassandra.storage.total_bytes", Value: val, Unit: "By", Attributes: attrs,
		})
	}

	// Live disk space used
	val, err = c.queryJolokiaMBean(ctx,
		"org.apache.cassandra.metrics:type=Table,name=LiveDiskSpaceUsed",
		"Count")
	if err != nil {
		log.Printf("WARN: cassandra %s live disk space: %v", c.name, err)
	} else {
		metrics = append(metrics, Metric{
			Name: "db.cassandra.storage.live_bytes", Value: val, Unit: "By", Attributes: attrs,
		})
	}

	return metrics, nil
}

// queryJolokiaMBean queries a single MBean attribute from the Jolokia endpoint.
func (c *CassandraCollector) queryJolokiaMBean(ctx context.Context, mbean, attribute string) (float64, error) {
	url := fmt.Sprintf("%s/read/%s/%s", strings.TrimRight(c.endpoint, "/"), mbean, attribute)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, fmt.Errorf("create request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("GET %s returned %d", url, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return 0, fmt.Errorf("read response: %w", err)
	}

	var result struct {
		Value interface{} `json:"value"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return 0, fmt.Errorf("parse JSON: %w", err)
	}

	switch v := result.Value.(type) {
	case float64:
		return v, nil
	case json.Number:
		return v.Float64()
	default:
		return 0, fmt.Errorf("unexpected value type %T for %s/%s", result.Value, mbean, attribute)
	}
}

// collectPrometheus scrapes the Prometheus-format metrics endpoint and extracts
// Cassandra-specific metrics.
func (c *CassandraCollector) collectPrometheus(ctx context.Context) ([]Metric, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", c.endpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s returned %d", c.endpoint, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	promMetrics := parsePrometheusText(string(body))
	attrs := c.baseAttrs()
	var metrics []Metric

	// Map Prometheus metric names to our metric names
	mappings := map[string]struct {
		name string
		unit string
		conv float64 // multiplier (e.g., us->ms = 0.001)
	}{
		"cassandra_clientrequest_latency_mean":       {name: "db.cassandra.read_latency_ms", unit: "ms", conv: 0.001},
		"cassandra_clientrequest_read_latency_mean":  {name: "db.cassandra.read_latency_ms", unit: "ms", conv: 0.001},
		"cassandra_clientrequest_write_latency_mean": {name: "db.cassandra.write_latency_ms", unit: "ms", conv: 0.001},
		"cassandra_compaction_pendingtasks":          {name: "db.cassandra.compactions_pending", unit: "{compactions}", conv: 1},
		"cassandra_table_tombstonescannedhistogram_mean": {name: "db.cassandra.tombstones_scanned", unit: "{tombstones}", conv: 1},
		"cassandra_client_connectednativeclients":        {name: "db.cassandra.connections.active", unit: "{connections}", conv: 1},
		"cassandra_storage_load":                         {name: "db.cassandra.storage.total_bytes", unit: "By", conv: 1},
		"cassandra_table_livediskspaceused_count":        {name: "db.cassandra.storage.live_bytes", unit: "By", conv: 1},
	}

	for promName, mapping := range mappings {
		if val, ok := promMetrics[promName]; ok {
			metrics = append(metrics, Metric{
				Name:       mapping.name,
				Value:      val * mapping.conv,
				Unit:       mapping.unit,
				Attributes: attrs,
			})
		}
	}

	return metrics, nil
}

// parsePrometheusText parses Prometheus text exposition format into metric name -> value.
// For simplicity, it takes the first occurrence of each metric name and ignores labels.
func parsePrometheusText(body string) map[string]float64 {
	result := make(map[string]float64)
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Strip labels: "metric_name{label=\"val\"} 123.4" -> "metric_name 123.4"
		metricName := line
		valueStr := ""

		if idx := strings.IndexByte(line, '{'); idx >= 0 {
			metricName = line[:idx]
			// Find closing brace, then the value
			if endIdx := strings.IndexByte(line[idx:], '}'); endIdx >= 0 {
				valueStr = strings.TrimSpace(line[idx+endIdx+1:])
			}
		} else {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				metricName = parts[0]
				valueStr = parts[1]
			}
		}

		if valueStr == "" {
			continue
		}

		if _, exists := result[metricName]; exists {
			continue // take first occurrence
		}

		if f, err := strconv.ParseFloat(valueStr, 64); err == nil {
			result[metricName] = f
		}
	}
	return result
}
