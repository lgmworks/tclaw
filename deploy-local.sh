#!/usr/bin/env bash
#
# Deploy from a source checkout on the server into the runtime directory.
# Required in .env:
#   TCLAW_RUNTIME_DIR=/home/ploi/tclaw
# Optional:
#   TCLAW_SERVICE=tclaw
#   TCLAW_BUILD_OUTPUT=tclaw-linux-amd64

set -euo pipefail

if [[ ! -f .env ]]; then
  echo "missing .env"
  exit 1
fi

set -a
# shellcheck disable=SC1091
. ./.env
set +a

runtime_dir="${TCLAW_RUNTIME_DIR:?set TCLAW_RUNTIME_DIR in .env}"
service="${TCLAW_SERVICE:-tclaw}"
build_output="${TCLAW_BUILD_OUTPUT:-tclaw-linux-amd64}"
timestamp="$(date +%Y%m%d-%H%M%S)"

echo ">> building linux/amd64..."
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o "$build_output" .

echo ">> stopping $service..."
sudo systemctl stop "$service"

echo ">> backing up current runtime binary..."
if [[ -f "$runtime_dir/tclaw" ]]; then
  cp "$runtime_dir/tclaw" "$runtime_dir/tclaw.bak.$timestamp"
  cp "$runtime_dir/tclaw" "$runtime_dir/tclaw.bak"
fi

echo ">> installing binary and frontend..."
install -m 0755 "$build_output" "$runtime_dir/tclaw"
install -m 0644 web/index.html "$runtime_dir/web/index.html"

echo ">> starting $service..."
sudo systemctl start "$service"

echo ">> status..."
systemctl status "$service" --no-pager | head -10

echo ">> done."
