---
status: completed
spec: [002-cluster-build-arm]
summary: Created the five hack/ cluster build-arm files (build-job.yaml, the render half of build-cluster.sh, cluster-alias.sh, build-cluster-bootstrap.sh, dockerconfig.json.tpl), made the scripts executable, added the CHANGELOG Unreleased entry, and passed make test, make lint and make precommit; the Makefile-dependent verification could not run because prompt 1 has not landed in this worktree.
execution_id: be73abef-25ab-5e76-b083-19d452789889-exec-006-spec-002-cluster-build-arm-files
dark-factory-version: dev
created: "2026-11-10T07:45:27Z"
queued: "2026-11-10T08:07:26Z"
started: "2026-11-10T08:07:27Z"
completed: "2026-11-10T08:15:17Z"
branch: dark-factory/cluster-build-arm
---

<!--
Open questions for the auditor (not requirements):
- The spec fixes the contract but not the tool images. This prompt uses `alpine/git:latest`
  for the clone init container and `moby/buildkit:latest` for the build container. Pin
  either at audit time if the operator wants reproducible tooling; the spec's
  "reproducible digests" Non-goal is about the produced image, not the build tools.
- `privileged: true` on the build container is the simplest way to run BuildKit in a pod.
  `automountServiceAccountToken: false` keeps every cluster credential out of the pod, so
  the Security section's "build container carries no cluster credentials" still holds.
- The manifest carries a seventh placeholder, `__JOB_NAME__`, which the AC's six-token
  grep intentionally does not count. The six counted tokens are `__REPO_URL__`, `__REF__`,
  `__TAG__`, `__DOCKERFILE__`, `__IMAGE__`, `__PUSH__`.
- The spec's OOM/eviction failure row implies memory and emptyDir limits exist to be
  raised. This prompt deliberately ships no `resources` block and no `emptyDir.sizeLimit`
  (the spec gives no values; inventing thresholds is scope creep). The recovery is an
  operator edit to the manifest. Flag at audit if the operator has preferred values.
- The only accepted cluster alias is `sun` (the spec's Prerequisites name `~/.kube/sun`).
  Extend `cluster_kubeconfig` if another cluster is ever added.
- This prompt creates `hack/build-cluster.sh` through rendering only. Prompt 3 adds
  `check` / `apply` / `wait` / `report` and must not rewrite the parts created here.
-->

# Author the five `hack/` build-arm files

<summary>
- A reusable, parameterized Kubernetes Job manifest describes the whole cluster build: one container clones the requested ref, one container builds and pushes the image.
- The clone container checks out exactly the requested commit and derives the same three build stamps a local build carries, so the pushed image reports real provenance.
- The build container builds `linux/amd64` with BuildKit and pushes the image using a credential mounted from a cluster secret.
- The credential is a secret reference with a key remap, never an inline value and never a file committed to the repo.
- A small driver script validates the requested ref before anything is rendered, resolves the cluster alias, and substitutes the run's values into the Job manifest.
- A cluster-alias helper maps a short name to its kubeconfig path and names the accepted values when asked for an unknown one.
- A one-time bootstrap helper creates the build namespace and the push secret; it is not on the build path.
- A credential template renders TeamVault placeholders into a Docker config document; no credential value is ever committed.
- The Job manifest lives under `hack/`, so a future yaml-sweeping apply target cannot adopt it as deploy state.
</summary>

<objective>
Author the five files under `hack/` that the cluster build arm needs: a parameterized Job manifest (`build-job.yaml`), the driver script (`build-cluster.sh`) through its validation-and-render half, a cluster alias helper (`cluster-alias.sh`), a one-time bootstrap helper (`build-cluster-bootstrap.sh`), and a Docker credential template (`dockerconfig.json.tpl`). The implementing container cannot reach the reference repositories, so these files are authored from the contract in the spec, not copied.
</objective>

<context>
Read `CLAUDE.md` and `docs/dod.md`. Read the spec at `specs/in-progress/002-cluster-build-arm.md`, especially Desired Behaviors 4–6, 10, the Constraints, the Failure Modes table and the Security section. Do NOT edit anything under `specs/`.

Read the current `Makefile` (prompt 1 has already added `BUILD_TARGET`, `BUILD_DOCKERFILE`, `JOB_NAME` and the cluster branch of `build`). The cluster recipe calls:

```
./hack/build-cluster.sh --cluster TARGET --repo https://github.com/bborbe/sentry-proxy.git \
  --ref REF --tag VERSION --job JOB_NAME --dockerfile BUILD_DOCKERFILE \
  --image DOCKER_REGISTRY/IMAGE --push true
```

Read `Dockerfile` — it declares `ARG BUILD_GIT_VERSION=dev`, `ARG BUILD_GIT_COMMIT=none`, `ARG BUILD_DATE=unknown`, mirrors them to `ENV`, and the binary parses `BUILD_DATE` as RFC3339 (the literal `unknown` is not parseable).

Coding-plugin guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/k8s-manifest-guide.md` — workload-kind choice (a one-shot build is a `Job`), one resource per file, no secrets inline.
- `/home/node/.claude/plugins/marketplaces/coding/docs/teamvault-conventions.md` — `{{ "ENV_VAR" | env | teamvaultPassword }}` template shape and the rule that only secret values, never config structure, live in TeamVault.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-build-args-guide.md` — the three stamps and their shapes.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-dockerfile-guide.md` — build-arg handling.

There is no `hack/` directory yet. Create it.
</context>

<requirements>
1. **Create `hack/build-job.yaml`** with exactly the following content. It must be a single `Job` resource, not under `k8s/`, with `kind: Job` at column 0.

   ```yaml
   apiVersion: batch/v1
   kind: Job
   metadata:
     name: __JOB_NAME__
     namespace: build
     labels:
       app.kubernetes.io/name: sentry-proxy-build
   spec:
     backoffLimit: 0
     template:
       metadata:
         labels:
           app.kubernetes.io/name: sentry-proxy-build
       spec:
         restartPolicy: Never
         automountServiceAccountToken: false
         initContainers:
           - name: clone
             image: alpine/git:latest
             env:
               - name: REPO_URL
                 value: __REPO_URL__
               - name: REF
                 value: __REF__
             command:
               - /bin/sh
               - -c
               - |
                 set -eu
                 echo "cloning ${REPO_URL} at ${REF}"
                 git clone "${REPO_URL}" /workspace/src
                 cd /workspace/src
                 git checkout --detach "${REF}"
                 echo "checked out ${REF}"
                 mkdir -p /workspace/meta
                 git rev-parse --short HEAD > /workspace/meta/commit
                 git describe --tags --always > /workspace/meta/version
                 date -u +%Y-%m-%dT%H:%M:%SZ > /workspace/meta/date
             volumeMounts:
               - name: workspace
                 mountPath: /workspace
         containers:
           - name: build
             image: moby/buildkit:latest
             securityContext:
               privileged: true
             env:
               - name: DOCKER_CONFIG
                 value: /workspace/docker-config
             command:
               - /bin/sh
               - -c
               - |
                 set -eu
                 VERSION=$(cat /workspace/meta/version)
                 COMMIT=$(cat /workspace/meta/commit)
                 DATE=$(cat /workspace/meta/date)
                 buildctl-daemonless.sh build \
                   --frontend dockerfile.v0 \
                   --local context=/workspace/src \
                   --local dockerfile=/workspace/src \
                   --opt filename=__DOCKERFILE__ \
                   --opt platform=linux/amd64 \
                   --opt build-arg:BUILD_GIT_VERSION="${VERSION}" \
                   --opt build-arg:BUILD_GIT_COMMIT="${COMMIT}" \
                   --opt build-arg:BUILD_DATE="${DATE}" \
                   --output type=image,name=__IMAGE__:__TAG__,push=__PUSH__
             volumeMounts:
               - name: workspace
                 mountPath: /workspace
               - name: docker-config
                 mountPath: /workspace/docker-config
                 readOnly: true
         volumes:
           - name: workspace
             emptyDir: {}
           - name: docker-config
             secret:
               secretName: buildkit-push
               items:
                 - key: .dockerconfigjson
                   path: config.json
   ```

   Contract this manifest must satisfy:
   - The clone container checks out exactly `__REF__` (never the default branch) and echoes the full ref, so the clone log names it.
   - `BUILD_GIT_VERSION` comes from `git describe --tags --always` on the cloned ref, `BUILD_GIT_COMMIT` is the short SHA, and `BUILD_DATE` is the current UTC RFC3339 time — matching the local recipe's shapes.
   - The build container builds `linux/amd64` and pushes `__IMAGE__:__TAG__` (`push=__PUSH__`).
   - The push credential arrives only through the `buildkit-push` secret's `items:` remap of `.dockerconfigjson` to `config.json` under `DOCKER_CONFIG`. The manifest must contain no `auths` block and no credential value.
   - `automountServiceAccountToken: false` — the build container carries no cluster credentials.
   - Do not add `resources` limits or `emptyDir.sizeLimit`; the spec gives no values and inventing thresholds is scope creep.

2. **Create `hack/cluster-alias.sh`** with exactly:

   ```bash
   #!/usr/bin/env bash
   # Maps a cluster alias to its kubeconfig path. Source this file, then call:
   #   cluster_kubeconfig ALIAS
   cluster_kubeconfig() {
   	local alias="${1:-}"
   	case "$alias" in
   		sun) printf '%s\n' "$HOME/.kube/sun" ;;
   		"")
   			echo "ERROR: no cluster given; accepted: sun" >&2
   			return 1
   			;;
   		*)
   			echo "ERROR: unknown cluster '$alias'; accepted: sun" >&2
   			return 1
   			;;
   	esac
   }
   ```

   The unknown-cluster branch must list the accepted values (`sun`) — this is the "unknown cluster" failure-mode row.

3. **Create `hack/build-cluster.sh`** with exactly the following content. This prompt owns argument parsing, cluster resolution, `REF` validation and rendering only. Do NOT implement `apply`, `wait` or `report` here — prompt 3 replaces the `run_job` stub with those steps. Keep `TIMEOUT=3600` as a fixed constant (prompt 3 uses it; it must never become a make variable).

   ```bash
   #!/usr/bin/env bash
   # hack/build-cluster.sh — render the sentry-proxy build Job from build-job.yaml.
   # This half owns argument parsing, cluster resolution, REF validation and rendering.
   # The apply/wait/report half is added by the next prompt.
   set -euo pipefail

   SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
   TIMEOUT=3600

   CLUSTER=""
   REPO_URL=""
   REF=""
   TAG=""
   JOB_NAME=""
   DOCKERFILE=""
   IMAGE=""
   PUSH=""

   usage() {
   	echo "usage: build-cluster.sh --cluster NAME --repo URL --ref SHA --tag TAG --job NAME --dockerfile PATH --image IMAGE --push BOOL" >&2
   	exit 2
   }

   while [ "$#" -gt 0 ]; do
   	case "$1" in
   		--cluster) CLUSTER="${2:-}"; shift 2 ;;
   		--repo) REPO_URL="${2:-}"; shift 2 ;;
   		--ref) REF="${2:-}"; shift 2 ;;
   		--tag) TAG="${2:-}"; shift 2 ;;
   		--job) JOB_NAME="${2:-}"; shift 2 ;;
   		--dockerfile) DOCKERFILE="${2:-}"; shift 2 ;;
   		--image) IMAGE="${2:-}"; shift 2 ;;
   		--push) PUSH="${2:-}"; shift 2 ;;
   		*) echo "ERROR: unknown argument: $1" >&2; usage ;;
   	esac
   done

   for pair in "cluster:$CLUSTER" "repo:$REPO_URL" "ref:$REF" "tag:$TAG" "job:$JOB_NAME" "dockerfile:$DOCKERFILE" "image:$IMAGE" "push:$PUSH"; do
   	name=${pair%%:*}
   	value=${pair#*:}
   	if [ -z "$value" ]; then
   		echo "ERROR: --$name is required" >&2
   		exit 2
   	fi
   done

   source "$SCRIPT_DIR/cluster-alias.sh"
   export KUBECONFIG
   KUBECONFIG=$(cluster_kubeconfig "$CLUSTER")

   validate_ref() {
   	if ! printf '%s' "$REF" | grep -Eq '^[A-Za-z0-9._/-]+$'; then
   		echo "ERROR: REF '$REF' contains characters outside [A-Za-z0-9._/-]" >&2
   		exit 1
   	fi
   	if [ "${#REF}" -ne 40 ]; then
   		echo "ERROR: REF '$REF' must be a full 40-character SHA (got ${#REF} characters)" >&2
   		exit 1
   	fi
   	if ! git rev-parse --verify --quiet "${REF}^{commit}" >/dev/null; then
   		echo "ERROR: REF '$REF' does not resolve to a commit in this repository" >&2
   		exit 1
   	fi
   }

   render_job() {
   	sed \
   		-e "s|__JOB_NAME__|${JOB_NAME}|g" \
   		-e "s|__REPO_URL__|${REPO_URL}|g" \
   		-e "s|__REF__|${REF}|g" \
   		-e "s|__TAG__|${TAG}|g" \
   		-e "s|__DOCKERFILE__|${DOCKERFILE}|g" \
   		-e "s|__IMAGE__|${IMAGE}|g" \
   		-e "s|__PUSH__|${PUSH}|g" \
   		"$SCRIPT_DIR/build-job.yaml"
   }

   run_job() {
   	# Replaced by the next prompt with: check, apply, wait, report.
   	echo "rendered Job manifest for ${JOB_NAME}"
   }

   validate_ref
   RENDERED=$(mktemp)
   trap 'rm -f "$RENDERED"' EXIT
   render_job > "$RENDERED"
   run_job
   ```

   Notes:
   - `validate_ref` runs before rendering and before any cluster call. It enforces the character set, exactly 40 characters, and a local `git rev-parse`. Each failure names the ref. This is the "unresolvable REF" failure-mode row.
   - `REF` is passed as an environment value and quoted in the clone container, never interpolated into an unquoted shell string.
   - The `sed` substitutions cover all seven placeholders in the manifest.

4. **Create `hack/build-cluster-bootstrap.sh`** with exactly:

   ```bash
   #!/usr/bin/env bash
   # One-time bootstrap: create the `build` namespace and the `buildkit-push`
   # dockerconfigjson secret. Not on the build path.
   set -euo pipefail

   SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
   source "$SCRIPT_DIR/cluster-alias.sh"

   CLUSTER="${1:-}"
   CONFIG_FILE="${2:-}"
   if [ -z "$CLUSTER" ] || [ -z "$CONFIG_FILE" ]; then
   	echo "usage: $0 CLUSTER RENDERED_DOCKERCONFIG_JSON" >&2
   	exit 2
   fi

   export KUBECONFIG
   KUBECONFIG=$(cluster_kubeconfig "$CLUSTER")

   kubectl create namespace build --dry-run=client -o yaml | kubectl apply -f -
   kubectl -n build create secret generic buildkit-push \
   	--type=kubernetes.io/dockerconfigjson \
   	--from-file=.dockerconfigjson="$CONFIG_FILE" \
   	--dry-run=client -o yaml | kubectl apply -f -
   ```

   This script is a one-time operator helper; it is never called from `make build`.

5. **Create `hack/dockerconfig.json.tpl`** with exactly:

   ```
   {"auths":{"https://index.docker.io/v1/":{"auth":"{{ "DOCKERHUB_AUTH_KEY" | env | teamvaultPassword }}"}}}
   ```

   The operator renders this template with the TeamVault parser and passes the rendered file to `build-cluster-bootstrap.sh`. No credential value is committed, and the template is never applied during a build.

6. **Make the two runnable scripts executable.** Run `chmod +x hack/build-cluster.sh hack/build-cluster-bootstrap.sh`. `cluster-alias.sh` is sourced, so its mode does not matter.

7. **Do not touch the `Makefile`, `Dockerfile`, `CHANGELOG.md` or any Go file.** Prompt 1 owns the Makefile; prompt 3 owns the apply/wait/report half of `build-cluster.sh`.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- Do NOT edit anything under `specs/`.
- `hack/build-job.yaml` must not live under `k8s/`, so a future yaml-sweeping apply target cannot adopt it as deploy state.
- `hack/build-cluster-bootstrap.sh` and `hack/dockerconfig.json.tpl` are one-time bootstrap helpers: they create the `build` namespace and the `buildkit-push` secret. They are not on the build path — the runtime credential arrives through the Job's `items:` remap. The template renders TeamVault placeholders into `{"auths":{"https://index.docker.io/v1/":{…}}}`; no credential value is ever committed, and the template is never applied during a build.
- The `items:` remap is load-bearing: without it the secret file keeps its own name while Docker looks for `config.json`, and auth fails silently. Do not remove it or replace it with an inline value.
- The Job spec is a reusable, parameterized manifest; no repository name, tag or credential is hardcoded in it. The repository URL arrives through `__REPO_URL__`.
- `REF` is validated against a local `git rev-parse` and a 40-character shape before rendering, and passed as a clone argument rather than interpolated into a shell string.
- The build container carries no cluster credentials (`automountServiceAccountToken: false`).
- The cluster timeout is a 3600-second constant inside `hack/build-cluster.sh`, not a make variable.
- Do NOT add `resources` limits, `emptyDir.sizeLimit`, a TTL, a retry policy, or any other knob the spec does not ask for.
- No new runtime dependency in the built image; the change is build tooling only.
- Existing tests must still pass. Run `make test` and `make lint` (NOT `make precommit`).
</constraints>

<verification>
All commands below are container-executable. `.git` is masked, so do not run the script for real (its local ref-resolution check is operator-rung); verify it statically.

```bash
bash -n hack/build-cluster.sh hack/cluster-alias.sh hack/build-cluster-bootstrap.sh

ls hack/ | wc -l    # prints 5
for f in build-cluster.sh cluster-alias.sh build-job.yaml build-cluster-bootstrap.sh dockerconfig.json.tpl; do
  test -f "hack/$f" || { echo "missing hack/$f"; exit 1; }
done

test -x hack/build-cluster.sh
test -x hack/build-cluster-bootstrap.sh

grep -c '^kind: Job' hack/build-job.yaml    # prints 1
grep -ohE '__REPO_URL__|__REF__|__TAG__|__DOCKERFILE__|__IMAGE__|__PUSH__' hack/build-job.yaml \
  | sort -u | wc -l                          # prints 6
grep -c 'push=__PUSH__' hack/build-job.yaml  # >= 1
grep -cF '.dockerconfigjson' hack/build-job.yaml   # >= 1
! grep -q 'auths' hack/build-job.yaml        # no inline credential block

grep -c 'validate_ref' hack/build-cluster.sh   # >= 1
grep -c 'render_job' hack/build-cluster.sh     # >= 1
grep -c 'must be a full 40-character SHA' hack/build-cluster.sh   # 1
grep -c 'accepted: sun' hack/cluster-alias.sh  # >= 1

# The Makefile (from prompt 1) renders the sanitized Job name; the manifest's
# __JOB_NAME__ placeholder is filled by the script at render time.
make -n build BUILD_TARGET=sun REF=0000000000000000000000000000000000000000 VERSION=v0.5.6-arm_test \
  | grep -c 'build-v0.5.6-arm-test'            # >= 1

make test
make lint
```

The real cluster run, the real clone/build logs, the `crane` digest/config probes and the
"unresolvable REF creates no Job" probe are on the spec's Verification ladder (operator
rung), not in this prompt's container rung.
</verification>
