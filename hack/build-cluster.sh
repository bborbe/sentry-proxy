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
