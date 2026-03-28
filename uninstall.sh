#!/usr/bin/env bash
set -euo pipefail

# ---------------------------------------------------------------------------
# astron-claw uninstaller
# Removes the astron-claw OpenClaw channel plugin, its configuration,
# and optionally a legacy bridge server installation from older releases.
#
# Wrapped in main() so `curl ... | bash` reads the entire script before
# executing — prevents sub-commands from consuming stdin.
# ---------------------------------------------------------------------------

main() {

OPENCLAW_BIN="${OPENCLAW_BIN:-openclaw}"
PLUGIN_NAME="astron-claw"
TARGET_DIR="${TARGET_DIR:-$HOME/.openclaw/extensions/astron-claw}"
SERVER_DIR="${SERVER_DIR:-$HOME/.openclaw/astron-claw-server}"

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------
usage() {
  cat <<'USAGE'
Usage:
  uninstall.sh [options]

Options:
  --target-dir <path>       Plugin install directory
                            (default: ~/.openclaw/extensions/astron-claw)
  --keep-config             Do not remove channel config from openclaw.json
  --with-server             Also remove a legacy local bridge server directory
  --server-dir <path>       Legacy server install directory
                            (default: ~/.openclaw/astron-claw-server)
  -y, --yes                 Skip confirmation prompt
  -h, --help                Show this help message
USAGE
}

log() {
  printf "[astron-uninstall] %s\n" "$*"
}

log_error() {
  printf "[astron-uninstall] ERROR: %s\n" "$*" >&2
}

# ---------------------------------------------------------------------------
# Parse arguments
# ---------------------------------------------------------------------------
SKIP_CONFIRM="0"
KEEP_CONFIG="0"
WITH_SERVER="0"

while [ "$#" -gt 0 ]; do
  case "$1" in
    --target-dir)
      if [ "$#" -lt 2 ]; then
        log_error "missing value for $1"
        exit 1
      fi
      TARGET_DIR="$2"
      shift 2
      ;;
    --keep-config)
      KEEP_CONFIG="1"
      shift
      ;;
    --with-server)
      WITH_SERVER="1"
      shift
      ;;
    --server-dir)
      if [ "$#" -lt 2 ]; then
        log_error "missing value for $1"
        exit 1
      fi
      SERVER_DIR="$2"
      shift 2
      ;;
    -y|--yes)
      SKIP_CONFIRM="1"
      shift
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
# Confirmation
# ---------------------------------------------------------------------------
if [ "$SKIP_CONFIRM" != "1" ]; then
  printf "[astron-uninstall] This will remove the astron-claw channel plugin.\n"
  printf "[astron-uninstall]   Plugin directory: %s\n" "$TARGET_DIR"
  if [ "$WITH_SERVER" = "1" ]; then
    printf "[astron-uninstall]   Legacy server directory: %s\n" "$SERVER_DIR"
  fi
  printf "[astron-uninstall] Continue? [y/N] "
  read -r answer </dev/tty || { log_error "cannot read from terminal (use -y for non-interactive mode)"; exit 1; }
  case "$answer" in
    [yY]|[yY][eE][sS]) ;;
    *)
      log "aborted"
      exit 0
      ;;
  esac
fi

# ---------------------------------------------------------------------------
# Check for openclaw CLI
# ---------------------------------------------------------------------------
HAS_OPENCLAW="0"
if command -v "$OPENCLAW_BIN" >/dev/null 2>&1; then
  HAS_OPENCLAW="1"
fi

# ---------------------------------------------------------------------------
# Disable and unregister plugin
# ---------------------------------------------------------------------------
if [ "$HAS_OPENCLAW" = "1" ]; then
  log "disabling plugin"
  "$OPENCLAW_BIN" plugins disable "$PLUGIN_NAME" </dev/null >/dev/null 2>&1 || true

  log "unregistering plugin"
  "$OPENCLAW_BIN" plugins uninstall "$PLUGIN_NAME" </dev/null >/dev/null 2>&1 || true

  if [ "$KEEP_CONFIG" != "1" ]; then
    log "removing channel config"
    # Remove channel config (canonical location)
    "$OPENCLAW_BIN" config set "channels.$PLUGIN_NAME" --json "null" </dev/null >/dev/null 2>&1 || true
    # Also clean up legacy plugins.entries path if present
    "$OPENCLAW_BIN" config set "plugins.entries.$PLUGIN_NAME" --json "null" </dev/null >/dev/null 2>&1 || true
    # Remove plugin from plugins.allow trust list
    if command -v node >/dev/null 2>&1; then
      log "removing plugin from plugins.allow trust list"
      OPENCLAW_CONFIG_PATH="${OPENCLAW_CONFIG_PATH:-$HOME/.openclaw/openclaw.json}" PLUGIN_NAME="$PLUGIN_NAME" OPENCLAW_BIN="$OPENCLAW_BIN" node -e "
        const fs = require('fs');
        const { execFileSync } = require('child_process');
        const cfgPath = process.env.OPENCLAW_CONFIG_PATH;
        const pluginName = process.env.PLUGIN_NAME;
        const cliBin = process.env.OPENCLAW_BIN;
        const cfg = JSON.parse(fs.readFileSync(cfgPath, 'utf8'));
        const allow = (cfg.plugins && cfg.plugins.allow) || [];
        const filtered = allow.filter(n => n !== pluginName);
        try {
          execFileSync(cliBin, ['config', 'set', 'plugins.allow', '--json', JSON.stringify(filtered)], { stdio: 'ignore' });
        } catch {
          if (!cfg.plugins) cfg.plugins = {};
          cfg.plugins.allow = filtered;
          fs.writeFileSync(cfgPath, JSON.stringify(cfg, null, 2));
        }
      " </dev/null 2>/dev/null || log "warning: failed to update plugins.allow (non-fatal)"
    else
      log "warning: node not found, skipping plugins.allow cleanup"
    fi
  else
    log "keeping config (--keep-config)"
  fi
else
  log "openclaw CLI not found, skipping plugin unregister"
fi

# ---------------------------------------------------------------------------
# Remove plugin files (must happen BEFORE gateway restart to prevent
# OpenClaw from auto-discovering the plugin in the extensions directory)
# ---------------------------------------------------------------------------
if [ -d "$TARGET_DIR" ]; then
  log "removing plugin directory: $TARGET_DIR"
  rm -rf "$TARGET_DIR"
else
  log "plugin directory not found: $TARGET_DIR (already removed?)"
fi

# Clean up any leftover backup directories
for bak in "${TARGET_DIR}.bak."*; do
  if [ -d "$bak" ]; then
    log "removing leftover backup: $bak"
    rm -rf "$bak"
  fi
done

# ---------------------------------------------------------------------------
# Remove server installation (if requested)
# ---------------------------------------------------------------------------
if [ "$WITH_SERVER" = "1" ]; then
  if [ -d "$SERVER_DIR" ]; then
    # Remove media directory first (log explicitly as it may contain user data)
    if [ -d "$SERVER_DIR/media" ]; then
      log "removing legacy media directory: $SERVER_DIR/media"
    fi
    log "removing legacy server directory: $SERVER_DIR"
    rm -rf "$SERVER_DIR"
  else
    log "legacy server directory not found: $SERVER_DIR (already removed?)"
  fi
fi

# ---------------------------------------------------------------------------
# Restart gateway (after files are removed so plugin won't be re-discovered)
# ---------------------------------------------------------------------------
if [ "$HAS_OPENCLAW" = "1" ]; then
  log "restarting OpenClaw gateway"
  "$OPENCLAW_BIN" gateway restart </dev/null >/dev/null 2>&1 || true
fi

log "done! astron-claw channel plugin has been removed"
if [ "$WITH_SERVER" = "1" ]; then
  log "legacy bridge server directory has also been removed"
fi

}

main "$@"
