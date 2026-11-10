---
status: draft
spec: [002-cluster-build-arm]
created: "2026-11-10T07:45:27Z"
branch: dark-factory/cluster-build-arm
---

<!--
Open questions for the auditor (not requirements):
- The spec says the cluster arm refuses a *defaulted* VERSION but does not say where that
  refusal lives. It lives in the Makefile (`check-cluster-version`) because only the
  Makefile knows whether VERSION was defaulted. REF validation stays in
  `hack/build-cluster.sh` per Desired Behavior 4, so `make -n` stays dry and works with
  `.git` masked.
- AC10 assigns the DNS-1123 Job-name rendering to prompt 2, but the name is a
  `$(shell ...)` Makefile expression. This prompt computes `JOB_NAME`; prompt 2 verifies
  the rendered name.
- The `## Unreleased` changelog entry is required by `docs/dod.md` (the repo's
  `validationPrompt`) but is not an Acceptance Criterion; it is included here.
- `make build BUILD_TARGET=sun ...` is an operator-rung command (real cluster / real
  docker). This prompt's in-container verification uses only `make -n` and the
  `check-cluster-version` guard target, never a real cluster build.
-->

# Add the BUILD_TARGET selector to `make build`

<summary>
- `make build` keeps today's behaviour exactly when no target is named: same `docker build`, same tag, same five build args, image in the local Docker store.
- A new `BUILD_TARGET` variable selects where the build runs: `local` is the default, any other value means "run this build as a Kubernetes Job".
- The repo's two local-only build guards (the HEAD-tag check and `go mod vendor`) run only for local builds; a cluster build does not compare the wrong tree.
- A cluster build refuses to start when the version tag was defaulted to the newest release, so a bare cluster build cannot silently overwrite a released image.
- The Job name is derived from the version tag and sanitized for Kubernetes, so an underscore-bearing tag becomes a valid Job name.
- `make upload` and `make clean` become harmless no-ops for a cluster target: the Job already pushed the image and there is no local image to remove.
- The host-side `buca` composition is unchanged (`build upload clean apply`).
- A changelog entry records the new selector.
- No runtime image change; this is build tooling only.
</summary>

<objective>
Give `make build` a `BUILD_TARGET` selector. With the default `local`, the target runs the existing `docker build` recipe unchanged. With any other value, the target delegates to `hack/build-cluster.sh`, which runs the build as a Kubernetes Job in that cluster. The two local-only guards and the host-side `upload` / `clean` steps are skipped for a cluster target, and a cluster build refuses a `VERSION` that came from the Makefile default.
</objective>

<context>
Read `CLAUDE.md` for project conventions and the dark-factory workflow (do not commit; the daemon handles git). Read `docs/dod.md` — it is this repo's `validationPrompt`.

Read these files before changing anything:
- `Makefile` — the whole file. The relevant parts are the `VERSION` default block near the top, the `build` target, the `check-version-tag` and `check-go-mod` guards, and the `upload` / `clean` targets.
- `Dockerfile` — declares only `BUILD_GIT_VERSION`, `BUILD_GIT_COMMIT` and `BUILD_DATE` as `ARG`s; the local recipe's extra `DOCKER_REGISTRY` / `BRANCH` args are harmless and must stay.
- `CHANGELOG.md` — the newest heading is `## v0.5.5`; there is no `## Unreleased` section yet.

Read the spec at `specs/in-progress/002-cluster-build-arm.md` for the full contract. Do NOT edit anything under `specs/`.

Coding-plugin guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-makefile-commands.md` — the canonical `build` / `check-go-mod` / `check-version-tag` shapes.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-build-args-guide.md` — the three build stamps (`BUILD_GIT_VERSION`, `BUILD_GIT_COMMIT`, `BUILD_DATE`) and the `$$(...)` escaping rule inside make recipes.
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md` — `## Unreleased` placement and conventional prefixes.

Prompts 2 and 3 create the `hack/` files this prompt's cluster recipe calls. That directory does not exist yet; `make -n` does not execute the recipe, so the missing script is expected in this prompt.
</context>

<requirements>
1. **Add the selector and Dockerfile variable at the top of `Makefile`.** Insert these two lines as the first lines of the file (before `DOCKER_REGISTRY`):

   ```make
   BUILD_TARGET ?= local
   BUILD_DOCKERFILE ?= Dockerfile
   ```

   `BUILD_DOCKERFILE` stays a make variable because the failure-mode probe varies it (`BUILD_DOCKERFILE=Dockerfile.missing`). The cluster timeout must NOT become a make variable — it is a fixed constant in `hack/build-cluster.sh` (prompt 3).

2. **Track whether `VERSION` was defaulted.** Replace the existing block

   ```make
   ifeq ($(VERSION),)
   	VERSION := $(shell git describe --tags `git rev-list --tags --max-count=1`)
   endif
   ```

   with exactly

   ```make
   VERSION_DEFAULTED :=
   ifeq ($(VERSION),)
   	VERSION := $(shell git describe --tags `git rev-list --tags --max-count=1`)
   	VERSION_DEFAULTED := 1
   endif
   ```

   `VERSION` keeps its current definition and meaning. `VERSION_DEFAULTED` is non-empty only when the user did not pass `VERSION`; a command-line `VERSION=v0.5.5` leaves it empty even though the value equals a release tag.

3. **Compute the DNS-1123 Job name in the Makefile.** Immediately after the `VERSION` block, add exactly:

   ```make
   JOB_NAME := $(shell printf '%s' 'build-$(VERSION)' | tr '[:upper:]' '[:lower:]' | sed -e 's/[^a-z0-9.-]/-/g' | cut -c1-63 | sed -e 's/[-.]*$$//')
   ```

   This lowercases, replaces everything outside `[a-z0-9.-]` with `-`, truncates to 63 characters, and strips trailing `-` and `.`. Verified: `VERSION=v0.5.6-arm_test` renders `build-v0.5.6-arm-test`. The name is computed at parse time so `make -n` renders the sanitized name rather than the raw tag.

4. **Add the conditional prerequisite list.** Before the `build` target, add:

   ```make
   ifeq ($(BUILD_TARGET),local)
   BUILD_PREREQS := check-version-tag check-go-mod
   else
   BUILD_PREREQS := check-cluster-version
   endif
   ```

5. **Rewrite the `build` target as a conditional recipe.** Replace

   ```make
   .PHONY: build
   build: check-version-tag check-go-mod
   ```

   with

   ```make
   .PHONY: build
   build: $(BUILD_PREREQS)
   ```

   Then wrap the existing recipe in a conditional. The local branch must stay byte-for-byte identical to today's recipe — same `DOCKER_BUILDKIT=1`, `--no-cache`, `--rm=true`, `--platform=linux/amd64`, all five `--build-arg` lines, `-t $(DOCKER_REGISTRY)/$(IMAGE):$(VERSION)`, `-f Dockerfile .`. The complete target must be:

   ```make
   .PHONY: build
   build: $(BUILD_PREREQS)
   ifeq ($(BUILD_TARGET),local)
   	DOCKER_BUILDKIT=1 \
   	docker build \
   	--no-cache \
   	--rm=true \
   	--platform=linux/amd64 \
   	--build-arg DOCKER_REGISTRY=$(DOCKER_REGISTRY) \
   	--build-arg BRANCH=$(BRANCH) \
   	--build-arg BUILD_GIT_VERSION=$$(git describe --tags --always --dirty) \
   	--build-arg BUILD_GIT_COMMIT=$$(git rev-parse --short HEAD) \
   	--build-arg BUILD_DATE=$$(date -u +%Y-%m-%dT%H:%M:%SZ) \
   	-t $(DOCKER_REGISTRY)/$(IMAGE):$(VERSION) \
   	-f Dockerfile .
   else
   	./hack/build-cluster.sh \
   		--cluster "$(BUILD_TARGET)" \
   		--repo "https://github.com/bborbe/sentry-proxy.git" \
   		--ref "$(REF)" \
   		--tag "$(VERSION)" \
   		--job "$(JOB_NAME)" \
   		--dockerfile "$(BUILD_DOCKERFILE)" \
   		--image "$(DOCKER_REGISTRY)/$(IMAGE)" \
   		--push "true"
   endif
   ```

   Notes:
   - The `ifeq` / `else` / `endif` directives are at column 0; recipe lines are tab-indented. This pattern is valid GNU Make.
   - The repository URL is fixed at `https://github.com/bborbe/sentry-proxy.git`; do not derive it from `git remote` (unavailable in the build container).
   - Do not add an `ALLOW_DEFAULT_VERSION` escape hatch or any other opt-out. The spec's Non-goals forbid it.
   - Do not add a make variable for the cluster timeout.

6. **Wrap the two local-only guard definitions.** Put an `ifeq ($(BUILD_TARGET),local)` before `.PHONY: check-version-tag` and an `endif` after the end of the `check-go-mod` recipe. Keep both targets' message text, bodies and the `ALLOW_UNTAGGED_BUILD` escape exactly as they are today; the only change is the wrapper.

7. **Add the defaulted-VERSION refusal.** Add this target outside the local wrapper:

   ```make
   .PHONY: check-cluster-version
   check-cluster-version:
   	@if [ -n "$(VERSION_DEFAULTED)" ] || [ -z "$(VERSION)" ]; then \
   		echo "ERROR: BUILD_TARGET=$(BUILD_TARGET) requires an explicit VERSION=TAG; VERSION was not passed." >&2; \
   		exit 1; \
   	fi
   ```

   This runs before the cluster recipe (it is in `BUILD_PREREQS`), so no Job is created when it fires. It must name `VERSION` in stderr. It must not fire when the user passes `VERSION` explicitly, even if that value happens to equal a release tag.

8. **Wrap `upload` and `clean`.** Put an `ifeq ($(BUILD_TARGET),local)` around the existing `upload` and `clean` definitions and add no-op definitions in the `else` branch:

   ```make
   .PHONY: upload
   upload:
   	@echo "BUILD_TARGET=$(BUILD_TARGET): upload skipped (the cluster Job already pushed the image)"

   .PHONY: clean
   clean:
   	@echo "BUILD_TARGET=$(BUILD_TARGET): clean skipped (no local image)"
   ```

   The skip messages must not contain the substrings `docker push`, `docker rmi`, `docker builder prune` or `rm -rf vendor` — the acceptance probe greps for those absences. Both no-op targets must exit 0.

9. **Do not touch the `buca` target.** It keeps its current composition: `buca: build upload clean apply`. Do not change `apply`, `precommit`, `ensure`, `format`, `generate`, `test`, `lint`, `vet`, `vulncheck`, `osv-scanner`, `trivy`, `addlicense`, or `fix`.

10. **Add the changelog entry.** Insert a new `## Unreleased` section directly above `## v0.5.5` in `CHANGELOG.md`, with one bullet in the existing style:

    ```
    ## Unreleased

    - feat: add `BUILD_TARGET` to `make build` — `local` (the default) keeps the host `docker build`, while naming a cluster renders and runs a Kubernetes Job that clones `REF`, builds `linux/amd64` with BuildKit, and pushes the image; `make upload` and `make clean` skip their host-side work for a cluster target
    ```

    Leave the preamble and every released section untouched.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- Do NOT edit anything under `specs/`.
- `buca` keeps its current composition — `build upload clean apply`.
- `DOCKER_REGISTRY`, `IMAGE`, `BRANCH` and `VERSION` keep their current definitions and meanings. `VERSION` remains the image tag.
- The local recipe's `docker build` arguments must not change: `--no-cache`, `--rm=true`, `--platform=linux/amd64`, `-f Dockerfile .`, the `DOCKER_BUILDKIT=1` prefix, and all five `--build-arg` lines (`DOCKER_REGISTRY`, `BRANCH`, `BUILD_GIT_VERSION`, `BUILD_GIT_COMMIT`, `BUILD_DATE`).
- `check-version-tag` keeps its current message text and its `ALLOW_UNTAGGED_BUILD` escape; the change is the `ifeq ($(BUILD_TARGET),local)` wrapper only. `check-go-mod` gets the same wrapper.
- The cluster arm must refuse a *defaulted* `VERSION`. Do NOT add an escape hatch for it (`ALLOW_DEFAULT_VERSION` is explicitly a Non-goal).
- The cluster timeout is NOT a make variable. Do not add one.
- The local path must work with no kubeconfig and no cluster credentials present. Do not add any cluster lookup, `kubectl` call, or file existence check to the local branch.
- The repository URL is fixed: `https://github.com/bborbe/sentry-proxy.git`. Do not derive it from `git remote`.
- No new runtime dependency in the built image; the change is build tooling only.
- Do NOT run `go mod vendor` — `vendor/` is a build-time artifact regenerated by `make buca`.
- Existing tests must still pass. Run `make test` and `make lint` (NOT `make precommit`: its `check` target pulls trivy / osv-scanner / vulncheck over the network and asserts nothing about this change).
- `CHANGELOG.md` must carry the entry under `## Unreleased` (`docs/dod.md`).
</constraints>

<verification>
All commands below are container-executable (no cluster, no Docker daemon, no git; `.git` is masked).

```bash
make test
make lint
```

Selector and local recipe (the local invocation must be unchanged):

```bash
grep -c '^BUILD_TARGET ?= local' Makefile          # prints 1

make -n build VERSION=probe BRANCH=probe > /tmp/local-build.txt
grep -c -- '--build-arg DOCKER_REGISTRY=docker.io' /tmp/local-build.txt   # 1
grep -c -- '--build-arg BRANCH=probe' /tmp/local-build.txt                 # 1
grep -c -- '--build-arg BUILD_GIT_VERSION=' /tmp/local-build.txt           # 1
grep -c -- '--build-arg BUILD_GIT_COMMIT=' /tmp/local-build.txt            # 1
grep -c -- '--build-arg BUILD_DATE=' /tmp/local-build.txt                  # 1
grep -c -- '--platform=linux/amd64' /tmp/local-build.txt                   # 1
grep -c -- '--no-cache' /tmp/local-build.txt                               # 1
grep -c -- '--rm=true' /tmp/local-build.txt                                # 1
grep -c -- '-t docker.io/bborbe/sentry-proxy:probe' /tmp/local-build.txt   # 1
grep -c -- '-f Dockerfile .' /tmp/local-build.txt                          # 1
```

Cluster recipe dispatch and DNS-1123 name (dry run only):

```bash
make -n build BUILD_TARGET=sun REF=0000000000000000000000000000000000000000 VERSION=probe > /tmp/cluster-build.txt
grep -c 'hack/build-cluster.sh' /tmp/cluster-build.txt    # 1
! grep -q 'refusing to build' /tmp/cluster-build.txt       # no local refusal line

make -n build BUILD_TARGET=sun REF=0000000000000000000000000000000000000000 VERSION=v0.5.6-arm_test \
  | grep -c 'build-v0.5.6-arm-test'                        # >= 1
```

Guard and skip behaviour:

```bash
# The guard target refuses a defaulted VERSION and names VERSION; no cluster is touched.
out=$(make check-cluster-version BUILD_TARGET=sun 2>&1 || true)
printf '%s' "$out" | grep -q 'VERSION'

make -n upload BUILD_TARGET=sun VERSION=probe > /tmp/upload.txt
! grep -q 'docker push' /tmp/upload.txt

make -n clean BUILD_TARGET=sun VERSION=probe > /tmp/clean.txt
! grep -qE 'docker rmi|docker builder prune|rm -rf vendor' /tmp/clean.txt

grep -n '## Unreleased' CHANGELOG.md
```

The real local image build, the real cluster Job, and the two operator-rung refusal
probes are on the spec's Verification ladder, not in this prompt's container rung.
</verification>
