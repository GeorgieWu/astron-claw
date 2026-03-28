#!/usr/bin/env bash
set -euo pipefail

# ---------------------------------------------------------------------------
# astron-claw installer
# Installs the astron-claw OpenClaw channel plugin.
# The bridge backend is now deployed separately via the Go backend in backend/.
# Supports both local (plugin/ directory present) and remote (GitHub Release
# download) modes, so it works equally well from a git clone or via:
#
#   curl -fsSL https://raw.githubusercontent.com/hygao1024/astron-claw/master/install.sh | bash -s -- \
#     --bot-token <token> --server-url ws://server:8765/bridge/bot
#
# Wrapped in main() so `curl ... | bash` reads the entire script before
# executing — prevents sub-commands from consuming stdin.
# ---------------------------------------------------------------------------

main() {

GITHUB_REPO="hygao1024/astron-claw"
TARBALL_NAME="astron-claw-plugin.tar.gz"

OPENCLAW_BIN="${OPENCLAW_BIN:-openclaw}"
PLUGIN_NAME="astron-claw"
TARGET_DIR="${TARGET_DIR:-$HOME/.openclaw/extensions/astron-claw}"
OPENCLAW_CONFIG_PATH="${OPENCLAW_CONFIG_PATH:-$HOME/.openclaw/openclaw.json}"

BOT_TOKEN=""
SERVER_URL="ws://localhost:8765/bridge/bot"
ACCOUNT_NAME="AstronClaw"
VERSION="latest"

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------
usage() {
  cat <<'USAGE'
Usage:
  install.sh --bot-token <token> [options]

Options:
  --bot-token <token>       Bot authentication token (required)
  --server-url <url>        Astron bridge WebSocket URL
                            (default: ws://localhost:8765/bridge/bot)
  --name <name>             Display name for the channel account
                            (default: AstronClaw)
  --target-dir <path>       Plugin install directory
                            (default: ~/.openclaw/extensions/astron-claw)
  --version <tag>           Release version to download (default: latest)
                            Only used in remote mode (no local plugin/ dir)
  -h, --help                Show this help message
USAGE
}

log() {
  printf "[astron-install] %s\n" "$*"
}

log_error() {
  printf "[astron-install] ERROR: %s\n" "$*" >&2
}

require_cmd() {
  local cmd="$1"
  local hint="$2"
  if ! command -v "$cmd" >/dev/null 2>&1; then
    log_error "missing command: $cmd"
    log_error "$hint"
    exit 1
  fi
}

need_next_arg() {
  local opt="$1"
  local argc="$2"
  if [ "$argc" -lt 2 ]; then
    log_error "missing value for $opt"
    exit 1
  fi
}

# ---------------------------------------------------------------------------
# Parse arguments
# ---------------------------------------------------------------------------
while [ "$#" -gt 0 ]; do
  case "$1" in
    --bot-token)
      need_next_arg "$1" "$#"
      BOT_TOKEN="$2"
      shift 2
      ;;
    --server-url)
      need_next_arg "$1" "$#"
      SERVER_URL="$2"
      shift 2
      ;;
    --name)
      need_next_arg "$1" "$#"
      ACCOUNT_NAME="$2"
      shift 2
      ;;
    --target-dir)
      need_next_arg "$1" "$#"
      TARGET_DIR="$2"
      shift 2
      ;;
    --version)
      need_next_arg "$1" "$#"
      VERSION="$2"
      shift 2
      ;;
    --with-server)
      log_error "--with-server has been removed"
      log_error "Deploy the Go backend separately (see README.md or docker-compose.yml)."
      exit 1
      ;;
    --server-dir)
      log_error "--server-dir has been removed"
      log_error "Deploy the Go backend separately (see README.md or docker-compose.yml)."
      exit 1
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      log_error "unknown argument: $1"
      usage
      exit 1
      ;;
  esac
done

# ---------------------------------------------------------------------------
# Validate required arguments
# ---------------------------------------------------------------------------
if [ -z "$BOT_TOKEN" ]; then
  log_error "--bot-token is required"
  usage
  exit 1
fi

# ---------------------------------------------------------------------------
# Check prerequisites
# ---------------------------------------------------------------------------
require_cmd "$OPENCLAW_BIN" "Install OpenClaw CLI then retry: https://docs.openclaw.dev"
require_cmd node "Install Node.js then retry: https://nodejs.org"

log "prerequisites check passed"

# ---------------------------------------------------------------------------
# Determine plugin source: local directory or remote tarball
# ---------------------------------------------------------------------------
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" 2>/dev/null && pwd || pwd)"
PLUGIN_SRC="$SCRIPT_DIR/plugin"
TMP_DIR=""

if [ -d "$PLUGIN_SRC" ] && [ -f "$PLUGIN_SRC/package.json" ]; then
  log "detected local plugin directory: $PLUGIN_SRC"
else
  log "no local plugin/ directory found, will download from GitHub Release"
  require_cmd curl "Install curl then retry"
  require_cmd tar  "Install tar then retry"

  TMP_DIR="$(mktemp -d)"

  # Build download URL
  if [ "$VERSION" = "latest" ]; then
    DOWNLOAD_URL="https://github.com/${GITHUB_REPO}/releases/latest/download/${TARBALL_NAME}"
  else
    DOWNLOAD_URL="https://github.com/${GITHUB_REPO}/releases/download/${VERSION}/${TARBALL_NAME}"
  fi

  log "downloading $DOWNLOAD_URL"
  if ! curl -fSL "$DOWNLOAD_URL" -o "$TMP_DIR/$TARBALL_NAME"; then
    log_error "failed to download release tarball"
    log_error "URL: $DOWNLOAD_URL"
    rm -rf "$TMP_DIR"
    exit 1
  fi

  log "extracting tarball"
  tar -xzf "$TMP_DIR/$TARBALL_NAME" -C "$TMP_DIR"
  PLUGIN_SRC="$TMP_DIR/plugin"

  if [ ! -f "$PLUGIN_SRC/package.json" ]; then
    log_error "tarball does not contain expected plugin/ directory"
    rm -rf "$TMP_DIR"
    exit 1
  fi
fi

# ---------------------------------------------------------------------------
# Rollback state
# ---------------------------------------------------------------------------
BACKUP_DIR=""
ROLLBACK_NEEDED="0"

cleanup() {
  local exit_code=$?
  if [ "$ROLLBACK_NEEDED" = "1" ] && [ -n "$BACKUP_DIR" ] && [ -d "$BACKUP_DIR" ]; then
    log "install failed (exit=$exit_code), rolling back..."
    rm -rf "$TARGET_DIR"
    mv "$BACKUP_DIR" "$TARGET_DIR"
    "$OPENCLAW_BIN" plugins install -l "$TARGET_DIR" </dev/null >/dev/null 2>&1 || true
    "$OPENCLAW_BIN" plugins enable "$PLUGIN_NAME" </dev/null >/dev/null 2>&1 || true
    "$OPENCLAW_BIN" gateway restart </dev/null >/dev/null 2>&1 || true
    log "rollback completed"
  fi
  # Clean up backup on success
  if [ "$exit_code" -eq 0 ] && [ -n "$BACKUP_DIR" ] && [ -d "$BACKUP_DIR" ]; then
    rm -rf "$BACKUP_DIR"
  fi
  # Clean up temp download directory
  if [ -n "$TMP_DIR" ] && [ -d "$TMP_DIR" ]; then
    rm -rf "$TMP_DIR"
  fi
}
trap cleanup EXIT

# ---------------------------------------------------------------------------
# Backup existing installation (if any)
# ---------------------------------------------------------------------------
if [ -d "$TARGET_DIR" ]; then
  BACKUP_DIR="${TARGET_DIR}.bak.$(date +%s)"
  log "backing up existing install to $BACKUP_DIR"
  mv "$TARGET_DIR" "$BACKUP_DIR"
fi
ROLLBACK_NEEDED="1"

# ---------------------------------------------------------------------------
# Copy plugin files to target directory
# ---------------------------------------------------------------------------
log "copying plugin files to $TARGET_DIR"
mkdir -p "$TARGET_DIR"
cp -r "$PLUGIN_SRC/"* "$TARGET_DIR/"

# ---------------------------------------------------------------------------
# Install npm dependencies (always install if node_modules is missing)
# ---------------------------------------------------------------------------
if [ ! -d "$TARGET_DIR/node_modules" ]; then
  NPM_BIN="${NPM_BIN:-npm}"
  require_cmd "$NPM_BIN" "Install Node.js + npm then retry: https://nodejs.org"
  log "installing npm dependencies"
  (
    cd "$TARGET_DIR"
    "$NPM_BIN" install --omit=dev
  )
else
  log "node_modules present, skipping npm install"
fi

# ---------------------------------------------------------------------------
# Verify plugin can be loaded
# ---------------------------------------------------------------------------
if [ -f "$TARGET_DIR/index.ts" ]; then
  log "plugin entry point found: index.ts"
elif [ -f "$TARGET_DIR/dist/index.js" ]; then
  if node -e "import('$TARGET_DIR/dist/index.js').then(() => process.exit(0)).catch(() => process.exit(1))" 2>/dev/null; then
    log "plugin sanity check passed"
  else
    log "warning: plugin sanity check inconclusive (may need runtime dependencies)"
  fi
fi

# ---------------------------------------------------------------------------
# Register and enable the channel plugin with OpenClaw
# ---------------------------------------------------------------------------
log "registering channel plugin with OpenClaw"

# Ensure cwd is valid — the backup step may have removed the directory the
# caller was sitting in, which causes Node.js (openclaw) to fail with ENOENT.
cd /

# Disable any previous version first
"$OPENCLAW_BIN" plugins disable "$PLUGIN_NAME" </dev/null >/dev/null 2>&1 || true

# Install from local path and enable
"$OPENCLAW_BIN" plugins install -l "$TARGET_DIR" </dev/null >/dev/null 2>&1 || true
"$OPENCLAW_BIN" plugins enable "$PLUGIN_NAME" </dev/null

# ---------------------------------------------------------------------------
# Installation succeeded -- disable rollback
# ---------------------------------------------------------------------------
ROLLBACK_NEEDED="0"

# ---------------------------------------------------------------------------
# Restart gateway to load and register the channel plugin
# ---------------------------------------------------------------------------
log "restarting OpenClaw gateway to register channel"
"$OPENCLAW_BIN" gateway restart </dev/null >/dev/null 2>&1 || true
sleep 3

# ---------------------------------------------------------------------------
# Migrate legacy config and write channel configuration
# After plugin registration, OpenClaw creates channels.* namespace.
# We migrate any legacy plugins.entries.astron-claw.config to the
# canonical channels.astron-claw location and remove the legacy entry.
# ---------------------------------------------------------------------------
log "configuring channel (name=$ACCOUNT_NAME, server=$SERVER_URL)"

OPENCLAW_CONFIG_PATH="$OPENCLAW_CONFIG_PATH" \
PLUGIN_NAME="$PLUGIN_NAME" \
ACCOUNT_NAME="$ACCOUNT_NAME" \
SERVER_URL="$SERVER_URL" \
BOT_TOKEN="$BOT_TOKEN" \
node -e "
const fs = require('fs');
const cfgPath = process.env.OPENCLAW_CONFIG_PATH;
const pluginName = process.env.PLUGIN_NAME;
const accountName = process.env.ACCOUNT_NAME;
const serverUrl = process.env.SERVER_URL;
const botToken = process.env.BOT_TOKEN;

// Read current config
const cfg = JSON.parse(fs.readFileSync(cfgPath, 'utf8'));

// Read legacy config if it exists
const legacyCfg = cfg?.plugins?.entries?.[pluginName]?.config ?? {};

// Build final channel config by merging:
// 1. Installation parameters (highest priority)
// 2. Legacy config optional fields (if not overridden)
const channelCfg = {
  enabled: true,
  name: accountName,
  bridge: {
    url: serverUrl,
    token: botToken
  },
  allowFrom: legacyCfg.allowFrom ?? ['*']
};

// Preserve optional fields from legacy config
if (legacyCfg.retry) {
  channelCfg.retry = legacyCfg.retry;
}
if (legacyCfg.media) {
  channelCfg.media = legacyCfg.media;
}

// Write to channels.astron-claw
if (!cfg.channels) cfg.channels = {};
cfg.channels[pluginName] = channelCfg;

// Clean up legacy config from plugins.entries.astron-claw.config
if (cfg.plugins?.entries?.[pluginName]?.config) {
  delete cfg.plugins.entries[pluginName].config;
  // Keep plugins.entries.astron-claw.enabled for plugin registration
  if (Object.keys(cfg.plugins.entries[pluginName]).length === 0) {
    cfg.plugins.entries[pluginName] = { enabled: true };
  }
}

// Write back to file
fs.writeFileSync(cfgPath, JSON.stringify(cfg, null, 2));
" </dev/null

if [ $? -ne 0 ]; then
  log_error "failed to write channel configuration"
  exit 1
fi

log "channel config written to channels.$PLUGIN_NAME"

# ---------------------------------------------------------------------------
# Register plugin in the trust allow-list so OpenClaw does not warn about
# "plugins.allow is empty" on every startup.
# ---------------------------------------------------------------------------
log "registering plugin in plugins.allow trust list"
OPENCLAW_CONFIG_PATH="$OPENCLAW_CONFIG_PATH" PLUGIN_NAME="$PLUGIN_NAME" OPENCLAW_BIN="$OPENCLAW_BIN" node -e "
  const fs = require('fs');
  const { execFileSync } = require('child_process');
  const cfgPath = process.env.OPENCLAW_CONFIG_PATH;
  const pluginName = process.env.PLUGIN_NAME;
  const cliBin = process.env.OPENCLAW_BIN;
  const cfg = JSON.parse(fs.readFileSync(cfgPath, 'utf8'));
  if (!cfg.plugins) cfg.plugins = {};
  const allow = cfg.plugins.allow || [];
  if (!allow.includes(pluginName)) allow.push(pluginName);
  try {
    execFileSync(cliBin, ['config', 'set', 'plugins.allow', '--json', JSON.stringify(allow)], { stdio: 'ignore' });
  } catch {
    cfg.plugins.allow = allow;
    fs.writeFileSync(cfgPath, JSON.stringify(cfg, null, 2));
  }
" </dev/null || log "warning: failed to update plugins.allow (non-fatal)"

# ---------------------------------------------------------------------------
# Restart gateway again to apply the new configuration
# ---------------------------------------------------------------------------
log "restarting OpenClaw gateway to apply configuration"
"$OPENCLAW_BIN" gateway restart </dev/null >/dev/null 2>&1 || true

log "done! astron-claw channel plugin installed successfully"
log "channel name: $ACCOUNT_NAME"
log "bridge server: $SERVER_URL"
log "plugin directory: $TARGET_DIR"

}

main "$@"
