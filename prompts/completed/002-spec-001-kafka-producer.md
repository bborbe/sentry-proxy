---
status: completed
spec: [001-tee-sentry-alerts-to-kafka]
summary: 'Implemented the Kafka producer half of the tee: added bborbe/kafka v1.25.15, extended Metrics with a kafka_publish GaugeVec, created the KafkaAlertRecord durable contract + bounded-channel producer with 5s flush-on-shutdown and lazy sender retry, wired required KAFKA_BROKERS/KAFKA_TOPIC config and producer.Run into service.Run, and added docs plus Ginkgo and AC8 re-exec tests; make precommit exits 0'
execution_id: sentry-proxy-kafka-tee-exec-002-spec-001-kafka-producer
dark-factory-version: dev
created: "2026-09-09T15:06:15Z"
queued: "2026-09-09T16:55:31Z"
started: "2026-09-09T17:20:53Z"
completed: "2026-09-09T17:33:27Z"
---

# Kafka producer component, config, metrics, record contract

<summary>
- The service now requires `KAFKA_BROKERS` and `KAFKA_TOPIC` at startup; starting without either exits non-zero and names the missing flag, so the tee can never be silently disabled
- A new producer component buffers alert records on a bounded channel (default capacity 1000, injectable via the constructor) and a background worker publishes them to Kafka asynchronously, so a stalled broker never blocks the request path
- The worker flushes every queued record within a 5s deadline before the worker shuts down, so records buffered at pod-termination time are not lost
- Publish outcomes are observable without raising log verbosity via a new `sentry_proxy_kafka_publish_counter{result="success|failure|dropped"}` metric
- A broker unreachable at startup is logged and counted as publish failures, never fatal; the worker re-creates the Kafka sender lazily so a broker that returns is picked up without a restart
- The published record is a documented durable contract — JSON keys `body`, `project`, `received_at`, `outcome`, keyed by `project`, specified in the schema doc
- README flag table and CHANGELOG `## Unreleased` entry are updated
</summary>

<objective>
Build the Kafka publishing half of the tee: a producer component with a non-blocking bounded-channel worker that publishes alert records to Kafka, driven by two new required configuration flags, observable through a new Prometheus metric, and documented as a durable record contract. This prompt deliberately does NOT touch the request path — connecting the round tripper to the producer is the next prompt.
</objective>

<context>
Read `CLAUDE.md` for project conventions (Interface → Constructor → Struct → Method, `errors.Wrap(ctx, err, "...")` from `github.com/bborbe/errors`, `github.com/bborbe/time` over stdlib `time` for injected clocks, Ginkgo v2 + Gomega, Counterfeiter mocks via `//counterfeiter:generate`). Read `docs/dod.md` — it is this repo's `validationPrompt`.

Read these files before making changes:
- `main.go` — the `application` struct (find `BuildGitVersion`/`BuildGitCommit`/`BuildDate` fields), `Run` (finds `factory.CreateMetrics(prometheus.DefaultRegisterer)`), `createHTTPServer`. New config fields go in the `application` struct; the producer is constructed in `Run` and its `Run` added to `service.Run`.
- `pkg/metrics.go` — the `Metrics` interface (with its `//counterfeiter:generate -o ../mocks/metrics.go --fake-name Metrics . Metrics` directive) and the three existing plain `Gauge`s.
- `pkg/factory/factory.go` — existing factory wiring (`CreateMetrics`).
- `pkg/pkg_suite_test.go` — the Ginkgo suite bootstrap in `package pkg_test` that all `pkg/` specs join.
- `Makefile`, `Dockerfile`, `go.mod` — `go.mod` has NO Kafka dependency today; the build is `CGO_ENABLED=0` into `FROM scratch`, which is why the Kafka client must be pure Go.

Library APIs (verified against the module cache — quote these exactly):
- `github.com/bborbe/kafka` (add at v1.25.15). Relevant API:
  - `type Brokers []Broker`; `func (b *Brokers) UnmarshalText(text []byte) error` — the libargument library explicitly supports `kafka.Brokers` as a struct field (it implements `encoding.TextUnmarshaler` on the slice type itself; a bare broker string gets a `plain://` schema default via `ParseBroker`).
  - `type Topic string`; `func (t Topic) Validate(ctx context.Context) error` — `Topic` does NOT implement `encoding.TextUnmarshaler`, so the config field must be a plain `string` converted with `libkafka.Topic(...)`.
  - `func NewSyncProducer(ctx context.Context, brokers Brokers, opts ...SaramaConfigOptions) (SyncProducer, error)` — returns an error when no broker is reachable (sarama retries `Metadata.Retry.Max` = 10 then fails), which is exactly why broker unreachability at startup must not be fatal.
  - `type SyncProducer interface { SendMessage(ctx, msg) (int32, int64, error); SendMessages(...) error; Close() error }`
  - `func NewJSONSender(producer SyncProducer, logSamplerFactory log.SamplerFactory, optionsFns ...func(options *JSONSenderOptions)) JSONSender`
  - `type JSONSender interface { SendUpdate(ctx, topic Topic, key Key, value Value, headers ...sarama.RecordHeader) error; ... }` — `SendUpdate` JSON-encodes `value` and calls `value.Validate(ctx)` first (unless `ValidationDisabled`), so the record's `Validate` is on the hot path.
  - `type Key interface { Bytes() []byte; String() string }`; `func NewKey[K ~[]byte | ~string](value K) Key`
  - `type Value interface { validation.HasValidation }` — a `Validate(ctx context.Context) error` method.
- `github.com/bborbe/log` (already a direct dep): `var DefaultSamplerFactory SamplerFactory` — pass to `kafka.NewJSONSender`.
- `github.com/bborbe/validation` (currently indirect; becomes direct): `var Error = stderrors.New("validation error")` — use `validation.Error` as the wrapped error for record validation, mirroring `kafka.Topic.Validate`.
- `github.com/bborbe/service`: `service.Main` → `argument.ParseAndPrint` → on a missing `required:"true"` field logs `Required field empty, define parameter <arg> or define env <ENV>` and returns exit code 4; it also calls `ValidateHasValidation` which invokes `Validate(ctx)` on the app struct if it implements it.
- Counterfeiter mock for the sender, used in tests: `github.com/bborbe/kafka/mocks` → `KafkaJSONSender` fake with `SendUpdateCallCount()`, `SendUpdateArgsForCall(i) (context.Context, kafka.Topic, kafka.Key, kafka.Value, []sarama.RecordHeader)`, `SendUpdateReturns(err)`, `SendUpdateCalls(stub)`.

Coding-plugin guides to follow (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-prometheus-metrics-guide.md` — pre-initialize all label combinations to 0 so absent series don't break queries; metric-type rules.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-concurrency-patterns.md` — worker-loop guidance; raw `go func()` is allowed in `*_test.go` only.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-mocking-guide.md` and `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — Counterfeiter + Ginkgo patterns.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md` — `errors.Wrap(ctx, err, "...")`, never `fmt.Errorf`.
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md` — `## Unreleased` entry style with conventional prefixes.

Notes for the reviewer (not requirements):
- AC11 spans this prompt and the next: this prompt covers the bounded channel, the default capacity of 1000, and the `result="dropped"` count; the next prompt covers the request-path latency assertion under a stalled sender.
- The CHANGELOG entry written here describes the complete feature (the tee), even though the round-tripper wiring lands in the next prompt; do not add a second entry there.
</context>

<requirements>
1. **Add the Kafka dependency.** After writing the imports in step 2, run:
   ```bash
   go get github.com/bborbe/kafka@v1.25.15
   ```
   then `make ensure` (which runs `go mod tidy -e` and promotes `github.com/bborbe/validation` to a direct dependency). Do NOT run `go mod vendor`. This change adds NO new `replace` or `exclude` directives — the pre-existing `exclude (cloud.google.com/go v0.26.0)` in `go.mod` stays untouched.

2. **Extend `pkg.Metrics` in `pkg/metrics.go`.** Add three methods to the existing interface (keep the existing `//counterfeiter:generate` directive and the three existing methods unchanged):
   ```go
   type Metrics interface {
   	SentryAlertTotalInc()
   	SentryAlertRejectedInc()
   	SentryAlertForwardInc()
   	KafkaPublishSuccessInc()
   	KafkaPublishFailureInc()
   	KafkaPublishDroppedInc()
   }
   ```
   In `NewMetrics`, register a `GaugeVec` — NOT a `CounterVec`, deliberately (the spec mandates a gauge so a reviewer does not silently "correct" it), and no `_total` suffix on a gauge:
   ```go
   kafkaPublishCounter := prometheus.NewGaugeVec(prometheus.GaugeOpts{
   	Namespace: "sentry_proxy",
   	Subsystem: "kafka_publish",
   	Name:      "counter",
   	Help:      "Counter for kafka publishes by result",
   }, []string{"result"})
   ```
   Register it in the existing `registerer.MustRegister(...)` call, and pre-initialize all three label values to 0 immediately after creation so the series exists before any publish:
   ```go
   for _, result := range []string{"success", "failure", "dropped"} {
   	kafkaPublishCounter.WithLabelValues(result).Add(0)
   }
   ```
   Add the field to the `metrics` struct and three methods on `*metrics`:
   - `KafkaPublishSuccessInc()` → `WithLabelValues("success").Inc()`
   - `KafkaPublishFailureInc()` → `WithLabelValues("failure").Inc()`
   - `KafkaPublishDroppedInc()` → `WithLabelValues("dropped").Inc()`

3. **Create `pkg/kafka-record.go`** — the durable record type, whose JSON shape IS the contract (spec AC7). Exactly four JSON keys, in this order and with these tags:
   ```go
   // KafkaAlertRecord is the JSON record published to the Kafka topic for every
   // alert the proxy receives. It is a durable contract; consumers parse it
   // verbatim. Schema is documented in docs/kafka-alert-record.md.
   type KafkaAlertRecord struct {
   	Body       string `json:"body"`
   	Project    string `json:"project"`
   	ReceivedAt string `json:"received_at"`
   	Outcome    string `json:"outcome"`
   }
   ```
   Add `Validate(ctx context.Context) error` so the type satisfies `kafka.Value` (and the JSONSender's `validateValue` accepts it). Validate ONLY the `Outcome` domain — body/project/received_at are carried verbatim:
   ```go
   func (r KafkaAlertRecord) Validate(ctx context.Context) error {
   	switch r.Outcome {
   	case "forwarded", "rejected", "upstream_error":
   		return nil
   	default:
   		return errors.Wrapf(ctx, validation.Error, "invalid outcome %q", r.Outcome)
   	}
   }
   ```
   Imports: `context`, `github.com/bborbe/errors`, `github.com/bborbe/validation`.

4. **Create `pkg/kafka-producer.go`** — the component. Interface with counterfeiter directive (generates `mocks/producer.go` on `make generate`):
   ```go
   //counterfeiter:generate -o ../mocks/producer.go --fake-name Producer . Producer
   type Producer interface {
   	// Publish enqueues an alert record for asynchronous publication. It never
   	// blocks and never returns an error; when the bounded channel is full the
   	// record is dropped and counted (result="dropped"). Deliberately does NOT
   	// take a context: the enqueue must not depend on the request's lifecycle.
   	Publish(body []byte, project string, receivedAt time.Time, outcome string)
   	// Run drains the channel and publishes records until ctx is cancelled,
   	// then flushes all remaining queued records within a 5s deadline and
   	// returns nil.
   	Run(ctx context.Context) error
   }
   ```
   Constructor:
   ```go
   // NewProducer creates a Producer that asynchronously publishes alert records
   // to the given Kafka topic. createSender is called lazily by the worker and
   // cached on success; a failed creation is retried on the next record, so a
   // broker that returns after startup is picked up without a restart.
   // channelCapacity <= 0 defaults to 1000.
   func NewProducer(
   	createSender func(ctx context.Context) (libkafka.JSONSender, error),
   	topic libkafka.Topic,
   	metrics Metrics,
   	channelCapacity int,
   ) Producer
   ```
   Implementation (struct `producer` with fields `createSender`, `topic`, `metrics`, `channel chan KafkaAlertRecord`, plus `mux sync.Mutex` and `sender libkafka.JSONSender` for lazy sender caching):
   - `Publish`: build `KafkaAlertRecord{Body: string(body), Project: project, ReceivedAt: receivedAt.UTC().Format(time.RFC3339), Outcome: outcome}`, then a non-blocking enqueue — `select { case p.channel <- record: default: p.metrics.KafkaPublishDroppedInc(); glog.V(2).Infof("kafka publish dropped: channel full") }`. `received_at` must be RFC3339 and derived from the passed `receivedAt` (the round tripper's injected clock) — never `time.Now()` inside this component.
   - `Run`: loop on `select { case <-ctx.Done(): return p.flush(); case record := <-p.channel: p.send(ctx, record) }`.
   - `flush`: bound the drain with a 5s `time.NewTimer(5 * time.Second)`; loop `select { case record := <-p.channel: p.send(context.Background(), record); case <-timer.C: return nil; default: return nil }`. Uses `context.Background()` because the original ctx is already cancelled. Returns `nil`.
   - `send`: obtain the cached-or-new sender via `getSender`; on `SendUpdate` error count `KafkaPublishFailureInc()` and log at `glog.V(2)`; on success count `KafkaPublishSuccessInc()`. A sender-creation error counts `KafkaPublishFailureInc()` and is logged — never propagated.
   - `getSender`: mutex-guarded; return the cached sender if present, else call `createSender(ctx)`, cache on success, and wrap failure with `errors.Wrap(ctx, err, "create kafka sender failed")` without caching (so the next record retries creation — this is the broker-returns recovery path).
   Imports: `bytes` not needed here; `context`, `sync`, `time`, `github.com/bborbe/errors`, `libkafka "github.com/bborbe/kafka"`, `github.com/golang/glog`. Use the `libkafka` alias consistently in every signature in this file.

5. **Add `CreateProducer` to `pkg/factory/factory.go`.** It must NOT return an error — broker unreachability is not fatal, only missing/malformed config is. Wire brokers → sync producer → JSONSender → component:
   ```go
   func CreateProducer(
   	brokers libkafka.Brokers,
   	topic libkafka.Topic,
   	metrics pkg.Metrics,
   ) pkg.Producer {
   	return pkg.NewProducer(
   		func(ctx context.Context) (libkafka.JSONSender, error) {
   			syncProducer, err := libkafka.NewSyncProducer(ctx, brokers)
   			if err != nil {
   				return nil, errors.Wrapf(ctx, err, "create kafka sync producer failed")
   			}
   			return libkafka.NewJSONSender(syncProducer, log.DefaultSamplerFactory), nil
   		},
   		topic,
   		metrics,
   		0,
   	)
   }
   ```
   Add imports `context`, `github.com/bborbe/errors`, `libkafka "github.com/bborbe/kafka"`, `"github.com/bborbe/log"` to `factory.go` (the `libkafka` alias, matching the repo's `libhttp`/`libtime` convention). `channelCapacity` 0 selects the 1000 default.

6. **Wire config and startup in `main.go`.**
   - Add the import `libkafka "github.com/bborbe/kafka"` (grouped with the other `github.com/bborbe/*` third-party imports).
   - Add two fields to the `application` struct, immediately before `BuildGitVersion`, with the same tag order (`required`, `arg`, `env`, `usage`) as the existing fields, then realign the manually-aligned tag columns across the whole struct (gofmt does not do this):
     ```go
     KafkaBrokers libkafka.Brokers `required:"true"  arg:"kafka-brokers" env:"KAFKA_BROKERS" usage:"Kafka brokers"`
     KafkaTopic   string           `required:"true"  arg:"kafka-topic"   env:"KAFKA_TOPIC"   usage:"Kafka topic"`
     ```
     Both are `required:"true"` — never optional (spec AC8). `kafka.Brokers` implements `encoding.TextUnmarshaler`, so libargument parses the comma-separated env value directly. `KafkaTopic` is a plain `string` because `kafka.Topic` does not implement `TextUnmarshaler`.
   - Add a `Validate` method on `*application` so malformed configuration is fatal at startup (the libargument `ValidateHasValidation` step calls it after required-field validation):
     ```go
     func (a *application) Validate(ctx context.Context) error {
     	if err := libkafka.Topic(a.KafkaTopic).Validate(ctx); err != nil {
     		return errors.Wrap(ctx, err, "validate kafka topic failed")
     	}
     	return nil
     }
     ```
   - In `Run`, after `metrics := factory.CreateMetrics(prometheus.DefaultRegisterer)`, construct the producer and start its worker:
     ```go
     producer := factory.CreateProducer(a.KafkaBrokers, libkafka.Topic(a.KafkaTopic), metrics)
     return service.Run(
     	ctx,
     	producer.Run,
     	a.createHTTPServer(sentryClient, metrics, currentTime),
     )
     ```
     Do NOT change the `createHTTPServer` signature in this prompt — threading the producer into the request path is the next prompt. The producer is live (worker up) but not yet connected to any request.

7. **Create `docs/kafka-alert-record.md`** — the durable schema contract (spec AC7). The file MUST contain the literal strings `body`, `project`, `received_at`, `outcome`, `forwarded`, `rejected`, `upstream_error` (the verification greps for them, so an empty file cannot pass). Required content:
   - A JSON example of one record with all four keys.
   - Field table: `body` (string, raw Sentry envelope bytes as received, unparsed), `project` (string, second path segment of `/api/{project_id}/envelope/`, literal `unknown` when the path does not match), `received_at` (string, RFC3339, pod clock at receipt), `outcome` (string, one of `forwarded` / `rejected` / `upstream_error`).
   - Message-key semantics: the Kafka message key is the `project` value, so all alerts for one project land on one partition and preserve per-project order; re-keying is a schema change for existing consumers.
   - Topic naming: configured per environment via `KAFKA_TOPIC`; dev uses `develop-raw-sentry-alert-input`, prod uses `master-raw-sentry-alert-input`.
   - A note that the schema is versioned and changes are not freely reversible once consumers exist.

8. **Update `README.md`.** Add two rows to the `## Configuration` table, in the existing column format:
   ```
   | `KAFKA_BROKERS` | `-kafka-brokers` | yes | Kafka bootstrap brokers (comma-separated; `plain://` assumed) |
   | `KAFKA_TOPIC` | `-kafka-topic` | yes | Kafka topic to publish received alert records to |
   ```
   Also update `CLAUDE.md`: add the two `KAFKA_BROKERS` / `KAFKA_TOPIC` rows to its `## Configuration` table, and reword the "There is **no Kafka client and no datastore** … stateless HTTP proxy" sentence (~lines 100-101) to reflect the Kafka producer (e.g. "The service publishes every received alert to Kafka via `github.com/bborbe/kafka`; its only in-memory state is the request counter.").

9. **Update `CHANGELOG.md`.** Create a new `## Unreleased` section directly above `## v0.3.3` (the file has no `## Unreleased` yet) with one `feat:` bullet in the existing bullet style describing the whole feature, e.g.:
   ```
   - feat: tee every received Sentry alert to a Kafka topic as a durable record (`docs/kafka-alert-record.md`) before the rate-limit or forwarding decision, keyed by project; new required config `KAFKA_BROKERS` / `KAFKA_TOPIC`
   ```

10. **Tests — `pkg/kafka_producer_test.go`** (Ginkgo, `package pkg_test`, joins the existing `pkg/pkg_suite_test.go` suite). Import `kafkamocks "github.com/bborbe/kafka/mocks"` for the `KafkaJSONSender` fake, `libkafka "github.com/bborbe/kafka"`, and `mocks "github.com/bborbe/sentry-proxy/mocks"` for the `Metrics` fake. Helper: a `createSender` returning the fake:
    ```go
    createSender := func(ctx context.Context) (libkafka.JSONSender, error) { return fake, nil }
    ```
    Write these specs (each requirement below is one `It`):
    - **Flush on shutdown (AC9):** capacity 100; enqueue 10 records via `Publish`; start `producer.Run(ctx)` in a goroutine (`done := make(chan error, 1); go func() { done <- producer.Run(ctx) }()`); then `cancel()`; assert `Eventually(done).Should(Receive(BeNil()))` and `fake.SendUpdateCallCount() == 10`. This asserts records queued at cancellation time are all published before `Run` returns.
    - **Failure counted (AC6):** real registry path — `registry := prometheus.NewRegistry()`, `metrics := pkg.NewMetrics(registry)`, `createSender` that returns an error; `Publish` one record; run `producer.Run` with an already-cancelled context (so it goes straight to flush); then gather `registry.Gather()`, find the metric family named `sentry_proxy_kafka_publish_counter`, and assert the metric with label `result="failure"` has value 1. Repeat with a succeeding fake to assert `result="success"` == 1. This is the boundary test that the metric is actually registered and incremented — a `mocks.Metrics` call-count assertion alone does not satisfy AC6.
    - **Drop counted (AC11 drop clause):** capacity 1 (constructor injection), `metrics := &mocks.Metrics{}`; `Publish` twice WITHOUT running the worker; assert `metrics.KafkaPublishDroppedIncCallCount() == 1`. The first enqueue fills the channel, the second hits the `default` branch.
    - **Record contract (AC7):** capacity 10, fake `KafkaJSONSender`; `Publish([]byte("raw-envelope"), "project-a", time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC), "forwarded")`; run `producer.Run` with an already-cancelled context; assert `fake.SendUpdateCallCount() == 1`; from `fake.SendUpdateArgsForCall(0)` assert topic == `libkafka.Topic("develop-raw-sentry-alert-input")`, `string(key.Bytes()) == "project-a"` (message key is the project), and the value type-asserts to `pkg.KafkaAlertRecord` with `Body == "raw-envelope"`, `Project == "project-a"`, `ReceivedAt == "2026-09-09T12:00:00Z"` (RFC3339), `Outcome == "forwarded"`. Then `json.Marshal` the record, unmarshal into `map[string]any`, and assert the key set is EXACTLY `{body, project, received_at, outcome}` (`HaveLen(4)` plus `HaveKey` for each). Additionally assert `libkafka.Topic("develop-raw-sentry-alert-input").Validate(ctx)` returns nil — the valid dev topic name crosses the library validator boundary and must be exercised.
    - **Sender-creation retry recovers:** `createSender` fails on the first call and succeeds on the second; enqueue two records and drain via `producer.Run` with a cancelled context; assert one `KafkaPublishFailureInc` and one `KafkaPublishSuccessInc` on the `mocks.Metrics` fake. This locks the broker-returns recovery path from the failure modes table.
    - **Record validation boundary (AC7 contract):** table test on `pkg.KafkaAlertRecord.Validate` — `forwarded` / `rejected` / `upstream_error` → `nil`; any other outcome → error wrapping `validation.Error` (mirrors `libkafka.Topic.Validate`). This exercises the validator the production JSONSender calls on every send — the AC7 test's fake sender bypasses it.

11. **Tests — `main_test.go` (AC8)** — a plain `testing` test at the repo root in `package main` (do NOT add it to the Ginkgo suite). It re-executes the test binary as a subprocess whose `main()` runs the real `service.Main` argument parsing. Structure:
    ```go
    func TestAC8MissingKafkaConfigFailsStartup(t *testing.T) {
    	if os.Getenv("SENTRY_PROXY_AC8_HELPER") == "1" {
    		main()
    		return
    	}
    	// base env supplies every OTHER required flag so only the kafka flag is missing
    	...
    }
    ```
    For each of the two cases (`KAFKA_BROKERS` and `KAFKA_TOPIC`): build `exec.Command(os.Args[0], "-test.run=^TestAC8MissingKafkaConfigFailsStartup$")`; inherit the test environment but strip the env var under test; append `SENTRY_DSN=https://00000000000000000000000000000000@ingest.example.com/1`, `LISTEN=:9090`, `REQUEST_LIMIT=100`, `REQUEST_DURATION=1h`, and `SENTRY_PROXY_AC8_HELPER=1`; run with `CombinedOutput()`. Assert the subprocess exited non-zero (`*exec.ExitError` with `ExitCode() != 0` — `service.Main` returns 4) and that the combined output contains the missing flag name (`KAFKA_BROKERS` / `KAFKA_TOPIC`). This is the process-level proof that the tee is required, never silently disabled.

Third case — malformed value, not missing: set `KAFKA_TOPIC` to a value that fails `libkafka.Topic.Validate` (e.g. containing characters outside `[a-zA-Z0-9._-]`); assert the same non-zero exit with the combined output containing the wrapped error text `validate kafka topic failed` (the topic value itself appears only in the printed config, not in the validation error). This is the `(*application).Validate` half of AC8 — required-field validation alone would pass a non-empty but invalid topic.
</requirements>

<constraints>
- [Copied from the spec]
- `NewRateLimitRoundTripper`'s allowance arithmetic must not change. Its integer-division defect (`pkg/ratelimit-roundtripper.go:35`) is tracked separately; fixing it here would confound two changes.
- The synthesised `429` response body and status code must not change — callers may match on them.
- The existing `SentryAlertTotal` / `SentryAlertForward` / `SentryAlertRejected` metrics keep their current meaning. New metrics are added; existing ones are never repurposed.
- **An unreachable broker never blocks forwarding, at any point in the process lifetime including startup.** Only missing or malformed *configuration* is fatal (AC8). The config is checkable at startup; broker reachability is not, and treating the latter as fatal would let a Kafka outage take down Sentry forwarding.
- `/healthz`, `/readiness`, `/metrics` and `/setloglevel/{level}` keep their current behavior.
- **The Kafka client must be pure Go.** `Dockerfile:4` builds with `CGO_ENABLED=0` into `FROM scratch` (`Dockerfile:10`), so a librdkafka-backed client such as `confluent-kafka-go` breaks the build outright. Use the fleet standard `github.com/bborbe/kafka`.
- Adds no **new** `replace` / `exclude` directives to `go.mod`. The existing `exclude (cloud.google.com/go v0.26.0)` at `go.mod:51-53` is pre-existing and stays.
- The repo has **no behavioral tests today** — `pkg/pkg_suite_test.go` and `pkg/factory/factory_suite_test.go` are Ginkgo bootstraps with zero specs. The new tests must therefore also lock the current forward / reject / `429`-body behavior frozen in the spec Constraints, not only the new tee.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
- Do NOT run `go mod vendor` — `vendor/` is a build-time artifact.
</constraints>

<verification>
Run `make precommit` — must pass.

Then run:
```bash
go test -mod=mod ./... -count=1
```
(all packages, so the AC8 re-exec test in `package main` runs too; `-run TestSuite` alone would skip it).

Targeted checks:
- `test -f mocks/producer.go` — the counterfeiter mock for `Producer` was generated by `make generate`
- `grep -n 'sentry_proxy' pkg/metrics.go | grep kafka_publish` — the GaugeVec is registered with subsystem `kafka_publish`; confirm it is `NewGaugeVec`, NOT `NewCounterVec`
- `grep -n 'KafkaBrokers\|KafkaTopic' main.go` — both fields present with `required:"true"`
- `grep -n 'producer.Run' main.go` — the worker is wired into `service.Run`
- `grep -n 'kafka' go.mod` — `github.com/bborbe/kafka` present; `grep -nE '^(replace|exclude)' go.mod` must show only the pre-existing `cloud.google.com/go` exclude
- For each of `body`, `project`, `received_at`, `outcome`, `forwarded`, `rejected`, `upstream_error`: `grep -c '<token>' docs/kafka-alert-record.md` returns at least 1 (the spec's anti-empty-file guard)
- `grep -n 'KAFKA_BROKERS\|KAFKA_TOPIC' README.md` — both rows present
- `grep -n '## Unreleased' CHANGELOG.md` — section exists above `## v0.3.3`
</verification>
