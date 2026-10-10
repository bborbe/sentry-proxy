# Changelog

All notable changes to this project will be documented in this file.

Please choose versions by [Semantic Versioning](http://semver.org/).

## Unreleased

- fix: bump `osv-scanner` to v2.6.0 so the Linux vulnerability gates stop failing. v2.3.1 pins `golang.org/x/tools` v0.38.0, whose SSA builder aborts with `unexpected expr: *ast.KeyValueExpr` on the promoted-field composite-literal key Go 1.27 permits in the Linux stdlib, so a repo on the old pin passes locally on darwin and fails only in Linux CI. Where `golang.org/x/net` is older than v0.60.0 it is raised to v0.60.0, clearing `GO-2026-6603/6610/6611/6612/6617`, which fail both `vulncheck` and `trivy`.

## v0.4.3

- chore: update github.com/IBM/sarama to v1.61.1, github.com/bborbe/errors to v1.6.1, github.com/bborbe/kafka to v1.26.0, github.com/bborbe/log to v1.7.1, github.com/bborbe/metrics to v0.6.3, github.com/bborbe/run to v1.11.0, github.com/bborbe/sentry to v1.10.2, github.com/bborbe/service to v1.10.14, github.com/bborbe/validation to v1.5.2, github.com/onsi/ginkgo/v2 to v2.33.0, github.com/onsi/gomega to v1.44.0, github.com/prometheus/client_golang to v1.25.0

## v0.4.2

- docs: Remove the dark-factory approval gate from this repo's `CLAUDE.md`. It contradicted the global `execution-phase-no-reask` carve-out and would have stopped every spec and prompt approval.

## v0.4.1

- fix: register the proxy metrics as counters and give them `_total` names (`sentry_proxy_alerts_total`, `sentry_proxy_alerts_rejected_total`, `sentry_proxy_alerts_forwarded_total`, `sentry_proxy_kafka_publishes_total`), so rate/increase queries are no longer computed over gauges

## v0.4.0

- feat: tee every received Sentry alert to a Kafka topic as a durable record (`docs/kafka-alert-record.md`) before the rate-limit or forwarding decision, keyed by project; new required config `KAFKA_BROKERS` / `KAFKA_TOPIC`

## v0.3.4

- fix: Enforce the request budget as a sliding window — at most `REQUEST_LIMIT` requests are forwarded in any `REQUEST_DURATION` span, timestamp-pruned per request from the injected clock instead of the cumulative uptime-based counter that truncated partial windows to zero; rejections are now logged at warn level instead of `glog.V(2)` only.

## v0.3.3

- docs: track `CLAUDE.md` in git instead of gitignoring it, and correct its contents. It described a Kafka Topic Reader service — wrong project, five `pkg/` files that do not exist, and IBM Sarama listed as a key dependency when there is no Kafka client in `go.mod`. Being gitignored, it was also absent from every feature worktree, so dark-factory containers ran here with no project instructions at all.

## v0.3.2

- fix: `make build` refuses to stamp a version onto a tree that is not that version's tag (`check-version-tag`, escape hatch `ALLOW_UNTAGGED_BUILD=1`). `VERSION` defaults to the newest tag repo-wide, so an operator-run build from an untagged or older tree silently republishes under the newest tag. The guard compares `git describe --exact-match HEAD` against `$(VERSION)` and exits non-zero on mismatch.

## v0.3.1

- chore: update Go to 1.27.0 and github.com/bborbe/errors to v1.6.0, github.com/bborbe/http to v1.26.25, github.com/bborbe/log to v1.6.25, github.com/bborbe/metrics to v0.6.0, github.com/bborbe/run to v1.10.1, github.com/bborbe/sentry to v1.10.0, github.com/bborbe/service to v1.10.10, github.com/bborbe/time to v1.27.11, github.com/onsi/gomega to v1.43.0

## v0.3.0

- feat: opt into `autoMerge.trivial` for mechanically-trivial update PRs

## v0.2.1

- update Go to 1.26.6 and update dependencies (fixes GO-2026-6179, GO-2026-6180, CVE-2026-56864, CVE-2026-56865)

## v0.2.0

- feat: Report running release version as a label on build_info metric, replacing private copy with shared github.com/bborbe/metrics library

## v0.1.1

- chore: Update Go dependencies to latest

## v0.1.0

- Initial release — extracted from `bborbe/quant` (`sentry/proxy`) into its own repo; module path `github.com/bborbe/sentry-proxy` (previously `github.com/bborbe/trading/sentry/proxy`), publish-only image build (`docker.io/bborbe/sentry-proxy`).
