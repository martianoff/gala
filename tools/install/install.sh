#!/bin/sh
#
# Installs the GALA compiler from a GitHub release.
#
#   curl -fsSL https://raw.githubusercontent.com/martianoff/gala/master/tools/install/install.sh | sh
#   curl -fsSL .../install.sh | sh -s -- --version 0.101.0 --dir "$HOME/bin"
#
# Options (each also settable through the environment variable after it):
#   --version VERSION   release to install, default: the latest   GALA_VERSION
#   --dir DIR           install directory, default: ~/.local/bin  GALA_INSTALL_DIR
#
# Other environment variables:
#   GITHUB_TOKEN        sent to the GitHub API when resolving the latest release,
#                       to avoid its unauthenticated rate limit
#   GALA_DOWNLOAD_BASE  base URL of the release downloads, for a mirror;
#                       assets are fetched from $GALA_DOWNLOAD_BASE/VERSION/ASSET
#
# The script downloads the binary for this OS and CPU and the release's
# checksums-sha256.txt, refuses to install a binary whose SHA-256 does not
# match, installs it as `gala`, and runs `gala version`. It never edits shell
# startup files and sends nothing anywhere except the downloads themselves.
# GALA compiles through Go, so it then checks for Go 1.25+ and says where to
# get it; it does not install Go.

set -eu

repo="martianoff/gala"
go_min_major=1
go_min_minor=25

version="${GALA_VERSION:-}"
install_dir="${GALA_INSTALL_DIR:-$HOME/.local/bin}"
download_base="${GALA_DOWNLOAD_BASE:-https://github.com/$repo/releases/download}"

say() { printf '%s\n' "$*"; }
die() {
  printf 'gala install: error: %s\n' "$*" >&2
  exit 1
}

usage() {
  say "usage: install.sh [--version VERSION] [--dir DIR]"
  say "  --version  release to install (default: latest; env GALA_VERSION)"
  say "  --dir      install directory (default: ~/.local/bin; env GALA_INSTALL_DIR)"
}

while [ $# -gt 0 ]; do
  case "$1" in
    --version)
      [ $# -ge 2 ] || die "--version needs a value, e.g. --version 0.101.0"
      version="$2"
      shift 2
      ;;
    --version=*) version="${1#*=}"; shift ;;
    --dir)
      [ $# -ge 2 ] || die "--dir needs a value, e.g. --dir \$HOME/bin"
      install_dir="$2"
      shift 2
      ;;
    --dir=*) install_dir="${1#*=}"; shift ;;
    -h | --help) usage; exit 0 ;;
    *) die "unknown option '$1' (supported: --version VERSION, --dir DIR)" ;;
  esac
done

# --- tools ------------------------------------------------------------------

if command -v curl >/dev/null 2>&1; then
  fetch() { curl -fsSL --retry 3 -o "$2" "$1"; }
  fetch_api() {
    if [ -n "${GITHUB_TOKEN:-}" ]; then
      curl -fsSL --retry 3 -H "Authorization: Bearer $GITHUB_TOKEN" "$1"
    else
      curl -fsSL --retry 3 "$1"
    fi
  }
  # The URL a redirect chain ends at, without downloading the page.
  final_url() { curl -fsSLI -o /dev/null -w '%{url_effective}' "$1"; }
elif command -v wget >/dev/null 2>&1; then
  fetch() { wget -q -O "$2" "$1"; }
  fetch_api() {
    if [ -n "${GITHUB_TOKEN:-}" ]; then
      wget -q -O - --header="Authorization: Bearer $GITHUB_TOKEN" "$1"
    else
      wget -q -O - "$1"
    fi
  }
  final_url() { wget -q -S --spider "$1" 2>&1 | sed -n 's/^ *[Ll]ocation: *//p' | tail -n 1 | tr -d '\r'; }
else
  die "neither curl nor wget is installed; install one of them and run this again"
fi

if command -v sha256sum >/dev/null 2>&1; then
  sha256() { sha256sum "$1" | cut -d ' ' -f 1; }
elif command -v shasum >/dev/null 2>&1; then
  sha256() { shasum -a 256 "$1" | cut -d ' ' -f 1; }
else
  die "neither sha256sum nor shasum is installed, so the download cannot be verified; install coreutils (or perl's shasum) and run this again"
fi

# --- platform ---------------------------------------------------------------

ext=""
case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  MINGW* | MSYS* | CYGWIN*) os=windows; ext=".exe" ;;
  *) die "unsupported OS '$(uname -s)'; GALA ships binaries for Linux, macOS and Windows (build from source: https://github.com/$repo#quick-start)" ;;
esac

case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) die "unsupported CPU '$(uname -m)'; GALA ships amd64 and arm64 binaries (build from source: https://github.com/$repo#quick-start)" ;;
esac

# A shell running under Rosetta reports x86_64 on Apple Silicon; the native
# binary is the better choice there.
if [ "$os" = darwin ] && [ "$arch" = amd64 ] \
  && [ "$(sysctl -n hw.optional.arm64 2>/dev/null || echo 0)" = 1 ]; then
  arch=arm64
fi

if [ "$os" = windows ] && [ "$arch" != amd64 ]; then
  arch=amd64 # the only Windows build; Windows on ARM runs it under emulation
fi

asset="gala-$os-$arch$ext"

# --- version ----------------------------------------------------------------

if [ -z "$version" ]; then
  api_url="https://api.github.com/repos/$repo/releases/latest"
  version="$(fetch_api "$api_url" 2>/dev/null \
    | sed -n 's/^[[:space:]]*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)" || version=""
  if [ -z "$version" ]; then
    # The API is rate-limited without a token; the release page redirect is not.
    version="$(final_url "https://github.com/$repo/releases/latest" 2>/dev/null \
      | sed -n 's|.*/releases/tag/\([^/?#]*\).*|\1|p')" || version=""
  fi
  [ -n "$version" ] || die "could not determine the latest GALA release from GitHub; check your network, set GITHUB_TOKEN if you are rate-limited, or pass --version (releases: https://github.com/$repo/releases)"
fi

# Release tags are bare versions (0.101.0); accept a v-prefixed one too.
version="${version#v}"
case "$version" in
  "" | *[!0-9A-Za-z.+-]*) die "'$version' is not a release version (expected something like 0.101.0)" ;;
esac

# --- download and verify ----------------------------------------------------

tmp="$(mktemp -d 2>/dev/null || mktemp -d -t gala-install)"
trap 'rm -rf "$tmp"' EXIT
trap 'exit 130' HUP INT TERM

say "Installing GALA $version ($asset)"

fetch "$download_base/$version/checksums-sha256.txt" "$tmp/checksums-sha256.txt" \
  || die "could not download checksums-sha256.txt for GALA $version from $download_base/$version/; check that the release exists: https://github.com/$repo/releases"
fetch "$download_base/$version/$asset" "$tmp/$asset" \
  || die "could not download $asset for GALA $version from $download_base/$version/; check that the release exists and has this asset: https://github.com/$repo/releases/tag/$version"

expected="$(awk -v name="$asset" '$2 == name || $2 == "*" name { print $1; exit }' "$tmp/checksums-sha256.txt")"
[ -n "$expected" ] || die "checksums-sha256.txt of GALA $version has no entry for $asset; refusing to install an unverified binary"
actual="$(sha256 "$tmp/$asset")"
if [ "$actual" != "$expected" ]; then
  die "SHA-256 mismatch for $asset: expected $expected, got $actual. The download is corrupt or was tampered with; nothing was installed. Try again, and report it at https://github.com/$repo/issues if it persists"
fi
say "Verified SHA-256 $actual"

# --- install ----------------------------------------------------------------

# --dir=~/bin reaches us with the tilde unexpanded.
case "$install_dir" in
  \~ | \~/*) install_dir="$HOME${install_dir#"~"}" ;;
esac
mkdir -p "$install_dir" || die "cannot create $install_dir; pick a writable directory with --dir"
# An absolute path without a trailing slash, as PATH entries are written.
install_dir="$(CDPATH='' cd -- "$install_dir" && pwd)" || die "cannot enter $install_dir"
target="$install_dir/gala$ext"
chmod +x "$tmp/$asset"
# Copy next to the target first so the final step is a rename within one
# filesystem: a running `gala` keeps its old file, and no half-written binary
# is ever on PATH.
cp "$tmp/$asset" "$target.new" || die "cannot write to $install_dir; pick a writable directory with --dir"
mv -f "$target.new" "$target" || die "cannot replace $target; is it in use?"

say "Installed $target"
"$target" version || die "$target was installed but 'gala version' failed; please report it at https://github.com/$repo/issues"

# --- PATH -------------------------------------------------------------------

case ":$PATH:" in
  *":$install_dir:"*) ;;
  *)
    say ""
    say "$install_dir is not on your PATH. Add it by putting this line in your shell's startup file"
    case "${SHELL:-}" in
      */zsh) say "(~/.zshrc):" ;;
      */bash) say "(~/.bashrc, or ~/.bash_profile on macOS):" ;;
      */fish) say "(for fish, run instead: fish_add_path \"$install_dir\"):" ;;
      *) say "(~/.profile):" ;;
    esac
    say ""
    say "  export PATH=\"$install_dir:\$PATH\""
    say ""
    say "then open a new terminal."
    ;;
esac

# --- Go ---------------------------------------------------------------------

say ""
go_hint="GALA compiles programs through Go, so it needs Go $go_min_major.$go_min_minor+ on your PATH: https://go.dev/dl/"
if ! command -v go >/dev/null 2>&1; then
  say "Go was not found. $go_hint"
else
  go_version="$(go version 2>/dev/null | sed -n 's/.*go\([0-9][0-9]*\)\.\([0-9][0-9]*\).*/\1 \2/p')"
  go_major="${go_version% *}"
  go_minor="${go_version#* }"
  if [ -z "$go_version" ]; then
    say "Could not read the version of the installed Go ('go version' failed). $go_hint"
  elif [ "$go_major" -lt "$go_min_major" ] \
    || { [ "$go_major" -eq "$go_min_major" ] && [ "$go_minor" -lt "$go_min_minor" ]; }; then
    say "Found Go $go_major.$go_minor, which is too old. $go_hint"
  else
    say "Found Go $go_major.$go_minor"
  fi
fi

say ""
say "Done. Get started: gala new hello && cd hello && gala run main.gala"
