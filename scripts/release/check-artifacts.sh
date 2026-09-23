#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
cd "$(dirname "$0")/../.." || exit 1
fail() { printf '%s\n' "$*" >&2; exit 1; }
set -- dist/witmoot_*_checksums.txt
[ "$#" = 1 ] && [ -f "$1" ] || fail 'Expected one checksum manifest.'
manifest=$(basename "$1")
(cd dist && sha256sum -c "$manifest") || exit 1
[ "$(wc -l < "$1" | tr -d ' ')" = 5 ] || fail 'Expected two archives, two packages, and one source archive.'
work=$(mktemp -d) || exit 1
trap 'rm -rf "$work"' EXIT
trap 'exit 130' HUP INT TERM
for arch in amd64 arm64; do
	set -- dist/witmoot_*_linux_"$arch".tar.gz
	[ "$#" = 1 ] && [ -f "$1" ] || fail "Missing $arch binary archive."
	mkdir "$work/$arch" || exit 1
	tar -xzf "$1" -C "$work/$arch" --strip-components=1 || exit 1
	test -x "$work/$arch/witmoot" || fail 'Archive binary is not executable.'
	test -s "$work/$arch/LICENSE" || fail 'Archive license is missing.'
	test -s "$work/$arch/THIRD_PARTY_NOTICES.txt" || fail 'Dependency notices are missing.'
	test -s "$work/$arch/contrib/openrc/witmoot" || fail 'OpenRC example is missing.'
	cmp "contrib/logrotate/witmoot" "$work/$arch/contrib/logrotate/witmoot" || fail 'Archive logrotate rule is missing or differs.'
	for setting in StandardOutput=journal StandardError=journal SyslogIdentifier=witmoot; do
		grep -qx "$setting" "$work/$arch/contrib/systemd/witmoot.service" || fail "Archive logging setting is missing: $setting"
	done
	test -s "$work/$arch/docs/releases.md" || fail 'Archive installation guide is missing.'
	test -s "$work/$arch/docs/reverse-proxies.md" || fail 'Archive proxy guide is missing.'
	for proxy in caddy/Caddyfile nginx/witmoot.conf apache/witmoot.conf; do
		test -s "$work/$arch/contrib/$proxy" || fail "Archive proxy example is missing: $proxy"
	done
	readelf -l "$work/$arch/witmoot" > "$work/elf" || exit 1
	if grep -q INTERP "$work/elf"; then fail 'Release binary needs a dynamic loader.'; fi
	set -- dist/witmoot_*_"$arch".deb
	[ "$#" = 1 ] && [ -f "$1" ] || fail "Missing $arch Debian package."
	[ "$(dpkg-deb -f "$1" Architecture)" = "$arch" ] || fail 'Incorrect Debian architecture.'
	[ "$(dpkg-deb -f "$1" Package)" = witmoot ] || fail 'Incorrect Debian package name.'
	dpkg-deb -e "$1" "$work/control-$arch" || exit 1
	grep -qx '/etc/witmoot/witmoot.env' "$work/control-$arch/conffiles" || fail 'Settings are not registered as a conffile.'
	for script in postinst prerm postrm; do
		test -x "$work/control-$arch/$script" || fail "Missing executable $script."
		sh -n "$work/control-$arch/$script" || exit 1
	done
	dpkg-deb -x "$1" "$work/deb-$arch" || exit 1
	for setting in StandardOutput=journal StandardError=journal SyslogIdentifier=witmoot; do
		grep -qx "$setting" "$work/deb-$arch/usr/lib/systemd/system/witmoot.service" || fail "Debian logging setting is missing: $setting"
	done
	for proxy in caddy/Caddyfile nginx/witmoot.conf apache/witmoot.conf; do
		cmp "$work/$arch/contrib/$proxy" "$work/deb-$arch/usr/share/doc/witmoot/contrib/$proxy" || fail "Debian proxy example is missing or differs: $proxy"
	done
	cmp "$work/$arch/witmoot" "$work/deb-$arch/usr/bin/witmoot" || fail 'Archive and Debian binaries differ.'
done
set -- dist/witmoot_*_source.tar.gz
[ "$#" = 1 ] && [ -f "$1" ] || fail 'Missing source archive.'
mkdir "$work/source" || exit 1
tar -xzf "$1" -C "$work/source" --strip-components=1 || exit 1
cmp go.mod "$work/source/go.mod" || fail 'Source archive does not match the build.'
cmp .goreleaser.yaml "$work/source/.goreleaser.yaml" || fail 'Source archive is missing current release configuration.'
test -f "$work/source/cmd/witmoot/main.go" || fail 'Source archive is incomplete.'
printf '%s release artifact checks passed.\n' witmoot
