# sentry-proxy

A small rate-limiting reverse proxy for the [Sentry](https://sentry.io) ingest API. It forwards
`/api/...` event traffic to the upstream derived from the configured `SENTRY_DSN`, applying a
request limit over a sliding window so a noisy client cannot exhaust the Sentry quota. Exposes a
tiny HTTP admin surface for health checks, metrics, and runtime log-level changes.

## Run locally

```bash
make test
make run
```

## Build & publish image

```bash
make buca   # builds + pushes docker.io/bborbe/sentry-proxy:<git-tag>
```

## Configuration

| Env | Flag | Required | Purpose |
|-----|------|----------|---------|
| `SENTRY_DSN` | `-sentry-dsn` | yes | Sentry DSN; its host is the upstream ingest target |
| `SENTRY_PROXY` | `-sentry-proxy` | no | Outbound proxy for the proxy's own Sentry client |
| `LISTEN` | `-listen` | yes | Address to listen on (e.g. `:9090`) |
| `REQUEST_LIMIT` | `-request-limit` | yes | Max forwarded requests per window |
| `REQUEST_DURATION` | `-request-duration` | yes | Sliding-window duration |

## HTTP endpoints

Served on the configured listen address (`-listen`):

```
GET   /healthz              # liveness — returns OK
GET   /readiness            # readiness — returns OK
GET   /metrics              # Prometheus metrics
GET   /setloglevel/{n}      # set glog verbosity to n
*     /api/...              # rate-limited proxy to the Sentry ingest upstream
```

## License

BSD-2-Clause — see [LICENSE](LICENSE).
