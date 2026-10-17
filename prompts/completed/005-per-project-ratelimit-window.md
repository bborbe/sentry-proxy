---
status: completed
summary: Added a per-project rate-limit window in front of the unchanged global window, keyed on extractProject, bounded by prune/sweep/hard-max, with a REQUEST_LIMIT_PER_PROJECT knob, a scope-labelled trip counter, and tests plus docs
execution_id: sentry-proxy-ratelimit-perproject-exec-005-per-project-ratelimit-window
dark-factory-version: v0.198.7
created: "2026-10-17T21:54:52Z"
queued: "2026-10-17T21:54:52Z"
started: "2026-10-17T21:55:45Z"
completed: "2026-10-17T22:02:08Z"
---

# Key the rate-limit window by project so one burst cannot starve the rest

<summary>
- One project's burst can no longer consume the entire forwarding window: each project is now capped at its own share of the same window
- The total forwarding budget is unchanged — a second cap is added, no allowance is created, so the shared Sentry quota stays protected exactly as before
- A project that keeps retrying after being rejected no longer blocks every other project's alerts from being forwarded
- The per-project share is configurable, so the strictness of the guarantee is a deployment decision rather than a code change
- Operators can now tell which cap rejected a request — the global budget or one project's share — instead of seeing a single undifferentiated rejection count
- Memory stays bounded even though project identifiers come from the request path and are therefore caller-controlled
- Requests whose project cannot be determined are treated as their own project rather than being exempted, so a malformed path cannot be used to escape the cap
- The existing counters, alert expressions and rejection response are untouched
- Behaviour, configuration and the project instructions are covered by tests and documentation
</summary>

<objective>
Make the rate limiter fair: keep the existing global sliding window exactly as it is, and add a second per-project window in front of it so that no single project can occupy the whole budget. The total allowance must not rise — the per-project window may only ever reject requests the global window would have accepted, never admit ones it would have refused.
</objective>

<context>
Read `CLAUDE.md` for project conventions (Interface → Constructor → Struct → Method, `errors.Wrap(ctx, err, "...")` from `github.com/bborbe/errors`, `github.com/bborbe/time` over stdlib `time`, Ginkgo v2 + Gomega testing, counterfeiter mocks). Read `docs/dod.md` — it is this project's validation prompt and is applied after your change.

Read `docs/ratelimit-per-project-design.md` — **the design for this change**. It states the defect, the constraint, the two-level admission rule, the allocation knob, the state bound, the `unknown` handling, the observability requirement and the rejected alternatives. Implement the design; do not re-derive it.

Read `pkg/ratelimit-roundtripper.go` — the file being changed. The anchor is `NewRateLimitRoundTripper`; it closes over one `var timestamps []time.Time` guarded by `var mux sync.Mutex`, and the admission test `if len(timestamps) >= requestLimit` never references `project` (which `extractProject(req.URL.Path)` produces at the top of the handler and which is currently used only for the `producer.Publish` call). The prune-check-append sequence is already correctly held under a single `mux.Lock()` — preserve that property and extend the same critical section to the per-project state; do not add a second mutex.

Read `pkg/project.go` — `extractProject(path string) string` returns the second path segment of `/api/{project_id}/…`, or the literal `"unknown"` when the path does not match. It needs no change.

Read `pkg/metrics.go` — the `Metrics` interface (`SentryAlertTotalInc`, `SentryAlertRejectedInc`, `SentryAlertForwardInc`, `KafkaPublish{Success,Failure,Dropped}Inc`) and `NewMetrics`, whose counters are registered on the passed `prometheus.Registerer`. The counterfeiter directive is `//counterfeiter:generate -o ../mocks/metrics.go --fake-name Metrics . Metrics`; `mocks/metrics.go` is generated and must not be hand-edited.

Read `main.go` — the `application` struct declares the env-backed flags via struct tags (`RequestLimit int` with `env:"REQUEST_LIMIT"`, `RequestDuration time.Duration` with `env:"REQUEST_DURATION"`), and `createHTTPServer` passes `a.RequestLimit` / `a.RequestDuration` to `factory.CreateProxyHandler`.

Read `pkg/factory/factory.go` — `CreateRoundTripper` and `CreateProxyHandler` are the wiring functions that thread the limits to `pkg.NewRateLimitRoundTripper`.

Pattern references (read before writing):
- `pkg/metrics.go` for the counter construction style (`prometheus.NewCounter` / `NewCounterVec` + `registerer.MustRegister`) and the pre-initialisation of label values
- `pkg/kafka-producer.go` for a bounded queue that drops on overflow, with a doc comment stating why the bound exists — and for the comment style used to justify a bound
- `pkg/ratelimit-roundtripper_test.go` for the existing Ginkgo v2 + Gomega test shape in `package pkg_test` — `libtime.NewCurrentTime()` + `currentTime.SetNow(...)` for clock control — advance the window by moving the clock, never by sleeping — `mocks.Metrics{}` for the fake, and a `libhttp.RoundTripperFunc` upstream stub
- `CHANGELOG.md` for bullet style (`- fix: …` / `- feat: …`, lowercase prefix). There is currently **no** `## Unreleased` section.

Coding-plugin guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — Ginkgo v2 + Gomega structure
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-prometheus-metrics-guide.md` — counter/label conventions and pre-initialisation
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-concurrency-patterns.md` — lock discipline for the prune-check-append decision
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md` — sentinel-error construction and `errors.Wrap` usage (governs requirement 5)
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-validation-framework-guide.md` — extending an application's `Validate` method (governs requirement 5)
</context>

<requirements>
1. In `pkg/ratelimit-roundtripper.go`, extend `NewRateLimitRoundTripper` with a new parameter for the per-project cap, placed immediately after `requestLimit`:

   ```go
   func NewRateLimitRoundTripper(
       currentTimeGetter libtime.CurrentTimeGetter,
       requestLimit int,
       requestLimitPerProject int,
       requestDuration time.Duration,
       metrics Metrics,
       producer Producer,
       roundTripper http.RoundTripper,
   ) http.RoundTripper
   ```

   Admission becomes a **two-level** decision evaluated inside the existing single `mux.Lock()` hold. Keep the global `timestamps` slice and its semantics exactly as they are; add per-project state alongside it:

   - Prune the global slice (existing rule: drop leading entries `ts` where `now.Sub(ts) >= requestDuration`).
   - Prune the requesting project's own slice with the same rule.
   - If `len(timestamps) >= requestLimit` → reject with scope `global`.
   - Else if the project's own slice length `>= requestLimitPerProject` → reject with scope `project`.
   - Else append `now` to **both** the global slice and the project's slice, then forward.

   Update the constructor's doc comment to state the two-level rule (global window first, then the per-project cap) — `docs/dod.md` requires doc comments on exported functions and the current comment describes only the global window.

   The global check stays first so that behaviour when the whole window is full is unchanged. A rejected request appends to neither slice — rejections never extend either window. The rejection path otherwise keeps its current shape (`SentryAlertRejectedInc`, `defer req.Body.Close()`, the `"reached request limit => 429"` body, `http.StatusTooManyRequests`) and the `producer.Publish(body, project, now, outcome)` call at the end is unchanged, with `outcome` still `"forwarded"` / `"rejected"` / `"upstream_error"`.

2. Extract the per-project bookkeeping into a small **unexported type in `pkg/`** with its own constructor and methods (prune, admit, and whatever the bound needs), so that its pruning, sweeping and size bound are unit-testable from an internal test in `package pkg` — the current closure-local slice cannot be reached from a test at all. `NewRateLimitRoundTripper` uses that type for the project state and keeps the global slice as it is.

   The type must be **unsynchronised**: it owns no mutex, and each method documents that the caller already holds `mux`. `NewRateLimitRoundTripper` must call its methods **inside the existing single `mux.Lock()` hold** that already covers the global prune-check-append — not in a second critical section, which would make the two decisions non-atomic and contradict the "do not add a second mutex" note in `<context>`. Its constructor takes the per-project limit, `requestDuration`, the sweep growth threshold and the hard maximum as parameters, so the internal test can drive the hard bound by passing a small maximum instead of admitting hundreds of projects.

   Bound the project state on three levels, all three required:
   - per-request prune of the requesting project's slice (step 1);
   - a sweep that deletes any project whose newest timestamp is older than `requestDuration` — run it on a growth threshold rather than every request, so the common path does not iterate the whole map;
   - a hard maximum number of tracked projects, evicting the least-recently-seen entry when the sweep does not bring the map back under it. **This bound is not optional**: the project identifier is taken from the request path and is therefore caller-controlled, so an unbounded map is a memory-exhaustion vector.

3. Treat the `"unknown"` key as an ordinary project: it gets its own window of `requestLimitPerProject` and is **not** exempt from the cap. Add a code comment stating why (exempting it would let a malformed path bypass the cap entirely).

4. In `pkg/metrics.go`, add `SentryAlertLimitTrippedInc(scope string)` to the `Metrics` interface and implement it with a new `prometheus.NewCounterVec` named `sentry_proxy_alerts_limit_tripped_total` with a single `scope` label, registered on the same registerer and **pre-initialised for both label values** `"global"` and `"project"` so the series exist before the first trip (follow the pre-initialisation already done for `sentry_proxy_kafka_publishes_total`). The three existing alert counters must keep their exact names, types and semantics — the `SentryProxyAlertsSaturated` and `SentryProxyAlertsDropping` alert expressions read them. Call the new method from the rejection path with the scope that actually tripped.

   Regenerate the counterfeiter fake for the interface (`make generate`, which `make precommit` also runs) — do not hand-edit `mocks/metrics.go`.

   Update `pkg/metrics_test.go`. Its `It("exposes every metric as a counter with a _total name")` asserts the registry contains **exactly** the four existing families via `Expect(names).To(ConsistOf(…))`, so the new family must be added to that list or `make test` — and therefore `make precommit` — fails. Add a case asserting both `scope` label series (`global`, `project`) are pre-initialised to `0`, mirroring the existing `sentry_proxy_kafka_publishes_total` pre-initialisation case.

5. In `main.go`, add to the `application` struct a field for the per-project cap, following the existing tag style: `env:"REQUEST_LIMIT_PER_PROJECT"`, `arg:"request-limit-per-project"`, `required:"false"`, `default:"1"`, and a `usage:` string. Extend `application.Validate` to reject a value that is less than `1` or greater than `RequestLimit`. Declare a package-level sentinel with stdlib `errors` aliased as `stderrors` — the `errname` linter is enabled in `.golangci.yml`, so a name without the `Err` prefix fails `make lint` — and wrap it:

   ```go
   var ErrRequestLimitPerProjectOutOfRange = stderrors.New("requestLimitPerProject must be >= 1 and <= requestLimit")
   // …
   return errors.Wrap(ctx, ErrRequestLimitPerProjectOutOfRange, "validate requestLimitPerProject failed")
   ``` Thread the value through `createHTTPServer` → `factory.CreateProxyHandler` → `factory.CreateRoundTripper` → `pkg.NewRateLimitRoundTripper`.

6. In `CHANGELOG.md`, create a `## Unreleased` section directly above the first existing `## vX.Y.Z` heading and add one bullet in the existing style describing the per-project cap, the unchanged total, and the new `scope`-labelled trip counter.

7. In `CLAUDE.md`: add a `REQUEST_LIMIT_PER_PROJECT` row to the `### Configuration` table, and extend the `### Rate limiting` section to describe the two-level admission rule — the global window unchanged, the per-project window as a second veto, the `unknown` treatment, and the trip-scope counter. Also update the `### Architecture` bullet for `ratelimit-roundtripper.go` and the closing sentence of the Kafka paragraph, which currently says the service's *"only in-memory state is the request counter"* — that is no longer true.

8. Add tests to `pkg/ratelimit-roundtripper_test.go` (external, `package pkg_test`) for the admission behaviour, and an internal test in `package pkg` for the project-state type from requirement 2. The external tests must cover, each as its own block, using `libtime.NewCurrentTime()` + `SetNow` only:
   - **fairness (the regression test for this change):** `requestLimit=2`, `requestLimitPerProject=1`. Project A forwards once; A's second request in the same window is rejected; project B's first request in the same window is **forwarded**. Assert the scope counts — this is the case the old code gets wrong (it forwards A's second and rejects B).
   - **total preserved:** `requestLimit=2`, `requestLimitPerProject=1`. A forwards, B forwards, and a third distinct project C is **rejected** with scope `global` — the aggregate is still 2 per window.
   - **independent expiry:** after advancing the clock by `requestDuration`, the project that was capped can forward again.
   - **unknown key is capped:** `requestLimit=2`, `requestLimitPerProject=1`; two requests on a path that does not match `/api/{id}/…` behave as one ordinary project under `requestLimitPerProject`, i.e. the second is rejected with scope `project`.
   - **a rejection does not extend the project window:** `requestLimit=2`, `requestLimitPerProject=1`. Project A forwards at `t0`; A is rejected at `t0+1m`; at `t0+requestDuration` A must be **forwarded** again. If the rejection had appended to A's own slice, the `t0+1m` entry would still be live and A would be rejected. This is the only case that pins *"a rejected request appends to neither slice"* for the **project** slice — an implementation that appends on per-project rejection passes every other case here.
   - **the existing four scenarios from `prompts/completed/002-fix-ratelimit-sliding-window.md`** (first-window allowance, sliding-window expiry, burst-then-quiet recovery, boundary-straddling burst) must still hold when `requestLimitPerProject` is set equal to `requestLimit` — keep them passing by updating their construction calls, not by deleting them. **All three** `NewRateLimitRoundTripper` call sites in this file must be updated so the package still compiles: the direct call in the AC2 block (~line 67) and the two helpers `newRateLimitRoundTripper` (~line 280) and `newSlidingWindowRoundTripper` (~line 410).

   The internal test must cover the project-state type directly: pruning after `requestDuration`, eviction of an aged-out project, and the hard bound being enforced when more distinct projects than the maximum are admitted. `pkg/pkg_suite_test.go` bootstraps `package pkg_test` only, so an internal `package pkg` test file needs its own Ginkgo bootstrap (`func TestPkg(t *testing.T) { RunSpecs(t, "pkg internal suite") }`) — Go permits both package names in one directory, and each needs its own `func Test…` entry point. This internal file is a **deliberate deviation** from `go-testing-guide.md`'s external-package convention, justified by the type being unexported — state that in a comment at the top of the file so the next reader does not "fix" it.

9. Before you finish, re-run every `<verification>` command and confirm each passes; then walk each acceptance criterion in requirement 8 against the actual test file and confirm each is implemented as specified, and confirm the `CHANGELOG.md` and `CLAUDE.md` updates are present.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- The **total** allowance must not rise. The per-project window is a second veto only: a request rejected by it must have been admissible by the global window. Never append to the global slice without also appending to the project slice, and never admit on a per-project check alone.
- Do NOT change the global window's prune rule, the 429 response shape or body, or the `producer.Publish` call and its `outcome` values.
- Do NOT rename, re-type or remove `sentry_proxy_alerts_total`, `sentry_proxy_alerts_rejected_total` or `sentry_proxy_alerts_forwarded_total` — two Prometheus alert expressions read them.
- Do NOT change `extractProject` or the Kafka record schema (`docs/kafka-alert-record.md`).
- Do NOT hand-edit `mocks/metrics.go`; regenerate it.
- Error handling must use `github.com/bborbe/errors` — never `fmt.Errorf`.
- Use `github.com/bborbe/time` in tests — never `time.Sleep`.
- Do NOT run `go mod vendor`; do NOT add dependencies.
- Do NOT change `bborbe/nuke` or any deployment manifest — that is a separate repository and a separate change.
- Existing tests must still pass.
</constraints>

<verification>
Run `make precommit` — must pass. This project's Makefile does not use the `ROOTDIR`/`Makefile.variables` pattern (verified: no `ROOTDIR` reference in `Makefile` or `tools.env`), so the bare form is correct here; the `ROOTDIR=/workspace` form applies to bborbe repos that do.

Then confirm the change with filesystem checks (all must succeed):

- `grep -n "requestLimitPerProject" pkg/ratelimit-roundtripper.go` — the per-project cap reaches the limiter
- `grep -n "SentryAlertLimitTrippedInc" pkg/metrics.go` — the trip-scope counter is declared and implemented
- `grep -n "sentry_proxy_alerts_limit_tripped_total" pkg/metrics.go` — the metric name is present
- `grep -n "REQUEST_LIMIT_PER_PROJECT" main.go` — the env knob is declared
- `grep -n "REQUEST_LIMIT_PER_PROJECT" CLAUDE.md` — the configuration table documents the knob
- `! grep -q "only in-memory state is the request counter" CLAUDE.md` — the stale single-state claim is gone
- `grep -n "^## Unreleased" CHANGELOG.md` — the Unreleased section exists
- `grep -q "requestLimitPerProject" pkg/ratelimit-roundtripper_test.go` — the external tests exercise the new cap
- `grep -cE "Describe\(|It\(" pkg/ratelimit-roundtripper_test.go` — must print at least `23` (the pre-change file already matches 19 lines: 10 `It(` blocks in the first `Describe` plus 4 `Describe(`/`It(` pairs in the sliding-window suite; the four new admission cases add 4 more `It(` blocks)
- `grep -rn "SentryAlertLimitTrippedInc" mocks/metrics.go` — the regenerated counterfeiter fake carries the new interface method
- `grep -rln "^package pkg$" pkg/*_test.go` — at least one test file is an **internal** test of `package pkg` (the project-state type is unexported, so an external `pkg_test` file cannot reach it); name the file as you see fit
</verification>
