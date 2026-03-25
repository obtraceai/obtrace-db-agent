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
	"strings"
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

	appID := envOr("OBTRACE_APP_ID", "infra-agent")
	env := envOr("OBTRACE_ENV", "prod")
	tenantID := os.Getenv("OBTRACE_TENANT_ID")
	projectID := os.Getenv("OBTRACE_PROJECT_ID")
	healthPort := envOr("HEALTH_PORT", "9090")

	intervalMS, _ := strconv.Atoi(envOr("COLLECT_INTERVAL_MS", "15000"))
	if intervalMS < 1000 {
		intervalMS = 15000
	}
	interval := time.Duration(intervalMS) * time.Millisecond

	connJSON := os.Getenv("INFRA_CONNECTIONS")
	if connJSON == "" {
		connJSON = os.Getenv("DB_CONNECTIONS")
	}
	if connJSON == "" {
		log.Fatal("INFRA_CONNECTIONS is required (JSON array of connection configs)")
	}

	var conns []ConnectionConfig
	if err := json.Unmarshal([]byte(connJSON), &conns); err != nil {
		log.Fatalf("failed to parse INFRA_CONNECTIONS: %v", err)
	}
	if len(conns) == 0 {
		log.Fatal("INFRA_CONNECTIONS must contain at least one connection")
	}

	hostMetrics := envOr("HOST_METRICS", "")
	if hostMetrics == "true" || hostMetrics == "1" {
		conns = append(conns, ConnectionConfig{
			Type: "host",
			Name: envOr("HOST_METRICS_NAME", "host"),
		})
	}

	cloudProvider := envOr("CLOUD_PROVIDER", "")
	cloudRegion := envOr("CLOUD_REGION", "")

	resourceAttrs := map[string]string{
		"service.name":           appID,
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

	orchestrator := NewResilientOrchestrator(collectors)

	health := NewHealthServer(orchestrator, healthPort)
	health.Start()
	log.Printf("INFO: health endpoint on :%s/health", healthPort)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		log.Printf("INFO: received signal %s, shutting down", sig)
		cancel()
	}()

	log.Printf("INFO: starting obtrace-infra-agent (interval=%s, targets=%d, ingest=%s)", interval, len(collectors), ingestURL)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	collectAndSend(ctx, orchestrator, health, ingestURL, apiKey, resourceAttrs)
	for {
		select {
		case <-ctx.Done():
			log.Println("INFO: shutting down collectors")
			orchestrator.Close()
			log.Println("INFO: shutdown complete")
			return
		case <-ticker.C:
			collectAndSend(ctx, orchestrator, health, ingestURL, apiKey, resourceAttrs)
		}
	}
}

func collectAndSend(ctx context.Context, orch *ResilientOrchestrator, health *HealthServer, ingestURL, apiKey string, resourceAttrs map[string]string) {
	allMetrics := orch.CollectAll(ctx)

	allMetrics = append(allMetrics, Metric{
		Name:  "agent.up",
		Value: 1,
		Unit:  "1",
		Attributes: map[string]string{
			"agent.type":    "infra-agent",
			"agent.version": "2.0.0",
		},
	})

	statuses := orch.Status()
	for _, s := range statuses {
		val := 1.0
		if s.Status == "open" {
			val = 0
		} else if s.Status == "degraded" {
			val = 0.5
		}
		allMetrics = append(allMetrics, Metric{
			Name:  "agent.collector.health",
			Value: val,
			Unit:  "1",
			Attributes: map[string]string{
				"agent.collector.type":   s.Type,
				"agent.collector.status": s.Status,
			},
		})
	}

	err := sendMetricsWithRetry(ctx, ingestURL, apiKey, resourceAttrs, allMetrics, 3)
	health.RecordSend(err)

	if err != nil {
		log.Printf("ERROR: failed to send %d metrics after retries: %v", len(allMetrics), err)
	} else {
		log.Printf("INFO: sent %d metrics", len(allMetrics))
	}
}

func newCollector(cfg ConnectionConfig) (Collector, error) {
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
	case "kafka":
		return NewKafkaCollector(cfg.DSN, cfg.Name, instance)
	case "rabbitmq":
		return NewRabbitMQCollector(cfg.DSN, cfg.Name, instance)
	case "nats":
		return NewNATSCollector(cfg.DSN, cfg.Name, instance)
	case "bullmq":
		return NewBullMQCollector(cfg.DSN, cfg.Name, instance)
	case "sidekiq":
		return NewSidekiqCollector(cfg.DSN, cfg.Name, instance)
	case "celery":
		return NewCeleryCollector(cfg.DSN, cfg.Name, instance)
	case "ibmmq":
		return NewIBMMQCollector(cfg.DSN, cfg.Name, instance)
	case "activemq", "artemis":
		return NewActiveMQCollector(cfg.DSN, cfg.Name, instance)
	case "sqs":
		return NewSQSCollector(cfg.DSN, cfg.Name, instance)
	case "beanstalkd":
		return NewBeanstalkdCollector(cfg.DSN, cfg.Name, instance)
	case "host":
		return NewHostCollector(cfg.Name)
	default:
		return nil, fmt.Errorf("unsupported type: %s", cfg.Type)
	}
}

func extractInstance(dsn, dbType string) string {
	switch dbType {
	case "postgres", "mongodb", "redis", "bullmq", "sidekiq", "celery":
		u, err := url.Parse(dsn)
		if err != nil {
			return "unknown"
		}
		return u.Host
	case "kafka":
		return strings.Split(dsn, ",")[0]
	case "beanstalkd":
		return dsn
	case "sqs":
		if strings.HasPrefix(dsn, "http") {
			u, err := url.Parse(dsn)
			if err != nil {
				return dsn
			}
			return u.Host
		}
		return dsn
	case "mysql":
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
	case "cassandra", "elasticsearch", "clickhouse", "cockroachdb", "rabbitmq", "nats", "ibmmq", "activemq", "artemis":
		u, err := url.Parse(dsn)
		if err != nil {
			return "unknown"
		}
		return u.Host
	case "sqlite":
		return dsn
	case "host":
		return "localhost"
	default:
		return "unknown"
	}
}
