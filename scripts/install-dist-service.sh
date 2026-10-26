#!/usr/bin/env bash
# Installs `cbdist` and starts it as a user service on 127.0.0.1:18780.
# Does NOT touch `tailscale serve`; exposing it to the tailnet is a separate, explicit step
# (docs/update.md, "Publishing to the tailnet").
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"
make cbdist >/dev/null
mkdir -p "$HOME/.local/bin" "$HOME/.local/share/clipbridge/releases" "$HOME/.config/systemd/user"
install -m 755 core/dist/host/cbdist "$HOME/.local/bin/cbdist"
install -m 644 deploy/clipbridge-dist.service "$HOME/.config/systemd/user/clipbridge-dist.service"
systemctl --user daemon-reload
systemctl --user enable --now clipbridge-dist.service
sleep 1
curl -fsS http://127.0.0.1:18780/healthz && echo "clipbridge-dist is up on 127.0.0.1:18780"
