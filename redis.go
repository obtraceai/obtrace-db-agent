package main

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"
)

// RedisCollector collects metrics from a Redis instance.
type RedisCollector struct {
	client   *redis.Client
	name     string
	instance string
}

func NewRedisCollector(dsn, name, instance string) (*RedisCollector, error) {
	opts, err := redis.ParseURL(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse redis DSN: %w", err)
	}
	opts.PoolSize = 2

	client := redis.NewClient(opts)

	// Verify connectivity
	if err := client.Ping(context.Background()).Err(); err != nil {
		client.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}

	return &RedisCollector{
		client:   client,
		name:     name,
		instance: instance,
	}, nil
}

func (r *RedisCollector) Type() string { return "redis" }

func (r *RedisCollector) Close() error {
	return r.client.Close()
}

func (r *RedisCollector) baseAttrs() map[string]string {
	return map[string]string{
		"db.system":   "redis",
		"db.name":     r.name,
		"db.instance": r.instance,
	}
}

func (r *RedisCollector) Collect(ctx context.Context) ([]DBMetric, error) {
	var metrics []DBMetric

	// Collect server version for instance metadata
	serverInfo, verErr := r.client.Info(ctx, "server").Result()
	if verErr == nil {
		parsed := parseRedisInfo(serverInfo)
		version := parsed["redis_version"]
		metrics = append(metrics, DBMetric{
			Name:  "db.instance.info",
			Value: 1,
			Unit:  "1",
			Attributes: map[string]string{
				"db.system":   "redis",
				"db.version":  version,
				"db.name":     r.name,
				"db.instance": r.instance,
			},
		})
	}

	m, err := r.collectInfo(ctx)
	if err != nil {
		log.Printf("WARN: redis %s INFO: %v", r.name, err)
	} else {
		metrics = append(metrics, m...)
	}

	m, err = r.collectSlowlog(ctx)
	if err != nil {
		log.Printf("WARN: redis %s SLOWLOG: %v", r.name, err)
	} else {
		metrics = append(metrics, m...)
	}

	return metrics, nil
}

func (r *RedisCollector) collectInfo(ctx context.Context) ([]DBMetric, error) {
	info, err := r.client.Info(ctx, "all").Result()
	if err != nil {
		return nil, fmt.Errorf("INFO command: %w", err)
	}

	parsed := parseRedisInfo(info)
	attrs := r.baseAttrs()

	var metrics []DBMetric

	addGauge := func(name, key, unit string) {
		if v, ok := parsed[key]; ok {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				metrics = append(metrics, DBMetric{Name: name, Value: f, Unit: unit, Attributes: attrs})
			}
		}
	}

	addGauge("db.redis.connections.connected", "connected_clients", "{connections}")
	addGauge("db.redis.connections.blocked", "blocked_clients", "{connections}")
	addGauge("db.redis.memory.used_bytes", "used_memory", "By")
	addGauge("db.redis.memory.peak_bytes", "used_memory_peak", "By")
	addGauge("db.redis.memory.fragmentation_ratio", "mem_fragmentation_ratio", "1")
	addGauge("db.redis.keyspace.hits", "keyspace_hits", "{hits}")
	addGauge("db.redis.keyspace.misses", "keyspace_misses", "{misses}")
	addGauge("db.redis.commands.processed", "total_commands_processed", "{commands}")
	addGauge("db.redis.commands.per_second", "instantaneous_ops_per_sec", "{ops}/s")
	addGauge("db.redis.evictions", "evicted_keys", "{keys}")
	addGauge("db.redis.expired_keys", "expired_keys", "{keys}")

	// Compute hit ratio
	hits, hitsOK := parseFloat(parsed, "keyspace_hits")
	misses, missesOK := parseFloat(parsed, "keyspace_misses")
	if hitsOK && missesOK && (hits+misses) > 0 {
		ratio := hits / (hits + misses)
		metrics = append(metrics, DBMetric{Name: "db.redis.keyspace.hit_ratio", Value: ratio, Unit: "1", Attributes: attrs})
	}

	// Replication lag (if replica)
	role := parsed["role"]
	if role == "slave" {
		masterOffset, mOK := parseFloat(parsed, "master_repl_offset")
		slaveOffset, sOK := parseFloat(parsed, "slave_repl_offset")
		if mOK && sOK {
			lag := masterOffset - slaveOffset
			metrics = append(metrics, DBMetric{Name: "db.redis.replication.lag", Value: lag, Unit: "{bytes}", Attributes: attrs})
		}
	}

	return metrics, nil
}

func (r *RedisCollector) collectSlowlog(ctx context.Context) ([]DBMetric, error) {
	entries, err := r.client.SlowLogGet(ctx, 10).Result()
	if err != nil {
		return nil, fmt.Errorf("SLOWLOG GET: %w", err)
	}

	attrs := r.baseAttrs()
	var metrics []DBMetric

	metrics = append(metrics, DBMetric{
		Name:       "db.redis.slowlog.count",
		Value:      float64(len(entries)),
		Unit:       "{entries}",
		Attributes: attrs,
	})

	if len(entries) > 0 {
		// entries[0] is the latest
		metrics = append(metrics, DBMetric{
			Name:       "db.redis.slowlog.latest_duration_us",
			Value:      float64(entries[0].Duration.Microseconds()),
			Unit:       "us",
			Attributes: attrs,
		})
	}

	return metrics, nil
}

// parseRedisInfo parses the text output of Redis INFO into key-value pairs.
func parseRedisInfo(info string) map[string]string {
	result := make(map[string]string)
	for _, line := range strings.Split(info, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			result[parts[0]] = parts[1]
		}
	}
	return result
}

func parseFloat(m map[string]string, key string) (float64, bool) {
	v, ok := m[key]
	if !ok {
		return 0, false
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}
