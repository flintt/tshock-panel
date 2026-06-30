#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

log()  { echo -e "${GREEN}[SETUP]${NC} $*"; }
warn() { echo -e "${YELLOW}[WARN]${NC} $*"; }
err()  { echo -e "${RED}[ERROR]${NC} $*"; }
info() { echo -e "${CYAN}[INFO]${NC} $*"; }

TSHOCK_CONTAINER="terraria-server"
PANEL_CONTAINER="terraria-panel"
NET9RT_DIR="/tmp/net9rt"
PLUGIN_BUILD_DIR="/tmp/tpplugin_build"
TSHOCK_IMAGE="ghcr.io/pryxis/tshock:stable"

# ============================================================
# Phase 0: Check existing installation
# ============================================================
check_existing() {
    if docker ps --format '{{.Names}}' | grep -q "^${TSHOCK_CONTAINER}$"; then
        log "TShock container '${TSHOCK_CONTAINER}' is already running"
        return 0
    fi
    return 1
}

# ============================================================
# Phase 1: Install system dependencies
# ============================================================
install_deps() {
    log "=== Phase 1: Installing system dependencies ==="

    if ! command -v docker &>/dev/null; then
        err "Docker not found. Please install Docker first: https://docs.docker.com/engine/install/"
        exit 1
    fi
    log "Docker: $(docker --version)"

    if ! docker compose version &>/dev/null 2>&1 && ! docker-compose version &>/dev/null 2>&1; then
        err "Docker Compose not found. Please install Docker Compose plugin."
        exit 1
    fi
    log "Docker Compose: OK"

    if ! command -v go &>/dev/null; then
        warn "Go not found, installing..."
        if command -v apt &>/dev/null; then
            apt update && apt install -y golang-go
        elif command -v yum &>/dev/null; then
            yum install -y golang
        else
            err "Cannot auto-install Go. Please install Go 1.21+ manually: https://go.dev/dl/"
            exit 1
        fi
    fi
    log "Go: $(go version)"

    if ! command -v dotnet &>/dev/null; then
        warn ".NET SDK not found, installing..."
        if command -v apt &>/dev/null; then
            apt update && apt install -y dotnet-sdk-8.0 2>/dev/null || {
                warn "dotnet-sdk-8.0 not in apt, trying Microsoft feed..."
                apt install -y wget apt-transport-https
                wget -q https://packages.microsoft.com/config/ubuntu/$(lsb_release -rs)/packages-microsoft-prod.deb -O /tmp/packages-microsoft-prod.deb
                dpkg -i /tmp/packages-microsoft-prod.deb
                apt update && apt install -y dotnet-sdk-8.0
            }
        else
            err "Cannot auto-install .NET SDK. Please install .NET SDK 8.0+ manually: https://dotnet.microsoft.com/download"
            exit 1
        fi
    fi
    log ".NET SDK: $(dotnet --version)"

    CSC=$(find /usr/lib/dotnet/sdk -name "csc.dll" -path "*/Roslyn/*" 2>/dev/null | head -1)
    if [ -z "$CSC" ]; then
        err "csc.dll not found in .NET SDK. .NET SDK installation may be incomplete."
        exit 1
    fi
    log "csc.dll: $CSC"
}

# ============================================================
# Phase 2: Create directory structure
# ============================================================
create_dirs() {
    log "=== Phase 2: Creating directory structure ==="

    mkdir -p tshock worlds plugins presets trails worldbackups
    mkdir -p plugin
    touch tshock/setup.lock

    log "Directory structure created"
}

# ============================================================
# Phase 3: Pull TShock image and start container (to extract DLLs)
# ============================================================
start_tshock_temp() {
    log "=== Phase 3: Starting TShock container (first run for DLL extraction) ==="

    if check_existing; then
        log "TShock already running, skipping temp start"
        return 0
    fi

    if [ ! -f tshock/config.json ]; then
        warn "tshock/config.json not found. Starting TShock with default config first..."
        warn "You will need to configure REST API after this step."

        docker run -d --name "${TSHOCK_CONTAINER}_temp" \
            -p 7777:7777 -p 7878:7878 \
            -v "$PWD/tshock:/tshock" \
            -v "$PWD/worlds:/worlds" \
            -v "$PWD/plugins:/plugins" \
            "${TSHOCK_IMAGE}" \
            -world /worlds/MyWorld.wld -autocreate 2 -worldname "MyWorld" -maxplayers 8 -difficulty 0

        log "Waiting for TShock to initialize (30s)..."
        sleep 30

        docker stop "${TSHOCK_CONTAINER}_temp" 2>/dev/null || true
        docker rm "${TSHOCK_CONTAINER}_temp" 2>/dev/null || true

        if [ -f tshock/config.json ]; then
            log "config.json generated. Configuring REST API..."
            configure_tshock_rest
        fi
    fi

    log "Starting TShock via docker-compose..."
    docker-compose up -d terraria 2>/dev/null || docker compose up -d terraria

    log "Waiting for TShock to start (30s)..."
    sleep 30

    if ! docker ps --format '{{.Names}}' | grep -q "^${TSHOCK_CONTAINER}$"; then
        err "TShock container failed to start. Check logs:"
        docker logs "$TSHOCK_CONTAINER" 2>&1 | tail -20
        exit 1
    fi
    log "TShock container started"
}

# ============================================================
# Phase 3.5: Configure TShock REST API
# ============================================================
configure_tshock_rest() {
    local config="tshock/config.json"

    if [ ! -f "$config" ]; then
        warn "config.json not found, skipping REST configuration"
        return
    fi

    log "Configuring TShock REST API in config.json..."

    python3 -c "
import json, sys
with open('$config', 'r') as f:
    cfg = json.load(f)

cfg['RestApiEnabled'] = True
cfg['RestApiPort'] = 7878

if 'ApplicationRestTokens' not in cfg or not cfg['ApplicationRestTokens']:
    cfg['ApplicationRestTokens'] = {
        'opencode-panel-key-2024': {
            'Username': 'rest_api',
            'UserGroupName': 'superadmin'
        }
    }

with open('$config', 'w') as f:
    json.dump(cfg, f, indent=2)
print('REST API configured: RestApiEnabled=True, RestApiPort=7878')
" 2>/dev/null || warn "python3 not available, please configure config.json manually"

    log "REST API configuration done"
}

# ============================================================
# Phase 4: Extract dependency DLLs from TShock container
# ============================================================
extract_dlls() {
    log "=== Phase 4: Extracting dependency DLLs from TShock container ==="

    mkdir -p "$NET9RT_DIR" "$PLUGIN_BUILD_DIR"

    local container_id
    container_id=$(docker ps --filter "name=${TSHOCK_CONTAINER}" --format '{{.ID}}' | head -1)

    if [ -z "$container_id" ]; then
        err "TShock container not running. Cannot extract DLLs."
        exit 1
    fi

    log "Extracting .NET 9 runtime DLLs..."
    local net9_path
    net9_path=$(docker exec "$container_id" find /usr/share/dotnet/shared/Microsoft.NETCore.App -maxdepth 1 -mindepth 1 -type d | sort -V | tail -1)
    if [ -n "$net9_path" ]; then
        docker cp "${container_id}:${net9_path}/." "$NET9RT_DIR/"
        log "Extracted from: ${net9_path}"
    else
        err "Could not find .NET runtime in container"
        exit 1
    fi

    log "Extracting TShock server DLLs..."
    docker cp "${container_id}:/server/TerrariaServer.dll" "$PLUGIN_BUILD_DIR/"
    docker cp "${container_id}:/server/OTAPI.dll" "$PLUGIN_BUILD_DIR/"
    docker cp "${container_id}:/server/OTAPI.Runtime.dll" "$PLUGIN_BUILD_DIR/"
    docker cp "${container_id}:/server/ServerPlugins/TShockAPI.dll" "$PLUGIN_BUILD_DIR/"

    log "DLL extraction complete"
    log "  Runtime: $(ls "$NET9RT_DIR"/*.dll 2>/dev/null | wc -l) DLLs"
    log "  Server:  $(ls "$PLUGIN_BUILD_DIR"/*.dll 2>/dev/null | wc -l) DLLs"
}

# ============================================================
# Phase 5: Compile TeleportRestPlugin
# ============================================================
compile_plugin() {
    log "=== Phase 5: Compiling TeleportRestPlugin ==="

    local cs_file="plugin/TeleportRestPlugin.cs"
    local out_file="plugins/TeleportRestPlugin.dll"

    if [ ! -f "$cs_file" ]; then
        err "$cs_file not found"
        exit 1
    fi

    local CSC
    CSC=$(find /usr/lib/dotnet/sdk -name "csc.dll" -path "*/Roslyn/*" | head -1)
    if [ -z "$CSC" ]; then
        err "csc.dll not found"
        exit 1
    fi

    local REFS=""
    for dll in "$NET9RT_DIR"/*.dll; do
        REFS="$REFS -reference:$dll"
    done
    REFS="$REFS -reference:$PLUGIN_BUILD_DIR/TerrariaServer.dll"
    REFS="$REFS -reference:$PLUGIN_BUILD_DIR/OTAPI.dll"
    REFS="$REFS -reference:$PLUGIN_BUILD_DIR/OTAPI.Runtime.dll"
    REFS="$REFS -reference:$PLUGIN_BUILD_DIR/TShockAPI.dll"

    log "Compiling with csc..."
    dotnet "$CSC" \
        -target:library \
        -nostdlib+ \
        $REFS \
        -out:"$out_file" \
        "$cs_file"

    if [ -f "$out_file" ]; then
        log "Plugin compiled: $out_file ($(stat -c%s "$out_file") bytes)"
    else
        err "Plugin compilation failed"
        exit 1
    fi
}

# ============================================================
# Phase 6: Compile Go panel
# ============================================================
compile_panel() {
    log "=== Phase 6: Compiling Go panel ==="

    if [ ! -f panel/main.go ]; then
        err "panel/main.go not found"
        exit 1
    fi

    cd panel
    CGO_ENABLED=0 go build -o panel main.go
    cd "$SCRIPT_DIR"

    if [ -f panel/panel ]; then
        log "Panel compiled: panel/panel ($(stat -c%s panel/panel) bytes)"
    else
        err "Panel compilation failed"
        exit 1
    fi
}

# ============================================================
# Phase 7: Deploy
# ============================================================
deploy() {
    log "=== Phase 7: Deploying ==="

    docker-compose up -d 2>/dev/null || docker compose up -d

    log "Waiting for services to start (10s)..."
    sleep 10

    log "=== Deployment Status ==="
    docker ps --filter "name=${TSHOCK_CONTAINER}" --filter "name=${PANEL_CONTAINER}" --format "table {{.Names}}\t{{.Status}}\t{{.Ports}}"
}

# ============================================================
# Phase 8: Verify
# ============================================================
verify() {
    log "=== Phase 8: Verification ==="

    local ok=true

    if docker ps --format '{{.Names}}' | grep -q "^${TSHOCK_CONTAINER}$"; then
        log "TShock container: RUNNING"
        if docker logs "$TSHOCK_CONTAINER" 2>&1 | grep -q "TeleportRest.*Registered"; then
            log "TeleportRestPlugin: LOADED"
        else
            warn "TeleportRestPlugin: NOT DETECTED (may still be starting)"
        fi
    else
        err "TShock container: NOT RUNNING"
        ok=false
    fi

    if docker ps --format '{{.Names}}' | grep -q "^${PANEL_CONTAINER}$"; then
        log "Panel container: RUNNING"
    else
        err "Panel container: NOT RUNNING"
        ok=false
    fi

    if curl -sf http://127.0.0.1:4891/api/check >/dev/null 2>&1; then
        log "Panel HTTP: OK"
    else
        warn "Panel HTTP: not responding (may still be starting)"
    fi

    if $ok; then
        log ""
        log "========================================="
        log "  Deployment complete!"
        log "  Panel:  http://$(hostname -I 2>/dev/null | awk '{print $1}' || echo 'your-server-ip'):4891"
        log "  Game:   $(hostname -I 2>/dev/null | awk '{print $1}' || echo 'your-server-ip'):7777"
        log "========================================="
    else
        err "Deployment has issues. Check container logs."
    fi
}

# ============================================================
# Main
# ============================================================
main() {
    echo ""
    info "TShock Web Panel - Setup Script"
    info "================================"
    echo ""

    install_deps
    create_dirs
    start_tshock_temp
    extract_dlls
    compile_plugin
    compile_panel
    deploy
    verify

    echo ""
    info "Next steps:"
    info "  1. Open the panel URL in your browser"
    info "  2. Login with any username/password (Panel uses TShock App Token)"
    info "  3. Connect Terraria client to server port 7777"
    echo ""
}

main "$@"
