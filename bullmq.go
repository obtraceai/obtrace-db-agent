package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

type BullMQCollector struct {
	client        *redis.Client
	name          string
	instance      string
	prefix        string
	cachedQueues  []string
	lastDiscovery time.Time
	discoveryTTL  time.Duration
}

func NewBullMQCollector(dsn, name, instance string) (*BullMQCollector, error) {
	opts, err := redis.ParseURL(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse redis DSN: %w", err)
	}
	opts.PoolSize = 2

	client := redis.NewClient(opts)
	if err := client.Ping(context.Background()).Err(); err != nil {
		client.Close()
		return nil, fmt.Errorf("ping redis for BullMQ: %w", err)
	}

	return &BullMQCollector{
		client:       client,
		name:         name,
		instance:     instance,
		prefix:       "bull",
		discoveryTTL: 5 * time.Minute,
	}, nil
}

func (b *BullMQCollector) Type() string { return "bullmq" }

func (b *BullMQCollector) Close() error {
	return b.client.Close()
}

func (b *BullMQCollector) baseAttrs() map[string]string {
	return map[string]string{
		"messaging.system":   "bullmq",
		"messaging.instance": b.instance,
		"messaging.name":     b.name,
	}
}

func (b *BullMQCollector) Collect(ctx context.Context) ([]Metric, error) {
	if time.Since(b.lastDiscovery) > b.discoveryTTL || len(b.cachedQueues) == 0 {
		queues, err := b.discoverQueues(ctx)
		if err != nil {
			if len(b.cachedQueues) == 0 {
				return nil, fmt.Errorf("discover BullMQ queues: %w", err)
			}
			log.Printf("WARN: BullMQ rediscovery failed, using cached %d queues: %v", len(b.cachedQueues), err)
		} else {
			b.cachedQueues = queues
			b.lastDiscovery = time.Now()
		}
	}

	var metrics []Metric
	totalWaiting := int64(0)
	totalActive := int64(0)
	totalDelayed := int64(0)
	totalFailed := int64(0)
	totalCompleted := int64(0)
	totalPaused := int64(0)

	limit := len(b.cachedQueues)
	if limit > 200 {
		log.Printf("WARN: BullMQ queues truncated to 200 (total discovered: %d)", len(b.cachedQueues))
		limit = 200
	}

	for _, qName := range b.cachedQueues[:limit] {
		qAttrs := b.baseAttrs()
		qAttrs["messaging.destination.name"] = qName

		waiting := b.zcard(ctx, b.key(qName, "wait")) + b.llen(ctx, b.key(qName, "wait"))
		active := b.zcard(ctx, b.key(qName, "active")) + b.llen(ctx, b.key(qName, "active"))
		delayed := b.zcard(ctx, b.key(qName, "delayed"))
		failed := b.zcard(ctx, b.key(qName, "failed"))
		completed := b.zcard(ctx, b.key(qName, "completed"))
		paused := b.zcard(ctx, b.key(qName, "paused")) + b.llen(ctx, b.key(qName, "paused"))

		totalWaiting += waiting
		totalActive += active
		totalDelayed += delayed
		totalFailed += failed
		totalCompleted += completed
		totalPaused += paused

		metrics = append(metrics,
			Metric{Name: "messaging.bullmq.queue.waiting", Value: float64(waiting), Unit: "{jobs}", Attributes: qAttrs},
			Metric{Name: "messaging.bullmq.queue.active", Value: float64(active), Unit: "{jobs}", Attributes: qAttrs},
			Metric{Name: "messaging.bullmq.queue.delayed", Value: float64(delayed), Unit: "{jobs}", Attributes: qAttrs},
			Metric{Name: "messaging.bullmq.queue.failed", Value: float64(failed), Unit: "{jobs}", Attributes: qAttrs},
			Metric{Name: "messaging.bullmq.queue.completed", Value: float64(completed), Unit: "{jobs}", Attributes: qAttrs},
			Metric{Name: "messaging.bullmq.queue.paused", Value: float64(paused), Unit: "{jobs}", Attributes: qAttrs},
		)
	}

	attrs := b.baseAttrs()
	metrics = append(metrics,
		Metric{Name: "messaging.bullmq.queues.count", Value: float64(len(b.cachedQueues)), Unit: "{queues}", Attributes: attrs},
		Metric{Name: "messaging.bullmq.jobs.waiting", Value: float64(totalWaiting), Unit: "{jobs}", Attributes: attrs},
		Metric{Name: "messaging.bullmq.jobs.active", Value: float64(totalActive), Unit: "{jobs}", Attributes: attrs},
		Metric{Name: "messaging.bullmq.jobs.delayed", Value: float64(totalDelayed), Unit: "{jobs}", Attributes: attrs},
		Metric{Name: "messaging.bullmq.jobs.failed", Value: float64(totalFailed), Unit: "{jobs}", Attributes: attrs},
		Metric{Name: "messaging.bullmq.jobs.completed", Value: float64(totalCompleted), Unit: "{jobs}", Attributes: attrs},
	)

	return metrics, nil
}

func (b *BullMQCollector) discoverQueues(ctx context.Context) ([]string, error) {
	scanCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	seen := map[string]bool{}
	var cursor uint64
	for {
		keys, next, err := b.client.Scan(scanCtx, cursor, b.prefix+":*:meta", 200).Result()
		if err != nil {
			return nil, err
		}
		for _, k := range keys {
			parts := strings.Split(k, ":")
			if len(parts) >= 3 {
				seen[parts[1]] = true
			}
		}
		cursor = next
		if cursor == 0 || len(seen) >= 500 {
			break
		}
	}

	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	return names, nil
}

func (b *BullMQCollector) key(queue, suffix string) string {
	return b.prefix + ":" + queue + ":" + suffix
}

func (b *BullMQCollector) zcard(ctx context.Context, key string) int64 {
	n, _ := b.client.ZCard(ctx, key).Result()
	return n
}

func (b *BullMQCollector) llen(ctx context.Context, key string) int64 {
	n, _ := b.client.LLen(ctx, key).Result()
	return n
}
