#!/bin/sh
# Herdr's build step for herdr-context. It fetches this version's prebuilt
# binary from the GitHub release, checks it against the release's
# checksums.txt, and puts it at bin/herdr-context.exe. If that fails and Go is
# installed, it builds from source instead. Herdr runs it from the plugin
# root, with no shell of its own; a non-zero exit aborts the install.
#
# HERDR_CONTEXT_BASE overrides the download base (tests point it at file://).
set -eu

out=bin/herdr-context.exe
base=${HERDR_CONTEXT_BASE:-https://github.com/Somliga/herdr-context/releases/download}
version=$(sed -n 's/^version *= *"\(.*\)"/\1/p' herdr-plugin.toml | head -n 1)

case $(uname -s) in
Linux) os=linux ;;
Darwin) os=darwin ;;
*) os= ;;
esac
case $(uname -m) in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*) arch= ;;
esac
asset=herdr-context-$os-$arch

fetch() {
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL -o "$2" "$1"
	elif command -v wget >/dev/null 2>&1; then
		wget -q -O "$2" "$1"
	else
		return 1
	fi
}

sha256() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d ' ' -f 1
	else
		shasum -a 256 "$1" | cut -d ' ' -f 1
	fi
}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

if [ -n "$os" ] && [ -n "$arch" ] && [ -n "$version" ] &&
	fetch "$base/v$version/$asset" "$tmp/bin" 2>/dev/null &&
	fetch "$base/v$version/checksums.txt" "$tmp/sums" 2>/dev/null; then
	want=$(awk -v a="$asset" '$2 == a { print $1 }' "$tmp/sums")
	if [ -n "$want" ] && [ "$(sha256 "$tmp/bin")" = "$want" ]; then
		mkdir -p bin
		chmod 755 "$tmp/bin"
		mv "$tmp/bin" "$out"
		echo "herdr-context: installed the prebuilt $asset binary for v$version"
		exit 0
	fi
	echo "herdr-context: $asset does not match the release's checksum; not using it" >&2
fi

if command -v go >/dev/null 2>&1; then
	echo "herdr-context: no usable prebuilt binary; building from source with $(go version)"
	exec go build -o "$out" ./cmd/herdr-context
fi

echo "herdr-context: no prebuilt binary for ${os:-this OS}/${arch:-this CPU} at v$version could be downloaded, and Go is not installed." >&2
echo "Install curl (or wget) and retry, or install Go 1.27+ to build from source." >&2
exit 1
