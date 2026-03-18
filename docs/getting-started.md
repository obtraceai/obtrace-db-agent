# Getting Started

This guide walks you through installing and running the Obtrace Database Agent.

## Prerequisites

- An Obtrace account with an API key
- Network access from the agent to your databases
- Network access from the agent to your Obtrace ingest endpoint

## Step 1: Choose an Installation Method

### Docker (recommended)

```bash
docker pull ghcr.io/obtrace/db-agent:latest
```

### Binary

```bash
go install github.com/obtrace/db-agent@latest
```

### From Source

```bash
git clone https://github.com/obtrace/db-agent.git
cd db-agent
go build -o db-agent .
```

## Step 2: Configure Database Connections

Create a JSON array describing each database you want to monitor. Each entry requires:

| Field | Description |
|-------|-------------|
| `type` | Database type: `postgres`, `mysql`, `mongodb`, `redis`, `cassandra`, `elasticsearch`, `clickhouse`, `cockroachdb`, `sqlite` |
| `dsn` | Connection string (format varies by database type) |
| `name` | Human-readable label for this database |

Example for a PostgreSQL database:

```json
[
  {
    "type": "postgres",
    "dsn": "postgres://readonly:password@db.example.com:5432/myapp?sslmode=require",
    "name": "primary"
  }
]
```

## Step 3: Set Environment Variables

```bash
export OBTRACE_INGEST_URL=https://ingest.obtrace.ai
export OBTRACE_API_KEY=obt_live_your_key_here
export OBTRACE_ENV=production
export DB_CONNECTIONS='[{"type":"postgres","dsn":"postgres://readonly:password@localhost:5432/myapp","name":"primary"}]'
```

## Step 4: Run the Agent

### Docker

```bash
docker run -d \
  --name obtrace-db-agent \
  -e OBTRACE_INGEST_URL \
  -e OBTRACE_API_KEY \
  -e OBTRACE_ENV \
  -e DB_CONNECTIONS \
  ghcr.io/obtrace/db-agent:latest
```

### Binary

```bash
./db-agent
```

## Step 5: Verify

Check the agent logs for successful collection messages:

```
INFO: starting obtrace-db-agent (interval=15s, targets=1, ingest=https://ingest.obtrace.ai)
INFO: registered collector primary (type=postgres)
INFO: sent 22 metrics
```

Metrics should appear in the Obtrace dashboard within one collection interval.

## Monitoring Multiple Databases

Pass multiple entries in `DB_CONNECTIONS`:

```bash
export DB_CONNECTIONS='[
  {"type": "postgres", "dsn": "postgres://ro:pass@pg:5432/app", "name": "primary"},
  {"type": "redis", "dsn": "redis://redis:6379/0", "name": "cache"},
  {"type": "mongodb", "dsn": "mongodb://ro:pass@mongo:27017/admin", "name": "documents"},
  {"type": "elasticsearch", "dsn": "http://es:9200", "name": "search"}
]'
```

The agent collects from all databases concurrently within each collection cycle.

## Next Steps

- [Configuration](configuration.md) -- All environment variables and connection formats
- [Metrics Reference](metrics-reference.md) -- Complete list of metrics by database type
