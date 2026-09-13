---
status: completed
summary: Registered all four sentry-proxy metrics as Prometheus counters with _total names, updated the kafka metric reader and added a registry-level spec asserting type, name and pre-initialization.
execution_id: sentry-proxy-metrics-counter-exec-004-fix-metrics-counter-type-and-total-suffix
dark-factory-version: v0.193.0
created: "2026-09-13T12:31:25Z"
queued: "2026-09-13T13:34:46Z"
started: "2026-09-13T13:35:24Z"
completed: "2026-09-13T13:38:30Z"
---

# Register the sentry-proxy metrics as counters with _total names

<summary>
- The proxy's four metrics are registered as counters, so `rate()` and `increase()` queries in PromQL — and every dashboard built on them — report correct numbers instead of reading a legitimate dip as a counter reset
- Every exposed metric name now ends in `_total`, as the project's metrics convention requires for counters
- The three alert metrics keep their current meaning — received, rejected, forwarded — and the Kafka publish metric keeps its `result` label with the `success`, `failure` and `dropped` series
- All four metrics are exposed with value 0 from startup — the three `result` series are pre-initialized — so an alert or a dashboard panel never silently skips an absent series
- A new test proves, through the real metrics registry, that every exposed metric is a counter and every name ends in `_total`
- The existing tests that read the Kafka publish metric were updated for the new name and the counter shape, so they keep asserting real values instead of silently reading zero
- The old series names have no consumers anywhere, so the rename breaks nothing downstream
- A changelog entry records the fix
</summary>

<objective>
Make the four proxy metrics honest counters: each one is only ever incremented, so each must be registered as a Prometheus counter and must carry the `_total` suffix the naming convention requires — a gauge used as a counter makes PromQL reset detection unsound, so `rate()`/`increase()` and the Grafana panels built on them silently produce wrong numbers. This is a metric type-and-name fix only: no HTTP behavior, no interface, and no request-path or Kafka-path change.
</objective>

<context>
Read `CLAUDE.md` for project conventions (Interface → Constructor → Struct → Method, `errors.Wrap(ctx, err, "...")` from `github.com/bborbe/errors`, Ginkgo v2 + Gomega, Counterfeiter mocks via `//counterfeiter:generate`) and `docs/dod.md` — it is this repo's `validationPrompt`. The repo's `CLAUDE.md` routes every code change through dark-factory; this prompt is that artifact, so make the change in the working tree and leave git to the daemon.

Read these files before making changes:
- `pkg/metrics.go` — the `Metrics` interface, `NewMetrics`, and the four metrics to convert. This is the whole change surface.
- `pkg/kafka_producer_test.go` — the `metricValue(registry, familyName, labelValue)` helper (it reads `metric.GetGauge().GetValue()` today) and the three call sites that name the kafka family. This file must be updated in the same change.
- `pkg/pkg_suite_test.go` — the Ginkgo suite bootstrap in `package pkg_test` that a new spec file joins; do not add a second suite.
- `pkg/ratelimit-roundtripper.go` and `pkg/kafka-producer.go` — the only two callers of the `Metrics` interface (`.Inc()` only). They must not change.
- `pkg/factory/factory.go` — `CreateMetrics(registerer prometheus.Registerer)` is the single production call site of `NewMetrics`; `main.go` passes `prometheus.DefaultRegisterer`.

Key API facts (verified against the module cache, `github.com/prometheus/client_golang v1.24.1` — do not re-derive them):
- `prometheus.Counter` has both `Inc()` and `Add(float64)`, so every existing `.Inc()` call site and the `.Add(0)` pre-initialization loop compile unchanged after the type switch.
- `prometheus.NewCounterVec(prometheus.CounterOpts{...}, []string{"result"})` returns `*prometheus.CounterVec`; `WithLabelValues(...)` returns a `prometheus.Counter`.
- `Registry.Gather()` returns the registered fully-qualified name verbatim as `MetricFamily.GetName()` — client_golang does NOT append `_total` at gather time, so a name-suffix assertion over gathered families is a real guard, not a tautology.
- `MetricFamily.GetType()` returns a `dto.MetricType` from `github.com/prometheus/client_model/go`; for a counter it is `dto.MetricType_COUNTER`, which is exactly the type the text exposition prints as `# TYPE <name> counter`.
- `dto.Metric.GetGauge()` on a counter metric returns nil and `GetValue()` on that nil returns `0` — a stale gauge read therefore fails as `0 != 1`, never as a compile error. That silent failure mode is why step 4 below exists.

Rename safety (verified): no `sentry_proxy` metric reference exists outside this repo — a search across the consuming nuke repo returns zero matches — so the old series names have no consumers and no dashboard or alert rule can break.

Coding-plugin guides to follow (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-prometheus-metrics-guide.md` — the MUST rules driving this change: `go-prometheus/counter-total-suffix` (a `CounterOpts.Name` must end in `_total`), `go-prometheus/no-gauge-for-monotonic` (a metric that is only ever `.Inc()`d must be a counter), plus the ones this change must keep satisfied: `go-prometheus/counter-pre-initialization` (pre-initialize every known label value with `.Add(0)`) and `go-prometheus/help-string-quality` (non-empty, distinct, accurate `Help`).
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` and `/home/node/.claude/plugins/marketplaces/coding/docs/go-mocking-guide.md` — Ginkgo v2 + Gomega patterns.
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md` — `## Unreleased` placement (directly above the highest `## vX.Y.Z`) and the conventional prefixes.

Notes for the reviewer (not requirements):
- These metric rules are enforced by review (`go-metrics-assistant`), not by `make precommit` — nothing in this repo's lint or test config catches a missing `_total` or a gauge-used-as-counter. The new spec file is therefore the only mechanical guard against a regression, which is why it asserts both the type and the name.
- The in-progress spec `specs/in-progress/001-tee-sentry-alerts-to-kafka.md` deliberately chose a `GaugeVec` and recorded that deviation ("so a reviewer does not silently correct it"). This prompt supersedes that decision: the guide's MUST rules win. Do not edit `specs/` — dark-factory owns it. Operator follow-up (not agent work): three places in that spec still name the old series — AC6's evidence sentence, AC11's evidence sentence, and the Detection column of the "Broker unreachable" failure-mode row — and must be updated on the spec side, otherwise the spec's acceptance evidence and its incident-detection column point at a metric that no longer exists.
</context>

<requirements>
1. **Replace the four metric constructors in `pkg/metrics.go`.** Each of the four metrics is only ever incremented, so each becomes a counter; the complete exposed name moves into the `Name:` field (ending in `_total`) and the `Namespace:` / `Subsystem:` fields are dropped, matching the guide's `order_handle_total` example. The result must be exactly:

   ```go
   sentryAlertTotalCounter := prometheus.NewCounter(prometheus.CounterOpts{
   	Name: "sentry_proxy_alerts_total",
   	Help: "Counter for all sentryAlerts",
   })
   sentryAlertRejectCounter := prometheus.NewCounter(prometheus.CounterOpts{
   	Name: "sentry_proxy_alerts_rejected_total",
   	Help: "Counter for rejected sentryAlerts",
   })
   sentryAlertForwardCounter := prometheus.NewCounter(prometheus.CounterOpts{
   	Name: "sentry_proxy_alerts_forwarded_total",
   	Help: "Counter for forwarded sentryAlerts",
   })
   kafkaPublishCounter := prometheus.NewCounterVec(prometheus.CounterOpts{
   	Name: "sentry_proxy_kafka_publishes_total",
   	Help: "Counter for kafka publishes by result",
   }, []string{"result"})
   ```

   The four exposed series are exactly `sentry_proxy_alerts_total`, `sentry_proxy_alerts_rejected_total`, `sentry_proxy_alerts_forwarded_total` and `sentry_proxy_kafka_publishes_total{result="success|failure|dropped"}`. Keep the existing `Help` strings verbatim — they are already non-empty, distinct and accurate. Do not register the old names alongside the new ones as a compatibility alias.

2. **Keep the rest of `pkg/metrics.go` intact.** The `registerer.MustRegister(...)` call keeps all four metrics. The pre-initialization loop stays exactly as it is, so the three `result` series exist before the first publish (`Counter` has `Add(float64)`, so this still compiles):

   ```go
   for _, result := range []string{"success", "failure", "dropped"} {
   	kafkaPublishCounter.WithLabelValues(result).Add(0)
   }
   ```

   Update the four `metrics` struct fields to the new types — `prometheus.Counter` for the three alert metrics and `*prometheus.CounterVec` for the kafka metric. Leave the six `Metrics` methods, their names and their `.Inc()` bodies unchanged.

3. **Do not touch the interface or its consumers.** `SentryAlertTotalInc`, `SentryAlertRejectedInc`, `SentryAlertForwardInc`, `KafkaPublishSuccessInc`, `KafkaPublishFailureInc` and `KafkaPublishDroppedInc` keep their names and signatures. Do not hand-edit `mocks/metrics.go` — it is generated by `make generate` from the `//counterfeiter:generate` directive. `pkg/ratelimit-roundtripper.go` and `pkg/kafka-producer.go` must not change at all.

4. **Fix the existing metric reader in `pkg/kafka_producer_test.go`.** The `metricValue` helper reads the value through `metric.GetGauge().GetValue()`; once the metric is a counter that getter returns nil and its nil-safe `GetValue()` returns `0`, so the three specs asserting `1.0` would fail as `0 != 1` rather than at compile time. Change the helper to read the counter instead, and update the three call sites that name the family:

   ```go
   // in metricValue:
   return metric.GetCounter().GetValue()

   // call sites (failure / success / dropped):
   metricValue(registry, "sentry_proxy_kafka_publishes_total", "failure")
   ```

   `dto.Metric.GetCounter()` lives in `github.com/prometheus/client_model/go` and returns a `*Counter` whose `GetValue()` is the counter's value.

5. **Add `pkg/metrics_test.go`** — Ginkgo v2 + Gomega in `package pkg_test`, joining the suite bootstrapped in `pkg/pkg_suite_test.go` (do not add a suite file). It must exercise the real registry, never a mock: `registry := prometheus.NewRegistry()` followed by `pkg.NewMetrics(registry)`. Two specs:

   a. **Every exposed metric is a counter with a `_total` name.** Gather once and assert per family, then assert the exact expected name set — this is the boundary assertion (metric opts → registry → gathered family name) and the only mechanical guard for the naming rule:

   ```go
   families, err := registry.Gather()
   Expect(err).NotTo(HaveOccurred())
   names := make([]string, 0, len(families))
   for _, family := range families {
   	Expect(family.GetName()).To(HaveSuffix("_total"))
   	Expect(family.GetType()).To(Equal(dto.MetricType_COUNTER))
   	names = append(names, family.GetName())
   }
   Expect(names).To(ConsistOf(
   	"sentry_proxy_alerts_total",
   	"sentry_proxy_alerts_rejected_total",
   	"sentry_proxy_alerts_forwarded_total",
   	"sentry_proxy_kafka_publishes_total",
   ))
   ```

   Import `dto "github.com/prometheus/client_model/go"` for `dto.MetricType_COUNTER`. That module is already required (indirect, v0.6.3); `go mod tidy` — which `make ensure` runs inside `make precommit` — promotes it to a direct requirement, and that promotion is expected, not drift.

   b. **The three `result` series are pre-initialized.** Assert each of `success`, `failure` and `dropped` exists with value `0` immediately after construction, reusing the `metricValue` helper from `pkg/kafka_producer_test.go` (same `pkg_test` package — do not duplicate it): `metricValue(registry, "sentry_proxy_kafka_publishes_total", "success")` and the same for `"failure"` and `"dropped"` must each return `0.0`. The helper returns `-1` when a series is missing, so this fails loudly if the pre-initialization loop is ever dropped — which is exactly the `counter-pre-initialization` rule's failure mode.

   c. **Each alert counter increments through the real registry.** Call `SentryAlertTotalInc()`, `SentryAlertRejectedInc()` and `SentryAlertForwardInc()` once each, gather, and assert the single metric in each of the three alert families has `GetCounter().GetValue() == 1`. Do NOT reuse `metricValue` here — it only matches metrics carrying the `result` label and returns `-1` for a label-less family. This is the only coverage for the three plain `Counter.Inc()` paths; the kafka specs exercise the `CounterVec` child path, which is different client_golang code.

6. **Update `CHANGELOG.md`.** The file currently has no `## Unreleased` section. Insert one directly above `## v0.4.0` with a single bullet in the existing style and a conventional prefix:

   ```
   - fix: register the proxy metrics as counters and give them `_total` names (`sentry_proxy_alerts_total`, `sentry_proxy_alerts_rejected_total`, `sentry_proxy_alerts_forwarded_total`, `sentry_proxy_kafka_publishes_total`), so rate/increase queries are no longer computed over gauges
   ```

   Leave the preamble and every released section untouched.

7. **Self-check before finishing.** Re-run `<verification>` and confirm it passes, then walk the change: are all four exposed names ending in `_total`, is every one of them registered as a counter, is the `result` series still pre-initialized, do the updated `pkg/kafka_producer_test.go` specs still assert `1.0` after a real publish, and is the changelog entry under `## Unreleased`?
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass. `pkg/kafka_producer_test.go` is updated in this prompt — never deleted, skipped or weakened.
- Do NOT run `go mod vendor` — `vendor/` is a build-time artifact regenerated by `make buca`.
- No new `replace` or `exclude` directives in `go.mod`. The pre-existing `exclude (cloud.google.com/go v0.26.0)` stays untouched.
- Error handling follows project conventions: `errors.Wrap(ctx, err, "...")` from `github.com/bborbe/errors`, never `fmt.Errorf`. Tests use Ginkgo v2 + Gomega.
- The exposed metric names are exactly the four listed above. Do not keep the old names as aliases, do not leave a `_total`-suffixed gauge behind, and do not invent additional metrics.
- The rate limiter's budget arithmetic, the synthesized `429` response, the Kafka record contract, and every HTTP route keep their current behavior. This is a metric type-and-name change only.
- Do NOT rename the `Metrics` interface methods, and do NOT hand-edit `mocks/metrics.go` (generated by `make generate`).
- Do NOT edit anything under `specs/` — the in-progress spec's deliberate `GaugeVec` choice is superseded by this prompt, and dark-factory owns that directory.
- `CHANGELOG.md` must carry the entry under `## Unreleased` (`docs/dod.md`).
</constraints>

<verification>
Run `make precommit` — must pass (it runs ensure, format, generate, test, check and addlicense).

Then run the focused package tests:

```bash
go test -mod=mod ./pkg/... -count=1
```

Targeted checks (all container-executable):
- `test -f pkg/metrics_test.go` — the new spec file exists
- `! grep -q 'NewGauge' pkg/metrics.go` — no gauge constructor remains (absence is asserted with `! grep -q`; `grep -c` exits 1 on a zero count and would misreport)
- `grep -c 'CounterOpts' pkg/metrics.go` — prints `4`, one per metric
- `grep -cE '"sentry_proxy_alerts_total"|"sentry_proxy_alerts_rejected_total"|"sentry_proxy_alerts_forwarded_total"|"sentry_proxy_kafka_publishes_total"' pkg/metrics.go` — prints `4`, one per name literal
- `! grep -rq 'sentry_proxy_kafka_publish_counter\|sentry_proxy_total_counter\|sentry_proxy_reject_counter\|sentry_proxy_forward_counter' pkg/ main.go` — no reference to an old series name survives in the code
- `grep -n '## Unreleased' CHANGELOG.md` — the section exists above `## v0.4.0`
</verification>
