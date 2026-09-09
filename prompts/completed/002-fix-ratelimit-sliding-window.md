---
status: completed
summary: Rewrote the rate limiter as a timestamp-pruned sliding window (at most REQUEST_LIMIT forwarded per REQUEST_DURATION, no cumulative counter, no uptime truncation), switched rejections to warn-level logging, added clock-driven Ginkgo tests covering all four scenarios plus the body-read error path, and updated CHANGELOG.md and CLAUDE.md
execution_id: sentry-proxy-exec-002-fix-ratelimit-sliding-window
dark-factory-version: dev
created: "2026-09-09T14:48:11Z"
queued: "2026-09-09T16:55:30Z"
started: "2026-09-09T16:55:43Z"
completed: "2026-09-09T16:59:08Z"
---

# Fix rate limiter: sliding window instead of cumulative counter

<summary>
- The proxy now enforces its configured budget as a true sliding window: at most `REQUEST_LIMIT` requests are forwarded in any `REQUEST_DURATION` span, instead of a counter that only grows since process start
- A restart no longer produces a guaranteed blind hour: partial windows get proportional allowance instead of zero, because the integer-division truncation is removed
- A quiet period no longer banks budget that can never be spent, and one burst no longer permanently exhausts the limit until enough wall-clock passes
- Dropped events are now logged at warn level, so a proxy rejecting everything no longer looks identical to a healthy quiet one
- Forward and reject metrics keep working unchanged, and the 429 response shape is unchanged
- The sliding-window behavior is covered by clock-driven tests (no sleeping), including window expiry, burst recovery, and a boundary-straddling burst
- The change log gains an Unreleased entry, and the project instructions no longer describe the old cumulative behavior
</summary>

<objective>
Make the rate limiter actually implement its configured budget — at most `REQUEST_LIMIT` requests per sliding `REQUEST_DURATION` window — instead of the current per-request uptime arithmetic that truncates partial windows to zero and accumulates a counter that never resets. The same fix makes rejections observable without debug verbosity.
</objective>

<context>
Read `CLAUDE.md` for project conventions (Interface → Constructor → Struct → Method, `errors.Wrap(ctx, err, "...")` from `github.com/bborbe/errors`, `github.com/bborbe/time` over stdlib `time`, Ginkgo v2 + Gomega testing, counterfeiter mocks).

Read `pkg/ratelimit-roundtripper.go` — the file being rewritten. The `NewRateLimitRoundTripper` constructor is the anchor; it captures `started := currentTimeGetter.Now()` once, computes `limit := uint64(float64(requestLimit) * float64(uptime/requestDuration))` per request (both operands are `time.Duration` int64, so `uptime/requestDuration` truncates BEFORE the float conversion — with `REQUEST_LIMIT=5, REQUEST_DURATION=1h` the allowance is 0 for the first hour after every restart), and never resets `requestCounter`. `pkg/factory/factory.go` calls this constructor with `(currentTime, requestLimit, requestDuration, metrics, libhttp.CreateDefaultRoundTripper())` — do not change that call.

Read `vendor/github.com/bborbe/time/time_current-time.go` — the injected clock API: `CurrentTimeGetter.Now()`, `CurrentTimeSetter.SetNow(now)`, `CurrentTime` interface (getter + setter), `NewCurrentTime()` returns a settable clock. Tests must use `libtime.NewCurrentTime()` + `SetNow(...)`, never sleep.

Read `pkg/metrics.go` for the `Metrics` interface (`SentryAlertTotalInc`, `SentryAlertRejectedInc`, `SentryAlertForwardInc`; counterfeiter fake at `mocks/metrics.go` with `...CallCount()` accessors) and `pkg/pkg_suite_test.go` for the existing Ginkgo bootstrap in `package pkg_test` — the new test file joins that suite automatically.

Pattern references (read before writing):
- `pkg/ratelimit-roundtripper.go` for the `libhttp.RoundTripperFunc(func(req *http.Request) (*http.Response, error) {...})` wrapping pattern and the existing `errors.Wrap(ctx, err, "read body failed")` rejection error
- `main.go` (`currentTime := libtime.NewCurrentTime()` at the top of `Run`) and `pkg/factory/factory.go` for the injected-clock convention
- `CHANGELOG.md` for bullet style (`- fix: ...`, `- feat: ...`, lowercase prefix) and the fact that there is currently no `## Unreleased` section

Coding-plugin guides (in-container paths — the repo has no Ginkgo test-body exemplar in-tree, so use these for shape):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — Ginkgo v2 + Gomega structure for the new test file
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-glog-guide.md` — glog level gating and the `glog.Warningf` warn-level convention
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md` — `## Unreleased` entry format
</context>

<requirements>
1. Rewrite the limiter core in `pkg/ratelimit-roundtripper.go`. Keep the exported signature exactly as it is:

   ```go
   func NewRateLimitRoundTripper(
       currentTimeGetter libtime.CurrentTimeGetter,
       requestLimit int,
       requestDuration time.Duration,
       metrics Metrics,
       roundTripper http.RoundTripper,
   ) http.RoundTripper
   ```

   The internal budget must be a timestamp-pruned sliding window (a slice of `time.Time` holding the timestamps of forwarded requests, oldest first), replacing both the `started` capture and the `requestCounter uint64` counter. Guard every access to the window with the existing `mux sync.Mutex`.

   Per-request behavior (inside the `libhttp.RoundTripperFunc`):
   - `metrics.SentryAlertTotalInc()` on every request, unchanged, before any lock.
   - Under a single `mux.Lock()` hold: `now := currentTimeGetter.Now()`; prune leading window entries `ts` where `now.Sub(ts) >= requestDuration` (an entry exactly one window old drops out, so a request arriving exactly at the boundary is allowed); then if `len(timestamps) >= requestLimit` take the rejection path, else append `now`, call `metrics.SentryAlertForwardInc()`, and unlock before forwarding. Unlock before the body read on the rejection path as well — only the prune-check-append decision needs the lock. The prune-check-append must be atomic under one lock hold — the current code checks the counter outside the lock; that race goes away with the rewrite.
   - Forward path (unchanged): `roundTripper.RoundTrip(req)` after unlocking.
   - Rejection path (unchanged shape): `metrics.SentryAlertRejectedInc()`, `defer req.Body.Close()`, `io.ReadAll(req.Body)`, on read error `return nil, errors.Wrap(ctx, err, "read body failed")`, then return `&http.Response{Body: io.NopCloser(bytes.NewBufferString("reached request limit => 429")), StatusCode: http.StatusTooManyRequests}, nil`. A rejected request must NOT append its timestamp — rejections never extend the window.
   - First-window behavior is deliberate: with an empty window the first `requestLimit` requests are forwarded immediately. Partial windows get proportional allowance — never an artificial zero. This is the fix for the blind-hour defect.

   Remove `started`, `uptime`, and the `limit` arithmetic entirely (the integer-division truncation). Keep the `glog.V(4)` debug lines in spirit but update them to the new variables.

2. Rejections must be visible without `-v=2`. Replace BOTH `glog.V(2).Infof` calls in the rejection branch (the `... => 429` decision line and the `sentry alert rejected: <body>` echo line) with `glog.Warningf`, keeping a message that reports the current window size and the configured limit on the decision line.

3. Create `pkg/ratelimit-roundtripper_test.go` in `package pkg_test` (the existing `pkg/pkg_suite_test.go` bootstrap picks it up — no new suite file). Use Ginkgo v2 + Gomega with dot-imports, following the conventions in `CLAUDE.md`'s Testing Framework section and `go-testing-guide.md`.

   Test harness (one shared setup per Describe): `currentTime := libtime.NewCurrentTime()`; `currentTime.SetNow(baseTime)` with `baseTime := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)`; `fakeMetrics := &mocks.Metrics{}`; an upstream `libhttp.RoundTripperFunc` that returns `&http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewBufferString("ok"))}, nil`; construct the round tripper with `pkg.NewRateLimitRoundTripper(currentTime, requestLimit, requestDuration, fakeMetrics, upstream)`. Build requests with `http.NewRequest(http.MethodPost, "https://example.com/api/123/envelope/", bytes.NewBufferString("sentry envelope body"))` — the body must be non-nil because the rejection path reads it. Assert status codes and the counterfeiter call counts (`SentryAlertForwardIncCallCount()`, `SentryAlertRejectedIncCallCount()`, `SentryAlertTotalIncCallCount()`).

   Cover all four scenarios (each as its own `Describe`/`It` block, advancing the clock only via `currentTime.SetNow(...)`):

   a. **First-minute allowance (proportional, not zero)** — `requestLimit=5`, `requestDuration=1h`, clock at baseTime. One request at baseTime must be FORWARDED (status 200, `SentryAlertForwardIncCallCount()==1`, `SentryAlertRejectedIncCallCount()==0`). This is the direct regression test for the blind-hour bug: the old code computed limit 0 and rejected it.

   b. **Sliding-window expiry** — `requestLimit=2`, `requestDuration=1h`, clock at baseTime. Send 2 (both forwarded, window full), then a 3rd at baseTime (429, `SentryAlertRejectedIncCallCount()==1`), then `SetNow(baseTime.Add(1*time.Hour))` and send one more — it must be FORWARDED because the baseTime entries are exactly one window old and are pruned. Assert the 429 response has `StatusCode == http.StatusTooManyRequests`.

   c. **Burst-then-quiet recovery** — `requestLimit=5`, `requestDuration=1h`, clock at baseTime. Send 5 (all forwarded), a 6th at baseTime (429), then `SetNow(baseTime.Add(1*time.Hour))` and send one more — it must be FORWARDED. Assert final counts: forwarded 6, rejected 1.

   d. **Boundary-straddling burst** — `requestLimit=2`, `requestDuration=1h`, clock at baseTime (12:00). Worked example to follow exactly: 12:00 send 2 (both forwarded); 12:59 send 2 (both 429 — the 12:00 entries are still in the window); 13:00 send 2 (both forwarded — the 12:00 entries are exactly one window old and are pruned); 13:30 send 2 (both 429 — the 13:00 entries are still in the window). Final counts: forwarded 4, rejected 4. The invariant the test proves: never more than `requestLimit` requests forwarded in any `requestDuration` span.

4. In `CHANGELOG.md`, create a new `## Unreleased` section directly above the existing `## v0.3.3` heading (the file has no `## Unreleased` yet — only released version headings). Add one bullet in the existing style (lowercase `- fix:` prefix), describing the sliding-window enforcement, the removal of the partial-window truncation, and the warn-level rejection logging.

5. Update the `### Rate limiting` section in `CLAUDE.md` — it currently describes the pre-fix behavior as the design and must not remain stale. Replace the descriptive paragraph with a description of the timestamp-pruned sliding-window budget (at most `REQUEST_LIMIT` forwarded per `REQUEST_DURATION`, no banking, rejections logged at warn level and counted by `SentryAlertRejected`). Keep the closing "Read `pkg/ratelimit-roundtripper.go` ..." sentence. No other section of `CLAUDE.md` changes.

6. Before you finish, re-run the `<verification>` commands and confirm every one passes; then walk each acceptance criterion — the four test scenarios (a)–(d) — against the actual test file and confirm each is implemented as specified, and confirm the changelog and `CLAUDE.md` updates are present.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- Do NOT change the exported signature of `NewRateLimitRoundTripper` — the call site in `pkg/factory/factory.go` and `main.go` stay untouched.
- Do NOT change the `Metrics` interface, the metric semantics, or the 429 response shape/body.
- Do NOT add config fields, flags, or env vars — no new knobs; the behavior is defined purely by the existing `REQUEST_LIMIT` / `REQUEST_DURATION`.
- Do NOT introduce a fixed-interval reset (e.g. a goroutine that zeroes the counter) — the window must be timestamp-pruned per request, purely from the injected clock.
- Error handling must use `errors.Wrap(ctx, err, "...")` from `github.com/bborbe/errors` — never `fmt.Errorf`.
- Use `github.com/bborbe/time` (`libtime.NewCurrentTime()` / `SetNow`) in tests — never `time.Sleep`.
- Do NOT run `go mod vendor`; do NOT add dependencies.
- Existing tests must still pass.
- No new `Describe`/`It` for out-of-scope behavior (no concurrency stress tests, no glog-output assertions — the warn-level log is verified by grep in `<verification>`, not by a test).
</constraints>

<verification>
Run `make precommit` — must pass.

Then confirm the fix with filesystem checks (all must succeed):

- `! grep -q "uptime" pkg/ratelimit-roundtripper.go` — the uptime-based allowance arithmetic (integer-division truncation) is gone
- `! grep -q "started :=" pkg/ratelimit-roundtripper.go` — the one-time `started` capture is gone
- `grep -n "glog.Warningf" pkg/ratelimit-roundtripper.go` — rejections are logged at warn level, not only at `glog.V(2)`
- `grep -n "requestDuration" pkg/ratelimit-roundtripper.go` — the window pruning references the configured duration
- `test -f pkg/ratelimit-roundtripper_test.go` — the new test file exists
- `grep -cE "Describe\(|It\(" pkg/ratelimit-roundtripper_test.go` — must print at least `4` (one block per required scenario)
- `grep -n "^## Unreleased" CHANGELOG.md` — the Unreleased section exists
- `grep -nE "sliding[- ]window" CLAUDE.md` — the project instructions describe the new behavior, not the old cumulative counter
</verification>
