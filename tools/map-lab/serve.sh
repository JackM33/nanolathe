#!/bin/sh
# Serve a curated map lab to the desktop client's More maps dialog.
#
# Usage: tools/map-lab/serve.sh [LAB] [PORT]
#   LAB  defaults to ~/nanolathe-map-lab (tools/map-curate's --lab)
#   PORT defaults to the first free port from 8765 upwards
#
# Run it in your own terminal: it must stay up while you play. The lab
# manifest uses relative URLs, so this one server answers the manifest, the
# packages and the previews. Point the client at it with
# NANOLATHE_MAP_CATALOG (docs/DESIGN_MODS_MUTATORS.md §5.6); the script
# prints the exact command.
set -eu
lab=${1:-$HOME/nanolathe-map-lab}
manifest="$lab/out/manifest.json"
if [ ! -f "$manifest" ]; then
    echo "serve-lab: no lab manifest at $manifest; run tools/map-curate first" >&2
    exit 1
fi

free_port() {
    python3 - "$1" <<'EOF'
import socket, sys
sock = socket.socket()
try:
    sock.bind(("127.0.0.1", int(sys.argv[1])))
    sock.close()
    sys.exit(0)
except OSError:
    sys.exit(1)
EOF
}

if [ -n "${2:-}" ]; then
    port=$2
    if ! free_port "$port"; then
        echo "serve-lab: port $port is already in use" >&2
        exit 1
    fi
else
    port=8765
    while ! free_port "$port"; do
        port=$((port + 1))
    done
fi

cd "$lab/out"
echo "Map lab: $(pwd)"
echo "Manifest: http://127.0.0.1:$port/manifest.json"
echo "Run the client with:"
echo "  NANOLATHE_MAP_CATALOG=http://127.0.0.1:$port/manifest.json go run ./cmd/nanolathe --root \"<Total Annihilation install>\""
exec python3 -m http.server "$port" --bind 127.0.0.1
