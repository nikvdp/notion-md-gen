#!/usr/bin/env sh
set -eu

VERSION="2.0"
REPO="${NOTION_MD_GEN_REPO:-nikvdp/notion-md-gen}"
BIN_DIR="${NOTION_MD_GEN_BIN_DIR:-/usr/local/bin}"

printf "notion-md-gen installer %s\n" "$VERSION"
printf "repo: %s\n" "$REPO"

uname_s="$(uname -s)"
uname_m="$(uname -m)"

case "$uname_s" in
Darwin)
  os="macos"
  ;;
Linux)
  os="linux"
  ;;
*)
  echo "unsupported OS: $uname_s"
  exit 1
  ;;
esac

case "$uname_m" in
x86_64|amd64)
  arch="amd64"
  ;;
arm64|aarch64)
  arch="arm64"
  ;;
*)
  echo "unsupported architecture: $uname_m"
  exit 1
  ;;
esac

asset_suffix="${os}-${arch}.tar.gz"

release_json="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest")"
asset_url="$(printf "%s" "$release_json" | grep "browser_download_url.*${asset_suffix}\"" | cut -d '"' -f 4 | head -n 1)"

if [ -z "$asset_url" ]; then
  echo "could not find release asset matching ${asset_suffix}"
  echo "set NOTION_MD_GEN_REPO=<owner/repo> if needed"
  exit 1
fi

printf "asset: %s\n" "$asset_url"
printf "install dir: %s\n" "$BIN_DIR"

mkdir -p "$BIN_DIR"
tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

curl -fsSL "$asset_url" -o "$tmpdir/notion-md-gen.tar.gz"
tar -xzf "$tmpdir/notion-md-gen.tar.gz" -C "$tmpdir"

if [ ! -f "$tmpdir/notion-md-gen" ]; then
  echo "release archive did not contain notion-md-gen binary"
  exit 1
fi

install -m 0755 "$tmpdir/notion-md-gen" "$BIN_DIR/notion-md-gen"

echo "installation complete"
"$BIN_DIR/notion-md-gen" version || true
