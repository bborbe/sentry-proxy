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
