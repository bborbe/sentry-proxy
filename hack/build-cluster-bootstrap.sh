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
