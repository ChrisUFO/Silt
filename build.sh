#!/bin/bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")" && pwd)"
cd "$ROOT"

VERSION_FILE="$ROOT/VERSION"
DIST_DIR="$ROOT/distributions"
APP_NAME="silt"
PRODUCT_NAME="Silt"

# --- helpers ---
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log_info()  { printf "${GREEN}[INFO]${NC}  %s\n" "$*"; }
log_warn()  { printf "${YELLOW}[WARN]${NC}  %s\n" "$*"; }
log_error() { printf "${RED}[ERROR]${NC} %s\n" "$*"; }

check_tool() {
    if ! command -v "$1" &> /dev/null; then
        log_error "$1 is required but not found. Install it and re-run."
        exit 1
    fi
}

# Create a zip archive portably. `zip` is unavailable on Windows runners and
# recent windows-* images no longer ship 7-Zip either (actions/runner-images
# #9361), so fall back through 7z -> PowerShell Compress-Archive, which ships
# with every Windows install. Works for local builds (zip present) too.
# Usage: make_zip <archive.zip> <path>
make_zip() {
    local archive="$1" target="$2"
    if command -v zip &> /dev/null; then
        zip -9q "$archive" "$target"
    elif command -v 7z &> /dev/null; then
        7z a -bd -y -tzip "$archive" "$target" >/dev/null
    elif command -v powershell.exe &> /dev/null; then
        powershell.exe -NoProfile -Command "Compress-Archive -LiteralPath '$target' -DestinationPath '$archive' -CompressionLevel Optimal"
    else
        log_error "No zip tool available (need zip, 7z, or powershell.exe)."
        exit 1
    fi
}

# Bump the patch component of a semver string (echoes MAJOR.MINOR.PATCH+1).
bump_patch() {
    local major minor patch
    IFS='.' read -r major minor patch <<< "$1"
    patch=$((patch + 1))
    echo "${major}.${minor}.${patch}"
}

# --- args ---
# CI is the release authority (the Release workflow bumps and tags on merge),
# so a local build asks before advancing. --bump/--no-bump skip the prompt
# (CI passes --no-bump so it never blocks).
#   BUMP_REQUESTED: "" = prompt (default), "yes" = --bump, "no" = --no-bump
BUMP_REQUESTED=""
for arg in "$@"; do
    case "$arg" in
        --no-bump) BUMP_REQUESTED="no" ;;
        --bump)    BUMP_REQUESTED="yes" ;;
        -h|--help)
            echo "Usage: $0 [--no-bump|--bump]"
            echo "  (default)  prompt whether to bump the patch version (y/N)."
            echo "  --no-bump  never bump (CI / quick local rebuilds)."
            echo "  --bump     bump without prompting."
            echo "Releases are tagged automatically by the Release workflow on merge to main."
            exit 0 ;;
        *) log_error "Unknown option: $arg"; exit 1 ;;
    esac
done

# --- prereq checks ---
check_tool go
check_tool node
check_tool npm
check_tool wails
check_tool makensis

# --- read version & decide whether to advance -------------------------------
if [ ! -f "$VERSION_FILE" ]; then
    echo "0.1.0" > "$VERSION_FILE"
    log_info "Created VERSION file with 0.1.0"
fi
OLD_VERSION=$(tr -d '[:space:]' < "$VERSION_FILE")
CANDIDATE_VERSION="$(bump_patch "$OLD_VERSION")"

if [[ "$BUMP_REQUESTED" == "yes" ]]; then
    BUMP="yes"
elif [[ "$BUMP_REQUESTED" == "no" ]]; then
    BUMP="no"
else
    # Prompt only on an interactive TTY. In any non-interactive context
    # (piped input, CI) default to NO bump so we never block.
    if [[ -t 0 ]]; then
        read -rp "Bump patch version ${OLD_VERSION} -> ${CANDIDATE_VERSION}? [y/N] " ans || ans=""
        case "${ans:-n}" in
            y|Y|yes|YES) BUMP="yes" ;;
            *)           BUMP="no" ;;
        esac
    else
        BUMP="no"
    fi
fi

if [[ "$BUMP" == "yes" ]]; then
    VERSION="$CANDIDATE_VERSION"
    log_info "Building version: $OLD_VERSION -> $VERSION"
else
    VERSION="$OLD_VERSION"
    log_info "Building version: $VERSION (no bump)"
fi

# --- build frontend + backend + NSIS scaffolding ---
# Build frontend before wails because wails runs Go bindings generation
# first (which requires frontend/dist to exist for the embed directive).
log_info "Installing frontend dependencies..."
(cd "$ROOT/frontend" && npm install)

# --- generate app icon from logo.svg ---
log_info "Generating app icon from logo.svg..."
NODE_PATH="$ROOT/frontend/node_modules" node "$ROOT/scripts/generate-icon.mjs" \
    "$ROOT/frontend/src/assets/logo.svg" \
    "$ROOT/build/appicon.png"

log_info "Building frontend..."
(cd "$ROOT/frontend" && npm run build)

# Clean previous build artifacts (do this after frontend build so dist/ survives).
rm -rf "$ROOT/build/bin"

# Running --nsis populates build/windows/ (icon.ico) and builds the binary.
# --clean forces a full Go recompile (clears the build cache) so a stale
# embed or binary never ships. We skip wiping frontend/dist (done above).
log_info "Building with Wails..."
wails build --platform windows/amd64 --nsis --clean

BINARY="$ROOT/build/bin/${APP_NAME}.exe"
if [ ! -f "$BINARY" ]; then
    log_error "Binary not found at $BINARY. Build may have failed."
    exit 1
fi

log_info "Binary built: $BINARY"

# --- create distribution directory ---
BUILD_DIR="$DIST_DIR/v${VERSION}"
rm -rf "$BUILD_DIR"
mkdir -p "$BUILD_DIR"

# --- 1) portable .zip ---
log_info "Creating portable zip..."
ZIP_NAME="${APP_NAME}-v${VERSION}-windows-portable.zip"
cp "$BINARY" "$BUILD_DIR/${APP_NAME}.exe"
(cd "$BUILD_DIR" && make_zip "$ZIP_NAME" "${APP_NAME}.exe")
rm "$BUILD_DIR/${APP_NAME}.exe"
log_info "  -> $BUILD_DIR/$ZIP_NAME"

# --- 2) NSIS installer ---
# `wails build --nsis` (above) already compiled the installer into
# build/bin/<name>-amd64-installer.exe, passing makensis the binary path in
# native Windows form. Re-running makensis here was both redundant and broken
# under Git Bash: the MSYS-style path (/d/a/...) can't be resolved by native
# makensis ("Error in macro wails.files"). Wails is the single source of truth
# for the installer — copy its output with a versioned name.
INSTALLER_NAME="${APP_NAME}-v${VERSION}-windows-installer.exe"
NSIS_OUTPUT="$ROOT/build/bin/${APP_NAME}-amd64-installer.exe"

if [ ! -f "$NSIS_OUTPUT" ]; then
    log_error "Wails did not produce an installer at $NSIS_OUTPUT."
    log_error "Ensure 'wails build' was invoked with --nsis."
    exit 1
fi

log_info "Copying installer..."
cp "$NSIS_OUTPUT" "$BUILD_DIR/$INSTALLER_NAME"
log_info "  -> $BUILD_DIR/$INSTALLER_NAME"

# --- persist new version (only on success, and only if we bumped) ---
if [[ "$BUMP" == "yes" ]]; then
    echo "$VERSION" > "$VERSION_FILE"
    log_info "Version bumped to $VERSION"
fi

# --- summary ---
echo ""
echo "  ┌─────────────────────────────────────────────┐"
echo "  │  Build complete — version $VERSION"
echo "  ├─────────────────────────────────────────────┤"
echo "  │  Portable  : $ZIP_NAME"
echo "  │  Installer : $INSTALLER_NAME"
echo "  │  Location  : $BUILD_DIR"
echo "  └─────────────────────────────────────────────┘"
