#!/usr/bin/env bash
# hack/build-cluster.sh — render, apply, wait for and report the sentry-proxy
# build Job from build-job.yaml. Argument parsing, cluster resolution, REF
# validation and rendering live here; the apply/wait/report half drives the Job.
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

job_exists() {
	kubectl -n build get job "$JOB_NAME" >/dev/null 2>&1
}

check_job_absent() {
	if job_exists; then
		echo "ERROR: Job '$JOB_NAME' already exists; delete it first: kubectl -n build delete job $JOB_NAME" >&2
		exit 1
	fi
}

apply_job() {
	if ! kubectl apply -f "$RENDERED"; then
		echo "ERROR: Job '$JOB_NAME' was rejected by the cluster" >&2
		kubectl -n build describe job "$JOB_NAME" >&2 || true
		exit 1
	fi
}

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

report_failure() {
	echo "---- Job events ----" >&2
	kubectl -n build describe job "$JOB_NAME" >&2 || true
	echo "---- clone container logs ----" >&2
	kubectl -n build logs "job/$JOB_NAME" -c clone --tail=200 >&2 || true
	echo "---- build container logs ----" >&2
	kubectl -n build logs "job/$JOB_NAME" -c build --tail=200 >&2 || true
}

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

validate_ref
RENDERED=$(mktemp)
trap 'rm -f "$RENDERED"' EXIT
trap 'echo "interrupted; Job ${JOB_NAME} continues in the cluster" >&2; exit 1' INT TERM
render_job > "$RENDERED"
run_job
