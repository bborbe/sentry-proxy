# Changelog

All notable changes to this project will be documented in this file.

Please choose versions by [Semantic Versioning](http://semver.org/).

## Unreleased

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
