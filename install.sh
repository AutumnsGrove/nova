#!/bin/sh
# install.sh installs the nova binary from a GitHub release.
#
# It detects the OS and architecture, resolves a version, downloads the archive
# and checksums.txt over HTTPS, verifies the SHA-256, and only then installs the
# binary. It never asks for root and never executes the download except for the
# final `nova version` sanity check.
#
# Environment:
#   NOVA_VERSION       install this tag instead of the latest release
#   NOVA_FLAVOUR       "min" for the notui build (default: full)
#   NOVA_INSTALL_DIR   where to install (default: $HOME/.local/bin)
#   NOVA_RELEASE_BASE  download base (default: https://github.com/AutumnsGrove/nova)
#   NOVA_API_URL       release API URL used to resolve "latest"
set -eu

say() { printf '%s\n' "$*"; }
err() { printf 'install.sh: %s\n' "$*" >&2; exit 1; }

tmp=""
cleanup() {
	if [ -n "$tmp" ] && [ -d "$tmp" ]; then
		rm -rf "$tmp"
	fi
}
# A trap on a signal runs and then the script would carry on, so the signals
# exit; EXIT then runs cleanup once.
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

detect_os() {
	case "$(uname -s)" in
	Linux) echo linux ;;
	Darwin) echo darwin ;;
	*) err "unsupported operating system: $(uname -s)" ;;
	esac
}

detect_arch() {
	case "$(uname -m)" in
	x86_64 | amd64) echo amd64 ;;
	arm64 | aarch64) echo arm64 ;;
	*) err "unsupported architecture: $(uname -m)" ;;
	esac
}

# download fetches a URL to a file, failing on any HTTP error.
download() {
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL "$1" -o "$2"
	elif command -v wget >/dev/null 2>&1; then
		wget -qO "$2" "$1"
	else
		err "curl or wget is required"
	fi
}

# fetch prints a URL's body to stdout.
fetch() {
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL "$1"
	elif command -v wget >/dev/null 2>&1; then
		wget -qO- "$1"
	else
		err "curl or wget is required"
	fi
}

# sha256_file prints the hex SHA-256 of a file.
sha256_file() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | awk '{print $1}'
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | awk '{print $1}'
	else
		err "sha256sum or shasum is required"
	fi
}

# check_version refuses anything but a plain release tag: it becomes part of a
# URL and a file name, so a slash, a space, or a leading dash must not get in.
check_version() {
	case "$1" in
	"" | [!A-Za-z0-9]* | *[!A-Za-z0-9._+-]*) err "invalid version \"$1\": want a release tag such as v1.2.3" ;;
	esac
}

resolve_version() {
	if [ -n "${NOVA_VERSION:-}" ]; then
		printf '%s' "$NOVA_VERSION"
		return
	fi
	api="${NOVA_API_URL:-}"
	if [ -z "$api" ]; then
		if [ -n "${NOVA_RELEASE_BASE:-}" ]; then
			api="$NOVA_RELEASE_BASE/releases/latest"
		else
			api="https://api.github.com/repos/AutumnsGrove/nova/releases/latest"
		fi
	fi
	body="$(fetch "$api")" || err "could not reach the release API"
	version="$(printf '%s' "$body" | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)"
	[ -n "$version" ] || err "could not resolve the latest version"
	printf '%s' "$version"
}

main() {
	os="$(detect_os)"
	arch="$(detect_arch)"
	version="$(resolve_version)"
	check_version "$version"
	flavour="${NOVA_FLAVOUR:-full}"
	install_dir="${NOVA_INSTALL_DIR:-$HOME/.local/bin}"
	base="${NOVA_RELEASE_BASE:-https://github.com/AutumnsGrove/nova}"

	case "$flavour" in
	full) prefix="nova" ;;
	min) prefix="nova-min" ;;
	*) err "unknown NOVA_FLAVOUR: $flavour" ;;
	esac
	archive="${prefix}_${version}_${os}_${arch}.tar.gz"
	release_url="${base}/releases/download/${version}"

	tmp="$(mktemp -d "${TMPDIR:-/tmp}/nova-install.XXXXXX")"
	download "${release_url}/${archive}" "$tmp/$archive" || err "could not download $archive"
	download "${release_url}/checksums.txt" "$tmp/checksums.txt" || err "could not download checksums.txt"

	# A line is "<hash>  <name>", or "<hash> *<name>" when sha256sum was run with -b.
	expected="$(awk -v f="$archive" '{ n = $2; sub(/^\*/, "", n) } n == f { print tolower($1) }' "$tmp/checksums.txt")"
	[ -n "$expected" ] || err "checksums.txt has no entry for $archive"
	actual="$(sha256_file "$tmp/$archive")"
	[ "$expected" = "$actual" ] || err "checksum mismatch for $archive: expected $expected, got $actual"

	mkdir -p "$tmp/extract"
	tar -xzf "$tmp/$archive" -C "$tmp/extract" || err "could not extract $archive"
	[ -f "$tmp/extract/nova" ] && [ ! -L "$tmp/extract/nova" ] || err "$archive does not contain the nova binary"

	mkdir -p "$install_dir"
	staged="$install_dir/.nova.new"
	cp "$tmp/extract/nova" "$staged"
	chmod 0755 "$staged"
	if ! "$staged" version >/dev/null 2>&1; then
		rm -f "$staged"
		err "the new binary failed its sanity check"
	fi
	mv -f "$staged" "$install_dir/nova"

	say "installed nova $version ($flavour) to $install_dir/nova"
	case ":$PATH:" in
	*":$install_dir:"*) ;;
	*) say "add $install_dir to PATH to run nova" ;;
	esac
}

main "$@"
