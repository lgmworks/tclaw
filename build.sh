#!/usr/bin/env bash

set -euo pipefail

mode="${1:-local}"

export GOCACHE="${GOCACHE:-$(pwd)/.gocache}"
export GOMODCACHE="${GOMODCACHE:-$(pwd)/.gomodcache}"

mkdir -p "$GOCACHE" "$GOMODCACHE"

case "$mode" in
  local)
    go build -o tclaw .
    ;;
  prod)
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o tclaw-linux-amd64 .
    ;;
  *)
    echo "usage: ./build.sh [local|prod]"
    exit 1
    ;;
esac
