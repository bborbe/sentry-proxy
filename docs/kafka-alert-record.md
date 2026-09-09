# Kafka Alert Record — durable schema contract

Every alert the proxy receives is published to Kafka as a single JSON record on
the topic configured via `KAFKA_TOPIC`. The JSON shape below **is the contract**:
consumers parse it verbatim. Changes to this schema are versioned and are not
freely reversible once consumers exist, so treat any key rename, removal, or
semantic change as a coordinated migration.

## Record shape

```json
{
  "body": "{\"event_id\":\"...\",\"type\":\"transaction\"}...",
  "project": "project-a",
  "received_at": "2026-09-09T12:00:00Z",
  "outcome": "forwarded"
}
```

## Fields

| Field | Type | Meaning |
|-------|------|---------|
| `body` | string | Raw Sentry envelope bytes as received, unparsed |
| `project` | string | Second path segment of `/api/{project_id}/envelope/`, literal `unknown` when the path does not match |
| `received_at` | string | RFC3339, pod clock at receipt |
| `outcome` | string | One of `forwarded` / `rejected` / `upstream_error` |

## Message key

The Kafka message key is the `project` value, so all alerts for one project land
on one partition and preserve per-project order. Re-keying is a schema change for
existing consumers and must be treated as one.

## Topic naming

The topic is configured per environment via `KAFKA_TOPIC`:

| Environment | Topic |
|-------------|-------|
| dev | `develop-raw-sentry-alert-input` |
| prod | `master-raw-sentry-alert-input` |
