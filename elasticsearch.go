package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// ElasticsearchCollector collects metrics from an Elasticsearch cluster
// using the REST API.
type ElasticsearchCollector struct {
	endpoint string
	name     string
	instance string
	client   *http.Client
}

func NewElasticsearchCollector(dsn, name, instance string) (*ElasticsearchCollector, error) {
	endpoint := strings.TrimRight(dsn, "/")
	client := &http.Client{Timeout: 10 * time.Second}

	// Verify connectivity with a cluster health check
	resp, err := client.Get(endpoint + "/_cluster/health")
	if err != nil {
		return nil, fmt.Errorf("connect to elasticsearch: %w", err)
	}
	resp.Body.Close()

	return &ElasticsearchCollector{
		endpoint: endpoint,
		name:     name,
		instance: instance,
		client:   client,
	}, nil
}

func (e *ElasticsearchCollector) Type() string { return "elasticsearch" }

func (e *ElasticsearchCollector) Close() error {
	e.client.CloseIdleConnections()
	return nil
}

func (e *ElasticsearchCollector) baseAttrs() map[string]string {
	return map[string]string{
		"db.system":   "elasticsearch",
		"db.name":     e.name,
		"db.instance": e.instance,
	}
}

func (e *ElasticsearchCollector) Collect(ctx context.Context) ([]Metric, error) {
	var metrics []Metric

	m, err := e.collectClusterHealth(ctx)
	if err != nil {
		log.Printf("WARN: elasticsearch %s _cluster/health: %v", e.name, err)
	} else {
		metrics = append(metrics, m...)
	}

	m, err = e.collectNodeStats(ctx)
	if err != nil {
		log.Printf("WARN: elasticsearch %s _nodes/stats: %v", e.name, err)
	} else {
		metrics = append(metrics, m...)
	}

	return metrics, nil
}

func (e *ElasticsearchCollector) doGet(ctx context.Context, path string) ([]byte, error) {
	url := e.endpoint + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("GET %s returned %d: %s", url, resp.StatusCode, string(body))
	}

	return io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
}

func (e *ElasticsearchCollector) collectClusterHealth(ctx context.Context) ([]Metric, error) {
	body, err := e.doGet(ctx, "/_cluster/health")
	if err != nil {
		return nil, err
	}

	var health struct {
		Status             string  `json:"status"`
		NumberOfNodes      float64 `json:"number_of_nodes"`
		ActiveShards       float64 `json:"active_shards"`
		UnassignedShards   float64 `json:"unassigned_shards"`
	}
	if err := json.Unmarshal(body, &health); err != nil {
		return nil, fmt.Errorf("parse cluster health: %w", err)
	}

	attrs := e.baseAttrs()

	// Map status to numeric value
	var statusVal float64
	switch health.Status {
	case "green":
		statusVal = 0
	case "yellow":
		statusVal = 1
	case "red":
		statusVal = 2
	default:
		statusVal = -1
	}

	return []Metric{
		{Name: "db.elasticsearch.cluster.status", Value: statusVal, Unit: "1", Attributes: attrs},
		{Name: "db.elasticsearch.cluster.nodes", Value: health.NumberOfNodes, Unit: "{nodes}", Attributes: attrs},
		{Name: "db.elasticsearch.cluster.shards.active", Value: health.ActiveShards, Unit: "{shards}", Attributes: attrs},
		{Name: "db.elasticsearch.cluster.shards.unassigned", Value: health.UnassignedShards, Unit: "{shards}", Attributes: attrs},
	}, nil
}

func (e *ElasticsearchCollector) collectNodeStats(ctx context.Context) ([]Metric, error) {
	body, err := e.doGet(ctx, "/_nodes/stats")
	if err != nil {
		return nil, err
	}

	var stats struct {
		Nodes map[string]struct {
			JVM struct {
				Mem struct {
					HeapUsedInBytes float64 `json:"heap_used_in_bytes"`
					HeapMaxInBytes  float64 `json:"heap_max_in_bytes"`
				} `json:"mem"`
			} `json:"jvm"`
			Indices struct {
				Docs struct {
					Count float64 `json:"count"`
				} `json:"docs"`
				Store struct {
					SizeInBytes float64 `json:"size_in_bytes"`
				} `json:"store"`
				Indexing struct {
					IndexTotal float64 `json:"index_total"`
				} `json:"indexing"`
				Search struct {
					QueryTotal        float64 `json:"query_total"`
					QueryTimeInMillis float64 `json:"query_time_in_millis"`
				} `json:"search"`
			} `json:"indices"`
			ThreadPool map[string]struct {
				Rejected float64 `json:"rejected"`
			} `json:"thread_pool"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(body, &stats); err != nil {
		return nil, fmt.Errorf("parse node stats: %w", err)
	}

	// Aggregate across all nodes
	var totalHeapUsed, totalHeapMax float64
	var totalDocsCount, totalStoreSize float64
	var totalIndexingRate, totalSearchRate, totalSearchTime float64
	var totalSearchRejected, totalWriteRejected float64

	for _, node := range stats.Nodes {
		totalHeapUsed += node.JVM.Mem.HeapUsedInBytes
		totalHeapMax += node.JVM.Mem.HeapMaxInBytes
		totalDocsCount += node.Indices.Docs.Count
		totalStoreSize += node.Indices.Store.SizeInBytes
		totalIndexingRate += node.Indices.Indexing.IndexTotal
		totalSearchRate += node.Indices.Search.QueryTotal
		totalSearchTime += node.Indices.Search.QueryTimeInMillis

		if sp, ok := node.ThreadPool["search"]; ok {
			totalSearchRejected += sp.Rejected
		}
		if wp, ok := node.ThreadPool["write"]; ok {
			totalWriteRejected += wp.Rejected
		}
	}

	attrs := e.baseAttrs()
	metrics := []Metric{
		{Name: "db.elasticsearch.jvm.heap_used_bytes", Value: totalHeapUsed, Unit: "By", Attributes: attrs},
		{Name: "db.elasticsearch.jvm.heap_max_bytes", Value: totalHeapMax, Unit: "By", Attributes: attrs},
		{Name: "db.elasticsearch.indices.docs_count", Value: totalDocsCount, Unit: "{documents}", Attributes: attrs},
		{Name: "db.elasticsearch.indices.store_size_bytes", Value: totalStoreSize, Unit: "By", Attributes: attrs},
		{Name: "db.elasticsearch.indices.indexing_rate", Value: totalIndexingRate, Unit: "{operations}", Attributes: attrs},
		{Name: "db.elasticsearch.indices.search_rate", Value: totalSearchRate, Unit: "{operations}", Attributes: attrs},
		{Name: "db.elasticsearch.thread_pool.search_rejected", Value: totalSearchRejected, Unit: "{requests}", Attributes: attrs},
		{Name: "db.elasticsearch.thread_pool.write_rejected", Value: totalWriteRejected, Unit: "{requests}", Attributes: attrs},
	}

	// Compute average search latency
	if totalSearchRate > 0 {
		avgLatency := totalSearchTime / totalSearchRate
		metrics = append(metrics, Metric{
			Name: "db.elasticsearch.indices.search_latency_ms", Value: avgLatency, Unit: "ms", Attributes: attrs,
		})
	}

	return metrics, nil
}
