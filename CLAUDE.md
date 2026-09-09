This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Dark Factory Workflow

**Never code directly.** All code changes go through the dark-factory pipeline.

### Complete Flow

**Spec-based (multi-prompt features):**

1. Create spec → `/dark-factory:create-spec`
2. Audit spec → `/dark-factory:audit-spec`
3. User confirms → `dark-factory spec approve <name>`
4. dark-factory auto-generates prompts from spec
5. Audit prompts → `/dark-factory:audit-prompt`
6. User confirms → `dark-factory prompt approve <name>`
7. Start daemon → `dark-factory daemon` (use Bash `run_in_background: true`)
8. dark-factory executes prompts automatically

**Standalone prompts (simple changes):**

1. Create prompt → `/dark-factory:create-prompt`
2. Audit prompt → `/dark-factory:audit-prompt`
3. User confirms → `dark-factory prompt approve <name>`
4. Start daemon → `dark-factory daemon` (use Bash `run_in_background: true`)
5. dark-factory executes prompt automatically

### Assess the change size

| Change | Action |
|--------|--------|
| Simple fix, config change, 1-2 files | Write a prompt → `/dark-factory:create-prompt` |
| Multi-prompt feature, unclear edges, shared interfaces | Write a spec first → `/dark-factory:create-spec` |

### Read the relevant guide before starting — every time, not from memory

- Writing a spec → read [[Dark Factory - Write Spec]] and [[Dark Factory Guide#Specs What Makes a Good Spec]]
- Writing prompts → read [[Dark Factory - Write Prompts]] and [[Dark Factory Guide#Prompts What Makes a Good Prompt]]
- Running prompts → read [[Dark Factory - Run Prompt]]

### Claude Code Commands

| Command | Purpose |
|---------|---------|
| `/dark-factory:create-spec` | Create a spec file interactively |
| `/dark-factory:create-prompt` | Create a prompt file from spec or task description |
| `/dark-factory:audit-spec` | Audit spec against preflight checklist |
| `/dark-factory:audit-prompt` | Audit prompt against Definition of Done |

### CLI Commands

| Command | Purpose |
|---------|---------|
| `dark-factory spec approve <name>` | Approve spec (inbox → queue, triggers prompt generation) |
| `dark-factory prompt approve <name>` | Approve prompt (inbox → queue) |
| `dark-factory daemon` | Start daemon (watches queue, executes prompts) |
| `dark-factory run` | One-shot mode (process all queued, then exit) |
| `dark-factory status` | Show combined status of prompts and specs |
| `dark-factory prompt list` | List all prompts with status |
| `dark-factory spec list` | List all specs with status |
| `dark-factory prompt retry` | Re-queue failed prompts for retry |

### Key rules

- Prompts go to **`prompts/`** (inbox) — never to `prompts/in-progress/` or `prompts/completed/`
- Specs go to **`specs/`** (inbox) — never to `specs/in-progress/` or `specs/completed/`
- Never number filenames — dark-factory assigns numbers on approve
- Never manually edit frontmatter status — use CLI commands above
- Always audit before approving (`/dark-factory:audit-prompt`, `/dark-factory:audit-spec`)
- **BLOCKING: Never run `dark-factory prompt approve`, `dark-factory spec approve`, or `dark-factory daemon` without explicit user confirmation.** Write the prompt/spec, then STOP and ask the user to approve. Do not assume approval from prior context or task momentum.
- **Before starting daemon** — run `dark-factory status` first to check if one is already running. Only start if not running.
- **Start daemon in background** — use Bash tool with `run_in_background: true` (not foreground, not detached with `&`)

## Project Overview

`sentry-proxy` is a rate-limiting reverse proxy that sits in front of Sentry's ingest
endpoint. Services do not talk to Sentry directly: they set `SENTRY_PROXY` alongside
`SENTRY_DSN`, and `github.com/bborbe/service` installs a proxy round tripper that
rewrites the ingest host to this service. The proxy forwards envelopes upstream until a
configured budget is spent, then answers `429` locally so the org's Sentry quota is not
exhausted by a single noisy service.

This matters operationally: the deployed Sentry plan has a hard monthly event cap, and
every proxy instance enforces its own budget independently. See `## Rate limiting` below.

### Architecture

- **`main.go`** — application struct + `Run()`. Builds a `mux` router with `/healthz`,
  `/readiness`, `/metrics` and `/setloglevel/{level}`, parses `SENTRY_DSN` to find the
  upstream ingest host, and proxies everything else to it.
- **`pkg/`**
  - `ratelimit-roundtripper.go` — the budget. `NewRateLimitRoundTripper` wraps the
    outbound transport, counts forwarded requests, and returns a synthesised `429`
    (never touching the network) once the allowance is spent.
  - `metrics.go` — Prometheus counters: total received, forwarded, rejected.
  - `factory/factory.go` — dependency injection wiring for the metrics, round tripper
    and proxy handler.
  - `factory/factory_loglevel-handler.go` — runtime log-level handler.

The service publishes every received alert to Kafka via `github.com/bborbe/kafka`;
its only in-memory state is the request counter.

### Configuration

| Env | Required | Meaning |
|-----|----------|---------|
| `SENTRY_DSN` | yes | Upstream Sentry DSN; its host is the proxy target |
| `SENTRY_PROXY` | no | Proxy for this service's *own* error reporting |
| `LISTEN` | yes | Listen address, e.g. `:9090` |
| `REQUEST_LIMIT` | yes | Requests allowed per `REQUEST_DURATION` |
| `REQUEST_DURATION` | yes | Budget window, e.g. `1h` |
| `KAFKA_BROKERS` | yes | Kafka bootstrap brokers (comma-separated; `plain://` assumed) |
| `KAFKA_TOPIC` | yes | Kafka topic to publish received alert records to |

### Rate limiting

The budget is a timestamp-pruned sliding window: at most `REQUEST_LIMIT` requests are
forwarded in any `REQUEST_DURATION` span, with no banking — a quiet period does not build
up allowance that can be spent later. Rejections never extend the window, are logged at
warn level and increment `SentryAlertRejected`, so a proxy dropping everything is
observable without debug verbosity.

Read `pkg/ratelimit-roundtripper.go` before changing anything here; the arithmetic is
subtler than the env-var names suggest.

### Dark Factory settings (`.dark-factory.yaml`)

`pr: false`, `workflow: direct` — dark-factory commits straight to the working branch and
does **not** open a PR. `autoRelease: false` — releases are cut by hand.
`validationPrompt: docs/dod.md`.

### Key Dependencies

- **HTTP**: `github.com/gorilla/mux` for routing, `github.com/bborbe/http` for round
  trippers, proxy and handler utilities
- **Benjamin Borbe's ecosystem**: `github.com/bborbe/service` (bootstrap),
  `github.com/bborbe/errors` (context-aware errors), `github.com/bborbe/time`
  (prefer over stdlib `time`), `github.com/bborbe/run` (concurrency),
  `github.com/bborbe/sentry` (this service's own reporting),
  `github.com/bborbe/metrics`, `github.com/bborbe/log`
- **Monitoring**: Prometheus via `/metrics`

## Development Commands

### Building and Testing
```bash
# Run full precommit pipeline (format, generate, test, check) - MANDATORY before commits
make precommit

# Individual commands
make ensure        # Tidy and verify go modules, clean vendor
make format        # Format code with goimports-reviser
make generate      # Generate code (removes mocks/avro, runs go generate)
make test          # Run tests with race detection and coverage
make check         # Run vet, errcheck, and vulnerability checks
make addlicense    # Add license headers to Go files
```

### Code Quality Checks
```bash
make vet           # Run go vet
make errcheck      # Check for unhandled errors (ignores Close/Write/Fprint)
make vulncheck     # Run vulnerability scanner
```

### Docker Operations
```bash
make build         # Build Docker image
make upload        # Push Docker image to registry
make clean         # Remove Docker image
```

### Running Tests
```bash
# Run all tests
go test -mod=mod ./...

# Run tests for specific package
go test -mod=mod ./pkg

# Run single test
go test -mod=mod -run TestSpecificFunction ./pkg
```

## Git Workflow - MANDATORY

**CRITICAL**: Follow these steps for ALL commits:

1. **Run pre-commit checks**: `make precommit` - ALWAYS run this first
2. **Update changelog**: Add changes to `CHANGELOG.md` with new version number - REQUIRED for all commits
3. **Stage and commit**: `git add . && git commit -m "commit message"`
4. **Tag version**: `git tag v1.x.x` (follow semantic versioning) - REQUIRED, use exact version from changelog

**Never commit without updating the changelog and tagging with the exact version from CHANGELOG.md**

## Development Guidelines

### Code Patterns
- Follow Interface → Constructor → Struct → Method pattern for all services
- Use `//counterfeiter:generate` comments for all interfaces for mock generation
- Always wrap errors with context: `errors.Wrap(ctx, err, "operation failed")`
- Use `github.com/bborbe/time` instead of standard `time` package
- Use `github.com/bborbe/collection.Ptr()` instead of custom pointer helper functions

### Time Handling
```go
import libtime "github.com/bborbe/time"

// Inject in constructor
func NewService(currentDateTime libtime.CurrentDateTime) Service {
    return &service{currentDateTime: currentDateTime}
}

// Use in methods
now := s.currentDateTime.Now() // No context parameter

// In tests
currentDateTime.SetNow(libtimetest.ParseDateTime("2023-12-25T00:00:00Z"))
```

### Testing Framework
- Uses **Ginkgo v2** with **Gomega** for BDD-style testing
- **Counterfeiter** for mock generation via `//go:generate` directives
- Test files follow pattern `*_test.go` with test suite file `pkg_suite_test.go`
- All tests run in UTC timezone for consistency
- Mock generation automated via `make generate`

### Execution Patterns
The service uses `github.com/bborbe/run` for concurrent execution:
- **Sequential**: Execute functions one after another
- **Parallel with error handling**:
  - `CancelOnFirstError`: Cancel remaining on first error
  - `CancelOnFirstFinish`: Cancel remaining on first completion
  - `All`: Execute all and aggregate errors

### Security Considerations

- The proxy forwards whatever envelope body it receives; it does not parse or validate
  Sentry payloads
- `SENTRY_DSN` is supplied via a Kubernetes secret, never inline in a manifest
- `/setloglevel/{level}` mutates runtime verbosity and is unauthenticated — it is only
  safe because the service is not exposed outside the cluster
