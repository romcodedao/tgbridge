#!/bin/sh
# tgbridge installer: curl --proto '=https' --tlsv1.2 -fsSL <url>/install.sh | sh
set -eu

usage() {
  cat <<'USAGE'
Usage: install.sh [-h|--help]

Downloads the tgbridge release for this machine, verifies its sha256 and
installs it, then starts the setup wizard (tgbridge init).

Environment:
  TGBRIDGE_VERSION      release tag to install, e.g. v0.1.0 (default: latest)
  TGBRIDGE_INSTALL_DIR  where to put the binary (default: ~/.local/bin)
  TGBRIDGE_NO_INIT=1    install only; skip the setup wizard
  TGBRIDGE_BASE_URL     https releases URL (default: https://github.com/romcodedao/tgbridge/releases)

To remove tgbridge later: tgbridge uninstall (add --purge to delete its config and data)
USAGE
}

die() {
  echo "install.sh: $*" >&2
  exit 1
}

# Everything runs from main, called on the last line: a download cut short runs nothing.
main() {
  case "${1:-}" in
    -h | --help)
      usage
      exit 0
      ;;
    "") ;;
    *)
      usage >&2
      exit 2
      ;;
  esac

  case "$(uname -s)" in
    Linux) os=linux ;;
    Darwin) os=darwin ;;
    MINGW* | MSYS* | CYGWIN*) die "Windows: use WSL2 (install Claude Code and tgbridge inside WSL)" ;;
    *) die "unsupported OS: $(uname -s)" ;;
  esac
  case "$(uname -m)" in
    x86_64 | amd64) arch=amd64 ;;
    aarch64 | arm64) arch=arm64 ;;
    *) die "unsupported architecture: $(uname -m)" ;;
  esac

  base="${TGBRIDGE_BASE_URL:-https://github.com/romcodedao/tgbridge/releases}"
  case "$base" in
    https://*) ;;
    *) die "TGBRIDGE_BASE_URL must start with https:// (got $base)" ;;
  esac
  dir="${TGBRIDGE_INSTALL_DIR:-$HOME/.local/bin}"

  tag="${TGBRIDGE_VERSION:-}"
  if [ -z "$tag" ]; then
    url=$(curl --proto '=https' --tlsv1.2 -fsSLI -o /dev/null -w '%{url_effective}' "$base/latest") || die "could not look up the latest release"
    tag="${url##*/tag/}"
    [ "$tag" != "$url" ] && [ -n "$tag" ] || die "could not read the latest version from $url"
  fi
  case "$tag" in
    *[!A-Za-z0-9._-]*) die "unexpected version: $tag" ;;
    v[0-9]*.[0-9]*.[0-9]*) ;;
    *) die "unexpected version: $tag" ;;
  esac
  version="${tag#v}"
  archive="tgbridge_${version}_${os}_${arch}.tar.gz"

  tmp=$(mktemp -d) # global, so the EXIT trap still sees it after main returns
  trap 'rm -rf "$tmp"' EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM

  echo "Downloading tgbridge $tag ($os/$arch)"
  curl --proto '=https' --tlsv1.2 -fsSL -o "$tmp/$archive" "$base/download/$tag/$archive" || die "download failed: $base/download/$tag/$archive"
  curl --proto '=https' --tlsv1.2 -fsSL -o "$tmp/checksums.txt" "$base/download/$tag/checksums.txt" || die "download failed: checksums.txt"

  want=$(awk -v f="$archive" '$2 == f { print $1; exit }' "$tmp/checksums.txt")
  [ -n "$want" ] || die "no checksum listed for $archive"
  if command -v sha256sum >/dev/null 2>&1; then
    got=$(sha256sum "$tmp/$archive" | cut -d ' ' -f 1)
  elif command -v shasum >/dev/null 2>&1; then
    got=$(shasum -a 256 "$tmp/$archive" | cut -d ' ' -f 1)
  else
    die "need sha256sum or shasum to verify the download"
  fi
  [ "$got" = "$want" ] || die "checksum mismatch for $archive (expected $want, got $got)"

  tar -xzf "$tmp/$archive" -C "$tmp" tgbridge || die "could not extract tgbridge from $archive"
  mkdir -p "$dir"
  install -m 755 "$tmp/tgbridge" "$dir/tgbridge"
  echo "Installed $dir/tgbridge"

  bin=tgbridge
  case ":$PATH:" in
    *":$dir:"*) ;;
    *)
      bin="$dir/tgbridge"
      echo "$dir is not on your PATH. Add it with (and to your shell profile):"
      echo "  export PATH=\"$dir:\$PATH\""
      ;;
  esac

  [ "${TGBRIDGE_NO_INIT:-}" = 1 ] && exit 0

  # stdin is the curl pipe, so the wizard reads the terminal; /dev/tty can exist yet not open
  if ( : </dev/tty ) 2>/dev/null; then
    "$dir/tgbridge" init </dev/tty
  else
    echo "No terminal available. Finish setup in a terminal with:"
    echo "  run: \"$bin\" init"
  fi
}

main "$@"
