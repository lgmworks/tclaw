#!/usr/bin/env bash
#
# Compile, upload and restart tclaw on the openclaw server.
#
# Requires: ssh config alias "openclaw" pointing at the deploy host.
# Target layout (must already exist on the server):
#   /home/openclaw/tclaw/tclaw         — binary, owned by user openclaw
#   /home/openclaw/tclaw/web/          — static frontend dir
#   systemd unit "tclaw.service" running as user openclaw
#
# Usage: ./deploy.sh

set -euo pipefail

REMOTE_HOST="openclaw"
REMOTE_DIR="/home/openclaw/tclaw"
LOCAL_BIN="tclaw-linux-amd64"

echo ">> compiling for linux/amd64..."
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o "$LOCAL_BIN" .

echo ">> backing up current binary on server..."
ssh "$REMOTE_HOST" "cp $REMOTE_DIR/tclaw $REMOTE_DIR/tclaw.bak"

echo ">> uploading binary..."
scp "$LOCAL_BIN" "$REMOTE_HOST:$REMOTE_DIR/tclaw.new"

echo ">> uploading web/index.html..."
scp web/index.html "$REMOTE_HOST:$REMOTE_DIR/web/index.html.new"

echo ">> swapping in place and restarting service..."
ssh "$REMOTE_HOST" "set -e
  cd $REMOTE_DIR
  mv tclaw.new tclaw
  chmod +x tclaw
  mv web/index.html.new web/index.html
  sudo systemctl restart tclaw
  sleep 1
  systemctl status tclaw --no-pager | head -10
"

echo ">> done."
