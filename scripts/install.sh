#!/bin/sh
set -eu

fail() { printf '%s\n' "readback install: $*" >&2; exit 1; }
case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) fail "unsupported OS: $(uname -s)" ;;
esac
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) fail "unsupported architecture: $(uname -m)" ;;
esac
command -v curl >/dev/null 2>&1 || fail 'curl is required'
command -v tar >/dev/null 2>&1 || fail 'tar is required'
if command -v sha256sum >/dev/null 2>&1; then
  hash_tool=sha256sum
elif command -v shasum >/dev/null 2>&1; then
  hash_tool=shasum
else
  fail 'sha256sum or shasum is required'
fi
if [ -z "${READBACK_INSTALL_DIR:-}" ] && [ -z "${HOME:-}" ]; then fail 'HOME is not set; set READBACK_INSTALL_DIR'; fi
install_dir=${READBACK_INSTALL_DIR:-"$HOME/.local/bin"}
case "$install_dir" in /*) ;; *) install_dir="$PWD/$install_dir" ;; esac
tmp=$(mktemp -d) || fail 'cannot create temporary directory'
trap 'rm -f "$tmp/release.json" "$tmp/checksums.txt" "$tmp/archive.tar.gz" "$tmp/readback"; rmdir "$tmp"' 0
trap 'exit 1' HUP INT TERM
version=${READBACK_VERSION:-latest}
if [ "$version" = latest ]; then
  curl -fsSL --proto '=https' --proto-redir '=https' https://api.github.com/repos/joshduffy/readback/releases/latest -o "$tmp/release.json"
  version=$(sed -n 's/^[[:space:]]*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$tmp/release.json")
fi
case "$version" in v*) tag=$version; version=${version#v} ;; *) tag=v$version ;; esac
case "$version" in ''|*[!0-9A-Za-z.+-]*) fail 'invalid release version' ;; esac
archive="readback_${version}_${os}_${arch}.tar.gz"
base="https://github.com/joshduffy/readback/releases/download/$tag"
curl -fsSL --proto '=https' --proto-redir '=https' "$base/$archive" -o "$tmp/archive.tar.gz"
curl -fsSL --proto '=https' --proto-redir '=https' "$base/checksums.txt" -o "$tmp/checksums.txt"
expected=$(awk -v file="$archive" '$2 == file {print $1}' "$tmp/checksums.txt")
[ "${#expected}" -eq 64 ] || fail 'missing or ambiguous checksum'
case "$expected" in *[!0-9a-fA-F]*) fail 'invalid checksum' ;; esac
if [ "$hash_tool" = sha256sum ]; then
  actual=$(sha256sum "$tmp/archive.tar.gz")
else
  actual=$(shasum -a 256 "$tmp/archive.tar.gz")
fi
actual=${actual%% *}
[ "$actual" = "$expected" ] || fail 'checksum mismatch'
# Stream only the binary so archive paths cannot escape the temporary directory.
tar -xzOf "$tmp/archive.tar.gz" readback > "$tmp/readback"
[ -s "$tmp/readback" ] || fail 'archive has no binary'
mkdir -p "$install_dir"
# Never write through an existing destination (symlink or hardlink): unlink it, then
# rename a fresh file into place so the only inode touched is one we created.
if [ -e "$install_dir/readback" ] || [ -L "$install_dir/readback" ]; then
  [ ! -d "$install_dir/readback" ] || fail 'destination is a directory'
  rm -f "$install_dir/readback" || fail 'cannot replace existing destination'
fi
chmod 755 "$tmp/readback"
mv -f "$tmp/readback" "$install_dir/readback" 2>/dev/null || {
  cp "$tmp/readback" "$install_dir/readback.tmp.$$" && chmod 755 "$install_dir/readback.tmp.$$" && mv -f "$install_dir/readback.tmp.$$" "$install_dir/readback"
} || fail 'cannot write destination'
printf 'Installed %s\nRun %s doctor to check provider access.\n' "$install_dir/readback" "$install_dir/readback"
