#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
HOST="${DEPLOY_HOST:-oracle-sunucum}"
REMOTE_DIR="${DEPLOY_DIR:-/home/ubuntu/webrtc-signaling}"

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
go build -o webrtc-app .
sudo cp "$REMOTE_DIR/deploy/systemd/webrtc.service" /etc/systemd/system/webrtc.service
sudo cp "$REMOTE_DIR/deploy/nginx/furkanturn.conf" /etc/nginx/sites-available/furkanturn
sudo ln -sfn /etc/nginx/sites-available/furkanturn /etc/nginx/sites-enabled/furkanturn
sudo nginx -t
sudo systemctl daemon-reload
sudo systemctl enable webrtc.service
sudo systemctl restart webrtc.service
sudo systemctl reload nginx
curl -fsS http://127.0.0.1:8080/healthz
EOF

echo "Deploy tamam: $HOST"
