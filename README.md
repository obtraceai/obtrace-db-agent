# obtrace-db-agent

Lightweight database metrics collector for [Obtrace](https://obtrace.ai). Connects to your databases, collects native performance metrics, and sends them as OTLP telemetry to the Obtrace ingest endpoint.

## Supported Databases

| Database | Metrics Source | Key Metrics |
|----------|---------------|-------------|
| **PostgreSQL** | pg_stat_database, pg_stat_activity, pg_stat_statements | Connections, cache hit ratio, deadlocks, slow queries, table bloat |
| **MySQL** | SHOW GLOBAL STATUS, InnoDB | Connections, queries, buffer pool, row locks |
| **MongoDB** | serverStatus, replSetGetStatus | Connections, opcounters, memory, replication lag |
| **Redis** | INFO, SLOWLOG | Memory, hit ratio, commands/sec, evictions |
| **Cassandra** | Jolokia/Prometheus exporter | Read/write latency, compactions, tombstones |
| **Elasticsearch** | _cluster/health, _nodes/stats | Index rate, search latency, heap, disk |
| **SQLite** | PRAGMA stats | Page count, cache usage (lightweight) |
| **CockroachDB** | _status/vars (Prometheus endpoint) | SQL latency, ranges, liveness |
| **ClickHouse** | system.metrics, system.events | Queries, merges, parts, memory |

## Design Principle

The agent is thin -- it collects and forwards, never modifies your database. All queries are read-only (SELECT, INFO, SHOW STATUS). Zero writes, zero schema changes.

## Install

### Docker (recommended)

```bash
docker run -d \
  --name obtrace-db-agent \
  -e OBTRACE_INGEST_URL=https://ingest.obtrace.ai \
  -e OBTRACE_API_KEY=your-api-key \
  -e DB_CONNECTIONS='[{"type":"postgres","dsn":"postgres://user:pass@host:5432/mydb","name":"primary"}]' \
  ghcr.io/obtrace/db-agent:latest
```

### Binary

```bash
go install github.com/obtrace/db-agent@latest
```

### Kubernetes

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: obtrace-db-agent
spec:
  replicas: 1
  selector:
    matchLabels:
      app: obtrace-db-agent
  template:
    metadata:
      labels:
        app: obtrace-db-agent
    spec:
      containers:
        - name: agent
          image: ghcr.io/obtrace/db-agent:latest
          env:
            - name: OBTRACE_INGEST_URL
              value: https://ingest.obtrace.ai
            - name: OBTRACE_API_KEY
              valueFrom:
                secretKeyRef:
                  name: obtrace-secrets
                  key: api-key
            - name: DB_CONNECTIONS
              valueFrom:
                configMapKeyRef:
                  name: obtrace-db-agent
                  key: connections
          resources:
            requests:
              memory: 32Mi
              cpu: 10m
            limits:
              memory: 64Mi
              cpu: 100m
```

## Configuration

### Required Environment Variables

| Variable | Description | Example |
|----------|-------------|---------|
| `OBTRACE_INGEST_URL` | Ingest endpoint | `https://ingest.obtrace.ai` |
| `OBTRACE_API_KEY` | Project API key | `obt_live_abc123...` |
| `DB_CONNECTIONS` | JSON array of database connections | See below |

### Optional Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `OBTRACE_APP_ID` | Application identifier | `db-agent` |
| `OBTRACE_ENV` | Environment name | `prod` |
| `OBTRACE_TENANT_ID` | Tenant identifier | (none) |
| `OBTRACE_PROJECT_ID` | Project identifier | (none) |
| `COLLECT_INTERVAL_MS` | Collection interval in milliseconds | `15000` |

### Connection Format

Each entry in `DB_CONNECTIONS` has three fields:

```json
{
  "type": "postgres|mysql|mongodb|redis|cassandra|elasticsearch|clickhouse|cockroachdb|sqlite",
  "dsn": "connection string",
  "name": "human-readable name"
}
```

#### PostgreSQL

```json
{"type": "postgres", "dsn": "postgres://user:pass@host:5432/mydb?sslmode=disable", "name": "primary"}
```

#### MySQL

```json
{"type": "mysql", "dsn": "user:pass@tcp(host:3306)/mydb", "name": "app-mysql"}
```

#### MongoDB

```json
{"type": "mongodb", "dsn": "mongodb://user:pass@host:27017/admin", "name": "documents"}
```

#### Redis

```json
{"type": "redis", "dsn": "redis://:password@host:6379/0", "name": "cache"}
```

#### Cassandra (Jolokia)

```json
{"type": "cassandra", "dsn": "http://host:8778/jolokia", "name": "cluster1"}
```

#### Cassandra (Prometheus exporter)

```json
{"type": "cassandra", "dsn": "http://host:9103/metrics", "name": "cluster1"}
```

#### Elasticsearch

```json
{"type": "elasticsearch", "dsn": "http://host:9200", "name": "search-cluster"}
```

With authentication:

```json
{"type": "elasticsearch", "dsn": "http://user:pass@host:9200", "name": "search-cluster"}
```

#### ClickHouse (HTTP API)

```json
{"type": "clickhouse", "dsn": "http://user:pass@host:8123", "name": "analytics"}
```

#### CockroachDB

```json
{"type": "cockroachdb", "dsn": "http://host:8080", "name": "crdb1"}
```

#### SQLite

```json
{"type": "sqlite", "dsn": "/path/to/db.sqlite", "name": "app-db"}
```

## Quickstart

Monitor a PostgreSQL database and a Redis cache with Docker Compose:

```yaml
version: "3.8"
services:
  postgres:
    image: postgres:16
    environment:
      POSTGRES_PASSWORD: secret
    ports:
      - "5432:5432"

  redis:
    image: redis:7
    ports:
      - "6379:6379"

  obtrace-db-agent:
    image: ghcr.io/obtrace/db-agent:latest
    depends_on:
      - postgres
      - redis
    environment:
      OBTRACE_INGEST_URL: https://ingest.obtrace.ai
      OBTRACE_API_KEY: ${OBTRACE_API_KEY}
      OBTRACE_ENV: staging
      COLLECT_INTERVAL_MS: "15000"
      DB_CONNECTIONS: >-
        [
          {"type": "postgres", "dsn": "postgres://postgres:secret@postgres:5432/postgres?sslmode=disable", "name": "primary"},
          {"type": "redis", "dsn": "redis://redis:6379/0", "name": "cache"}
        ]
```

```bash
export OBTRACE_API_KEY=obt_live_abc123
docker compose up -d
```

Metrics appear in the Obtrace dashboard within 15 seconds.

## Metrics Reference

### PostgreSQL

| Metric | Unit | Description |
|--------|------|-------------|
| `db.postgres.connections.active` | {connections} | Active backend connections |
| `db.postgres.connections.idle` | {connections} | Idle backend connections |
| `db.postgres.connections.waiting` | {connections} | Connections waiting on locks |
| `db.postgres.transactions.committed` | {transactions} | Total committed transactions |
| `db.postgres.transactions.rolledback` | {transactions} | Total rolled back transactions |
| `db.postgres.cache_hit_ratio` | 1 | Buffer cache hit ratio (0-1) |
| `db.postgres.deadlocks` | {deadlocks} | Total deadlocks detected |
| `db.postgres.rows.returned` | {rows} | Rows returned by queries |
| `db.postgres.rows.fetched` | {rows} | Rows fetched by queries |
| `db.postgres.rows.inserted` | {rows} | Rows inserted |
| `db.postgres.rows.updated` | {rows} | Rows updated |
| `db.postgres.rows.deleted` | {rows} | Rows deleted |
| `db.postgres.temp_bytes` | By | Temporary file bytes written |
| `db.postgres.database_size_bytes` | By | Total database size |
| `db.postgres.longest_query_seconds` | s | Duration of longest running query |
| `db.postgres.table.seq_scan` | {scans} | Sequential scans per table |
| `db.postgres.table.idx_scan` | {scans} | Index scans per table |
| `db.postgres.table.dead_tuples` | {tuples} | Dead tuples per table |
| `db.postgres.table.live_tuples` | {tuples} | Live tuples per table |
| `db.postgres.query.avg_time_ms` | ms | Average query execution time (pg_stat_statements) |
| `db.postgres.query.calls` | {calls} | Query call count (pg_stat_statements) |

### MySQL

| Metric | Unit | Description |
|--------|------|-------------|
| `db.mysql.connections.current` | {connections} | Currently connected threads |
| `db.mysql.connections.running` | {connections} | Currently running threads |
| `db.mysql.connections.aborted` | {connections} | Aborted connection attempts |
| `db.mysql.queries.total` | {queries} | Total queries executed |
| `db.mysql.queries.slow` | {queries} | Slow queries count |
| `db.mysql.bytes.sent` | By | Total bytes sent |
| `db.mysql.bytes.received` | By | Total bytes received |
| `db.mysql.innodb.row_lock_waits` | {waits} | InnoDB row lock waits |
| `db.mysql.innodb.row_lock_time_ms` | ms | InnoDB row lock wait time |
| `db.mysql.innodb.buffer_pool_hit_ratio` | 1 | InnoDB buffer pool hit ratio |
| `db.mysql.table_locks.waited` | {locks} | Table lock waits |

### MongoDB

| Metric | Unit | Description |
|--------|------|-------------|
| `db.mongodb.connections.current` | {connections} | Current connections |
| `db.mongodb.connections.available` | {connections} | Available connections |
| `db.mongodb.opcounters.query` | {operations} | Query operations |
| `db.mongodb.opcounters.insert` | {operations} | Insert operations |
| `db.mongodb.opcounters.update` | {operations} | Update operations |
| `db.mongodb.opcounters.delete` | {operations} | Delete operations |
| `db.mongodb.memory.resident_mb` | MiBy | Resident memory |
| `db.mongodb.memory.virtual_mb` | MiBy | Virtual memory |
| `db.mongodb.network.bytes_in` | By | Network bytes received |
| `db.mongodb.network.bytes_out` | By | Network bytes sent |
| `db.mongodb.wiredtiger.cache_used_bytes` | By | WiredTiger cache usage |
| `db.mongodb.wiredtiger.cache_dirty_bytes` | By | WiredTiger dirty cache bytes |
| `db.mongodb.replication.lag_seconds` | s | Replication lag |

### Redis

| Metric | Unit | Description |
|--------|------|-------------|
| `db.redis.connections.connected` | {connections} | Connected clients |
| `db.redis.connections.blocked` | {connections} | Blocked clients |
| `db.redis.memory.used_bytes` | By | Used memory |
| `db.redis.memory.peak_bytes` | By | Peak memory usage |
| `db.redis.memory.fragmentation_ratio` | 1 | Memory fragmentation ratio |
| `db.redis.keyspace.hits` | {hits} | Keyspace hits |
| `db.redis.keyspace.misses` | {misses} | Keyspace misses |
| `db.redis.keyspace.hit_ratio` | 1 | Keyspace hit ratio |
| `db.redis.commands.processed` | {commands} | Total commands processed |
| `db.redis.commands.per_second` | {ops}/s | Instantaneous ops/sec |
| `db.redis.evictions` | {keys} | Evicted keys |
| `db.redis.expired_keys` | {keys} | Expired keys |
| `db.redis.replication.lag` | {bytes} | Replication lag in bytes |
| `db.redis.slowlog.count` | {entries} | Slow log entries (last 10) |
| `db.redis.slowlog.latest_duration_us` | us | Latest slow log entry duration |

### Cassandra

| Metric | Unit | Description |
|--------|------|-------------|
| `db.cassandra.read_latency_ms` | ms | Client request read latency |
| `db.cassandra.write_latency_ms` | ms | Client request write latency |
| `db.cassandra.compactions_pending` | {compactions} | Pending compaction tasks |
| `db.cassandra.tombstones_scanned` | {tombstones} | Tombstones scanned |
| `db.cassandra.connections.active` | {connections} | Active connections |
| `db.cassandra.storage.total_bytes` | By | Total storage used |
| `db.cassandra.storage.live_bytes` | By | Live data storage |

### Elasticsearch

| Metric | Unit | Description |
|--------|------|-------------|
| `db.elasticsearch.cluster.status` | 1 | Cluster health (0=green, 1=yellow, 2=red) |
| `db.elasticsearch.cluster.nodes` | {nodes} | Number of nodes |
| `db.elasticsearch.cluster.shards.active` | {shards} | Active shards |
| `db.elasticsearch.cluster.shards.unassigned` | {shards} | Unassigned shards |
| `db.elasticsearch.jvm.heap_used_bytes` | By | JVM heap used (aggregated) |
| `db.elasticsearch.jvm.heap_max_bytes` | By | JVM heap max (aggregated) |
| `db.elasticsearch.indices.docs_count` | {documents} | Total document count |
| `db.elasticsearch.indices.store_size_bytes` | By | Total index store size |
| `db.elasticsearch.indices.indexing_rate` | {operations} | Total indexing operations |
| `db.elasticsearch.indices.search_rate` | {operations} | Total search query operations |
| `db.elasticsearch.indices.search_latency_ms` | ms | Average search latency |
| `db.elasticsearch.thread_pool.search_rejected` | {requests} | Rejected search thread pool requests |
| `db.elasticsearch.thread_pool.write_rejected` | {requests} | Rejected write thread pool requests |

### ClickHouse

| Metric | Unit | Description |
|--------|------|-------------|
| `db.clickhouse.queries.active` | {queries} | Currently running queries |
| `db.clickhouse.connections.http` | {connections} | Active HTTP connections |
| `db.clickhouse.connections.tcp` | {connections} | Active TCP connections |
| `db.clickhouse.memory.tracked_bytes` | By | Memory tracked by allocator |
| `db.clickhouse.queries.total` | {queries} | Total queries executed |
| `db.clickhouse.queries.failed` | {queries} | Total failed queries |
| `db.clickhouse.merges.total` | {merges} | Total merge operations |
| `db.clickhouse.inserted_rows` | {rows} | Total rows inserted |
| `db.clickhouse.inserted_bytes` | By | Total bytes inserted |
| `db.clickhouse.selected_rows` | {rows} | Total rows selected |
| `db.clickhouse.selected_bytes` | By | Total bytes selected |
| `db.clickhouse.parts.active` | {parts} | Active data parts |
| `db.clickhouse.parts.total_bytes` | By | Total bytes on disk for active parts |

### CockroachDB

| Metric | Unit | Description |
|--------|------|-------------|
| `db.cockroachdb.sql.query_count` | {queries} | Total SQL queries |
| `db.cockroachdb.sql.failure_count` | {queries} | Failed SQL queries |
| `db.cockroachdb.sql.latency_p99` | ns | SQL query latency (p99) |
| `db.cockroachdb.ranges.total` | {ranges} | Total ranges |
| `db.cockroachdb.ranges.unavailable` | {ranges} | Unavailable ranges |
| `db.cockroachdb.liveness.live_nodes` | {nodes} | Live nodes in cluster |
| `db.cockroachdb.capacity.used_bytes` | By | Storage used |
| `db.cockroachdb.capacity.available_bytes` | By | Storage available |

### SQLite

| Metric | Unit | Description |
|--------|------|-------------|
| `db.sqlite.database_size_bytes` | By | Database file size (page_count * page_size) |
| `db.sqlite.cache_size_pages` | {pages} | Cache size in pages |
| `db.sqlite.page_size` | By | Page size in bytes |
| `db.sqlite.wal_checkpoint_count` | {checkpoints} | WAL checkpoints (WAL mode only) |

## Production Hardening

- Use read-only database credentials for all monitored databases.
- Set appropriate collect interval (15-60s recommended for production).
- Monitor agent memory usage (64MB is sufficient for most setups).
- Use connection pooling -- the agent limits itself to 2 connections per database.
- Run the agent close to the databases it monitors to minimize network latency.
- For Kubernetes, set resource limits and use a dedicated service account.

## Troubleshooting

| Symptom | Cause | Fix |
|---------|-------|-----|
| `401` from ingest | Invalid API key | Verify `OBTRACE_API_KEY` matches your project key |
| No metrics appear | Database unreachable | Check DSN, network access, and firewall rules |
| High agent memory | Too many targets or low interval | Reduce `DB_CONNECTIONS` count or increase `COLLECT_INTERVAL_MS` |
| Collector skipped on startup | Invalid DSN or auth failure | Check logs for the specific error message |
| pg_stat_statements missing | Extension not installed | Run `CREATE EXTENSION pg_stat_statements` in PostgreSQL |
| Redis SLOWLOG empty | No slow queries | Normal behavior; adjust `slowlog-log-slower-than` in Redis config |

## Documentation

- [Getting Started](docs/getting-started.md)
- [Configuration](docs/configuration.md)
- [Metrics Reference](docs/metrics-reference.md)
