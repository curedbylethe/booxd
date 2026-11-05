#!/bin/sh
set -eu

fail() {
	printf 'install test: %s\n' "$*" >&2
	exit 1
}

tmp=$(mktemp -d "${TMPDIR:-/tmp}/booxd-install-test.XXXXXX") || fail "could not create temporary directory"
trap 'rm -rf "$tmp"' 0
trap 'exit 1' HUP INT TERM
release=$tmp/release
home=$tmp/home
bin=$home/.local/bin
mkdir -p "$release" "$home"
mkdir "$tmp/fakebin"
cat >"$tmp/fakebin/uname" <<'EOF'
#!/bin/sh
case "$1" in
	-s) printf 'Linux\n' ;;
	-m) printf 'x86_64\n' ;;
esac
EOF
chmod +x "$tmp/fakebin/uname"

cat >"$release/booxd" <<'EOF'
#!/bin/sh
printf 'booxd v9.9.9 (linux/amd64)\n'
EOF
chmod 644 "$release/booxd"
tar -czf "$release/booxd_9.9.9_linux_amd64.tar.gz" -C "$release" booxd
if command -v sha256sum >/dev/null 2>&1; then
	digest=$(sha256sum "$release/booxd_9.9.9_linux_amd64.tar.gz" | awk '{print $1}')
else
	digest=$(shasum -a 256 "$release/booxd_9.9.9_linux_amd64.tar.gz" | awk '{print $1}')
fi
printf '%s  %s\n' "$digest" booxd_9.9.9_linux_amd64.tar.gz >"$release/SHA256SUMS"

run_install() {
	PATH=$tmp/fakebin:$PATH HOME=$home SHELL=/bin/bash BOOXD_BASE_URL=$release BOOXD_VERSION=9.9.9 ./install.sh
}

run_install
run_install
[ -x "$bin/booxd" ] || fail "installed binary is missing or not executable"
mode=$(stat -c '%a' "$bin/booxd" 2>/dev/null || stat -f '%Lp' "$bin/booxd")
[ "$mode" = 755 ] || fail "installed mode is $mode, want 755"
[ "$("$bin/booxd" version)" = 'booxd v9.9.9 (linux/amd64)' ] || fail "installed version did not run"
[ "$(grep -cF 'export PATH="'"$bin"':$PATH"' "$home/.bashrc")" -eq 1 ] || fail "PATH entry was not added exactly once"

cp -R "$release" "$tmp/corrupt"
printf x >>"$tmp/corrupt/booxd_9.9.9_linux_amd64.tar.gz"
if PATH=$tmp/fakebin:$PATH HOME=$home SHELL=/bin/bash BOOXD_BASE_URL=$tmp/corrupt BOOXD_VERSION=9.9.9 BOOXD_BIN_DIR=$tmp/corrupt-bin ./install.sh >/dev/null 2>&1; then
	fail "corrupt archive was accepted"
fi
[ ! -e "$tmp/corrupt-bin/booxd" ] || fail "corrupt archive installed a binary"

cat >"$tmp/fakebin/uname" <<'EOF'
#!/bin/sh
printf 'Plan9\n'
EOF
chmod +x "$tmp/fakebin/uname"
if PATH=$tmp/fakebin:$PATH HOME=$home PREFIX= SHELL=/bin/bash ./install.sh >"$tmp/unsupported.out" 2>&1; then
	fail "unsupported platform was accepted"
fi
grep -q 'darwin/arm64, darwin/amd64, linux/arm64, linux/amd64, android/arm64' "$tmp/unsupported.out" || fail "unsupported platform message omitted supported targets"

printf 'install tests passed\n'
