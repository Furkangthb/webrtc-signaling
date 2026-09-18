#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
HOST="${DEPLOY_HOST:-oracle-sunucum}"
REMOTE_DIR="${DEPLOY_DIR:-/home/ubuntu/webrtc-docker-test}"

rsync -az --delete \
  --exclude '.git/' \
  --exclude '.env' \
  --exclude 'webrtc-app' \
  --exclude 'tmp/' \
  --exclude 'kayitlar/' \
  --exclude 'public/uploads/' \
  "$ROOT/" "$HOST:$REMOTE_DIR/"

ssh "$HOST" bash -s <<EOF
set -euo pipefail
cd "$REMOTE_DIR"
docker compose build
docker compose up -d
docker image prune -f
curl -fsS http://127.0.0.1:8080/healthz
EOF

echo "Deploy tamam: $HOST"