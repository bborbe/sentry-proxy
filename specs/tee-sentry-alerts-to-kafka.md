---
status: draft
created: 2026-09-08
---

## Summary

- `sentry-proxy` forwards Sentry alerts upstream until a rate-limit budget is spent, then rejects the rest with a locally-synthesised `429`. Rejected alerts are logged and discarded.
- Discarded alerts are unrecoverable. When the org's Sentry quota was exhausted on 2026-09-06, every alert across four projects was lost for the remainder of the billing period.
- This change publishes every alert the proxy receives to a Kafka topic, on both the forward path and the reject path, before any forwarding decision is made.
- Kafka becomes the complete record; Sentry becomes a sampled, quota-limited human view of it.
- This is a precondition for consuming alerts from Kafka instead of polling the quota-bound Sentry API.

## Problem

The proxy's rate limiter reads the request body, logs it at `glog.V(2)`, and returns a synthesised `429` without forwarding. The alert is then gone — the log line is not a durable record, is not structured, and is invisible at default verbosity.

This is not a rare path. The deployed budgets across four proxy instances total 21,600 events per 30 days against a Sentry Developer-plan cap of 5,000, so rejection is the expected steady state rather than an exception. On 2026-09-06 the cap was reached and Sentry additionally began dropping accepted envelopes server-side (1,374 to spike protection, 407 over-quota, out of 7,000 ingested). Between the proxy's own rejections and Sentry's, an unknown fraction of alerts has no record anywhere.

There is currently no way to answer "what errors did we produce last Tuesday" for any period in which the budget was spent.

## Goal

Every HTTP request the proxy receives on the ingest path is published to a Kafka topic as a durable record, independent of whether it is subsequently forwarded to Sentry or rejected by the rate limiter. A consumer reading that topic from the beginning can reconstruct the complete alert stream, including alerts Sentry never saw.

## Assumptions

- The Kafka topic is provisioned by `strimzi-topic-controller` **before** AC10 is attempted. It does **not** exist today — verified 2026-09-08, `kubectlquant -n strimzi get kafkatopics` returns 2115 topics and zero match `sentry` or `alert`. Provisioning is a named prerequisite of the operator rung, not an assumption that quietly holds.
- The deployment manifest lives **outside this repo** — there is no `k8s/` directory here — so adding the two new env vars is a manifest change someone must make in the owning repo before the new image can start.
- `libhttp.NewProxy` rewrites scheme and host but **preserves the request path**. The round tripper sits on the outbound transport, so `project` is extracted after that rewrite; if the path were also rewritten, every record would key `unknown` and per-project partitioning would silently collapse.
- Pod clocks are not materially skewed. `received_at` comes from the pod's injected clock and AC10 compares it against the deploy time; a skewed clock makes a correct deploy look unverified. If AC10 fails on timestamps alone, fall back to "message count on the topic increased from zero".
- Brokers are reached over the plaintext NodePort bootstrap on the local network, matching every other nuke workload.
- The proxy runs on nuke-dev / nuke-prod while Kafka and its topics live on quant. That split is the current fleet topology, not an error.

## Non-goals

- Consuming the topic or analysing its contents. That is a separate change.
- Changing the rate-limit arithmetic, budgets, or the `REQUEST_LIMIT` / `REQUEST_DURATION` semantics.
- Parsing, validating, or transforming the Sentry envelope. The record is the bytes as received.
- Deduplication, grouping, or issue-formation. The topic carries events, not issues.
- Implementing topic provisioning. The topic is created through `strimzi-topic-controller` like every other topic in the fleet — but because it does not exist yet, creating it is a **prerequisite** of AC10 and is named as such in Verification, not silently assumed away.

## Acceptance Criteria

AC1-AC7, AC9 and AC11 are proven against a Counterfeiter-mocked producer and an `httptest` upstream. AC8 starts the process with missing configuration. Only AC10 observes a live topic.

- [ ] **AC1** — An alert the proxy forwards to Sentry is also published.
      *Evidence:* the fake producer records exactly one `Publish` call, payload `body` equal to the request body byte-for-byte and `outcome` == `forwarded`, on the configured topic (`develop-raw-sentry-alert-input` in dev, `master-raw-sentry-alert-input` in prod).
- [ ] **AC2** — An alert the rate limiter rejects with `429` is published.
      *Evidence:* the fake producer records exactly one `Publish` call, payload `body` equal to the request body **byte-for-byte**, and `outcome` == `rejected`. The body clause is load-bearing: `pkg/ratelimit-roundtripper.go:41-45` already drains `req.Body` before returning the synthesised 429, so without it an empty-bodied record passes. This is the criterion the current implementation fails.
- [ ] **AC3** — Exactly one message is published per received request, never two.
      *Evidence (negative):* after N requests, `PublishCallCount() == N`, not `2N`. A wrapper and an inner path must not both publish.
- [ ] **AC4** — A Kafka publish failure does not prevent the alert reaching Sentry.
      *Evidence (negative):* with the fake producer returning an error on every call, the HTTP response for a forwarded request is byte-identical (status, body, headers) to the same request with the producer succeeding.
- [ ] **AC5** — An upstream Sentry failure does not prevent the Kafka publish.
      *Evidence (negative):* with the `httptest` upstream returning a connection error, the fake producer still records exactly one `Publish` call, payload `body` equal to the request body **byte-for-byte**, and `outcome` == `upstream_error`. The transport has consumed the body by this point, so the equality clause is what proves it was buffered.
- [ ] **AC6** — Publish failures are observable without raising log verbosity.
      *Evidence:* `testutil.ToFloat64(vec.WithLabelValues("failure"))` increases by one after a failing publish — the selector is required, since `ToFloat64` on a bare `GaugeVec` errors with "collected more than one metric". The metric is `sentry_proxy_kafka_publish_counter` with a `result` label of `success` / `failure` / `dropped`, so a broker outage is distinguishable from a capacity drop, i.e. namespace `sentry_proxy`, subsystem `kafka_publish`, name `counter` — matching the existing `sentry_proxy_{total,reject,forward}_counter` shape in `pkg/metrics.go`. It is a `GaugeVec` for consistency with the three existing plain `Gauge`s in `pkg/metrics.go:19-36` rather than because a label demands it — `CounterVec` would be the semantically better type for a monotonic count, and this deviation is deliberate so a reviewer does not silently "correct" it. A `_total` suffix is avoided on a gauge.
- [ ] **AC7** — The published record is a durable contract a future consumer can parse.
      *Evidence:* the fake producer's recorded payload unmarshals into a `map[string]any` whose key set is exactly `{body, project, received_at, outcome}`. The payload is JSON with exactly the keys `body` (string, raw envelope bytes), `project` (string, the second path segment of the Sentry ingest path `/api/{project_id}/envelope/`, routed by `router.PathPrefix("/api")` in `main.go:78`; literal `unknown` when the path does not match), `received_at` (string, RFC3339, from the injected `libtime.CurrentTimeGetter` the round tripper already receives — never `time.Now()`), and `outcome` (string, one of `forwarded` / `rejected` / `upstream_error`). The message key is the `project` value, so all alerts for one project land on one partition and preserve per-project order. Schema and key are documented in `docs/kafka-alert-record.md` (created by prompt 1); `grep -c` over that file finds all four key names and all three `outcome` values, so the doc cannot be satisfied by an empty file.
- [ ] **AC8** — Broker list and topic are required configuration.
      *Evidence:* started without `KAFKA_BROKERS` or `KAFKA_TOPIC`, the process exits non-zero with stderr containing the missing flag name. It does not start with the tee silently disabled.
- [ ] **AC9** — No record is lost when the pod terminates.
      *Evidence:* with 10 records queued at cancellation time — enqueued but never yet passed to `Publish` — all 10 appear in `PublishCallCount()` after `Run` returns, within the 5s flush deadline. Asserting only that in-flight calls complete is vacuous under a synchronous implementation; the queued-record clause is what makes this criterion bite. This is the loss mode the whole change exists to prevent, so it is an AC and not only a failure mode.
- [ ] **AC11** — a stalled broker does not stall the request path.
      *Evidence:* with a fake producer whose `Publish` blocks 2s per call, a forwarded request's handler returns in under 100ms (measured). The channel capacity is a constructor parameter defaulting to 1000, so the drop clause is exercised with a small injected capacity rather than by driving 1001 requests, and once the channel's capacity is exceeded `sentry_proxy_kafka_publish_counter{result="dropped"}` increases by the number of dropped records. Without this, the non-blocking bounded-channel design in DB3/DB6 is prose an implementer can silently ignore — a synchronous inline `Publish` passes every other AC.
- [ ] **Post-Deploy (Rung-2):** **AC10** — the deployed proxy publishes real traffic to the configured topic.
      - `deploy_check:` `P=$(kubectlnukedev -n dev get pod -l app=sentry-proxy -o jsonpath='{.items[0].metadata.name}'); kubectlnukedev -n dev get --raw "/api/v1/namespaces/dev/pods/$P:9090/proxy/metrics" | grep -o 'commit="[^"]*"' | head -1 | cut -d'"' -f2`
      - `deploy_target:` `$(git rev-parse --short HEAD)`
      - The image **tag** cannot serve as the freshness signal: `Makefile:5-7` sets `VERSION` to the newest existing release tag regardless of what HEAD contains, so pre-fix and post-fix builds carry the same tag. The commit **is** shipped — `Dockerfile:12,19` bake `BUILD_GIT_COMMIT`, `main.go:47` feeds it to `libmetrics.NewBuildInfoMetrics().SetBuildInfo(...)`, and it surfaces as `build_info{version,commit}`. The image is `FROM scratch`, so `kubectl exec … curl` is impossible and port-forward is barred; the API-server pod-proxy path above is the working route and was confirmed live against the running dev pod (`build_info{commit="efbd0e9",version="v0.3.0"}`). Fallback: compare `{.status.containerStatuses[0].imageID}` against the digest printed by `make upload`. Never the version tag.
      - *Evidence:* consuming the configured topic yields at least one message whose `received_at` is later than the deploy. Unit tests do not satisfy this.

## Verification

### Container-executable

```bash
make precommit
go test -mod=mod ./... -run TestSuite -count=1
```

- AC1-AC7, AC9 and AC11 are unit/integration-testable with a Counterfeiter-mocked producer and an `httptest` upstream; AC8 starts the process. AC3, AC4 and AC5 in particular are assertions about call counts and error isolation, both reachable without a live broker.
- Assert on the mocked producer's invocation count and arguments, not on log output.

### Operator-executable

> ⚠️ **Do not run bare `make buca` in this repo from a feature branch.** `DOCKER_REGISTRY ?= docker.io` and, with `VERSION` unset, `Makefile:5-7` resolves it to the newest existing release tag regardless of HEAD. `upload` would push feature-branch code over the **public Docker Hub tag** `docker.io/bborbe/sentry-proxy:<latest release>`. `BRANCH=dev` does not protect against this — it is only a `--build-arg` (`Makefile:112`) and does not affect the tag. Separately, `apply` iterates `DIRS`, which is **empty in this repo**, so `buca` deploys nothing even when it succeeds.

Two prerequisites must be satisfied **before** the rollout, or dev breaks:

```bash
# PREREQ 1 — provision the topic. It does not exist (verified 2026-09-08: zero of 2115
# topics match sentry/alert). Create develop-raw-sentry-alert-input via strimzi-topic-controller
# the same way every other topic in the fleet is created.
kubectlquant -n strimzi get kafkatopics | grep raw-sentry-alert   # must return the topic first

# PREREQ 2 — add KAFKA_BROKERS and KAFKA_TOPIC to the deployment manifest and apply it.
# The manifest lives OUTSIDE this repo (there is no k8s/ dir here). The dev deployment
# currently sets only LISTEN, SENTRY_DSN, SENTRY_PROXY, REQUEST_LIMIT, REQUEST_DURATION.
# Because both new flags are required:"true" (AC8), rolling the new image without them
# exits 4 on start and takes dev Sentry forwarding down until someone edits the manifest.
```

Then build, push and roll:

```bash
# Pinned registry and version. Never the bare command.
cd ~/Documents/workspaces/sentry-proxy-kafka-tee && \
  DOCKER_REGISTRY=docker.dev.nuke.benjamin-borbe.de:443 \
  VERSION=v0.3.4-rc1 ALLOW_UNTAGGED_BUILD=1 make build upload

# The container is named `service`, NOT `sentry-proxy` — verified against the running deployment.
kubectlnukedev -n dev set image deploy/sentry-proxy \
  service=docker.dev.nuke.benjamin-borbe.de:443/bborbe/sentry-proxy:v0.3.4-rc1

# AC10: consume develop-raw-sentry-alert-input through the fleet's topic reader and find a
# message whose `received_at` is later than the deploy. Reading the proxy's own logs does NOT
# satisfy AC10 — that observes the producer, not the topic.
```

`ALLOW_UNTAGGED_BUILD=1` is required either way: `build: check-version-tag` (added v0.3.2) refuses to stamp a version onto a tree that is not that version's tag, which a feature branch never is.

> Repo defects noted, both out of scope: `buca` is defined twice in the Makefile (lines 101 and 166, later wins), and `make buca` defaults to pushing a public Docker Hub tag chosen with no reference to HEAD. Each deserves its own fix.

## Desired Behavior

1. A Kafka producer is constructed at startup from `KAFKA_BROKERS` and `KAFKA_TOPIC` and injected into the request path via the existing factory.
2. **Exactly one publish per received request, from a single call site, after the outcome is known.** The single call site is what makes AC3 unviolatable — not the ordering relative to the rate-limit decision. The outcome cannot be determined earlier: AC7 requires `outcome` to distinguish `upstream_error`, knowable only once the round trip returns.
3. **The publish is a non-blocking enqueue onto a bounded channel (default capacity 1000, injectable via the constructor for test), drained by a worker.** When the channel is full the record is dropped and counted as `result="dropped"`; the request path never waits. A synchronous publish is explicitly rejected — the call site sits after `RoundTrip` returns, so a synchronous send would stall the client response whenever the broker stalls.
4. The request body is buffered once per request **before any forwarding decision** and restored, so that both the publish and the upstream forward see the full bytes. This is not reject-path-specific: the transport consumes the body on the forward path too, and the publish happens after it.
5. A publish failure is counted and logged, never returned in a way that fails the HTTP request. An upstream failure is recorded in `outcome` rather than suppressing the publish.
6. The worker drains and flushes with a 5s timeout before `Run` returns — chosen to sit inside a default 30s Kubernetes termination grace period. `main.go` runs under `service.Run` with a cancellable context; a producer that drops its buffer on SIGTERM loses exactly the records this change exists to keep, with no counter recording the loss.

> Placement note (not a behavior): rate limiting lives in `pkg/ratelimit-roundtripper.go` as an `http.RoundTripper` on the **outbound** transport, wired by `factory.CreateProxyHandler` → `CreateRoundTripper` → `NewRateLimitRoundTripper`. It is not ingest middleware. The round tripper is the one place that sees all three outcomes, so the single call site belongs there.

## Constraints

- `NewRateLimitRoundTripper`'s allowance arithmetic must not change. Its integer-division defect (`pkg/ratelimit-roundtripper.go:35`) is tracked separately; fixing it here would confound two changes.
- The synthesised `429` response body and status code must not change — callers may match on them.
- The existing `SentryAlertTotal` / `SentryAlertForward` / `SentryAlertRejected` metrics keep their current meaning. New metrics are added; existing ones are never repurposed.
- **An unreachable broker never blocks forwarding, at any point in the process lifetime including startup.** Only missing or malformed *configuration* is fatal (AC8). This resolves the otherwise-contradictory demands of failing fast on a bad broker and serving through one — the config is checkable at startup, broker reachability is not, and treating the latter as fatal would let a Kafka outage take down Sentry forwarding.
- `/healthz`, `/readiness`, `/metrics` and `/setloglevel/{level}` keep their current behavior.
- **The Kafka client must be pure Go.** `Dockerfile:4` builds with `CGO_ENABLED=0` into `FROM scratch` (`Dockerfile:10`), so a librdkafka-backed client such as `confluent-kafka-go` breaks the build outright. Use the fleet standard `github.com/bborbe/kafka`; `go.mod` currently has no Kafka dependency at all.
- Adds no **new** `replace` / `exclude` directives to `go.mod`. The existing `exclude (cloud.google.com/go v0.26.0)` at `go.mod:51-53` is pre-existing and stays — `docs/dod.md:22` reads as a blanket prohibition, but removing it is not part of this change.
- The repo has **no behavioral tests today** — `pkg/pkg_suite_test.go` and `pkg/factory/factory_suite_test.go` are Ginkgo bootstraps with zero specs, so `go test ./...` is green on an unchanged tree and proves nothing on its own. The new tests must therefore also lock the current forward / reject / `429`-body behavior frozen above, not only the new tee.

## Failure Modes

| Trigger | Expected behavior | Detection | Recovery | Reversibility | Concurrency |
|---|---|---|---|---|---|
| `KAFKA_BROKERS` / `KAFKA_TOPIC` missing or malformed | Process exits non-zero at startup naming the missing flag | Pod `CrashLoopBackOff` with the flag name in logs | Fix the manifest and redeploy | Full — revert the manifest | n/a |
| Broker unreachable, at startup or later | Alerts continue to Sentry unaffected; publishes fail and are counted. **Not fatal** | `sentry_proxy_kafka_publish_counter{result="failure"}` rises | The failure series stops rising and the success series increases within one scrape interval; no proxy restart | Records during the outage are lost to Kafka, retained in Sentry if within quota | Producer must be safe for concurrent requests |
| Topic does not exist | Publish fails and is counted; forwarding unaffected | Failure metric at 100% of requests | Provision via `strimzi-topic-controller`; the failure series drops to zero within one scrape interval | Records published before provisioning are lost | n/a |
| Kafka slow / backpressure | The forward path does not block; the enqueue is non-blocking and the channel drops when full | Request latency unchanged under a stalled broker, `result="dropped"` series rising | `result="dropped"` stops rising and `result="success"` resumes within one scrape interval; no proxy restart | Records dropped while the broker stalled are lost | Publish must not hold a lock the request path needs |
| **Pod receives SIGTERM with records buffered** | The worker drains the channel and flushes within a bounded timeout before `Run` returns | Publish-success total equals request total across the pod's lifetime | None needed if flushed | Unflushed records are lost silently — no counter records them | Flush must complete before `Run` returns |
| **Producer buffer full under a burst** | The enqueue drops the record and counts it; the request path is not blocked | `result="dropped"` series rises while request latency stays flat | Burst subsides | Records dropped during the burst are lost | Bounded buffer, never unbounded growth |
| Request body consumed by the publish | Forward path sends an empty body upstream — silent alert corruption | AC4's byte-identical assertion | Reset the body before forwarding | Full | n/a |
| Both a wrapper and the inner path publish | Topic receives 2N messages; downstream double-counts | AC3 | Single call site | Full | n/a |
| Request body read fails while buffering | The request fails before any forwarding decision; no record is published and no `outcome` is invented for it | Existing error path in `ratelimit-roundtripper.go:42-45`, now moved to the top of the closure | Caller retries | Full | n/a |
| Sentry ingest path does not match `/api/{project_id}/…` | `project` is recorded as `unknown`; the record is still published | Messages keyed `unknown` appear on the topic | Fix the extraction rule if a real path shape was missed | Full — key rule is code, not data | Re-keying changes partitioning for existing consumers |
| Pod clock skew | `received_at` ordering disagrees with topic offsets; AC10's freshness comparison misreads | Consumer sees `received_at` non-monotonic against offset order | Order by topic offset rather than `received_at`; AC10 falls back to "message count increased from zero" | Timestamps already written are not correctable | Skew differs per pod |
| Record schema changes after consumers exist | Consumers break on unknown or missing keys | Consumer-side parse errors | Version the schema in `docs/kafka-alert-record.md` | Schema is a durable contract; changes are not freely reversible | n/a |

## Security / Abuse

- The proxy does not authenticate callers; it is reachable only in-cluster. Teeing to Kafka does not widen that surface, but it does make unauthenticated input durable — an in-cluster caller can now write arbitrary bytes to a Kafka topic via the proxy.
- The topic inherits whatever ACLs the Kafka cluster applies. It must not be world-readable if alert payloads can contain request data, environment values, or stack frames with user identifiers.
- `KAFKA_BROKERS` is a plaintext (`plain://`) NodePort connection on the local network, matching every other nuke workload. This change does not introduce TLS and does not regress it.
- Envelope bodies are stored verbatim. Anything Sentry would consider sensitive is now also in Kafka, with Kafka's retention rather than Sentry's.

## Suggested Decomposition

| # | Prompt focus | Covers DBs | Covers ACs | Depends on |
|---|---|---|---|---|
| 1 | Kafka producer component, bounded-channel worker, config (`KAFKA_BROKERS` / `KAFKA_TOPIC`), metrics, `docs/kafka-alert-record.md`, the `README.md` flag table, the `## Unreleased` CHANGELOG entry, producer unit tests | 1, 3, 6 | 6, 7, 8, 9 | — |
| 2 | Tee the request path: single call site, body buffering/restore, error isolation both directions | 2, 4, 5 | 1, 2, 3, 4, 5 | prompt 1 |

AC10 is post-deploy and belongs to neither prompt; it is verified by the operator after release.

Splitting this way keeps prompt 1 free of request-path risk and makes prompt 2's diff small enough to review directly against AC3-AC5, the three criteria most likely to be silently violated.

## Do-Nothing Option

Leave the proxy discarding rejected alerts and accept that alert history is lost whenever the budget or the Sentry quota is spent.

This is defensible only if the budgets are lowered enough that rejection is genuinely rare — but lowering them without a tee makes the loss *worse*, not better, because more alerts get rejected. That is the trap the current configuration is in: the budgets are set 4.3x above the plan cap precisely because tightening them costs visibility. The tee is what makes tightening them free, so doing nothing here also blocks the re-budget work.

The quota resets 2026-10-05. Doing nothing means the next flood repeats the outage.
