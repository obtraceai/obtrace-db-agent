# Configuration

The Obtrace Database Agent is configured entirely through environment variables.

## Environment Variables

### Required

| Variable | Description | Example |
|----------|-------------|---------|
| `OBTRACE_API_KEY` | Project API key for authentication | `obt_live_abc123` |
| `DB_CONNECTIONS` | JSON array of database connection configs | See below |

### Optional

| Variable | Description | Default |
|----------|-------------|---------|
| `OBTRACE_INGEST_URL` | Ingest endpoint URL | `http://localhost:8080` |
| `OBTRACE_APP_ID` | Application identifier sent as `service.name` | `db-agent` |
| `OBTRACE_ENV` | Environment name sent as `deployment.environment` | `prod` |
| `OBTRACE_TENANT_ID` | Tenant identifier (multi-tenant setups) | (none) |
| `OBTRACE_PROJECT_ID` | Project identifier | (none) |
| `COLLECT_INTERVAL_MS` | Collection interval in milliseconds (minimum 1000) | `15000` |

## Connection Configuration

`DB_CONNECTIONS` is a JSON array. Each element has three fields:

```json
{
  "type": "<database-type>",
  "dsn": "<connection-string>",
  "name": "<human-readable-name>"
}
```

### PostgreSQL

**Type:** `postgres`

**DSN format:** Standard PostgreSQL connection URI.

```json
{"type": "postgres", "dsn": "postgres://user:pass@host:5432/dbname?sslmode=disable", "name": "primary"}
```

**Parameters:**
- `sslmode` -- `disable`, `require`, `verify-ca`, `verify-full`
- Standard libpq parameters are supported

**Required permissions:** The user needs `SELECT` on `pg_stat_database`, `pg_stat_activity`, and `pg_stat_user_tables`. For `pg_stat_statements`, the extension must be installed and the user needs access.

### MySQL

**Type:** `mysql`

**DSN format:** Go MySQL driver format.

```json
{"type": "mysql", "dsn": "user:pass@tcp(host:3306)/dbname", "name": "app-mysql"}
```

**Parameters:**
- `tls=true` for TLS connections
- `timeout=5s` for connection timeout

**Required permissions:** `PROCESS` privilege for `SHOW GLOBAL STATUS`.

### MongoDB

**Type:** `mongodb`

**DSN format:** Standard MongoDB connection URI.

```json
{"type": "mongodb", "dsn": "mongodb://user:pass@host:27017/admin", "name": "documents"}
```

**Parameters:**
- `authSource=admin` for authentication database
- `replicaSet=rs0` for replica set connections

**Required permissions:** `clusterMonitor` role for `serverStatus` and `replSetGetStatus`.

### Redis

**Type:** `redis`

**DSN format:** Standard Redis URI.

```json
{"type": "redis", "dsn": "redis://:password@host:6379/0", "name": "cache"}
```

**Parameters:**
- Database number in the path (`/0`, `/1`, etc.)
- TLS: use `rediss://` scheme

**Required permissions:** `INFO` and `SLOWLOG` commands must be allowed.

### Cassandra

**Type:** `cassandra`

**DSN format:** HTTP URL to Jolokia JMX endpoint or Prometheus exporter.

Jolokia:
```json
{"type": "cassandra", "dsn": "http://host:8778/jolokia", "name": "cluster1"}
```

Prometheus exporter (mcac or similar):
```json
{"type": "cassandra", "dsn": "http://host:9103/metrics", "name": "cluster1"}
```

**Required setup:** Jolokia agent installed on the Cassandra node, or a Prometheus metrics exporter (e.g., `mcac`, `cassandra-exporter`).

### Elasticsearch

**Type:** `elasticsearch`

**DSN format:** HTTP URL to the Elasticsearch cluster.

```json
{"type": "elasticsearch", "dsn": "http://host:9200", "name": "search-cluster"}
```

With basic authentication:
```json
{"type": "elasticsearch", "dsn": "http://user:pass@host:9200", "name": "search-cluster"}
```

**Required permissions:** Cluster `monitor` privilege for `_cluster/health` and `_nodes/stats`.

### ClickHouse

**Type:** `clickhouse`

**DSN format:** HTTP URL to the ClickHouse HTTP interface (port 8123).

```json
{"type": "clickhouse", "dsn": "http://user:pass@host:8123", "name": "analytics"}
```

**Required permissions:** `SELECT` on `system.metrics`, `system.events`, and `system.parts`.

### CockroachDB

**Type:** `cockroachdb`

**DSN format:** HTTP URL to the CockroachDB admin UI endpoint.

```json
{"type": "cockroachdb", "dsn": "http://host:8080", "name": "crdb1"}
```

**Required setup:** The `_status/vars` endpoint must be accessible. No authentication is required by default, but secure clusters may require TLS client certificates.

### SQLite

**Type:** `sqlite`

**DSN format:** File path to the SQLite database.

```json
{"type": "sqlite", "dsn": "/path/to/db.sqlite", "name": "app-db"}
```

**Required permissions:** Read access to the database file. The agent opens the database in read-only mode.

## Resource Attributes

The agent attaches these OTLP resource attributes to all metrics:

| Attribute | Source |
|-----------|--------|
| `service.name` | `OBTRACE_APP_ID` |
| `deployment.environment` | `OBTRACE_ENV` |
| `obtrace.tenant_id` | `OBTRACE_TENANT_ID` (if set) |
| `obtrace.project_id` | `OBTRACE_PROJECT_ID` (if set) |

Each metric also carries these attributes:

| Attribute | Description |
|-----------|-------------|
| `db.system` | Database system type (e.g., `postgres`, `redis`) |
| `db.name` | User-provided connection name |
| `db.instance` | Extracted host:port from the DSN |

## Collection Behavior

- All collectors run concurrently within each collection cycle.
- Each collection cycle has a 10-second timeout.
- If a collector fails, it logs a warning and the agent continues with the remaining collectors.
- If a collector cannot be initialized at startup (bad DSN, unreachable host), it is skipped with a warning.
- If no collectors can be initialized, the agent exits with an error.
