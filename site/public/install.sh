#!/bin/sh
# StudyHomie installer for macOS and Linux:
#   curl -fsSL https://studyhomie.maxi42.space/install.sh | sh
#
# Downloads the latest release (or $STUDYHOMIE_VERSION), checks its SHA-256 against the
# release's checksums.txt and puts the binary in ~/.local/bin (or $STUDYHOMIE_BIN_DIR).
# Running it again updates StudyHomie.
set -eu

repo=krilinxito/studyhomie
bin_dir=${STUDYHOMIE_BIN_DIR:-"$HOME/.local/bin"}

fail() { echo "studyhomie: $*" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || fail "'$1' is required"; }
need curl
need tar
need uname

v=${STUDYHOMIE_VERSION:-}
if [ -z "$v" ]; then
  v=$(curl -fsSL -H 'User-Agent: studyhomie-installer' "https://api.github.com/repos/$repo/releases/latest" |
    sed -n 's/.*"tag_name"[^"]*"\([^"]*\)".*/\1/p' | head -n 1)
fi
[ -n "$v" ] || fail "could not find the latest version"
case "$v" in v*) ;; *) v="v$v" ;; esac

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
  linux | darwin) ;;
  *) fail "unsupported system '$os' (on Windows, use install.ps1)" ;;
esac
arch=$(uname -m)
case "$arch" in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) fail "unsupported architecture '$arch'" ;;
esac

name="studyhomie_${v}_${os}_${arch}"
base="https://github.com/$repo/releases/download/$v"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

echo "Downloading StudyHomie $v ($os/$arch)..."
curl -fsSL "$base/$name.tar.gz" -o "$tmp/$name.tar.gz"
curl -fsSL "$base/checksums.txt" -o "$tmp/checksums.txt"

want=$(awk -v f="$name.tar.gz" '$2 == f || $2 == "*" f { print $1 }' "$tmp/checksums.txt")
[ -n "$want" ] || fail "checksums.txt has no entry for $name.tar.gz"
if command -v sha256sum >/dev/null 2>&1; then
  got=$(sha256sum "$tmp/$name.tar.gz" | cut -d ' ' -f 1)
elif command -v shasum >/dev/null 2>&1; then
  got=$(shasum -a 256 "$tmp/$name.tar.gz" | cut -d ' ' -f 1)
else
  fail "sha256sum or shasum is required to verify the download"
fi
[ "$want" = "$got" ] || fail "checksum mismatch for $name.tar.gz (expected $want, got $got). Nothing was installed."

tar -xzf "$tmp/$name.tar.gz" -C "$tmp"
mkdir -p "$bin_dir"
# write next to the target and rename, so a running copy is never half-overwritten
cp "$tmp/$name/studyhomie" "$bin_dir/.studyhomie.new"
chmod 755 "$bin_dir/.studyhomie.new"
mv -f "$bin_dir/.studyhomie.new" "$bin_dir/studyhomie"

echo
echo "StudyHomie $v is installed in $bin_dir/studyhomie"
case ":$PATH:" in
  *":$bin_dir:"*) ;;
  *)
    echo
    echo "$bin_dir is not on your PATH. Add this line to ~/.bashrc or ~/.zshrc and open a new terminal:"
    echo "  export PATH=\"$bin_dir:\$PATH\""
    ;;
esac
echo
echo "Next, in a folder for your studies:"
echo "  mkdir estudios && cd estudios"
echo "  studyhomie init"
echo "  studyhomie"
