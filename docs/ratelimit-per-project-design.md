# Per-project rate-limit window — design

Status: design, not implemented. Input to the dark-factory prompt/spec.
Targets the deployed tag **`v0.4.1`** (`pkg/ratelimit-roundtripper.go`).

## The defect

`NewRateLimitRoundTripper` closes over **one** `timestamps []time.Time` guarded by
one `sync.Mutex` — a single window shared by every project:

```go
var mux sync.Mutex
var timestamps []time.Time
...
project := extractProject(req.URL.Path)   // line 43 — extracted…
...
if len(timestamps) >= requestLimit {      // line 56 — …but never consulted
```

`project` is used only for the Kafka payload at line 78. The admission decision at
line 56 never references it, so the window is **global and unkeyed**.

Consequence, measured 2026-10-17 over the topic's readable range
(`master-raw-sentry-alert-input`, offsets 3800–4260, 2026-10-12T02:07Z →
2026-10-17T10:30Z): project `4505397462827008` produced **234 rejections inside the
single hour `2026-10-12T02`**, consuming the whole 2/hour window — and project
`4505975198449664` was rejected 20 times in `2026-10-16T16`. One project's burst
starves every other project. This is **fairness**, not capacity: the total loss is
unchanged by any fix here.

## The constraint

The total must **not** rise. The window protects a shared Sentry free-tier quota
(5,000 events/month), and the four instances are budgeted `1/2/1/1` = 5/h ≈
3,600/30d. A design that raises the aggregate is out of scope.

## Design — two-level admission control

Keep the existing global window **exactly as it is** and add a second, per-project
window in front of it. A request is admitted only when **both** have room.

```
admit(req):
  project := extractProject(req.URL.Path)          // unchanged
  now     := currentTimeGetter.Now()
  lock:
    prune(global,              now, requestDuration)   // unchanged
    prune(perProject[project], now, requestDuration)
    if len(global) >= requestLimit:                    // unchanged ceiling
        reject("global");  unlock; return 429
    if len(perProject[project]) >= perProjectLimit:
        reject("project"); unlock; return 429
    global              = append(global, now)          // unchanged
    perProject[project] = append(perProject[project], now)
    forward(); unlock
  producer.Publish(body, project, now, outcome)        // unchanged
```

**Why this preserves the total.** The global window and its check are untouched, so
the number of admissions per `REQUEST_DURATION` can never exceed `REQUEST_LIMIT`.
The per-project window only ever *removes* admissions — it is a second veto, never a
second grant.

**Why it fixes starvation.** With `perProjectLimit < requestLimit`, one project can
occupy at most `perProjectLimit` of the `requestLimit` slots, so at least
`requestLimit − perProjectLimit` slots stay reachable by other projects. On prod
(`REQUEST_LIMIT=2`, `REQUEST_LIMIT_PER_PROJECT=1`) a burst takes one slot and leaves
one — the `4505397462827008` storm can no longer consume both.

**Both prune-check-append decisions sit under one lock hold.** The current code
already does this correctly for the global slice (lines 47–71); the per-project map
must join the same critical section rather than take a second mutex, so the two
checks are evaluated against a consistent snapshot.

## The key

`extractProject` already exists (`pkg/project.go`): the second path segment of
`/api/{project_id}/…`, or the literal `"unknown"`. It is already correct and needs
no change — the defect is that its result is unused for admission. The Kafka message
key is already `project`, so per-project state and per-project Kafka ordering share
one identity.

## The allocation

A new env knob **`REQUEST_LIMIT_PER_PROJECT`**, default `1`, with the invariant

```
1 <= REQUEST_LIMIT_PER_PROJECT <= REQUEST_LIMIT
```

Deploy sets it to `1` on every instance. This is a *fixed per-project cap*, not a
re-division of the budget: the sum of what projects may take is deliberately allowed
to exceed `REQUEST_LIMIT`, because the global window still enforces the real total.
Stating it the other way round — "each project gets `REQUEST_LIMIT / N`" — would
require knowing `N`, and `N` changes as projects come and go.

**The trade-off, stated plainly.** When only one project is active it now gets
`REQUEST_LIMIT_PER_PROJECT` per window instead of `REQUEST_LIMIT` — on prod,
1/hour instead of 2/hour. That is the price of the guarantee, and it is inside the
constraint (the total is a ceiling, not a target). If that cost is judged too high,
the honest alternative is a higher `REQUEST_LIMIT_PER_PROJECT` with a correspondingly
weaker guarantee — the knob makes that a deploy-time choice, not a code change.

## Bounded state and eviction

Per-project windows are `map[string][]time.Time` under the **same** mutex as the
global slice — one lock hold covers prune-check-append for both, so the decision
stays atomic.

Eviction has three parts, and all three are required:

1. **Per-request prune** — the requesting project's slice is pruned with the existing
   rule (`now.Sub(ts) >= requestDuration`), identical to the global slice.
2. **Sweep** — a project whose newest timestamp is older than `requestDuration` holds
   no live entries; its key is deleted. Run the sweep when the map grows past a
   threshold (e.g. every 256 admitted requests) rather than on every request, so the
   common path stays O(1) in the number of projects.
3. **Hard bound** — `maxProjects` (e.g. 256). If the map still exceeds it after a
   sweep, evict the least-recently-seen project. **This is not optional:**
   `project` is taken from an unauthenticated request path, so an attacker can mint
   unlimited distinct ids and grow the map without bound. A fairness fix that
   introduces an unbounded allocation keyed on attacker-controlled input trades one
   outage for another.

## Absent or unknown project

`extractProject` returns `"unknown"` for any path that is not `/api/{id}/…`. Treat
`"unknown"` as an **ordinary key** — it gets its own window of
`REQUEST_LIMIT_PER_PROJECT`, exactly like a well-formed project id.

The alternative (exempting it from the per-project cap) is rejected: it would let a
caller bypass the cap entirely by sending a malformed path, which is precisely the
evasion this design exists to close. The residual cost is that all malformed-path
traffic shares one bucket and can starve *itself* — acceptable, and strictly better
than letting it starve everyone.

## At the aggregate cap

When the global window is full, the request is rejected with the existing
locally-synthesised `429` and `outcome = "rejected"` — **unchanged**, including the
`defer req.Body.Close()` and the warning log. The order of the two checks does not
change the verdict (both are vetoes); check the **global first** so the behaviour
when the window is full is byte-identical to today, and only then the per-project
cap. Record *which* limit tripped (below) — without that, the two are
indistinguishable in production.

## Observability

The three existing counters keep their names and semantics exactly — the
`SentryProxyAlertsSaturated` / `SentryProxyAlertsDropping` alert expressions read
them, and a rename would silently break both:

| Metric | Change |
|---|---|
| `sentry_proxy_alerts_total` | unchanged |
| `sentry_proxy_alerts_rejected_total` | unchanged |
| `sentry_proxy_alerts_forwarded_total` | unchanged |
| `sentry_proxy_kafka_publishes_total{result}` | unchanged |
| `sentry_proxy_alerts_limit_tripped_total{scope}` | **new** — `scope="global"` \| `"project"` |

The new counter is what makes the fix legible: `scope="project"` rising while
`scope="global"` stays flat is the signature of per-project budgeting doing the work.
Without it, a correct fix and a fix where the per-project cap never binds look
identical. Pre-initialise both label values to `0` so the series exists before the
first trip.

## What does not change

- `REQUEST_LIMIT` and `REQUEST_DURATION` — unchanged on all four instances
  (`kubectlnukedev -n dev`, `kubectlnukeprod -n prod`, `kubectlquant -n dev`,
  `kubectlquant -n prod`); the aggregate stays `1/2/1/1` = 5/h.
- The global window's prune rule, lock discipline and 429 body.
- The Kafka record schema (`docs/kafka-alert-record.md`) — no field changes.
- Both alert expressions.
- The exported `NewRateLimitRoundTripper` signature, except for the added
  `perProjectLimit` parameter.

## Design gap this exposes — the dev burst test cannot work

The task's verification plan runs the controlled-burst pair (A then B in one window)
on **nuke dev**, where `REQUEST_LIMIT=1`. With a global cap of 1 and a per-project
cap of 1, **the two are equal and the per-project window can never be the binding
constraint**: A is admitted, B is rejected — byte-identical to today's behaviour. The
dev instance physically cannot demonstrate the fix.

Options:

1. **Run the burst pair on nukeprod** (`REQUEST_LIMIT=2`, per-project 1) — A admitted,
   B admitted; the control run before the change reads A admitted / B rejected.
   **Recommended.** It is a read-only pair of synthetic requests against prod's
   proxy, and prod is where the starvation was measured.
2. Raise dev's `REQUEST_LIMIT` to 2 — **forbidden** (raises the total).
3. Assert on `sentry_proxy_alerts_limit_tripped_total{scope}` instead — weaker: it
   shows the cap fired, not that another project got through.

Until this is settled the task's SC2(a) demonstration has no valid venue, so it is
filed as a gap rather than assumed away.

## Alternatives rejected

- **Per-project limit equal to the global limit.** No fairness gain — the burst still
  fills the whole window.
- **Statically dividing the budget across known projects.** Needs a project registry
  that the proxy does not have, and a new project would have no allocation until
  someone edited config.
- **Dropping the global window and relying on per-project limits alone.** Raises the
  aggregate with the number of projects — forbidden by the free-tier constraint.
- **Shared state across replicas (Redis).** The proxy runs a single replica per
  instance, so per-pod state is sufficient; adding shared state would introduce a new
  failure mode for no benefit at the current replica count. Revisit only if the
  replica count changes.
