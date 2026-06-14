#!/usr/bin/env bash
# Linux build script for Silt — mirrors build.sh (Windows / Git Bash) but
# targets linux/amd64. Produces two universal artifacts:
#   1) AppImage  — single self-contained executable (Ubuntu / Fedora / Arch /
#                  Debian / Mint / openSUSE; no install required).
#   2) .deb      — Debian/Ubuntu-native package (apt install, desktop menu,
#                  icon, libwebkit2gtk-4.1 dependency declared).
#
# Both artifacts rely on the host providing libwebkit2gtk-4.1 (or -4.0 on older
# distros). Bundling webkit would bloat the packages ~10x and is intentionally
# avoided; Silt follows the standard Wails dependency story.
#
# Usage:  ./build-linux.sh            # prompt: bump patch version? (y/N)
#         ./build-linux.sh --no-bump  # never bump (used by CI, or quick rebuilds)
#         ./build-linux.sh --bump     # bump without prompting
set -euo pipefail

ROOT="$(cd "$(dirname "$0")" && pwd)"
cd "$ROOT"

VERSION_FILE="$ROOT/VERSION"
DIST_DIR="$ROOT/distributions"
APP_NAME="silt"
PRODUCT_NAME="Silt"
APPIMAGETOOL_URL="https://github.com/AppImage/AppImageKit/releases/download/continuous/appimagetool-x86_64.AppImage"
APPIMAGETOOL_CACHE="${APPIMAGETOOL_CACHE:-$HOME/.cache/silt/appimagetool-x86_64.AppImage}"

# --- helpers ---
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log_info()  { printf "${GREEN}[INFO]${NC}  %s\n" "$*"; }
log_warn()  { printf "${YELLOW}[WARN]${NC} %s\n" "$*"; }
log_error() { printf "${RED}[ERROR]${NC} %s\n" "$*"; }

# --- args ---
# BUMP_REQUESTED: "" = prompt interactively (default), "yes" = --bump,
#                 "no" = --no-bump. CI passes --no-bump so it never blocks.
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

check_tool() {
    if ! command -v "$1" &> /dev/null; then
        log_error "$1 is required but not found. Install it and re-run."
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

# --- prereq checks ---
check_tool go
check_tool node
check_tool npm
check_tool wails
check_tool gcc
check_tool dpkg-deb

# webkit2gtk is required to compile the Wails webview layer. Ubuntu 24.04+
# ships only webkit2gtk-4.1 (the 4.0 dev package was dropped); older distros
# (Ubuntu 22.04, Debian bullseye) ship 4.0. Detect whichever is present and
# set the matching Wails build tag so the binary links against the installed ABI.
WEBKIT_TAG=""
if pkg-config --exists webkit2gtk-4.1 2>/dev/null; then
    WEBKIT_TAG="webkit2_41"
    WEBKIT_DEB="libwebkit2gtk-4.1-0"
    log_info "Found webkit2gtk-4.1 (building with -tags $WEBKIT_TAG)"
elif pkg-config --exists webkit2gtk-4.0 2>/dev/null; then
    WEBKIT_TAG=""
    WEBKIT_DEB="libwebkit2gtk-4.0-37"
    log_info "Found webkit2gtk-4.0 (default Wails tag)"
else
    log_error "Neither webkit2gtk-4.1 nor webkit2gtk-4.0 was found."
    log_error "Install the dev package, e.g.:"
    log_error "  Ubuntu 24.04: sudo apt install libwebkit2gtk-4.1-dev"
    log_error "  Ubuntu 22.04: sudo apt install libwebkit2gtk-4.0-dev"
    exit 1
fi

# --- read version & decide whether to advance -------------------------------
# CI releases on merge; locally we ask before advancing so test builds don't
# create spurious versions. --bump/--no-bump skip the prompt (CI uses --no-bump).
# Check existence BEFORE reading: under `set -euo pipefail`, reading a missing
# file via redirection exits the script before this create-block could run.
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
    # (piped input, CI without --no-bump) default to NO bump so we never block.
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

# --- frontend + icon ---
log_info "Installing frontend dependencies..."
(cd "$ROOT/frontend" && npm install)

log_info "Generating app icon from logo.svg..."
NODE_PATH="$ROOT/frontend/node_modules" node "$ROOT/scripts/generate-icon.mjs" \
    "$ROOT/frontend/src/assets/logo.svg" \
    "$ROOT/build/appicon.png"

log_info "Building frontend..."
(cd "$ROOT/frontend" && npm run build)

# --- backend build (linux/amd64) ---
rm -rf "$ROOT/build/bin"

log_info "Building with Wails (linux/amd64)..."
WAILS_ARGS=(build --platform linux/amd64)
if [[ -n "$WEBKIT_TAG" ]]; then
    WAILS_ARGS+=(-tags "$WEBKIT_TAG")
fi
wails "${WAILS_ARGS[@]}"

BINARY="$ROOT/build/bin/${APP_NAME}"
if [ ! -f "$BINARY" ]; then
    log_error "Binary not found at $BINARY. Build may have failed."
    exit 1
fi
log_info "Binary built: $BINARY ($(du -h "$BINARY" | cut -f1))"

# --- distribution directory ---
BUILD_DIR="$DIST_DIR/v${VERSION}"
rm -rf "$BUILD_DIR"
mkdir -p "$BUILD_DIR"

# Shared XDG desktop entry used by both artifacts.
DESKTOP_FILE="$BUILD_DIR/${APP_NAME}.desktop"
cat > "$DESKTOP_FILE" <<EOF
[Desktop Entry]
Type=Application
Name=${PRODUCT_NAME}
GenericName=Journal & Task Manager
Comment=Local-first hybrid journal and task manager
Exec=${APP_NAME}
Icon=${APP_NAME}
Terminal=false
Categories=Office;Utility;TextEditor;
StartupWMClass=${PRODUCT_NAME}
EOF

# --- 1) AppImage (universal single-file) ------------------------------------
APPIMAGE_NAME="${PRODUCT_NAME}-${VERSION}-x86_64.AppImage"

fetch_appimagetool() {
    if [[ -x "$APPIMAGETOOL_CACHE" ]]; then
        return 0
    fi
    log_info "Downloading appimagetool..."
    if ! command -v curl &> /dev/null; then
        log_warn "curl not found; skipping AppImage (install curl to enable it)."
        return 1
    fi
    mkdir -p "$(dirname "$APPIMAGETOOL_CACHE")"
    if ! curl -sSL --fail -o "$APPIMAGETOOL_CACHE" "$APPIMAGETOOL_URL"; then
        log_warn "Failed to download appimagetool; skipping AppImage."
        rm -f "$APPIMAGETOOL_CACHE"
        return 1
    fi
    chmod +x "$APPIMAGETOOL_CACHE"
}

build_appimage() {
    if ! fetch_appimagetool; then
        return 1
    fi

    local temp_dir appdir status=0
    temp_dir="$(mktemp -d)"
    appdir="$temp_dir/${PRODUCT_NAME}.AppDir"
    # Always clean up the staging dir (contains the full binary copy) so /tmp
    # doesn't accumulate large artifacts across runs.
    trap 'rm -rf "$temp_dir"' RETURN
    mkdir -p "$appdir/usr/bin" \
             "$appdir/usr/share/applications" \
             "$appdir/usr/share/icons/hicolor/1024x1024/apps"

    cp "$BINARY" "$appdir/usr/bin/${APP_NAME}"
    chmod 755 "$appdir/usr/bin/${APP_NAME}"
    cp "$ROOT/build/appicon.png" "$appdir/usr/share/icons/hicolor/1024x1024/apps/${APP_NAME}.png"
    cp "$DESKTOP_FILE" "$appdir/usr/share/applications/${APP_NAME}.desktop"
    cp "$DESKTOP_FILE" "$appdir/${APP_NAME}.desktop"
    ln -sf "usr/bin/${APP_NAME}" "$appdir/AppRun"
    ln -sf "usr/share/icons/hicolor/1024x1024/apps/${APP_NAME}.png" "$appdir/.DirIcon"
    ln -sf "usr/share/icons/hicolor/1024x1024/apps/${APP_NAME}.png" "$appdir/${APP_NAME}.png"

    # --appimage-extract-and-run avoids needing a FUSE mount at build time
    # (e.g. inside containers or WSL where FUSE may be unavailable).
    if ARCH=x86_64 "$APPIMAGETOOL_CACHE" --appimage-extract-and-run \
            "$appdir" "$BUILD_DIR/$APPIMAGE_NAME" >/dev/null 2>&1; then
        chmod +x "$BUILD_DIR/$APPIMAGE_NAME"
        log_info "  -> $BUILD_DIR/$APPIMAGE_NAME"
    else
        log_warn "appimagetool failed; skipping AppImage."
        status=1
    fi
    rm -rf "$temp_dir"
    trap - RETURN
    return "$status"
}

log_info "Building AppImage..."
if build_appimage; then
    APPIMAGE_OK=1
else
    APPIMAGE_OK=0
fi

# --- 2) .deb (Debian/Ubuntu native) ----------------------------------------
DEB_NAME="${APP_NAME}_${VERSION}_amd64.deb"

log_info "Building .deb package..."
DEB_TEMP_DIR="$(mktemp -d)"
DEBROOT="$DEB_TEMP_DIR/${APP_NAME}_${VERSION}_amd64"
mkdir -p "$DEBROOT/DEBIAN" \
         "$DEBROOT/usr/bin" \
         "$DEBROOT/usr/share/applications" \
         "$DEBROOT/usr/share/icons/hicolor/1024x1024/apps" \
         "$DEBROOT/usr/share/doc/${APP_NAME}"

cp "$BINARY" "$DEBROOT/usr/bin/${APP_NAME}"
chmod 755 "$DEBROOT/usr/bin/${APP_NAME}"
cp "$ROOT/build/appicon.png" "$DEBROOT/usr/share/icons/hicolor/1024x1024/apps/${APP_NAME}.png"
cp "$DESKTOP_FILE" "$DEBROOT/usr/share/applications/${APP_NAME}.desktop"
cat > "$DEBROOT/usr/share/doc/${APP_NAME}/copyright" <<EOF
Copyright (c) $(date +%Y) ChrisUFO. Licensed per the project LICENSE.
EOF
cat > "$DEBROOT/usr/share/doc/${APP_NAME}/changelog.Debian" <<EOF
${APP_NAME} (${VERSION}) stable; urgency=low

  * Linux package build.
EOF
gzip -9n "$DEBROOT/usr/share/doc/${APP_NAME}/changelog.Debian"

INSTALLED_SIZE_KB="$(du -sk "$BINARY" | cut -f1)"
cat > "$DEBROOT/DEBIAN/control" <<EOF
Package: ${APP_NAME}
Version: ${VERSION}
Section: office
Priority: optional
Architecture: amd64
Depends: ${WEBKIT_DEB}
Maintainer: ChrisUFO <chrisufo@users.noreply.github.com>
Installed-Size: ${INSTALLED_SIZE_KB}
Description: ${PRODUCT_NAME} - local-first hybrid journal and task manager
 Plain-text Markdown on your drive, real-time index in memory.
EOF

dpkg-deb --build --root-owner-group "$DEBROOT" "$BUILD_DIR/$DEB_NAME" >/dev/null
rm -rf "$DEB_TEMP_DIR"
log_info "  -> $BUILD_DIR/$DEB_NAME"
DEB_OK=1

# Remove the loose .desktop helper from the dist dir (it lives inside the packages).
rm -f "$BUILD_DIR/${APP_NAME}.desktop"

# --- persist new version (only on success, and only if we bumped) ---
if [[ "$BUMP" == "yes" ]]; then
    echo "$VERSION" > "$VERSION_FILE"
    log_info "Version bumped to $VERSION"
fi

# --- summary ---
echo ""
if [[ "$APPIMAGE_OK" -eq 1 && "$DEB_OK" -eq 1 ]]; then
    echo "  ┌─────────────────────────────────────────────┐"
    echo "  │  Build complete — version $VERSION"
    echo "  ├─────────────────────────────────────────────┤"
    echo "  │  AppImage : $APPIMAGE_NAME"
    echo "  │  Deb      : $DEB_NAME"
    echo "  │  Location : $BUILD_DIR"
    echo "  └─────────────────────────────────────────────┘"
elif [[ "$DEB_OK" -eq 1 ]]; then
    echo "  ┌─────────────────────────────────────────────┐"
    echo "  │  Build PARTIAL — version $VERSION"
    echo "  ├─────────────────────────────────────────────┤"
    echo "  │  AppImage : SKIPPED (see warnings above)"
    echo "  │  Deb      : $DEB_NAME"
    echo "  │  Location : $BUILD_DIR"
    echo "  └─────────────────────────────────────────────┘"
    exit 2
else
    log_error "Both packaging steps failed."
    exit 1
fi
