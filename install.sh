#!/bin/sh
set -eu

fail() {
	printf 'booxd installer: %s\n' "$*" >&2
	exit 1
}

case "${PREFIX:-}" in
	*com.termux*)
		goos=android
		goarch=arm64
		bin=${BOOXD_BIN_DIR:-$PREFIX/bin}
		termux=1
		;;
	*)
		termux=0
		case "$(uname -s)" in
			Darwin) goos=darwin ;;
			Linux) goos=linux ;;
			*) fail "unsupported platform; supports darwin/arm64, darwin/amd64, linux/arm64, linux/amd64, android/arm64" ;;
		esac
		case "$(uname -m)" in
			x86_64|amd64) goarch=amd64 ;;
			arm64|aarch64) goarch=arm64 ;;
			*) fail "unsupported platform; supports darwin/arm64, darwin/amd64, linux/arm64, linux/amd64, android/arm64" ;;
		esac
		bin=${BOOXD_BIN_DIR:-$HOME/.local/bin}
		;;
esac

if [ -n "${BOOXD_VERSION:-}" ]; then
	tag=$BOOXD_VERSION
else
	api=https://api.github.com/repos/curedbylethe/booxd/releases/latest
	if command -v curl >/dev/null 2>&1; then
		response=$(curl -fsSL "$api") || fail "could not read latest release from GitHub"
	elif command -v wget >/dev/null 2>&1; then
		response=$(wget -qO- "$api") || fail "could not read latest release from GitHub"
	else
		fail "install curl or wget to resolve the latest release"
	fi
	tag=$(printf '%s\n' "$response" | grep -m1 '"tag_name"' | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')
	[ -n "$tag" ] || fail "GitHub returned no release tag"
fi

case "$tag" in
	v*) version=${tag#v} ;;
	*) version=$tag; tag=v$version ;;
esac
[ -n "$version" ] || fail "release version is empty"

asset="booxd_${version}_${goos}_${goarch}.tar.gz"
base=${BOOXD_BASE_URL:-"https://github.com/curedbylethe/booxd/releases/download/$tag"}
tmp=$(mktemp -d "${TMPDIR:-/tmp}/booxd.XXXXXX") || fail "could not create a temporary directory"
trap 'rm -rf "$tmp"' 0
trap 'exit 1' HUP INT TERM

case "$base" in
	*://*)
		if command -v curl >/dev/null 2>&1; then
			curl -fLsS "$base/$asset" -o "$tmp/$asset" || fail "could not download $asset"
			curl -fLsS "$base/SHA256SUMS" -o "$tmp/SHA256SUMS" || fail "could not download SHA256SUMS"
		elif command -v wget >/dev/null 2>&1; then
			wget -qO "$tmp/$asset" "$base/$asset" || fail "could not download $asset"
			wget -qO "$tmp/SHA256SUMS" "$base/SHA256SUMS" || fail "could not download SHA256SUMS"
		else
			fail "install curl or wget to download release assets"
		fi
		;;
	*)
		cp "$base/$asset" "$tmp/$asset" || fail "could not read $asset from $base"
		cp "$base/SHA256SUMS" "$tmp/SHA256SUMS" || fail "could not read SHA256SUMS from $base"
		;;
esac

expected=$(awk -v name="$asset" '$2 == name { print $1 }' "$tmp/SHA256SUMS")
[ -n "$expected" ] || fail "SHA256SUMS has no entry for $asset"
if command -v sha256sum >/dev/null 2>&1; then
	actual=$(sha256sum "$tmp/$asset" | awk '{print $1}')
elif command -v shasum >/dev/null 2>&1; then
	actual=$(shasum -a 256 "$tmp/$asset" | awk '{print $1}')
else
	fail "install sha256sum or shasum to verify the release"
fi
[ "$actual" = "$expected" ] || fail "checksum mismatch for $asset"

tar -xzf "$tmp/$asset" -C "$tmp" booxd || fail "could not extract booxd from $asset"
mkdir -p "$bin" || fail "could not create install directory $bin"
install -m 755 "$tmp/booxd" "$bin/booxd" || fail "could not install into $bin"
got=$("$bin/booxd" version 2>/dev/null) || fail "installed booxd did not run"
want="booxd v$version ($goos/$goarch)"
[ "$got" = "$want" ] || fail "installed version says '$got'; expected '$want'"

printf 'installed booxd %s (%s/%s) in %s\n' "$version" "$goos" "$goarch" "$bin"
if [ "$termux" -eq 1 ]; then
	printf 'booxd is ready; no PATH edit needed. termux-open comes from termux-tools.\n'
elif [ -n "${BOOXD_BIN_DIR:-}" ]; then
	printf 'BOOXD_BIN_DIR set; update PATH yourself if needed.\n'
else
	case ":${PATH:-}:" in
		*":$bin:"*) ;;
		*)
			shell=$(basename "${SHELL:-sh}")
			case "$shell" in
				fish)
				rcdir=${XDG_CONFIG_HOME:-$HOME/.config}/fish/conf.d
				mkdir -p "$rcdir"
				rc=$rcdir/booxd.fish
				line="fish_add_path \"$bin\""
				;;
				zsh) rc=${ZDOTDIR:-$HOME}/.zshrc; line="export PATH=\"$bin:\$PATH\"" ;;
				bash)
					if [ -f "$HOME/.bash_profile" ]; then rc=$HOME/.bash_profile; else rc=$HOME/.bashrc; fi
					line="export PATH=\"$bin:\$PATH\""
					;;
				*) rc=$HOME/.profile; line="export PATH=\"$bin:\$PATH\"" ;;
			esac
			if ! grep -qxF "$line" "$rc" 2>/dev/null; then
				printf '%s\n' "$line" >>"$rc"
			fi
			printf 'added PATH entry in %s; run exec "%s" -l or open a new terminal\n' "$rc" "${SHELL:-sh}"
			;;
	esac
fi
