#!/bin/sh
set -eu

REPO="infinityOrga/helm-deep-pack"
BINARY="helm-deep-pack"
INSTALL_DIR="${HELM_DEEP_PACK_INSTALL:-/usr/local/bin}"
VERSION="${HELM_DEEP_PACK_VERSION:-latest}"

log() {
  printf '%s\n' "$*"
}

fail() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || fail "required command not found: $1"
}

detect_os() {
  case "$(uname -s)" in
    Linux) printf 'linux' ;;
    Darwin) printf 'darwin' ;;
    *) fail "unsupported OS: $(uname -s). Download a release manually: https://github.com/$REPO/releases" ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    x86_64 | amd64) printf 'amd64' ;;
    arm64 | aarch64) printf 'arm64' ;;
    *) fail "unsupported architecture: $(uname -m). Download a release manually: https://github.com/$REPO/releases" ;;
  esac
}

extract_json_value() {
  key="$1"
  printf '%s' "$RELEASE_JSON" | sed -n "s/.*\"$key\":\"\\([^\"]*\\)\".*/\\1/p" | head -n1
}

require_cmd curl
require_cmd tar
require_cmd uname
require_cmd mktemp
require_cmd grep
require_cmd sed

OS="$(detect_os)"
ARCH="$(detect_arch)"

RELEASE_API="https://api.github.com/repos/$REPO/releases/latest"
if [ "$VERSION" != "latest" ]; then
  RELEASE_API="https://api.github.com/repos/$REPO/releases/tags/$VERSION"
fi

RELEASE_JSON="$(curl -fsSL "$RELEASE_API")" || fail "failed to fetch release metadata"

TAG_NAME="$(extract_json_value tag_name)"
[ -n "$TAG_NAME" ] || TAG_NAME="$VERSION"

ASSET_PATTERN="https://[^\"]*/${BINARY}_[^\"]*_${OS}_${ARCH}\\.tar\\.gz"
ARCHIVE_URL="$(printf '%s' "$RELEASE_JSON" | grep -Eo "$ASSET_PATTERN" | head -n1 || true)"
[ -n "$ARCHIVE_URL" ] || fail "no release archive found for ${OS}/${ARCH}"

CHECKSUM_URL="$(printf '%s' "$RELEASE_JSON" | grep -Eo 'https://[^"]*/checksums\.txt' | head -n1 || true)"

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT INT TERM

ARCHIVE_NAME="$(basename "$ARCHIVE_URL")"
ARCHIVE_PATH="$TMP_DIR/$ARCHIVE_NAME"

log "Installing $BINARY ($TAG_NAME) for ${OS}/${ARCH}..."
curl -fL "$ARCHIVE_URL" -o "$ARCHIVE_PATH" || fail "failed to download archive"

if [ -n "$CHECKSUM_URL" ]; then
  CHECKSUM_PATH="$TMP_DIR/checksums.txt"
  curl -fL "$CHECKSUM_URL" -o "$CHECKSUM_PATH" || fail "failed to download checksums"
  CHECKSUM_LINE="$(grep "  $ARCHIVE_NAME\$" "$CHECKSUM_PATH" || true)"
  [ -n "$CHECKSUM_LINE" ] || fail "checksum entry not found for $ARCHIVE_NAME"

  if command -v sha256sum >/dev/null 2>&1; then
    printf '%s\n' "$CHECKSUM_LINE" | (cd "$TMP_DIR" && sha256sum -c - >/dev/null) || fail "checksum verification failed"
  elif command -v shasum >/dev/null 2>&1; then
    EXPECTED_SUM="$(printf '%s' "$CHECKSUM_LINE" | awk '{print $1}')"
    ACTUAL_SUM="$(shasum -a 256 "$ARCHIVE_PATH" | awk '{print $1}')"
    [ "$EXPECTED_SUM" = "$ACTUAL_SUM" ] || fail "checksum verification failed"
  else
    log "Warning: sha256 checksum tool not found; skipping checksum verification."
  fi
fi

tar -xzf "$ARCHIVE_PATH" -C "$TMP_DIR" "$BINARY" || fail "failed to unpack archive"

mkdir -p "$INSTALL_DIR" || fail "failed to create install directory: $INSTALL_DIR"
if [ -w "$INSTALL_DIR" ]; then
  cp "$TMP_DIR/$BINARY" "$INSTALL_DIR/$BINARY" || fail "failed to install binary to $INSTALL_DIR/$BINARY"
  chmod 0755 "$INSTALL_DIR/$BINARY" || fail "failed to set executable bit on $INSTALL_DIR/$BINARY"
elif command -v sudo >/dev/null 2>&1; then
  sudo cp "$TMP_DIR/$BINARY" "$INSTALL_DIR/$BINARY" || fail "failed to install binary to $INSTALL_DIR/$BINARY (sudo)"
  sudo chmod 0755 "$INSTALL_DIR/$BINARY" || fail "failed to set executable bit on $INSTALL_DIR/$BINARY (sudo)"
else
  fail "install dir is not writable: $INSTALL_DIR (set HELM_DEEP_PACK_INSTALL to a writable directory)"
fi

log "Installed to $INSTALL_DIR/$BINARY"

case ":$PATH:" in
  *":$INSTALL_DIR:"*)
    log "Run: $BINARY --help"
    ;;
  *)
    log "Add this directory to PATH:"
    log "  export PATH=\"$INSTALL_DIR:\$PATH\""
    ;;
esac
