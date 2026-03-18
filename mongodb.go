package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// MongoDBCollector collects metrics from a MongoDB instance.
type MongoDBCollector struct {
	client   *mongo.Client
	name     string
	instance string
}

func NewMongoDBCollector(dsn, name, instance string) (*MongoDBCollector, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	clientOpts := options.Client().ApplyURI(dsn).
		SetMaxPoolSize(2).
		SetMinPoolSize(1)

	client, err := mongo.Connect(ctx, clientOpts)
	if err != nil {
		return nil, fmt.Errorf("connect to mongodb: %w", err)
	}

	if err := client.Ping(ctx, nil); err != nil {
		client.Disconnect(ctx)
		return nil, fmt.Errorf("ping mongodb: %w", err)
	}

	return &MongoDBCollector{
		client:   client,
		name:     name,
		instance: instance,
	}, nil
}

func (m *MongoDBCollector) Type() string { return "mongodb" }

func (m *MongoDBCollector) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return m.client.Disconnect(ctx)
}

func (m *MongoDBCollector) baseAttrs() map[string]string {
	return map[string]string{
		"db.system":   "mongodb",
		"db.name":     m.name,
		"db.instance": m.instance,
	}
}

func (m *MongoDBCollector) Collect(ctx context.Context) ([]DBMetric, error) {
	var metrics []DBMetric

	ss, err := m.collectServerStatus(ctx)
	if err != nil {
		log.Printf("WARN: mongodb %s serverStatus: %v", m.name, err)
	} else {
		metrics = append(metrics, ss...)
	}

	rs, err := m.collectReplSetStatus(ctx)
	if err != nil {
		// Expected if not a replica set
		log.Printf("DEBUG: mongodb %s replSetGetStatus: %v", m.name, err)
	} else {
		metrics = append(metrics, rs...)
	}

	return metrics, nil
}

func (m *MongoDBCollector) collectServerStatus(ctx context.Context) ([]DBMetric, error) {
	adminDB := m.client.Database("admin")
	result := adminDB.RunCommand(ctx, bson.D{{Key: "serverStatus", Value: 1}})

	var doc bson.M
	if err := result.Decode(&doc); err != nil {
		return nil, fmt.Errorf("decode serverStatus: %w", err)
	}

	attrs := m.baseAttrs()
	var metrics []DBMetric

	// connections
	if conns, ok := doc["connections"].(bson.M); ok {
		if v, ok := toFloat64(conns["current"]); ok {
			metrics = append(metrics, DBMetric{Name: "db.mongodb.connections.current", Value: v, Unit: "{connections}", Attributes: attrs})
		}
		if v, ok := toFloat64(conns["available"]); ok {
			metrics = append(metrics, DBMetric{Name: "db.mongodb.connections.available", Value: v, Unit: "{connections}", Attributes: attrs})
		}
	}

	// opcounters
	if ops, ok := doc["opcounters"].(bson.M); ok {
		for _, op := range []string{"query", "insert", "update", "delete"} {
			if v, ok := toFloat64(ops[op]); ok {
				metrics = append(metrics, DBMetric{
					Name:       fmt.Sprintf("db.mongodb.opcounters.%s", op),
					Value:      v,
					Unit:       "{operations}",
					Attributes: attrs,
				})
			}
		}
	}

	// mem
	if mem, ok := doc["mem"].(bson.M); ok {
		if v, ok := toFloat64(mem["resident"]); ok {
			metrics = append(metrics, DBMetric{Name: "db.mongodb.memory.resident_mb", Value: v, Unit: "MiBy", Attributes: attrs})
		}
		if v, ok := toFloat64(mem["virtual"]); ok {
			metrics = append(metrics, DBMetric{Name: "db.mongodb.memory.virtual_mb", Value: v, Unit: "MiBy", Attributes: attrs})
		}
	}

	// network
	if net, ok := doc["network"].(bson.M); ok {
		if v, ok := toFloat64(net["bytesIn"]); ok {
			metrics = append(metrics, DBMetric{Name: "db.mongodb.network.bytes_in", Value: v, Unit: "By", Attributes: attrs})
		}
		if v, ok := toFloat64(net["bytesOut"]); ok {
			metrics = append(metrics, DBMetric{Name: "db.mongodb.network.bytes_out", Value: v, Unit: "By", Attributes: attrs})
		}
	}

	// wiredTiger cache
	if wt, ok := doc["wiredTiger"].(bson.M); ok {
		if cache, ok := wt["cache"].(bson.M); ok {
			if v, ok := toFloat64(cache["bytes currently in the cache"]); ok {
				metrics = append(metrics, DBMetric{Name: "db.mongodb.wiredtiger.cache_used_bytes", Value: v, Unit: "By", Attributes: attrs})
			}
			if v, ok := toFloat64(cache["tracked dirty bytes in the cache"]); ok {
				metrics = append(metrics, DBMetric{Name: "db.mongodb.wiredtiger.cache_dirty_bytes", Value: v, Unit: "By", Attributes: attrs})
			}
		}
	}

	return metrics, nil
}

func (m *MongoDBCollector) collectReplSetStatus(ctx context.Context) ([]DBMetric, error) {
	adminDB := m.client.Database("admin")
	result := adminDB.RunCommand(ctx, bson.D{{Key: "replSetGetStatus", Value: 1}})

	var doc bson.M
	if err := result.Decode(&doc); err != nil {
		return nil, fmt.Errorf("replSetGetStatus: %w", err)
	}

	members, ok := doc["members"].(bson.A)
	if !ok {
		return nil, fmt.Errorf("no members in replSetGetStatus")
	}

	var primaryOptime, selfOptime interface{}
	for _, member := range members {
		m, ok := member.(bson.M)
		if !ok {
			continue
		}
		stateStr, _ := m["stateStr"].(string)
		isSelf, _ := m["self"].(bool)

		if stateStr == "PRIMARY" {
			if optime, ok := m["optimeDate"].(time.Time); ok {
				primaryOptime = optime
			}
		}
		if isSelf {
			if optime, ok := m["optimeDate"].(time.Time); ok {
				selfOptime = optime
			}
		}
	}

	if primaryOptime != nil && selfOptime != nil {
		primary := primaryOptime.(time.Time)
		self := selfOptime.(time.Time)
		lagSeconds := primary.Sub(self).Seconds()
		if lagSeconds < 0 {
			lagSeconds = 0
		}

		attrs := m.baseAttrs()
		return []DBMetric{
			{Name: "db.mongodb.replication.lag_seconds", Value: lagSeconds, Unit: "s", Attributes: attrs},
		}, nil
	}

	return nil, nil
}

// toFloat64 converts various BSON numeric types to float64.
func toFloat64(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case float64:
		return n, true
	default:
		return 0, false
	}
}
