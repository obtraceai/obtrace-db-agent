# Contributing

## Workflow

1. Create a branch from `main`.
2. Make focused changes with clear commit messages.
3. Run local validation before opening a PR.
4. Open a PR with context, scope, and validation evidence.

## Commit Style

Use concise, imperative commit messages:
- `feat: add elasticsearch collector`
- `fix: handle clickhouse connection timeout`
- `docs: update metrics reference`

## Validation

Run these checks before pushing:

```bash
go build ./...
go test ./...
go vet ./...
```

Ensure all collectors compile and pass any existing tests. If adding a new collector, verify it implements the `Collector` interface:

```go
var _ Collector = (*YourCollector)(nil)
```

## Pull Requests

PR description should include:
- What changed
- Why it changed
- How it was validated (e.g., tested against a local database instance)
- Any breaking changes (new env vars, changed metric names)

## Adding a New Collector

1. Create a new file named after the database (e.g., `newdb.go`).
2. Implement the `Collector` interface (`Type()`, `Collect()`, `Close()`).
3. Register the collector in `main.go` inside `newCollector()` and `extractInstance()`.
4. Use read-only queries only. Never write to, modify, or create schemas in monitored databases.
5. Use `baseAttrs()` to attach `db.system`, `db.name`, and `db.instance` to every metric.
6. Handle errors gracefully -- log warnings and continue rather than crashing.
7. Update `README.md` and `docs/metrics-reference.md` with new metrics.

## Security

- Never commit secrets, tokens, or private keys.
- Use env vars for credentials.
- All example DSNs should use placeholder values (`user:pass`, `your-api-key`).
- Test credentials should only exist in local/CI environments.
