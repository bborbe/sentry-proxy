---
status: completed
summary: Replaced private build-info-metrics copy with shared github.com/bborbe/metrics library, added BUILD_GIT_VERSION field, wired three-arg SetBuildInfo call, and deleted pkg/metrics/
execution_id: sentry-proxy-exec-001-emit-version-label-on-build-info
dark-factory-version: v0.192.9
created: "2026-07-25T08:33:56Z"
queued: "2026-07-25T08:33:56Z"
started: "2026-07-25T08:57:51Z"
completed: "2026-07-25T08:59:13Z"
---

# Emit version label on build_info metric

<summary>
- The service reports which release it is running, not just when it was built
- Monitoring can tell a pinned, immutable release apart from an ad-hoc build
- The build-staleness alert stops false-firing on this service once it reports a real release version
- Build provenance now comes from the shared library every sibling service already uses, instead of a private copy
- The private, divergent copy of the build-metrics code is removed
- No change to what the service does at runtime beyond the extra metric labels
</summary>

<objective>
Report the running release version as a label on the build provenance metric, so fleet-wide monitoring can distinguish immutable pinned releases from ad-hoc builds. Today this service emits an unlabelled build timestamp from a private copy of the metrics code, which makes it indistinguishable from an unversioned build. Replace that private copy with the shared `github.com/bborbe/metrics` library that sibling services already use.
</objective>

<context>
Read `CLAUDE.md` for project conventions (Interface → Constructor → Struct → Method, `github.com/bborbe/errors` wrapping, `github.com/bborbe/time` over stdlib `time`).

Read `main.go` — find the `application` struct and its `Run` method. Note the existing `BuildGitCommit` and `BuildDate` fields and their struct-tag formatting.

Read `pkg/metrics/build-info-metrics.go` — this is the private copy being removed. It is imported in exactly one place (`main.go`, aliased `libmetrics`) and used on exactly one line.

The replacement library `github.com/bborbe/metrics` (use `v0.5.9` or later) exposes this interface — it is a drop-in superset of the local one:

```go
package metrics

// Emits a GaugeVec "build_info{version, commit}" whose value is the build
// timestamp in Unix seconds. Service identification comes from the Prometheus
// "job" label set by the scrape config, not a metric label.
type BuildInfoMetrics interface {
    // SetBuildInfo records build provenance for the current process.
    // Call exactly once at startup, after argument parsing.
    // Does nothing when buildDate is nil.
    SetBuildInfo(version, commit string, buildDate *libtime.DateTime)
}

func NewBuildInfoMetrics() BuildInfoMetrics
```

The `Dockerfile` and the `build` target in `Makefile` already define and pass `BUILD_GIT_VERSION` (via `git describe --tags --always --dirty`) all the way through to a container env var. Only the Go side is missing — do not change the Dockerfile or the Makefile.
</context>

<requirements>
1. In `main.go`, add a `BuildGitVersion` field to the `application` struct, immediately before the existing `BuildGitCommit` field. Write exactly this field, preserving the tag key order (`required`, `arg`, `env`, `usage`, `default`) used by every other field in the struct:

   ```go
   BuildGitVersion string `required:"false" arg:"build-git-version" env:"BUILD_GIT_VERSION" usage:"Build Git version (git describe --tags --always --dirty)" default:"dev"`
   ```

   `build-git-version` is one character longer than `build-git-commit`, so the struct's manually-aligned tag columns shift. Realign the tag columns across all fields in the `application` struct so they stay visually consistent — `gofmt` does not do this for you.

2. In `main.go`, change the import alias `libmetrics` to point at the shared library instead of the local package:
   - remove `libmetrics "github.com/bborbe/sentry-proxy/pkg/metrics"`
   - add `libmetrics "github.com/bborbe/metrics"`
   - keep it grouped with the other third-party `github.com/bborbe/*` imports, matching the file's existing import grouping

3. In `main.go`, in the `Run` method, update the single call site to pass all three values:
   - old: `libmetrics.NewBuildInfoMetrics().SetBuildInfo(a.BuildDate)`
   - new: `libmetrics.NewBuildInfoMetrics().SetBuildInfo(a.BuildGitVersion, a.BuildGitCommit, a.BuildDate)`

4. Delete the private copy entirely: remove the file `pkg/metrics/build-info-metrics.go`. It is the only file in `pkg/metrics/`, so remove that now-empty directory too.

   **Deletion hazard — read carefully.** This repo contains BOTH a directory `pkg/metrics/` (being deleted) and a separate, unrelated file `pkg/metrics.go` (which declares the `Metrics` interface consumed by `pkg/factory`). Delete ONLY the directory `pkg/metrics/` and its contents. Do NOT touch `pkg/metrics.go` — deleting it breaks the build. Never use a glob like `rm -rf pkg/metrics*`.

5. Add the new dependency in the correct order — write the import first (step 2), then run `go get github.com/bborbe/metrics@v0.5.9`, then `make ensure`. Do NOT run `go mod tidy` before the import exists (it would drop the dep), and do NOT run `go mod vendor` at any point.

6. In `CHANGELOG.md`, create a new `## Unreleased` section directly above the existing `## v0.1.1` heading (the file has no `## Unreleased` section yet — only released version headings). Add one bullet describing the change, matching the bullet style used under `## v0.1.1`.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- Do NOT modify `Dockerfile` or `Makefile` — `BUILD_GIT_VERSION` is already plumbed through both.
- Do NOT run `go mod vendor`. `vendor/` is a build-time artifact and is not committed in this repo.
- Do NOT change the metric name. It must remain `build_info` so it joins the fleet-wide metric and the existing alert.
- Do NOT add a `version` field to any other struct or introduce a new config source — the value comes solely from the `BUILD_GIT_VERSION` env var / `build-git-version` arg.
- Error handling must use `errors.Wrap(ctx, err, "...")` from `github.com/bborbe/errors` — never `fmt.Errorf`.
- Existing tests must still pass.
- The `default:"dev"` value matters: an unversioned local build must report `dev`, not an empty string.
- This change is exempt from the Definition-of-Done testing criterion: `main.go` has no test harness (the repo's only test files are Ginkgo suite bootstraps under `pkg/`), and the behavior being wired in is covered by the upstream library's own tests (`metrics_build_info_test.go`). The exemption is a property of this change, not something to work around.
- Do NOT add `BUILD_GIT_VERSION` to the `## Configuration` table in `README.md`. Build-metadata variables are deliberately excluded from it — `BUILD_GIT_COMMIT` and `BUILD_DATE` are both absent. Follow that precedent; README needs no change for this prompt.
</constraints>

<verification>
Run `make precommit` -- must pass.

Then confirm the private copy is gone and the shared library is wired in:

- `grep -r "sentry-proxy/pkg/metrics" --include="*.go" .` returns no matches outside `vendor/`
- `test ! -d pkg/metrics` — the whole directory must be gone, not just the file
- `grep -n "bborbe/metrics" go.mod | grep -v indirect` returns a match — the dep must be direct, not indirect
- `test -f pkg/metrics.go` still succeeds — the unrelated `Metrics` interface file must survive
- `grep -n "SetBuildInfo" main.go` shows the three-argument call passing version, commit, and build date
- `grep -n "BUILD_GIT_VERSION" main.go` shows the new struct field
</verification>
