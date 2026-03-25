package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
)

type SidekiqCollector struct {
	client        *redis.Client
	name          string
	instance      string
	namespace     string
	cachedQueues  []string
	lastDiscovery time.Time
}

func NewSidekiqCollector(dsn, name, instance string) (*SidekiqCollector, error) {
	opts, err := redis.ParseURL(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse redis DSN: %w", err)
	}
	opts.PoolSize = 2

	client := redis.NewClient(opts)
	if err := client.Ping(context.Background()).Err(); err != nil {
		client.Close()
		return nil, fmt.Errorf("ping redis for Sidekiq: %w", err)
	}

	return &SidekiqCollector{
		client:    client,
		name:      name,
		instance:  instance,
		namespace: "",
	}, nil
}

func (s *SidekiqCollector) Type() string { return "sidekiq" }

func (s *SidekiqCollector) Close() error {
	return s.client.Close()
}

func (s *SidekiqCollector) baseAttrs() map[string]string {
	return map[string]string{
		"messaging.system":   "sidekiq",
		"messaging.instance": s.instance,
		"messaging.name":     s.name,
	}
}

func (s *SidekiqCollector) key(k string) string {
	if s.namespace != "" {
		return s.namespace + ":" + k
	}
	return k
}

func (s *SidekiqCollector) Collect(ctx context.Context) ([]Metric, error) {
	if time.Since(s.lastDiscovery) > 5*time.Minute || len(s.cachedQueues) == 0 {
		queues, err := s.client.SMembers(ctx, s.key("queues")).Result()
		if err == nil && len(queues) > 0 {
			s.cachedQueues = queues
			s.lastDiscovery = time.Now()
		} else if len(s.cachedQueues) == 0 {
			return nil, fmt.Errorf("no Sidekiq queues found: %w", err)
		}
	}

	var metrics []Metric
	attrs := s.baseAttrs()

	processed, _ := s.client.Get(ctx, s.key("stat:processed")).Int64()
	failed, _ := s.client.Get(ctx, s.key("stat:failed")).Int64()
	metrics = append(metrics,
		Metric{Name: "messaging.sidekiq.processed", Value: float64(processed), Unit: "{jobs}", Attributes: attrs},
		Metric{Name: "messaging.sidekiq.failed", Value: float64(failed), Unit: "{jobs}", Attributes: attrs},
		Metric{Name: "messaging.sidekiq.queues.count", Value: float64(len(s.cachedQueues)), Unit: "{queues}", Attributes: attrs},
	)

	scheduleSize, _ := s.client.ZCard(ctx, s.key("schedule")).Result()
	retrySize, _ := s.client.ZCard(ctx, s.key("retry")).Result()
	deadSize, _ := s.client.ZCard(ctx, s.key("dead")).Result()
	metrics = append(metrics,
		Metric{Name: "messaging.sidekiq.scheduled", Value: float64(scheduleSize), Unit: "{jobs}", Attributes: attrs},
		Metric{Name: "messaging.sidekiq.retries", Value: float64(retrySize), Unit: "{jobs}", Attributes: attrs},
		Metric{Name: "messaging.sidekiq.dead", Value: float64(deadSize), Unit: "{jobs}", Attributes: attrs},
	)

	processes, _ := s.client.SMembers(ctx, s.key("processes")).Result()
	totalBusy := int64(0)
	totalConcurrency := int64(0)
	for _, procKey := range processes {
		data, err := s.client.HGetAll(ctx, s.key(procKey)).Result()
		if err != nil {
			continue
		}
		if info, ok := data["info"]; ok {
			var pi struct {
				Concurrency int64 `json:"concurrency"`
				Busy        int64 `json:"busy"`
			}
			if json.Unmarshal([]byte(info), &pi) == nil {
				totalBusy += pi.Busy
				totalConcurrency += pi.Concurrency
			}
		}
	}
	metrics = append(metrics,
		Metric{Name: "messaging.sidekiq.processes", Value: float64(len(processes)), Unit: "{processes}", Attributes: attrs},
		Metric{Name: "messaging.sidekiq.busy", Value: float64(totalBusy), Unit: "{workers}", Attributes: attrs},
		Metric{Name: "messaging.sidekiq.concurrency", Value: float64(totalConcurrency), Unit: "{workers}", Attributes: attrs},
	)

	totalEnqueued := int64(0)
	limit := len(s.cachedQueues)
	if limit > 100 {
		log.Printf("WARN: Sidekiq queues truncated to 100 (total: %d)", len(s.cachedQueues))
		limit = 100
	}
	for _, q := range s.cachedQueues[:limit] {
		size, _ := s.client.LLen(ctx, s.key("queue:"+q)).Result()
		totalEnqueued += size

		qAttrs := s.baseAttrs()
		qAttrs["messaging.destination.name"] = q
		metrics = append(metrics, Metric{
			Name: "messaging.sidekiq.queue.size", Value: float64(size), Unit: "{jobs}", Attributes: qAttrs,
		})
	}
	metrics = append(metrics, Metric{
		Name: "messaging.sidekiq.enqueued", Value: float64(totalEnqueued), Unit: "{jobs}", Attributes: attrs,
	})

	return metrics, nil
}
