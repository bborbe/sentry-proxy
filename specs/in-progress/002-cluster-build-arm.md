---
status: approved
approved: "2026-11-08T23:26:03Z"
branch: dark-factory/cluster-build-arm
---

## Summary

- `make build` gains a `BUILD_TARGET` selector. `local` — the default — keeps today's `docker build` invocation unchanged; naming a cluster instead renders and applies a Kubernetes Job, waits for it, and reports its result.
- The Job clones the requested git ref, builds `linux/amd64` with BuildKit, and pushes `docker.io/bborbe/sentry-proxy:<VERSION>` using a credential mounted inside the pod.
- The arm lives in this repo's `Makefile`; there is no `Makefile.docker` here.
- `check-version-tag` keeps firing for local builds and stops firing for cluster builds, where HEAD's tag is meaningless because the Job clones an arbitrary ref.
- The local path's observable behaviour is unchanged: same tag, same platform, same five build args, image present in the local Docker store, `make upload` still pushes it.

## Problem

Every `bborbe/sentry-proxy` image today is cut by `docker build` on whichever machine invokes `make build`. The service runs on nukedev, nukeprod and quant, and the deployment pulls `bborbe/sentry-proxy` through nuke's `mirror:` target — so a released image depends on one Mac being awake and its Docker daemon running.

This repo carries two build guards the reference implementation did not: `build:` depends on `check-version-tag`, which refuses unless `HEAD`'s exact tag equals `$(VERSION)`, and `VERSION` defaults to the newest release tag. A naive cluster arm therefore either fails before any Job runs (the guard compares the wrong tree) or silently overwrites a released image (the default resolves to that release).

## Goal

`make build` with no `BUILD_TARGET` behaves exactly as it does today. `BUILD_TARGET=<cluster> make build REF=<sha> VERSION=<tag>` creates a Job in that cluster, which clones the ref, builds and pushes the image; the make target blocks until the Job reaches a terminal state and exits non-zero if it failed. `make upload` and `make clean` skip their host-side work for a cluster target, because the Job already pushed and no local image exists.

## Non-goals

- Migrating any other repository. One repo, one arm.
- Changing image naming or tagging — `$(DOCKER_REGISTRY)/$(IMAGE):$(VERSION)` stays, and `VERSION` keeps its meaning.
- Pushing to the private registry. `docker.prod.nuke.benjamin-borbe.de:443` is produced by `nuke/sentry-proxy/Makefile`'s `mirror:` target, not by this build.
- Build cache export/import. Cold-cache cost is recorded, not solved.
- Loading the built image back to the invoking machine. Rejected, not deferred: it keeps the push credential on the local machine.
- Multi-arch images. `linux/amd64` only, matching the cluster nodes.
- Provisioning cluster namespaces or credentials. Those are prerequisites.
- A second executor (buildx Kubernetes driver, Kaniko). One executor ships.
- Changing `nuke/sentry-proxy`'s two tag pins. A later pin bump is its own change.
- **A committed end-to-end scenario.** The cluster path needs sun access and registry credentials, which CI does not have; the operator-executable rung below is the smoke check instead of a permanently-skipped test.
- **Protecting a released tag from an explicit `VERSION`.** The arm refuses a *defaulted* `VERSION`, but `make build BUILD_TARGET=sun VERSION=v0.5.5` will overwrite that release. Guarding it would need a registry query before every build; recorded here as a known limitation, not implemented.
- **An escape hatch for a defaulted `VERSION`.** The reference arm offers `ALLOW_DEFAULT_VERSION=1`; this spec does not adopt it. No case needs to build the newest release tag by default, and the switch would re-enable exactly the overwrite the refusal exists to prevent.
- **A tunable cluster timeout.** The 3600-second timeout is a fixed constant inside `hack/build-cluster.sh`, not a make variable, until something needs to vary it. The Dockerfile path stays a make variable (`BUILD_DOCKERFILE ?= Dockerfile`) because the failure-mode probe varies it.
- **Reproducible digests.** `SOURCE_DATE_EPOCH` pinning and timestamp rewriting are not adopted; two builds of the same ref may differ.

## Acceptance Criteria

- [ ] **`BUILD_TARGET` defaults to `local` and the local invocation is unchanged** — evidence: `grep -c '^BUILD_TARGET ?= local' Makefile` returns `1`, and `make -n build VERSION=<local-scratch-tag> BRANCH=<branch>` emits a recipe containing all five `--build-arg` flags (`DOCKER_REGISTRY`, `BRANCH`, `BUILD_GIT_VERSION`, `BUILD_GIT_COMMIT`, `BUILD_DATE`), `--platform=linux/amd64`, `--no-cache`, `--rm=true`, `-f Dockerfile .` and `-t docker.io/bborbe/sentry-proxy:<local-scratch-tag>`. All five are asserted because `BUILD_GIT_VERSION`, `BUILD_GIT_COMMIT` and `BUILD_DATE` are read by the Dockerfile as image stamps; `VERSION` and `BRANCH` are passed explicitly because the container masks `.git` and both default from `git`.
- [ ] **The local path still produces an image in the local Docker store, with no cluster configured** — evidence: state transition — `docker image inspect docker.io/bborbe/sentry-proxy:<local-scratch-tag>` exits non-zero before the run, `KUBECONFIG=/nonexistent make build VERSION=<local-scratch-tag> ALLOW_UNTAGGED_BUILD=1` exits 0, and the same inspect then exits 0 with a non-empty `.Id`. The `KUBECONFIG` override is what proves the local arm never needs a cluster.
- [ ] **`check-version-tag` still fires for a bare local build** — evidence: negative — on a tree whose `HEAD` is untagged (the feature branch tip, not a release tag), `make build VERSION=<local-scratch-tag>` exits non-zero and stderr contains `ERROR: refusing to build <local-scratch-tag> from this tree`.
- [ ] **Post-Deploy (Rung-2): a cluster target builds in the cluster, not on the host** — evidence: `make build BUILD_TARGET=sun REF=<full 40-character sha> VERSION=<cluster-scratch-tag>` exits 0 with no `refusing to build` line; `kubectlsun -n build get job build-<cluster-scratch-tag> -o jsonpath='{.status.succeeded}'` returns `1`; `kubectlsun -n build logs job/build-<cluster-scratch-tag> -c clone | grep -c '<full 40-character sha>'` returns ≥ 1 (the init container cloned that ref); and `kubectlsun -n build logs job/build-<cluster-scratch-tag> -c build | grep -cE '^#[0-9]+ \[internal\] '` returns ≥ 1 (BuildKit step lines exist only when a build actually ran in that pod). The Job is created by this command, so no deploy-freshness gate applies. The last two probes are what separate a real cluster build from a host build with a decorative Job.
  - `deploy_check:` `git ls-tree --name-only HEAD hack/ | wc -l`
  - `deploy_target:` `5`
- [ ] **Post-Deploy (Rung-2): the pushed image is the one built from `REF`, it carries the same stamps as a local build, and it exists only in the registry** — evidence: `crane config docker.io/bborbe/sentry-proxy:<cluster-scratch-tag> | jq -r '.config.Env[]'` shows `BUILD_GIT_COMMIT=` the short SHA of `REF`, `BUILD_GIT_VERSION=` a non-`dev` value, and `BUILD_DATE=` matching `^[0-9]{4}-[0-9]{2}-[0-9]{2}T`; `crane digest docker.io/bborbe/sentry-proxy:<cluster-scratch-tag>` returns a `sha256:` digest; and `docker image inspect` on that reference exits non-zero on this Mac with `No such image`. The `BUILD_DATE` shape matters because the Dockerfile default is the literal `unknown`, which the binary cannot parse.
  - `deploy_check:` `git ls-tree --name-only HEAD hack/ | wc -l`
  - `deploy_target:` `5`
- [ ] **The cluster arm refuses a missing `VERSION` and an unresolvable `REF`** — evidence: negative, two probes — on a tree where `VERSION` was not passed, `make build BUILD_TARGET=sun REF=<sha>` exits non-zero and stderr names `VERSION`; and `make build BUILD_TARGET=sun REF=deadbeef VERSION=<cluster-scratch-tag>` exits non-zero naming the ref, because `deadbeef` is not a resolvable full 40-character SHA. Neither refusal may create a Job: after both, `kubectlsun -n build get job build-<cluster-scratch-tag>` exits non-zero with `NotFound`.
- [ ] **Post-Deploy (Rung-2): a failing build surfaces the pod log tail** — evidence: `make build BUILD_TARGET=sun REF=<sha> VERSION=<cluster-scratch-tag> BUILD_DOCKERFILE=Dockerfile.missing` exits non-zero and its stdout contains a line from the pod log naming the missing file, without a separate `kubectl` call.
  - `deploy_check:` `git ls-tree --name-only HEAD hack/ | wc -l`
  - `deploy_target:` `5`
- [ ] **`make upload` and `make clean` are no-ops for a cluster target** — evidence: negative — `make -n upload BUILD_TARGET=sun VERSION=<cluster-scratch-tag>` contains no `docker push`, `make -n clean BUILD_TARGET=sun VERSION=<cluster-scratch-tag>` contains no `docker rmi`, `docker builder prune` or `rm -rf vendor`, and both targets exit 0 for real.
- [ ] **The arm files exist in `hack/`, the Job spec is parameterized, and the credential arrives by remap** — evidence: `ls hack/` lists `build-cluster.sh`, `cluster-alias.sh`, `build-job.yaml`, `build-cluster-bootstrap.sh` and `dockerconfig.json.tpl`; `grep -c '^kind: Job' hack/build-job.yaml` returns `1`; `grep -ohE '__REPO_URL__|__REF__|__TAG__|__DOCKERFILE__|__IMAGE__|__PUSH__' hack/build-job.yaml | sort -u | wc -l` returns `6` (distinct tokens, so two on one line still count twice); `grep -c 'push=__PUSH__' hack/build-job.yaml` returns ≥ 1; and `grep -cF '.dockerconfigjson' hack/build-job.yaml` returns ≥ 1 while `grep -c 'auths' hack/build-job.yaml` returns `0` — the credential is a secret reference with an `items:` remap, never an inline value.
- [ ] **Post-Deploy (Rung-2): the Job name is DNS-1123 sanitized** — evidence: `make -n build BUILD_TARGET=sun REF=<sha> VERSION=v0.5.6-arm_test` renders a Job name of `build-v0.5.6-arm-test` (underscore replaced, nothing else changed), and a real run with that `VERSION` yields `kubectlsun -n build get job build-v0.5.6-arm-test`.
  - `deploy_check:` `git ls-tree --name-only HEAD hack/ | wc -l`
  - `deploy_target:` `5`
- [ ] **Post-Deploy (Rung-2): the build does not push to the private registry** — evidence: negative — `kubectlsun -n build logs job/build-<cluster-scratch-tag> | grep -c 'docker.prod.nuke.benjamin-borbe.de'` returns `0`, and `crane digest docker.prod.nuke.benjamin-borbe.de:443/bborbe/sentry-proxy:<cluster-scratch-tag>` fails for a tag only this arm produced.
  - `deploy_check:` `git ls-tree --name-only HEAD hack/ | wc -l`
  - `deploy_target:` `5`
- [ ] **`make test` and `make lint` pass** — evidence: exit code 0 for both. (Not `make precommit`: its `check` target pulls trivy / osv-scanner / vulncheck over the network and asserts nothing about this change.)

## Verification

### Container-executable (runs inside the YOLO container at prompt time)

- `make test` — the unit and integration suite exits 0
- `make lint` — exits 0
- `bash -n hack/build-cluster.sh hack/cluster-alias.sh hack/build-cluster-bootstrap.sh` — the new shell parses (the Go lint does not cover it)
- `grep -c '^BUILD_TARGET ?= local' Makefile` — returns `1`
- `ls hack/ | wc -l` — returns `5`, and each of the five names is present
- `grep -c '^kind: Job' hack/build-job.yaml` — returns `1`
- `grep -cE '__REPO_URL__|__REF__|__TAG__|__DOCKERFILE__|__IMAGE__|__PUSH__' hack/build-job.yaml` — returns ≥ 6
- `make -n build VERSION=probe BRANCH=probe` — the default recipe still emits the unchanged `docker build` arguments, including all five `--build-arg` lines
- `make -n build BUILD_TARGET=sun REF=<40-char-sha> VERSION=probe` — the cluster recipe names `hack/build-cluster.sh` and contains no `refusing to build`
- `make -n build BUILD_TARGET=sun REF=<40-char-sha> VERSION=v0.5.6-arm_test` — renders the Job name `build-v0.5.6-arm-test`
- `make -n upload BUILD_TARGET=sun VERSION=probe` — contains no `docker push`
- `make -n clean BUILD_TARGET=sun VERSION=probe` — contains no `docker rmi`, `docker builder prune` or `rm -rf vendor`

### Operator-executable (runs on the host after PR merge)

- `make build VERSION=<local-scratch-tag>` — refused by `check-version-tag` on an untagged tree
- `KUBECONFIG=/nonexistent make build VERSION=<local-scratch-tag> ALLOW_UNTAGGED_BUILD=1` — the local path still lands an image with no cluster configured
- `make build BUILD_TARGET=sun REF=<sha> VERSION=<cluster-scratch-tag>` — a cluster Job runs to completion
- `kubectlsun -n build get job build-<cluster-scratch-tag> -o jsonpath='{.status.succeeded}'` — returns `1`
- `kubectlsun -n build logs job/build-<cluster-scratch-tag> -c clone | grep -c <full-sha>` — returns ≥ 1
- `kubectlsun -n build logs job/build-<cluster-scratch-tag> -c build | grep -cE '^#[0-9]+ \[internal\] '` — returns ≥ 1
- `crane config docker.io/bborbe/sentry-proxy:<cluster-scratch-tag> | jq -r '.config.Env[]'` — shows `BUILD_GIT_COMMIT=<short sha>`, a non-`dev` `BUILD_GIT_VERSION`, and an RFC3339 `BUILD_DATE`
- `crane digest docker.io/bborbe/sentry-proxy:<cluster-scratch-tag>` — returns the digest the Job pushed
- `make build BUILD_TARGET=sun REF=<sha> VERSION=<cluster-scratch-tag> BUILD_DOCKERFILE=Dockerfile.missing` — exits non-zero with the pod log tail inline

## Desired Behavior

1. `make build` with no `BUILD_TARGET`, or `BUILD_TARGET=local`, runs the existing `docker build` recipe and leaves the image in the local Docker store under `$(DOCKER_REGISTRY)/$(IMAGE):$(VERSION)`.
2. `BUILD_TARGET=<cluster>` renders `hack/build-job.yaml` with the run's values, applies it to the cluster's `build` namespace, and blocks until the Job reaches a terminal state.
3. The make target exits 0 when the Job succeeded and non-zero when it failed, printing the Job's pod log tail in the failure case so the cause is visible without a separate `kubectl` call.
4. `hack/build-cluster.sh` validates `REF` before rendering — it must match `^[A-Za-z0-9._/-]+$`, be exactly 40 characters, and resolve locally via `git rev-parse` — and fails with a message naming the ref otherwise. Validation lives in the script, not in the `Makefile`, so `make -n` stays dry and works in a container where `.git` is masked. The Job's init container clones exactly that ref, never the default branch.
5. The build container builds `linux/amd64` from `Dockerfile` and pushes `docker.io/bborbe/sentry-proxy:<VERSION>` with the same three stamps the local recipe passes — `BUILD_GIT_VERSION`, `BUILD_GIT_COMMIT` (the short SHA of `REF`) and `BUILD_DATE` (RFC3339) — because the Dockerfile defaults them to `dev` / `none` / `unknown` and `BUILD_DATE=unknown` cannot be parsed by the binary. The clone container derives `BUILD_GIT_VERSION` with `git describe --tags --always` on the cloned ref and `BUILD_DATE` from the current UTC time, matching the local recipe's shapes.
6. The push credential is read from a mounted secret presented as `config.json` under `DOCKER_CONFIG`; it is never read from the invoking machine.
7. `BUILD_TARGET=local` never touches a cluster: no Job is created, no cluster context is required, and the target works on a machine with no kubeconfig.
8. `check-version-tag` and `check-go-mod` run only for `BUILD_TARGET=local`; the cluster arm instead refuses a `VERSION` that came from the Makefile default, so a bare cluster build cannot overwrite the newest release tag.
9. `upload` and `clean` skip their host-side work for a non-`local` target and exit 0.
10. The Job is named `build-<VERSION>` after DNS-1123 sanitization — lowercased, everything outside `[a-z0-9.-]` replaced with `-`, truncated to 63 characters, trailing `-` and `.` stripped — so `build-v0.5.6-arm_test` becomes `build-v0.5.6-arm-test`. The name is computed in the `Makefile` as a `$(shell …)` expression and passed to the script, so `make -n` renders the sanitized name rather than the raw tag.

## Constraints

- `buca` keeps its current composition — `build upload clean apply`.
- `DOCKER_REGISTRY`, `IMAGE`, `BRANCH` and `VERSION` keep their current definitions and meanings. `VERSION` remains the image tag.
- The `local` recipe's `docker build` arguments must not change: `--no-cache`, `--rm=true`, `--platform=linux/amd64`, `-f Dockerfile .`, the `DOCKER_BUILDKIT=1` prefix, and all five `--build-arg` lines (`DOCKER_REGISTRY`, `BRANCH`, `BUILD_GIT_VERSION`, `BUILD_GIT_COMMIT`, `BUILD_DATE`).
- `check-version-tag` keeps its current message text and its `ALLOW_UNTAGGED_BUILD` escape; the change is the `ifeq ($(BUILD_TARGET),local)` wrapper only. `check-go-mod` gets the same wrapper.
- `hack/build-job.yaml` must not live under `k8s/`, so a future yaml-sweeping apply target cannot adopt it as deploy state. (This repo has no such sweep today; the constraint matches the reference arm and costs nothing.)
- `hack/build-cluster-bootstrap.sh` and `hack/dockerconfig.json.tpl` are one-time bootstrap helpers: they create the `build` namespace and the `buildkit-push` secret. They are not on the build path — the runtime credential arrives through the Job's `items:` remap. The template renders TeamVault placeholders into `{"auths":{"https://index.docker.io/v1/":{…}}}`; no credential value is ever committed, and the template is never applied during a build.
- The arm's shape is taken from `bborbe/go-skeleton` `hack/` and `bborbe/agent-task-executor` `Makefile.docker`. The implementing container cannot reach either repository, so the files are authored from this spec's contract, not copied.
- The repository URL is fixed: `https://github.com/bborbe/sentry-proxy.git`. It is not derived from `git remote`, which is unavailable in the build container.
- The cluster timeout is a 3600-second constant inside `hack/build-cluster.sh`. The Dockerfile path is a make variable, `BUILD_DOCKERFILE ?= Dockerfile`.
- The `local` path must work with no kubeconfig and no cluster credentials present.
- No new runtime dependency in the built image; the change is build tooling only.

## Prerequisites

Named so the operator can check them before the first cluster run:

- `~/.kube/sun` — the context `hack/cluster-alias.sh` maps the alias `sun` onto.
- The `build` namespace on that cluster.
- A `kubernetes.io/dockerconfigjson` secret named `buildkit-push` in `build`, authenticating against `docker.io` only.
- `crane` on the invoking host, for the digest and config probes.

## Failure Modes

| Trigger | Expected behavior | Recovery |
|---|---|---|
| `BUILD_TARGET` names an unknown cluster | The make target fails before rendering, listing the accepted values | Rerun with a valid target |
| The cluster is unreachable | The make target fails with the connection error; no partial Job is left | Rerun when the cluster is available |
| `REF` does not resolve, or is shorter than 40 characters | The make target fails before rendering, naming the ref | Re-run with a full 40-character SHA that exists on the remote |
| `VERSION` is omitted on a cluster build | The make target refuses before rendering, naming `VERSION` as the missing input | Re-run with an explicit `VERSION` |
| The Job is rejected at admission (pod-security, RBAC) | The make target prints the Job's events and exits non-zero | Fix the namespace policy or the Job's security context |
| The registry push is refused (401) | The Job fails at the push; its log carries the registry error | Verify the secret's `items:` remap; recovery is confirmed by a re-run whose Job log shows `pushing manifest` and whose `crane digest` resolves |
| The registry throttles (429) | The Job fails at the push; its log carries the status code | Re-run after the window; the tag did not land, so nothing is overwritten |
| The build container is OOMKilled or evicted for ephemeral storage | The Job fails with `OOMKilled` / `Evicted` in its pod status | Raise the Job's memory or `emptyDir` size, then re-run; the recovery is confirmed by `succeeded=1` |
| The Job exceeds the 3600-second timeout | The make target fails and names the timeout it waited | Raise the constant in `hack/build-cluster.sh`, re-run, and confirm `succeeded=1` |
| `make build` is interrupted mid-wait | The Job continues in the cluster; a later `kubectl get job build-<VERSION>` reports its state | Read the Job's state, or delete it with `kubectlsun -n build delete job build-<VERSION>` and re-run |
| A previous Job with the same name exists | The make target fails before applying, naming the existing Job | `kubectlsun -n build delete job build-<VERSION>`, re-run, confirm `succeeded=1` |

## Security / Abuse

- The push credential exists only as a cluster secret, mounted into the build pod. It is never written to the invoking machine, never passed as a make variable, and never printed.
- The `items:` remap is load-bearing: without it the secret file keeps its own name while Docker looks for `config.json`, and auth fails silently in the same shape a missing credential produces. This is why `dockerconfig.json.tpl` is bootstrap-only — a template on the build path would put the credential through a rendering step.
- The build container carries no cluster credentials — it cannot create, read or modify other resources.
- `REF` is validated against a local `git rev-parse` and a 40-character shape before rendering, and passed as a clone argument rather than interpolated into a shell string, so a crafted ref cannot execute commands in the init container.
- The Job spec is a reusable, parameterized manifest; no repository name, tag or credential is hardcoded in it.

## Suggested Decomposition

| # | Prompt focus | Covers DBs | Covers ACs | Rung | Depends on |
|---|---|---|---|---|---|
| 1 | `Makefile` — the `BUILD_TARGET` selector, the `check-version-tag` / `check-go-mod` wraps, the missing-`VERSION` refusal, and the `upload` / `clean` skips | 1, 7, 8, 9 | 1, 2, 3, 6, 8, 12 | container (1, 3, 8, 12) + operator (2) | — |
| 2 | `hack/` — author the five arm files from the contract in DBs 4–6 and Constraints; the implementing container cannot reach another repository, so the scripts and manifest are written from the spec, not copied | 4, 5, 6, 10 | 9, 10 | container | — |
| 3 | The cluster branch of `build`: validate `REF`, render, apply, wait, report | 2, 3, 4 | 4, 5, 7, 11 | operator (real cluster) | prompts 1, 2 |

Rationale: prompt 1 changes only the local recipe and the guards and is independently verifiable; prompt 2 authors static manifests and scripts from the stated contract; prompt 3 is the only prompt that shells out to a cluster and needs both prior artifacts. ACs marked operator in prompt 1 and 3 are verified on the host after merge, not inside the container — the container inherits only the container rung.

This ships as one spec with three prompts rather than a split: the Makefile half and the `hack/` half share one behavioural contract (the guard interaction and the `REF` rule cross both), so splitting them would duplicate the Constraints section and let the two halves drift.

**Size exception, recorded deliberately:** 10 Desired Behaviors × 12 Acceptance Criteria is 120, above the 50-cell guidance. Most of the ACs are negative probes on one contract (refusals, skips, absences) rather than independent surface, and the three-prompt decomposition keeps each prompt independently verifiable — the reference arm this repo copies ships as a single spec of comparable size.

## Do-Nothing Option

Every `sentry-proxy` release keeps requiring an awake Mac with a running Docker daemon, and the repo's two build guards stay untested against a cluster arm — so the next repo that copies the pattern inherits the same trap. The migration itself stays unproven for this component.
