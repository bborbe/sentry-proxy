---
status: completed
spec: [002-cluster-build-arm]
summary: Completed the cluster build driver (existing-Job guard, apply with admission handling, 3600s wait, failure log report, interrupt trap) and wired the Makefile's BUILD_TARGET cluster recipe against the script's parser
execution_id: be73abef-25ab-5e76-b083-19d452789889-exec-007-spec-002-cluster-build-branch
dark-factory-version: dev
created: "2026-11-10T07:45:27Z"
queued: "2026-11-10T08:13:34Z"
started: "2026-11-10T08:15:18Z"
completed: "2026-11-10T08:18:28Z"
branch: dark-factory/cluster-build-arm
---

<!--
Open questions for the auditor (not requirements):
- The real cluster probes in the spec (kubectlsun, crane, the pod log greps) are the
  operator rung. They cannot run in the YOLO container (no cluster credentials, no
  kubectl, no crane). This prompt's container rung is `bash -n`, static greps and
  `make -n`; the operator rung stays on the spec's Verification ladder.
- `privileged: true` on the build container is inherited from prompt 2. If the cluster's
  pod-security policy rejects a privileged pod, that is the "Job rejected at admission"
  failure row: the script prints the Job's events and exits non-zero. The policy change
  is operator work, not a code change here.
-->

# Implement the cluster branch: validate, render, apply, wait, report

<summary>
- A cluster build now actually runs: the driver checks the target, applies the rendered Job, waits for it, and reports the outcome.
- The command blocks until the Job succeeds or fails; success exits zero, failure exits non-zero.
- A failed build prints the pod logs inline, so the cause is visible without a separate `kubectl` call.
- A Job that already exists is refused before anything is applied, with the delete command in the message.
- A Job that never reaches a terminal state fails after a fixed one-hour timeout and names that timeout.
- A Job rejected at admission prints the Job's events instead of a bare `kubectl apply` error.
- An interrupted wait leaves the Job running in the cluster and says so.
- Every row in the spec's Failure Modes table maps to a concrete branch in the driver.
- The Makefile's cluster branch and the script's argument parser are checked against each other, so no flag is silently dropped.
</summary>

<objective>
Complete the cluster branch of `make build`: extend `hack/build-cluster.sh` from its render-only half to the full driver — check for an existing Job, apply the rendered manifest, poll until the Job reaches a terminal state or the fixed 3600-second timeout expires, and report success or a failure with the pod log tail. Then verify the Makefile's cluster recipe passes every flag the script's parser accepts.
</objective>

<context>
Read `CLAUDE.md` and `docs/dod.md`. Read the spec at `specs/in-progress/002-cluster-build-arm.md`, especially Desired Behaviors 2, 3 and 4, the Failure Modes table and the Security section. Do NOT edit anything under `specs/`.

Read these files before changing anything:
- `hack/build-cluster.sh` — created by prompt 2. It parses `--cluster`, `--repo`, `--ref`, `--tag`, `--job`, `--dockerfile`, `--image`, `--push`; resolves the alias via `hack/cluster-alias.sh`; validates `REF`; renders `hack/build-job.yaml` into a temp file; and calls a `run_job` stub. This prompt replaces the stub and adds the apply/wait/report functions. Do not rewrite the argument parser, `validate_ref` or `render_job`.
- `hack/build-job.yaml` — the Job has `backoffLimit: 0`, `restartPolicy: Never`, and two containers named `clone` and `build`. The wait logic relies on the Job conditions and on those container names.
- `Makefile` — prompt 1 added the cluster branch of `build`, which invokes `hack/build-cluster.sh` with all eight flags. Cross-check it against the parser.

The container has no cluster credentials, no `kubectl` and no `.git`. Do not run the script for real here; verify it statically and with `make -n`.
</context>

<requirements>
1. **Add the existing-Job guard.** In `hack/build-cluster.sh`, add these functions above `run_job`:

   ```bash
   job_exists() {
   	kubectl -n build get job "$JOB_NAME" >/dev/null 2>&1
   }

   check_job_absent() {
   	if job_exists; then
   		echo "ERROR: Job '$JOB_NAME' already exists; delete it first: kubectl -n build delete job $JOB_NAME" >&2
   		exit 1
   	fi
   }
   ```

   This is the "previous Job with the same name exists" failure-mode row. It runs before any apply, so no partial Job is left.

2. **Add the apply step with admission-rejection handling.** Add:

   ```bash
   apply_job() {
   	if ! kubectl apply -f "$RENDERED"; then
   		echo "ERROR: Job '$JOB_NAME' was rejected by the cluster" >&2
   		kubectl -n build describe job "$JOB_NAME" >&2 || true
   		exit 1
   	fi
   }
   ```

   If the cluster is unreachable, `kubectl apply` prints the connection error and the script exits non-zero; no Job is left behind. If the Job object is admitted but a pod is rejected (pod-security, RBAC), the wait step fails and the report step prints the events.

3. **Add the wait loop with the fixed 3600-second timeout.** Add:

   ```bash
   job_conditions() {
   	kubectl -n build get job "$JOB_NAME" -o jsonpath='{.status.conditions[*].type}' 2>/dev/null || true
   }

   wait_for_job() {
   	local deadline=$((SECONDS + TIMEOUT))
   	local succeeded=""
   	while true; do
   		succeeded=$(kubectl -n build get job "$JOB_NAME" -o jsonpath='{.status.succeeded}' 2>/dev/null || true)
   		if [ "$succeeded" = "1" ]; then
   			return 0
   		fi
   		if job_conditions | grep -q 'Failed'; then
   			return 1
   		fi
   		if [ "$SECONDS" -ge "$deadline" ]; then
   			echo "ERROR: Job '$JOB_NAME' did not reach a terminal state within ${TIMEOUT} seconds" >&2
   			return 1
   		fi
   		sleep 5
   	done
   }
   ```

   `TIMEOUT` is the existing `TIMEOUT=3600` constant. Do NOT make it a make variable or an argument. The timeout branch must name the value it waited (the "Job exceeds the 3600-second timeout" failure-mode row). Polling `{.status.succeeded}` matches the operator probe `kubectl ... -o jsonpath='{.status.succeeded}'` returning `1`.

4. **Add the failure report.** Add:

   ```bash
   report_failure() {
   	echo "---- Job events ----" >&2
   	kubectl -n build describe job "$JOB_NAME" >&2 || true
   	echo "---- clone container logs ----" >&2
   	kubectl -n build logs "job/$JOB_NAME" -c clone --tail=200 >&2 || true
   	echo "---- build container logs ----" >&2
   	kubectl -n build logs "job/$JOB_NAME" -c build --tail=200 >&2 || true
   }
   ```

   This covers the registry-refused (401), registry-throttled (429), OOMKilled/Evicted and missing-Dockerfile rows: the pod's own log carries the cause, and the make target prints it inline. Do not add a second `kubectl` invocation to the Makefile — the report lives here.

5. **Replace the `run_job` stub.** The stub currently prints `rendered Job manifest for ...`. Replace the whole function with:

   ```bash
   run_job() {
   	check_job_absent
   	apply_job
   	if wait_for_job; then
   		echo "Job '$JOB_NAME' completed; image ${IMAGE}:${TAG} pushed"
   		return 0
   	fi
   	report_failure
   	exit 1
   }
   ```

   Success exits 0; failure exits non-zero after printing the logs. The apply/wait/report order must not change.

6. **Handle an interrupted wait.** After the `RENDERED=$(mktemp)` line (where the EXIT trap is set), add:

   ```bash
   trap 'echo "interrupted; Job ${JOB_NAME} continues in the cluster" >&2; exit 1' INT TERM
   ```

   The EXIT trap still removes only the temp file. The script must never delete the Job. This is the "make build interrupted mid-wait" failure-mode row.

7. **Cross-check the Makefile's cluster recipe against the script's parser.** Read the `else` branch of the `build` target in `Makefile`. Confirm it passes every flag the parser accepts (`--cluster`, `--repo`, `--ref`, `--tag`, `--job`, `--dockerfile`, `--image`, `--push`) and no flag the parser rejects. If prompt 1 and prompt 2 drifted, fix the Makefile here. Do not change the local recipe.

8. **Do not change the rendered manifest, `validate_ref`, `render_job`, the argument parser, `cluster-alias.sh`, the bootstrap script or the template.** Prompt 2 owns those. Do not add retries, a tunable timeout, a `--force` flag, a TTL, or an `ALLOW_DEFAULT_VERSION` escape — the spec's Non-goals forbid them.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- Do NOT edit anything under `specs/`.
- The cluster timeout is a 3600-second constant inside `hack/build-cluster.sh`; do not make it a make variable or a flag.
- Do NOT add an `ALLOW_DEFAULT_VERSION` escape hatch or any other opt-out.
- The make target exits 0 when the Job succeeded and non-zero when it failed, printing the Job's pod log tail in the failure case so the cause is visible without a separate `kubectl` call.
- `REF` validation stays in `hack/build-cluster.sh`, before rendering; do not move it into the Makefile (that would make `make -n` non-dry and break with `.git` masked).
- The build container carries no cluster credentials; do not add a service-account mount.
- No new runtime dependency in the built image; the change is build tooling only.
- Existing tests must still pass. Run `make test` and `make lint` (NOT `make precommit`).
</constraints>

<verification>
All commands below are container-executable. The real cluster run, the cluster Job probes,
the image digest/config probes and the missing-Dockerfile pod-log probe are operator-rung
and stay on the spec's Verification ladder.

```bash
bash -n hack/build-cluster.sh hack/cluster-alias.sh hack/build-cluster-bootstrap.sh

grep -c 'TIMEOUT=3600' hack/build-cluster.sh            # 1
grep -c 'kubectl apply' hack/build-cluster.sh           # >= 1
grep -c 'already exists' hack/build-cluster.sh          # >= 1
grep -c 'describe job' hack/build-cluster.sh            # >= 1
grep -c 'status.succeeded' hack/build-cluster.sh        # >= 1
grep -c 'did not reach a terminal state' hack/build-cluster.sh   # 1
grep -c 'continues in the cluster' hack/build-cluster.sh         # 1
grep -c 'report_failure' hack/build-cluster.sh          # >= 2 (definition + call)
grep -c 'check_job_absent' hack/build-cluster.sh        # >= 2 (definition + call)
grep -c 'apply_job' hack/build-cluster.sh               # >= 2 (definition + call)
grep -c 'wait_for_job' hack/build-cluster.sh            # >= 2 (definition + call)

# The Makefile cluster recipe still names the driver and renders the sanitized name.
make -n build BUILD_TARGET=sun REF=0000000000000000000000000000000000000000 VERSION=probe \
  | grep -c 'hack/build-cluster.sh'                     # 1
make -n build BUILD_TARGET=sun REF=0000000000000000000000000000000000000000 VERSION=v0.5.6-arm_test \
  | grep -c 'build-v0.5.6-arm-test'                     # >= 1

make test
make lint
```
</verification>
