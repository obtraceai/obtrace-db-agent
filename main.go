package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"
)

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	ingestURL := envOr("OBTRACE_INGEST_URL", "http://localhost:8080")
	apiKey := os.Getenv("OBTRACE_API_KEY")
	if apiKey == "" {
		log.Fatal("OBTRACE_API_KEY is required")
	}

	appID := envOr("OBTRACE_APP_ID", "db-agent")
	env := envOr("OBTRACE_ENV", "prod")
	tenantID := os.Getenv("OBTRACE_TENANT_ID")
	projectID := os.Getenv("OBTRACE_PROJECT_ID")

	intervalMS, _ := strconv.Atoi(envOr("COLLECT_INTERVAL_MS", "15000"))
	if intervalMS < 1000 {
		intervalMS = 15000
	}
	interval := time.Duration(intervalMS) * time.Millisecond

	connJSON := os.Getenv("DB_CONNECTIONS")
	if connJSON == "" {
		log.Fatal("DB_CONNECTIONS is required (JSON array of connection configs)")
	}

	var conns []ConnectionConfig
	if err := json.Unmarshal([]byte(connJSON), &conns); err != nil {
		log.Fatalf("failed to parse DB_CONNECTIONS: %v", err)
	}
	if len(conns) == 0 {
		log.Fatal("DB_CONNECTIONS must contain at least one connection")
	}

	cloudProvider := envOr("CLOUD_PROVIDER", "")
	cloudRegion := envOr("CLOUD_REGION", "")

	resourceAttrs := map[string]string{
		"service.name":    appID,
		"deployment.environment": env,
	}
	if tenantID != "" {
		resourceAttrs["obtrace.tenant_id"] = tenantID
	}
	if projectID != "" {
		resourceAttrs["obtrace.project_id"] = projectID
	}
	if cloudProvider != "" {
		resourceAttrs["cloud.provider"] = cloudProvider
	}
	if cloudRegion != "" {
		resourceAttrs["cloud.region"] = cloudRegion
	}

	// Build collectors
	var collectors []Collector
	for _, c := range conns {
		col, err := newCollector(c)
		if err != nil {
			log.Printf("WARN: skipping %s (%s): %v", c.Name, c.Type, err)
			continue
		}
		collectors = append(collectors, col)
		log.Printf("INFO: registered collector %s (type=%s)", c.Name, c.Type)
	}

	if len(collectors) == 0 {
		log.Fatal("no collectors could be initialized")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		log.Printf("INFO: received signal %s, shutting down", sig)
		cancel()
	}()

	log.Printf("INFO: starting obtrace-db-agent (interval=%s, targets=%d, ingest=%s)", interval, len(collectors), ingestURL)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Collect immediately on start, then on each tick
	collectAndSend(ctx, collectors, ingestURL, apiKey, resourceAttrs)
	for {
		select {
		case <-ctx.Done():
			log.Println("INFO: shutting down collectors")
			for _, c := range collectors {
				if err := c.Close(); err != nil {
					log.Printf("WARN: error closing collector %s: %v", c.Type(), err)
				}
			}
			log.Println("INFO: shutdown complete")
			return
		case <-ticker.C:
			collectAndSend(ctx, collectors, ingestURL, apiKey, resourceAttrs)
		}
	}
}

func collectAndSend(ctx context.Context, collectors []Collector, ingestURL, apiKey string, resourceAttrs map[string]string) {
	collectCtx, collectCancel := context.WithTimeout(ctx, 10*time.Second)
	defer collectCancel()

	var allMetrics []DBMetric
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, c := range collectors {
		wg.Add(1)
		go func(col Collector) {
			defer wg.Done()
			metrics, err := col.Collect(collectCtx)
			if err != nil {
				log.Printf("WARN: collector %s failed: %v", col.Type(), err)
				return
			}
			mu.Lock()
			allMetrics = append(allMetrics, metrics...)
			mu.Unlock()
		}(c)
	}
	wg.Wait()

	if len(allMetrics) == 0 {
		log.Println("WARN: no metrics collected this cycle")
		return
	}

	if err := sendMetrics(ctx, ingestURL, apiKey, resourceAttrs, allMetrics); err != nil {
		log.Printf("ERROR: failed to send metrics: %v", err)
	} else {
		log.Printf("INFO: sent %d metrics", len(allMetrics))
	}
}

func newCollector(cfg ConnectionConfig) (Collector, error) {
	// Extract host:port from DSN for db.instance attribute
	instance := extractInstance(cfg.DSN, cfg.Type)

	switch cfg.Type {
	case "postgres":
		return NewPostgresCollector(cfg.DSN, cfg.Name, instance)
	case "redis":
		return NewRedisCollector(cfg.DSN, cfg.Name, instance)
	case "mysql":
		return NewMySQLCollector(cfg.DSN, cfg.Name, instance)
	case "mongodb":
		return NewMongoDBCollector(cfg.DSN, cfg.Name, instance)
	case "cassandra":
		return NewCassandraCollector(cfg.DSN, cfg.Name, instance)
	case "elasticsearch":
		return NewElasticsearchCollector(cfg.DSN, cfg.Name, instance)
	case "clickhouse":
		return NewClickHouseCollector(cfg.DSN, cfg.Name, instance)
	case "cockroachdb":
		return NewCockroachDBCollector(cfg.DSN, cfg.Name, instance)
	case "sqlite":
		return NewSQLiteCollector(cfg.DSN, cfg.Name, instance)
	default:
		return nil, fmt.Errorf("unsupported database type: %s", cfg.Type)
	}
}

func extractInstance(dsn, dbType string) string {
	switch dbType {
	case "postgres", "mongodb", "redis":
		u, err := url.Parse(dsn)
		if err != nil {
			return "unknown"
		}
		return u.Host
	case "mysql":
		// format: user:pass@tcp(host:port)/db
		// try to extract host:port
		for i := 0; i < len(dsn); i++ {
			if dsn[i] == '(' {
				end := i + 1
				for end < len(dsn) && dsn[end] != ')' {
					end++
				}
				if end < len(dsn) {
					return dsn[i+1 : end]
				}
			}
		}
		return "unknown"
	case "cassandra", "elasticsearch", "clickhouse", "cockroachdb":
		u, err := url.Parse(dsn)
		if err != nil {
			return "unknown"
		}
		return u.Host
	case "sqlite":
		return dsn
	default:
		return "unknown"
	}
}
