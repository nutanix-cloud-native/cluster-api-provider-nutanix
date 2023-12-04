#!/usr/bin/env bash
set -euo pipefail
IFS=$'\n\t'

SCRIPT_NAME="$(basename "$0")"
readonly SCRIPT_NAME

function print_usage {
  cat >&2 <<EOF
  Usage: ${SCRIPT_NAME} [COMMAND] [OPTIONS]
  Creates kind cluster with given configuration
  Options:
    --cluster-name     Name of the KinD cluster
    --output-dir       Output directory for generated resources.
    --base-config      Base configuration of KinD cluster.
    --kindest-image    KinD image used in controller-plane
EOF
}

function assert_not_empty {
  local -r arg_name="$1"
  local -r arg_value="$2"

  if [[ -z $arg_value ]]; then
    echo "The value for '$arg_name' cannot be empty"
    print_usage
    exit 1
  fi
}

function run_cmd() {
  local cluster_name=""
  local output_dir=""
  local base_config=""
  local kindest_image=""

  # read options
  while [[ $# -gt 0 ]]; do
    local key="$1"
    case "$key" in
    --cluster-name)
      cluster_name="$2"
      shift
      ;;
    --output-dir)
      output_dir="$2"
      shift
      ;;
    --base-config)
      base_config="$2"
      shift
      ;;
    --kindest-image)
      kindest_image="$2"
      shift
      ;;
    --help)
      print_usage
      exit
      ;;
    esac
    shift
  done

  # validate parameters
  assert_not_empty "--cluster-name" "$cluster_name"
  assert_not_empty "--output-dir" "$output_dir"
  assert_not_empty "--base-config" "$base_config"
  assert_not_empty "--kindest-image" "$kindest_image"

  local cluster_config="$output_dir/kind-config.yaml"

  # make sure output directory exist
  mkdir -p "$output_dir"

  # create/override base config
  export KINDEST_IMAGE="${kindest_image}"
  envsubst <"$base_config" >"$cluster_config"

  kind create cluster --name "$cluster_name" --config "$cluster_config"
}

run_cmd "$@"
