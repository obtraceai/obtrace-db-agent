# obtrace-db-agent Documentation

The Obtrace Database Agent is a lightweight, standalone binary that connects to your databases, collects native performance metrics, and forwards them as OTLP telemetry to the Obtrace platform.

## How It Works

1. The agent starts and connects to each configured database using read-only credentials.
2. On a configurable interval (default: 15 seconds), it queries each database for performance metrics.
3. Metrics are converted to OTLP format and sent to the Obtrace ingest endpoint via HTTP POST.
4. The Obtrace platform stores, indexes, and visualizes the metrics alongside your application telemetry.

## Supported Databases

- **PostgreSQL** -- pg_stat_database, pg_stat_activity, pg_stat_statements, pg_stat_user_tables
- **MySQL** -- SHOW GLOBAL STATUS, InnoDB buffer pool statistics
- **MongoDB** -- serverStatus command, replSetGetStatus
- **Redis** -- INFO command, SLOWLOG
- **Cassandra** -- Jolokia JMX endpoint or Prometheus exporter
- **Elasticsearch** -- Cluster health API, node stats API
- **ClickHouse** -- system.metrics, system.events, system.parts
- **CockroachDB** -- _status/vars Prometheus endpoint
- **SQLite** -- PRAGMA queries (lightweight, for embedded databases)

## Architecture

```
+----------------+       +-------------------+       +------------------+
|  PostgreSQL    |       |                   |       |                  |
|  MySQL         | <---> |  obtrace-db-agent | ----> |  Obtrace Ingest  |
|  MongoDB       |       |                   |       |  (OTLP HTTP)     |
|  Redis         |       +-------------------+       +------------------+
|  Cassandra     |              |
|  Elasticsearch |              v
|  ClickHouse    |        Read-only queries
|  CockroachDB   |        every N seconds
|  SQLite        |
+----------------+
```

## Guides

- [Getting Started](getting-started.md) -- Install and run in under 5 minutes
- [Configuration](configuration.md) -- Environment variables and connection format
- [Metrics Reference](metrics-reference.md) -- Complete list of collected metrics by database
