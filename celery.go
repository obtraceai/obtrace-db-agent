package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

type CeleryCollector struct {
	client        *redis.Client
	name          string
	instance      string
	cachedQueues  []string
	lastDiscovery time.Time
}

func NewCeleryCollector(dsn, name, instance string) (*CeleryCollector, error) {
	opts, err := redis.ParseURL(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse redis DSN: %w", err)
	}
	opts.PoolSize = 2

	client := redis.NewClient(opts)
	if err := client.Ping(context.Background()).Err(); err != nil {
		client.Close()
		return nil, fmt.Errorf("ping redis for Celery: %w", err)
	}

	return &CeleryCollector{
		client:   client,
		name:     name,
		instance: instance,
	}, nil
}

func (c *CeleryCollector) Type() string { return "celery" }

func (c *CeleryCollector) Close() error {
	return c.client.Close()
}

func (c *CeleryCollector) baseAttrs() map[string]string {
	return map[string]string{
		"messaging.system":   "celery",
		"messaging.instance": c.instance,
		"messaging.name":     c.name,
	}
}

func (c *CeleryCollector) Collect(ctx context.Context) ([]Metric, error) {
	if time.Since(c.lastDiscovery) > 5*time.Minute || len(c.cachedQueues) == 0 {
		queues, err := c.discoverQueues(ctx)
		if err == nil && len(queues) > 0 {
			c.cachedQueues = queues
			c.lastDiscovery = time.Now()
		} else if len(c.cachedQueues) == 0 {
			c.cachedQueues = []string{"celery"}
		}
	}

	var metrics []Metric
	attrs := c.baseAttrs()

	totalPending := int64(0)
	totalReserved := int64(0)

	limit := len(c.cachedQueues)
	if limit > 100 {
		log.Printf("WARN: Celery queues truncated to 100 (total: %d)", len(c.cachedQueues))
		limit = 100
	}

	for _, q := range c.cachedQueues[:limit] {
		pending, _ := c.client.LLen(ctx, q).Result()
		reserved, _ := c.client.ZCard(ctx, q+"\x06\x16\x06\x16").Result()
		if reserved == 0 {
			reserved, _ = c.client.LLen(ctx, q+":reserved").Result()
		}

		totalPending += pending
		totalReserved += reserved

		qAttrs := c.baseAttrs()
		qAttrs["messaging.destination.name"] = q
		metrics = append(metrics,
			Metric{Name: "messaging.celery.queue.pending", Value: float64(pending), Unit: "{tasks}", Attributes: qAttrs},
			Metric{Name: "messaging.celery.queue.reserved", Value: float64(reserved), Unit: "{tasks}", Attributes: qAttrs},
		)
	}

	activeWorkers := int64(0)
	scanCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var cursor uint64
	for {
		keys, next, err := c.client.Scan(scanCtx, cursor, "celery-task-meta-*", 200).Result()
		if err != nil {
			break
		}
		activeWorkers += int64(len(keys))
		cursor = next
		if cursor == 0 || activeWorkers >= 10000 {
			break
		}
	}

	metrics = append(metrics,
		Metric{Name: "messaging.celery.queues.count", Value: float64(len(c.cachedQueues)), Unit: "{queues}", Attributes: attrs},
		Metric{Name: "messaging.celery.tasks.pending", Value: float64(totalPending), Unit: "{tasks}", Attributes: attrs},
		Metric{Name: "messaging.celery.tasks.reserved", Value: float64(totalReserved), Unit: "{tasks}", Attributes: attrs},
		Metric{Name: "messaging.celery.results.count", Value: float64(activeWorkers), Unit: "{results}", Attributes: attrs},
	)

	return metrics, nil
}

func (c *CeleryCollector) discoverQueues(ctx context.Context) ([]string, error) {
	scanCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	known := map[string]bool{"celery": true}

	var cursor uint64
	for {
		keys, next, err := c.client.Scan(scanCtx, cursor, "_kombu.binding.*", 200).Result()
		if err != nil {
			break
		}
		for _, k := range keys {
			qName := strings.TrimPrefix(k, "_kombu.binding.")
			if qName != "" {
				known[qName] = true
			}
		}
		cursor = next
		if cursor == 0 || len(known) >= 200 {
			break
		}
	}

	names := make([]string, 0, len(known))
	for n := range known {
		names = append(names, n)
	}
	return names, nil
}
