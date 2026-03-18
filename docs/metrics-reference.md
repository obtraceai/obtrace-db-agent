# Metrics Reference

Complete list of metrics collected by the Obtrace Database Agent, organized by database type.

All metrics are sent as OTLP Gauge data points. Each metric includes `db.system`, `db.name`, and `db.instance` attributes.

## PostgreSQL

Source: `pg_stat_database`, `pg_stat_activity`, `pg_stat_user_tables`, `pg_stat_statements`, `pg_database_size()`

| Metric | Unit | Source | Description |
|--------|------|--------|-------------|
| `db.postgres.connections.active` | {connections} | pg_stat_database | Active backend connections (numbackends) |
| `db.postgres.connections.idle` | {connections} | pg_stat_activity | Idle connections |
| `db.postgres.connections.waiting` | {connections} | pg_stat_activity | Connections waiting on locks |
| `db.postgres.transactions.committed` | {transactions} | pg_stat_database | Cumulative committed transactions |
| `db.postgres.transactions.rolledback` | {transactions} | pg_stat_database | Cumulative rolled back transactions |
| `db.postgres.blocks.read` | {blocks} | pg_stat_database | Disk blocks read |
| `db.postgres.blocks.hit` | {blocks} | pg_stat_database | Buffer cache hits |
| `db.postgres.cache_hit_ratio` | 1 | pg_stat_database | `blks_hit / (blks_hit + blks_read)` |
| `db.postgres.rows.returned` | {rows} | pg_stat_database | Rows returned by queries |
| `db.postgres.rows.fetched` | {rows} | pg_stat_database | Rows fetched by queries |
| `db.postgres.rows.inserted` | {rows} | pg_stat_database | Rows inserted |
| `db.postgres.rows.updated` | {rows} | pg_stat_database | Rows updated |
| `db.postgres.rows.deleted` | {rows} | pg_stat_database | Rows deleted |
| `db.postgres.deadlocks` | {deadlocks} | pg_stat_database | Deadlocks detected |
| `db.postgres.temp_bytes` | By | pg_stat_database | Temp file bytes written |
| `db.postgres.database_size_bytes` | By | pg_database_size() | Total database size |
| `db.postgres.longest_query_seconds` | s | pg_stat_activity | Longest running active query |
| `db.postgres.table.seq_scan` | {scans} | pg_stat_user_tables | Sequential scans (top 10 tables) |
| `db.postgres.table.idx_scan` | {scans} | pg_stat_user_tables | Index scans (top 10 tables) |
| `db.postgres.table.dead_tuples` | {tuples} | pg_stat_user_tables | Dead tuples (top 10 tables) |
| `db.postgres.table.live_tuples` | {tuples} | pg_stat_user_tables | Live tuples (top 10 tables) |
| `db.postgres.query.avg_time_ms` | ms | pg_stat_statements | Average query time (top 10 by mean time) |
| `db.postgres.query.calls` | {calls} | pg_stat_statements | Query call count (top 10 by mean time) |

Table-level metrics include a `db.sql.table` attribute. Query-level metrics include `db.statement` and `db.query_id` attributes.

## MySQL

Source: `SHOW GLOBAL STATUS`

| Metric | Unit | Status Variable | Description |
|--------|------|-----------------|-------------|
| `db.mysql.connections.current` | {connections} | Threads_connected | Currently connected threads |
| `db.mysql.connections.running` | {connections} | Threads_running | Threads executing queries |
| `db.mysql.connections.aborted` | {connections} | Aborted_connects | Failed connection attempts |
| `db.mysql.queries.total` | {queries} | Questions | Total queries executed |
| `db.mysql.queries.slow` | {queries} | Slow_queries | Queries exceeding `long_query_time` |
| `db.mysql.bytes.sent` | By | Bytes_sent | Total bytes sent to clients |
| `db.mysql.bytes.received` | By | Bytes_received | Total bytes received from clients |
| `db.mysql.innodb.row_lock_waits` | {waits} | Innodb_row_lock_waits | InnoDB row lock wait events |
| `db.mysql.innodb.row_lock_time_ms` | ms | Innodb_row_lock_time | Cumulative InnoDB row lock wait time |
| `db.mysql.innodb.buffer_pool_hit_ratio` | 1 | Computed | `(read_requests - reads) / read_requests` |
| `db.mysql.table_locks.waited` | {locks} | Table_locks_waited | Table lock contentions |

## MongoDB

Source: `serverStatus`, `replSetGetStatus`

| Metric | Unit | Source Field | Description |
|--------|------|--------------|-------------|
| `db.mongodb.connections.current` | {connections} | connections.current | Current client connections |
| `db.mongodb.connections.available` | {connections} | connections.available | Available connection slots |
| `db.mongodb.opcounters.query` | {operations} | opcounters.query | Query operations |
| `db.mongodb.opcounters.insert` | {operations} | opcounters.insert | Insert operations |
| `db.mongodb.opcounters.update` | {operations} | opcounters.update | Update operations |
| `db.mongodb.opcounters.delete` | {operations} | opcounters.delete | Delete operations |
| `db.mongodb.memory.resident_mb` | MiBy | mem.resident | Resident memory in MB |
| `db.mongodb.memory.virtual_mb` | MiBy | mem.virtual | Virtual memory in MB |
| `db.mongodb.network.bytes_in` | By | network.bytesIn | Network bytes received |
| `db.mongodb.network.bytes_out` | By | network.bytesOut | Network bytes sent |
| `db.mongodb.wiredtiger.cache_used_bytes` | By | wiredTiger.cache | WiredTiger cache usage |
| `db.mongodb.wiredtiger.cache_dirty_bytes` | By | wiredTiger.cache | Dirty bytes in WiredTiger cache |
| `db.mongodb.replication.lag_seconds` | s | replSetGetStatus | Replication lag (replica sets only) |

## Redis

Source: `INFO ALL`, `SLOWLOG GET`

| Metric | Unit | INFO Field | Description |
|--------|------|------------|-------------|
| `db.redis.connections.connected` | {connections} | connected_clients | Connected client count |
| `db.redis.connections.blocked` | {connections} | blocked_clients | Blocked client count |
| `db.redis.memory.used_bytes` | By | used_memory | Current memory usage |
| `db.redis.memory.peak_bytes` | By | used_memory_peak | Peak memory usage |
| `db.redis.memory.fragmentation_ratio` | 1 | mem_fragmentation_ratio | Memory fragmentation |
| `db.redis.keyspace.hits` | {hits} | keyspace_hits | Successful key lookups |
| `db.redis.keyspace.misses` | {misses} | keyspace_misses | Failed key lookups |
| `db.redis.keyspace.hit_ratio` | 1 | Computed | `hits / (hits + misses)` |
| `db.redis.commands.processed` | {commands} | total_commands_processed | Total commands processed |
| `db.redis.commands.per_second` | {ops}/s | instantaneous_ops_per_sec | Current ops per second |
| `db.redis.evictions` | {keys} | evicted_keys | Keys evicted due to maxmemory |
| `db.redis.expired_keys` | {keys} | expired_keys | Keys expired by TTL |
| `db.redis.replication.lag` | {bytes} | Computed | Replication offset lag (replicas only) |
| `db.redis.slowlog.count` | {entries} | SLOWLOG GET | Number of recent slow log entries |
| `db.redis.slowlog.latest_duration_us` | us | SLOWLOG GET | Duration of most recent slow entry |

## Cassandra

Source: Jolokia JMX HTTP endpoint or Prometheus exporter

| Metric | Unit | JMX Bean / Prometheus Metric | Description |
|--------|------|------------------------------|-------------|
| `db.cassandra.read_latency_ms` | ms | ClientRequest/Read latency | Client read request latency |
| `db.cassandra.write_latency_ms` | ms | ClientRequest/Write latency | Client write request latency |
| `db.cassandra.compactions_pending` | {compactions} | CompactionManager/PendingTasks | Pending compaction tasks |
| `db.cassandra.tombstones_scanned` | {tombstones} | ColumnFamily/TombstoneScannedHistogram | Tombstones scanned |
| `db.cassandra.connections.active` | {connections} | Client/connectedNativeClients | Active native client connections |
| `db.cassandra.storage.total_bytes` | By | Storage/Load | Total storage bytes |
| `db.cassandra.storage.live_bytes` | By | ColumnFamily/LiveDiskSpaceUsed | Live data disk space |

## Elasticsearch

Source: `_cluster/health`, `_nodes/stats`

| Metric | Unit | API Field | Description |
|--------|------|-----------|-------------|
| `db.elasticsearch.cluster.status` | 1 | _cluster/health.status | Cluster health (0=green, 1=yellow, 2=red) |
| `db.elasticsearch.cluster.nodes` | {nodes} | number_of_nodes | Cluster node count |
| `db.elasticsearch.cluster.shards.active` | {shards} | active_shards | Active shard count |
| `db.elasticsearch.cluster.shards.unassigned` | {shards} | unassigned_shards | Unassigned shard count |
| `db.elasticsearch.jvm.heap_used_bytes` | By | jvm.mem.heap_used_in_bytes | Aggregated JVM heap used |
| `db.elasticsearch.jvm.heap_max_bytes` | By | jvm.mem.heap_max_in_bytes | Aggregated JVM heap max |
| `db.elasticsearch.indices.docs_count` | {documents} | indices.docs.count | Total document count |
| `db.elasticsearch.indices.store_size_bytes` | By | indices.store.size_in_bytes | Total index store size |
| `db.elasticsearch.indices.indexing_rate` | {operations} | indices.indexing.index_total | Total indexing operations |
| `db.elasticsearch.indices.search_rate` | {operations} | indices.search.query_total | Total search operations |
| `db.elasticsearch.indices.search_latency_ms` | ms | Computed | `query_time_in_millis / query_total` |
| `db.elasticsearch.thread_pool.search_rejected` | {requests} | thread_pool.search.rejected | Rejected search requests |
| `db.elasticsearch.thread_pool.write_rejected` | {requests} | thread_pool.write.rejected | Rejected write requests |

## ClickHouse

Source: `system.metrics`, `system.events`, `system.parts`

| Metric | Unit | Source Table.Column | Description |
|--------|------|---------------------|-------------|
| `db.clickhouse.queries.active` | {queries} | system.metrics (Query) | Currently executing queries |
| `db.clickhouse.connections.http` | {connections} | system.metrics (HTTPConnection) | Active HTTP connections |
| `db.clickhouse.connections.tcp` | {connections} | system.metrics (TCPConnection) | Active TCP connections |
| `db.clickhouse.memory.tracked_bytes` | By | system.metrics (MemoryTracking) | Memory tracked by allocator |
| `db.clickhouse.queries.total` | {queries} | system.events (Query) | Total queries executed |
| `db.clickhouse.queries.failed` | {queries} | system.events (FailedQuery) | Total failed queries |
| `db.clickhouse.merges.total` | {merges} | system.events (Merge) | Total merge operations |
| `db.clickhouse.inserted_rows` | {rows} | system.events (InsertedRows) | Total rows inserted |
| `db.clickhouse.inserted_bytes` | By | system.events (InsertedBytes) | Total bytes inserted |
| `db.clickhouse.selected_rows` | {rows} | system.events (SelectedRows) | Total rows selected |
| `db.clickhouse.selected_bytes` | By | system.events (SelectedBytes) | Total bytes selected |
| `db.clickhouse.parts.active` | {parts} | system.parts (active=1) | Active data parts |
| `db.clickhouse.parts.total_bytes` | By | system.parts (bytes_on_disk) | Total bytes on disk |

## CockroachDB

Source: `_status/vars` (Prometheus format)

| Metric | Unit | Prometheus Metric | Description |
|--------|------|-------------------|-------------|
| `db.cockroachdb.sql.query_count` | {queries} | sql_query_count | Total SQL queries |
| `db.cockroachdb.sql.failure_count` | {queries} | sql_failure_count | Failed SQL queries |
| `db.cockroachdb.sql.latency_p99` | ns | sql_service_latency_bucket (p99) | SQL query latency (99th percentile) |
| `db.cockroachdb.ranges.total` | {ranges} | ranges | Total key ranges |
| `db.cockroachdb.ranges.unavailable` | {ranges} | ranges_unavailable | Unavailable ranges |
| `db.cockroachdb.liveness.live_nodes` | {nodes} | liveness_livenodes | Live nodes |
| `db.cockroachdb.capacity.used_bytes` | By | capacity_used | Storage used |
| `db.cockroachdb.capacity.available_bytes` | By | capacity_available | Storage available |

## SQLite

Source: PRAGMA queries

| Metric | Unit | PRAGMA | Description |
|--------|------|--------|-------------|
| `db.sqlite.database_size_bytes` | By | page_count * page_size | Total database file size |
| `db.sqlite.cache_size_pages` | {pages} | cache_size | Cache size in pages |
| `db.sqlite.page_size` | By | page_size | Page size in bytes |
| `db.sqlite.wal_checkpoint_count` | {checkpoints} | wal_checkpoint | WAL checkpoints (WAL mode only) |
